-- Add encrypted OIDC token storage and unified session/outage timestamps to phoenix_session_bindings.
ALTER TABLE phoenix_session_bindings
    ADD COLUMN IF NOT EXISTS access_token_enc text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refresh_token_enc text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS access_expires_at bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS idle_expires_at bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS absolute_expires_at bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS outage_since bigint NOT NULL DEFAULT 0;
