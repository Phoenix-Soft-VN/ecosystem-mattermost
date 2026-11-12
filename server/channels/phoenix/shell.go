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
	"time"
)

// SessionLookup resolves the native session of a request (token from the native cookie only) and
// its CSRF token. It is implemented by the app layer.
type SessionLookup interface {
	// SessionFromRequest returns (sessionID, userID, csrfToken, ok).
	SessionFromRequest(ctx context.Context, r *http.Request) (string, string, string, bool)
}

// ShellService is the native BFF of design §6.7. The browser never supplies tenant, sub or sid:
// they come from the immutable session binding, and every Gateway call uses a fresh HMAC assertion
// bound to one operation audience. Upstream 401/403/409 are preserved, never wrapped as 502.
type ShellService struct {
	Policy     *Policy
	Sessions   SessionLookup
	Signer     *BridgeSigner
	GatewayURL string
	SiteOrigin string
	LogoutURL  string // Gateway global logout entry (public URL), advertised to the shell
	HTTP       *http.Client
	Audit      func(event string, fields map[string]string)
}

const maxShellBody = 4096

func (s *ShellService) actor(w http.ResponseWriter, r *http.Request) (ActorContext, string, bool) {
	sid, uid, csrf, ok := s.Sessions.SessionFromRequest(r.Context(), r)
	if !ok {
		WriteError(w, ErrNoActor, s.Audit)
		return ActorContext{}, "", false
	}
	actor, err := s.Policy.ResolveActor(r.Context(), sid, uid)
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

// proxy posts {appCode, assertion, ...extra} to a Gateway bridge route and relays status and body.
func (s *ShellService) proxy(w http.ResponseWriter, r *http.Request, path, audience string, actor ActorContext, extra map[string]any, header http.Header) {
	payload := map[string]any{"appCode": BridgeAppCode, "assertion": s.Signer.Sign(audience, "", actor.Sub, actor.TenantID, actor.SID)}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.GatewayURL+path, bytes.NewReader(body))
	if err != nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeAuthorityDown, "shell unavailable"), s.Audit)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		WriteError(w, wrapErr(http.StatusServiceUnavailable, CodeAuthorityDown, "shell unavailable", err), s.Audit)
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	w.Header().Set("Cache-Control", "no-store")
	if s.LogoutURL != "" {
		w.Header().Set("X-Phoenix-Logout-Url", s.LogoutURL)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	if et := resp.Header.Get("ETag"); et != "" {
		w.Header().Set("ETag", et)
	}
	w.WriteHeader(resp.StatusCode) // 200/304/401/403/409 pass through unchanged
	_, _ = w.Write(data)
}

// ShellContext handles GET /phoenix/shell-context.
func (s *ShellService) ShellContext(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.actor(w, r)
	if !ok {
		return
	}
	s.proxy(w, r, "/internal/ui/shell-context", AudienceShellContext, actor, nil, nil)
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
	s.proxy(w, r, "/internal/ui/shell-navigation", AudienceShellNav, actor, nil, h)
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
	dec.DisallowUnknownFields() // tenant, sub, sid and anything else from the browser is refused
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
	s.proxy(w, r, "/internal/ui/preferences", AudienceUIPreferences, actor, map[string]any{"preference": pref}, h)
}

// Register mounts the shell routes.
func (s *ShellService) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle("/phoenix/shell-context", http.HandlerFunc(s.ShellContext), http.MethodGet)
	handle("/phoenix/shell-navigation", http.HandlerFunc(s.ShellNavigation), http.MethodGet)
	handle("/phoenix/ui-preferences", http.HandlerFunc(s.UIPreferences), http.MethodPatch)
}
