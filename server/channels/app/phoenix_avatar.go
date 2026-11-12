// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package app

import (
	"bytes"
	"context"
	"errors"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Existing Gateway avatar saga attributes converge through ENSURE_USER_ACCOUNT.
// Props provide a monotonic tombstone without a second avatar catalog/schema.
func (p *PhoenixNative) syncGatewayAvatar(ctx context.Context, userID string, profile phoenix.Profile) error {
	if profile.AvatarVersion == nil {
		if profile.AvatarURL != nil {
			return &phoenix.Error{Status: 400, Code: phoenix.CodeInvalidRequest, Message: "avatar version is required"}
		}
		return nil
	}
	if *profile.AvatarVersion < 0 {
		return &phoenix.Error{Status: 400, Code: phoenix.CodeInvalidRequest, Message: "avatar version must be non-negative"}
	}
	conn, err := p.a.Srv().Store().GetInternalMasterDB().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('phoenix_avatar'),hashtext($1))`, userID); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('phoenix_avatar'),hashtext($1))`, userID)
	user, appErr := p.a.GetUser(p.rctx(ctx), userID)
	if appErr != nil {
		return appErr
	}
	previous := int64(-1)
	if raw := user.Props["phoenix_avatar_version"]; raw != "" {
		previous, _ = strconv.ParseInt(raw, 10, 64)
	}
	if *profile.AvatarVersion <= previous {
		return nil
	}
	wanted := ""
	if profile.AvatarURL != nil {
		wanted = strings.TrimSpace(*profile.AvatarURL)
	}
	if wanted == "" {
		if appErr = p.a.SetDefaultProfileImage(p.rctx(ctx), user); appErr != nil {
			return appErr
		}
	} else {
		settings := phoenix.LoadSettings()
		origin, err := url.Parse(settings.GatewayURL)
		if err != nil {
			return err
		}
		target, err := url.Parse(wanted)
		if err != nil || target.Scheme != "https" || target.Host != origin.Host || target.User != nil || target.Fragment != "" || !strings.HasPrefix(target.Path, "/avatar/") {
			return &phoenix.Error{Status: 400, Code: phoenix.CodeInvalidRequest, Message: "avatar must use the configured Gateway origin and signed avatar path"}
		}
		client, err := phoenix.NewHTTPClient(settings.CAFile, 5*time.Second)
		if err != nil {
			return err
		}
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("avatar redirects are not allowed") }
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, wanted, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return errors.New("Gateway avatar unavailable")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("Gateway avatar unavailable")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 10*1024*1024+1))
		if err != nil || len(data) > 10*1024*1024 {
			return errors.New("Gateway avatar exceeds limit")
		}
		if appErr = p.a.SetProfileImageFromFile(p.rctx(ctx), userID, bytes.NewReader(data)); appErr != nil {
			return appErr
		}
	}
	user, appErr = p.a.GetUser(p.rctx(ctx), userID)
	if appErr != nil {
		return appErr
	}
	if user.Props == nil {
		user.Props = map[string]string{}
	}
	user.Props["phoenix_avatar_version"] = strconv.FormatInt(*profile.AvatarVersion, 10)
	_, appErr = p.a.UpdateUser(p.rctx(ctx), user, false)
	if appErr != nil {
		return appErr
	}
	return nil
}
