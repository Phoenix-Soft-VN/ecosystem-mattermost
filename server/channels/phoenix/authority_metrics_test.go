// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Mutation: omit telemetry at the actual authorization boundary, emit actor
// identifiers, or emit once per request instead of bounded aggregate samples.
func TestAuthorityTelemetry(t *testing.T) {
	var failed atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if failed.Load() {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(AuthorityDecision{Active: true, ChatEntitled: true, MembershipState: "ACTIVE", TenantState: "ACTIVE", ExpiresAt: time.Now().Add(time.Minute)})
	}))
	defer server.Close()
	authority := NewAuthority(&AuthorityClient{URL: server.URL, HTTP: server.Client(), Signer: &BridgeSigner{Secret: []byte("unit-only")}})
	var samples []map[string]string
	authority.Audit = func(event string, fields map[string]string) {
		require.Equal(t, "phoenix.authority.metrics", event)
		samples = append(samples, fields)
	}
	actor := ActorContext{SessionID: "session-private", TenantID: "tenant-private", Sub: "subject-private", SID: "sid-private", AccessVersion: 1}
	require.NoError(t, authority.Check(context.Background(), actor))
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, authority.Check(context.Background(), actor))
	require.Len(t, samples, 1, "the cache hit must not emit a per-request sample")
	failed.Store(true)
	authority.Invalidate(actor.SessionID)
	require.Error(t, authority.Check(context.Background(), actor))
	require.Len(t, samples, 1, "failed checks share the same rate bound")
	time.Sleep(5100 * time.Millisecond)
	failed.Store(false)
	require.NoError(t, authority.Check(context.Background(), actor))
	require.EqualValues(t, 3, calls.Load(), "four checks include exactly one cache hit")
	require.Len(t, samples, 2)
	last := samples[1]
	for key, want := range map[string]string{"checks_total": "4", "refreshes_total": "3", "cache_hits_total": "1", "failures_total": "1", "unavailable_total": "1"} {
		require.Equal(t, want, last[key], key)
	}
	require.Len(t, last, 8, "only fixed aggregate numeric fields may be logged")
	for key, value := range last {
		_, err := strconv.ParseInt(value, 10, 64)
		require.NoError(t, err, key)
	}
	age, err := strconv.ParseInt(last["max_cache_age_milliseconds"], 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, age, int64(20))
	require.LessOrEqual(t, age, int64(18000))
	duration, err := strconv.ParseInt(last["duration_microseconds_total"], 10, 64)
	require.NoError(t, err)
	require.Positive(t, duration)
}
