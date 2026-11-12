// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// BotIssuer is the server-side principal namespace of tenant bots. It cannot collide with an OIDC
// subject of a customer because customer bindings use the Gateway issuer.
const BotIssuer = "phoenix:bot"

// NotifyRequest is the body of POST /phoenix/internal/v1/notifications (scope chat:notify).
type NotifyRequest struct {
	OperationKey string `json:"operationKey"` // the upstream event key, kept stable across retries
	TenantID     string `json:"tenantId"`
	ChannelID    string `json:"channelId"`
	RootPostID   string `json:"rootPostId,omitempty"`
	Message      string `json:"message"`
	Caller       string `json:"-"`
}

// NotifyResult is the sanitized outcome of a notification.
type NotifyResult struct {
	OperationKey string `json:"operationKey"`
	State        string `json:"state"`
	PostID       string `json:"postId"`
	RootPostID   string `json:"rootPostId"`
	ChannelID    string `json:"channelId"`
}

// NotifyOps are the extra native operations a notification needs.
type NotifyOps interface {
	CreateTenantBot(ctx context.Context, username, displayName string) (*UserInfo, error)
	EnsureChannelMember(ctx context.Context, channelID, userID string) error
	CreatePost(ctx context.Context, userID, channelID, rootID, message, eventKey string) (string, error)
}

// BotUsername is derived from the immutable tenant key.
func BotUsername(tenantID string) string { return "b" + shortHash("bot", tenantID)[:24] }

// Notify posts a task notice as the tenant bot. The journal entry (caller, "notify:"+key) makes it
// idempotent: a retry after a timeout returns the stored post, and the event key is also written into
// the post itself so a crash between the native post and the journal write is recovered by lookup
// instead of a second post.
func (s *Service) Notify(ctx context.Context, ops NotifyOps, req NotifyRequest) (NotifyResult, error) {
	bad := func(msg string) error { return newErr(http.StatusBadRequest, CodeInvalidRequest, msg) }
	if req.Caller == "" || req.OperationKey == "" || len(req.OperationKey) > 200 || req.TenantID == "" || len(req.ChannelID) != 26 || strings.TrimSpace(req.Message) == "" || len(req.Message) > 16000 {
		return NotifyResult{}, bad("operationKey, tenantId, channelId (26) and message (<=16000) are required")
	}
	if req.RootPostID != "" && len(req.RootPostID) != 26 {
		return NotifyResult{}, bad("rootPostId must be a native id")
	}
	key := "notify:" + shortHash(req.TenantID, req.OperationKey)
	rec, _, err := s.store.BeginOperation(ctx, OperationRecord{
		Caller: req.Caller, Key: key, RequestHash: shortHash(req.TenantID, req.ChannelID, req.RootPostID, req.Message),
		Operation: "NOTIFY_TASK", TenantID: req.TenantID,
	})
	if err != nil {
		return NotifyResult{}, err
	}
	if rec.State == OpStateSucceeded && rec.Result != "" {
		var out NotifyResult
		if json.Unmarshal([]byte(rec.Result), &out) == nil {
			return out, nil
		}
	}
	unlock := s.locks.Lock("tenant:" + req.TenantID)
	defer unlock()

	tb, terr := s.tenantOrErr(ctx, req.TenantID)
	if terr != nil {
		_ = s.store.FinishOperation(ctx, req.Caller, key, OpStateFailed, "")
		return NotifyResult{}, terr
	}
	if tb.State != TenantActive {
		_ = s.store.FinishOperation(ctx, req.Caller, key, OpStateFailed, "")
		return NotifyResult{}, newErr(http.StatusConflict, CodeTenantSuspended, "tenant is suspended")
	}
	owned, err := s.store.ChannelOwnedBy(ctx, req.ChannelID, req.TenantID)
	if err != nil || !owned {
		_ = s.store.FinishOperation(ctx, req.Caller, key, OpStateFailed, "")
		if err != nil {
			return NotifyResult{}, err
		}
		return NotifyResult{}, ErrNotFound // foreign and unknown channels look the same
	}
	result, err := s.notifyOnce(ctx, ops, tb, req)
	if err != nil {
		state := OpStateUnknown
		var pe *Error
		if errors.As(err, &pe) {
			state = OpStateFailed
		}
		_ = s.store.FinishOperation(ctx, req.Caller, key, state, "")
		return NotifyResult{}, err
	}
	result.OperationKey, result.State = req.OperationKey, string(OpStateSucceeded)
	blob, _ := json.Marshal(result)
	if ferr := s.store.FinishOperation(ctx, req.Caller, key, OpStateSucceeded, string(blob)); ferr != nil {
		return NotifyResult{}, ferr
	}
	return result, nil
}

func (s *Service) notifyOnce(ctx context.Context, ops NotifyOps, tb TenantBinding, req NotifyRequest) (NotifyResult, error) {
	botSub := "bot:" + req.TenantID
	ub, err := s.store.GetUser(ctx, BotIssuer, botSub)
	if errors.Is(err, sql.ErrNoRows) {
		username := BotUsername(req.TenantID)
		user, ferr := s.native.FindUserByUsername(ctx, username)
		if ferr != nil {
			return NotifyResult{}, ferr
		}
		if user == nil {
			if user, ferr = ops.CreateTenantBot(ctx, username, "Phoenix Tasks"); ferr != nil {
				return NotifyResult{}, ferr
			}
		}
		if ub, err = s.store.AttachUser(ctx, BotIssuer, botSub, req.TenantID, user.ID, ActorBot); err != nil {
			return NotifyResult{}, err
		}
	} else if err != nil {
		return NotifyResult{}, err
	}
	if ub.AccessState == AccessRevoked {
		return NotifyResult{}, ErrAccessDenied
	}
	if ub.AccessState == AccessPending {
		if ub, _, err = s.store.SetAccess(ctx, BotIssuer, botSub, AccessGranted, maxInt64(ub.AccessVersion, 1)); err != nil {
			return NotifyResult{}, err
		}
	}
	if err := s.native.AddTeamMember(ctx, tb.TeamID, ub.NativeUserID); err != nil {
		return NotifyResult{}, err
	}
	if err := ops.EnsureChannelMember(ctx, req.ChannelID, ub.NativeUserID); err != nil {
		return NotifyResult{}, err
	}
	// Crash recovery: an earlier attempt may already have posted this event.
	if postID, rootID, found, ferr := s.store.PostByEventKey(ctx, req.ChannelID, req.OperationKey); ferr != nil {
		return NotifyResult{}, ferr
	} else if found {
		return NotifyResult{PostID: postID, RootPostID: firstNonEmpty(rootID, postID), ChannelID: req.ChannelID}, nil
	}
	root := req.RootPostID
	if root != "" {
		ch, found, rerr := s.store.PostChannel(ctx, root)
		if rerr != nil {
			return NotifyResult{}, rerr
		}
		if !found || ch != req.ChannelID {
			return NotifyResult{}, newErr(http.StatusConflict, CodeOperationConflict, "root post is not in the notification channel")
		}
	}
	postID, err := ops.CreatePost(ctx, ub.NativeUserID, req.ChannelID, root, req.Message, req.OperationKey)
	if err != nil {
		return NotifyResult{}, err
	}
	return NotifyResult{PostID: postID, RootPostID: firstNonEmpty(root, postID), ChannelID: req.ChannelID}, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ChannelOwnedBy reports whether a channel belongs to the tenant (binding or bound team).
func (s *Store) ChannelOwnedBy(ctx context.Context, channelID, tenantID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, channelOwnedSQL, channelID, tenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// PostChannel returns the channel of a post.
func (s *Store) PostChannel(ctx context.Context, postID string) (string, bool, error) {
	var ch string
	err := s.db.QueryRowContext(ctx, `SELECT channelid FROM posts WHERE id=$1 AND deleteat=0`, postID).Scan(&ch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return ch, err == nil, err
}

// PostByEventKey finds a post this process already created for an event key.
func (s *Store) PostByEventKey(ctx context.Context, channelID, eventKey string) (postID, rootID string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id, rootid FROM posts WHERE channelid=$1 AND deleteat=0 AND props::jsonb ->> 'phoenix_event_key' = $2 ORDER BY createat LIMIT 1`, channelID, eventKey).Scan(&postID, &rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return postID, rootID, err == nil, err
}
