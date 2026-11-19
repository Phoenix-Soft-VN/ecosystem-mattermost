// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type memBindingState struct {
	mu           sync.Mutex
	binding      SessionBinding
	replays      map[string]bool
	failNextSIDs bool
	failNextSub  bool
	expiredSubs  int
}

type memSQLDriver struct{ state *memBindingState }

func (d memSQLDriver) Open(string) (driver.Conn, error) { return &memSQLConn{state: d.state}, nil }

type memSQLConn struct{ state *memBindingState }

func (c *memSQLConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *memSQLConn) Close() error                        { return nil }
func (c *memSQLConn) Begin() (driver.Tx, error)           { return memTx{}, nil }
func (c *memSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return memTx{}, nil
}

type memTx struct{}

func (memTx) Commit() error   { return nil }
func (memTx) Rollback() error { return nil }

type memRows struct {
	cols []string
	rows [][]driver.Value
	idx  int
}

func (r *memRows) Columns() []string { return r.cols }
func (r *memRows) Close() error      { return nil }
func (r *memRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

func (c *memSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	switch {
	case strings.Contains(query, "FROM phoenix_session_bindings WHERE session_id=$1"):
		b := c.state.binding
		return &memRows{
			cols: []string{"session_id", "issuer", "sub", "sid", "tenant_id", "native_user_id", "access_version", "expires_at", "access_token_enc", "refresh_token_enc", "access_expires_at", "idle_expires_at", "absolute_expires_at", "outage_since"},
			rows: [][]driver.Value{{b.SessionID, b.Issuer, b.Sub, b.SID, b.TenantID, b.NativeUserID, b.AccessVersion, b.ExpiresAtMS, b.AccessTokenEnc, b.RefreshTokenEnc, b.AccessExpiresAtMS, b.IdleExpiresAtMS, b.AbsoluteExpiresAtMS, b.OutageSinceMS}},
		}, nil
	case strings.Contains(query, "FROM phoenix_session_bindings WHERE sid=$1"):
		if c.state.failNextSIDs {
			c.state.failNextSIDs = false
			return nil, errors.New("db hiccup")
		}
		return &memRows{cols: []string{"session_id"}, rows: [][]driver.Value{{c.state.binding.SessionID}}}, nil
	case strings.Contains(query, "FROM phoenix_session_bindings WHERE issuer=$1 AND sub=$2"):
		return &memRows{cols: []string{"session_id"}, rows: [][]driver.Value{{c.state.binding.SessionID}}}, nil
	case strings.Contains(query, "UPDATE phoenix_session_bindings SET access_expires_at=0"):
		if c.state.failNextSub {
			c.state.failNextSub = false
			return nil, errors.New("db hiccup")
		}
		c.state.binding.AccessExpiresAtMS = 0
		c.state.expiredSubs++
		return &memRows{cols: []string{"session_id"}, rows: [][]driver.Value{{c.state.binding.SessionID}}}, nil
	case strings.Contains(query, "FROM phoenix_replay_keys"):
		key, _ := args[1].Value.(string)
		if c.state.replays[key] {
			return &memRows{cols: []string{"one"}, rows: [][]driver.Value{{int64(1)}}}, nil
		}
		return &memRows{cols: []string{"one"}, rows: nil}, nil
	default:
		return &memRows{cols: []string{"session_id"}, rows: nil}, nil
	}
}

func (c *memSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	switch {
	case strings.Contains(query, "UPDATE phoenix_session_bindings") && strings.Contains(query, "access_token_enc=$2"):
		c.state.binding.AccessTokenEnc = args[1].Value.(string)
		if rf := args[2].Value.(string); rf != "" {
			c.state.binding.RefreshTokenEnc = rf
		}
		c.state.binding.AccessExpiresAtMS = args[3].Value.(int64)
		c.state.binding.IdleExpiresAtMS = args[4].Value.(int64)
		c.state.binding.OutageSinceMS = args[5].Value.(int64)
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "SET outage_since=CASE"):
		if c.state.binding.OutageSinceMS == 0 {
			c.state.binding.OutageSinceMS = args[1].Value.(int64)
		}
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "INSERT INTO phoenix_replay_keys"):
		key := args[1].Value.(string)
		if c.state.replays[key] {
			return driver.RowsAffected(0), nil
		}
		c.state.replays[key] = true
		return driver.RowsAffected(1), nil
	default:
		return driver.RowsAffected(1), nil
	}
}

type unitSessionOps struct{ revoked atomic.Int32 }

func (u *unitSessionOps) NewSession(context.Context, http.ResponseWriter, *http.Request, string, func(string, int64) error) (string, error) {
	return "", nil
}
func (u *unitSessionOps) RevokeSession(context.Context, string) error { u.revoked.Add(1); return nil }
func (u *unitSessionOps) SessionIDFromRequest(context.Context, *http.Request) (string, bool) {
	return "s1", true
}
func (u *unitSessionOps) SessionFromRequest(context.Context, *http.Request) (string, string, string, bool) {
	return "s1", "s1", "csrf", true
}

type staticUnitKeySet map[string]*rsa.PublicKey

func (s staticUnitKeySet) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if k := s[kid]; k != nil {
		return k, nil
	}
	return nil, errors.New("unknown kid")
}

func openMemDB(t *testing.T, state *memBindingState) *sql.DB {
	t.Helper()
	name := "phoenix_mem_" + t.Name()
	sql.Register(name, memSQLDriver{state: state})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUnifiedPushMM04AndEvents(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Unix(1800000000, 0)
	state := &memBindingState{
		binding:      SessionBinding{SessionID: "sess-1", Issuer: "https://auth.example.test", Sub: "sub-1", SID: "sid-1"},
		replays:      map[string]bool{},
		failNextSIDs: true,
	}
	db := openMemDB(t, state)
	ops := &unitSessionOps{}
	shell := &ShellService{}
	shell.mu.Lock()
	shell.cache = map[string]cachedShellEntry{"k": {sessionID: "sess-1", sub: "sub-1", tenantID: "tenant-1"}}
	shell.mu.Unlock()

	svc := &LogoutService{
		Store:    NewStore(db, func() time.Time { return now }),
		Settings: Settings{OIDCIssuer: "https://auth.example.test", OIDCClientID: "mattermost"},
		Keys:     staticUnitKeySet{"k1": &key.PublicKey},
		Sessions: ops,
		Shell:    shell,
		Now:      func() time.Time { return now },
	}

	sign := func(claims jwt.MapClaims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		raw, serr := tok.SignedString(key)
		require.NoError(t, serr)
		return raw
	}

	// Missing exp must fail.
	noExp := sign(jwt.MapClaims{"iss": svc.Settings.OIDCIssuer, "aud": "mattermost", "iat": now.Unix(), "sid": "sid-1", "jti": "jti-1", "events": map[string]any{backchannelEvent: map[string]any{}}})
	_, err = svc.VerifyPushToken(context.Background(), noExp)
	require.Error(t, err)

	// MM-04: First attempt fails in Store -> jti NOT consumed -> retry succeeds and revokes session!
	validLogout := sign(jwt.MapClaims{"iss": svc.Settings.OIDCIssuer, "aud": "mattermost", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "sid": "sid-1", "jti": "logout-jti", "events": map[string]any{backchannelEvent: map[string]any{}}})
	req1 := httptest.NewRequest(http.MethodPost, "/phoenix/push", strings.NewReader(url.Values{"logout_token": {validLogout}}.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	svc.Backchannel(rec1, req1)
	require.Equal(t, http.StatusServiceUnavailable, rec1.Code)
	require.False(t, state.replays["logout-jti"])
	require.EqualValues(t, 0, ops.revoked.Load())

	req2 := httptest.NewRequest(http.MethodPost, "/phoenix/push", strings.NewReader(url.Values{"logout_token": {validLogout}}.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	svc.Backchannel(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.True(t, state.replays["logout-jti"])
	require.EqualValues(t, 1, ops.revoked.Load())
	require.Empty(t, shell.cache)

	// access-changed expires access_expires_at without needing sid.
	accessChanged := sign(jwt.MapClaims{"iss": svc.Settings.OIDCIssuer, "aud": "mattermost", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "sub": "sub-1", "jti": "ac-jti", "events": map[string]any{accessChangedEvent: map[string]any{}}})
	req3 := httptest.NewRequest(http.MethodPost, "/phoenix/push", strings.NewReader(url.Values{"logout_token": {accessChanged}}.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec3 := httptest.NewRecorder()
	svc.Backchannel(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)
	require.Equal(t, 1, state.expiredSubs)
}

func TestUnifiedAuthorityLazyRefreshAndOutageGrace(t *testing.T) {
	now := time.Unix(1800000000, 0)
	nowMS := now.UnixMilli()
	state := &memBindingState{
		binding: SessionBinding{
			SessionID: "sess-1", Issuer: "https://auth.example.test", Sub: "sub-1", SID: "sid-1", TenantID: "t-1",
			AccessTokenEnc: "at-0", RefreshTokenEnc: "rt-0",
			AccessExpiresAtMS:   nowMS - 1000,
			IdleExpiresAtMS:     nowMS + 3600_000,
			AbsoluteExpiresAtMS: nowMS + 90*24*3600*1000,
		},
		replays: map[string]bool{},
	}
	db := openMemDB(t, state)
	var tokenCalls atomic.Int32
	var tokenStatus atomic.Int32
	tokenStatus.Store(http.StatusOK)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		st := int(tokenStatus.Load())
		w.WriteHeader(st)
		if st == http.StatusOK {
			_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","expires_in":300}`))
		}
	}))
	defer upstream.Close()

	inactiveCount := atomic.Int32{}
	auth := NewAuthority(&AuthorityClient{URL: upstream.URL, HTTP: upstream.Client(), Signer: &BridgeSigner{Secret: []byte("bridge")}})
	auth.Store = NewStore(db, func() time.Time { return now })
	auth.TokenURL = upstream.URL + "/oauth2/token"
	auth.ClientID = "mattermost"
	auth.ClientSecret = "secret"
	auth.Now = func() time.Time { return now }
	auth.OnInactive = func(context.Context, string) { inactiveCount.Add(1) }

	actor := ActorContext{SessionID: "sess-1", TenantID: "t-1", Sub: "sub-1", SID: "sid-1"}
	require.NoError(t, auth.Check(WithRequestMethod(context.Background(), http.MethodGet), actor))
	require.EqualValues(t, 1, tokenCalls.Load())
	// Fresh token -> second Check does NOT call Gateway!
	require.NoError(t, auth.Check(WithRequestMethod(context.Background(), http.MethodGet), actor))
	require.EqualValues(t, 1, tokenCalls.Load())

	// Force expiry + Gateway 503 outage -> GET allowed within 5m, POST returns 503 without killing session.
	state.binding.AccessExpiresAtMS = nowMS - 1000
	tokenStatus.Store(http.StatusServiceUnavailable)
	require.NoError(t, auth.Check(WithRequestMethod(context.Background(), http.MethodGet), actor))
	err := auth.Check(WithRequestMethod(context.Background(), http.MethodPost), actor)
	require.Error(t, err)
	require.EqualValues(t, 0, inactiveCount.Load())

	// Gateway 403 APP_NOT_ALLOWED -> 403 returned, session NOT killed.
	tokenStatus.Store(http.StatusForbidden)
	err = auth.Check(WithRequestMethod(context.Background(), http.MethodGet), actor)
	require.ErrorIs(t, err, ErrAccessDenied)
	require.EqualValues(t, 0, inactiveCount.Load())

	// Terminal 400 invalid_grant -> calls OnInactive and returns 401.
	tokenStatus.Store(http.StatusBadRequest)
	err = auth.Check(WithRequestMethod(context.Background(), http.MethodGet), actor)
	require.Error(t, err)
	require.EqualValues(t, 1, inactiveCount.Load())
}

func TestUnifiedShellAssertionRetryAnd502(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"ASSERTION_INVALID"}`))
	}))
	defer upstream.Close()

	state := &memBindingState{replays: map[string]bool{}}
	db := openMemDB(t, state)
	pol := NewPolicy(db, nil)
	pol.SetDrift("", map[string]string{})
	svc := &ShellService{
		Policy:     pol,
		Sessions:   &unitSessionOps{},
		Signer:     &BridgeSigner{Issuer: "chat", Secret: []byte("secret")},
		GatewayURL: upstream.URL,
		SiteOrigin: "https://chat.phoenixsoft.vn",
		HTTP:       upstream.Client(),
	}
	// Pre-populate policy actor cache directly so Load does not hit absent PG joins.
	pol.mu.Lock()
	pol.cache["s1"] = cachedActor{actor: ActorContext{SessionID: "s1", UserID: "s1", TenantID: "t1", Sub: "sub1", SID: "sid1"}, expires: time.Now().Add(time.Minute)}
	pol.mu.Unlock()

	rec := httptest.NewRecorder()
	svc.ShellContext(rec, httptest.NewRequest(http.MethodGet, "/phoenix/shell-context", nil))
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.EqualValues(t, 2, calls.Load())
}
