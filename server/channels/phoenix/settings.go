// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"errors"
	"os"
	"strings"
)

// Settings are read from the environment at startup. Secrets are referenced by *_FILE
// paths and never placed in config.json, evidence or logs.
type Settings struct {
	// Internal provisioning caller (C2).
	InternalIssuer   string
	InternalAudience string
	InternalJWKSURL  string
	CAFile           string

	// Gateway OIDC client (design §6.3). AuthorizeURL is browser-facing; TokenURL and JWKSURL are
	// server-to-server and fixed here, never taken from a token or from request input.
	SiteOrigin       string // exact public origin, e.g. https://chat.phoenixsoft.vn
	OIDCIssuer       string
	OIDCClientID     string
	OIDCSecretFile   string
	OIDCAuthorizeURL string
	OIDCTokenURL     string
	OIDCJWKSURL      string
	TxnKeyFile       string // 32-byte key (base64) protecting the PKCE verifier at rest

	// Gateway authority + shell BFF (design §6.4, §6.7).
	GatewayURL       string // internal Gateway base URL
	BridgeIssuer     string // registered issuer of the "chat" bridge app
	BridgeSecretFile string // HMAC secret registered for appCode chat (ui_bridge_app)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// LoadSettings reads PHOENIX_* variables.
func LoadSettings() Settings {
	return Settings{
		InternalIssuer:   envOr("PHOENIX_INTERNAL_ISSUER", ""),
		InternalAudience: envOr("PHOENIX_INTERNAL_AUDIENCE", InternalAudience),
		InternalJWKSURL:  envOr("PHOENIX_INTERNAL_JWKS_URL", ""),
		CAFile:           envOr("PHOENIX_CA_FILE", ""),

		SiteOrigin:       strings.TrimRight(envOr("PHOENIX_SITE_ORIGIN", ""), "/"),
		OIDCIssuer:       envOr("PHOENIX_OIDC_ISSUER", ""),
		OIDCClientID:     envOr("PHOENIX_OIDC_CLIENT_ID", ""),
		OIDCSecretFile:   envOr("PHOENIX_OIDC_CLIENT_SECRET_FILE", ""),
		OIDCAuthorizeURL: envOr("PHOENIX_OIDC_AUTHORIZE_URL", ""),
		OIDCTokenURL:     envOr("PHOENIX_OIDC_TOKEN_URL", ""),
		OIDCJWKSURL:      envOr("PHOENIX_OIDC_JWKS_URL", ""),
		TxnKeyFile:       envOr("PHOENIX_TXN_KEY_FILE", ""),

		GatewayURL:       strings.TrimRight(envOr("PHOENIX_GATEWAY_URL", ""), "/"),
		BridgeIssuer:     envOr("PHOENIX_BRIDGE_ISSUER", ""),
		BridgeSecretFile: envOr("PHOENIX_BRIDGE_SECRET_FILE", ""),
	}
}

// Validate reports whether the settings are complete enough to serve the internal API.
func (s Settings) Validate() error {
	var missing []string
	if s.InternalIssuer == "" {
		missing = append(missing, "PHOENIX_INTERNAL_ISSUER")
	}
	if s.InternalJWKSURL == "" {
		missing = append(missing, "PHOENIX_INTERNAL_JWKS_URL")
	} else if !strings.HasPrefix(s.InternalJWKSURL, "https://") {
		return errors.New("PHOENIX_INTERNAL_JWKS_URL must be https")
	}
	if s.InternalAudience != InternalAudience {
		return errors.New("PHOENIX_INTERNAL_AUDIENCE must be " + InternalAudience)
	}
	if len(missing) > 0 {
		return errors.New("missing settings: " + strings.Join(missing, ","))
	}
	return nil
}

// ValidateAuth reports whether the SSO, authority and shell settings are complete. Without
// them login is refused (fail closed) and readiness stays red.
func (s Settings) ValidateAuth() error {
	var missing []string
	need := map[string]string{
		"PHOENIX_SITE_ORIGIN": s.SiteOrigin, "PHOENIX_OIDC_ISSUER": s.OIDCIssuer, "PHOENIX_OIDC_CLIENT_ID": s.OIDCClientID,
		"PHOENIX_OIDC_CLIENT_SECRET_FILE": s.OIDCSecretFile, "PHOENIX_OIDC_AUTHORIZE_URL": s.OIDCAuthorizeURL,
		"PHOENIX_OIDC_TOKEN_URL": s.OIDCTokenURL, "PHOENIX_OIDC_JWKS_URL": s.OIDCJWKSURL, "PHOENIX_TXN_KEY_FILE": s.TxnKeyFile,
		"PHOENIX_GATEWAY_URL": s.GatewayURL, "PHOENIX_BRIDGE_ISSUER": s.BridgeIssuer, "PHOENIX_BRIDGE_SECRET_FILE": s.BridgeSecretFile,
	}
	for k, v := range need {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return errors.New("missing auth settings: " + strings.Join(missing, ","))
	}
	for name, u := range map[string]string{"PHOENIX_OIDC_TOKEN_URL": s.OIDCTokenURL, "PHOENIX_OIDC_JWKS_URL": s.OIDCJWKSURL, "PHOENIX_GATEWAY_URL": s.GatewayURL} {
		if !strings.HasPrefix(u, "https://") {
			return errors.New(name + " must be https")
		}
	}
	if !strings.HasPrefix(s.SiteOrigin, "https://") && !strings.HasPrefix(s.SiteOrigin, "http://127.0.0.1") && !strings.HasPrefix(s.SiteOrigin, "http://localhost") {
		return errors.New("PHOENIX_SITE_ORIGIN must be https (loopback http allowed for local tests only)")
	}
	return nil
}
