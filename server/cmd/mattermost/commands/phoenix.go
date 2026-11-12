// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
)

var PhoenixCmd = &cobra.Command{
	Use:   "phoenix",
	Short: "Phoenix infrastructure commands",
}

var PhoenixBootstrapOperatorCmd = &cobra.Command{
	Use:   "bootstrap-operator",
	Short: "Create the platform operator account and register it (infrastructure bootstrap only)",
	Long: `Creates (or re-registers) the platform operator as a native system administrator and records it
in phoenix_operator_accounts with an audit reference. This is the only way an operator account
is created: customers, request bodies and a missing binding can never become operators.
The password is read from the file given by --password-file, never from the command line.`,
	Args: cobra.NoArgs,
	RunE: phoenixBootstrapOperatorCmdF,
}

func init() {
	PhoenixBootstrapOperatorCmd.Flags().String("username", "", "operator username")
	PhoenixBootstrapOperatorCmd.Flags().String("email", "", "operator email")
	PhoenixBootstrapOperatorCmd.Flags().String("password-file", "", "file containing the operator password")
	PhoenixBootstrapOperatorCmd.Flags().String("audit-ref", "", "change/ticket reference recorded with the operator")
	PhoenixCmd.AddCommand(PhoenixBootstrapOperatorCmd)
	RootCmd.AddCommand(PhoenixCmd)
}

func phoenixBootstrapOperatorCmdF(command *cobra.Command, _ []string) error {
	username, _ := command.Flags().GetString("username")
	email, _ := command.Flags().GetString("email")
	pwFile, _ := command.Flags().GetString("password-file")
	auditRef, _ := command.Flags().GetString("audit-ref")
	if username == "" || email == "" || pwFile == "" || auditRef == "" {
		return errors.New("--username, --email, --password-file and --audit-ref are required")
	}
	raw, err := os.ReadFile(pwFile)
	if err != nil {
		return errors.Wrap(err, "reading password file")
	}
	password := strings.TrimSpace(string(raw))

	a, err := InitDBCommandContextCobra(command)
	if err != nil {
		return err
	}
	defer a.Srv().Shutdown()
	rctx := request.EmptyContext(a.Log())

	user, appErr := a.GetUserByUsername(username)
	if appErr != nil && appErr.StatusCode != 404 {
		return appErr
	}
	if user == nil {
		user, appErr = a.CreateUser(rctx, &model.User{Username: username, Email: strings.ToLower(email), EmailVerified: true, Password: password})
		if appErr != nil {
			return appErr
		}
	}
	if _, appErr = a.UpdateUserRoles(rctx, user.Id, model.SystemAdminRoleId+" "+model.SystemUserRoleId, false); appErr != nil {
		return appErr
	}
	store := phoenix.NewStore(a.Srv().Store().GetInternalMasterDB(), nil)
	if err := store.RegisterOperator(context.Background(), user.Id, auditRef); err != nil {
		return err
	}
	fmt.Printf("operator registered userId=%s auditRef=%s\n", user.Id, auditRef)
	return nil
}
