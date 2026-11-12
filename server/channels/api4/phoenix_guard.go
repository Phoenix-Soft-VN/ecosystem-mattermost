// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package api4

import (
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// These helpers cover ids that arrive in request bodies or multipart forms; ids in the URL path
// and query are checked once, before the handler, by web.Handler.phoenixGuard. All of them are
// no-ops when the tenant boundary is not enforced, and every denial has the same 404 shape.

func phoenixNotFound() *model.AppError {
	return model.NewAppError("phoenixGuard", "api.context.404.app_error", nil, "", http.StatusNotFound)
}

func phoenixActor(c *Context) (*phoenix.Policy, phoenix.ActorContext, bool) {
	pol := phoenix.Default()
	if pol == nil {
		return nil, phoenix.ActorContext{}, true
	}
	actor, ok := phoenix.ActorFrom(c.AppContext.Context())
	if !ok {
		c.Err = model.NewAppError("phoenixGuard", phoenix.CodeAuthInvalid, nil, "", http.StatusUnauthorized)
		return nil, actor, false
	}
	return pol, actor, true
}

func phoenixFail(c *Context, err error) {
	if pe, ok := err.(*phoenix.Error); ok {
		if pe.Status == http.StatusNotFound {
			c.Err = phoenixNotFound()
			return
		}
		c.Err = model.NewAppError("phoenixGuard", pe.Code, nil, pe.Message, pe.Status)
		return
	}
	c.Err = model.NewAppError("phoenixGuard", phoenix.CodeNotReady, nil, "", http.StatusServiceUnavailable).Wrap(err)
}

// phoenixRequireResource denies unless the resource belongs to the caller's tenant.
func phoenixRequireResource(c *Context, kind phoenix.ResourceKind, id string) bool {
	pol, actor, ok := phoenixActor(c)
	if !ok {
		return false
	}
	if pol == nil || id == "" {
		return true
	}
	if err := pol.CanAccessResource(c.AppContext.Context(), actor, kind, id); err != nil {
		phoenixFail(c, err)
		return false
	}
	return true
}

// phoenixRequireParticipants enforces that every user id in a body belongs to the caller's tenant.
func phoenixRequireParticipants(c *Context, userIDs []string) bool {
	pol, actor, ok := phoenixActor(c)
	if !ok {
		return false
	}
	if pol == nil {
		return true
	}
	if len(userIDs) > phoenix.MaxBatchIDs {
		c.Err = model.NewAppError("phoenixGuard", phoenix.CodeInvalidRequest, nil, "too many ids (max 200)", http.StatusBadRequest)
		return false
	}
	if err := pol.ParticipantsShareTenant(c.AppContext.Context(), actor, userIDs); err != nil {
		phoenixFail(c, err)
		return false
	}
	return true
}

// phoenixAllowedUserIDs enforces the batch cap (400 above 200) and drops foreign/unknown ids in
// one query. It returns the allowed ids in caller order.
func phoenixAllowedUserIDs(c *Context, ids []string) ([]string, bool) {
	pol, actor, ok := phoenixActor(c)
	if !ok {
		return nil, false
	}
	if pol == nil {
		return ids, true
	}
	out, err := pol.FilterUserIDs(c.AppContext.Context(), actor, ids)
	if err != nil {
		if pe, isPE := err.(*phoenix.Error); isPE && pe.Status == http.StatusBadRequest {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeInvalidRequest, nil, pe.Message, http.StatusBadRequest)
			return nil, false
		}
		phoenixFail(c, err)
		return nil, false
	}
	return out, true
}
