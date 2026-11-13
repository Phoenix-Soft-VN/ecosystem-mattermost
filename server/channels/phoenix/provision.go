// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// TeamInfo and UserInfo are the native read-back shapes returned by NativeOps.
type TeamInfo struct {
	ID            string
	Name          string
	Type          string
	AllowOpenInvt bool
	DeleteAt      int64
}

type UserInfo struct {
	ID       string
	Username string
	Email    string
	DeleteAt int64
}

// NativeOps is the narrow set of native resource operations the provisioning service needs.
// The app layer implements it; keeping it an interface avoids a channels/app import cycle and
// lets the journal/binding logic be exercised against real native resources and a real database.
type NativeOps interface {
	FindTeamByName(ctx context.Context, name string) (*TeamInfo, error)
	CreatePrivateTeam(ctx context.Context, name, displayName string) (*TeamInfo, error)
	EnsureTeamPrivate(ctx context.Context, teamID string) (*TeamInfo, error)
	ReadTeam(ctx context.Context, teamID string) (*TeamInfo, error)
	DefaultChannelID(ctx context.Context, teamID string) (string, error)

	FindUserByUsername(ctx context.Context, username string) (*UserInfo, error)
	FindUserByEmail(ctx context.Context, email string) (*UserInfo, error)
	CreateAccountUser(ctx context.Context, username, email string, p Profile) (*UserInfo, error)
	UpdateAccountProfile(ctx context.Context, userID, email string, p Profile) error

	AddTeamMember(ctx context.Context, teamID, userID string) error
	RemoveTeamMember(ctx context.Context, teamID, userID string) error
	IsTeamMember(ctx context.Context, teamID, userID string) (bool, error)
	RevokeUserSessions(ctx context.Context, userID string) error
}

// Service implements the C2 operations on top of Store and NativeOps.
type Service struct {
	store    *Store
	native   NativeOps
	locks    keyedMutex
	revision string
	build    string
	// OnChange is invoked after an operation changes who may act (tenant state, access
	// state, session revocation) so cached actors are dropped immediately.
	OnChange func(tenantID, nativeUserID string)
}

func (s *Service) changed(tenantID, nativeUserID string) {
	if s.OnChange != nil {
		s.OnChange(tenantID, nativeUserID)
	}
}

func NewService(store *Store, native NativeOps) *Service {
	return &Service{store: store, native: native, revision: model.BuildHash, build: model.BuildNumber}
}

// SetArtifact overrides the reported artifact identity (used by tests and packaging checks).
func (s *Service) SetArtifact(revision, build string) { s.revision, s.build = revision, build }

func shortHash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

// TeamName is derived from the immutable tenant key, never from a display name or slug.
func TeamName(tenantID string) string { return "p" + shortHash("tenant", tenantID)[:24] }

// Username is derived from the immutable (issuer, sub), never from email or display name.
func Username(issuer, sub string) string { return "u" + shortHash("principal", issuer, sub)[:24] }

func placeholderEmail(issuer, sub string) string {
	return Username(issuer, sub) + "@accounts.phoenix.invalid"
}

func requestHash(r OperationRequest) string {
	b, _ := json.Marshal(struct {
		Operation      Operation
		DesiredVersion int64
		TenantID       string
		Issuer         string
		Sub            string
		Profile        *Profile
		TaskEndpointID string
	}{r.Operation, r.DesiredVersion, r.TenantID, r.Issuer, r.Sub, r.Profile, r.TaskEndpointID})
	return shortHash(string(b))
}

func validateOperation(r OperationRequest) *Error {
	bad := func(msg string) *Error { return newErr(http.StatusBadRequest, CodeInvalidRequest, msg) }
	if r.Caller == "" {
		return bad("caller missing")
	}
	if r.OperationKey == "" || len(r.OperationKey) > 255 {
		return bad("operationKey required (max 255)")
	}
	if !validOperations[r.Operation] {
		return bad("unknown operation")
	}
	if r.TenantID == "" || len(r.TenantID) > 255 || strings.TrimSpace(r.TenantID) != r.TenantID {
		return bad("tenantId required (max 255)")
	}
	if r.DesiredVersion < 0 {
		return bad("desiredVersion must be >= 0")
	}
	switch r.Operation {
	case OpEnsureUserAccount, OpGrantAccess, OpRevokeAccess:
		if r.Issuer == "" || r.Sub == "" || len(r.Sub) > 255 {
			return bad("issuer and sub required")
		}
	}
	switch r.Operation {
	case OpGrantAccess, OpRevokeAccess, OpSuspendTenant, OpResumeTenant:
		if r.DesiredVersion < 1 {
			return bad("desiredVersion must be >= 1 for this operation")
		}
	}
	if r.Profile != nil {
		if len(r.Profile.Email) > 128 || len(r.Profile.DisplayName) > 64 || len(r.Profile.FirstName) > 64 || len(r.Profile.LastName) > 64 {
			return bad("profile field too long")
		}
		if r.Profile.Email != "" && !model.IsValidEmail(r.Profile.Email) {
			return bad("profile email invalid")
		}
	}
	return nil
}

// Provision runs one idempotent operation. Retrying the same key and payload re-executes
// an unfinished operation and replays the stored result of a finished one.
func (s *Service) Provision(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	if verr := validateOperation(req); verr != nil {
		return OperationEvidence{}, verr
	}
	rec, _, err := s.store.BeginOperation(ctx, OperationRecord{
		Caller: req.Caller, Key: req.OperationKey, RequestHash: requestHash(req),
		Operation: req.Operation, TenantID: req.TenantID, DesiredVersion: req.DesiredVersion,
	})
	if err != nil {
		return OperationEvidence{}, err
	}
	if rec.State == OpStateSucceeded && rec.Result != "" {
		var ev OperationEvidence
		if jerr := json.Unmarshal([]byte(rec.Result), &ev); jerr == nil {
			return ev, nil
		}
	}

	unlock := s.locks.Lock("tenant:" + req.TenantID)
	defer unlock()

	ev, execErr := s.execute(ctx, req)
	if execErr != nil {
		state := OpStateFailed
		var pe *Error
		if !errors.As(execErr, &pe) {
			state = OpStateUnknown
		}
		_ = s.store.FinishOperation(ctx, req.Caller, req.OperationKey, state, "")
		return OperationEvidence{}, execErr
	}
	ev.OperationKey, ev.DesiredVersion, ev.State = req.OperationKey, req.DesiredVersion, OpStateSucceeded
	blob, _ := json.Marshal(ev)
	if ferr := s.store.FinishOperation(ctx, req.Caller, req.OperationKey, OpStateSucceeded, string(blob)); ferr != nil {
		return OperationEvidence{}, ferr
	}
	return ev, nil
}

func (s *Service) execute(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	switch req.Operation {
	case OpEnsureTenantBoundary:
		return s.ensureTenant(ctx, req)
	case OpEnsureUserAccount:
		return s.ensureUser(ctx, req)
	case OpGrantAccess:
		return s.grant(ctx, req)
	case OpRevokeAccess:
		return s.revoke(ctx, req)
	case OpSuspendTenant:
		return s.setTenantState(ctx, req, TenantSuspended)
	case OpResumeTenant:
		return s.setTenantState(ctx, req, TenantActive)
	}
	return OperationEvidence{}, newErr(http.StatusBadRequest, CodeInvalidRequest, "unknown operation")
}

func (s *Service) base() OperationEvidence {
	return OperationEvidence{PolicyVersion: CurrentPolicyVersion, ArtifactRevision: s.revision, ArtifactBuild: s.build}
}

func (s *Service) tenantOrErr(ctx context.Context, tenantID string) (TenantBinding, error) {
	tb, err := s.store.GetTenant(ctx, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return tb, newErr(http.StatusNotFound, CodeTenantNotFound, "tenant has no boundary")
	}
	return tb, err
}

func (s *Service) ensureTenant(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	tb, err := s.store.GetTenant(ctx, req.TenantID)
	switch {
	case err == nil:
		team, rerr := s.native.EnsureTeamPrivate(ctx, tb.TeamID)
		if rerr != nil {
			return OperationEvidence{}, rerr
		}
		if team == nil || team.DeleteAt != 0 {
			return OperationEvidence{}, newErr(http.StatusConflict, CodeOperationConflict, "bound team is archived or missing")
		}
		if tb, err = s.store.AttachTenant(ctx, req.TenantID, tb.TeamID, tb.DefaultChannelID, req.DesiredVersion, req.TaskEndpointID); err != nil {
			return OperationEvidence{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		name := TeamName(req.TenantID)
		team, ferr := s.native.FindTeamByName(ctx, name)
		if ferr != nil {
			return OperationEvidence{}, ferr
		}
		if team == nil {
			if team, ferr = s.native.CreatePrivateTeam(ctx, name, "Phoenix "+name[1:9]); ferr != nil {
				return OperationEvidence{}, ferr
			}
		}
		if team, ferr = s.native.EnsureTeamPrivate(ctx, team.ID); ferr != nil {
			return OperationEvidence{}, ferr
		}
		ch, cerr := s.native.DefaultChannelID(ctx, team.ID)
		if cerr != nil {
			return OperationEvidence{}, cerr
		}
		if tb, err = s.store.AttachTenant(ctx, req.TenantID, team.ID, ch, req.DesiredVersion, req.TaskEndpointID); err != nil {
			return OperationEvidence{}, err
		}
	default:
		return OperationEvidence{}, err
	}
	return s.evidence(ctx, tb, nil)
}

func (s *Service) ensureUser(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	tb, err := s.tenantOrErr(ctx, req.TenantID)
	if err != nil {
		return OperationEvidence{}, err
	}
	profile := Profile{}
	if req.Profile != nil {
		profile = *req.Profile
	}
	ub, err := s.store.GetUser(ctx, req.Issuer, req.Sub)
	switch {
	case err == nil:
		if ub.TenantID != req.TenantID {
			return OperationEvidence{}, newErr(http.StatusConflict, CodeSingleTenant, "principal is already bound to another chat tenant")
		}
		if perr := s.syncProfile(ctx, ub.NativeUserID, req.Issuer, req.Sub, profile); perr != nil {
			return OperationEvidence{}, perr
		}
	case errors.Is(err, sql.ErrNoRows):
		email := profile.Email
		if email == "" {
			email = placeholderEmail(req.Issuer, req.Sub)
		}
		if other, ferr := s.native.FindUserByEmail(ctx, strings.ToLower(email)); ferr != nil {
			return OperationEvidence{}, ferr
		} else if other != nil && other.Username != Username(req.Issuer, req.Sub) {
			return OperationEvidence{}, newErr(http.StatusConflict, CodeEmailConflict, "profile email belongs to another native account")
		}
		username := Username(req.Issuer, req.Sub)
		user, ferr := s.native.FindUserByUsername(ctx, username)
		if ferr != nil {
			return OperationEvidence{}, ferr
		}
		if user == nil {
			if user, ferr = s.native.CreateAccountUser(ctx, username, email, profile); ferr != nil {
				return OperationEvidence{}, ferr
			}
		}
		if perr := s.syncProfile(ctx, user.ID, req.Issuer, req.Sub, profile); perr != nil {
			return OperationEvidence{}, perr
		}
		if ub, err = s.store.AttachUser(ctx, req.Issuer, req.Sub, req.TenantID, user.ID, ActorCustomer); err != nil {
			return OperationEvidence{}, err
		}
	default:
		return OperationEvidence{}, err
	}
	return s.evidence(ctx, tb, &ub)
}

func (s *Service) syncProfile(ctx context.Context, userID, issuer, sub string, p Profile) error {
	if p.Email != "" {
		if other, err := s.native.FindUserByEmail(ctx, strings.ToLower(p.Email)); err != nil {
			return err
		} else if other != nil && other.ID != userID {
			return newErr(http.StatusConflict, CodeEmailConflict, "profile email belongs to another native account")
		}
	}
	return s.native.UpdateAccountProfile(ctx, userID, strings.ToLower(p.Email), p)
}

func (s *Service) grant(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	tb, err := s.tenantOrErr(ctx, req.TenantID)
	if err != nil {
		return OperationEvidence{}, err
	}
	ub, err := s.userInTenant(ctx, req)
	if err != nil {
		return OperationEvidence{}, err
	}
	if tb.State != TenantActive {
		return OperationEvidence{}, newErr(http.StatusConflict, CodeTenantSuspended, "tenant is suspended")
	}
	// The monotonic version check happens before any native effect, so a stale grant can
	// never re-add a membership that a newer revoke removed.
	if ub, _, err = s.store.SetAccess(ctx, req.Issuer, req.Sub, AccessGranted, req.DesiredVersion); err != nil {
		return OperationEvidence{}, err
	}
	s.changed(tb.TenantID, ub.NativeUserID)
	if err = s.native.AddTeamMember(ctx, tb.TeamID, ub.NativeUserID); err != nil {
		return OperationEvidence{}, err
	}
	return s.evidence(ctx, tb, &ub)
}

func (s *Service) revoke(ctx context.Context, req OperationRequest) (OperationEvidence, error) {
	tb, err := s.tenantOrErr(ctx, req.TenantID)
	if err != nil {
		return OperationEvidence{}, err
	}
	ub, err := s.userInTenant(ctx, req)
	if err != nil {
		return OperationEvidence{}, err
	}
	// Close the binding first, then revoke sessions and drop the membership.
	if ub, _, err = s.store.SetAccess(ctx, req.Issuer, req.Sub, AccessRevoked, req.DesiredVersion); err != nil {
		return OperationEvidence{}, err
	}
	s.changed(tb.TenantID, ub.NativeUserID)
	if err = s.native.RevokeUserSessions(ctx, ub.NativeUserID); err != nil {
		return OperationEvidence{}, err
	}
	if err = s.native.RemoveTeamMember(ctx, tb.TeamID, ub.NativeUserID); err != nil {
		return OperationEvidence{}, err
	}
	return s.evidence(ctx, tb, &ub)
}

func (s *Service) setTenantState(ctx context.Context, req OperationRequest, state TenantState) (OperationEvidence, error) {
	tb, err := s.store.SetTenantState(ctx, req.TenantID, state, req.DesiredVersion)
	if err != nil {
		return OperationEvidence{}, err
	}
	s.changed(req.TenantID, "")
	if state == TenantSuspended {
		ids, lerr := s.store.TenantUserIDs(ctx, req.TenantID)
		if lerr != nil {
			return OperationEvidence{}, lerr
		}
		for _, id := range ids {
			if rerr := s.native.RevokeUserSessions(ctx, id); rerr != nil {
				return OperationEvidence{}, rerr
			}
		}
	}
	// Resume deliberately changes no user binding: an individually revoked user stays revoked.
	return s.evidence(ctx, tb, nil)
}

func (s *Service) userInTenant(ctx context.Context, req OperationRequest) (UserBinding, error) {
	ub, err := s.store.GetUser(ctx, req.Issuer, req.Sub)
	if errors.Is(err, sql.ErrNoRows) {
		return ub, newErr(http.StatusNotFound, CodeUserNotFound, "no native account for principal")
	}
	if err != nil {
		return ub, err
	}
	if ub.TenantID != req.TenantID {
		return ub, newErr(http.StatusConflict, CodeSingleTenant, "principal is already bound to another chat tenant")
	}
	return ub, nil
}

// ReadEvidence returns a native read-back for a tenant and optionally one principal.
func (s *Service) ReadEvidence(ctx context.Context, req EvidenceRequest) (OperationEvidence, error) {
	if req.TenantID == "" || len(req.TenantID) > 255 {
		return OperationEvidence{}, newErr(http.StatusBadRequest, CodeInvalidRequest, "tenantId required")
	}
	if (req.Issuer == "") != (req.Sub == "") {
		return OperationEvidence{}, newErr(http.StatusBadRequest, CodeInvalidRequest, "issuer and sub must be supplied together")
	}
	tb, err := s.tenantOrErr(ctx, req.TenantID)
	if err != nil {
		return OperationEvidence{}, err
	}
	var ubp *UserBinding
	if req.Sub != "" {
		ub, uerr := s.store.GetUser(ctx, req.Issuer, req.Sub)
		if errors.Is(uerr, sql.ErrNoRows) {
			return OperationEvidence{}, newErr(http.StatusNotFound, CodeUserNotFound, "no native account for principal")
		}
		if uerr != nil {
			return OperationEvidence{}, uerr
		}
		if ub.TenantID != req.TenantID {
			return OperationEvidence{}, newErr(http.StatusConflict, CodeSingleTenant, "principal is bound to another chat tenant")
		}
		ubp = &ub
	}
	ev, err := s.evidence(ctx, tb, ubp)
	if err != nil {
		return ev, err
	}
	ev.State = OpStateSucceeded
	return ev, nil
}

// evidence builds the sanitized read-back directly from native resources and bindings.
func (s *Service) evidence(ctx context.Context, tb TenantBinding, ub *UserBinding) (OperationEvidence, error) {
	ev := s.base()
	ev.DesiredVersion = tb.DesiredVersion
	team, err := s.native.ReadTeam(ctx, tb.TeamID)
	if err != nil {
		return ev, err
	}
	if team == nil {
		return ev, newErr(http.StatusConflict, CodeOperationConflict, "bound team missing in native store")
	}
	ev.TenantState, ev.TeamID, ev.TeamType, ev.DefaultChannelID, ev.PolicyVersion = tb.State, tb.TeamID, team.Type, tb.DefaultChannelID, tb.PolicyVersion
	ev.TeamName = team.Name
	channelID, err := s.native.DefaultChannelID(ctx, tb.TeamID)
	if err != nil {
		return ev, err
	}
	if channelID == "" || channelID != tb.DefaultChannelID {
		return ev, newErr(http.StatusConflict, CodeOperationConflict, "bound default channel missing or changed in native store")
	}
	if ub != nil {
		member, merr := s.native.IsTeamMember(ctx, tb.TeamID, ub.NativeUserID)
		if merr != nil {
			return ev, merr
		}
		ev.UserID, ev.AccessState, ev.AccessVersion, ev.TeamMember = ub.NativeUserID, ub.AccessState, ub.AccessVersion, &member
	}
	return ev, nil
}
