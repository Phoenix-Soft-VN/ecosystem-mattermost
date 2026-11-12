// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/lib/pq"
)

// ResourceKind names the native resource types the tenant guard resolves.
type ResourceKind int

const (
	ResUser ResourceKind = iota + 1
	ResTeam
	ResChannel
	ResPost
	ResFile
	ResUsername
	ResEmail
	ResTeamName
)

// ActorContext is the immutable identity of the caller, resolved from server-side bindings
// only (never from request input). Policy is: same tenant AND active access AND native permission.
type ActorContext struct {
	SessionID     string
	UserID        string
	Kind          ActorKind
	TenantID      string
	TeamID        string
	Issuer        string
	Sub           string
	SID           string
	AccessVersion int64
	Operator      bool
}

type actorKey struct{}

// WithActor stores the resolved actor in a context.
func WithActor(ctx context.Context, a ActorContext) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor placed by the HTTP/WS entry guard.
func ActorFrom(ctx context.Context) (ActorContext, bool) {
	a, ok := ctx.Value(actorKey{}).(ActorContext)
	return a, ok
}

// ErrNotFound is the single shape used for foreign and unknown (valid-format) resources.
var ErrNotFound = newErr(http.StatusNotFound, CodeNotFound, "resource not found")

// ErrNoActor means the session has no Phoenix binding and is not an operator.
var ErrNoActor = newErr(http.StatusUnauthorized, CodeAuthInvalid, "session is not bound to a Phoenix principal")

// ErrAccessDenied means the binding exists but access is revoked/pending or the tenant is suspended.
var ErrAccessDenied = newErr(http.StatusForbidden, CodeAccessDenied, "chat access is not granted")

// Policy resolves actors and answers resource-ownership questions against the bindings.
// ResolveActor results are cached for at most ActorCacheTTL on a monotonic clock; membership
// or state changes invalidate immediately through the Invalidate* methods.
type Policy struct {
	db  *sql.DB
	now func() time.Time

	mu    sync.Mutex
	cache map[string]cachedActor

	driftMu  sync.RWMutex
	verified bool
	global   string
	tenants  map[string]string

	// Authority, when set, is the live Gateway check (<= 20 s cache). It runs for every tenant
	// principal and may only shorten how long a resolved actor is reused.
	Authority *Authority
}

// SetDrift publishes the reconciler's verified state. Until the first call the policy is
// unverified and tenant principals are refused (503) rather than served unchecked.
func (p *Policy) SetDrift(global string, tenants map[string]string) {
	p.driftMu.Lock()
	p.verified, p.global, p.tenants = true, global, tenants
	p.driftMu.Unlock()
}

func (p *Policy) driftError(tenantID string) *Error {
	p.driftMu.RLock()
	defer p.driftMu.RUnlock()
	switch {
	case !p.verified:
		return newErr(http.StatusServiceUnavailable, CodeNotReady, "policy not verified")
	case p.global != "":
		return ErrAccessDenied
	}
	if _, bad := p.tenants[tenantID]; bad {
		return ErrAccessDenied
	}
	return nil
}

// ActorCacheTTL bounds how long a resolved local actor may be reused. It is deliberately far
// below the 30 s Gateway-authority bound of contracts C2.
const ActorCacheTTL = 5 * time.Second

type cachedActor struct {
	actor   ActorContext
	err     *Error
	expires time.Time
}

func NewPolicy(db *sql.DB, now func() time.Time) *Policy {
	if now == nil {
		now = time.Now
	}
	return &Policy{db: db, now: now, cache: map[string]cachedActor{}}
}

var (
	defaultMu     sync.RWMutex
	defaultPolicy *Policy
)

// SetDefault installs the process-wide policy (called once at startup when enforced).
func SetDefault(p *Policy) { defaultMu.Lock(); defaultPolicy = p; defaultMu.Unlock() }

// Default returns the installed policy, or nil when the boundary is not enforced.
func Default() *Policy { defaultMu.RLock(); defer defaultMu.RUnlock(); return defaultPolicy }

// InvalidateSession drops the cached actor of one native session.
func (p *Policy) InvalidateSession(sessionID string) {
	p.mu.Lock()
	delete(p.cache, sessionID)
	p.mu.Unlock()
}

// InvalidateUser drops every cached actor of one native user.
func (p *Policy) InvalidateUser(userID string) {
	p.mu.Lock()
	for k, v := range p.cache {
		if v.actor.UserID == userID {
			delete(p.cache, k)
		}
	}
	p.mu.Unlock()
}

// InvalidateTenant drops every cached actor of one tenant.
func (p *Policy) InvalidateTenant(tenantID string) {
	p.mu.Lock()
	for k, v := range p.cache {
		if v.actor.TenantID == tenantID {
			delete(p.cache, k)
		}
	}
	p.mu.Unlock()
}

// ResolveActor builds the immutable ActorContext for a native session. A session without a
// Phoenix session binding is accepted only for an enabled registered operator; it is never
// promoted to operator because a binding is missing.
func (p *Policy) ResolveActor(ctx context.Context, sessionID, userID string) (ActorContext, error) {
	if sessionID == "" || userID == "" {
		return ActorContext{}, ErrNoActor
	}
	now := p.now()
	p.mu.Lock()
	if c, ok := p.cache[sessionID]; ok && now.Before(c.expires) && c.actor.UserID == userID {
		p.mu.Unlock()
		if c.err != nil {
			return ActorContext{}, c.err
		}
		if !c.actor.Operator {
			if derr := p.driftError(c.actor.TenantID); derr != nil {
				return ActorContext{}, derr
			}
		}
		return c.actor, nil
	}
	p.mu.Unlock()

	actor, perr := p.load(ctx, sessionID, userID)
	ttl := ActorCacheTTL
	if perr == nil && !actor.Operator && p.Authority != nil {
		// Live Gateway authority: an outage after the cache window closes access (503); a revoked
		// session or CHAT entitlement is refused with 401/403.
		if aerr := p.Authority.Check(ctx, actor); aerr != nil {
			var ae *Error
			if errors.As(aerr, &ae) {
				perr = ae
			} else {
				perr = ErrAuthorityUnavailable
			}
		} else if left := p.Authority.ValidFor(sessionID); left > 0 && left < ttl {
			ttl = left
		}
	}
	if perr != nil && perr.Status == http.StatusServiceUnavailable {
		ttl = time.Second // outages are retried quickly, never held
	}
	p.mu.Lock()
	if len(p.cache) > 50_000 { // hard bound on memory
		p.cache = map[string]cachedActor{}
	}
	p.cache[sessionID] = cachedActor{actor: actor, err: perr, expires: now.Add(ttl)}
	p.mu.Unlock()
	if perr != nil {
		return ActorContext{}, perr
	}
	if !actor.Operator {
		if derr := p.driftError(actor.TenantID); derr != nil {
			return ActorContext{}, derr
		}
	}
	return actor, nil
}

func (p *Policy) load(ctx context.Context, sessionID, userID string) (ActorContext, *Error) {
	var a ActorContext
	var kind, access, tstate string
	var teamID string
	err := p.db.QueryRowContext(ctx, `SELECT sb.issuer, sb.sub, sb.sid, sb.tenant_id, ub.actor_kind, ub.access_state, ub.access_version, tb.state, tb.team_id
		FROM phoenix_session_bindings sb
		JOIN phoenix_user_bindings ub ON ub.issuer = sb.issuer AND ub.sub = sb.sub AND ub.native_user_id = sb.native_user_id
		JOIN phoenix_tenant_bindings tb ON tb.tenant_id = ub.tenant_id AND tb.tenant_id = sb.tenant_id
		WHERE sb.session_id = $1 AND sb.native_user_id = $2 AND sb.expires_at > $3`, sessionID, userID, p.now().UnixMilli()).
		Scan(&a.Issuer, &a.Sub, &a.SID, &a.TenantID, &kind, &access, &a.AccessVersion, &tstate, &teamID)
	switch {
	case err == nil:
		a.SessionID, a.UserID, a.Kind, a.TeamID = sessionID, userID, ActorKind(kind), teamID
		if AccessState(access) != AccessGranted || TenantState(tstate) != TenantActive {
			return ActorContext{}, ErrAccessDenied
		}
		var invariant bool
		if err := p.db.QueryRowContext(ctx, `SELECT t.type='I' AND NOT t.allowopeninvite AND t.deleteat=0
			AND NOT EXISTS(SELECT 1 FROM teammembers WHERE userid=$1 AND teamid<>$2 AND deleteat=0)
			FROM teams t WHERE t.id=$2`, userID, teamID).Scan(&invariant); err != nil || !invariant {
			return ActorContext{}, ErrAccessDenied
		}
		return a, nil
	case errors.Is(err, sql.ErrNoRows):
		var enabled bool
		oerr := p.db.QueryRowContext(ctx, `SELECT enabled FROM phoenix_operator_accounts WHERE native_user_id=$1`, userID).Scan(&enabled)
		if oerr == nil && enabled {
			return ActorContext{SessionID: sessionID, UserID: userID, Kind: ActorOperator, Operator: true}, nil
		}
		if oerr != nil && !errors.Is(oerr, sql.ErrNoRows) {
			return ActorContext{}, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", oerr)
		}
		return ActorContext{}, ErrNoActor
	default:
		return ActorContext{}, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
}

// FilterPostIDs applies ownership to a batch in one query before native content lookup.
func (p *Policy) FilterPostIDs(ctx context.Context, actor ActorContext, ids []string) ([]string, error) {
	if len(ids) > MaxBatchIDs {
		return nil, newErr(http.StatusBadRequest, CodeInvalidRequest, "too many ids (max 200)")
	}
	if actor.Operator {
		return ids, nil
	}
	rows, err := p.db.QueryContext(ctx, `SELECT p.id FROM posts p JOIN channels c ON c.id=p.channelid
		LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id=c.id
		LEFT JOIN phoenix_tenant_bindings tb ON tb.team_id=c.teamid AND c.teamid<>''
		WHERE p.id=ANY($1) AND (cb.tenant_id=$2 OR tb.tenant_id=$2)`, pq.Array(ids), actor.TenantID)
	if err != nil {
		return nil, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	out := []string{}
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// channelOwnedSQL is the single definition of "channel belongs to tenant $2": either an explicit
// channel binding (mandatory for DM/group DM) or a team bound to the tenant.
const channelOwnedSQL = `SELECT 1 FROM channels c
	LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id = c.id
	LEFT JOIN phoenix_tenant_bindings tb ON tb.team_id = c.teamid AND c.teamid <> ''
	WHERE c.id = $1 AND (cb.tenant_id = $2 OR tb.tenant_id = $2)`

// CanAccessResource answers the tenant half of the guard for one resource. Operators pass;
// customers/bots pass only for resources owned by their tenant. A foreign or unknown
// resource always yields the same ErrNotFound.
func (p *Policy) CanAccessResource(ctx context.Context, actor ActorContext, kind ResourceKind, id string) error {
	if actor.Operator {
		return nil
	}
	if id == "" {
		return ErrNotFound
	}
	var q string
	var args []any
	switch kind {
	case ResUser:
		if id == actor.UserID {
			return nil
		}
		q, args = `SELECT 1 FROM phoenix_user_bindings WHERE native_user_id=$1 AND tenant_id=$2`, []any{id, actor.TenantID}
	case ResUsername:
		q, args = `SELECT 1 FROM users u JOIN phoenix_user_bindings ub ON ub.native_user_id = u.id WHERE u.username=$1 AND ub.tenant_id=$2`, []any{id, actor.TenantID}
	case ResEmail:
		q, args = `SELECT 1 FROM users u JOIN phoenix_user_bindings ub ON ub.native_user_id = u.id WHERE lower(u.email)=lower($1) AND ub.tenant_id=$2`, []any{id, actor.TenantID}
	case ResTeam:
		q, args = `SELECT 1 FROM phoenix_tenant_bindings WHERE team_id=$1 AND tenant_id=$2`, []any{id, actor.TenantID}
	case ResTeamName:
		q, args = `SELECT 1 FROM teams t JOIN phoenix_tenant_bindings tb ON tb.team_id = t.id WHERE t.name=$1 AND tb.tenant_id=$2`, []any{id, actor.TenantID}
	case ResChannel:
		q, args = channelOwnedSQL, []any{id, actor.TenantID}
	case ResPost:
		q = `SELECT 1 FROM posts p JOIN channels c ON c.id = p.channelid
			LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id = c.id
			LEFT JOIN phoenix_tenant_bindings tb ON tb.team_id = c.teamid AND c.teamid <> ''
			WHERE p.id = $1 AND (cb.tenant_id = $2 OR tb.tenant_id = $2)`
		args = []any{id, actor.TenantID}
	case ResFile:
		// A file is owned through its channel; an unattached upload is owned by its creator's tenant.
		q = `SELECT 1 FROM fileinfo f
			LEFT JOIN channels c ON c.id = f.channelid AND f.channelid <> ''
			LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id = c.id
			LEFT JOIN phoenix_tenant_bindings tb ON tb.team_id = c.teamid AND c.teamid <> ''
			LEFT JOIN phoenix_user_bindings ub ON ub.native_user_id = f.creatorid
			WHERE f.id = $1 AND (
				(c.id IS NOT NULL AND (cb.tenant_id = $2 OR tb.tenant_id = $2))
				OR (c.id IS NULL AND ub.tenant_id = $2))`
		args = []any{id, actor.TenantID}
	default:
		return ErrNotFound
	}
	var one int
	err := p.db.QueryRowContext(ctx, q, args...).Scan(&one)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	default:
		return wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
}

// FilterUserIDs returns the subset of ids that belong to the actor's tenant, in one query.
// A batch above MaxBatchIDs is rejected before any query. Foreign and unknown ids are omitted.
func (p *Policy) FilterUserIDs(ctx context.Context, actor ActorContext, ids []string) ([]string, error) {
	if len(ids) > MaxBatchIDs {
		return nil, newErr(http.StatusBadRequest, CodeInvalidRequest, "too many ids (max 200)")
	}
	if actor.Operator {
		return ids, nil
	}
	if len(ids) == 0 {
		return []string{}, nil
	}
	rows, err := p.db.QueryContext(ctx, `SELECT native_user_id FROM phoenix_user_bindings WHERE tenant_id=$1 AND native_user_id = ANY($2)`, actor.TenantID, pq.Array(ids))
	if err != nil {
		return nil, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(allowed))
	for _, id := range ids { // preserve caller order, drop duplicates
		if allowed[id] {
			out = append(out, id)
			delete(allowed, id)
		}
	}
	return out, nil
}

// ParticipantsShareTenant is the DM / group-DM rule: every participant must be a bound
// principal of the actor's tenant (the actor included), or creation is refused.
func (p *Policy) ParticipantsShareTenant(ctx context.Context, actor ActorContext, userIDs []string) error {
	if actor.Operator {
		return nil
	}
	uniq := map[string]bool{actor.UserID: true}
	for _, id := range userIDs {
		uniq[id] = true
	}
	list := make([]string, 0, len(uniq))
	for id := range uniq {
		list = append(list, id)
	}
	allowed, err := p.FilterUserIDs(ctx, actor, list)
	if err != nil {
		return err
	}
	if len(allowed) != len(list) {
		return ErrNotFound
	}
	return nil
}

// BindChannel records ownership of a channel (DM/group DM mandatory). It is idempotent for
// the same tenant and conflicts for another tenant.
func (p *Policy) BindChannel(ctx context.Context, channelID, tenantID string) error {
	res, err := p.db.ExecContext(ctx, `INSERT INTO phoenix_channel_bindings (channel_id, tenant_id) VALUES ($1,$2) ON CONFLICT (channel_id) DO NOTHING`, channelID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var owner string
	if err := p.db.QueryRowContext(ctx, `SELECT tenant_id FROM phoenix_channel_bindings WHERE channel_id=$1`, channelID).Scan(&owner); err != nil {
		return err
	}
	if owner != tenantID {
		return ErrNotFound
	}
	return nil
}

// TenantOfUser returns the tenant of a bound native user ("" when unbound).
func (p *Policy) TenantOfUser(ctx context.Context, userID string) (string, error) {
	var t string
	err := p.db.QueryRowContext(ctx, `SELECT tenant_id FROM phoenix_user_bindings WHERE native_user_id=$1`, userID).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return t, err
}

// IsOperator reports whether a native user is an enabled registered operator.
func (p *Policy) IsOperator(ctx context.Context, userID string) (bool, error) {
	var enabled bool
	err := p.db.QueryRowContext(ctx, `SELECT enabled FROM phoenix_operator_accounts WHERE native_user_id=$1`, userID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// UsersShareTenant reports whether both users are bound to the same tenant. Unbound users
// share nothing; the caller decides what operators may do.
func (p *Policy) UsersShareTenant(ctx context.Context, a, b string) (bool, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT count(DISTINCT tenant_id) FROM phoenix_user_bindings WHERE native_user_id = ANY($1)`, pq.Array([]string{a, b})).Scan(&n)
	if err != nil {
		return false, err
	}
	if a == b {
		return n == 1, nil
	}
	var cnt int
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM phoenix_user_bindings WHERE native_user_id = ANY($1)`, pq.Array([]string{a, b})).Scan(&cnt); err != nil {
		return false, err
	}
	return cnt == 2 && n == 1, nil
}

// CommonTenant returns the tenant shared by every listed user. If any user is unbound or the
// users span tenants it returns ErrNotFound (the same shape as an unknown resource).
func (p *Policy) CommonTenant(ctx context.Context, ids []string) (string, error) {
	uniq := map[string]bool{}
	for _, id := range ids {
		uniq[id] = true
	}
	list := make([]string, 0, len(uniq))
	for id := range uniq {
		list = append(list, id)
	}
	rows, err := p.db.QueryContext(ctx, `SELECT tenant_id FROM phoenix_user_bindings WHERE native_user_id = ANY($1)`, pq.Array(list))
	if err != nil {
		return "", wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
	defer rows.Close()
	tenants := map[string]bool{}
	n := 0
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return "", err
		}
		tenants[t] = true
		n++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n != len(list) || len(tenants) != 1 {
		return "", ErrNotFound
	}
	for t := range tenants {
		return t, nil
	}
	return "", ErrNotFound
}

// SessionValid is the background liveness check for idle WebSocket connections: it needs no
// frame and no invalidation message to notice that access ended.
func (p *Policy) SessionValid(ctx context.Context, sessionID, userID string) error {
	_, err := p.ResolveActor(ctx, sessionID, userID)
	return err
}
