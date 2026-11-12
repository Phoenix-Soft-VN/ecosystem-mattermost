// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TaskEnvelope is the signed body of the native /task adapter (contracts C4). It carries no tenant:
// the Gateway resolves the tenant from the endpoint and the native bindings.
type TaskEnvelope struct {
	RequestID    string `json:"requestId"`
	IssuedAt     int64  `json:"issuedAt"`
	NativeUserID string `json:"nativeUserId"`
	ChannelID    string `json:"channelId"`
	Text         string `json:"text"`
}

// TaskReply is the Gateway answer shown to the user.
type TaskReply struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// DeriveEndpointKey derives the per-endpoint signing key from the deployment secret. A revoked or
// rotated endpoint stops verifying on the Gateway without any key being stored in the native DB.
func DeriveEndpointKey(secret []byte, endpointID string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("phoenix-task-endpoint:" + endpointID))
	return m.Sum(nil)
}

// SignTask returns hex(HMAC-SHA256(key, "<timestamp>.<raw body>")).
func SignTask(key []byte, timestamp int64, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// TaskCommand calls the Gateway task callback POST /hooks/mattermost/{endpointId}/task.
type TaskCommand struct {
	GatewayURL string
	Secret     []byte
	HTTP       *http.Client
	Now        func() time.Time
}

func (c *TaskCommand) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Execute signs and sends one /task request. The request id is fixed by the caller, so a retry of
// the same command resolves to the Gateway's stored result instead of creating a second task. A
// transport failure after sending is reported as UNKNOWN: the caller must not blind-retry with a
// new request id.
func (c *TaskCommand) Execute(ctx context.Context, endpointID string, env TaskEnvelope) (TaskReply, error) {
	env.IssuedAt = c.now().Unix()
	body, err := json.Marshal(env)
	if err != nil {
		return TaskReply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GatewayURL+"/hooks/mattermost/"+endpointID+"/task", bytes.NewReader(body))
	if err != nil {
		return TaskReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Phoenix-Timestamp", strconv.FormatInt(env.IssuedAt, 10))
	req.Header.Set("X-Phoenix-Signature", SignTask(DeriveEndpointKey(c.Secret, endpointID), env.IssuedAt, body))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TaskReply{State: "UNKNOWN", Text: "Chưa xác nhận được kết quả tạo công việc. Vui lòng kiểm tra lại trước khi thử lại."}, wrapErr(http.StatusGatewayTimeout, CodeAuthorityDown, "task callback outcome unknown", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	var reply TaskReply
	_ = json.Unmarshal(data, &reply)
	if reply.Text == "" {
		reply.Text = strings.TrimSpace(string(data))
		if len(reply.Text) > 300 {
			reply.Text = reply.Text[:300]
		}
	}
	if resp.StatusCode >= 500 {
		reply.State = "UNKNOWN"
	}
	return reply, nil
}

var (
	taskMu      sync.RWMutex
	defaultTask *TaskCommand
)

// SetDefaultTask installs the process-wide /task adapter.
func SetDefaultTask(t *TaskCommand) { taskMu.Lock(); defaultTask = t; taskMu.Unlock() }

// DefaultTask returns the /task adapter, or nil when it is not configured.
func DefaultTask() *TaskCommand { taskMu.RLock(); defer taskMu.RUnlock(); return defaultTask }

// TaskEndpointID returns the Gateway task endpoint registered for a tenant ("" if none).
func (p *Policy) TaskEndpointID(ctx context.Context, tenantID string) (string, error) {
	var id *string
	err := p.db.QueryRowContext(ctx, `SELECT task_endpoint_id FROM phoenix_tenant_bindings WHERE tenant_id=$1`, tenantID).Scan(&id)
	if err != nil || id == nil {
		return "", err
	}
	return *id, nil
}

// ShortHash is the exported form of the stable hash used for deterministic identifiers.
func ShortHash(parts ...string) string { return shortHash(parts...)[:24] }
