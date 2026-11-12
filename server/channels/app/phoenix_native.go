// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package app

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/mattermost/mattermost/server/v8/channels/store"
)

// PhoenixNative implements phoenix.NativeOps with the native app layer. It only creates
// Team type "I" resources and accounts that cannot sign in with a password; access itself
// stays in the Phoenix bindings.
type PhoenixNative struct {
	a *App
}

// NewPhoenixNative returns the native operations adapter for the Phoenix provisioning service.
func NewPhoenixNative(a *App) *PhoenixNative { return &PhoenixNative{a: a} }

func (p *PhoenixNative) rctx(ctx context.Context) request.CTX {
	return request.EmptyContext(p.a.Log()).WithContext(ctx)
}

func asErr(appErr *model.AppError) error {
	if appErr == nil {
		return nil
	}
	return appErr
}

func notFound(appErr *model.AppError) bool {
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

func teamInfo(t *model.Team) *phoenix.TeamInfo {
	return &phoenix.TeamInfo{ID: t.Id, Name: t.Name, Type: t.Type, AllowOpenInvt: t.AllowOpenInvite, DeleteAt: t.DeleteAt}
}

func userInfo(u *model.User) *phoenix.UserInfo {
	return &phoenix.UserInfo{ID: u.Id, Username: u.Username, Email: u.Email, DeleteAt: u.DeleteAt}
}

func (p *PhoenixNative) FindTeamByName(_ context.Context, name string) (*phoenix.TeamInfo, error) {
	t, err := p.a.GetTeamByName(name)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return teamInfo(t), nil
}

func (p *PhoenixNative) CreatePrivateTeam(ctx context.Context, name, displayName string) (*phoenix.TeamInfo, error) {
	t, err := p.a.CreateTeam(p.rctx(ctx), &model.Team{
		Name: name, DisplayName: displayName, Type: model.TeamInvite, AllowOpenInvite: false,
		Email: name + "@teams.phoenix.invalid",
	})
	if err != nil {
		return nil, err
	}
	return teamInfo(t), nil
}

// EnsureTeamPrivate re-applies Team type I / no open invite and returns the read-back.
func (p *PhoenixNative) EnsureTeamPrivate(_ context.Context, teamID string) (*phoenix.TeamInfo, error) {
	t, err := p.a.GetTeam(teamID)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.Type != model.TeamInvite || t.AllowOpenInvite {
		t.Type, t.AllowOpenInvite = model.TeamInvite, false
		if t, err = p.a.UpdateTeam(t); err != nil {
			return nil, err
		}
	}
	return teamInfo(t), nil
}

func (p *PhoenixNative) ReadTeam(_ context.Context, teamID string) (*phoenix.TeamInfo, error) {
	t, err := p.a.Srv().Store().Team().Get(teamID)
	if err != nil {
		var nf *store.ErrNotFound
		if errors.As(err, &nf) {
			return nil, nil
		}
		return nil, err
	}
	return teamInfo(t), nil
}

func (p *PhoenixNative) DefaultChannelID(ctx context.Context, teamID string) (string, error) {
	ch, err := p.a.GetChannelByName(p.rctx(ctx), model.DefaultChannelName, teamID, false)
	if err != nil {
		return "", err
	}
	return ch.Id, nil
}

func (p *PhoenixNative) FindUserByUsername(_ context.Context, username string) (*phoenix.UserInfo, error) {
	u, err := p.a.GetUserByUsername(username)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return userInfo(u), nil
}

func (p *PhoenixNative) FindUserByEmail(_ context.Context, email string) (*phoenix.UserInfo, error) {
	u, err := p.a.GetUserByEmail(email)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return userInfo(u), nil
}

func randomPassword() (string, error) {
	const sets = "abcdefghijklmnopqrstuvwxyz|ABCDEFGHIJKLMNOPQRSTUVWXYZ|0123456789|!@#$%^&*"
	groups := strings.Split(sets, "|")
	out := make([]byte, 0, 48)
	for i := 0; i < 48; i++ {
		g := groups[i%len(groups)]
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(g))))
		if err != nil {
			return "", err
		}
		out = append(out, g[n.Int64()])
	}
	return string(out), nil
}

// CreateAccountUser creates a customer account with an unknown random password, so no
// password login exists for it; access comes only from a Phoenix binding plus SSO.
func (p *PhoenixNative) CreateAccountUser(ctx context.Context, username, email string, prof phoenix.Profile) (*phoenix.UserInfo, error) {
	// A fresh native store would promote its first account to system admin. The operator
	// bootstrap must therefore exist before any customer account is created.
	count, cerr := p.a.Srv().Store().User().Count(model.UserCountOptions{IncludeDeleted: true})
	if cerr != nil {
		return nil, cerr
	}
	if count == 0 {
		return nil, &phoenix.Error{Status: http.StatusServiceUnavailable, Code: phoenix.CodeNotReady, Message: "operator bootstrap required before customer accounts"}
	}
	pw, perr := randomPassword()
	if perr != nil {
		return nil, perr
	}
	u := &model.User{
		Username: username, Email: strings.ToLower(email), EmailVerified: true,
		Nickname: prof.DisplayName, FirstName: prof.FirstName, LastName: prof.LastName,
		Password: pw, Roles: model.SystemUserRoleId,
	}
	created, err := p.a.CreateUser(p.rctx(ctx), u)
	if err != nil {
		return nil, err
	}
	if created.Roles != model.SystemUserRoleId {
		return nil, errors.New("customer account received an elevated native role; aborting")
	}
	if err := p.syncGatewayAvatar(ctx, created.Id, prof); err != nil {
		return nil, err
	}
	return userInfo(created), nil
}

// UpdateAccountProfile mirrors the allowlisted Gateway profile fields only.
func (p *PhoenixNative) UpdateAccountProfile(ctx context.Context, userID, email string, prof phoenix.Profile) error {
	if err := p.syncGatewayAvatar(ctx, userID, prof); err != nil {
		return err
	}
	u, err := p.a.GetUser(p.rctx(ctx), userID)
	if err != nil {
		return err
	}
	changed := false
	set := func(dst *string, v string) {
		if v != "" && *dst != v {
			*dst = v
			changed = true
		}
	}
	set(&u.Nickname, prof.DisplayName)
	set(&u.FirstName, prof.FirstName)
	set(&u.LastName, prof.LastName)
	set(&u.Email, email)
	if !changed {
		return nil
	}
	if _, err = p.a.UpdateUser(p.rctx(ctx), u, false); err != nil {
		return err
	}
	// Gateway owns the profile. Publish the normal sanitized native event so
	// connected tenant peers see its new fields, without password/email notices.
	updated, err := p.a.GetUser(p.rctx(ctx), userID)
	if err != nil {
		return err
	}
	p.a.sendUpdatedUserEvent(updated)
	return nil
}

func (p *PhoenixNative) AddTeamMember(ctx context.Context, teamID, userID string) error {
	_, _, err := p.a.AddUserToTeam(p.rctx(ctx), teamID, userID, "")
	return asErr(err)
}

func (p *PhoenixNative) RemoveTeamMember(ctx context.Context, teamID, userID string) error {
	err := p.a.RemoveUserFromTeam(p.rctx(ctx), teamID, userID, "")
	if notFound(err) {
		return nil
	}
	return asErr(err)
}

func (p *PhoenixNative) IsTeamMember(ctx context.Context, teamID, userID string) (bool, error) {
	m, err := p.a.GetTeamMember(p.rctx(ctx), teamID, userID)
	if notFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return m.DeleteAt == 0, nil
}

func (p *PhoenixNative) RevokeUserSessions(ctx context.Context, userID string) error {
	return asErr(p.a.RevokeAllSessions(p.rctx(ctx), userID))
}

// phoenixForbiddenPermissions are the permissions customer roles must not hold under the
// Phoenix policy: no creating, listing or joining Teams, no invitations, no tokens, no OAuth,
// no bots, no custom commands/webhooks. The reconciler enforces them by read-back, so a
// permission restored by an admin or an upgrade is detected and the customers are locked out.
var phoenixForbiddenPermissions = map[string][]string{
	model.SystemUserRoleId: {
		model.PermissionCreateTeam.Id, model.PermissionListPublicTeams.Id, model.PermissionListPrivateTeams.Id,
		model.PermissionJoinPublicTeams.Id, model.PermissionJoinPrivateTeams.Id,
		model.PermissionCreateUserAccessToken.Id, model.PermissionCreateBot.Id,
	},
	model.TeamUserRoleId: {
		model.PermissionInviteUser.Id, model.PermissionAddUserToTeam.Id,
		model.PermissionManageSlashCommands.Id, model.PermissionManageIncomingWebhooks.Id, model.PermissionManageOAuth.Id,
	},
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// EnsureRoleLockdown patches the customer roles and returns whatever forbidden permission is still
// present when the roles are read back from the store.
func (p *PhoenixNative) EnsureRoleLockdown(ctx context.Context) (map[string][]string, error) {
	rctx := p.rctx(ctx)
	residual := map[string][]string{}
	for roleName, forbidden := range phoenixForbiddenPermissions {
		role, err := p.a.GetRoleByName(rctx, roleName)
		if err != nil {
			return nil, err
		}
		keep := make([]string, 0, len(role.Permissions))
		changed := false
		for _, perm := range role.Permissions {
			if contains(forbidden, perm) {
				changed = true
				continue
			}
			keep = append(keep, perm)
		}
		if changed {
			if _, uerr := p.a.PatchRole(role, &model.RolePatch{Permissions: &keep}); uerr != nil {
				return nil, uerr
			}
		}
		fresh, ferr := p.a.Srv().Store().Role().GetByName(rctx, roleName)
		if ferr != nil {
			return nil, ferr
		}
		for _, perm := range fresh.Permissions {
			if contains(forbidden, perm) {
				residual[roleName] = append(residual[roleName], perm)
			}
		}
	}
	return residual, nil
}

// ConfigViolations lists effective settings that contradict the Phoenix policy (design §6.5).
func (p *PhoenixNative) ConfigViolations(_ context.Context) []string {
	cfg := p.a.Config()
	var v []string
	add := func(bad bool, name string) {
		if bad {
			v = append(v, name)
		}
	}
	add(*cfg.FileSettings.EnablePublicLink, "FileSettings.EnablePublicLink")
	add(*cfg.TeamSettings.EnableUserCreation, "TeamSettings.EnableUserCreation")
	add(*cfg.TeamSettings.EnableOpenServer, "TeamSettings.EnableOpenServer")
	add(*cfg.TeamSettings.RestrictDirectMessage != model.DirectMessageTeam, "TeamSettings.RestrictDirectMessage")
	add(*cfg.ServiceSettings.EnableUserAccessTokens, "ServiceSettings.EnableUserAccessTokens")
	add(*cfg.ServiceSettings.EnableOAuthServiceProvider, "ServiceSettings.EnableOAuthServiceProvider")
	add(*cfg.ServiceSettings.EnableBotAccountCreation, "ServiceSettings.EnableBotAccountCreation")
	add(*cfg.ServiceSettings.EnableIncomingWebhooks, "ServiceSettings.EnableIncomingWebhooks")
	add(*cfg.ServiceSettings.EnableOutgoingWebhooks, "ServiceSettings.EnableOutgoingWebhooks")
	add(*cfg.PluginSettings.EnableUploads, "PluginSettings.EnableUploads")
	add(*cfg.EmailSettings.EnableSignUpWithEmail, "EmailSettings.EnableSignUpWithEmail")
	return v
}

// bufferedResponse captures the headers a native login writes so they are released to the browser
// only after the Phoenix session binding exists.
type bufferedResponse struct {
	header http.Header
	status int
}

func newBufferedResponse() *bufferedResponse            { return &bufferedResponse{header: http.Header{}} }
func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return len(p), nil }
func (b *bufferedResponse) WriteHeader(code int)        { b.status = code }

// NewSession creates the native session through the native login path (cookies, CSRF token,
// session lifetime stay native), runs bind, and only then releases the cookies. If bind fails the
// session is revoked and nothing reaches the browser, so a session without a binding can never exist
// outside a failed request.
func (p *PhoenixNative) NewSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string, bind func(sessionID string, expiresAtMS int64) error) (string, error) {
	rctx := p.rctx(ctx)
	user, err := p.a.GetUser(rctx, userID)
	if err != nil {
		return "", err
	}
	buf := newBufferedResponse()
	session, derr := p.a.DoLogin(rctx, buf, r, user, model.LoginOptions{IsOAuthUser: true})
	if derr != nil {
		return "", derr
	}
	if berr := bind(session.Id, session.ExpiresAt); berr != nil {
		if rerr := p.a.RevokeSession(rctx, session); rerr != nil {
			return "", errors.Join(berr, rerr)
		}
		return "", berr
	}
	p.a.AttachSessionCookies(rctx.WithSession(session), buf, r)
	for k, vals := range buf.header {
		if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(model.HeaderToken) {
			continue // the session token lives in the HttpOnly cookie only
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	return session.Id, nil
}

// RevokeSession revokes one native session; an already missing session is success (idempotent).
func (p *PhoenixNative) RevokeSession(ctx context.Context, sessionID string) error {
	rctx := p.rctx(ctx)
	s, err := p.a.GetSessionById(rctx, sessionID)
	if err != nil {
		return nil
	}
	return asErr(p.a.RevokeSession(rctx, s))
}

// SessionIDFromRequest resolves the caller's own session cookie; it never reads a client-chosen id.
func (p *PhoenixNative) SessionIDFromRequest(_ context.Context, r *http.Request) (string, bool) {
	token, _ := ParseAuthTokenFromRequest(r)
	if token == "" {
		return "", false
	}
	s, err := p.a.GetSession(token)
	if err != nil {
		return "", false
	}
	return s.Id, true
}

// SessionFromRequest resolves the native session of a request and its CSRF token (cookie token only).
func (p *PhoenixNative) SessionFromRequest(_ context.Context, r *http.Request) (string, string, string, bool) {
	token, loc := ParseAuthTokenFromRequest(r)
	if token == "" || loc != TokenLocationCookie {
		return "", "", "", false
	}
	s, err := p.a.GetSession(token)
	if err != nil {
		return "", "", "", false
	}
	return s.Id, s.UserId, s.GetCSRF(), true
}

// CreateTenantBot creates the tenant-scoped notification bot. It is a native bot account with
// no system role; its power is exactly Team and channel membership of its tenant.
func (p *PhoenixNative) CreateTenantBot(ctx context.Context, username, displayName string) (*phoenix.UserInfo, error) {
	rctx := p.rctx(ctx)
	var owner string
	err := p.a.Srv().Store().GetInternalMasterDB().QueryRowContext(ctx,
		`SELECT native_user_id FROM phoenix_operator_accounts WHERE enabled ORDER BY created_at, native_user_id LIMIT 1`).Scan(&owner)
	if err != nil {
		return nil, errors.New("an explicitly bootstrapped operator must own tenant bots")
	}
	bot, appErr := p.a.createBot(rctx, &model.Bot{Username: username, DisplayName: displayName, Description: "Phoenix task notifications", OwnerId: owner}, false)
	if appErr != nil {
		return nil, appErr
	}
	u, uerr := p.a.GetUser(rctx, bot.UserId)
	if uerr != nil {
		return nil, uerr
	}
	return userInfo(u), nil
}

// EnsureChannelMember adds a user to a channel if it is not already a member.
func (p *PhoenixNative) EnsureChannelMember(ctx context.Context, channelID, userID string) error {
	rctx := p.rctx(ctx)
	ch, err := p.a.GetChannel(rctx, channelID)
	if err != nil {
		return err
	}
	if _, merr := p.a.GetChannelMember(rctx, channelID, userID); merr == nil {
		return nil
	}
	u, uerr := p.a.GetUser(rctx, userID)
	if uerr != nil {
		return uerr
	}
	_, aerr := p.a.AddUserToChannel(rctx, u, ch, false)
	return asErr(aerr)
}

// CreatePost creates one post as the bot; the event key is stored in the post for crash recovery.
func (p *PhoenixNative) CreatePost(ctx context.Context, userID, channelID, rootID, message, eventKey string) (string, error) {
	rctx := p.rctx(ctx)
	ch, err := p.a.GetChannel(rctx, channelID)
	if err != nil {
		return "", err
	}
	post := &model.Post{UserId: userID, ChannelId: channelID, RootId: rootID, Message: message}
	post.AddProp("phoenix_event_key", eventKey)
	created, _, cerr := p.a.CreatePost(rctx, post, ch, model.CreatePostFlags{SetOnline: false})
	if cerr != nil {
		return "", cerr
	}
	return created.Id, nil
}
