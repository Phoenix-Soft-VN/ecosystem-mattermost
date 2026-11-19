// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package platform

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
)

// TestPhoenixInvalidatedSocket verifies that a WebConn with an invalidated or missing
// session is no longer authenticated and rejects WebSocket events fail-closed.
func TestPhoenixInvalidatedSocket(t *testing.T) {
	phoenix.SetDefault(phoenix.NewPolicy(nil, nil))
	t.Cleanup(func() { phoenix.SetDefault(nil) })

	wc := &WebConn{}
	wc.SetSessionToken("")
	require.False(t, wc.IsBasicAuthenticated(), "unauthenticated socket without token must not pass")

	userID := model.NewId()
	session := &model.Session{
		Id:        model.NewId(),
		UserId:    userID,
		ExpiresAt: model.GetMillis() + 60_000,
	}
	wc.UserId = userID
	wc.SetSession(session)
	wc.SetSessionExpiresAt(session.ExpiresAt)
	wc.SetSessionToken("tok-1")
	require.True(t, wc.IsBasicAuthenticated())

	wc.InvalidateCache()
	wc.SetSessionToken("")
	require.False(t, wc.IsBasicAuthenticated(), "invalidated socket without token must fail authentication")
	require.False(t, wc.phoenixAllowsEvent(model.NewWebSocketEvent(model.WebsocketEventPosted, "", "chan-1", userID, nil, "")), "socket without session must reject events fail-closed")
}

// TestPhoenixCloseSessionConns verifies immediate push-driven WebSocket session termination
// closes only the revoked session's connection while keeping sibling sessions open.
func TestPhoenixCloseSessionConns(t *testing.T) {
	connIndex := newHubConnectionIndex(time.Minute, nil, nil, false)
	userID := model.NewId()
	revokedSessID := model.NewId()
	otherSessID := model.NewId()

	wcRevoked := &WebConn{
		UserId: userID,
		send:   make(chan model.WebSocketMessage, 1),
	}
	wcRevoked.SetConnectionID(model.NewId())
	wcRevoked.SetSessionToken("tok-revoked")
	wcRevoked.SetSession(&model.Session{Id: revokedSessID, UserId: userID, ExpiresAt: model.GetMillis() + 60_000})
	wcRevoked.SetSessionExpiresAt(model.GetMillis() + 60_000)

	wcOther := &WebConn{
		UserId: userID,
		send:   make(chan model.WebSocketMessage, 1),
	}
	wcOther.SetConnectionID(model.NewId())
	wcOther.SetSessionToken("tok-other")
	wcOther.SetSession(&model.Session{Id: otherSessID, UserId: userID, ExpiresAt: model.GetMillis() + 60_000})
	wcOther.SetSessionExpiresAt(model.GetMillis() + 60_000)

	require.NoError(t, connIndex.Add(wcRevoked))
	require.NoError(t, connIndex.Add(wcOther))

	closeSessionConns(connIndex, webConnCloseSessionMessage{userID: userID, sessionID: revokedSessID})

	require.False(t, connIndex.Has(wcRevoked), "revoked session WebConn must be removed from hub index")
	require.Empty(t, wcRevoked.GetSessionToken())
	require.Nil(t, wcRevoked.GetSession())
	_, open := <-wcRevoked.send
	require.False(t, open, "revoked session WebConn send channel must be closed immediately")

	require.True(t, connIndex.Has(wcOther), "other active session for same user must stay connected")
	require.Equal(t, "tok-other", wcOther.GetSessionToken())
}
