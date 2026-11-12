// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	backchannelEvent  = "http://schemas.openid.net/event/backchannel-logout"
	logoutMaxAge      = 600 * time.Second // the Gateway logout token has no exp; iat + max-age anchors freshness
	logoutReplayGrace = 700 * time.Second
)

// LogoutService revokes native sessions on Gateway back-channel logout and browser cleanup.
type LogoutService struct {
	Store     *Store
	Settings  Settings
	Keys      KeySet
	Sessions  SessionOps
	Policy    *Policy
	Authority *Authority
	Limiter   *Limiter
	Now       func() time.Time
	Audit     func(event string, fields map[string]string)
}

func (l *LogoutService) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *LogoutService) audit(event string, f map[string]string) {
	if l.Audit != nil {
		l.Audit(event, f)
	}
}

// RevokeSID revokes only the sessions bound to that sid, and clears every cache of them.
func (l *LogoutService) RevokeSID(ctx context.Context, sid string) (int, error) {
	ids, err := l.Store.SessionIDsBySID(ctx, sid)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := l.Sessions.RevokeSession(ctx, id); err != nil {
			return n, err
		}
		l.forget(id)
		n++
	}
	return n, nil
}

func (l *LogoutService) forget(sessionID string) {
	if l.Policy != nil {
		l.Policy.InvalidateSession(sessionID)
	}
	if l.Authority != nil {
		l.Authority.Invalidate(sessionID)
	}
}

// VerifyLogoutToken validates a Gateway logout token: RS256 from the fixed JWKS, exact issuer and
// audience (the client id), the back-channel event, no nonce, a sid, iat within the max age and not
// in the future. The token carries no exp by Gateway convention, so none is required.
func (l *LogoutService) VerifyLogoutToken(ctx context.Context, raw string) (sid, jti string, err error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(l.Settings.OIDCIssuer), jwt.WithAudience(l.Settings.OIDCClientID),
		jwt.WithTimeFunc(l.now), jwt.WithLeeway(5*time.Second),
	)
	tok, perr := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("kid required")
		}
		return l.Keys.Key(ctx, kid)
	})
	if perr != nil || !tok.Valid {
		return "", "", errors.New("logout token rejected")
	}
	iat, e := claims.GetIssuedAt()
	if e != nil || iat == nil || l.now().Sub(iat.Time) > logoutMaxAge || iat.Time.After(l.now().Add(5*time.Second)) {
		return "", "", errors.New("iat outside the accepted window")
	}
	events, _ := claims["events"].(map[string]any)
	if _, ok := events[backchannelEvent]; !ok {
		return "", "", errors.New("missing back-channel logout event")
	}
	if _, has := claims["nonce"]; has {
		return "", "", errors.New("logout token must not carry a nonce")
	}
	sid, _ = claims["sid"].(string)
	jti, _ = claims["jti"].(string)
	if sid == "" || jti == "" || len(sid) > 255 || len(jti) > 255 {
		return "", "", errors.New("sid and jti required")
	}
	return sid, jti, nil
}

// Backchannel handles POST /phoenix/auth/backchannel-logout (form: logout_token).
func (l *LogoutService) Backchannel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if l.Limiter != nil {
		if ok, wait := l.Limiter.Allow(remoteIP(r)); !ok {
			w.Header().Set("Retry-After", itoa(wait))
			WriteError(w, newErr(http.StatusTooManyRequests, CodeRateLimited, "too many requests"), l.Audit)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") || r.ParseForm() != nil {
		WriteError(w, newErr(http.StatusBadRequest, CodeAuthInvalid, "invalid logout request"), l.Audit)
		return
	}
	sid, jti, err := l.VerifyLogoutToken(r.Context(), r.PostForm.Get("logout_token"))
	if err != nil {
		l.audit("phoenix.logout.reject", map[string]string{"reason": "token"})
		WriteError(w, newErr(http.StatusBadRequest, CodeAuthInvalid, "invalid logout token"), l.Audit)
		return
	}
	fresh, err := l.Store.ConsumeReplay(r.Context(), "logout", jti, l.now().Add(logoutReplayGrace).UnixMilli())
	if err != nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "logout unavailable"), l.Audit)
		return
	}
	if !fresh {
		// Replay: idempotent answer, no side effect.
		writeJSON(w, http.StatusOK, map[string]any{"revoked": 0, "replay": true})
		return
	}
	n, err := l.RevokeSID(r.Context(), sid)
	if err != nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "logout unavailable"), l.Audit)
		return
	}
	l.audit("phoenix.logout.revoked", map[string]string{"sessions": itoa(n)})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

var cleanupCookies = []string{"MMAUTHTOKEN", "MMUSERID", "MMCSRF", "MMCLOUDURL", authBindCookie}

// Cleanup handles GET /phoenix/logout/cleanup: it clears the cookies of the requesting browser and
// revokes only that browser's own session. It accepts no sid or user parameter, so it cannot be
// used to choose a victim.
func (l *LogoutService) Cleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if sid, ok := l.Sessions.SessionIDFromRequest(r.Context(), r); ok {
		_ = l.Sessions.RevokeSession(r.Context(), sid)
		l.forget(sid)
	}
	secure := strings.HasPrefix(l.Settings.SiteOrigin, "https://")
	for _, name := range cleanupCookies {
		path := "/"
		if name == authBindCookie {
			path = authPath
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: name != "MMUSERID" && name != "MMCSRF", Secure: secure, SameSite: http.SameSiteLaxMode})
	}
	w.Header().Set("Clear-Site-Data", `"cookies", "storage"`)
	w.WriteHeader(http.StatusNoContent)
}

// Register mounts the logout routes.
func (l *LogoutService) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle(authPath+"/backchannel-logout", http.HandlerFunc(l.Backchannel), http.MethodPost)
	handle("/phoenix/logout/cleanup", http.HandlerFunc(l.Cleanup), http.MethodGet)
}
