// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package platform

import (
	"encoding/json"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPhoenixPostEventIdentity(t *testing.T) {
	post := &model.Post{Id: model.NewId(), ChannelId: model.NewId(), UserId: model.NewId()}
	encoded, err := json.Marshal(post)
	require.NoError(t, err)
	for _, payload := range []any{string(encoded), map[string]any{"id": post.Id, "user_id": post.UserId}, post} {
		event := model.NewWebSocketEvent(model.WebsocketEventPosted, "", post.ChannelId, "", nil, "")
		event.Add("post", payload)
		require.Equal(t, post.Id, eventPostID(event))
		require.Contains(t, eventSubjects(event), post.UserId)
	}
	for _, payload := range []any{"invalid json", map[string]any{"id": 42}, (*model.Post)(nil), nil} {
		event := model.NewWebSocketEvent(model.WebsocketEventPosted, "", post.ChannelId, "", nil, "")
		event.Add("post", payload)
		require.Empty(t, eventPostID(event))
	}
}
