// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork.

package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(&cobra.Command{
		Use: "phoenix-ready", Short: "Check the running Phoenix policy readiness gate",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return checkPhoenixReady(cmd.Context(), "http://127.0.0.1:8065/phoenix/ready")
		},
	})
}

func checkPhoenixReady(ctx context.Context, endpoint string) error {
	unready := errors.New("Phoenix server is not ready")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unready
	}
	client := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return unready
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return unready
	}
	var body struct {
		Ready    bool              `json:"ready"`
		Enforced bool              `json:"enforced"`
		Checks   map[string]string `json:"checks"`
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 65536))
	if dec.Decode(&body) != nil || !body.Ready || !body.Enforced || len(body.Checks) == 0 {
		return unready
	}
	if dec.Decode(new(any)) != io.EOF {
		return unready
	}
	for _, state := range body.Checks {
		if state != "ok" {
			return unready
		}
	}
	return nil
}
