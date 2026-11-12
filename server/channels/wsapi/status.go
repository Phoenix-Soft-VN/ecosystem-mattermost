// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package wsapi

import (
	"context"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

func (api *API) InitStatus() {
	api.Router.Handle("get_statuses", api.APIWebSocketHandler(api.getStatuses))
	api.Router.Handle("get_statuses_by_ids", api.APIWebSocketHandler(api.getStatusesByIds))
}

// phoenixStatusActor resolves the caller for the status actions when the tenant boundary is
// enforced. It also spends the per-session budget (30/min). pol == nil means not enforced.
func phoenixStatusActor(req *model.WebSocketRequest) (*phoenix.Policy, phoenix.ActorContext, *model.AppError) {
	pol := phoenix.Default()
	if pol == nil {
		return nil, phoenix.ActorContext{}, nil
	}
	actor, err := pol.ResolveActor(context.Background(), req.Session.Id, req.Session.UserId)
	if err != nil {
		if pe, ok := err.(*phoenix.Error); ok {
			return nil, actor, model.NewAppError(req.Action, pe.Code, nil, "", pe.Status)
		}
		return nil, actor, model.NewAppError(req.Action, phoenix.CodeNotReady, nil, "", http.StatusServiceUnavailable).Wrap(err)
	}
	if ok, _ := phoenix.AllowStatusAction(req.Session.Id); !ok {
		return nil, actor, model.NewAppError(req.Action, phoenix.CodeRateLimited, nil, "", http.StatusTooManyRequests)
	}
	return pol, actor, nil
}

func (api *API) getStatuses(req *model.WebSocketRequest) (map[string]any, *model.AppError) {
	pol, actor, appErr := phoenixStatusActor(req)
	if appErr != nil {
		return nil, appErr
	}
	if pol != nil && !actor.Operator {
		// Bounded tenant query, then a multi-get of those ids; the global status map is never scanned.
		ids, err := pol.TenantUserIDs(context.Background(), actor.TenantID, 5000)
		if err != nil {
			return nil, model.NewAppError(req.Action, phoenix.CodeNotReady, nil, "", http.StatusServiceUnavailable).Wrap(err)
		}
		statusMap, sErr := api.App.Srv().Platform().GetStatusesByIds(ids)
		if sErr != nil {
			return nil, sErr
		}
		return statusMap, nil
	}
	statusMap := api.App.Srv().Platform().GetAllStatuses()
	return model.StatusMapToInterfaceMap(statusMap), nil
}

func (api *API) getStatusesByIds(req *model.WebSocketRequest) (map[string]any, *model.AppError) {
	pol, actor, appErr := phoenixStatusActor(req)
	if appErr != nil {
		return nil, appErr
	}
	var userIds []string
	if userIds = model.ArrayFromInterface(req.Data["user_ids"]); len(userIds) == 0 {
		mlog.Debug("Error while parsing user_ids", mlog.String("data", model.StringInterfaceToJSON(req.Data)))
		return nil, NewInvalidWebSocketParamError(req.Action, "user_ids")
	}
	if pol != nil {
		allowed, err := pol.FilterUserIDs(context.Background(), actor, userIds)
		if err != nil {
			if pe, ok := err.(*phoenix.Error); ok && pe.Status == http.StatusBadRequest {
				return nil, NewInvalidWebSocketParamError(req.Action, "user_ids")
			}
			return nil, model.NewAppError(req.Action, phoenix.CodeNotReady, nil, "", http.StatusServiceUnavailable).Wrap(err)
		}
		userIds = allowed
		if len(userIds) == 0 {
			return map[string]any{}, nil
		}
	}

	statusMap, err := api.App.Srv().Platform().GetStatusesByIds(userIds)
	if err != nil {
		return nil, err
	}

	return statusMap, nil
}
