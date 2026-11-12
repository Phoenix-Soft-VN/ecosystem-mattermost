// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package platform

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// eventSubjects extracts the user ids a payload refers to. Anything that names a user must be
// a principal of the recipient's tenant, even on an otherwise scoped event.
func eventSubjects(evt *model.WebSocketEvent) []string {
	data := evt.GetData()
	var out []string
	for _, k := range []string{"user_id", "other_user_id", "teammate_id", "creator_id", "userId", "sender_id"} {
		if v, ok := data[k].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	if u, ok := data["user"].(*model.User); ok && u != nil {
		out = append(out, u.Id)
	}
	// Native broadcasts commonly serialize their model into a JSON string before enqueueing.
	for _, key := range []string{"user", "post", "reaction", "channel_member", "team_member"} {
		var object map[string]any
		switch value := data[key].(type) {
		case string:
			if json.Unmarshal([]byte(value), &object) != nil {
				continue
			}
		case map[string]any:
			object = value
		}
		for _, field := range []string{"user_id", "creator_id"} {
			if id, ok := object[field].(string); ok && id != "" {
				out = append(out, id)
			}
		}
		if key == "user" {
			if id, ok := object["id"].(string); ok && id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// phoenixAllowsEvent revalidates one event for this connection. It runs in ShouldSendEvent
// (before the send buffer), in writePump (events that waited in the buffer while access changed)
// and in drainDeadQueue/writeMessage (reconnect replay), never only at subscribe time.
func (wc *WebConn) phoenixAllowsEvent(evt *model.WebSocketEvent) bool {
	pol := phoenix.Default()
	if pol == nil {
		return true
	}
	session := wc.GetSession()
	if session == nil {
		return false
	}
	b := evt.GetBroadcast()
	return pol.AllowEvent(context.Background(), session.Id, wc.UserId, phoenix.EventInfo{
		Type:         string(evt.EventType()),
		ChannelID:    b.ChannelId,
		TeamID:       b.TeamId,
		TargetUserID: b.UserId,
		Subjects:     eventSubjects(evt),
	})
}

// phoenixLivenessInterval is the background liveness period for WebSocket connections. With the
// bounded authority cache and refresh deadline, revocation stays inside the 30 s contract.
const phoenixLivenessInterval = 5 * time.Second

// phoenixStillAuthorized re-resolves the connection's actor through the live Gateway authority.
// Unauthenticated sockets (no session yet) are left to the native auth challenge.
func (wc *WebConn) phoenixStillAuthorized() bool {
	pol := phoenix.Default()
	if pol == nil {
		return true
	}
	// Native cache invalidation clears the cached session of an existing socket.
	// Reload it through native authentication; nil must not turn a revoked
	// authenticated connection into a fresh challenge connection.
	if wc.UserId == "" {
		return true
	}
	if !wc.IsBasicAuthenticated() {
		return false
	}
	session := wc.GetSession()
	if session == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return pol.SessionValid(ctx, session.Id, wc.UserId) == nil
}
