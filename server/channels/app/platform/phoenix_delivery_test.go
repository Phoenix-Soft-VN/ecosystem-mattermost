package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
)

// R1-001 mutation: remove the delivery/replay guard or replace fresh membership with queue-time state.
// Both paths write real WebSocket frames, backed by native PostgreSQL and its member store.
func TestPhoenixQueuedPrivateMembership(t *testing.T) {
	for _, boundary := range []string{"write", "replay-allowed", "replay-removed"} {
		t.Run(boundary, func(t *testing.T) {
			th := Setup(t).InitBasic(t)
			db := th.Store.GetInternalMasterDB()
			exec := func(q string, args ...any) { _, err := db.Exec(q, args...); require.NoError(t, err) }
			team, user := th.BasicTeam, th.BasicUser
			exec(`UPDATE teams SET type='I',allowopeninvite=false WHERE id=$1`, team.Id)
			blocked := th.BasicChannel
			allowed := th.CreateChannel(t, team)
			for _, ch := range []*model.Channel{blocked, allowed} {
				exec(`UPDATE channels SET type='P' WHERE id=$1`, ch.Id)
				_, err := th.Store.Channel().SaveMember(th.Context, &model.ChannelMember{ChannelId: ch.Id, UserId: user.Id, Roles: "channel_user", NotifyProps: model.GetDefaultChannelNotifyProps()})
				require.NoError(t, err)
			}
			session, err := th.Store.Session().Save(th.Context, &model.Session{UserId: user.Id, Roles: "system_user", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
			require.NoError(t, err)
			tenant := "queued-" + model.NewId()
			exec(`INSERT INTO phoenix_tenant_bindings(tenant_id,team_id,default_channel_id,state,policy_version,created_at,updated_at) VALUES($1,$2,$3,'ACTIVE',1,1,1)`, tenant, team.Id, allowed.Id)
			exec(`INSERT INTO phoenix_user_bindings(issuer,sub,tenant_id,native_user_id,actor_kind,access_state,access_version,created_at,updated_at) VALUES('https://auth.lab','recipient',$1,$2,'CUSTOMER','GRANTED',1,1,1)`, tenant, user.Id)
			exec(`INSERT INTO phoenix_session_bindings(session_id,issuer,sub,sid,tenant_id,native_user_id,access_version,expires_at) VALUES($1,'https://auth.lab','recipient','sid',$2,$3,1,$4)`, session.Id, tenant, user.Id, session.ExpiresAt)
			policy := phoenix.NewPolicy(db, nil)
			policy.SetDrift("", nil)
			phoenix.SetDefault(policy)
			defer phoenix.SetDefault(nil)
			upgraded := make(chan *websocket.Conn, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err == nil {
					upgraded <- conn
				}
			}))
			defer srv.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[4:], nil)
			require.NoError(t, err)
			defer client.Close()
			server := <-upgraded
			defer server.Close()
			wc := th.Service.NewWebConn(&WebConnConfig{WebSocket: server, Session: *session, ConnectionID: model.NewId()}, th.Suite, &hookRunner{})
			denied := model.NewWebSocketEvent(model.WebsocketEventPosted, "", blocked.Id, "", nil, "")
			denied.Add("marker", "queued-private")
			positive := model.NewWebSocketEvent(model.WebsocketEventPosted, "", allowed.Id, "", nil, "")
			positive.Add("marker", "allowed-member")
			require.True(t, wc.ShouldSendEvent(denied))
			require.True(t, wc.ShouldSendEvent(positive))
			oldConnectionID := wc.GetConnectionID()
			if boundary != "write" {
				wc.Sequence = 1
				wc.addToDeadQueue(denied.SetSequence(1))
				wc.addToDeadQueue(positive.SetSequence(2))
			} else {
				wc.send <- denied
				wc.send <- positive
			}
			if boundary != "replay-allowed" {
				require.NoError(t, th.Store.Channel().RemoveMember(th.Context, blocked.Id, user.Id))
			}
			// Leave the old membership cache populated: authorization must be fresh.
			done := make(chan struct{})
			go func() { wc.writePump(); close(done) }()
			defer func() { close(wc.endWritePump); <-done }()
			require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
			var frame map[string]any
			require.NoError(t, client.ReadJSON(&frame))
			if boundary == "replay-removed" {
				require.Equal(t, "hello", frame["event"], "filtered replay must reset the native stream, never serialize the removed private event")
				require.Equal(t, float64(0), frame["seq"])
				require.NotEqual(t, oldConnectionID, frame["data"].(map[string]any)["connection_id"])
				wc.send <- positive
				require.NoError(t, client.ReadJSON(&frame))
				require.Equal(t, float64(1), frame["seq"], "new authorized traffic must have the next native sequence")
			} else if boundary == "replay-allowed" {
				require.Equal(t, "queued-private", frame["data"].(map[string]any)["marker"], "member's queued private event must replay")
				require.Equal(t, float64(1), frame["seq"])
				require.NoError(t, client.ReadJSON(&frame))
				require.Equal(t, float64(2), frame["seq"])
			}
			require.Equal(t, "allowed-member", frame["data"].(map[string]any)["marker"], "removed channel's queued payload reached the wire")
			require.NoError(t, client.SetReadDeadline(time.Now().Add(150*time.Millisecond)))
			_, _, err = client.ReadMessage()
			require.Error(t, err, "no additional private payload may arrive")
		})
	}
}
