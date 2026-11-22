// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package platform

import (
	"encoding/json"
	"github.com/mattermost/mattermost/server/public/model"
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
		case *model.Post:
			if key == "post" && value != nil {
				object = map[string]any{"user_id": value.UserId}
			}
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

func eventPostID(evt *model.WebSocketEvent) string {
	var post struct {
		ID string `json:"id"`
	}
	switch value := evt.GetData()["post"].(type) {
	case string:
		if json.Unmarshal([]byte(value), &post) == nil {
			return post.ID
		}
	case map[string]any:
		id, _ := value["id"].(string)
		return id
	case *model.Post:
		if value != nil {
			return value.Id
		}
	}
	return ""
}
