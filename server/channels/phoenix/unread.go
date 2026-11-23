// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	unreadBridgePath       = "/internal/ui/unread-count"
	unreadMaxCount         = 9999
	unreadDefaultDeepLink  = "/"
	unreadDefaultThrottle  = 30 * time.Second
	unreadDefaultStopTTL   = 15 * time.Minute
	unreadDefaultWorkers   = 4
	unreadMaxWorkers       = 4
	unreadDefaultDeadline  = 4 * time.Minute
	unreadDefaultInterval  = 5 * time.Minute
	unreadDefaultTimeout   = 8 * time.Second
)

// UnreadCounter computes the authoritative unread badge count for a native user
// (mention + DM + CRT when enabled). Shared by UnreadPusher and GET /phoenix/unread-count.
type UnreadCounter interface {
	UnreadBadgeCount(ctx context.Context, userID string) (int, error)
}

// UnreadBindingStore provides live non-operator session bindings for unread pushes.
type UnreadBindingStore interface {
	LiveSessionBindingsOfUser(ctx context.Context, nativeUserID string) ([]SessionBinding, error)
	LiveSessionBindings(ctx context.Context) ([]SessionBinding, error)
	SessionBindingByID(ctx context.Context, sessionID string) (SessionBinding, error)
}

type tenantSubKey struct {
	tenantID string
	sub      string
}

type userPushState struct {
	inFlight bool
	dirty    bool
	timer    *time.Timer
}

// UnreadPusher pushes authoritative unread counts to Gateway POST /internal/ui/unread-count.
// All event-triggered pushes are asynchronous, coalesced per user, and throttled to at most
// one push per 30 s per (tenant, sub) with a trailing recomputed latest value.
type UnreadPusher struct {
	Store           UnreadBindingStore
	Counter         UnreadCounter
	Signer          *BridgeSigner
	GatewayURL      string
	HTTP            *http.Client
	Now             func() time.Time
	Throttle        time.Duration
	StopTTL         time.Duration
	RefreshWorkers  int
	RefreshDeadline time.Duration
	RequestTimeout  time.Duration
	DeepLink        string

	mu              sync.Mutex
	users           map[string]*userPushState
	lastPush        map[tenantSubKey]time.Time
	stopped         map[string]time.Time
	closed          bool
	refreshInFlight atomic.Bool
}

var (
	defaultUnreadMu     sync.RWMutex
	defaultUnreadPusher *UnreadPusher
)

// SetDefaultUnreadPusher installs the process-wide unread pusher.
func SetDefaultUnreadPusher(p *UnreadPusher) {
	defaultUnreadMu.Lock()
	defaultUnreadPusher = p
	defaultUnreadMu.Unlock()
}

// DefaultUnreadPusher returns the installed unread pusher, or nil if not configured.
func DefaultUnreadPusher() *UnreadPusher {
	defaultUnreadMu.RLock()
	defer defaultUnreadMu.RUnlock()
	return defaultUnreadPusher
}

// NotifyUnread triggers an asynchronous unread count push for userID when a pusher is installed.
func NotifyUnread(userID string) {
	if p := DefaultUnreadPusher(); p != nil {
		p.NotifyUser(userID)
	}
}

// NotifyUnreadActivity marks user-initiated session activity (un-stopping that session's sid
// if previously stopped) and schedules an asynchronous unread count push.
func NotifyUnreadActivity(userID, sessionID string) {
	if p := DefaultUnreadPusher(); p != nil {
		if sessionID != "" {
			go p.UnstopSession(context.Background(), sessionID)
		}
		p.NotifyUser(userID)
	}
}

// NewUnreadPusher builds an UnreadPusher with the contract defaults.
func NewUnreadPusher(store UnreadBindingStore, counter UnreadCounter, signer *BridgeSigner, gatewayURL string, httpClient *http.Client) *UnreadPusher {
	return &UnreadPusher{
		Store:           store,
		Counter:         counter,
		Signer:          signer,
		GatewayURL:      strings.TrimRight(gatewayURL, "/"),
		HTTP:            httpClient,
		Throttle:        unreadDefaultThrottle,
		StopTTL:         unreadDefaultStopTTL,
		RefreshWorkers:  unreadDefaultWorkers,
		RefreshDeadline: unreadDefaultDeadline,
		RequestTimeout:  unreadDefaultTimeout,
		DeepLink:        unreadDefaultDeepLink,
		users:           map[string]*userPushState{},
		lastPush:        map[tenantSubKey]time.Time{},
		stopped:         map[string]time.Time{},
	}
}

func (p *UnreadPusher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *UnreadPusher) throttle() time.Duration {
	if p.Throttle > 0 {
		return p.Throttle
	}
	return unreadDefaultThrottle
}

func (p *UnreadPusher) stopTTL() time.Duration {
	if p.StopTTL > 0 {
		return p.StopTTL
	}
	return unreadDefaultStopTTL
}

func (p *UnreadPusher) refreshWorkers() int {
	w := p.RefreshWorkers
	if w <= 0 {
		w = unreadDefaultWorkers
	}
	if w > unreadMaxWorkers {
		w = unreadMaxWorkers
	}
	return w
}

func (p *UnreadPusher) refreshDeadline() time.Duration {
	d := p.RefreshDeadline
	if d <= 0 {
		d = unreadDefaultDeadline
	}
	if d >= unreadDefaultInterval {
		d = unreadDefaultDeadline
	}
	return d
}

func (p *UnreadPusher) requestTimeout() time.Duration {
	if p.RequestTimeout > 0 {
		return p.RequestTimeout
	}
	return unreadDefaultTimeout
}

func (p *UnreadPusher) deepLink() string {
	if strings.HasPrefix(p.DeepLink, "/") {
		return p.DeepLink
	}
	return unreadDefaultDeepLink
}

func clampUnreadCount(n int) int {
	if n < 0 {
		return 0
	}
	if n > unreadMaxCount {
		return unreadMaxCount
	}
	return n
}

func (p *UnreadPusher) ensureMapsLocked() {
	if p.users == nil {
		p.users = map[string]*userPushState{}
	}
	if p.lastPush == nil {
		p.lastPush = map[tenantSubKey]time.Time{}
	}
	if p.stopped == nil {
		p.stopped = map[string]time.Time{}
	}
}

func (p *UnreadPusher) sweepStoppedLocked(now time.Time) {
	for sid, exp := range p.stopped {
		if !now.Before(exp) {
			delete(p.stopped, sid)
		}
	}
}

func (p *UnreadPusher) isStopped(sid string) bool {
	if sid == "" {
		return true
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepStoppedLocked(now)
	_, ok := p.stopped[sid]
	return ok
}

func (p *UnreadPusher) stopSID(sid string) {
	if sid == "" {
		return
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureMapsLocked()
	p.sweepStoppedLocked(now)
	p.stopped[sid] = now.Add(p.stopTTL())
}

// UnstopSID clears a stopped sid on user-initiated activity (login, mark-read, shell request).
func (p *UnreadPusher) UnstopSID(sid string) {
	if p == nil || sid == "" {
		return
	}
	p.mu.Lock()
	delete(p.stopped, sid)
	p.mu.Unlock()
}

// UnstopSession resolves a native session ID to its Gateway sid and un-stops it.
func (p *UnreadPusher) UnstopSession(ctx context.Context, sessionID string) {
	if p == nil || p.Store == nil || sessionID == "" {
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := p.Store.SessionBindingByID(lookupCtx, sessionID)
	if err == nil && b.SID != "" {
		p.UnstopSID(b.SID)
	}
}

// Close cancels pending trailing timers.
func (p *UnreadPusher) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	for _, st := range p.users {
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
	}
	p.mu.Unlock()
}

// NotifyUser schedules an asynchronous unread push for userID. Multiple calls for the same user
// coalesce and obey the per-(tenant, sub) 30 s throttle with a trailing recomputed push.
func (p *UnreadPusher) NotifyUser(userID string) {
	if p == nil || userID == "" || p.Store == nil || p.Counter == nil || p.Signer == nil || p.GatewayURL == "" || p.HTTP == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.ensureMapsLocked()
	st, ok := p.users[userID]
	if !ok {
		st = &userPushState{}
		p.users[userID] = st
	}
	if st.inFlight || st.timer != nil {
		st.dirty = true
		p.mu.Unlock()
		return
	}
	st.inFlight = true
	st.dirty = false
	p.mu.Unlock()

	go p.runUserPush(userID)
}

func (p *UnreadPusher) runUserPush(userID string) {
	wait := p.pushUserNow(context.Background(), userID, true)

	p.mu.Lock()
	st := p.users[userID]
	if st == nil || p.closed {
		p.mu.Unlock()
		return
	}
	st.inFlight = false
	needTrailing := st.dirty || wait > 0
	if !needTrailing {
		delete(p.users, userID)
		p.mu.Unlock()
		return
	}
	delay := wait
	if delay <= 0 {
		delay = p.throttle()
	}
	st.dirty = false
	st.timer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		cur := p.users[userID]
		if cur == nil || p.closed {
			p.mu.Unlock()
			return
		}
		cur.timer = nil
		if cur.inFlight {
			cur.dirty = true
			p.mu.Unlock()
			return
		}
		cur.inFlight = true
		p.mu.Unlock()
		p.runUserPush(userID)
	})
	p.mu.Unlock()
}

// pushUserNow recomputes the user's unread badge count and pushes to each live (tenant, sub).
// When respectThrottle is true, any (tenant, sub) still within its throttle window is skipped
// and the shortest remaining wait duration is returned so the caller schedules a single trailing push.
func (p *UnreadPusher) pushUserNow(ctx context.Context, userID string, respectThrottle bool) time.Duration {
	bindings, err := p.Store.LiveSessionBindingsOfUser(ctx, userID)
	if err != nil || len(bindings) == 0 {
		return 0
	}
	return p.pushUserBindings(ctx, userID, bindings, respectThrottle)
}

func (p *UnreadPusher) pushUserBindings(ctx context.Context, userID string, bindings []SessionBinding, respectThrottle bool) time.Duration {
	// Group live, non-stopped bindings by (tenant, sub).
	byKey := map[tenantSubKey][]SessionBinding{}
	order := make([]tenantSubKey, 0, len(bindings))
	for _, b := range bindings {
		if b.TenantID == "" || b.Sub == "" || b.SID == "" {
			continue
		}
		if p.isStopped(b.SID) {
			continue
		}
		k := tenantSubKey{tenantID: b.TenantID, sub: b.Sub}
		if _, exists := byKey[k]; !exists {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], b)
	}
	if len(order) == 0 {
		return 0
	}

	// Check throttle per (tenant, sub) before computing count.
	now := p.now()
	throttle := p.throttle()
	var due []tenantSubKey
	var minWait time.Duration
	if respectThrottle {
		p.mu.Lock()
		for _, k := range order {
			if last, ok := p.lastPush[k]; ok {
				if rem := throttle - now.Sub(last); rem > 0 {
					if minWait == 0 || rem < minWait {
						minWait = rem
					}
					continue
				}
			}
			due = append(due, k)
		}
		p.mu.Unlock()
	} else {
		due = order
	}
	if len(due) == 0 {
		return minWait
	}

	// Recompute authoritative count from source of truth once for this user.
	count, status := 0, "ready"
	if n, cerr := p.Counter.UnreadBadgeCount(ctx, userID); cerr != nil {
		count, status = 0, "unavailable"
	} else {
		count = clampUnreadCount(n)
	}

	for _, k := range due {
		if ctx.Err() != nil {
			return minWait
		}
		pushed := false
		for _, b := range byKey[k] {
			if p.isStopped(b.SID) {
				continue
			}
			if p.pushOneBinding(ctx, b, count, status) {
				pushed = true
				break
			}
		}
		if pushed {
			p.mu.Lock()
			p.ensureMapsLocked()
			p.lastPush[k] = p.now()
			p.mu.Unlock()
		}
	}
	return minWait
}

func gatewayErrorCode(data []byte) string {
	var parsed struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return ""
	}
	if parsed.Code != "" {
		return parsed.Code
	}
	return parsed.Error.Code
}

func isStopSIDError(statusCode int, data []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	code := gatewayErrorCode(data)
	return strings.EqualFold(code, "SESSION_ENDED") || strings.EqualFold(code, "ACCESS_REVOKED")
}

// pushOneBinding posts a single unread update for one session binding.
// Returns true if the push completed (200) or should not try alternate sids for this tick.
func (p *UnreadPusher) pushOneBinding(ctx context.Context, b SessionBinding, count int, status string) bool {
	extra := map[string]any{
		"count":    count,
		"status":   status,
		"deepLink": p.deepLink(),
	}
	timeout := p.requestTimeout()
	resp, data, err := postBridge(ctx, p.HTTP, p.GatewayURL, unreadBridgePath, p.Signer, AudienceUnread, b.Sub, b.TenantID, b.SID, extra, nil, timeout)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && isAssertionInvalidBody(data) {
		// Retry exactly once with a freshly signed assertion (new jti).
		// A second 401 ASSERTION_INVALID does NOT stop the sid.
		resp, data, err = postBridge(ctx, p.HTTP, p.GatewayURL, unreadBridgePath, p.Signer, AudienceUnread, b.Sub, b.TenantID, b.SID, extra, nil, timeout)
	}
	if err != nil || resp == nil {
		return false
	}
	if isStopSIDError(resp.StatusCode, data) {
		p.stopSID(b.SID)
		return false
	}
	return resp.StatusCode == http.StatusOK
}

// StartRefresh launches the bounded periodic refresh loop (default 5 minutes).
func (p *UnreadPusher) StartRefresh(ctx context.Context, interval time.Duration) {
	if p == nil {
		return
	}
	if interval <= 0 {
		interval = unreadDefaultInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.RefreshOnce(ctx)
			}
		}
	}()
}

// RefreshOnce pushes fresh unread counts for all SQL-filtered live, non-operator sessions.
// Guarded against overlapping runs, bounded by a worker pool (<= 4) and an overall deadline (< 5m).
// Returns false if another refresh is already in flight.
func (p *UnreadPusher) RefreshOnce(ctx context.Context) bool {
	if p == nil || p.Store == nil || p.Counter == nil || p.Signer == nil || p.GatewayURL == "" || p.HTTP == nil {
		return false
	}
	if !p.refreshInFlight.CompareAndSwap(false, true) {
		return false
	}
	defer p.refreshInFlight.Store(false)

	runCtx, cancel := context.WithTimeout(ctx, p.refreshDeadline())
	defer cancel()

	bindings, err := p.Store.LiveSessionBindings(runCtx)
	if err != nil || len(bindings) == 0 {
		return true
	}

	type userWork struct {
		userID   string
		bindings []SessionBinding
	}
	byUser := map[string][]SessionBinding{}
	userOrder := make([]string, 0)
	for _, b := range bindings {
		if b.NativeUserID == "" {
			continue
		}
		if _, ok := byUser[b.NativeUserID]; !ok {
			userOrder = append(userOrder, b.NativeUserID)
		}
		byUser[b.NativeUserID] = append(byUser[b.NativeUserID], b)
	}
	if len(userOrder) == 0 {
		return true
	}

	workers := p.refreshWorkers()
	if len(userOrder) < workers {
		workers = len(userOrder)
	}
	jobs := make(chan userWork, len(userOrder))
	for _, uid := range userOrder {
		jobs <- userWork{userID: uid, bindings: byUser[uid]}
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for work := range jobs {
				if runCtx.Err() != nil {
					return
				}
				p.pushUserBindings(runCtx, work.userID, work.bindings, false)
			}
		}()
	}
	wg.Wait()
	return true
}
