package platform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
			f := newPhoenixDeliveryFixture(t)
			th, wc, client, blocked, allowed, user := f.th, f.conn, f.client, f.blocked, f.allowed, f.th.BasicUser
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
			_, _, err := client.ReadMessage()
			require.Error(t, err, "no additional private payload may arrive")
		})
	}
}

// The suite's session boundary reads real native sessions, including reloads after
// invalidation. Existing native permissions/MFA remain supplied by the platform test suite.
type phoenixDeliverySuite struct {
	*mockSuite
	th *TestHelper
}

func (s *phoenixDeliverySuite) GetSession(token string) (*model.Session, *model.AppError) {
	session, err := s.th.Store.Session().Get(s.th.Context, token)
	if err != nil {
		return nil, model.NewAppError("fixture", "session missing", nil, "", 401)
	}
	return session, nil
}

type phoenixDeliveryFixture struct {
	th               *TestHelper
	conn             *WebConn
	client           *websocket.Conn
	blocked, allowed *model.Channel
}

func newPhoenixDeliveryFixture(t *testing.T) *phoenixDeliveryFixture {
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
	t.Cleanup(func() { phoenix.SetDefault(nil) })
	upgraded := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			upgraded <- conn
		}
	}))
	t.Cleanup(srv.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[4:], nil)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	server := <-upgraded
	t.Cleanup(func() { server.Close() })
	wc := th.Service.NewWebConn(&WebConnConfig{WebSocket: server, Session: *session, ConnectionID: model.NewId()}, &phoenixDeliverySuite{mockSuite: &mockSuite{}, th: th}, &hookRunner{})

	return &phoenixDeliveryFixture{th: th, conn: wc, client: client, blocked: blocked, allowed: allowed}
}

// R2-001 mutations: remove shared cache synchronization, use queue admission in
// the writer, or skip native/Phoenix restrictions. Three goroutines exercise
// actual hub admission/enqueue, cache/session invalidation and socket writer.
// Reconnect replay is owned by the writer, never drained concurrently by the test.
func TestPhoenixConcurrentDelivery(t *testing.T) {
	for _, mode := range []string{"write", "replay"} {
		t.Run(mode, func(t *testing.T) {
			f := newPhoenixDeliveryFixture(t)
			wc, th := f.conn, f.th
			th.Service.UpdateConfig(func(c *model.Config) { *c.ServiceSettings.EnableWebHubChannelIteration = false })
			require.False(t, *th.Service.Config().ServiceSettings.EnableWebHubChannelIteration)
			allowed := func(marker string) *model.WebSocketEvent {
				e := model.NewWebSocketEvent(model.WebsocketEventPosted, "", f.allowed.Id, "", nil, "")
				e.Add("marker", marker)
				return e
			}
			removed := model.NewWebSocketEvent(model.WebsocketEventPosted, "", f.blocked.Id, "", nil, "")
			removed.Add("marker", "removed-private")
			require.True(t, wc.ShouldSendEvent(removed))
			if mode == "replay" {
				wc.Sequence = 1
				wc.addToDeadQueue(removed.SetSequence(1))
				wc.addToDeadQueue(allowed("old-allowed").SetSequence(2))
			} else {
				wc.send <- removed
			}
			require.NoError(t, th.Store.Channel().RemoveMember(th.Context, f.blocked.Id, wc.UserId))
			// The connection-index remains confined to the broadcaster goroutine, as in Hub.Start.
			index := newHubConnectionIndex(time.Minute, th.Store, th.Service.logger, false)
			require.NoError(t, index.Add(wc))
			hub := &Hub{platform: th.Service}
			const frames = 48
			begin := make(chan struct{})
			var wg sync.WaitGroup
			var hubChecks, invalidations atomic.Int32
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-begin
				for i := 0; i < frames; i++ {
					hub.broadcastToConn(index, wc, allowed(fmt.Sprintf("allowed-%d", i)), nil, nil, nil)
					hub.broadcastToConn(index, wc, removed, nil, nil, nil)
					hubChecks.Add(2)
				}
			}()
			go func() {
				defer wg.Done()
				<-begin
				for i := 0; i < frames; i++ {
					wc.InvalidateCache()
					// Force the real cache-expiry branch under its declared ownership boundary.
					wc.eventMu.Lock()
					wc.lastAllChannelMembersTime = 0
					wc.eventMu.Unlock()
					wc.ShouldSendEvent(allowed("predicate-only"))
					invalidations.Add(1)
				}
			}()
			done := make(chan struct{})
			go func() { <-begin; wc.writePump(); close(done) }()
			defer func() { wg.Wait(); close(wc.endWritePump); <-done }()
			close(begin)
			require.NoError(t, f.client.SetReadDeadline(time.Now().Add(20*time.Second)))
			seen := map[string]bool{}
			hello := false
			for len(seen) < frames {
				var frame map[string]any
				require.NoError(t, f.client.ReadJSON(&frame))
				data := frame["data"].(map[string]any)
				if frame["event"] == "hello" {
					hello = true
					require.Equal(t, float64(0), frame["seq"])
					continue
				}
				marker, _ := data["marker"].(string)
				require.NotEqual(t, "removed-private", marker)
				if marker != "old-allowed" {
					require.False(t, seen[marker])
					seen[marker] = true
				}
			}
			wg.Wait()
			// A FIFO barrier after both producers proves no trailing removed event was
			// hidden by stopping the observer as soon as the last positive arrived.
			wc.send <- allowed("after-concurrency")
			var barrier map[string]any
			require.NoError(t, f.client.ReadJSON(&barrier))
			require.Equal(t, "after-concurrency", barrier["data"].(map[string]any)["marker"])
			require.Equal(t, int32(frames*2), hubChecks.Load())
			require.Equal(t, int32(frames), invalidations.Load())
			if mode == "replay" {
				require.True(t, hello, "numbered removed event must trigger native resync")
			}
		})
	}
}

// Queue shedding is hub-only. Already queued/replayed events retain all native
// authorization restrictions but must not mutate slow-log state or get shed again.
func TestPhoenixDeliveryAdmissionSeparation(t *testing.T) {
	f := newPhoenixDeliveryFixture(t)
	wc := f.conn
	wc.Active.Store(true)
	for i := 0; i < sendSlowWarn; i++ {
		wc.send <- model.NewWebSocketEvent(model.WebsocketEventHello, "", "", wc.UserId, nil, "")
	}
	event := model.NewWebSocketEvent(model.WebsocketEventTyping, "", f.allowed.Id, "", nil, "")
	wc.eventMu.Lock()
	wc.lastLogTimeSlow = time.Time{}
	wc.eventMu.Unlock()
	require.True(t, wc.shouldDeliverEvent(event))
	wc.eventMu.Lock()
	require.True(t, wc.lastLogTimeSlow.IsZero())
	wc.eventMu.Unlock()
	require.False(t, wc.ShouldSendEvent(event), "slow queue must still shed new typing admission")
	var wg sync.WaitGroup
	var wrong atomic.Bool
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range 32 {
			if !wc.shouldDeliverEvent(event) {
				wrong.Store(true)
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 32 {
			if wc.ShouldSendEvent(event) {
				wrong.Store(true)
			}
		}
	}()
	close(start)
	wg.Wait()
	require.False(t, wrong.Load(), "concurrent delivery and admission retain distinct slow-queue behavior")
	for _, tc := range []struct {
		name      string
		broadcast *model.WebsocketBroadcast
		want      bool
	}{
		{"member", &model.WebsocketBroadcast{ChannelId: f.allowed.Id}, true},
		{"omitted-user", &model.WebsocketBroadcast{ChannelId: f.allowed.Id, OmitUsers: map[string]bool{wc.UserId: true}}, false},
		{"other-connection", &model.WebsocketBroadcast{ChannelId: f.allowed.Id, ConnectionId: model.NewId()}, false},
		{"omitted-connection", &model.WebsocketBroadcast{ChannelId: f.allowed.Id, OmitConnectionId: wc.GetConnectionID()}, false},
		{"sanitized-sensitive", &model.WebsocketBroadcast{ChannelId: f.allowed.Id, ContainsSanitizedData: true, ContainsSensitiveData: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := model.NewWebSocketEvent(model.WebsocketEventPosted, "", "", "", nil, "").SetBroadcast(tc.broadcast)
			require.Equal(t, tc.want, wc.shouldDeliverEvent(e))
		})
	}
}
