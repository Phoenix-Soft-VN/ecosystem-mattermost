// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Break: signing another endpoint/body/timestamp, or retrying an uncertain downstream mutation.
func TestTaskCommandSigning(t *testing.T) {
	secret := []byte("lab-deployment-task-key")
	stamp := time.Unix(1800000000, 0)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/hooks/mattermost/endpoint-a/task" {
			t.Error("wrong endpoint")
		}
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		if payload["requestId"] != "request-1" || payload["nativeUserId"] != "a1" || payload["channelId"] != "channel-a" {
			t.Error("command identity lost")
		}
		derive := hmac.New(sha256.New, secret)
		derive.Write([]byte("phoenix-task-endpoint:endpoint-a"))
		mac := hmac.New(sha256.New, derive.Sum(nil))
		mac.Write(append([]byte("1800000000."), raw...))
		if r.Header.Get("X-Phoenix-Timestamp") != strconv.FormatInt(stamp.Unix(), 10) || r.Header.Get("X-Phoenix-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature does not cover endpoint/raw body/timestamp")
		}
		w.WriteHeader(504)
		_, _ = w.Write([]byte(`{"state":"UNKNOWN"}`))
	}))
	defer server.Close()
	client := &TaskCommand{GatewayURL: server.URL, Secret: secret, HTTP: server.Client(), Now: func() time.Time { return stamp }}
	reply, err := client.Execute(context.Background(), "endpoint-a", TaskEnvelope{RequestID: "request-1", NativeUserID: "a1", ChannelID: "channel-a", Text: "Create a task"})
	if reply.State != "UNKNOWN" || calls.Load() != 1 {
		t.Fatalf("uncertain task was retried or not held UNKNOWN: %s calls=%d err=%v", reply.State, calls.Load(), err)
	}
}
