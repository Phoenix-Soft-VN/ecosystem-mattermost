// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Break: allowing a wrong OIDC claim/algorithm or consuming a transaction more than once.
func TestOIDCAbuse(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	key := newTestKey(t, "gateway-key")
	now := time.Unix(1800000000, 0)
	service := &AuthService{Store: NewStore(f.db, func() time.Time { return now }), Settings: Settings{OIDCIssuer: testIssuer, OIDCClientID: "mattermost", SiteOrigin: "https://chat.mm-replace.test", OIDCAuthorizeURL: testIssuer + "/oauth2/authorize"}, Keys: staticKeys{"gateway-key": &key.key.PublicKey}, Now: func() time.Time { return now }, TxnKey: []byte("01234567890123456789012345678901")}
	token := func(change func(jwt.MapClaims), algorithm jwt.SigningMethod, signing any) string {
		claims := jwt.MapClaims{"iss": testIssuer, "aud": "mattermost", "sub": "sub-a1", "sid": "sid-a1", "tenant_id": w.TenantA, "nonce": "test-nonce", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()}
		if change != nil {
			change(claims)
		}
		tok := jwt.NewWithClaims(algorithm, claims)
		tok.Header["kid"] = "gateway-key"
		raw, err := tok.SignedString(signing)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := service.VerifyIDToken(context.Background(), token(nil, jwt.SigningMethodRS256, key.key), hashHex("test-nonce")); err != nil {
		t.Fatal("valid OIDC positive control:", err)
	}
	cases := []struct {
		name   string
		change func(jwt.MapClaims)
	}{
		{"wrong issuer", func(c jwt.MapClaims) { c["iss"] = "https://foreign.invalid" }},
		{"wrong audience", func(c jwt.MapClaims) { c["aud"] = "another-client" }},
		{"multiple audiences without azp", func(c jwt.MapClaims) { c["aud"] = []string{"mattermost", "another-client"} }},
		{"wrong azp", func(c jwt.MapClaims) { c["azp"] = "another-client" }},
		{"missing nonce", func(c jwt.MapClaims) { delete(c, "nonce") }},
		{"wrong nonce", func(c jwt.MapClaims) { c["nonce"] = "wrong" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() }},
		{"future iat", func(c jwt.MapClaims) { c["iat"] = now.Add(time.Minute).Unix() }},
		{"missing iat", func(c jwt.MapClaims) { delete(c, "iat") }},
		{"missing sub", func(c jwt.MapClaims) { delete(c, "sub") }},
		{"missing sid", func(c jwt.MapClaims) { delete(c, "sid") }},
		{"missing tenant", func(c jwt.MapClaims) { delete(c, "tenant_id") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.VerifyIDToken(context.Background(), token(tc.change, jwt.SigningMethodRS256, key.key), hashHex("test-nonce")); err == nil {
				t.Fatal("abusive token accepted")
			}
		})
	}
	if _, err := service.VerifyIDToken(context.Background(), token(nil, jwt.SigningMethodHS256, []byte("forged-secret")), hashHex("test-nonce")); err == nil {
		t.Fatal("HMAC accepted as OIDC")
	}
	other := newTestKey(t, "other")
	if _, err := service.VerifyIDToken(context.Background(), token(nil, jwt.SigningMethodRS256, other.key), hashHex("test-nonce")); err == nil {
		t.Fatal("foreign signature accepted")
	}

	rec := httptest.NewRecorder()
	service.Login(rec, httptest.NewRequest("GET", "https://chat.mm-replace.test/phoenix/auth/login?return=https://evil.invalid", nil))
	if rec.Code != 302 {
		t.Fatalf("login status=%d", rec.Code)
	}
	redirect, _ := url.Parse(rec.Header().Get("Location"))
	q := redirect.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatal("login must create PKCE/state/nonce")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("browser binding must be secure/HttpOnly")
	}
	stateHash, browserHash := hashHex(q.Get("state")), hashHex(cookies[0].Value)
	if _, err := service.Store.ConsumeAuthTxn(context.Background(), stateHash, hashHex("wrong-browser")); err != ErrTxnInvalid {
		t.Fatal("browser mismatch accepted")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := service.Store.ConsumeAuthTxn(context.Background(), stateHash, browserHash); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("transaction consumed %d times", successes.Load())
	}
	expired := AuthTxn{StateHash: hashHex("expired-state"), BrowserBindHash: hashHex("browser"), NonceHash: hashHex("nonce"), EncryptedPKCE: "encrypted", ReturnPath: "/", ExpiresAtMS: now.Add(-time.Second).UnixMilli()}
	if err := service.Store.CreateAuthTxn(context.Background(), expired); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.ConsumeAuthTxn(context.Background(), expired.StateHash, expired.BrowserBindHash); err != ErrTxnInvalid {
		t.Fatal("301s transaction accepted")
	}
	for _, path := range []string{"https://evil.invalid", "//evil.invalid", "/%2f%2fevil.invalid", "/\\evil.invalid", "/api/v4/users", "/phoenix/auth/callback"} {
		if got := SanitizeReturnPath(path); got != "/" {
			t.Fatalf("unsafe return %q accepted as %q", path, got)
		}
	}
	// Pending binding must stop callback before any native session can be created.
	if _, err := f.db.Exec(`UPDATE phoenix_user_bindings SET access_state='PENDING' WHERE sub='sub-a1'`); err != nil {
		t.Fatal(err)
	}
	raw := token(nil, jwt.SigningMethodRS256, key.key)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": raw})
	}))
	defer endpoint.Close()
	service.HTTP = endpoint.Client()
	service.Settings.OIDCTokenURL = endpoint.URL
	sealed, err := service.seal("verifier")
	if err != nil {
		t.Fatal(err)
	}
	txn := AuthTxn{StateHash: hashHex("pending-state"), BrowserBindHash: hashHex("pending-browser"), NonceHash: hashHex("test-nonce"), EncryptedPKCE: sealed, ReturnPath: "/", ExpiresAtMS: now.Add(300 * time.Second).UnixMilli()}
	if err := service.Store.CreateAuthTxn(context.Background(), txn); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "https://chat.mm-replace.test/phoenix/auth/callback?state=pending-state&code=code", nil)
	req.AddCookie(&http.Cookie{Name: authBindCookie, Value: "pending-browser"})
	pending := httptest.NewRecorder()
	service.Callback(pending, req)
	if pending.Code != 409 || !strings.Contains(pending.Body.String(), "CHAT_PROVISIONING_PENDING") {
		t.Fatalf("pending callback status=%d body=%s", pending.Code, pending.Body)
	}
	for _, cookie := range pending.Result().Cookies() {
		if cookie.Name == "MMAUTHTOKEN" && cookie.Value != "" {
			t.Fatal("pending created a native cookie")
		}
	}
}

// Break: caching past 30s, sharing authority across tenant/version, or serving after outage.
func TestSessionDeadline(t *testing.T) {
	var mode atomic.Int32
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if mode.Load() == 3 {
			w.WriteHeader(503)
			return
		}
		d := AuthorityDecision{Active: mode.Load() != 1, ChatEntitled: mode.Load() != 2, MembershipState: "ACTIVE", TenantState: "ACTIVE", ExpiresAt: time.Now().Add(30 * time.Second)}
		_ = json.NewEncoder(w).Encode(d)
	}))
	defer server.Close()
	authority := NewAuthority(&AuthorityClient{URL: server.URL, HTTP: server.Client(), Issuer: testIssuer, Signer: &BridgeSigner{Issuer: "native", Secret: []byte("lab-bridge")}})
	actor := ActorContext{SessionID: "session-a1", TenantID: "tenant-a", Sub: "a1", SID: "sid-a1", AccessVersion: 1}
	if err := authority.Check(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	actor.AccessVersion = 2
	if err := authority.Check(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("new access revision reused old authority")
	}
	actor.TenantID = "tenant-b"
	if err := authority.Check(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("tenant mixed cached authority")
	}
	for _, scenario := range []struct {
		mode   int32
		status int
	}{{1, 401}, {2, 403}, {3, 503}} {
		mode.Store(scenario.mode)
		authority.mu.Lock()
		entry := authority.entries[actor.SessionID]
		entry.fetched = time.Now().Add(-31 * time.Second)
		authority.entries[actor.SessionID] = entry
		authority.mu.Unlock()
		err := authority.Check(context.Background(), actor)
		pe, ok := err.(*Error)
		if !ok || pe.Status != scenario.status {
			t.Fatalf("authority mode=%d got %v want %d", scenario.mode, err, scenario.status)
		}
	}
}

// Keep RSA fixture type anchored to the same public key API used by production.
var _ *rsa.PublicKey
