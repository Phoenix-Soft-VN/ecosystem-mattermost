// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SessionLookup resolves the native session of a request (token from the native cookie only) and
// its CSRF token. It is implemented by the app layer.
type SessionLookup interface {
	// SessionFromRequest returns (sessionID, userID, csrfToken, ok).
	SessionFromRequest(ctx context.Context, r *http.Request) (string, string, string, bool)
}

const (
	shellCacheTTL = 600 * time.Second
	shellLKGTTL   = 900 * time.Second
)

type cachedShellEntry struct {
	sessionID   string
	sub         string
	tenantID    string
	status      int
	contentType string
	etag        string
	body        []byte
	freshAt     time.Time
	lkgUntil    time.Time
}

// ShellService is the native BFF of design §6.7. The browser never supplies tenant, sub or sid:
// they come from the immutable session binding, and every Gateway call uses a fresh HMAC assertion
// bound to one operation audience. Upstream 401/403/409 are preserved, 1x ASSERTION_INVALID is
// retried and repeated ASSERTION_INVALID maps to 502 Bad Gateway.
type ShellService struct {
	Policy        *Policy
	Sessions      SessionLookup
	Signer        *BridgeSigner
	GatewayURL    string
	SiteOrigin    string
	LogoutURL     string
	HTTP          *http.Client
	Now           func() time.Time
	Audit         func(event string, fields map[string]string)
	UnreadCounter UnreadCounter
	UnreadPusher  *UnreadPusher

	mu    sync.RWMutex
	cache map[string]cachedShellEntry
}

const maxShellBody = 4096

func (s *ShellService) nowTime() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// InvalidateSession drops cached shell entries for a native session id.
func (s *ShellService) InvalidateSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.cache {
		if v.sessionID == sessionID {
			delete(s.cache, k)
		}
	}
}

// InvalidateSubject drops cached shell entries for a federated subject.
func (s *ShellService) InvalidateSubject(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.cache {
		if v.sub == sub {
			delete(s.cache, k)
		}
	}
}

// InvalidateTenant drops cached shell entries for a tenant id (or all if empty).
func (s *ShellService) InvalidateTenant(tenantID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenantID == "" {
		s.cache = nil
		return
	}
	for k, v := range s.cache {
		if v.tenantID == tenantID {
			delete(s.cache, k)
		}
	}
}

// InvalidateAll clears the entire shell cache.
func (s *ShellService) InvalidateAll() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}

func (s *ShellService) actor(w http.ResponseWriter, r *http.Request) (ActorContext, string, bool) {
	sid, uid, csrf, ok := s.Sessions.SessionFromRequest(r.Context(), r)
	if !ok {
		WriteError(w, ErrNoActor, s.Audit)
		return ActorContext{}, "", false
	}
	actor, err := s.Policy.ResolveActor(WithRequestMethod(r.Context(), r.Method), sid, uid)
	if err != nil {
		WriteError(w, err, s.Audit)
		return ActorContext{}, "", false
	}
	if actor.Operator {
		WriteError(w, newErr(http.StatusForbidden, CodeAccessDenied, "operators have no tenant shell"), s.Audit)
		return ActorContext{}, "", false
	}
	return actor, csrf, true
}

func isAssertionInvalidBody(data []byte) bool {
	var parsed struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return false
	}
	return strings.EqualFold(parsed.Code, "ASSERTION_INVALID") || strings.EqualFold(parsed.Error.Code, "ASSERTION_INVALID")
}

func (s *ShellService) writeShellResponse(w http.ResponseWriter, status int, contentType, etag string, data []byte) {
	w.Header().Set("Cache-Control", "no-store")
	if s.LogoutURL != "" {
		w.Header().Set("X-Phoenix-Logout-Url", s.LogoutURL)
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func postBridge(ctx context.Context, httpClient *http.Client, gatewayURL, path string, signer *BridgeSigner, audience, sub, tenantID, sid string, extra map[string]any, header http.Header, timeout time.Duration) (*http.Response, []byte, error) {
	payload := map[string]any{"appCode": BridgeAppCode, "assertion": signer.Sign(audience, "", sub, tenantID, sid)}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, gatewayURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, data, nil
}

func (s *ShellService) doBridge(ctx context.Context, path, audience string, actor ActorContext, extra map[string]any, header http.Header) (*http.Response, []byte, error) {
	return postBridge(ctx, s.HTTP, s.GatewayURL, path, s.Signer, audience, actor.Sub, actor.TenantID, actor.SID, extra, header, 8*time.Second)
}

// proxy posts {appCode, assertion, ...extra} to a Gateway bridge route and relays status and body.
// Read requests (`cacheKey != ""`) cache 200 answers for 600s and fall back to LKG up to 900s on outage.
func (s *ShellService) proxy(w http.ResponseWriter, r *http.Request, path, audience string, actor ActorContext, extra map[string]any, header http.Header, cacheKey string) {
	now := s.nowTime()
	if cacheKey != "" {
		s.mu.RLock()
		entry, ok := s.cache[cacheKey]
		s.mu.RUnlock()
		if ok && now.Before(entry.freshAt) {
			if inm := r.Header.Get("If-None-Match"); inm != "" && entry.etag != "" && inm == entry.etag {
				s.writeShellResponse(w, http.StatusNotModified, entry.contentType, entry.etag, nil)
				return
			}
			s.writeShellResponse(w, entry.status, entry.contentType, entry.etag, entry.body)
			return
		}
	}

	resp, data, err := s.doBridge(r.Context(), path, audience, actor, extra, header)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && isAssertionInvalidBody(data) {
		// Re-sign assertion once; repeated rejection indicates bridge credential mismatch (502, never 401).
		resp, data, err = s.doBridge(r.Context(), path, audience, actor, extra, header)
		if err == nil && resp.StatusCode == http.StatusUnauthorized && isAssertionInvalidBody(data) {
			s.writeShellResponse(w, http.StatusBadGateway, "application/json; charset=utf-8", "", []byte(`{"code":"ASSERTION_INVALID","message":"gateway rejected bridge assertion"}`))
			return
		}
	}

	if err != nil || resp.StatusCode >= 500 {
		if cacheKey != "" {
			s.mu.RLock()
			lkg, ok := s.cache[cacheKey]
			s.mu.RUnlock()
			if ok && now.Before(lkg.lkgUntil) {
				s.writeShellResponse(w, lkg.status, lkg.contentType, lkg.etag, lkg.body)
				return
			}
		}
		WriteError(w, wrapErr(http.StatusServiceUnavailable, CodeAuthorityDown, "shell unavailable", err), s.Audit)
		return
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.InvalidateSession(actor.SessionID)
	}

	ct := resp.Header.Get("Content-Type")
	etag := resp.Header.Get("ETag")
	if cacheKey != "" && resp.StatusCode == http.StatusOK {
		s.mu.Lock()
		if s.cache == nil {
			s.cache = map[string]cachedShellEntry{}
		}
		s.cache[cacheKey] = cachedShellEntry{
			sessionID: actor.SessionID, sub: actor.Sub, tenantID: actor.TenantID,
			status: resp.StatusCode, contentType: ct, etag: etag, body: data,
			freshAt: now.Add(shellCacheTTL), lkgUntil: now.Add(shellLKGTTL),
		}
		s.mu.Unlock()
	}
	s.writeShellResponse(w, resp.StatusCode, ct, etag, data)
}

// ShellContext handles GET /phoenix/shell-context.
func (s *ShellService) ShellContext(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.actor(w, r)
	if !ok {
		return
	}
	if s.UnreadPusher != nil {
		s.UnreadPusher.UnstopSID(actor.SID)
	}
	s.proxy(w, r, "/internal/ui/shell-context", AudienceShellContext, actor, nil, nil, actor.SessionID+":"+actor.TenantID+":context")
}

// ShellNavigation handles GET /phoenix/shell-navigation (If-None-Match is relayed).
func (s *ShellService) ShellNavigation(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.actor(w, r)
	if !ok {
		return
	}
	h := http.Header{}
	if inm := r.Header.Get("If-None-Match"); inm != "" && len(inm) < 256 {
		h.Set("If-None-Match", inm)
	}
	s.proxy(w, r, "/internal/ui/shell-navigation", AudienceShellNav, actor, nil, h, actor.SessionID+":"+actor.TenantID+":nav")
}

// UIPreferences handles PATCH /phoenix/ui-preferences. It needs the native CSRF token, a same-origin
// Origin, a JSON body and carries only mode/density; If-Match is relayed so a stale version stays a 409.
func (s *ShellService) UIPreferences(w http.ResponseWriter, r *http.Request) {
	actor, csrf, ok := s.actor(w, r)
	if !ok {
		return
	}
	if got := r.Header.Get("X-CSRF-Token"); csrf == "" || subtle.ConstantTimeCompare([]byte(got), []byte(csrf)) != 1 {
		WriteError(w, newErr(http.StatusForbidden, CodeAccessDenied, "csrf token required"), s.Audit)
		return
	}
	if origin := r.Header.Get("Origin"); origin == "" || origin != s.SiteOrigin {
		WriteError(w, newErr(http.StatusForbidden, CodeAccessDenied, "origin not allowed"), s.Audit)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		WriteError(w, newErr(http.StatusUnsupportedMediaType, CodeInvalidRequest, "content type must be application/json"), s.Audit)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxShellBody)
	var in struct {
		Mode    *string `json:"mode"`
		Density *string `json:"density"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, newErr(http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "body too large"), s.Audit)
			return
		}
		WriteError(w, newErr(http.StatusBadRequest, CodeInvalidRequest, "malformed JSON body"), s.Audit)
		return
	}
	pref := map[string]any{}
	if in.Mode != nil {
		pref["mode"] = *in.Mode
	}
	if in.Density != nil {
		pref["density"] = *in.Density
	}
	if len(pref) == 0 {
		WriteError(w, newErr(http.StatusBadRequest, CodeInvalidRequest, "mode or density required"), s.Audit)
		return
	}
	h := http.Header{}
	if im := r.Header.Get("If-Match"); im != "" && len(im) < 128 {
		h.Set("If-Match", im)
	}
	s.InvalidateSession(actor.SessionID)
	s.proxy(w, r, "/internal/ui/preferences", AudienceUIPreferences, actor, map[string]any{"preference": pref}, h, "")
}

// UnreadCount handles GET /phoenix/unread-count using the exact same authoritative counter
// as UnreadPusher so the bell badge and the Gateway-pushed count always agree.
// Per R5 revised, periodic polls of GET /phoenix/unread-count must NOT un-stop a stopped sid.
func (s *ShellService) UnreadCount(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.actor(w, r)
	if !ok {
		return
	}
	if s.UnreadCounter == nil {
		s.writeShellResponse(w, http.StatusOK, "application/json; charset=utf-8", "", []byte(`{"count":0,"status":"unavailable"}`))
		return
	}
	n, err := s.UnreadCounter.UnreadBadgeCount(r.Context(), actor.UserID)
	if err != nil {
		s.writeShellResponse(w, http.StatusOK, "application/json; charset=utf-8", "", []byte(`{"count":0,"status":"unavailable"}`))
		return
	}
	body, _ := json.Marshal(map[string]any{"count": clampUnreadCount(n), "status": "ready"})
	s.writeShellResponse(w, http.StatusOK, "application/json; charset=utf-8", "", body)
}

// Register mounts the shell routes.
func (s *ShellService) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle("/phoenix/shell-context", http.HandlerFunc(s.ShellContext), http.MethodGet)
	handle("/phoenix/shell-navigation", http.HandlerFunc(s.ShellNavigation), http.MethodGet)
	handle("/phoenix/ui-preferences", http.HandlerFunc(s.UIPreferences), http.MethodPatch)
	handle("/phoenix/unread-count", http.HandlerFunc(s.UnreadCount), http.MethodGet)
}
