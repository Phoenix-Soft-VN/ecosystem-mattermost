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
	backchannelEvent     = "http://schemas.openid.net/event/backchannel-logout"
	accessChangedEvent   = "https://phoenixsoft.vn/events/access-changed"
	themeInvalidatedEvent = "https://phoenixsoft.vn/events/theme-invalidated"
	logoutMaxAge         = 600 * time.Second
	logoutReplayGrace    = 700 * time.Second
)

// PushEvent describes a verified Gateway push JWT (`backchannel-logout`, `access-changed`, or `theme-invalidated`).
type PushEvent struct {
	SID      string
	Sub      string
	TenantID string
	JTI      string
	ExpMS    int64
	Events   map[string]bool
}

// LogoutService revokes native sessions on Gateway back-channel logout / push events and browser cleanup.
type LogoutService struct {
	Store     *Store
	Settings  Settings
	Keys      KeySet
	Sessions  SessionOps
	Policy    *Policy
	Authority *Authority
	Shell     *ShellService
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
	return l.revokeSessionIDs(ctx, ids)
}

// RevokeSub revokes all native sessions bound to (issuer, sub).
func (l *LogoutService) RevokeSub(ctx context.Context, sub string) (int, error) {
	ids, err := l.Store.SessionIDsBySub(ctx, l.Settings.OIDCIssuer, sub)
	if err != nil {
		return 0, err
	}
	return l.revokeSessionIDs(ctx, ids)
}

func (l *LogoutService) revokeSessionIDs(ctx context.Context, ids []string) (int, error) {
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
	if l.Shell != nil {
		l.Shell.InvalidateSession(sessionID)
	}
}

// VerifyLogoutToken validates a standard back-channel logout token requiring exp, iat, jti, no nonce, and sid/sub.
func (l *LogoutService) VerifyLogoutToken(ctx context.Context, raw string) (sid, jti string, err error) {
	pe, err := l.VerifyPushToken(ctx, raw)
	if err != nil {
		return "", "", err
	}
	if !pe.Events[backchannelEvent] || pe.SID == "" {
		return "", "", errors.New("missing back-channel logout sid")
	}
	return pe.SID, pe.JTI, nil
}

// VerifyPushToken validates a signed Phoenix push JWT (`backchannel-logout`, `access-changed`, `theme-invalidated`),
// requiring RS256 from JWKS, exact iss/aud, required exp & iat & jti, and no nonce.
func (l *LogoutService) VerifyPushToken(ctx context.Context, raw string) (PushEvent, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(l.Settings.OIDCIssuer), jwt.WithAudience(l.Settings.OIDCClientID),
		jwt.WithExpirationRequired(), jwt.WithTimeFunc(l.now), jwt.WithLeeway(5*time.Second),
	)
	tok, perr := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("kid required")
		}
		return l.Keys.Key(ctx, kid)
	})
	if perr != nil || !tok.Valid {
		return PushEvent{}, errors.New("push token rejected")
	}
	iat, e := claims.GetIssuedAt()
	if e != nil || iat == nil || l.now().Sub(iat.Time) > logoutMaxAge || iat.Time.After(l.now().Add(5*time.Second)) {
		return PushEvent{}, errors.New("iat outside the accepted window")
	}
	exp, ee := claims.GetExpirationTime()
	if ee != nil || exp == nil {
		return PushEvent{}, errors.New("exp required")
	}
	rawEvents, _ := claims["events"].(map[string]any)
	if len(rawEvents) == 0 {
		return PushEvent{}, errors.New("events claim required")
	}
	parsedEvents := make(map[string]bool, 3)
	for _, known := range []string{backchannelEvent, accessChangedEvent, themeInvalidatedEvent} {
		if _, ok := rawEvents[known]; ok {
			parsedEvents[known] = true
		}
	}
	if len(parsedEvents) == 0 {
		return PushEvent{}, errors.New("unsupported push event type")
	}
	if _, has := claims["nonce"]; has {
		return PushEvent{}, errors.New("push token must not carry a nonce")
	}
	sid, _ := claims["sid"].(string)
	sub, _ := claims["sub"].(string)
	tenantID, _ := claims["tenant_id"].(string)
	jti, _ := claims["jti"].(string)
	if jti == "" || len(sid) > 255 || len(sub) > 255 || len(jti) > 255 {
		return PushEvent{}, errors.New("jti required")
	}
	if parsedEvents[backchannelEvent] && sid == "" && sub == "" {
		return PushEvent{}, errors.New("sid or sub required for backchannel-logout")
	}
	if parsedEvents[accessChangedEvent] && !parsedEvents[backchannelEvent] && sub == "" {
		return PushEvent{}, errors.New("sub required for access-changed")
	}
	expMS := exp.Time.Add(60 * time.Second).UnixMilli()
	if expMS < l.now().Add(logoutReplayGrace).UnixMilli() {
		expMS = l.now().Add(logoutReplayGrace).UnixMilli()
	}
	return PushEvent{SID: sid, Sub: sub, TenantID: tenantID, JTI: jti, ExpMS: expMS, Events: parsedEvents}, nil
}

// Backchannel handles POST /phoenix/auth/backchannel-logout and POST /phoenix/push (form: logout_token).
// Executes revocation/invalidation BEFORE recording replay jti (MM-04) so failed attempts can retry.
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
	pe, err := l.VerifyPushToken(r.Context(), r.PostForm.Get("logout_token"))
	if err != nil {
		l.audit("phoenix.logout.reject", map[string]string{"reason": "token"})
		WriteError(w, newErr(http.StatusBadRequest, CodeAuthInvalid, "invalid logout token"), l.Audit)
		return
	}
	alreadyConsumed, err := l.Store.HasReplay(r.Context(), "logout", pe.JTI)
	if err != nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "logout unavailable"), l.Audit)
		return
	}
	if alreadyConsumed {
		// Replay: idempotent answer, no side effect.
		writeJSON(w, http.StatusOK, map[string]any{"revoked": 0, "replay": true})
		return
	}

	revoked := 0
	if pe.Events[backchannelEvent] {
		var rerr error
		if pe.SID != "" {
			revoked, rerr = l.RevokeSID(r.Context(), pe.SID)
		} else {
			revoked, rerr = l.RevokeSub(r.Context(), pe.Sub)
		}
		if rerr != nil {
			WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "logout unavailable"), l.Audit)
			return
		}
		if l.Shell != nil && pe.Sub != "" {
			l.Shell.InvalidateSubject(pe.Sub)
		}
	}
	if pe.Events[accessChangedEvent] && pe.Sub != "" {
		affected, aerr := l.Store.ExpireAccessBySub(r.Context(), l.Settings.OIDCIssuer, pe.Sub)
		if aerr != nil {
			WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "access invalidation unavailable"), l.Audit)
			return
		}
		for _, id := range affected {
			l.forget(id)
		}
		if l.Shell != nil {
			l.Shell.InvalidateSubject(pe.Sub)
		}
	}
	if pe.Events[themeInvalidatedEvent] && l.Shell != nil {
		if pe.TenantID != "" {
			l.Shell.InvalidateTenant(pe.TenantID)
		} else {
			l.Shell.InvalidateAll()
		}
	}

	// Only persist replay protection AFTER all side effects commit (MM-04).
	if _, err := l.Store.ConsumeReplay(r.Context(), "logout", pe.JTI, pe.ExpMS); err != nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "logout unavailable"), l.Audit)
		return
	}
	l.audit("phoenix.logout.revoked", map[string]string{"sessions": itoa(revoked)})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
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

// Register mounts the logout and unified push routes.
func (l *LogoutService) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle(authPath+"/backchannel-logout", http.HandlerFunc(l.Backchannel), http.MethodPost)
	handle("/phoenix/push", http.HandlerFunc(l.Backchannel), http.MethodPost)
	handle("/phoenix/logout/cleanup", http.HandlerFunc(l.Cleanup), http.MethodGet)
}
