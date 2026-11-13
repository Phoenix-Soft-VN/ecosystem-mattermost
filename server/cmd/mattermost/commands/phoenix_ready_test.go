package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Mutation: accepting a version command, status alone, malformed JSON or a false policy gate.
func TestPhoenixRunningReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		good       bool
	}{
		{"ready", `{"ready":true,"enforced":true,"checks":{"schema":"ok","auth":"ok","policy":"ok"}}`, 200, true},
		{"auth", `{"ready":false,"enforced":true,"checks":{"auth":"fail"}}`, 503, false},
		{"schema", `{"ready":true,"enforced":true,"checks":{"schema":"fail"}}`, 200, false},
		{"unverified", `{"ready":false,"enforced":true,"checks":{"policy":"fail"}}`, 200, false},
		{"unenforced", `{"ready":true,"enforced":false,"checks":{"policy":"ok"}}`, 200, false},
		{"empty", `{}`, 200, false}, {"malformed", `ready`, 200, false},
		{"trailing", `{"ready":true,"enforced":true,"checks":{"policy":"ok"}}{}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/phoenix/ready", r.URL.Path)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			err := checkPhoenixReady(context.Background(), srv.URL+"/phoenix/ready")
			if tc.good {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	require.Error(t, checkPhoenixReady(context.Background(), srv.URL+"/phoenix/ready"))
}
