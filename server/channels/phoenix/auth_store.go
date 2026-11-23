// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"database/sql"
	"errors"
)

// AuthTxn is the server-side state of one login attempt (contracts C1 phoenix_auth_transactions).
type AuthTxn struct {
	NonceHash       string
	EncryptedPKCE   string
	ReturnPath      string
	ExpiresAtMS     int64
	StateHash       string
	BrowserBindHash string
}

// CreateAuthTxn stores a login transaction. Only hashes of state/nonce/browser binding are
// stored, and the PKCE verifier is already encrypted by the caller.
func (s *Store) CreateAuthTxn(ctx context.Context, t AuthTxn) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_auth_transactions (state_hash, nonce_hash, browser_hash, pkce_verifier, return_path, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.StateHash, t.NonceHash, t.BrowserBindHash, t.EncryptedPKCE, t.ReturnPath, t.ExpiresAtMS)
	if err == nil {
		// Opportunistic cleanup keeps abandoned transactions bounded.
		_, _ = s.db.ExecContext(ctx, `DELETE FROM phoenix_auth_transactions WHERE expires_at < $1`, s.nowMS()-3_600_000)
	}
	return err
}

// ErrTxnInvalid covers unknown, expired, consumed and browser-mismatched transactions alike;
// the caller never learns which, and a browser mismatch does not consume the victim's transaction.
var ErrTxnInvalid = errors.New("authentication transaction invalid")

// ConsumeAuthTxn atomically consumes a transaction: a single UPDATE succeeds exactly once, only
// before expiry and only for the browser that started it.
func (s *Store) ConsumeAuthTxn(ctx context.Context, stateHash, browserHash string) (AuthTxn, error) {
	var t AuthTxn
	now := s.nowMS()
	err := s.db.QueryRowContext(ctx, `UPDATE phoenix_auth_transactions SET consumed_at=$3
		WHERE state_hash=$1 AND browser_hash=$2 AND consumed_at IS NULL AND expires_at > $3
		RETURNING nonce_hash, pkce_verifier, return_path, expires_at`, stateHash, browserHash, now).
		Scan(&t.NonceHash, &t.EncryptedPKCE, &t.ReturnPath, &t.ExpiresAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthTxn{}, ErrTxnInvalid
	}
	t.StateHash, t.BrowserBindHash = stateHash, browserHash
	return t, err
}

// SessionBinding mirrors phoenix_session_bindings.
type SessionBinding struct {
	SessionID           string
	Issuer              string
	Sub                 string
	SID                 string
	TenantID            string
	NativeUserID        string
	AccessVersion       int64
	ExpiresAtMS         int64
	AccessTokenEnc      string
	RefreshTokenEnc     string
	AccessExpiresAtMS   int64
	IdleExpiresAtMS     int64
	AbsoluteExpiresAtMS int64
	OutageSinceMS       int64
}

// InsertSessionBinding attaches the immutable binding to a native session.
func (s *Store) InsertSessionBinding(ctx context.Context, b SessionBinding) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_session_bindings
		(session_id, issuer, sub, sid, tenant_id, native_user_id, access_version, expires_at,
		 access_token_enc, refresh_token_enc, access_expires_at, idle_expires_at, absolute_expires_at, outage_since)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		b.SessionID, b.Issuer, b.Sub, b.SID, b.TenantID, b.NativeUserID, b.AccessVersion, b.ExpiresAtMS,
		b.AccessTokenEnc, b.RefreshTokenEnc, b.AccessExpiresAtMS, b.IdleExpiresAtMS, b.AbsoluteExpiresAtMS, b.OutageSinceMS)
	return err
}

// SessionIDsBySID returns the native sessions bound to one Gateway sid (and only that sid).
func (s *Store) SessionIDsBySID(ctx context.Context, sid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM phoenix_session_bindings WHERE sid=$1`, sid)
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

// SessionIDsBySub returns native sessions bound to (issuer, sub).
func (s *Store) SessionIDsBySub(ctx context.Context, issuer, sub string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM phoenix_session_bindings WHERE issuer=$1 AND sub=$2`, issuer, sub)
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

// SessionBindingByID loads one binding.
func (s *Store) SessionBindingByID(ctx context.Context, sessionID string) (SessionBinding, error) {
	var b SessionBinding
	err := s.db.QueryRowContext(ctx, `SELECT session_id, issuer, sub, sid, tenant_id, native_user_id, access_version, expires_at,
		COALESCE(access_token_enc, ''), COALESCE(refresh_token_enc, ''), COALESCE(access_expires_at, 0),
		COALESCE(idle_expires_at, 0), COALESCE(absolute_expires_at, 0), COALESCE(outage_since, 0)
		FROM phoenix_session_bindings WHERE session_id=$1`, sessionID).
		Scan(&b.SessionID, &b.Issuer, &b.Sub, &b.SID, &b.TenantID, &b.NativeUserID, &b.AccessVersion, &b.ExpiresAtMS,
			&b.AccessTokenEnc, &b.RefreshTokenEnc, &b.AccessExpiresAtMS, &b.IdleExpiresAtMS, &b.AbsoluteExpiresAtMS, &b.OutageSinceMS)
	return b, err
}

// LockedSessionBinding keeps refresh writes on the transaction owning the row lock.
type LockedSessionBinding struct {
	SessionBinding
	tx *sql.Tx
}

// UpdateSessionTokens persists rotated tokens before releasing the refresh lock.
func (b *LockedSessionBinding) UpdateSessionTokens(ctx context.Context, accessEnc, refreshEnc string, accessExpMS, idleExpMS, outageSinceMS int64) error {
	_, err := b.tx.ExecContext(ctx, `UPDATE phoenix_session_bindings
		SET access_token_enc=$2, refresh_token_enc=CASE WHEN $3<>'' THEN $3 ELSE refresh_token_enc END,
		    access_expires_at=$4, idle_expires_at=$5, outage_since=$6
		WHERE session_id=$1`,
		b.SessionID, accessEnc, refreshEnc, accessExpMS, idleExpMS, outageSinceMS)
	return err
}

// MarkSessionOutage records when Gateway outage started (if not already set).
func (b *LockedSessionBinding) MarkSessionOutage(ctx context.Context, sinceMS int64) error {
	_, err := b.tx.ExecContext(ctx, `UPDATE phoenix_session_bindings
		SET outage_since=CASE WHEN outage_since=0 THEN $2 ELSE outage_since END
		WHERE session_id=$1`, b.SessionID, sinceMS)
	return err
}

// ExpireAccessBySub zeroes access_expires_at for all sessions of a subject so their next request refreshes via Gateway.
func (s *Store) ExpireAccessBySub(ctx context.Context, issuer, sub string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `UPDATE phoenix_session_bindings SET access_expires_at=0 WHERE issuer=$1 AND sub=$2 RETURNING session_id`, issuer, sub)
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

// WithLockedSessionBinding runs fn inside a DB transaction holding FOR UPDATE on the session binding row.
func (s *Store) WithLockedSessionBinding(ctx context.Context, sessionID string, fn func(*LockedSessionBinding) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var b SessionBinding
	err = tx.QueryRowContext(ctx, `SELECT session_id, issuer, sub, sid, tenant_id, native_user_id, access_version, expires_at,
		COALESCE(access_token_enc, ''), COALESCE(refresh_token_enc, ''), COALESCE(access_expires_at, 0),
		COALESCE(idle_expires_at, 0), COALESCE(absolute_expires_at, 0), COALESCE(outage_since, 0)
		FROM phoenix_session_bindings WHERE session_id=$1 FOR UPDATE`, sessionID).
		Scan(&b.SessionID, &b.Issuer, &b.Sub, &b.SID, &b.TenantID, &b.NativeUserID, &b.AccessVersion, &b.ExpiresAtMS,
			&b.AccessTokenEnc, &b.RefreshTokenEnc, &b.AccessExpiresAtMS, &b.IdleExpiresAtMS, &b.AbsoluteExpiresAtMS, &b.OutageSinceMS)
	if err != nil {
		return err
	}
	if err := fn(&LockedSessionBinding{SessionBinding: b, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// HasReplay reports whether a replay key has already been consumed without consuming it.
func (s *Store) HasReplay(ctx context.Context, kind, key string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM phoenix_replay_keys WHERE kind=$1 AND replay_key=$2 AND expires_at >= $3`, kind, key, s.nowMS()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// TenantTeamName returns the Team name of a tenant for the post-login redirect.
func (s *Store) TenantTeamName(ctx context.Context, tenantID string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT t.name FROM phoenix_tenant_bindings tb JOIN teams t ON t.id = tb.team_id WHERE tb.tenant_id=$1`, tenantID).Scan(&name)
	return name, err
}

// ActiveSessionIDsForTenant lists bound sessions of a tenant (suspend / drift closure).
func (s *Store) ActiveSessionIDsForTenant(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM phoenix_session_bindings WHERE tenant_id=$1`, tenantID)
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

// SessionIDsOfUser lists bound sessions of one native user.
func (s *Store) SessionIDsOfUser(ctx context.Context, nativeUserID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM phoenix_session_bindings WHERE native_user_id=$1`, nativeUserID)
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

// LiveSessionBindingsOfUser returns non-expired, non-operator session bindings of one native user.
func (s *Store) LiveSessionBindingsOfUser(ctx context.Context, nativeUserID string) ([]SessionBinding, error) {
	now := s.nowMS()
	rows, err := s.db.QueryContext(ctx, `SELECT b.session_id, b.issuer, b.sub, b.sid, b.tenant_id, b.native_user_id, b.access_version, b.expires_at
		FROM phoenix_session_bindings b
		WHERE b.native_user_id=$1
		  AND (b.expires_at = 0 OR b.expires_at > $2)
		  AND (COALESCE(b.idle_expires_at, 0) = 0 OR b.idle_expires_at > $2)
		  AND (COALESCE(b.absolute_expires_at, 0) = 0 OR b.absolute_expires_at > $2)
		  AND NOT EXISTS (SELECT 1 FROM phoenix_operator_accounts oa WHERE oa.native_user_id = b.native_user_id AND oa.enabled)
		ORDER BY b.session_id`, nativeUserID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionBinding
	for rows.Next() {
		var b SessionBinding
		if err := rows.Scan(&b.SessionID, &b.Issuer, &b.Sub, &b.SID, &b.TenantID, &b.NativeUserID, &b.AccessVersion, &b.ExpiresAtMS); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LiveSessionBindings returns all non-expired, non-operator session bindings for bounded periodic refresh.
func (s *Store) LiveSessionBindings(ctx context.Context) ([]SessionBinding, error) {
	now := s.nowMS()
	rows, err := s.db.QueryContext(ctx, `SELECT b.session_id, b.issuer, b.sub, b.sid, b.tenant_id, b.native_user_id, b.access_version, b.expires_at
		FROM phoenix_session_bindings b
		WHERE (b.expires_at = 0 OR b.expires_at > $1)
		  AND (COALESCE(b.idle_expires_at, 0) = 0 OR b.idle_expires_at > $1)
		  AND (COALESCE(b.absolute_expires_at, 0) = 0 OR b.absolute_expires_at > $1)
		  AND NOT EXISTS (SELECT 1 FROM phoenix_operator_accounts oa WHERE oa.native_user_id = b.native_user_id AND oa.enabled)
		ORDER BY b.native_user_id, b.session_id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionBinding
	for rows.Next() {
		var b SessionBinding
		if err := rows.Scan(&b.SessionID, &b.Issuer, &b.Sub, &b.SID, &b.TenantID, &b.NativeUserID, &b.AccessVersion, &b.ExpiresAtMS); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
