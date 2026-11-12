// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ReconcileOps is the native side of the lockdown reconciler.
type ReconcileOps interface {
	NativeOps
	// EnsureRoleLockdown removes the Team create/list/join/invite permissions from the customer
	// roles and returns the forbidden permissions still present after a read-back (empty = compliant).
	EnsureRoleLockdown(ctx context.Context) (map[string][]string, error)
	// ConfigViolations lists effective server settings that contradict the Phoenix policy.
	ConfigViolations(ctx context.Context) []string
}

// Reconciler applies and verifies the native lockdown continuously. Drift closes customer
// access (fail closed) until a native read-back shows the policy restored; a log line is never
// the only reaction.
type Reconciler struct {
	DB     *sql.DB
	Ops    ReconcileOps
	Policy *Policy
	Audit  func(event string, fields map[string]string)
	Now    func() time.Time

	mu       sync.Mutex
	ready    bool
	lastRun  time.Time
	lastErr  string
	global   string
	tenants  map[string]string
	policyAt int64
}

func (r *Reconciler) audit(event string, f map[string]string) {
	if r.Audit != nil {
		r.Audit(event, f)
	}
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// RunOnce executes one full reconcile + read-back pass and publishes the drift state.
func (r *Reconciler) RunOnce(ctx context.Context) error {
	global := ""
	tenants := map[string]string{}

	// 1) Role lockdown, applied and verified by read-back.
	residual, err := r.Ops.EnsureRoleLockdown(ctx)
	if err != nil {
		r.finish(ctx, "", nil, err)
		return err
	}
	if len(residual) > 0 {
		global = "role lockdown incomplete: " + describeResidual(residual)
	}

	// 2) Effective configuration (public files, signup, tokens, OAuth, plugins, sharing...).
	if v := r.Ops.ConfigViolations(ctx); len(v) > 0 {
		sort.Strings(v)
		global = strings.TrimSpace(global + " config: " + strings.Join(v, ","))
	}

	// 3) Bound Teams must stay private (type I, no open invite, not archived).
	rows, err := r.DB.QueryContext(ctx, `SELECT tb.tenant_id, tb.team_id FROM phoenix_tenant_bindings tb JOIN teams t ON t.id = tb.team_id
		WHERE t.type <> 'I' OR t.allowopeninvite OR t.deleteat <> 0`)
	if err != nil {
		r.finish(ctx, global, nil, err)
		return err
	}
	type bad struct{ tenant, team string }
	var badTeams []bad
	for rows.Next() {
		var b bad
		if err := rows.Scan(&b.tenant, &b.team); err != nil {
			rows.Close()
			r.finish(ctx, global, nil, err)
			return err
		}
		badTeams = append(badTeams, b)
	}
	rows.Close()
	for _, b := range badTeams {
		r.audit("phoenix.drift.team_public", map[string]string{"tenant": b.tenant, "team": b.team})
		tenants[b.tenant] = "team policy drift"
		if _, ferr := r.Ops.EnsureTeamPrivate(ctx, b.team); ferr != nil {
			continue
		}
	}
	for _, b := range badTeams { // read-back
		var n int
		if qerr := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM teams WHERE id=$1 AND type='I' AND NOT allowopeninvite AND deleteat=0`, b.team).Scan(&n); qerr == nil && n == 1 {
			delete(tenants, b.tenant)
			r.audit("phoenix.drift.team_restored", map[string]string{"tenant": b.tenant, "team": b.team})
		}
	}

	// 4) A principal may belong only to its tenant Team (second Team membership is drift).
	mrows, err := r.DB.QueryContext(ctx, `SELECT ub.tenant_id, ub.native_user_id, tm.teamid FROM phoenix_user_bindings ub
		JOIN phoenix_tenant_bindings tb ON tb.tenant_id = ub.tenant_id
		JOIN teammembers tm ON tm.userid = ub.native_user_id AND tm.deleteat = 0 AND tm.teamid <> tb.team_id
		WHERE ub.actor_kind <> 'OPERATOR'`)
	if err != nil {
		r.finish(ctx, global, tenants, err)
		return err
	}
	type extra struct{ tenant, user, team string }
	var extras []extra
	for mrows.Next() {
		var e extra
		if err := mrows.Scan(&e.tenant, &e.user, &e.team); err != nil {
			mrows.Close()
			r.finish(ctx, global, tenants, err)
			return err
		}
		extras = append(extras, e)
	}
	mrows.Close()
	for _, e := range extras {
		r.audit("phoenix.drift.second_team", map[string]string{"tenant": e.tenant, "user": e.user, "team": e.team})
		tenants[e.tenant] = "principal in a second team"
		_ = r.Ops.RemoveTeamMember(ctx, e.team, e.user)
	}
	for _, e := range extras { // read-back
		var n int
		if qerr := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM teammembers WHERE userid=$1 AND teamid=$2 AND deleteat=0`, e.user, e.team).Scan(&n); qerr == nil && n == 0 {
			// only reopen when no other extra of the same tenant remains
			stillBad := false
			for _, o := range extras {
				if o.tenant == e.tenant {
					var m int
					if r.DB.QueryRowContext(ctx, `SELECT count(*) FROM teammembers WHERE userid=$1 AND teamid=$2 AND deleteat=0`, o.user, o.team).Scan(&m) != nil || m > 0 {
						stillBad = true
					}
				}
			}
			if !stillBad {
				delete(tenants, e.tenant)
				r.audit("phoenix.drift.second_team_closed", map[string]string{"tenant": e.tenant, "user": e.user})
			}
		}
	}

	// 5) Stamp the applied policy version only for tenants without drift.
	if global == "" {
		ids := make([]string, 0)
		trows, qerr := r.DB.QueryContext(ctx, `SELECT tenant_id FROM phoenix_tenant_bindings WHERE policy_version <> $1`, CurrentPolicyVersion)
		if qerr == nil {
			for trows.Next() {
				var id string
				if trows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			trows.Close()
		}
		for _, id := range ids {
			if _, drift := tenants[id]; drift {
				continue
			}
			_, _ = r.DB.ExecContext(ctx, `UPDATE phoenix_tenant_bindings SET policy_version=$2, updated_at=$3 WHERE tenant_id=$1`, id, CurrentPolicyVersion, r.now().UnixMilli())
		}
	}
	r.finish(ctx, global, tenants, nil)
	return nil
}

func describeResidual(m map[string][]string) string {
	var parts []string
	for role, perms := range m {
		sort.Strings(perms)
		parts = append(parts, role+"="+strings.Join(perms, "+"))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func (r *Reconciler) finish(ctx context.Context, global string, tenants map[string]string, runErr error) {
	r.mu.Lock()
	r.lastRun = r.now()
	if runErr != nil {
		r.lastErr = runErr.Error()
		r.ready = false
		// A failed read-back invalidates the previous verification for serving, too.
		if r.Policy != nil {
			r.Policy.SetDrift("policy reconciliation unavailable", nil)
		}
		r.mu.Unlock()
		return
	}
	r.lastErr, r.global, r.tenants, r.ready = "", global, tenants, global == "" && len(tenants) == 0
	if r.Policy != nil {
		r.Policy.SetDrift(global, tenants)
	}
	r.mu.Unlock()
	if global != "" {
		r.audit("phoenix.drift.global", map[string]string{"detail": global})
	}
}

// Ready is the readiness predicate: a complete verified pass with no drift. Until the first
// pass completes the process is not ready and ResolveActor stays closed for tenant principals.
func (r *Reconciler) Ready(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.lastRun.IsZero():
		return errors.New("policy reconcile has not completed")
	case r.lastErr != "":
		return fmt.Errorf("policy reconcile failed: %s", r.lastErr)
	case r.global != "":
		return errors.New("policy drift: " + r.global)
	case len(r.tenants) > 0:
		return fmt.Errorf("policy drift in %d tenant(s)", len(r.tenants))
	case !r.ready:
		return errors.New("policy not verified")
	}
	if r.now().Sub(r.lastRun) > 2*time.Minute {
		return errors.New("policy reconcile is stale")
	}
	return nil
}

// Start runs the reconcile loop until ctx is cancelled.
func (r *Reconciler) Start(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := r.RunOnce(ctx); err != nil {
					r.audit("phoenix.reconcile.error", map[string]string{"cause": err.Error()})
				}
			}
		}
	}()
}
