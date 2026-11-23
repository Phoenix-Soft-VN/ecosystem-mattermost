// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeUnreadStore struct {
	mu       sync.Mutex
	bindings []SessionBinding
}

func (s *fakeUnreadStore) LiveSessionBindingsOfUser(_ context.Context, userID string) ([]SessionBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SessionBinding
	for _, b := range s.bindings {
		if b.NativeUserID == userID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *fakeUnreadStore) LiveSessionBindings(_ context.Context) ([]SessionBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionBinding, len(s.bindings))
	copy(out, s.bindings)
	return out, nil
}

func (s *fakeUnreadStore) SessionBindingByID(_ context.Context, sessionID string) (SessionBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.bindings {
		if b.SessionID == sessionID {
			return b, nil
		}
	}
	return SessionBinding{}, sql.ErrNoRows
}

type fakeUnreadCounter struct {
	mu    sync.Mutex
	count int
	err   error
}

func (c *fakeUnreadCounter) Set(n int, err error) {
	c.mu.Lock()
	c.count, c.err = n, err
	c.mu.Unlock()
}

func (c *fakeUnreadCounter) UnreadBadgeCount(_ context.Context, _ string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count, c.err
}

func verifyHS256Assertion(t *testing.T, raw string, secret []byte) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != wantSig {
		t.Fatalf("HS256 signature mismatch")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return claims
}

// Break (M1): changing AudienceUnread, adding extra body fields, skipping count clamp,
// or reusing jti across pushes.
func TestUnreadPayloadContractAndAud(t *testing.T) {
	secret := []byte("unread-test-secret")
	var mu sync.Mutex
	var bodies []map[string]any
	var jtis []string

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/ui/unread-count" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		claims := verifyHS256Assertion(t, body["assertion"].(string), secret)
		if claims["aud"] != "gateway:internal:ui-unread" {
			t.Errorf("wrong aud: got %v want gateway:internal:ui-unread", claims["aud"])
		}
		if claims["iss"] != "chat-issuer" || claims["sub"] != "sub-u1" || claims["tenant_id"] != "tenant-1" || claims["sid"] != "sid-1" || claims["app"] != "chat" {
			t.Errorf("unexpected claims: %v", claims)
		}
		iat, _ := claims["iat"].(float64)
		exp, _ := claims["exp"].(float64)
		if exp-iat <= 0 || exp-iat > 60 {
			t.Errorf("TTL out of bounds: %v", exp-iat)
		}
		mu.Lock()
		bodies = append(bodies, body)
		jtis = append(jtis, claims["jti"].(string))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()

	st := &fakeUnreadStore{bindings: []SessionBinding{
		{SessionID: "sess-1", Sub: "sub-u1", TenantID: "tenant-1", SID: "sid-1", NativeUserID: "u1"},
	}}
	cnt := &fakeUnreadCounter{count: 15000}
	signer := &BridgeSigner{Issuer: "chat-issuer", Secret: secret}
	p := NewUnreadPusher(st, cnt, signer, gw.URL, gw.Client())

	// 1st push: count 15000 must clamp to 9999, exact 5 body keys, static deepLink "/".
	p.pushUserNow(context.Background(), "u1", false)
	// 2nd push: counter error must send count 0, status "unavailable", with a fresh jti.
	cnt.Set(0, errors.New("db down"))
	p.pushUserNow(context.Background(), "u1", false)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 pushes, got %d", len(bodies))
	}
	allowed := map[string]bool{"appCode": true, "assertion": true, "count": true, "status": true, "deepLink": true}
	for i, b := range bodies {
		if len(b) != 5 {
			t.Fatalf("push %d: expected exactly 5 body keys, got %d: %v", i, len(b), b)
		}
		for k := range b {
			if !allowed[k] {
				t.Fatalf("push %d: unexpected key %q", i, k)
			}
		}
		dl, _ := b["deepLink"].(string)
		if !strings.HasPrefix(dl, "/") {
			t.Fatalf("push %d: deepLink must start with '/': %q", i, dl)
		}
	}
	if bodies[0]["appCode"] != "chat" || bodies[0]["count"].(float64) != 9999 || bodies[0]["status"] != "ready" {
		t.Fatalf("push 0 mismatch: %v", bodies[0])
	}
	if bodies[1]["count"].(float64) != 0 || bodies[1]["status"] != "unavailable" {
		t.Fatalf("push 1 mismatch: %v", bodies[1])
	}
	if jtis[0] == "" || jtis[1] == "" || jtis[0] == jtis[1] {
		t.Fatalf("expected unique jti per POST, got %v", jtis)
	}
}

// Break (M2 + R1): stopping sid on 403 or repeated ASSERTION_INVALID, not stopping on
// 401 SESSION_ENDED|ACCESS_REVOKED, making stop permanent, or clearing all stopped sids at once.
func TestUnreadAssertionRetryAndStopRule(t *testing.T) {
	secret := []byte("unread-test-secret")
	var mu sync.Mutex
	var mode string
	var attempts atomic.Int32
	var seenJTIs []string

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		claims := verifyHS256Assertion(t, body["assertion"].(string), secret)
		n := attempts.Add(1)
		mu.Lock()
		m := mode
		seenJTIs = append(seenJTIs, claims["jti"].(string))
		mu.Unlock()
		switch m {
		case "assertion_once":
			if n == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"ASSERTION_INVALID"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		case "assertion_always":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"ASSERTION_INVALID"}`))
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"APP_NOT_ALLOWED"}`))
		case "session_ended":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"SESSION_ENDED"}`))
		case "access_revoked":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"ACCESS_REVOKED"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer gw.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	st := &fakeUnreadStore{bindings: []SessionBinding{
		{SessionID: "sess-1", Sub: "sub-1", TenantID: "t1", SID: "sid-1", NativeUserID: "u1"},
		{SessionID: "sess-2", Sub: "sub-2", TenantID: "t1", SID: "sid-2", NativeUserID: "u2"},
	}}
	cnt := &fakeUnreadCounter{count: 3}
	p := NewUnreadPusher(st, cnt, &BridgeSigner{Issuer: "chat-issuer", Secret: secret}, gw.URL, gw.Client())
	p.Now = func() time.Time { return now }

	// 1. 401 ASSERTION_INVALID retries once with a fresh jti and succeeds.
	mu.Lock()
	mode = "assertion_once"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if attempts.Load() != 2 {
		t.Fatalf("expected 1 retry (2 attempts) on ASSERTION_INVALID, got %d", attempts.Load())
	}
	mu.Lock()
	if seenJTIs[0] == seenJTIs[1] {
		t.Fatal("retry must mint a fresh jti")
	}
	mu.Unlock()

	// 2. Repeated 401 ASSERTION_INVALID must NOT stop the sid.
	attempts.Store(0)
	mu.Lock()
	mode = "assertion_always"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if attempts.Load() != 2 {
		t.Fatalf("expected exactly 2 attempts (initial + 1 retry), got %d", attempts.Load())
	}
	if p.isStopped("sid-1") {
		t.Fatal("repeated 401 ASSERTION_INVALID must not stop sid")
	}

	// 3. 403 APP_NOT_ALLOWED must NOT stop the sid.
	attempts.Store(0)
	mu.Lock()
	mode = "forbidden"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if p.isStopped("sid-1") {
		t.Fatal("403 must not stop sid")
	}
	attempts.Store(0)
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if attempts.Load() != 1 {
		t.Fatalf("push after 403 must still run, got %d attempts", attempts.Load())
	}

	// 4. 401 SESSION_ENDED stops sid-1; subsequent event push and RefreshOnce skip it.
	attempts.Store(0)
	mu.Lock()
	mode = "session_ended"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if !p.isStopped("sid-1") {
		t.Fatal("401 SESSION_ENDED must stop sid-1")
	}
	attempts.Store(0)
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	if attempts.Load() != 0 {
		t.Fatalf("stopped sid-1 must not push, got %d attempts", attempts.Load())
	}

	// 5. R1: User-initiated activity un-stops sid-1 immediately.
	p.UnstopSID("sid-1")
	p.pushUserNow(context.Background(), "u1", false)
	if attempts.Load() != 1 {
		t.Fatalf("un-stopped sid-1 must push after user activity, got %d", attempts.Load())
	}

	// 6. R1: 401 ACCESS_REVOKED stops sid-1 at t=0 and sid-2 at t+10m; at t+16m TTL sweep
	// prunes sid-1 (not permanent) while keeping sid-2 stopped (no wholesale clear).
	mu.Lock()
	mode = "access_revoked"
	mu.Unlock()
	p.pushUserNow(context.Background(), "u1", false)
	now = now.Add(10 * time.Minute)
	p.pushUserNow(context.Background(), "u2", false)
	if !p.isStopped("sid-1") || !p.isStopped("sid-2") {
		t.Fatal("both sids must be stopped")
	}
	now = now.Add(6 * time.Minute) // t+16m: sid-1 expired (>15m), sid-2 still at 6m (<15m)
	if p.isStopped("sid-1") {
		t.Fatal("sid-1 must be un-stopped after 15m TTL sweep")
	}
	if !p.isStopped("sid-2") {
		t.Fatal("sid-2 must remain stopped (no wholesale clear)")
	}
}

// Break (M3): removing the 30s throttle or coalescing causes 5 rapid events to fire 5 POSTs
// instead of <= 1 immediate + 1 trailing latest count.
func TestUnreadThrottleAndCoalesce(t *testing.T) {
	secret := []byte("unread-test-secret")
	var mu sync.Mutex
	var counts []int

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		counts = append(counts, int(body["count"].(float64)))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()

	st := &fakeUnreadStore{bindings: []SessionBinding{
		{SessionID: "sess-1", Sub: "sub-1", TenantID: "t1", SID: "sid-1", NativeUserID: "u1"},
	}}
	cnt := &fakeUnreadCounter{count: 1}
	p := NewUnreadPusher(st, cnt, &BridgeSigner{Issuer: "chat-issuer", Secret: secret}, gw.URL, gw.Client())
	defer p.Close()

	if p.throttle() != 30*time.Second {
		t.Fatalf("default throttle must be 30s, got %v", p.throttle())
	}
	p.Throttle = 120 * time.Millisecond

	// Fire 5 rapid events while authoritative count advances 1 -> 5.
	p.NotifyUser("u1")
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(counts)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := 2; i <= 5; i++ {
		cnt.Set(i, nil)
		p.NotifyUser("u1")
		time.Sleep(10 * time.Millisecond)
	}
	// Still inside the throttle window: must have at most 1 push so far.
	mu.Lock()
	mid := len(counts)
	mu.Unlock()
	if mid > 1 {
		t.Fatalf("expected <=1 push inside throttle window, got %d: %v", mid, counts)
	}

	// Wait for trailing push after throttle window.
	deadline = time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(counts)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 2 {
		t.Fatalf("expected exactly 2 pushes (1 initial + 1 trailing), got %d: %v", len(counts), counts)
	}
	if counts[1] != 5 {
		t.Fatalf("trailing push must carry latest recomputed count 5, got %d", counts[1])
	}
}

// Break: blocking NotifyUser on 503/slow Gateway.
func TestUnreadNonBlockingOnOutage(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer gw.Close()

	st := &fakeUnreadStore{bindings: []SessionBinding{
		{SessionID: "sess-1", Sub: "sub-1", TenantID: "t1", SID: "sid-1", NativeUserID: "u1"},
	}}
	cnt := &fakeUnreadCounter{count: 2}
	p := NewUnreadPusher(st, cnt, &BridgeSigner{Issuer: "chat-issuer", Secret: []byte("s")}, gw.URL, gw.Client())
	defer p.Close()

	start := time.Now()
	p.NotifyUser("u1")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("NotifyUser must be async/non-blocking, took %v", elapsed)
	}
}

// Break (R2): removing the <=4 worker cap, in-flight guard, or <5m deadline cap.
func TestUnreadBoundedRefresh(t *testing.T) {
	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32
	var total atomic.Int32
	release := make(chan struct{})
	firstEntered := make(chan struct{}, 1)

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			prev := maxConcurrent.Load()
			if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
				break
			}
		}
		select {
		case firstEntered <- struct{}{}:
		default:
		}
		<-release
		inFlight.Add(-1)
		total.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()

	bindings := make([]SessionBinding, 0, 10)
	for i := 0; i < 10; i++ {
		uid := string(rune('a' + i))
		bindings = append(bindings, SessionBinding{
			SessionID: "sess-" + uid, Sub: "sub-" + uid, TenantID: "t1", SID: "sid-" + uid, NativeUserID: "u-" + uid,
		})
	}
	st := &fakeUnreadStore{bindings: bindings}
	cnt := &fakeUnreadCounter{count: 1}
	p := NewUnreadPusher(st, cnt, &BridgeSigner{Issuer: "chat-issuer", Secret: []byte("s")}, gw.URL, gw.Client())
	p.RefreshWorkers = 16 // must be capped to <= 4
	p.RefreshDeadline = 10 * time.Minute

	if p.refreshWorkers() > 4 {
		t.Fatalf("refreshWorkers cap broken: got %d, want <=4", p.refreshWorkers())
	}
	if p.refreshDeadline() >= 5*time.Minute {
		t.Fatalf("refreshDeadline must be <5m, got %v", p.refreshDeadline())
	}

	done := make(chan bool, 1)
	go func() {
		done <- p.RefreshOnce(context.Background())
	}()
	<-firstEntered
	time.Sleep(40 * time.Millisecond)

	// Overlapping run must be rejected immediately by in-flight guard.
	if p.RefreshOnce(context.Background()) {
		t.Fatal("overlapping RefreshOnce must be rejected by in-flight guard")
	}
	if peak := maxConcurrent.Load(); peak > 4 {
		t.Fatalf("worker pool exceeded cap of 4: peak=%d", peak)
	}

	close(release)
	if ok := <-done; !ok {
		t.Fatal("first RefreshOnce must return true")
	}
	if total.Load() != 10 {
		t.Fatalf("expected all 10 users refreshed, got %d", total.Load())
	}
}

type sqlCaptureDriver struct {
	mu      sync.Mutex
	queries []string
}

func (d *sqlCaptureDriver) Open(string) (driver.Conn, error) { return &sqlCaptureConn{d: d}, nil }

type sqlCaptureConn struct{ d *sqlCaptureDriver }

func (c *sqlCaptureConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *sqlCaptureConn) Close() error                        { return nil }
func (c *sqlCaptureConn) Begin() (driver.Tx, error)           { return memTx{}, nil }
func (c *sqlCaptureConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	c.d.queries = append(c.d.queries, q)
	c.d.mu.Unlock()
	return &memRows{cols: []string{"session_id", "issuer", "sub", "sid", "tenant_id", "native_user_id", "access_version", "expires_at"}}, nil
}

// Break (R2): removing SQL expiry filtering or operator exclusion from LiveSessionBindings.
func TestUnreadLiveBindingsSQLFiltersExpiredAndOperators(t *testing.T) {
	capDrv := &sqlCaptureDriver{}
	db := sql.OpenDB(&funcConnector{open: func() (driver.Conn, error) { return capDrv.Open("") }})
	defer db.Close()
	st := NewStore(db, func() time.Time { return time.Unix(1700000000, 0) })

	if _, err := st.LiveSessionBindings(context.Background()); err != nil {
		t.Fatalf("LiveSessionBindings: %v", err)
	}
	if _, err := st.LiveSessionBindingsOfUser(context.Background(), "u1"); err != nil {
		t.Fatalf("LiveSessionBindingsOfUser: %v", err)
	}

	if len(capDrv.queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(capDrv.queries))
	}
	for i, q := range capDrv.queries {
		for _, want := range []string{"expires_at", "idle_expires_at", "absolute_expires_at", "phoenix_operator_accounts"} {
			if !strings.Contains(q, want) {
				t.Fatalf("query %d missing %q filter: %s", i, want, q)
			}
		}
	}
}

type funcConnector struct {
	open func() (driver.Conn, error)
}

func (f *funcConnector) Connect(context.Context) (driver.Conn, error) { return f.open() }
func (f *funcConnector) Driver() driver.Driver                        { return nil }

// Break (C07 + R1 wiring): unregistered /phoenix/unread-count route, wrong count/status,
// operator not blocked, or GET /phoenix/unread-count failing to un-stop sid.
func TestUnreadRouteAndShellWiring(t *testing.T) {
	secret := []byte("unread-test-secret")
	var pushedCount atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		pushedCount.Store(int32(body["count"].(float64)))
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()

	// Seed a minimal in-memory policy cache for a tenant user and an operator.
	policy := NewPolicy(nil, time.Now)
	policy.SetDrift("", nil)
	policy.mu.Lock()
	policy.cache["sess-u1"] = cachedActor{
		actor:   ActorContext{SessionID: "sess-u1", UserID: "u1", TenantID: "t1", Sub: "sub-1", SID: "sid-1"},
		expires: time.Now().Add(time.Minute),
	}
	policy.cache["sess-op"] = cachedActor{
		actor:   ActorContext{SessionID: "sess-op", UserID: "op1", Operator: true},
		expires: time.Now().Add(time.Minute),
	}
	policy.mu.Unlock()

	st := &fakeUnreadStore{bindings: []SessionBinding{
		{SessionID: "sess-u1", Sub: "sub-1", TenantID: "t1", SID: "sid-1", NativeUserID: "u1"},
	}}
	cnt := &fakeUnreadCounter{count: 7}
	pusher := NewUnreadPusher(st, cnt, &BridgeSigner{Issuer: "chat-issuer", Secret: secret}, gw.URL, gw.Client())
	pusher.stopSID("sid-1")

	var curSess, curUser string
	var hasSess bool
	sessions := &dynamicShellSession{fn: func() (string, string, string, bool) { return curSess, curUser, "csrf", hasSess }}
	shell := &ShellService{Policy: policy, Sessions: sessions, UnreadCounter: cnt, UnreadPusher: pusher}

	mux := http.NewServeMux()
	shell.Register(func(path string, h http.Handler, _ ...string) { mux.Handle(path, h) })

	// 1. Authenticated tenant user gets 7 + ready, and sid-1 is un-stopped so push matches 7.
	curSess, curUser, hasSess = "sess-u1", "u1", true
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/phoenix/unread-count", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Count  int    `json:"count"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Count != 7 || got.Status != "ready" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if pusher.isStopped("sid-1") {
		t.Fatal("GET /phoenix/unread-count must un-stop actor sid")
	}
	pusher.pushUserNow(context.Background(), "u1", false)
	if int(pushedCount.Load()) != got.Count {
		t.Fatalf("bell count (%d) != pushed count (%d)", got.Count, pushedCount.Load())
	}

	// 2. Counter error returns unavailable, never a fake count.
	cnt.Set(0, errors.New("db error"))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/phoenix/unread-count", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("expected unavailable, got %d %s", rec.Code, rec.Body.String())
	}

	// 3. Operator is rejected (403).
	curSess, curUser, hasSess = "sess-op", "op1", true
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/phoenix/unread-count", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator must get 403, got %d", rec.Code)
	}

	// 4. Unauthenticated is rejected (401).
	hasSess = false
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/phoenix/unread-count", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated must get 401, got %d", rec.Code)
	}
}

type dynamicShellSession struct {
	fn func() (string, string, string, bool)
}

func (d *dynamicShellSession) SessionFromRequest(context.Context, *http.Request) (string, string, string, bool) {
	return d.fn()
}
