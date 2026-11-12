// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// dollar rewrites the squirrel "?" placeholder to the PostgreSQL form used by raw database/sql.
func dollar(sql string) string { return strings.Replace(sql, "?", "$1", 1) }

func isNotFound(err error) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Status == http.StatusNotFound && pe.Code == CodeNotFound
}

// POLICY-BOUNDARY: A1/A2 share tenant A, B1 is foreign. Same-tenant is allowed, foreign and unknown
// ids are the same 404, an unbound principal is never an operator, DM participants and channel
// ownership cannot cross tenants.
func TestPolicyBoundary(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()

	a1, err := p.ResolveActor(ctx, w.SessA1, w.A1)
	if err != nil || a1.Kind != ActorCustomer || a1.TenantID != w.TenantA || a1.TeamID != w.TeamA || a1.Operator {
		t.Fatalf("A1 must resolve to a tenant A customer, got %+v err=%v", a1, err)
	}

	// positive control: same tenant is allowed (native permission is a separate, additional check)
	if err := p.CanAccessResource(ctx, a1, ResUser, w.A2); err != nil {
		t.Fatalf("A1 must reach A2: %v", err)
	}
	if err := p.CanAccessResource(ctx, a1, ResChannel, w.ChanA); err != nil {
		t.Fatalf("A1 must reach its tenant channel: %v", err)
	}

	// foreign and unknown (valid-format) ids: identical shape
	foreign := p.CanAccessResource(ctx, a1, ResUser, w.B1)
	unknown := p.CanAccessResource(ctx, a1, ResUser, "zzzzzzzzzzzzzzzzzzzzzzzzzz")
	if !isNotFound(foreign) || !isNotFound(unknown) || foreign.Error() != unknown.Error() {
		t.Fatalf("foreign and unknown must be the same 404: %v / %v", foreign, unknown)
	}
	for _, kind := range []ResourceKind{ResTeam, ResChannel, ResPost, ResFile} {
		id := map[ResourceKind]string{ResTeam: w.TeamB, ResChannel: w.ChanB, ResPost: "zzzzzzzzzzzzzzzzzzzzzzzzzz", ResFile: "zzzzzzzzzzzzzzzzzzzzzzzzzz"}[kind]
		if err := p.CanAccessResource(ctx, a1, kind, id); !isNotFound(err) {
			t.Fatalf("kind %d: foreign/unknown must be 404, got %v", kind, err)
		}
	}
	if err := p.CanAccessResource(ctx, a1, ResUsername, "no-such-user"); !isNotFound(err) {
		t.Fatalf("unknown username must be 404: %v", err)
	}

	// A1 holding a high native role does not change the answer: the guard never reads native roles.
	if _, err := f.db.Exec(`UPDATE users SET roles = 'system_user system_admin' WHERE id = $1`, w.A1); err != nil {
		t.Fatal(err)
	}
	p.InvalidateUser(w.A1)
	a1, _ = p.ResolveActor(ctx, w.SessA1, w.A1)
	if err := p.CanAccessResource(ctx, a1, ResUser, w.B1); !isNotFound(err) {
		t.Fatalf("a native admin role must not read a foreign tenant user: %v", err)
	}

	// DM / group DM: all participants must share a tenant
	if _, err := p.CommonTenant(ctx, []string{w.A1, w.B1}); !isNotFound(err) {
		t.Fatalf("A1+B1 must not have a common tenant: %v", err)
	}
	if tenant, err := p.CommonTenant(ctx, []string{w.A1, w.A2}); err != nil || tenant != w.TenantA {
		t.Fatalf("A1+A2 common tenant = %q err=%v", tenant, err)
	}
	if err := p.ParticipantsShareTenant(ctx, a1, []string{w.B1}); !isNotFound(err) {
		t.Fatalf("adding a foreign participant must be refused: %v", err)
	}
	if err := p.ParticipantsShareTenant(ctx, a1, []string{w.A2}); err != nil {
		t.Fatalf("adding a same-tenant participant must pass: %v", err)
	}
	// An unbound account is neither a participant nor an operator.
	if _, err := p.CommonTenant(ctx, []string{w.A1, w.Loner}); !isNotFound(err) {
		t.Fatalf("unbound user must not be a participant: %v", err)
	}

	// channel ownership is recorded once; a channel move/upload into another tenant is refused
	dm := f.id("dm")
	f.insert("channels", map[string]any{"id": dm, "teamid": "", "name": "dm" + dm, "type": "D", "createat": 1})
	if err := p.BindChannel(ctx, dm, w.TenantA); err != nil {
		t.Fatalf("binding a DM to its tenant: %v", err)
	}
	if err := p.BindChannel(ctx, dm, w.TenantA); err != nil {
		t.Fatalf("binding is idempotent: %v", err)
	}
	if err := p.BindChannel(ctx, dm, w.TenantB); !isNotFound(err) {
		t.Fatalf("a channel bound to A must not be re-bound to B: %v", err)
	}
	if err := p.CanAccessResource(ctx, a1, ResChannel, dm); err != nil {
		t.Fatalf("A1 must reach its DM: %v", err)
	}
	b1, _ := p.ResolveActor(ctx, w.SessB1, w.B1)
	if err := p.CanAccessResource(ctx, b1, ResChannel, dm); !isNotFound(err) {
		t.Fatalf("B1 must not reach A's DM: %v", err)
	}

	// a missing binding is never an operator; the operator registry is the only exemption
	if _, err := p.ResolveActor(ctx, w.SessLoner, w.Loner); err == nil || err.(*Error).Status != http.StatusUnauthorized {
		t.Fatalf("a session without a Phoenix binding must be 401, got %v", err)
	}
	op, err := p.ResolveActor(ctx, w.SessOp, w.Op)
	if err != nil || !op.Operator || op.Kind != ActorOperator {
		t.Fatalf("registered operator must resolve as operator: %+v %v", op, err)
	}
	if _, err := f.db.Exec(`UPDATE phoenix_operator_accounts SET enabled=false WHERE native_user_id=$1`, w.Op); err != nil {
		t.Fatal(err)
	}
	p.InvalidateSession(w.SessOp)
	if _, err := p.ResolveActor(ctx, w.SessOp, w.Op); err == nil {
		t.Fatal("a disabled operator must be refused")
	}

	// revoked access and suspended tenant close the actor
	if _, err := f.db.Exec(`UPDATE phoenix_user_bindings SET access_state='REVOKED', access_version=2 WHERE sub='sub-a2'`); err != nil {
		t.Fatal(err)
	}
	p.InvalidateUser(w.A2)
	if _, err := p.ResolveActor(ctx, w.SessA2, w.A2); err == nil || err.(*Error).Code != CodeAccessDenied {
		t.Fatalf("revoked principal must get CHAT_ACCESS_DENIED: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE phoenix_tenant_bindings SET state='SUSPENDED' WHERE tenant_id=$1`, w.TenantA); err != nil {
		t.Fatal(err)
	}
	p.InvalidateTenant(w.TenantA)
	if _, err := p.ResolveActor(ctx, w.SessA1, w.A1); err == nil || err.(*Error).Code != CodeAccessDenied {
		t.Fatalf("suspended tenant must close A1: %v", err)
	}
	if _, err := p.ResolveActor(ctx, w.SessB1, w.B1); err != nil {
		t.Fatalf("suspending A must not affect B: %v", err)
	}
}

// QUERY-NONORACLE: batch [A1,A2,B1,unknown] returns only A1/A2, the cap is 200, the predicate filters
// inside the query before DISTINCT/LIMIT/COUNT, and the query count does not grow with tenant size.
func TestQueryNonOracle(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()
	a1, err := p.ResolveActor(ctx, w.SessA1, w.A1)
	if err != nil {
		t.Fatal(err)
	}
	unknown := "zzzzzzzzzzzzzzzzzzzzzzzzzz"

	before := f.count.Load()
	got, err := p.FilterUserIDs(ctx, a1, []string{w.A1, w.A2, w.B1, unknown})
	if err != nil || len(got) != 2 || got[0] != w.A1 || got[1] != w.A2 {
		t.Fatalf("batch must return exactly [A1 A2], got %v err=%v", got, err)
	}
	if q := f.count.Load() - before; q != 1 {
		t.Fatalf("one batch must cost exactly one query, used %d", q)
	}

	// 201 ids: 400 and no query at all
	many := make([]string, 201)
	for i := range many {
		many[i] = f.id("bx")
	}
	before = f.count.Load()
	_, err = p.FilterUserIDs(ctx, a1, many)
	var pe *Error
	if !errors.As(err, &pe) || pe.Status != http.StatusBadRequest || f.count.Load() != before {
		t.Fatalf("201 ids must be a 400 before any query: %v (queries=%d)", err, f.count.Load()-before)
	}
	if _, err := p.FilterUserIDs(ctx, a1, many[:200]); err != nil {
		t.Fatalf("exactly 200 ids is allowed: %v", err)
	}

	// the predicate is part of the query itself: count and DISTINCT/LIMIT see only the tenant
	for i := 0; i < 40; i++ { // grow tenant B; tenant A answers must not change
		id := f.id("bu")
		f.insert("users", map[string]any{"id": id, "username": f.name("bulk"), "email": f.name("bulk") + "@t.invalid", "createat": 1})
		f.insert("phoenix_user_bindings", map[string]any{"issuer": testIssuer, "sub": "bulk-" + id, "tenant_id": w.TenantB, "native_user_id": id, "actor_kind": "CUSTOMER", "access_state": "GRANTED", "access_version": 1, "created_at": 1, "updated_at": 1})
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM users WHERE `+dollar(TenantUsersPredicate("users.id")), w.TenantA).Scan(&n); err != nil || n != 2 {
		t.Fatalf("tenant A must count exactly 2 users inside the query, got %d err=%v", n, err)
	}
	var page []string
	rows, err := f.db.Query(`SELECT DISTINCT id FROM users WHERE `+dollar(TenantUsersPredicate("users.id"))+` ORDER BY id LIMIT 1`, w.TenantA)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		page = append(page, id)
	}
	rows.Close()
	if len(page) != 1 || (page[0] != w.A1 && page[0] != w.A2) {
		t.Fatalf("LIMIT 1 page must be a tenant A user, got %v", page)
	}
	before = f.count.Load()
	if got, _ = p.FilterUserIDs(ctx, a1, []string{w.A1, w.A2, w.B1, unknown}); len(got) != 2 {
		t.Fatalf("growing B changed A's answer: %v", got)
	}
	if q := f.count.Load() - before; q != 1 {
		t.Fatalf("query count must not grow with tenant size, used %d", q)
	}
}

// FILE-OWNERSHIP: posts, threads, files (attached and unattached) and avatars-by-user are owned
// through channel and creator; a known valid foreign id is denied before any bytes are produced.
func TestFileOwnership(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()
	a1, _ := p.ResolveActor(ctx, w.SessA1, w.A1)
	b1, _ := p.ResolveActor(ctx, w.SessB1, w.B1)

	postA, postB := f.id("po"), f.id("po")
	f.insert("posts", map[string]any{"id": postA, "channelid": w.ChanA, "userid": w.A2, "createat": 1, "updateat": 1})
	f.insert("posts", map[string]any{"id": postB, "channelid": w.ChanB, "userid": w.B1, "createat": 1, "updateat": 1})
	fileA, fileB, loose := f.id("fi"), f.id("fi"), f.id("fi")
	f.insert("fileinfo", map[string]any{"id": fileA, "creatorid": w.A2, "postid": postA, "channelid": w.ChanA, "createat": 1, "updateat": 1, "path": "a/file.png"})
	f.insert("fileinfo", map[string]any{"id": fileB, "creatorid": w.B1, "postid": postB, "channelid": w.ChanB, "createat": 1, "updateat": 1, "path": "b/file.png"})
	f.insert("fileinfo", map[string]any{"id": loose, "creatorid": w.A1, "postid": "", "channelid": "", "createat": 1, "updateat": 1, "path": "a/loose.png"})

	// positive controls: same-tenant access passes the tenant half (native membership still applies after it)
	for kind, id := range map[ResourceKind]string{ResPost: postA, ResFile: fileA} {
		if err := p.CanAccessResource(ctx, a1, kind, id); err != nil {
			t.Fatalf("A1 must reach own tenant resource kind %d: %v", kind, err)
		}
	}
	if err := p.CanAccessResource(ctx, a1, ResFile, loose); err != nil {
		t.Fatalf("A1 must reach its own unattached upload: %v", err)
	}
	// foreign post/thread/reaction/pin target, attachment download/preview/thumbnail (all file id routes), loose upload
	for _, tc := range []struct {
		kind ResourceKind
		id   string
		who  ActorContext
	}{
		{ResPost, postB, a1}, {ResFile, fileB, a1}, {ResPost, postA, b1}, {ResFile, fileA, b1}, {ResFile, loose, b1},
	} {
		if err := p.CanAccessResource(ctx, tc.who, tc.kind, tc.id); !isNotFound(err) {
			t.Fatalf("foreign kind %d id %s must be 404, got %v", tc.kind, tc.id, err)
		}
	}
	// same shape as an id that does not exist at all
	if e1, e2 := p.CanAccessResource(ctx, a1, ResFile, fileB), p.CanAccessResource(ctx, a1, ResFile, "zzzzzzzzzzzzzzzzzzzzzzzzzz"); e1.Error() != e2.Error() {
		t.Fatalf("a foreign file and an unknown file must be indistinguishable: %v vs %v", e1, e2)
	}
}

// WS-BOUNDARY: delivery is decided per recipient and per event, including replay; foreign channels,
// foreign subjects (status/typing/user_updated) and unknown unscoped events are dropped.
func TestWSBoundary(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()

	cases := []struct {
		name string
		who  string
		sess string
		ev   EventInfo
		want bool
	}{
		{"posted in own channel", w.A1, w.SessA1, EventInfo{Type: "posted", ChannelID: w.ChanA, Subjects: []string{w.A2}}, true},
		{"posted in foreign channel", w.A1, w.SessA1, EventInfo{Type: "posted", ChannelID: w.ChanB, Subjects: []string{w.B1}}, false},
		{"typing of foreign user", w.A1, w.SessA1, EventInfo{Type: "typing", ChannelID: w.ChanA, Subjects: []string{w.B1}}, false},
		{"typing of same-tenant user", w.A1, w.SessA1, EventInfo{Type: "typing", ChannelID: w.ChanA, Subjects: []string{w.A2}}, true},
		{"status_change of foreign user", w.A1, w.SessA1, EventInfo{Type: "status_change", Subjects: []string{w.B1}}, false},
		{"status_change of same-tenant user", w.A1, w.SessA1, EventInfo{Type: "status_change", Subjects: []string{w.A2}}, true},
		{"user_updated foreign", w.B1, w.SessB1, EventInfo{Type: "user_updated", Subjects: []string{w.A1}}, false},
		{"team event foreign team", w.A1, w.SessA1, EventInfo{Type: "update_team", TeamID: w.TeamB}, false},
		{"team event own team", w.A1, w.SessA1, EventInfo{Type: "update_team", TeamID: w.TeamA}, true},
		{"unknown unscoped event", w.A1, w.SessA1, EventInfo{Type: "emoji_added"}, false},
		{"allowlisted unscoped event", w.A1, w.SessA1, EventInfo{Type: "hello", TargetUserID: w.A1}, true},
		{"event addressed to someone else", w.A1, w.SessA1, EventInfo{Type: "preferences_changed", TargetUserID: w.A2}, false},
		{"mixed foreign subject on own channel", w.A1, w.SessA1, EventInfo{Type: "user_added", ChannelID: w.ChanA, Subjects: []string{w.A2, w.B1}}, false},
	}
	for _, c := range cases {
		if got := p.AllowEvent(ctx, c.sess, c.who, c.ev); got != c.want {
			t.Errorf("%s: AllowEvent = %v, want %v", c.name, got, c.want)
		}
	}

	// the same decision applies to a replayed (dead-queue) event, and a membership/access change
	// takes effect immediately after invalidation
	ev := EventInfo{Type: "posted", ChannelID: w.ChanA, Subjects: []string{w.A2}}
	if !p.AllowEvent(ctx, w.SessA1, w.A1, ev) {
		t.Fatal("precondition: event allowed before revoke")
	}
	if _, err := f.db.Exec(`UPDATE phoenix_user_bindings SET access_state='REVOKED', access_version=2 WHERE sub='sub-a1'`); err != nil {
		t.Fatal(err)
	}
	p.InvalidateUser(w.A1)
	if p.AllowEvent(ctx, w.SessA1, w.A1, ev) {
		t.Fatal("a revoked recipient must receive nothing, including queued/replayed events")
	}
}
