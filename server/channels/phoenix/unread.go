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
	unreadBridgePath         = "/internal/ui/unread-count"
	unreadMaxCount           = 9999
	unreadDefaultDeepLink    = "/"
	unreadDefaultThrottle    = 30 * time.Second
	unreadDefaultStopTTL     = 15 * time.Minute
	unreadDefaultNoBindTTL   = 60 * time.Second
	unreadDefaultWorkers     = 4
	unreadMaxWorkers         = 4
	unreadDefaultNotifyQueue = 256
	unreadDefaultDeadline    = 4 * time.Minute
	unreadDefaultInterval    = 5 * time.Minute
	unreadDefaultTimeout     = 8 * time.Second
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

// UnreadNotifier is the seam used by app hooks to trigger asynchronous unread count pushes.
type UnreadNotifier interface {
	NotifyUser(userID string)
	NotifyUserActivity(userID, sessionID string)
}

type tenantSubKey struct {
	tenantID string
	sub      string
}

type userPushState struct {
	queued   bool
	inFlight bool
	dirty    bool
	timer    *time.Timer
}

// UnreadPusher pushes authoritative unread counts to Gateway POST /internal/ui/unread-count.
// Event-triggered pushes are gated in memory before hitting the DB (per-user throttle +
// negative cache for users without live bindings) and dispatched through a bounded worker
// pool (<= 4 workers) so @channel fan-out never spawns unbounded goroutines or DB queries.
//
// Multi-replica (R7): per-process throttles, negative caches, and 5-minute refresh jobs are
// accepted; N replicas may send up to N refresh pushes per interval, and Gateway overwrites
// per (tenant, user, app), so extra pushes are safe.
type UnreadPusher struct {
	Store           UnreadBindingStore
	Counter         UnreadCounter
	Signer          *BridgeSigner
	GatewayURL      string
	HTTP            *http.Client
	Now             func() time.Time
	Throttle        time.Duration
	StopTTL         time.Duration
	NoBindingTTL    time.Duration
	NotifyWorkers   int
	NotifyQueueSize int
	RefreshWorkers  int
	RefreshDeadline time.Duration
	RequestTimeout  time.Duration
	DeepLink        string

	mu              sync.Mutex
	users           map[string]*userPushState
	userLastPush    map[string]time.Time
	lastPush        map[tenantSubKey]time.Time
	noBinding       map[string]time.Time
	stopped         map[string]time.Time
	notifyCh        chan string
	stopCh          chan struct{}
	poolStarted     bool
	closed          bool
	refreshStart    sync.Once
	cancelRefresh   context.CancelFunc
	refreshInFlight atomic.Bool
}

var (
	defaultUnreadMu       sync.RWMutex
	defaultUnreadNotifier UnreadNotifier
	defaultUnreadPusher   *UnreadPusher
)

// SetDefaultUnreadNotifier installs a custom UnreadNotifier (used in tests/wiring).
func SetDefaultUnreadNotifier(n UnreadNotifier) {
	defaultUnreadMu.Lock()
	defaultUnreadNotifier = n
	if p, ok := n.(*UnreadPusher); ok {
		defaultUnreadPusher = p
	} else {
		defaultUnreadPusher = nil
	}
	defaultUnreadMu.Unlock()
}

// SetDefaultUnreadPusher installs the process-wide unread pusher, closing any previous one.
func SetDefaultUnreadPusher(p *UnreadPusher) {
	defaultUnreadMu.Lock()
	prev := defaultUnreadPusher
	defaultUnreadPusher = p
	if p != nil {
		defaultUnreadNotifier = p
	} else {
		defaultUnreadNotifier = nil
	}
	defaultUnreadMu.Unlock()
	if prev != nil && prev != p {
		prev.Close()
	}
}

// DefaultUnreadPusher returns the installed unread pusher, or nil if not configured.
func DefaultUnreadPusher() *UnreadPusher {
	defaultUnreadMu.RLock()
	defer defaultUnreadMu.RUnlock()
	return defaultUnreadPusher
}

// CloseDefaultUnreadPusher stops the installed unread pusher on server shutdown.
func CloseDefaultUnreadPusher() {
	defaultUnreadMu.Lock()
	prev := defaultUnreadPusher
	defaultUnreadPusher = nil
	defaultUnreadNotifier = nil
	defaultUnreadMu.Unlock()
	if prev != nil {
		prev.Close()
	}
}

// NotifyUnread triggers an asynchronous unread count push for userID when a notifier is installed.
func NotifyUnread(userID string) {
	defaultUnreadMu.RLock()
	n := defaultUnreadNotifier
	defaultUnreadMu.RUnlock()
	if n != nil {
		n.NotifyUser(userID)
	}
}

// NotifyUnreadActivity marks user-initiated session activity (un-stopping that session's sid
// only if any sid is currently stopped) and schedules an asynchronous unread count push.
func NotifyUnreadActivity(userID, sessionID string) {
	defaultUnreadMu.RLock()
	n := defaultUnreadNotifier
	defaultUnreadMu.RUnlock()
	if n != nil {
		n.NotifyUserActivity(userID, sessionID)
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
		NoBindingTTL:    unreadDefaultNoBindTTL,
		NotifyWorkers:   unreadDefaultWorkers,
		NotifyQueueSize: unreadDefaultNotifyQueue,
		RefreshWorkers:  unreadDefaultWorkers,
		RefreshDeadline: unreadDefaultDeadline,
		RequestTimeout:  unreadDefaultTimeout,
		DeepLink:        unreadDefaultDeepLink,
		users:           map[string]*userPushState{},
		userLastPush:    map[string]time.Time{},
		lastPush:        map[tenantSubKey]time.Time{},
		noBinding:       map[string]time.Time{},
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

func (p *UnreadPusher) noBindingTTL() time.Duration {
	if p.NoBindingTTL > 0 {
		return p.NoBindingTTL
	}
	return unreadDefaultNoBindTTL
}

func (p *UnreadPusher) notifyWorkers() int {
	w := p.NotifyWorkers
	if w <= 0 {
		w = unreadDefaultWorkers
	}
	if w > unreadMaxWorkers {
		w = unreadMaxWorkers
	}
	return w
}

func (p *UnreadPusher) notifyQueueSize() int {
	q := p.NotifyQueueSize
	if q <= 0 {
		q = unreadDefaultNotifyQueue
	}
	return q
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
	if p.userLastPush == nil {
		p.userLastPush = map[string]time.Time{}
	}
	if p.lastPush == nil {
		p.lastPush = map[tenantSubKey]time.Time{}
	}
	if p.noBinding == nil {
		p.noBinding = map[string]time.Time{}
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

// sweepMapsLocked prunes all TTL-bounded maps (stopped, noBinding, lastPush, userLastPush).
func (p *UnreadPusher) sweepMapsLocked(now time.Time) {
	p.sweepStoppedLocked(now)
	for uid, exp := range p.noBinding {
		if !now.Before(exp) {
			delete(p.noBinding, uid)
		}
	}
	throttle := p.throttle()
	for k, ts := range p.lastPush {
		if now.Sub(ts) >= throttle {
			delete(p.lastPush, k)
		}
	}
	for uid, ts := range p.userLastPush {
		if now.Sub(ts) >= throttle {
			delete(p.userLastPush, uid)
		}
	}
}

// PruneExpired runs a TTL sweep across all in-memory maps.
func (p *UnreadPusher) PruneExpired() {
	if p == nil {
		return
	}
	now := p.now()
	p.mu.Lock()
	p.sweepMapsLocked(now)
	p.mu.Unlock()
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

func (p *UnreadPusher) hasStopped() bool {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepStoppedLocked(now)
	return len(p.stopped) > 0
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

// NotifyUserActivity clears the negative cache for userID, un-stops the session's sid only
// when the stopped set is non-empty (avoiding a DB lookup on every channel view), and queues a push.
func (p *UnreadPusher) NotifyUserActivity(userID, sessionID string) {
	if p == nil || userID == "" {
		return
	}
	p.mu.Lock()
	delete(p.noBinding, userID)
	p.mu.Unlock()
	if sessionID != "" && p.hasStopped() {
		go p.UnstopSession(context.Background(), sessionID)
	}
	p.NotifyUser(userID)
}

func (p *UnreadPusher) ensureNotifyPoolLocked() {
	if p.poolStarted || p.closed {
		return
	}
	p.poolStarted = true
	p.notifyCh = make(chan string, p.notifyQueueSize())
	p.stopCh = make(chan struct{})
	workers := p.notifyWorkers()
	for i := 0; i < workers; i++ {
		go p.notifyWorker(p.notifyCh, p.stopCh)
	}
}

func (p *UnreadPusher) notifyWorker(ch <-chan string, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case userID := <-ch:
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				return
			}
			st := p.users[userID]
			if st == nil {
				p.mu.Unlock()
				continue
			}
			st.queued = false
			st.inFlight = true
			p.mu.Unlock()

			p.runUserPush(userID)
		}
	}
}

// Close cancels pending trailing timers, the periodic refresh loop, and the notify worker pool.
func (p *UnreadPusher) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for _, st := range p.users {
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
	}
	cancel := p.cancelRefresh
	p.cancelRefresh = nil
	stopCh := p.stopCh
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

func (p *UnreadPusher) scheduleTrailingLocked(userID string, st *userPushState, delay time.Duration) {
	if st.timer != nil || p.closed {
		return
	}
	if delay <= 0 {
		delay = p.throttle()
	}
	st.timer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		cur := p.users[userID]
		if cur == nil || p.closed {
			p.mu.Unlock()
			return
		}
		cur.timer = nil
		if cur.inFlight || cur.queued {
			cur.dirty = true
			p.mu.Unlock()
			return
		}
		p.enqueueLocked(userID, cur)
		p.mu.Unlock()
	})
}

func (p *UnreadPusher) enqueueLocked(userID string, st *userPushState) {
	p.ensureNotifyPoolLocked()
	select {
	case p.notifyCh <- userID:
		st.queued = true
	default:
		// Queue saturated under heavy fan-out: coalesce onto a trailing timer instead of spawning goroutines.
		st.dirty = true
		p.scheduleTrailingLocked(userID, st, p.throttle())
	}
}

// NotifyUser schedules an asynchronous unread push for userID. Gated in memory before touching
// the DB (negative cache + per-user throttle) and dispatched through a bounded worker pool (<= 4).
func (p *UnreadPusher) NotifyUser(userID string) {
	if p == nil || userID == "" || p.Store == nil || p.Counter == nil || p.Signer == nil || p.GatewayURL == "" || p.HTTP == nil {
		return
	}
	now := p.now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.ensureMapsLocked()
	p.sweepMapsLocked(now)

	// Negative cache: skip DB query for users recently known to have no live binding.
	if exp, neg := p.noBinding[userID]; neg && now.Before(exp) {
		p.mu.Unlock()
		return
	}

	st, ok := p.users[userID]
	if !ok {
		st = &userPushState{}
		p.users[userID] = st
	}
	if st.queued || st.inFlight || st.timer != nil {
		st.dirty = true
		p.mu.Unlock()
		return
	}

	// Pre-DB per-user throttle gate: stay off the DB during the 30 s window and coalesce onto 1 trailing timer.
	if last, hasLast := p.userLastPush[userID]; hasLast {
		if rem := p.throttle() - now.Sub(last); rem > 0 {
			st.dirty = false
			p.scheduleTrailingLocked(userID, st, rem)
			p.mu.Unlock()
			return
		}
	}

	st.dirty = false
	p.enqueueLocked(userID, st)
	p.mu.Unlock()
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
	p.scheduleTrailingLocked(userID, st, delay)
	p.mu.Unlock()
}

// pushUserNow recomputes the user's unread badge count and pushes to each live (tenant, sub).
// When respectThrottle is true, any (tenant, sub) still within its throttle window is skipped
// and the shortest remaining wait duration is returned so the caller schedules a single trailing push.
func (p *UnreadPusher) pushUserNow(ctx context.Context, userID string, respectThrottle bool) time.Duration {
	bindings, err := p.Store.LiveSessionBindingsOfUser(ctx, userID)
	if err != nil {
		return 0
	}
	if len(bindings) == 0 {
		p.mu.Lock()
		p.ensureMapsLocked()
		p.noBinding[userID] = p.now().Add(p.noBindingTTL())
		p.mu.Unlock()
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
		p.sweepMapsLocked(now)
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

	anyReadyPushed := false
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
		// Do not stamp lastPush on counter error ("unavailable") so recovery is not delayed 30 s.
		if pushed && status == "ready" {
			anyReadyPushed = true
			p.mu.Lock()
			p.ensureMapsLocked()
			p.lastPush[k] = p.now()
			p.mu.Unlock()
		}
	}
	if anyReadyPushed {
		p.mu.Lock()
		p.ensureMapsLocked()
		p.userLastPush[userID] = p.now()
		p.mu.Unlock()
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

// StartRefresh launches the bounded periodic refresh loop once (default 5 minutes).
// Cancelled cleanly by ctx or Close().
func (p *UnreadPusher) StartRefresh(ctx context.Context, interval time.Duration) {
	if p == nil {
		return
	}
	if interval <= 0 {
		interval = unreadDefaultInterval
	}
	p.refreshStart.Do(func() {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		rctx, cancel := context.WithCancel(ctx)
		p.cancelRefresh = cancel
		p.mu.Unlock()

		go func() {
			defer cancel()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-rctx.Done():
					return
				case <-ticker.C:
					p.RefreshOnce(rctx)
				}
			}
		}()
	})
}

// RefreshOnce pushes fresh unread counts for all SQL-filtered live, non-operator sessions.
// Honours the 30 s throttle, guarded against overlapping runs, bounded by a worker pool (<= 4)
// and an overall deadline (< 5m). Returns false if another refresh is already in flight.
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

	p.PruneExpired()

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
				p.pushUserBindings(runCtx, work.userID, work.bindings, true)
			}
		}()
	}
	wg.Wait()
	return true
}
