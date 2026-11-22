// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package phoenix

import (
	"context"
	"testing"
)

func TestWSPlaybooksPostBoundary(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()
	f.insert("channelmembers", map[string]any{"channelid": w.ChanA, "userid": w.A1, "roles": "channel_user"})
	if _, err := f.db.Exec(`UPDATE channels SET deleteat=0 WHERE id=$1`, w.ChanA); err != nil {
		t.Fatal(err)
	}
	bot := w.Loner // deliberately unbound; ownership, not username, establishes the bot
	if _, err := f.db.Exec(`UPDATE users SET deleteat=0 WHERE id=$1`, bot); err != nil {
		t.Fatal(err)
	}
	f.insert("bots", map[string]any{"userid": bot, "ownerid": "playbooks", "deleteat": 0})
	post := f.id("po")
	f.insert("posts", map[string]any{"id": post, "userid": bot, "channelid": w.ChanA, "message": "Approval result"})
	event := EventInfo{Type: "posted", ChannelID: w.ChanA, Subjects: []string{bot}, PostID: post}
	check := func(name, session, user string, ev EventInfo, want bool) {
		t.Helper()
		if got := p.AllowEvent(ctx, session, user, ev); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	for _, kind := range []string{"posted", "post_edited", "post_deleted"} {
		ev := event
		ev.Type = kind
		check(kind, w.SessA1, w.A1, ev, true)
	}
	check("foreign recipient", w.SessB1, w.B1, event, false)
	check("same-tenant nonmember", w.SessA2, w.A2, event, false)
	for name, change := range map[string]func(*EventInfo){
		"no post":            func(e *EventInfo) { e.PostID = "" },
		"unknown post":       func(e *EventInfo) { e.PostID = "zzzzzzzzzzzzzzzzzzzzzzzzzz" },
		"no channel":         func(e *EventInfo) { e.ChannelID = "" },
		"foreign channel":    func(e *EventInfo) { e.ChannelID = w.ChanB },
		"foreign team":       func(e *EventInfo) { e.TeamID = w.TeamB },
		"different target":   func(e *EventInfo) { e.TargetUserID = w.A2 },
		"foreign co-subject": func(e *EventInfo) { e.Subjects = []string{bot, w.B1} },
		"typing":             func(e *EventInfo) { e.Type = "typing" },
		"profile event":      func(e *EventInfo) { e.Type = "user_updated" },
	} {
		ev := event
		change(&ev)
		check(name, w.SessA1, w.A1, ev, false)
	}
	otherChannel := f.id("ch")
	f.insert("channels", map[string]any{"id": otherChannel, "teamid": w.TeamA, "name": "other-channel", "deleteat": 0})
	f.insert("channelmembers", map[string]any{"channelid": otherChannel, "userid": w.A1, "roles": "channel_user"})
	wrongChannel := event
	wrongChannel.ChannelID = otherChannel
	check("post belongs to different same-tenant channel", w.SessA1, w.A1, wrongChannel, false)

	// A forged Playbooks username, arbitrary unbound author, or another plugin's bot
	// must not gain a tenant exception.
	for _, owner := range []string{"", "other-plugin"} {
		author := f.id("us")
		f.insert("users", map[string]any{"id": author, "username": f.name("playbooks"), "deleteat": 0})
		if owner != "" {
			f.insert("bots", map[string]any{"userid": author, "ownerid": owner, "deleteat": 0})
		}
		id := f.id("po")
		f.insert("posts", map[string]any{"id": id, "userid": author, "channelid": w.ChanA})
		ev := event
		ev.PostID, ev.Subjects = id, []string{author}
		check("untrusted author "+owner, w.SessA1, w.A1, ev, false)
		// A real bot ID in an unrelated subject cannot authorize someone else's post.
		ev.Subjects = []string{bot}
		check("mismatched persisted author "+owner, w.SessA1, w.A1, ev, false)
	}
	for _, table := range []string{"bots", "users"} {
		key := "userid"
		if table == "users" {
			key = "id"
		}
		if _, err := f.db.Exec(`UPDATE `+table+` SET deleteat=1 WHERE `+key+`=$1`, bot); err != nil {
			t.Fatal(err)
		}
		check("queued event after "+table+" deletion", w.SessA1, w.A1, event, false)
		if _, err := f.db.Exec(`UPDATE `+table+` SET deleteat=0 WHERE `+key+`=$1`, bot); err != nil {
			t.Fatal(err)
		}
		check("restored "+table, w.SessA1, w.A1, event, true)
	}
	if _, err := f.db.Exec(`DELETE FROM channelmembers WHERE channelid=$1 AND userid=$2`, w.ChanA, w.A1); err != nil {
		t.Fatal(err)
	}
	check("queued event after membership removal", w.SessA1, w.A1, event, false)
	f.insert("channelmembers", map[string]any{"channelid": w.ChanA, "userid": w.A1, "roles": "channel_user"})
	check("membership restored", w.SessA1, w.A1, event, true)
	f.insert("phoenix_user_bindings", map[string]any{"issuer": testIssuer, "sub": "bound-bot", "tenant_id": w.TenantB, "native_user_id": bot, "actor_kind": "CUSTOMER", "access_state": "GRANTED", "access_version": 1})
	check("bot bound to foreign tenant", w.SessA1, w.A1, event, false)
}
