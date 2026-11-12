// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ReadyCheck is one named readiness condition (schema, settings, policy...).
type ReadyCheck struct {
	Name string
	Fn   func(ctx context.Context) error
}

// API serves the authenticated internal surface /phoenix/internal/v1/* and /phoenix/ready.
type API struct {
	Service  *Service
	Verifier *CallerVerifier
	Limiter  *Limiter // per verified caller
	IPLimit  *Limiter // failed authentication per remote address
	Checks   []ReadyCheck
	Notify   NotifyOps // native operations used by the notification endpoint
	Audit    func(event string, fields map[string]string)
}

func NewAPI(svc *Service, v *CallerVerifier) *API {
	return &API{
		Service: svc, Verifier: v,
		Limiter: NewLimiter(InternalRatePerMin, time.Minute, nil),
		IPLimit: NewLimiter(InternalRatePerMin, time.Minute, nil),
	}
}

// Register mounts the handlers on any mux-like registrar.
func (a *API) Register(handle func(path string, h http.Handler, methods ...string)) {
	handle("/phoenix/internal/v1/operations", http.HandlerFunc(a.operations), http.MethodPost)
	handle("/phoenix/internal/v1/evidence", http.HandlerFunc(a.evidence), http.MethodPost)
	handle("/phoenix/internal/v1/notifications", http.HandlerFunc(a.notifications), http.MethodPost)
	handle("/phoenix/ready", http.HandlerFunc(a.ready), http.MethodGet)
}

func correlationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError renders a Phoenix error; internal causes never reach the response body.
func WriteError(w http.ResponseWriter, err error, audit func(string, map[string]string)) {
	var pe *Error
	cid := correlationID()
	if !errors.As(err, &pe) {
		pe = newErr(http.StatusInternalServerError, CodeInternal, "internal error")
		pe.Cause = err
	}
	if audit != nil {
		cause := ""
		if pe.Cause != nil {
			cause = pe.Cause.Error()
		}
		audit("phoenix.error", map[string]string{"code": pe.Code, "status": strconv.Itoa(pe.Status), "correlationId": cid, "cause": cause})
	}
	if pe.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, pe.Status, map[string]string{"code": pe.Code, "message": pe.Message, "correlationId": cid})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authenticate verifies the bearer, enforces the exact scope and the per-caller rate limit.
func (a *API) authenticate(w http.ResponseWriter, r *http.Request, scope string) (*Caller, bool) {
	if a.Verifier == nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "internal API is not configured"), a.Audit)
		return nil, false
	}
	if ok, wait := a.IPLimit.peek(remoteIP(r)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		WriteError(w, newErr(http.StatusTooManyRequests, CodeRateLimited, "too many failed requests"), a.Audit)
		return nil, false
	}
	caller, verr := a.Verifier.VerifyBearer(r.Context(), r.Header.Get("Authorization"))
	if verr != nil {
		a.IPLimit.Allow(remoteIP(r)) // only failures consume the IP budget
		WriteError(w, verr, a.Audit)
		return nil, false
	}
	if !caller.Scopes[scope] {
		WriteError(w, newErr(http.StatusForbidden, CodeScopeDenied, "caller lacks required scope"), a.Audit)
		return nil, false
	}
	if ok, wait := a.Limiter.Allow(caller.ID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		WriteError(w, newErr(http.StatusTooManyRequests, CodeRateLimited, "rate limit exceeded"), a.Audit)
		return nil, false
	}
	return caller, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) *Error {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "application/json") {
		return newErr(http.StatusUnsupportedMediaType, CodeInvalidRequest, "content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxInternalBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return newErr(http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "request body exceeds 64 KiB")
		}
		return newErr(http.StatusBadRequest, CodeInvalidRequest, "malformed JSON body")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return newErr(http.StatusBadRequest, CodeInvalidRequest, "trailing data after JSON body")
	}
	return nil
}

func (a *API) operations(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authenticate(w, r, ScopeProvision)
	if !ok {
		return
	}
	var req OperationRequest
	if perr := decodeBody(w, r, &req); perr != nil {
		WriteError(w, perr, a.Audit)
		return
	}
	req.Caller = caller.ID // never from the body
	ev, err := a.Service.Provision(r.Context(), req)
	if err != nil {
		WriteError(w, err, a.Audit)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

func (a *API) notifications(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authenticate(w, r, ScopeNotify)
	if !ok {
		return
	}
	if a.Notify == nil {
		WriteError(w, newErr(http.StatusServiceUnavailable, CodeNotReady, "notifications are not configured"), a.Audit)
		return
	}
	var req NotifyRequest
	if perr := decodeBody(w, r, &req); perr != nil {
		WriteError(w, perr, a.Audit)
		return
	}
	req.Caller = caller.ID
	out, err := a.Service.Notify(r.Context(), a.Notify, req)
	if err != nil {
		WriteError(w, err, a.Audit)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) evidence(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r, ScopeRead); !ok {
		return
	}
	var req EvidenceRequest
	if perr := decodeBody(w, r, &req); perr != nil {
		WriteError(w, perr, a.Audit)
		return
	}
	ev, err := a.Service.ReadEvidence(r.Context(), req)
	if err != nil {
		WriteError(w, err, a.Audit)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

// ready is the readiness endpoint: 200 only when every check passes; it does not
// reveal internals beyond the check names and a pass/fail flag.
func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	results := map[string]string{}
	ok := true
	for _, c := range a.Checks {
		if err := c.Fn(ctx); err != nil {
			ok = false
			results[c.Name] = "fail"
			if a.Audit != nil {
				a.Audit("phoenix.ready.fail", map[string]string{"check": c.Name, "cause": err.Error()})
			}
		} else {
			results[c.Name] = "ok"
		}
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ready": ok, "checks": results, "enforced": Enforced(),
		"artifactRevision": a.Service.revision, "artifactBuildNumber": a.Service.build,
		"policyVersion": CurrentPolicyVersion,
	})
}

// peek reports whether key still has budget without consuming it.
func (l *Limiter) peek(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	now := l.now()
	if b == nil || now.Sub(b.start) >= l.window || b.count < l.limit {
		return true, 0
	}
	wait := int(b.start.Add(l.window).Sub(now).Seconds()) + 1
	if wait < 1 {
		wait = 1
	}
	return false, wait
}
