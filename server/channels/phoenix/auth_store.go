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
	SessionID     string
	Issuer        string
	Sub           string
	SID           string
	TenantID      string
	NativeUserID  string
	AccessVersion int64
	ExpiresAtMS   int64
}

// InsertSessionBinding attaches the immutable binding to a native session.
func (s *Store) InsertSessionBinding(ctx context.Context, b SessionBinding) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO phoenix_session_bindings (session_id, issuer, sub, sid, tenant_id, native_user_id, access_version, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, b.SessionID, b.Issuer, b.Sub, b.SID, b.TenantID, b.NativeUserID, b.AccessVersion, b.ExpiresAtMS)
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

// SessionBindingByID loads one binding.
func (s *Store) SessionBindingByID(ctx context.Context, sessionID string) (SessionBinding, error) {
	var b SessionBinding
	err := s.db.QueryRowContext(ctx, `SELECT session_id, issuer, sub, sid, tenant_id, native_user_id, access_version, expires_at
		FROM phoenix_session_bindings WHERE session_id=$1`, sessionID).
		Scan(&b.SessionID, &b.Issuer, &b.Sub, &b.SID, &b.TenantID, &b.NativeUserID, &b.AccessVersion, &b.ExpiresAtMS)
	return b, err
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
