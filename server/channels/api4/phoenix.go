// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package api4

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/channels/app"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

// InitPhoenix mounts the native Phoenix surface (internal provisioning API, readiness, SSO login,
// back-channel logout, cleanup, shell BFF) on the native router when the tenant boundary is
// enforced. It runs before the static web handlers, so these routes are served by this process,
// never by a proxy.
func (api *API) InitPhoenix() {
	if !phoenix.Enforced() {
		return
	}
	srv := api.srv
	db := srv.Store().GetInternalMasterDB()
	store := phoenix.NewStore(db, nil)
	native := app.NewPhoenixNative(app.New(app.ServerConnector(srv.Channels())))
	svc := phoenix.NewService(store, native)
	policy := phoenix.NewPolicy(db, nil)
	phoenix.SetDefault(policy)

	audit := func(event string, fields map[string]string) {
		attrs := make([]mlog.Field, 0, len(fields))
		for k, v := range fields {
			attrs = append(attrs, mlog.String(k, v))
		}
		srv.Log().Warn(event, attrs...)
	}

	settings := phoenix.LoadSettings()
	settingsErr := settings.Validate()
	var verifier *phoenix.CallerVerifier
	if settingsErr == nil {
		keys, err := phoenix.NewJWKSSource(settings.InternalJWKSURL, settings.CAFile)
		if err != nil {
			settingsErr = err
		} else {
			verifier = &phoenix.CallerVerifier{Issuer: settings.InternalIssuer, Audience: settings.InternalAudience, Keys: keys, Replay: store}
		}
	}

	// SSO / authority / shell. Any missing piece leaves login closed and readiness red.
	authErr := settings.ValidateAuth()
	var authSvc *phoenix.AuthService
	var logoutSvc *phoenix.LogoutService
	var shellSvc *phoenix.ShellService
	if authErr == nil {
		authErr = func() error {
			secretRaw, err := os.ReadFile(settings.OIDCSecretFile)
			if err != nil {
				return err
			}
			bridgeRaw, err := os.ReadFile(settings.BridgeSecretFile)
			if err != nil {
				return err
			}
			txnKey, err := phoenix.LoadTxnKey(settings.TxnKeyFile)
			if err != nil {
				return err
			}
			httpc, err := phoenix.NewHTTPClient(settings.CAFile, 10*time.Second)
			if err != nil {
				return err
			}
			oidcKeys, err := phoenix.NewJWKSSource(settings.OIDCJWKSURL, settings.CAFile)
			if err != nil {
				return err
			}
			signer := &phoenix.BridgeSigner{Issuer: settings.BridgeIssuer, Secret: []byte(strings.TrimSpace(string(bridgeRaw)))}
			authority := phoenix.NewAuthority(&phoenix.AuthorityClient{URL: settings.GatewayURL, Signer: signer, Issuer: settings.OIDCIssuer, HTTP: httpc})
			authority.OnInactive = func(ctx context.Context, sessionID string) { _ = native.RevokeSession(ctx, sessionID) }
			policy.Authority = authority
			authLimiter := phoenix.NewLimiter(phoenix.AuthRatePerMin, time.Minute, nil)
			authSvc = &phoenix.AuthService{Store: store, Settings: settings, Keys: oidcKeys, Secret: strings.TrimSpace(string(secretRaw)), TxnKey: txnKey,
				HTTP: httpc, Sessions: native, Authority: authority, Policy: policy, Limiter: authLimiter, Audit: audit}
			shellSvc = &phoenix.ShellService{Policy: policy, Sessions: native, Signer: signer, GatewayURL: settings.GatewayURL,
				SiteOrigin: settings.SiteOrigin, LogoutURL: strings.TrimRight(settings.OIDCIssuer, "/") + "/oauth2/logout", HTTP: httpc, Audit: audit}
			logoutSvc = &phoenix.LogoutService{Store: store, Settings: settings, Keys: oidcKeys, Sessions: native, Policy: policy, Authority: authority,
				Limiter: phoenix.NewLimiter(phoenix.AuthRatePerMin*4, time.Minute, nil), Audit: audit}
			if taskFile := os.Getenv("PHOENIX_TASK_SECRET_FILE"); taskFile != "" {
				raw, terr := os.ReadFile(taskFile)
				if terr != nil {
					return terr
				}
				phoenix.SetDefaultTask(&phoenix.TaskCommand{GatewayURL: settings.GatewayURL, Secret: []byte(strings.TrimSpace(string(raw))), HTTP: httpc})
			}
			return nil
		}()
		if authErr != nil {
			srv.Log().Error("phoenix auth setup failed; login stays closed", mlog.Err(authErr))
		}
	}

	svc.OnChange = func(tenantID, nativeUserID string) {
		if nativeUserID != "" {
			policy.InvalidateUser(nativeUserID)
		} else {
			policy.InvalidateTenant(tenantID)
		}
		// A change in who may act also drops the Gateway authority entries of those sessions.
		if policy.Authority != nil {
			ids, _ := store.ActiveSessionIDsForTenant(context.Background(), tenantID)
			if nativeUserID != "" {
				ids, _ = store.SessionIDsOfUser(context.Background(), nativeUserID)
			}
			for _, id := range ids {
				policy.Authority.Invalidate(id)
				policy.InvalidateSession(id)
			}
		}
	}

	papi := phoenix.NewAPI(svc, verifier)
	papi.Audit = audit
	papi.Notify = native
	rec := &phoenix.Reconciler{DB: db, Ops: native, Policy: policy, Audit: audit}
	// The first pass runs before the HTTP listener accepts customers; until it verifies the
	// policy, tenant principals are refused and /phoenix/ready is red.
	if err := rec.RunOnce(context.Background()); err != nil {
		srv.Log().Error("phoenix initial policy reconcile failed", mlog.Err(err))
	}
	rec.Start(context.Background(), 15*time.Second)
	papi.Checks = []phoenix.ReadyCheck{
		{Name: "schema", Fn: store.SchemaReady},
		{Name: "settings", Fn: func(context.Context) error { return settingsErr }},
		{Name: "policy", Fn: rec.Ready},
	}
	// "auth" is a required readiness condition only when SSO settings were supplied or SSO is the
	// packaged mode; the check name is stable so a missing credential shows up as auth=fail.
	if authErr != nil || authSvc != nil {
		papi.Checks = append(papi.Checks, phoenix.ReadyCheck{Name: "auth", Fn: func(context.Context) error { return authErr }})
	}

	register := func(path string, h http.Handler, methods ...string) {
		srv.Router.Handle(path, h).Methods(methods...)
	}
	papi.Register(register)
	if authSvc != nil {
		authSvc.Register(register)
		logoutSvc.Register(register)
		shellSvc.Register(register)
	}
}
