// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	authBindCookie = "phoenix_auth_bind"
	authPath       = "/phoenix/auth"
)

// SessionOps is the native session lifecycle the auth service needs. The app layer implements it
// with the native login path, so cookies, CSRF and session caches stay native.
type SessionOps interface {
	// NewSession creates the native session for a user and releases cookies only after bind
	// succeeds. If bind fails the session is revoked and no cookie reaches the browser.
	NewSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string, bind func(sessionID string, expiresAtMS int64) error) (string, error)
	RevokeSession(ctx context.Context, sessionID string) error
	// SessionIDFromRequest resolves the caller's own session cookie (never a client-chosen id).
	SessionIDFromRequest(ctx context.Context, r *http.Request) (string, bool)
}

// AuthService implements the native OIDC client of design §6.3 against the Gateway: PKCE S256,
// state + nonce + browser binding, 300 s single-use transaction, RS256 only, live authority check
// and a session that is bound before its cookie is released.
type AuthService struct {
	Store     *Store
	Settings  Settings
	Keys      KeySet
	Secret    string
	TxnKey    []byte
	HTTP      *http.Client
	Sessions  SessionOps
	Authority *Authority
	Policy    *Policy
	Limiter   *Limiter
	Now       func() time.Time
	Audit     func(event string, fields map[string]string)
}

func (s *AuthService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *AuthService) audit(event string, f map[string]string) {
	if s.Audit != nil {
		s.Audit(event, f)
	}
}

// ---- helpers -------------------------------------------------------------------------

func hashHex(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *AuthService) seal(plain string) (string, error) {
	block, err := aes.NewCipher(s.TxnKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *AuthService) open(sealed string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.TxnKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

// LoadTxnKey reads the 32-byte transaction key (base64) from a file.
func LoadTxnKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("PHOENIX_TXN_KEY_FILE must contain a base64 encoded 32-byte key")
	}
	return key, nil
}

// SanitizeReturnPath accepts only a local absolute path. Schemes, hosts, protocol-relative and
// backslash forms, control characters and the Phoenix/native API prefixes are rejected.
func SanitizeReturnPath(p string) string {
	if p == "" || len(p) > 512 || p[0] != '/' || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") {
		return "/"
	}
	if strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n\t") {
		return "/"
	}
	if strings.HasPrefix(u.Path, "/phoenix/") || strings.HasPrefix(u.Path, "/api/") || strings.HasPrefix(u.Path, "/oauth") {
		return "/"
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	return p
}

func (s *AuthService) secureCookies() bool {
	return strings.HasPrefix(s.Settings.SiteOrigin, "https://")
}

func (s *AuthService) fail(w http.ResponseWriter, err error) {
	WriteError(w, err, s.Audit)
}

func (s *AuthService) rateLimited(w http.ResponseWriter, r *http.Request) bool {
	if s.Limiter == nil {
		return false
	}
	if ok, wait := s.Limiter.Allow(remoteIP(r)); !ok {
		w.Header().Set("Retry-After", itoa(wait))
		s.fail(w, newErr(http.StatusTooManyRequests, CodeRateLimited, "too many authentication requests"))
		return true
	}
	return false
}

func itoa(n int) string {
	if n <= 0 {
		return "1"
	}
	b := [20]byte{}
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ---- GET /phoenix/auth/login ---------------------------------------------------------

func (s *AuthService) Login(w http.ResponseWriter, r *http.Request) {
	if s.rateLimited(w, r) {
		return
	}
	// The callback leaves OAuth parameters behind before displaying a browser error.
	// This allowlisted code carries no identity and cannot authorize a native session.
	if r.URL.Query().Get("error") == CodeProvisioningPending {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `<!doctype html><html lang="vi"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Phoenix Chat</title><style>body{font:1rem system-ui;margin:0;min-height:100vh;display:grid;place-items:center;background:#f5f7fb;color:#182338}main{max-width:32rem;margin:1.5rem;padding:2rem;background:white;border-radius:1rem}a{display:inline-block;margin-top:1rem;padding:.75rem 1.25rem;background:#245edb;color:white;border-radius:.5rem;text-decoration:none}</style></head><body><main><h1>Phoenix Chat</h1><p role="status">Tài khoản Chat đang được chuẩn bị. Vui lòng thử lại sau.</p><a href="/phoenix/auth/login">Thử lại</a></main></body></html>`)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	state, nonce, verifier, browser := randomToken(32), randomToken(32), randomToken(48), randomToken(32)
	sealed, err := s.seal(verifier)
	if err != nil {
		s.fail(w, wrapErr(http.StatusInternalServerError, CodeInternal, "transaction setup failed", err))
		return
	}
	ret := SanitizeReturnPath(r.URL.Query().Get("return"))
	txn := AuthTxn{
		StateHash: hashHex(state), NonceHash: hashHex(nonce), BrowserBindHash: hashHex(browser), EncryptedPKCE: sealed,
		ReturnPath: ret, ExpiresAtMS: s.now().Add(AuthTransactionTTL * time.Second).UnixMilli(),
	}
	if err := s.Store.CreateAuthTxn(r.Context(), txn); err != nil {
		s.fail(w, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "transaction store unavailable", err))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: authBindCookie, Value: browser, Path: authPath, MaxAge: AuthTransactionTTL,
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {s.Settings.OIDCClientID},
		"redirect_uri": {s.Settings.SiteOrigin + authPath + "/callback"}, "scope": {"openid"},
		"state": {state}, "nonce": {nonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, s.Settings.OIDCAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

// ---- GET /phoenix/auth/callback ------------------------------------------------------

// IDClaims are the verified identity claims of a Gateway ID token.
type IDClaims struct {
	Subject  string
	SID      string
	TenantID string
	Nonce    string
}

func (s *AuthService) Callback(w http.ResponseWriter, r *http.Request) {
	if s.rateLimited(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	invalid := newErr(http.StatusUnauthorized, CodeAuthInvalid, "invalid sign-in request")
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	cookie, cerr := r.Cookie(authBindCookie)
	// Any authorization error from the Gateway, a missing parameter or a missing browser binding is
	// refused before the transaction is touched.
	if q.Get("error") != "" || state == "" || code == "" || len(state) > 256 || len(code) > 2048 || cerr != nil || cookie.Value == "" {
		s.audit("phoenix.auth.reject", map[string]string{"reason": "request-shape"})
		s.fail(w, invalid)
		return
	}
	txn, err := s.Store.ConsumeAuthTxn(r.Context(), hashHex(state), hashHex(cookie.Value))
	// The binding cookie is single-use whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: authBindCookie, Value: "", Path: authPath, MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	if err != nil {
		reason := "transaction"
		if !errors.Is(err, ErrTxnInvalid) {
			reason = "store"
		}
		s.audit("phoenix.auth.reject", map[string]string{"reason": reason})
		if reason == "store" {
			s.fail(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "authentication unavailable"))
			return
		}
		s.fail(w, invalid)
		return
	}
	verifier, err := s.open(txn.EncryptedPKCE)
	if err != nil {
		s.fail(w, invalid)
		return
	}
	idToken, err := s.exchange(r.Context(), code, verifier)
	if err != nil {
		s.audit("phoenix.auth.reject", map[string]string{"reason": "code-exchange"})
		var pe *Error
		if errors.As(err, &pe) {
			s.fail(w, pe)
			return
		}
		s.fail(w, invalid)
		return
	}
	claims, err := s.VerifyIDToken(r.Context(), idToken, txn.NonceHash)
	if err != nil {
		s.audit("phoenix.auth.reject", map[string]string{"reason": "id-token"})
		s.fail(w, invalid)
		return
	}

	ub, err := s.Store.GetUser(r.Context(), s.Settings.OIDCIssuer, claims.Subject)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		s.audit("phoenix.auth.reject", map[string]string{"reason": "unbound"})
		http.Redirect(w, r, authPath+"/login?error="+CodeProvisioningPending, http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "authentication unavailable"))
		return
	}
	if ub.TenantID != claims.TenantID {
		s.audit("phoenix.auth.reject", map[string]string{"reason": "tenant-mismatch"})
		s.fail(w, ErrAccessDenied)
		return
	}
	switch ub.AccessState {
	case AccessPending:
		http.Redirect(w, r, authPath+"/login?error="+CodeProvisioningPending, http.StatusSeeOther)
		return
	case AccessRevoked:
		s.fail(w, ErrAccessDenied)
		return
	}
	tb, err := s.Store.GetTenant(r.Context(), ub.TenantID)
	if err != nil || tb.State != TenantActive {
		s.fail(w, ErrAccessDenied)
		return
	}
	// Live authority: the Gateway session, membership and CHAT entitlement must hold right now.
	actor := ActorContext{UserID: ub.NativeUserID, Kind: ub.ActorKind, TenantID: ub.TenantID, TeamID: tb.TeamID, Issuer: ub.Issuer, Sub: ub.Sub, SID: claims.SID}
	probe := actor
	probe.SessionID = "login:" + randomToken(12)
	if aerr := s.Authority.Check(r.Context(), probe); aerr != nil {
		s.Authority.Invalidate(probe.SessionID)
		s.fail(w, aerr)
		return
	}
	s.Authority.Invalidate(probe.SessionID)

	teamName, err := s.Store.TenantTeamName(r.Context(), ub.TenantID)
	if err != nil {
		s.fail(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "authentication unavailable"))
		return
	}
	bindFn := func(sessionID string, expiresAtMS int64) error {
		return s.Store.InsertSessionBinding(r.Context(), SessionBinding{
			SessionID: sessionID, Issuer: ub.Issuer, Sub: ub.Sub, SID: claims.SID, TenantID: ub.TenantID,
			NativeUserID: ub.NativeUserID, AccessVersion: ub.AccessVersion, ExpiresAtMS: expiresAtMS,
		})
	}
	sessionID, err := s.Sessions.NewSession(r.Context(), w, r, ub.NativeUserID, bindFn)
	if err != nil {
		s.audit("phoenix.auth.reject", map[string]string{"reason": "session-binding"})
		s.fail(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "authentication unavailable"))
		return
	}
	if s.Policy != nil {
		s.Policy.InvalidateSession(sessionID)
	}
	dest := "/" + teamName
	if strings.HasPrefix(txn.ReturnPath, "/"+teamName+"/") {
		dest = txn.ReturnPath
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// exchange trades the authorization code for tokens at the fixed Gateway token endpoint.
func (s *AuthService) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {s.Settings.SiteOrigin + authPath + "/callback"}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Settings.OIDCTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(s.Settings.OIDCClientID), url.QueryEscape(s.Secret))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", wrapErr(http.StatusServiceUnavailable, CodeAuthorityDown, "token endpoint unreachable", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", newErr(http.StatusUnauthorized, CodeAuthInvalid, "code exchange rejected")
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.IDToken == "" {
		return "", newErr(http.StatusUnauthorized, CodeAuthInvalid, "no id_token in response")
	}
	return tok.IDToken, nil
}

// VerifyIDToken applies the design §6.3 checks: RS256 only (key from the fixed JWKS, never from the
// token), exact issuer and audience, azp when several audiences, exp and iat required, nonce bound to
// this transaction, and non-empty sub, sid and tenant_id.
func (s *AuthService) VerifyIDToken(ctx context.Context, raw, nonceHash string) (IDClaims, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(s.Settings.OIDCIssuer), jwt.WithAudience(s.Settings.OIDCClientID),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now), jwt.WithLeeway(5*time.Second),
	)
	tok, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("kid required")
		}
		return s.Keys.Key(ctx, kid)
	})
	if err != nil || !tok.Valid {
		return IDClaims{}, errors.New("id token rejected")
	}
	if iat, e := claims.GetIssuedAt(); e != nil || iat == nil {
		return IDClaims{}, errors.New("iat required")
	}
	if aud, e := claims.GetAudience(); e != nil || (len(aud) > 1 && claims["azp"] != s.Settings.OIDCClientID) {
		return IDClaims{}, errors.New("azp mismatch")
	}
	if azp, present := claims["azp"]; present && azp != s.Settings.OIDCClientID {
		return IDClaims{}, errors.New("azp mismatch")
	}
	str := func(k string) string { v, _ := claims[k].(string); return v }
	nonce := str("nonce")
	if nonce == "" || subtle.ConstantTimeCompare([]byte(hashHex(nonce)), []byte(nonceHash)) != 1 {
		return IDClaims{}, errors.New("nonce mismatch")
	}
	out := IDClaims{Subject: str("sub"), SID: str("sid"), TenantID: str("tenant_id"), Nonce: nonce}
	if out.Subject == "" || out.SID == "" || out.TenantID == "" || len(out.Subject) > 255 || len(out.TenantID) > 255 {
		return IDClaims{}, errors.New("sub, sid and tenant_id are required")
	}
	return out, nil
}

// Register mounts the auth routes.
func (s *AuthService) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle(authPath+"/login", http.HandlerFunc(s.Login), http.MethodGet)
	handle(authPath+"/callback", http.HandlerFunc(s.Callback), http.MethodGet)
}
