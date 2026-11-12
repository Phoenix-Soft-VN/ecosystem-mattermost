// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package phoenix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Mutation: omit native membership, tenant/private-Team, durable revoke, or live authority.
func TestNotificationBoundary(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()
	f.db.Exec(`UPDATE channels SET deleteat=0`)
	f.insert("channelmembers", map[string]any{"channelid": w.ChanA, "userid": w.A1})
	dm := f.id("dm")
	f.insert("channels", map[string]any{"id": dm, "teamid": "", "type": "D", "name": "unbound-dm", "deleteat": 0})
	f.insert("channelmembers", map[string]any{"channelid": dm, "userid": w.A1})
	if p.NotificationAllowed(ctx, w.A1, []string{dm}) {
		t.Fatal("missing DM tenant binding must fail closed, not SQL NULL")
	}
	before := f.count.Load()
	if !p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}) {
		t.Fatal("eligible bound native member must receive")
	}
	if f.count.Load()-before != 1 {
		t.Fatal("one indexed query per recipient/channel batch required")
	}
	for _, who := range []string{w.B1, w.Op, w.Loner} {
		if p.NotificationAllowed(ctx, who, []string{w.ChanA}) {
			t.Fatal("foreign/unbound/operator must not receive customer content")
		}
	}
	if p.NotificationAllowed(ctx, w.A1, []string{w.ChanA, w.ChanB}) {
		t.Fatal("mixed tenant digest leaked")
	}
	f.db.Exec(`UPDATE phoenix_user_bindings SET access_state='REVOKED' WHERE native_user_id=$1`, w.A1)
	if p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}) {
		t.Fatal("durably revoked recipient received")
	}
	f.db.Exec(`UPDATE phoenix_user_bindings SET access_state='GRANTED' WHERE native_user_id=$1`, w.A1)
	mode := 0
	server := httptest.NewServer(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		if mode == 2 {
			wr.WriteHeader(503)
			return
		}
		json.NewEncoder(wr).Encode(AuthorityDecision{Active: true, ChatEntitled: mode == 0, MembershipState: "ACTIVE", TenantState: "ACTIVE", ExpiresAt: time.Now().Add(time.Hour)})
	}))
	defer server.Close()
	p.Authority = NewAuthority(&AuthorityClient{URL: server.URL, HTTP: server.Client(), Signer: &BridgeSigner{Secret: []byte("unit-only")}})
	if !p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}) {
		t.Fatal("live entitlement positive")
	}
	for _, denial := range []int{1, 2} {
		mode = denial
		p.Authority.Invalidate(w.SessA1)
		if p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}) {
			t.Fatal("missed revoke/outage must suppress queued content")
		}
	}
	f.db.Exec(`DELETE FROM sessions WHERE id=$1`, w.SessA1)
	if p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}) {
		t.Fatal("logout must suppress queued content")
	}
}
