// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package app

import (
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// phoenixViewRestrictions applies the tenant boundary to user visibility. handled=false means
// the boundary is not enforced for this process and the native computation applies unchanged.
// For an enforced boundary the result is always non-nil for non-operators: the tenant predicate is
// ANDed with whatever the native permission model returns, and an unbound principal sees nobody.
func (a *App) phoenixViewRestrictions(rctx request.CTX, userID string, native func() (*model.ViewUsersRestrictions, *model.AppError)) (*model.ViewUsersRestrictions, *model.AppError, bool) {
	pol := phoenix.Default()
	if pol == nil {
		return nil, nil, false
	}
	internal := func(err error) *model.AppError {
		return model.NewAppError("phoenixViewRestrictions", "api.phoenix.policy_unavailable", nil, "", http.StatusServiceUnavailable).Wrap(err)
	}
	ctx := rctx.Context()
	if actor, ok := phoenix.ActorFrom(ctx); ok && actor.UserID == userID && actor.Operator {
		r, e := native()
		return r, e, true
	}
	op, err := pol.IsOperator(ctx, userID)
	if err != nil {
		return nil, internal(err), true
	}
	if op {
		r, e := native()
		return r, e, true
	}
	tenant, err := pol.TenantOfUser(ctx, userID)
	if err != nil {
		return nil, internal(err), true
	}
	if tenant == "" {
		return &model.ViewUsersRestrictions{Teams: []string{}, Channels: []string{}}, nil, true
	}
	r, e := native()
	if e != nil {
		return nil, e, true
	}
	if r == nil {
		r = &model.ViewUsersRestrictions{}
	}
	r.PhoenixTenantID = tenant
	return r, nil, true
}

// phoenixUserVisible is the tenant half of UserCanSeeOtherUser: a principal may only see
// principals of its own tenant; operators are exempt; unbound users see nobody.
func (a *App) phoenixUserVisible(rctx request.CTX, userID, otherUserID string) (visible bool, handled bool, appErr *model.AppError) {
	pol := phoenix.Default()
	if pol == nil {
		return false, false, nil
	}
	ctx := rctx.Context()
	if op, err := pol.IsOperator(ctx, userID); err != nil {
		return false, true, model.NewAppError("phoenixUserVisible", "api.phoenix.policy_unavailable", nil, "", http.StatusServiceUnavailable).Wrap(err)
	} else if op {
		return true, false, nil // operator: native rules continue to apply
	}
	same, err := pol.UsersShareTenant(ctx, userID, otherUserID)
	if err != nil {
		return false, true, model.NewAppError("phoenixUserVisible", "api.phoenix.policy_unavailable", nil, "", http.StatusServiceUnavailable).Wrap(err)
	}
	if !same {
		return false, true, nil
	}
	return true, false, nil // same tenant: native rules continue to apply (AND)
}

// phoenixDirectTenant enforces the DM / group-DM rule before a channel is created: every
// participant must be bound to the same tenant. It returns the tenant to record ownership for.
func (a *App) phoenixDirectTenant(rctx request.CTX, userIDs ...string) (string, *model.AppError) {
	pol := phoenix.Default()
	if pol == nil {
		return "", nil
	}
	tenant, err := pol.CommonTenant(rctx.Context(), userIDs)
	if err != nil {
		if pe, ok := err.(*phoenix.Error); ok && pe.Status == http.StatusNotFound {
			return "", model.NewAppError("phoenixDirectTenant", "api.context.404.app_error", nil, "", http.StatusNotFound)
		}
		return "", model.NewAppError("phoenixDirectTenant", "api.phoenix.policy_unavailable", nil, "", http.StatusServiceUnavailable).Wrap(err)
	}
	return tenant, nil
}

// phoenixBindChannel persists DM / group-DM ownership immediately after creation.
func (a *App) phoenixBindChannel(rctx request.CTX, channelID, tenant string) *model.AppError {
	pol := phoenix.Default()
	if pol == nil || tenant == "" {
		return nil
	}
	if err := pol.BindChannel(rctx.Context(), channelID, tenant); err != nil {
		return model.NewAppError("phoenixBindChannel", "api.phoenix.policy_unavailable", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return nil
}
