// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BridgeAudience values are operation specific (contracts C2) and distinct from OIDC RS256.
const (
	AudienceChatAccess     = "gateway:internal:chat-access"
	AudienceShellContext   = "gateway:internal:ui-shell-context"
	AudienceShellNav       = "gateway:internal:ui-shell-navigation"
	AudienceUIPreferences  = "gateway:internal:ui-preferences"
	ScopeChatAccessContext = "chat:access-context"
	BridgeAppCode          = "chat"
	assertionTTL           = 45 * time.Second
)

// BridgeSigner mints the HMAC-SHA256 assertions of the Gateway UI bridge. They are keyed by the
// registered "chat" app secret and bound to one audience; they never reuse the OIDC RS256 path.
type BridgeSigner struct {
	Issuer string
	Secret []byte
	Now    func() time.Time
}

func (b *BridgeSigner) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func randomID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func b64(v []byte) string { return base64.RawURLEncoding.EncodeToString(v) }

// Sign returns b64url(header).b64url(payload).b64url(hmac). sub and tenantID come from the
// immutable session binding, never from request input.
func (b *BridgeSigner) Sign(audience, scope, sub, tenantID, sid string) string {
	now := b.now()
	claims := map[string]any{
		"iss": b.Issuer, "sub": sub, "tenant_id": tenantID, "sid": sid, "app": BridgeAppCode,
		"aud": audience, "iat": now.Unix(), "exp": now.Add(assertionTTL).Unix(), "jti": randomID(),
	}
	if scope != "" {
		claims["scope"] = scope
	}
	head, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := b64(head) + "." + b64(body)
	mac := hmac.New(sha256.New, b.Secret)
	mac.Write([]byte(signed))
	return signed + "." + b64(mac.Sum(nil))
}

func httpClientWithCA(caFile string, timeout time.Duration) (*http.Client, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificate in CA bundle")
		}
		cfg.RootCAs = pool
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: cfg}}, nil
}

// AuthorityDecision is the Gateway answer for one (sub, sid, tenant) (contracts C2).
type AuthorityDecision struct {
	Active          bool      `json:"active"`
	ChatEntitled    bool      `json:"chatEntitled"`
	MembershipState string    `json:"membershipState"`
	TenantState     string    `json:"tenantState"`
	AccessVersion   int64     `json:"accessVersion"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// AuthorityClient calls the Gateway POST /internal/chat/access-context.
type AuthorityClient struct {
	URL    string
	Signer *BridgeSigner
	Issuer string // principal issuer reported in the request body (the Gateway OIDC issuer)
	HTTP   *http.Client
}

// ErrAuthorityUnavailable is returned when the Gateway cannot answer; it closes access.
var ErrAuthorityUnavailable = newErr(http.StatusServiceUnavailable, CodeAuthorityDown, "authority unavailable")

// AccessContext asks the Gateway; transport errors, non-200 answers and malformed bodies all
// mean "cannot tell", which callers must treat as no access (fail closed).
func (c *AuthorityClient) AccessContext(ctx context.Context, sub, sid, tenantID string) (AuthorityDecision, error) {
	// Leave room for the positive cache and idle WS tick inside the 30s
	// revocation bound, even when the shared Gateway stops answering.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	assertion := c.Signer.Sign(AudienceChatAccess, ScopeChatAccessContext, sub, tenantID, sid)
	body, _ := json.Marshal(map[string]string{"issuer": c.Issuer, "sub": sub, "sid": sid, "tenantId": tenantID, "assertion": assertion})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/internal/chat/access-context", strings.NewReader(string(body)))
	if err != nil {
		return AuthorityDecision{}, ErrAuthorityUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return AuthorityDecision{}, wrapErr(http.StatusServiceUnavailable, CodeAuthorityDown, "authority unreachable", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return AuthorityDecision{}, newErr(http.StatusServiceUnavailable, CodeAuthorityDown, "authority returned an error")
	}
	var d AuthorityDecision
	if err := json.Unmarshal(data, &d); err != nil {
		return AuthorityDecision{}, newErr(http.StatusServiceUnavailable, CodeAuthorityDown, "authority response malformed")
	}
	return d, nil
}

// AuthorityMaxAge bounds a positive decision (contracts C2: at most 30 s). The bound is
// 18s + 5s background tick + 5s refresh deadline stays within 30s even
// without a frame or invalidation, including a concurrent HTTP/WS refresh.
const AuthorityMaxAge = 18 * time.Second

type authorityEntry struct {
	tenantID string
	version  int64
	decision AuthorityDecision
	fetched  time.Time // carries a monotonic reading; ages are measured with time.Since
	err      *Error
}

// Authority is the live access cache. Entries are per native session and tenant; one session
// never reads another's entry. Age uses the monotonic clock, so wall-clock jumps cannot extend a grant.
type Authority struct {
	Client *AuthorityClient
	// Audit receives bounded, aggregate numeric telemetry only. No actor identity
	// or assertion is retained in these process-wide operational measurements.
	Audit func(event string, fields map[string]string)
	// OnInactive is called when the Gateway says the Gateway session is gone (revoked logout).
	OnInactive func(ctx context.Context, sessionID string)
	MaxAge     time.Duration

	mu        sync.Mutex
	entries   map[string]authorityEntry
	locks     keyedMutex
	metricsMu sync.Mutex
	metrics   authorityMetrics
}

type authorityMetrics struct {
	last                                                 time.Time
	checks, refreshes, hits, failures, unavailable       int64
	durationMicros, maxDurationMicros, maxCacheAgeMillis int64
}

func (a *Authority) observe(start time.Time, cached bool, age time.Duration, result error) {
	if a.Audit == nil {
		return
	}
	now := time.Now()
	a.metricsMu.Lock()
	m := &a.metrics
	m.checks++
	if cached {
		m.hits++
	} else {
		m.refreshes++
	}
	if result != nil {
		m.failures++
		var pe *Error
		if errors.As(result, &pe) && pe.Code == CodeAuthorityDown {
			m.unavailable++
		}
	}
	elapsed := now.Sub(start).Microseconds()
	m.durationMicros += elapsed
	if elapsed > m.maxDurationMicros {
		m.maxDurationMicros = elapsed
	}
	if age.Milliseconds() > m.maxCacheAgeMillis {
		m.maxCacheAgeMillis = age.Milliseconds()
	}
	if !m.last.IsZero() && now.Sub(m.last) < 5*time.Second {
		a.metricsMu.Unlock()
		return
	}
	m.last = now
	fields := map[string]string{
		"checks_total":                strconv.FormatInt(m.checks, 10),
		"refreshes_total":             strconv.FormatInt(m.refreshes, 10),
		"cache_hits_total":            strconv.FormatInt(m.hits, 10),
		"failures_total":              strconv.FormatInt(m.failures, 10),
		"unavailable_total":           strconv.FormatInt(m.unavailable, 10),
		"duration_microseconds_total": strconv.FormatInt(m.durationMicros, 10),
		"max_duration_microseconds":   strconv.FormatInt(m.maxDurationMicros, 10),
		"max_cache_age_milliseconds":  strconv.FormatInt(m.maxCacheAgeMillis, 10),
	}
	a.metricsMu.Unlock()
	a.Audit("phoenix.authority.metrics", fields)
}

func NewAuthority(c *AuthorityClient) *Authority {
	return &Authority{Client: c, MaxAge: AuthorityMaxAge, entries: map[string]authorityEntry{}}
}

func (a *Authority) freshFor(e authorityEntry) time.Duration {
	age := time.Since(e.fetched)
	remaining := a.MaxAge - age
	if !e.decision.ExpiresAt.IsZero() {
		if until := time.Until(e.decision.ExpiresAt); until < remaining {
			remaining = until
		}
	}
	return remaining
}

// ValidFor reports how long the cached decision for a session can still be used (0 if none).
func (a *Authority) ValidFor(sessionID string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.entries[sessionID]; ok {
		if d := a.freshFor(e); d > 0 {
			return d
		}
	}
	return 0
}

// Invalidate drops the entries of one session (push invalidation; never the only mechanism).
func (a *Authority) Invalidate(sessionID string) {
	a.mu.Lock()
	delete(a.entries, sessionID)
	a.mu.Unlock()
}

// Check validates a bound session against the Gateway, using the cache while it is fresh.
func (a *Authority) Check(ctx context.Context, actor ActorContext) (result error) {
	started := time.Now()
	cached, age := false, time.Duration(0)
	defer func() { a.observe(started, cached, age, result) }()
	unlock := a.locks.Lock(actor.SessionID)
	defer unlock()

	a.mu.Lock()
	e, ok := a.entries[actor.SessionID]
	a.mu.Unlock()
	if ok && e.tenantID == actor.TenantID && e.version == actor.AccessVersion && a.freshFor(e) > 0 {
		cached, age = true, time.Since(e.fetched)
		return decisionError(e)
	}

	d, err := a.Client.AccessContext(ctx, actor.Sub, actor.SID, actor.TenantID)
	entry := authorityEntry{tenantID: actor.TenantID, version: actor.AccessVersion, decision: d, fetched: time.Now()}
	if err != nil {
		var pe *Error
		if !errors.As(err, &pe) {
			pe = ErrAuthorityUnavailable
		}
		// A short unavailable result is fail-closed, never a positive grant. It
		// prevents queued HTTP/WS callers from each waiting another timeout.
		entry.err = pe
		entry.decision.ExpiresAt = time.Now().Add(2 * time.Second)
		a.mu.Lock()
		a.entries[actor.SessionID] = entry
		a.mu.Unlock()
		return pe
	}
	if d.Active && (d.ExpiresAt.IsZero() || !d.ExpiresAt.After(time.Now())) {
		return ErrAuthorityUnavailable
	}
	switch {
	case !d.Active:
		entry.err = newErr(http.StatusUnauthorized, CodeAuthInvalid, "gateway session is not active")
		entry.decision.ExpiresAt = time.Now().Add(2 * time.Second) // short negative cache only
	case !d.ChatEntitled || d.MembershipState != "ACTIVE" || d.TenantState != "ACTIVE":
		entry.err = ErrAccessDenied
		entry.decision.ExpiresAt = time.Now().Add(2 * time.Second)
	}
	a.mu.Lock()
	a.entries[actor.SessionID] = entry
	a.mu.Unlock()
	if !d.Active && a.OnInactive != nil {
		a.OnInactive(ctx, actor.SessionID)
	}
	return decisionError(entry)
}

func decisionError(e authorityEntry) error {
	if e.err != nil {
		return e.err
	}
	return nil
}

// NewHTTPClient builds the outbound HTTPS client (optional extra CA, bounded timeout).
func NewHTTPClient(caFile string, timeout time.Duration) (*http.Client, error) {
	return httpClientWithCA(caFile, timeout)
}
