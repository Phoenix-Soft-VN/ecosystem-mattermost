// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package platform

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

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
		PostID:       eventPostID(evt),
	})
}
