// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The webapp discovers prepackaged plugin bundles (Playbooks) through GET /plugins/webapp, so
// customers must reach exactly that read; every other plugin route stays operator-only.
func TestPhoenixRouteBlockedPlugins(t *testing.T) {
	cases := []struct {
		method, path string
		blocked      bool
	}{
		{http.MethodGet, "/api/v4/plugins/webapp", false},
		{http.MethodPost, "/api/v4/plugins/webapp", true},
		{http.MethodGet, "/api/v4/plugins", true},
		{http.MethodPost, "/api/v4/plugins", true},
		{http.MethodGet, "/api/v4/plugins/marketplace", true},
		{http.MethodPost, "/api/v4/plugins/playbooks/enable", true},
		{http.MethodDelete, "/api/v4/plugins/playbooks", true},
		{http.MethodGet, "/api/v4/plugins/webapp/extra", true},
		{http.MethodGet, "/api/v4/config/client", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := phoenixRouteBlocked(r); got != tc.blocked {
			t.Errorf("%s %s blocked=%v, want %v", tc.method, tc.path, got, tc.blocked)
		}
	}
}
