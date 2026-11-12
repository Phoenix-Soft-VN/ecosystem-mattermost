-- Phoenix tenant boundary foundation (contracts C1). All identifiers are lowercase.
-- Native ids are varchar(26); Gateway tenant/principal keys are opaque varchar(255);
-- times are epoch milliseconds.

CREATE TABLE IF NOT EXISTS phoenix_tenant_bindings (
    tenant_id          varchar(255) PRIMARY KEY,
    team_id            varchar(26)  NOT NULL UNIQUE REFERENCES teams(id),
    default_channel_id varchar(26)  NULL REFERENCES channels(id),
    state              varchar(16)  NOT NULL CHECK (state IN ('ACTIVE', 'SUSPENDED')),
    task_endpoint_id   varchar(64)  NULL,
    policy_version     bigint       NOT NULL DEFAULT 0 CHECK (policy_version >= 0),
    desired_version    bigint       NOT NULL DEFAULT 0 CHECK (desired_version >= 0),
    created_at         bigint       NOT NULL,
    updated_at         bigint       NOT NULL
);

CREATE TABLE IF NOT EXISTS phoenix_user_bindings (
    issuer          text         NOT NULL,
    sub             varchar(255) NOT NULL,
    tenant_id       varchar(255) NOT NULL REFERENCES phoenix_tenant_bindings(tenant_id),
    native_user_id  varchar(26)  NOT NULL UNIQUE REFERENCES users(id),
    actor_kind      varchar(16)  NOT NULL CHECK (actor_kind IN ('CUSTOMER', 'BOT', 'OPERATOR')),
    access_state    varchar(16)  NOT NULL CHECK (access_state IN ('PENDING', 'GRANTED', 'REVOKED')),
    access_version  bigint       NOT NULL DEFAULT 0 CHECK (access_version >= 0),
    created_at      bigint       NOT NULL,
    updated_at      bigint       NOT NULL,
    PRIMARY KEY (issuer, sub)
);
CREATE INDEX IF NOT EXISTS idx_phoenix_user_bindings_tenant ON phoenix_user_bindings (tenant_id);

CREATE TABLE IF NOT EXISTS phoenix_session_bindings (
    session_id      varchar(26)  PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    issuer          text         NOT NULL,
    sub             varchar(255) NOT NULL,
    sid             varchar(255) NOT NULL,
    tenant_id       varchar(255) NOT NULL REFERENCES phoenix_tenant_bindings(tenant_id),
    native_user_id  varchar(26)  NOT NULL REFERENCES users(id),
    access_version  bigint       NOT NULL CHECK (access_version >= 0),
    expires_at      bigint       NOT NULL,
    FOREIGN KEY (issuer, sub) REFERENCES phoenix_user_bindings(issuer, sub)
);
CREATE INDEX IF NOT EXISTS idx_phoenix_session_bindings_sid ON phoenix_session_bindings (sid);
CREATE INDEX IF NOT EXISTS idx_phoenix_session_bindings_tenant_user ON phoenix_session_bindings (tenant_id, native_user_id);

CREATE TABLE IF NOT EXISTS phoenix_auth_transactions (
    state_hash     varchar(64) PRIMARY KEY,
    nonce_hash     varchar(64) NOT NULL,
    browser_hash   varchar(64) NOT NULL,
    pkce_verifier  text        NOT NULL,
    return_path    text        NOT NULL,
    expires_at     bigint      NOT NULL,
    consumed_at    bigint      NULL
);
CREATE INDEX IF NOT EXISTS idx_phoenix_auth_transactions_expires ON phoenix_auth_transactions (expires_at);

CREATE TABLE IF NOT EXISTS phoenix_operation_keys (
    caller           varchar(255) NOT NULL,
    operation_key    varchar(255) NOT NULL,
    request_hash     varchar(64)  NOT NULL,
    operation        varchar(64)  NOT NULL,
    tenant_id        varchar(255) NOT NULL,
    desired_version  bigint       NOT NULL CHECK (desired_version >= 0),
    state            varchar(16)  NOT NULL CHECK (state IN ('PENDING', 'SUCCEEDED', 'FAILED', 'UNKNOWN')),
    result           text         NOT NULL DEFAULT '',
    created_at       bigint       NOT NULL,
    updated_at       bigint       NOT NULL,
    PRIMARY KEY (caller, operation_key)
);
CREATE INDEX IF NOT EXISTS idx_phoenix_operation_keys_tenant ON phoenix_operation_keys (tenant_id, operation);

CREATE TABLE IF NOT EXISTS phoenix_channel_bindings (
    channel_id  varchar(26)  PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    tenant_id   varchar(255) NOT NULL REFERENCES phoenix_tenant_bindings(tenant_id)
);
CREATE INDEX IF NOT EXISTS idx_phoenix_channel_bindings_tenant ON phoenix_channel_bindings (tenant_id);

CREATE TABLE IF NOT EXISTS phoenix_operator_accounts (
    native_user_id  varchar(26) PRIMARY KEY REFERENCES users(id),
    enabled         boolean     NOT NULL DEFAULT true,
    audit_ref       text        NOT NULL,
    created_at      bigint      NOT NULL
);

-- One-time keys for signed assertions/logout events (jti replay protection).
CREATE TABLE IF NOT EXISTS phoenix_replay_keys (
    kind        varchar(32)  NOT NULL,
    replay_key  varchar(255) NOT NULL,
    expires_at  bigint       NOT NULL,
    PRIMARY KEY (kind, replay_key)
);
CREATE INDEX IF NOT EXISTS idx_phoenix_replay_keys_expires ON phoenix_replay_keys (expires_at);

-- Mutation guard: a stale desired_version can never lower a binding version.
CREATE OR REPLACE FUNCTION phoenix_forbid_version_rollback() RETURNS trigger AS $$
BEGIN
    IF NEW.access_version < OLD.access_version THEN
        RAISE EXCEPTION 'phoenix access_version rollback rejected' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS phoenix_user_bindings_version_guard ON phoenix_user_bindings;
CREATE TRIGGER phoenix_user_bindings_version_guard BEFORE UPDATE ON phoenix_user_bindings
    FOR EACH ROW EXECUTE FUNCTION phoenix_forbid_version_rollback();
