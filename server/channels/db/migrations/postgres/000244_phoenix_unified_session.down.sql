ALTER TABLE phoenix_session_bindings
    DROP COLUMN IF EXISTS outage_since,
    DROP COLUMN IF EXISTS absolute_expires_at,
    DROP COLUMN IF EXISTS idle_expires_at,
    DROP COLUMN IF EXISTS access_expires_at,
    DROP COLUMN IF EXISTS refresh_token_enc,
    DROP COLUMN IF EXISTS access_token_enc;
