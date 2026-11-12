// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"
)

// EventInfo is the policy-relevant projection of a WebSocket event.
type EventInfo struct {
	Type         string
	ChannelID    string
	TeamID       string
	TargetUserID string   // event addressed to one user
	Subjects     []string // user ids the payload talks about
}

// unscopedAllowed lists the events without channel/team/user scope and without a user subject
// that every connection may receive. Any other unscoped event is dropped for tenant principals:
// default-deny keeps a new upstream broadcast from leaking across tenants.
var unscopedAllowed = map[string]bool{
	"hello": true, "config_changed": true, "license_changed": true, "plugin_statuses_changed": true,
	"client_config_changed": true, "server_version": true,
}

// tenantCache memoizes immutable resource->tenant mappings (a channel, team or user never
// changes tenant once bound). Only positive answers are cached.
type tenantCache struct {
	mu sync.RWMutex
	m  map[string]string
}

func (c *tenantCache) get(k string) (string, bool) {
	c.mu.RLock()
	v, ok := c.m[k]
	c.mu.RUnlock()
	return v, ok
}

func (c *tenantCache) put(k, v string) {
	c.mu.Lock()
	if c.m == nil || len(c.m) > 200_000 {
		c.m = map[string]string{}
	}
	c.m[k] = v
	c.mu.Unlock()
}

var wsTenants tenantCache

func (p *Policy) lookup(ctx context.Context, kind, id, query string) (string, error) {
	key := kind + ":" + id
	if t, ok := wsTenants.get(key); ok {
		return t, nil
	}
	var t sql.NullString
	err := p.db.QueryRowContext(ctx, query, id).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !t.Valid) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	wsTenants.put(key, t.String)
	return t.String, nil
}

// UserTenant returns the tenant of a bound native user ("" when unbound).
func (p *Policy) UserTenant(ctx context.Context, id string) (string, error) {
	return p.lookup(ctx, "u", id, `SELECT tenant_id FROM phoenix_user_bindings WHERE native_user_id=$1`)
}

// ChannelTenant returns the tenant owning a channel (binding or bound team).
func (p *Policy) ChannelTenant(ctx context.Context, id string) (string, error) {
	return p.lookup(ctx, "c", id, `SELECT COALESCE(cb.tenant_id, tb.tenant_id) FROM channels c
		LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id = c.id
		LEFT JOIN phoenix_tenant_bindings tb ON tb.team_id = c.teamid AND c.teamid <> ''
		WHERE c.id = $1`)
}

// TeamTenant returns the tenant bound to a team.
func (p *Policy) TeamTenant(ctx context.Context, id string) (string, error) {
	return p.lookup(ctx, "t", id, `SELECT tenant_id FROM phoenix_tenant_bindings WHERE team_id=$1`)
}

// AllowEvent decides whether one recipient may receive one event: the recipient must resolve to
// an active actor and every channel, team and subject user the event refers to must belong to
// the recipient's tenant. It is evaluated at queue time, at write time and for replayed events.
func (p *Policy) AllowEvent(ctx context.Context, sessionID, userID string, ev EventInfo) bool {
	actor, err := p.ResolveActor(ctx, sessionID, userID)
	if err != nil {
		return false
	}
	if actor.Operator {
		return true
	}
	same := func(tenant string, err error) bool { return err == nil && tenant == actor.TenantID }
	if ev.TargetUserID != "" && ev.TargetUserID != userID {
		return false
	}
	scoped := ev.ChannelID != "" || ev.TeamID != "" || ev.TargetUserID != ""
	if ev.ChannelID != "" {
		if t, e := p.ChannelTenant(ctx, ev.ChannelID); !same(t, e) {
			return false
		}
	}
	if ev.TeamID != "" {
		if t, e := p.TeamTenant(ctx, ev.TeamID); !same(t, e) {
			return false
		}
	}
	for _, s := range ev.Subjects {
		if s == "" || s == userID {
			continue
		}
		if t, e := p.UserTenant(ctx, s); !same(t, e) {
			return false
		}
	}
	if !scoped && len(ev.Subjects) == 0 && !unscopedAllowed[ev.Type] {
		return false
	}
	return true
}

// StatusLimiter enforces the WS status action budget (30/min/session).
var statusLimiter = NewLimiter(StatusWSRatePerMin, time.Minute, nil)

// AllowStatusAction consumes one status action for the session.
func AllowStatusAction(sessionID string) (bool, int) { return statusLimiter.Allow(sessionID) }

// TenantUserIDs lists the users of a tenant for the bounded "all statuses" action.
func (p *Policy) TenantUserIDs(ctx context.Context, tenantID string, limit int) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT native_user_id FROM phoenix_user_bindings WHERE tenant_id=$1 AND access_state <> 'PENDING' ORDER BY native_user_id LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "policy store unavailable", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
