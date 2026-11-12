// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package slashcommands

import (
	"context"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/i18n"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/app"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// TaskProvider is the built-in `/task <title>` command. It is not an installable integration:
// it only exists when the Phoenix boundary and task adapter are configured, authorizes the caller
// against the tenant bindings server-side, and forwards a signed envelope to the Gateway.
type TaskProvider struct{}

const CmdTask = "task"

func init() {
	app.RegisterCommandProvider(&TaskProvider{})
}

func (*TaskProvider) GetTrigger() string { return CmdTask }

func (*TaskProvider) GetCommand(a *app.App, T i18n.TranslateFunc) *model.Command {
	if phoenix.DefaultTask() == nil {
		return nil // not offered outside a configured Phoenix deployment
	}
	return &model.Command{
		Trigger:          CmdTask,
		AutoComplete:     true,
		AutoCompleteDesc: "Tạo công việc mới trong Tasks",
		AutoCompleteHint: "[tiêu đề công việc]",
		DisplayName:      "Task",
	}
}

func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{Text: text, ResponseType: model.CommandResponseTypeEphemeral}
}

func (*TaskProvider) DoCommand(a *app.App, rctx request.CTX, args *model.CommandArgs, message string) *model.CommandResponse {
	task, pol := phoenix.DefaultTask(), phoenix.Default()
	if task == nil || pol == nil {
		return ephemeral("Lệnh /task chưa được bật.")
	}
	title := strings.TrimSpace(message)
	if title == "" {
		return ephemeral("Vui lòng nhập tiêu đề: /task <tiêu đề>")
	}
	ctx, cancel := context.WithTimeout(rctx.Context(), 12*time.Second)
	defer cancel()
	actor, err := pol.ResolveActor(ctx, rctx.Session().Id, args.UserId)
	if err != nil || actor.Operator {
		return ephemeral("Bạn chưa được cấp quyền sử dụng Chat.")
	}
	if cerr := pol.CanAccessResource(ctx, actor, phoenix.ResChannel, args.ChannelId); cerr != nil {
		return ephemeral("Không tìm thấy kênh.")
	}
	endpoint, err := pol.TaskEndpointID(ctx, actor.TenantID)
	if err != nil || endpoint == "" {
		return ephemeral("Tích hợp Tasks chưa được cấu hình cho đơn vị này.")
	}
	// The request id is derived from the command's own identity, so a re-submitted command maps to
	// the Gateway's stored result and never creates a second task.
	reply, rerr := task.Execute(ctx, endpoint, phoenix.TaskEnvelope{
		RequestID:    args.RootId + ":" + args.UserId + ":" + args.ChannelId + ":" + phoenix.ShortHash(title, args.TriggerId),
		NativeUserID: args.UserId, ChannelID: args.ChannelId, Text: title,
	})
	if rerr != nil && reply.Text == "" {
		return ephemeral("Chưa thể tạo công việc. Vui lòng thử lại sau.")
	}
	return ephemeral(reply.Text)
}
