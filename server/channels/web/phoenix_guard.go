// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// customerBlockedRoutes are native alternatives (credential/token/OAuth/invite/plugin/sharing
// routes) that no non-operator may reach. They are denied here, on the server, regardless of
// native permissions or configuration drift. Password login is not listed: the login handler
// itself restricts it to registered operators.
var customerBlockedRoutes = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v4/(config|roles|schemes|permissions|cluster|jobs|migrate|brand|license)(/|$)`),
	regexp.MustCompile(`^/api/v4/users/[^/]+/roles(/|$)`),
	regexp.MustCompile(`^/api/v4/oauth(/|$)`),
	regexp.MustCompile(`^/oauth(/|$)`),
	regexp.MustCompile(`^/api/v4/users/tokens(/|$)`),
	regexp.MustCompile(`^/api/v4/users/[^/]+/tokens(/|$)`),
	regexp.MustCompile(`^/api/v4/users/[^/]+/(auth|mfa|password|email/verify/member)(/|$)`),
	regexp.MustCompile(`^/api/v4/users/(password|mfa|login/(switch|cws|sso)|logout/everywhere)(/|$)`),
	regexp.MustCompile(`^/api/v4/users/email/(verify|verify/send|verify/switch|verify/complete)(/|$)`),
	regexp.MustCompile(`^/api/v4/users/login/.+`),
	regexp.MustCompile(`^/api/v4/teams/invite(/|$)`),
	regexp.MustCompile(`^/api/v4/teams/[^/]+/(invite|import)(/|$)`),
	regexp.MustCompile(`^/api/v4/plugins(/|$)`),
	regexp.MustCompile(`^/api/v4/(sharedchannels|remotecluster)(/|$)`),
	regexp.MustCompile(`^/api/v4/files/[^/]+/link$`),
	regexp.MustCompile(`^/api/v4/(saml|ldap|openid|oauth2)(/|$)`),
	regexp.MustCompile(`^/api/v4/hooks(/|$)`),
	regexp.MustCompile(`^/api/v4/bots(/|$)`),
	regexp.MustCompile(`^/api/v4/teams/search$`),
	regexp.MustCompile(`^/api/v4/users/[^/]+/teams/[^/]+/(join|leave)(/|$)`),
}

// customerBlockedMethodRoutes only block the listed methods.
var customerBlockedMethodRoutes = []struct {
	re      *regexp.Regexp
	methods map[string]bool
}{
	{regexp.MustCompile(`^/api/v4/users/[^/]+(/patch)?$`), map[string]bool{http.MethodPut: true, http.MethodPatch: true}},
	{regexp.MustCompile(`^/api/v4/users/[^/]+/image$`), map[string]bool{http.MethodPost: true, http.MethodDelete: true}},
	{regexp.MustCompile(`^/api/v4/users$`), map[string]bool{http.MethodPost: true}},
	{regexp.MustCompile(`^/api/v4/teams$`), map[string]bool{http.MethodPost: true, http.MethodGet: true}},
	{regexp.MustCompile(`^/api/v4/channels$`), map[string]bool{http.MethodGet: true}},
	{regexp.MustCompile(`^/api/v4/teams/[^/]+(/patch)?$`), map[string]bool{http.MethodPut: true, http.MethodDelete: true}},
	{regexp.MustCompile(`^/api/v4/teams/[^/]+/members(/batch)?$`), map[string]bool{http.MethodPost: true}},
	{regexp.MustCompile(`^/api/v4/teams/[^/]+/members/[^/]+$`), map[string]bool{http.MethodDelete: true}},
	{regexp.MustCompile(`^/api/v4/commands$`), map[string]bool{http.MethodPost: true}},
}

func phoenixNotFound() *model.AppError {
	return model.NewAppError("phoenixGuard", "api.context.404.app_error", nil, "", http.StatusNotFound)
}

func phoenixError(err error) *model.AppError {
	pe, ok := err.(*phoenix.Error)
	if !ok {
		return model.NewAppError("phoenixGuard", phoenix.CodeNotReady, nil, "", http.StatusServiceUnavailable).Wrap(err)
	}
	if pe.Status == http.StatusNotFound {
		return phoenixNotFound()
	}
	return model.NewAppError("phoenixGuard", pe.Code, nil, pe.Message, pe.Status)
}

func phoenixRouteBlocked(r *http.Request) bool {
	// Native bootstrap reads contain shared client settings and role definitions,
	// not tenant resources. Preserve native validation and block every admin write.
	// GET /plugins/webapp is how the webapp discovers the bundles of enabled prepackaged plugins
	// (Playbooks); it returns only client manifests. Every other plugin route stays blocked.
	if (r.Method == http.MethodGet && (r.URL.Path == "/api/v4/config/client" || r.URL.Path == "/api/v4/license/client" || r.URL.Path == "/api/v4/plugins/webapp")) ||
		(r.Method == http.MethodPost && r.URL.Path == "/api/v4/roles/names") {
		return false
	}
	for _, re := range customerBlockedRoutes {
		if re.MatchString(r.URL.Path) {
			return true
		}
	}
	for _, m := range customerBlockedMethodRoutes {
		if m.methods[r.Method] && m.re.MatchString(r.URL.Path) {
			return true
		}
	}
	return false
}

// phoenixGuard is the single HTTP entry guard of the tenant boundary. It runs for every API
// request after session authentication and before the handler, so a foreign or unknown
// resource id is rejected before any query result is built or serialized. It does not replace the
// native permission checks: those still run afterwards (same tenant AND native permission).
func (h Handler) phoenixGuard(c *Context, r *http.Request) {
	pol := phoenix.Default()
	if pol == nil || h.IsStatic {
		return
	}
	session := c.AppContext.Session()
	authenticated := session != nil && session.UserId != "" && session.Id != ""
	if !authenticated {
		// Unauthenticated customers have no access to credential routes either.
		if phoenixRouteBlocked(r) {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeAccessDenied, nil, "route not available", http.StatusForbidden)
		}
		return
	}
	actor, err := pol.ResolveActor(r.Context(), session.Id, session.UserId)
	if err != nil {
		c.Err = phoenixError(err)
		return
	}
	c.AppContext = c.AppContext.WithContext(phoenix.WithActor(c.AppContext.Context(), actor))
	if actor.Operator {
		return
	}
	if phoenixRouteBlocked(r) {
		c.Err = model.NewAppError("phoenixGuard", phoenix.CodeAccessDenied, nil, "route not available", http.StatusForbidden)
		return
	}
	if raw := r.URL.Query().Get("per_page"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n > phoenix.MaxPageSize {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeInvalidRequest, nil, "page size exceeds 200", http.StatusBadRequest)
			return
		}
	}
	type check struct {
		kind phoenix.ResourceKind
		id   string
	}
	checks := []check{
		{phoenix.ResUser, resolveMe(c.Params.UserId, session.UserId)},
		{phoenix.ResUser, resolveMe(c.Params.OtherUserId, session.UserId)},
		{phoenix.ResUser, c.Params.BotUserId},
		{phoenix.ResTeam, c.Params.TeamId},
		{phoenix.ResTeamName, c.Params.TeamName},
		{phoenix.ResChannel, c.Params.ChannelId},
		{phoenix.ResPost, c.Params.PostId},
		{phoenix.ResPost, c.Params.ThreadId},
		{phoenix.ResFile, c.Params.FileId},
		{phoenix.ResUsername, c.Params.Username},
		{phoenix.ResEmail, c.Params.Email},
	}
	for _, ck := range checks {
		if ck.id == "" {
			continue
		}
		if cerr := pol.CanAccessResource(r.Context(), actor, ck.kind, ck.id); cerr != nil {
			c.Err = phoenixError(cerr)
			return
		}
	}
	keys := map[string]phoenix.ResourceKind{"user_id": phoenix.ResUser, "other_user_id": phoenix.ResUser,
		"team_id": phoenix.ResTeam, "channel_id": phoenix.ResChannel, "target_channel_id": phoenix.ResChannel,
		"post_id": phoenix.ResPost, "root_id": phoenix.ResPost, "file_id": phoenix.ResFile,
		"in_team": phoenix.ResTeam, "in_channel": phoenix.ResChannel, "not_in_channel": phoenix.ResChannel,
		"not_in_team": phoenix.ResTeam, "since_post": phoenix.ResPost, "before": phoenix.ResPost, "after": phoenix.ResPost}
	checkFields := func(fields map[string]any) bool {
		for name, kind := range keys {
			if id, ok := fields[name].(string); ok && id != "" {
				if err := pol.CanAccessResource(r.Context(), actor, kind, resolveMe(id, session.UserId)); err != nil {
					c.Err = phoenixError(err)
					return false
				}
			}
		}
		return true
	}
	query := map[string]any{}
	for name := range keys {
		query[name] = r.URL.Query().Get(name)
	}
	if !checkFields(query) {
		return
	}
	// Validate scalar resource references before native handlers query/serialize. Mixed batch
	// arrays retain their endpoint's omit policy; multipart files retain native upload limits.
	if r.Body != nil && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		data, err := io.ReadAll(io.LimitReader(r.Body, phoenix.MaxInternalBody+1))
		if err != nil {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeInvalidRequest, nil, "body unreadable", http.StatusBadRequest)
			return
		}
		if len(data) > phoenix.MaxInternalBody {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeBodyTooLarge, nil, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		// Enforce the request cap before native SortedArrayFromJSON removes
		// duplicates, otherwise 201 identical IDs bypass the input limit.
		var items []json.RawMessage
		if json.Unmarshal(data, &items) == nil && len(items) > phoenix.MaxBatchIDs {
			c.Err = model.NewAppError("phoenixGuard", phoenix.CodeInvalidRequest, nil, "too many items (max 200)", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if json.Unmarshal(data, &body) == nil {
			checkFields(body)
		}
	}
}

func resolveMe(id, sessionUserID string) string {
	if id == model.Me {
		return sessionUserID
	}
	return id
}
