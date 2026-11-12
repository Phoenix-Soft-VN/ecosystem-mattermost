// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// Store is the persistence layer of C1. It talks to the native master database directly
// through database/sql so that bindings and journal rows commit with explicit transactions.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

func (s *Store) nowMS() int64 { return s.now().UnixMilli() }

func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}

func isCheckViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23514"
}

// RequiredTables are the C1 tables whose presence readiness verifies.
var RequiredTables = []string{
	"phoenix_tenant_bindings", "phoenix_user_bindings", "phoenix_session_bindings",
	"phoenix_auth_transactions", "phoenix_operation_keys", "phoenix_channel_bindings",
	"phoenix_operator_accounts", "phoenix_replay_keys",
}

// SchemaReady verifies that every required table exists in the current schema.
func (s *Store) SchemaReady(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ANY($1)`, pq.Array(RequiredTables))
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		found[n] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var missing []string
	for _, t := range RequiredTables {
		if !found[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return errors.New("missing phoenix tables: " + strings.Join(missing, ","))
	}
	return nil
}

// ---- operation journal -------------------------------------------------------------

// BeginOperation inserts a PENDING journal row, or returns the existing one for the same
// (caller, key). A different request hash for the same key is a 409 conflict.
func (s *Store) BeginOperation(ctx context.Context, rec OperationRecord) (OperationRecord, bool, error) {
	now := s.nowMS()
	res, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_operation_keys
		(caller, operation_key, request_hash, operation, tenant_id, desired_version, state, result, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'PENDING','',$7,$7) ON CONFLICT (caller, operation_key) DO NOTHING`,
		rec.Caller, rec.Key, rec.RequestHash, string(rec.Operation), rec.TenantID, rec.DesiredVersion, now)
	if err != nil {
		return rec, false, err
	}
	n, _ := res.RowsAffected()
	existing, err := s.GetOperation(ctx, rec.Caller, rec.Key)
	if err != nil {
		return rec, false, err
	}
	if existing.RequestHash != rec.RequestHash {
		return existing, false, newErr(http.StatusConflict, CodeOperationConflict, "operation key reused with a different payload")
	}
	return existing, n == 1, nil
}

func (s *Store) GetOperation(ctx context.Context, caller, key string) (OperationRecord, error) {
	var r OperationRecord
	var op, st string
	err := s.db.QueryRowContext(ctx, `SELECT caller, operation_key, request_hash, operation, tenant_id, desired_version, state, result
		FROM phoenix_operation_keys WHERE caller=$1 AND operation_key=$2`, caller, key).
		Scan(&r.Caller, &r.Key, &r.RequestHash, &op, &r.TenantID, &r.DesiredVersion, &st, &r.Result)
	r.Operation, r.State = Operation(op), OperationState(st)
	return r, err
}

func (s *Store) FinishOperation(ctx context.Context, caller, key string, state OperationState, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE phoenix_operation_keys SET state=$3, result=$4, updated_at=$5 WHERE caller=$1 AND operation_key=$2`,
		caller, key, string(state), result, s.nowMS())
	return err
}

// ---- tenant bindings ---------------------------------------------------------------

func (s *Store) GetTenant(ctx context.Context, tenantID string) (TenantBinding, error) {
	var b TenantBinding
	var state string
	var ch sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT tenant_id, team_id, default_channel_id, state, policy_version, desired_version
		FROM phoenix_tenant_bindings WHERE tenant_id=$1`, tenantID).
		Scan(&b.TenantID, &b.TeamID, &ch, &state, &b.PolicyVersion, &b.DesiredVersion)
	b.State, b.DefaultChannelID = TenantState(state), ch.String
	return b, err
}

// AttachTenant atomically records the tenant binding and the default channel ownership.
// An existing binding for the same tenant and team is idempotent; another team or a team
// claimed by another tenant is a conflict. The binding is always read back.
func (s *Store) AttachTenant(ctx context.Context, tenantID, teamID, defaultChannelID string, desired int64, taskEndpointID string) (TenantBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.nowMS()
	if _, err = tx.ExecContext(ctx, `INSERT INTO phoenix_tenant_bindings (tenant_id, team_id, default_channel_id, state, policy_version, desired_version, created_at, updated_at)
		VALUES ($1,$2,NULLIF($3,''),'ACTIVE',$4,$5,$6,$6) ON CONFLICT (tenant_id) DO NOTHING`,
		tenantID, teamID, defaultChannelID, CurrentPolicyVersion, desired, now); err != nil {
		if isUniqueViolation(err) {
			return TenantBinding{}, newErr(http.StatusConflict, CodeTeamClaimed, "team already bound to another tenant")
		}
		return TenantBinding{}, err
	}
	var boundTeam string
	if err = tx.QueryRowContext(ctx, `SELECT team_id FROM phoenix_tenant_bindings WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&boundTeam); err != nil {
		return TenantBinding{}, err
	}
	if boundTeam != teamID {
		return TenantBinding{}, newErr(http.StatusConflict, CodeTeamClaimed, "tenant already bound to a different team")
	}
	if defaultChannelID != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO phoenix_channel_bindings (channel_id, tenant_id) VALUES ($1,$2) ON CONFLICT (channel_id) DO NOTHING`, defaultChannelID, tenantID); err != nil {
			return TenantBinding{}, err
		}
		var owner string
		if err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM phoenix_channel_bindings WHERE channel_id=$1`, defaultChannelID).Scan(&owner); err != nil {
			return TenantBinding{}, err
		}
		if owner != tenantID {
			return TenantBinding{}, newErr(http.StatusConflict, CodeTeamClaimed, "channel already bound to another tenant")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE phoenix_tenant_bindings SET default_channel_id=$2 WHERE tenant_id=$1 AND default_channel_id IS NULL`, tenantID, defaultChannelID); err != nil {
			return TenantBinding{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE phoenix_tenant_bindings SET desired_version=GREATEST(desired_version,$2), policy_version=$3, updated_at=$4,
		task_endpoint_id=COALESCE(NULLIF($5,''), task_endpoint_id) WHERE tenant_id=$1`,
		tenantID, desired, CurrentPolicyVersion, now, taskEndpointID); err != nil {
		return TenantBinding{}, err
	}
	if err = tx.Commit(); err != nil {
		return TenantBinding{}, err
	}
	return s.GetTenant(ctx, tenantID)
}

// SetTenantState applies suspend/resume with a monotonic desired version. A lower version
// is stale; the same version is accepted only when it requests the current state.
func (s *Store) SetTenantState(ctx context.Context, tenantID string, state TenantState, version int64) (TenantBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var cur string
	var curVer int64
	if err = tx.QueryRowContext(ctx, `SELECT state, desired_version FROM phoenix_tenant_bindings WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&cur, &curVer); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TenantBinding{}, newErr(http.StatusNotFound, CodeTenantNotFound, "tenant has no boundary")
		}
		return TenantBinding{}, err
	}
	switch {
	case version < curVer, version == curVer && TenantState(cur) != state:
		return TenantBinding{}, newErr(http.StatusConflict, CodeStaleVersion, "stale desired version")
	case version == curVer && TenantState(cur) == state:
		// idempotent retry
	default:
		if _, err = tx.ExecContext(ctx, `UPDATE phoenix_tenant_bindings SET state=$2, desired_version=$3, updated_at=$4 WHERE tenant_id=$1`, tenantID, string(state), version, s.nowMS()); err != nil {
			return TenantBinding{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return TenantBinding{}, err
	}
	return s.GetTenant(ctx, tenantID)
}

// ---- user bindings -----------------------------------------------------------------

func (s *Store) GetUser(ctx context.Context, issuer, sub string) (UserBinding, error) {
	var b UserBinding
	var kind, access string
	err := s.db.QueryRowContext(ctx, `SELECT issuer, sub, tenant_id, native_user_id, actor_kind, access_state, access_version
		FROM phoenix_user_bindings WHERE issuer=$1 AND sub=$2`, issuer, sub).
		Scan(&b.Issuer, &b.Sub, &b.TenantID, &b.NativeUserID, &kind, &access, &b.AccessVersion)
	b.ActorKind, b.AccessState = ActorKind(kind), AccessState(access)
	return b, err
}

// AttachUser records the PENDING binding of a native account; it never grants access.
// The same (issuer, sub) under a different tenant is CHAT_SINGLE_TENANT_REQUIRED.
func (s *Store) AttachUser(ctx context.Context, issuer, sub, tenantID, nativeUserID string, kind ActorKind) (UserBinding, error) {
	now := s.nowMS()
	_, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_user_bindings (issuer, sub, tenant_id, native_user_id, actor_kind, access_state, access_version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'PENDING',0,$6,$6) ON CONFLICT (issuer, sub) DO NOTHING`, issuer, sub, tenantID, nativeUserID, string(kind), now)
	if err != nil {
		if isUniqueViolation(err) {
			return UserBinding{}, newErr(http.StatusConflict, CodeOperationConflict, "native user already bound to another principal")
		}
		return UserBinding{}, err
	}
	b, err := s.GetUser(ctx, issuer, sub)
	if err != nil {
		return b, err
	}
	if b.TenantID != tenantID {
		return b, newErr(http.StatusConflict, CodeSingleTenant, "principal is already bound to another chat tenant")
	}
	if b.NativeUserID != nativeUserID {
		return b, newErr(http.StatusConflict, CodeOperationConflict, "principal is already bound to a different native user")
	}
	return b, nil
}

// SetAccess changes the access state under the per-user monotonic version rule.
// It returns the binding after the change and whether state was modified.
func (s *Store) SetAccess(ctx context.Context, issuer, sub string, state AccessState, version int64) (UserBinding, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserBinding{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var cur string
	var curVer int64
	if err = tx.QueryRowContext(ctx, `SELECT access_state, access_version FROM phoenix_user_bindings WHERE issuer=$1 AND sub=$2 FOR UPDATE`, issuer, sub).Scan(&cur, &curVer); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserBinding{}, false, newErr(http.StatusNotFound, CodeUserNotFound, "no native account for principal")
		}
		return UserBinding{}, false, err
	}
	changed := false
	switch {
	case version < curVer, version == curVer && AccessState(cur) != state && curVer > 0:
		return UserBinding{}, false, newErr(http.StatusConflict, CodeStaleVersion, "stale desired version")
	case version == curVer && AccessState(cur) == state:
		// idempotent retry
	default:
		if _, err = tx.ExecContext(ctx, `UPDATE phoenix_user_bindings SET access_state=$3, access_version=$4, updated_at=$5 WHERE issuer=$1 AND sub=$2`, issuer, sub, string(state), version, s.nowMS()); err != nil {
			if isCheckViolation(err) {
				return UserBinding{}, false, newErr(http.StatusConflict, CodeStaleVersion, "stale desired version")
			}
			return UserBinding{}, false, err
		}
		changed = true
	}
	if err = tx.Commit(); err != nil {
		return UserBinding{}, false, err
	}
	b, err := s.GetUser(ctx, issuer, sub)
	return b, changed, err
}

// TenantUserIDs lists the native ids bound to a tenant (used to revoke sessions on suspend).
func (s *Store) TenantUserIDs(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT native_user_id FROM phoenix_user_bindings WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ConsumeReplay records a one-time key; it returns false when the key was already used.
func (s *Store) ConsumeReplay(ctx context.Context, kind, key string, expiresAtMS int64) (bool, error) {
	now := s.nowMS()
	// Opportunistic expiry keeps the table bounded without a separate job.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM phoenix_replay_keys WHERE expires_at < $1`, now)
	res, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_replay_keys (kind, replay_key, expires_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, kind, key, expiresAtMS)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// keyedMutex serializes work per key inside this process (single native replica).
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) Lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*refLock{}
	}
	l := k.locks[key]
	if l == nil {
		l = &refLock{}
		k.locks[key] = l
	}
	l.refs++
	k.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// RegisterOperator records an operator account. Only the infrastructure bootstrap command calls
// it; no HTTP surface writes this table. Re-running is idempotent and re-enables the operator.
func (s *Store) RegisterOperator(ctx context.Context, nativeUserID, auditRef string) error {
	if nativeUserID == "" || auditRef == "" {
		return errors.New("operator user id and audit reference required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_operator_accounts (native_user_id, enabled, audit_ref, created_at)
		VALUES ($1, true, $2, $3) ON CONFLICT (native_user_id) DO UPDATE SET enabled = true, audit_ref = EXCLUDED.audit_ref`,
		nativeUserID, auditRef, s.nowMS())
	return err
}

// IsOperator reports whether a native user is an enabled registered operator.
func (s *Store) IsOperator(ctx context.Context, nativeUserID string) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM phoenix_operator_accounts WHERE native_user_id=$1`, nativeUserID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}
