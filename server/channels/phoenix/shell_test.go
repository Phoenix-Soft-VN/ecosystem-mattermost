// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type shellSession struct{ session, user string }

func (s shellSession) SessionFromRequest(context.Context, *http.Request) (string, string, string, bool) {
	return s.session, s.user, "native-csrf", true
}

// Break: trusting browser identity, missing CSRF/Origin checks, or replacing upstream errors.
func TestShellContract(t *testing.T) {
	f := newFixture(t)
	world := newWorld(f)
	status := 200
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" {
			t.Error("Gateway bridge must use POST")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := body["assertion"].(string)
		parts := strings.Split(raw, ".")
		if len(parts) != 3 {
			t.Error("missing assertion")
			return
		}
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(payload, &claims)
		if body["appCode"] != "chat" || claims["sub"] != "sub-a1" || claims["tenant_id"] != world.TenantA || claims["sid"] != "sid-a1" {
			t.Error("browser overrode canonical identity")
		}
		expected := "gateway:internal:ui-shell-context"
		if r.URL.Path == "/internal/ui/preferences" {
			expected = "gateway:internal:ui-preferences"
			if r.Header.Get("If-Match") != "\"7\"" {
				t.Error("preference version dropped")
			}
		}
		if claims["aud"] != expected {
			t.Error("wrong operation audience")
		}
		w.Header().Set("ETag", "\"8\"")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"mode":"dark"}`))
	}))
	defer upstream.Close()
	service := &ShellService{Policy: world.policy(), Sessions: shellSession{world.SessA1, world.A1}, Signer: &BridgeSigner{Issuer: "native", Secret: []byte("test-bridge")}, GatewayURL: upstream.URL, SiteOrigin: "https://chat.mm-replace.test", HTTP: upstream.Client()}
	contextResponse := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://chat.mm-replace.test/phoenix/shell-context?tenant=B&sid=other", nil)
	req.Header.Set("X-Tenant-ID", "B")
	service.ShellContext(contextResponse, req)
	if contextResponse.Code != 200 || contextResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("shell context must be no-store")
	}
	request := func(origin, csrf, content, body string) *http.Request {
		r := httptest.NewRequest("PATCH", "https://chat.mm-replace.test/phoenix/ui-preferences", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", content)
		r.Header.Set("If-Match", "\"7\"")
		return r
	}
	for _, tc := range []struct {
		origin, csrf, content, body string
		want                        int
	}{
		{service.SiteOrigin, "", "application/json", `{"mode":"dark"}`, 403},
		{"https://foreign.invalid", "native-csrf", "application/json", `{"mode":"dark"}`, 403},
		{service.SiteOrigin, "native-csrf", "text/plain", `{"mode":"dark"}`, 415},
		{service.SiteOrigin, "native-csrf", "application/json", `{"mode":"dark","tenant":"B"}`, 400},
	} {
		before := calls
		out := httptest.NewRecorder()
		service.UIPreferences(out, request(tc.origin, tc.csrf, tc.content, tc.body))
		if out.Code != tc.want || calls != before {
			t.Fatalf("invalid preference reached upstream: status=%d calls=%d", out.Code, calls)
		}
	}
	for _, want := range []int{200, 401, 403, 409} {
		status = want
		out := httptest.NewRecorder()
		service.UIPreferences(out, request(service.SiteOrigin, "native-csrf", "application/json", `{"mode":"dark"}`))
		if out.Code != want || out.Header().Get("ETag") != "\"8\"" {
			t.Fatalf("upstream status/version lost: %d", out.Code)
		}
	}
}
