// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"database/sql"
	"testing"
)

type reconcileNative struct {
	NativeOps
	db  *sql.DB
	bad bool
}

func (n *reconcileNative) EnsureRoleLockdown(context.Context) (map[string][]string, error) {
	return map[string][]string{}, nil
}
func (n *reconcileNative) ConfigViolations(context.Context) []string {
	if n.bad {
		return []string{"FileSettings.EnablePublicLink"}
	}
	return nil
}
func (n *reconcileNative) EnsureTeamPrivate(ctx context.Context, id string) (*TeamInfo, error) {
	_, err := n.db.ExecContext(ctx, `UPDATE teams SET type='I',allowopeninvite=false WHERE id=$1`, id)
	return &TeamInfo{ID: id, Type: "I"}, err
}
func (n *reconcileNative) RemoveTeamMember(ctx context.Context, team, user string) error {
	_, err := n.db.ExecContext(ctx, `UPDATE teammembers SET deleteat=1 WHERE teamid=$1 AND userid=$2`, team, user)
	return err
}

// Break: logging config/read-back failure without closing the serving policy.
func TestPolicyDrift(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	native := &reconcileNative{db: f.db}
	reconciler := &Reconciler{DB: f.db, Ops: native, Policy: p}
	ctx := context.Background()
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Ready(ctx); err != nil {
		t.Fatal("compliant positive control:", err)
	}
	native.bad = true
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if reconciler.Ready(ctx) == nil {
		t.Fatal("public file drift remained ready")
	}
	if _, err := p.ResolveActor(ctx, w.SessA1, w.A1); err == nil {
		t.Fatal("customer served while drifted")
	}
	native.bad = false
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveActor(ctx, w.SessA1, w.A1); err != nil {
		t.Fatal("read-back must reopen compliant tenant:", err)
	}
	if _, err := f.db.Exec(`UPDATE teams SET type='O',allowopeninvite=true WHERE id=$1`, w.TeamA); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var teamType string
	var invite bool
	if err := f.db.QueryRow(`SELECT type,allowopeninvite FROM teams WHERE id=$1`, w.TeamA).Scan(&teamType, &invite); err != nil || teamType != "I" || invite {
		t.Fatalf("native policy not restored/read back: %s %v %v", teamType, invite, err)
	}
	f.insert("teammembers", map[string]any{"teamid": w.TeamB, "userid": w.A1, "deleteat": 0})
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var extra int
	if err := f.db.QueryRow(`SELECT count(*) FROM teammembers WHERE userid=$1 AND teamid=$2 AND deleteat=0`, w.A1, w.TeamB).Scan(&extra); err != nil || extra != 0 {
		t.Fatal("second Team was not removed/read back")
	}
	if _, err := f.db.Exec(`ALTER TABLE phoenix_tenant_bindings RENAME TO phoenix_tenant_bindings_unavailable`); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec(`ALTER TABLE phoenix_tenant_bindings_unavailable RENAME TO phoenix_tenant_bindings`)
	if reconciler.RunOnce(ctx) == nil {
		t.Fatal("failed native policy read-back went unnoticed")
	}
	if _, err := p.ResolveActor(ctx, w.SessA1, w.A1); err == nil {
		t.Fatal("prior cached actor survived failed reconciliation")
	}
}
