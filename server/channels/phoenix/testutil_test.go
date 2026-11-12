// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

// These tests run against a real, fully migrated native schema (PHOENIX_TEST_DSN, one cloned
// database per check). A missing DSN is a failure, never a skip: a skipped test is not a pass.

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PHOENIX_TEST_DSN")
	if dsn == "" {
		t.Fatal("PHOENIX_TEST_DSN is required: these checks run against the real native schema")
	}
	return dsn
}

// countingConnector counts every statement the database/sql layer sends, to prove query counts.
type countingConnector struct {
	inner driver.Connector
	count *atomic.Int64
}

func (c countingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, count: c.count}, nil
}
func (c countingConnector) Driver() driver.Driver { return c.inner.Driver() }

type countingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c *countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

type fixture struct {
	t     *testing.T
	db    *sql.DB
	count *atomic.Int64
	seq   int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cn, err := pq.NewConnector(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	db := sql.OpenDB(countingConnector{inner: cn, count: &n})
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("native test database is not reachable: %v", err)
	}
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('phoenix_tenant_bindings') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("native schema lacks the Phoenix tables (migration 000243 not applied): %v", err)
	}
	return &fixture{t: t, db: db, count: &n}
}

// id returns a unique 26-character native id.
func (f *fixture) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%0*d", prefix, 26-len(prefix), f.seq)
}

// name returns a unique short lowercase token for usernames, team names and tenant keys.
func (f *fixture) name(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%d", prefix, f.seq)
}

// insert adds one row, auto-filling every NOT NULL column that has no default so the test only
// names the columns that matter for the behavior under test.
func (f *fixture) insert(table string, vals map[string]any) {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND is_nullable = 'NO' AND column_default IS NULL`, table)
	if err != nil {
		f.t.Fatal(err)
	}
	cols := map[string]any{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			f.t.Fatal(err)
		}
		switch typ {
		case "bigint", "integer", "smallint", "numeric":
			cols[name] = 0
		case "boolean":
			cols[name] = false
		case "json", "jsonb":
			cols[name] = "{}"
		default:
			cols[name] = ""
		}
	}
	rows.Close()
	for k, v := range vals {
		cols[strings.ToLower(k)] = v
	}
	names, ph, args := []string{}, []string{}, []any{}
	for k, v := range cols {
		names = append(names, k)
		args = append(args, v)
		ph = append(ph, fmt.Sprintf("$%d", len(args)))
	}
	if _, err := f.db.Exec(`INSERT INTO `+table+` (`+strings.Join(names, ",")+`) VALUES (`+strings.Join(ph, ",")+`)`, args...); err != nil {
		f.t.Fatalf("insert into %s: %v", table, err)
	}
}

type world struct {
	f                      *fixture
	TenantA, TenantB       string
	TeamA, TeamB           string
	ChanA, ChanB           string
	A1, A2, B1, Op, Loner  string
	SessA1, SessA2, SessB1 string
	SessOp, SessLoner      string
	IssuerURL              string
}

const testIssuer = "https://auth.mm-replace.test"

// newWorld builds two tenants with a Team, a default channel and customers each, an operator and
// an unbound native account, plus a live session (and binding) per principal.
func newWorld(f *fixture) *world {
	w := &world{f: f, TenantA: f.name("tenant-a-"), TenantB: f.name("tenant-b-"), IssuerURL: testIssuer}
	now := time.Now().UnixMilli()
	mkTeam := func(name string) string {
		id := f.id("tm")
		f.insert("teams", map[string]any{"id": id, "name": name, "displayname": name, "type": "I", "allowopeninvite": false, "deleteat": 0, "createat": now})
		return id
	}
	mkChan := func(team, name string) string {
		id := f.id("ch")
		f.insert("channels", map[string]any{"id": id, "teamid": team, "name": name, "displayname": name, "type": "O", "createat": now})
		return id
	}
	mkUser := func(name string) string {
		id := f.id("us")
		f.insert("users", map[string]any{"id": id, "username": name, "email": name + "@t.invalid", "createat": now})
		return id
	}
	w.TeamA, w.TeamB = mkTeam(f.name("team")), mkTeam(f.name("team"))
	w.ChanA, w.ChanB = mkChan(w.TeamA, "town-square"), mkChan(w.TeamB, "town-square")
	f.insert("phoenix_tenant_bindings", map[string]any{"tenant_id": w.TenantA, "team_id": w.TeamA, "default_channel_id": w.ChanA, "state": "ACTIVE", "policy_version": CurrentPolicyVersion, "created_at": now, "updated_at": now})
	f.insert("phoenix_tenant_bindings", map[string]any{"tenant_id": w.TenantB, "team_id": w.TeamB, "default_channel_id": w.ChanB, "state": "ACTIVE", "policy_version": CurrentPolicyVersion, "created_at": now, "updated_at": now})
	w.A1, w.A2, w.B1, w.Op, w.Loner = mkUser(f.name("a1u")), mkUser(f.name("a2u")), mkUser(f.name("b1u")), mkUser(f.name("opu")), mkUser(f.name("lou"))
	bind := func(sub, tenant, user string) {
		f.insert("phoenix_user_bindings", map[string]any{"issuer": testIssuer, "sub": sub, "tenant_id": tenant, "native_user_id": user, "actor_kind": "CUSTOMER", "access_state": "GRANTED", "access_version": 1, "created_at": now, "updated_at": now})
	}
	bind("sub-a1", w.TenantA, w.A1)
	bind("sub-a2", w.TenantA, w.A2)
	bind("sub-b1", w.TenantB, w.B1)
	f.insert("phoenix_operator_accounts", map[string]any{"native_user_id": w.Op, "enabled": true, "audit_ref": "test-bootstrap", "created_at": now})
	w.SessA1, w.SessA2, w.SessB1 = w.session("sub-a1", "sid-a1", w.TenantA, w.A1), w.session("sub-a2", "sid-a2", w.TenantA, w.A2), w.session("sub-b1", "sid-b1", w.TenantB, w.B1)
	w.SessOp = f.id("se")
	f.insert("sessions", map[string]any{"id": w.SessOp, "token": f.id("tk"), "userid": w.Op, "createat": now, "expiresat": now + 3_600_000, "lastactivityat": now})
	w.SessLoner = f.id("se")
	f.insert("sessions", map[string]any{"id": w.SessLoner, "token": f.id("tk"), "userid": w.Loner, "createat": now, "expiresat": now + 3_600_000, "lastactivityat": now})
	return w
}

func (w *world) session(sub, sid, tenant, user string) string {
	now := time.Now().UnixMilli()
	id := w.f.id("se")
	w.f.insert("sessions", map[string]any{"id": id, "token": w.f.id("tk"), "userid": user, "createat": now, "expiresat": now + 3_600_000, "lastactivityat": now})
	w.f.insert("phoenix_session_bindings", map[string]any{"session_id": id, "issuer": testIssuer, "sub": sub, "sid": sid, "tenant_id": tenant, "native_user_id": user, "access_version": 1, "expires_at": now + 3_600_000})
	return id
}

func (w *world) policy() *Policy {
	p := NewPolicy(w.f.db, nil)
	p.SetDrift("", nil)
	return p
}

// ---- RSA test keys + JWKS ---------------------------------------------------------------

type testKey struct {
	kid string
	key *rsa.PrivateKey
}

func newTestKey(t *testing.T, kid string) testKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{kid: kid, key: k}
}

func (k testKey) jwks() []byte {
	enc := func(b *big.Int) string { return base64.RawURLEncoding.EncodeToString(b.Bytes()) }
	doc := map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": k.kid, "use": "sig", "alg": "RS256", "n": enc(k.key.N), "e": enc(big.NewInt(int64(k.key.E)))}}}
	b, _ := json.Marshal(doc)
	return b
}

type staticKeys map[string]*rsa.PublicKey

func (s staticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if k, ok := s[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key %s", kid)
}
