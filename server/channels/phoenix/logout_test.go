// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"database/sql"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type logoutSessions struct {
	db  *sql.DB
	own string
}

func (s logoutSessions) NewSession(context.Context, http.ResponseWriter, *http.Request, string, func(string, int64) error) (string, error) {
	return "", errors.New("not used")
}
func (s logoutSessions) RevokeSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}
func (s logoutSessions) SessionIDFromRequest(context.Context, *http.Request) (string, bool) {
	return s.own, s.own != ""
}

// Break: accepting a forged logout, replaying side effects, or letting a browser choose another sid.
func TestLogoutReplay(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	key := newTestKey(t, "gateway-key")
	now := time.Now()
	service := &LogoutService{Store: NewStore(f.db, nil), Settings: Settings{OIDCIssuer: testIssuer, OIDCClientID: "mattermost"}, Keys: staticKeys{"gateway-key": &key.key.PublicKey}, Sessions: logoutSessions{db: f.db, own: w.SessA1}, Now: func() time.Time { return now }, Policy: w.policy()}
	sign := func(change func(jwt.MapClaims)) string {
		c := jwt.MapClaims{"iss": testIssuer, "aud": "mattermost", "iat": now.Unix(), "sid": "sid-a1", "jti": "logout-1", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
		if change != nil {
			change(c)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = "gateway-key"
		raw, err := tok.SignedString(key.key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, change := range []func(jwt.MapClaims){func(c jwt.MapClaims) { c["iss"] = "https://foreign.invalid" }, func(c jwt.MapClaims) { c["aud"] = "foreign" }, func(c jwt.MapClaims) { delete(c, "events") }, func(c jwt.MapClaims) { c["nonce"] = "unexpected" }, func(c jwt.MapClaims) { c["iat"] = now.Add(-time.Hour).Unix() }} {
		if _, _, err := service.VerifyLogoutToken(context.Background(), sign(change)); err == nil {
			t.Fatal("invalid logout accepted")
		}
	}
	raw := sign(nil)
	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://chat.mm-replace.test/phoenix/auth/backchannel-logout", strings.NewReader(url.Values{"logout_token": {raw}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		service.Backchannel(rec, r)
		if rec.Code != 200 {
			t.Fatalf("logout attempt=%d status=%d body=%s", attempt, rec.Code, rec.Body)
		}
		if attempt == 1 && !strings.Contains(rec.Body.String(), `"replay":true`) {
			t.Fatal("replay not idempotent")
		}
	}
	var a, b int
	_ = f.db.QueryRow(`SELECT count(*) FROM sessions WHERE id=$1`, w.SessA1).Scan(&a)
	_ = f.db.QueryRow(`SELECT count(*) FROM sessions WHERE id=$1`, w.SessB1).Scan(&b)
	if a != 0 || b != 1 {
		t.Fatal("logout crossed sid boundary")
	}
	rec := httptest.NewRecorder()
	service.Cleanup(rec, httptest.NewRequest("GET", "https://chat.mm-replace.test/phoenix/logout/cleanup?sid=sid-b1", nil))
	_ = f.db.QueryRow(`SELECT count(*) FROM sessions WHERE id=$1`, w.SessB1).Scan(&b)
	if b != 1 {
		t.Fatal("browser selected victim sid")
	}
}
