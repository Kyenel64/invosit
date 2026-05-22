package cmd

import (
	"errors"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/tui"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize an invosit workspace",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		apiClient := apiclient.NewClient(baseURL())
		workspaces, err := apiClient.GetWorkspaces(cmd.Context(), creds.SessionToken)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			}
			return err
		}
		if len(workspaces) == 0 {
			return errors.New("you don't have access to any workspaces")
		}

		labels := make([]string, len(workspaces))
		for i, workspace := range workspaces {
			labels[i] = workspace.Name
		}
		selectedIdx, err := tui.Select("Select a workspace:", labels)
		if err != nil {
			return err
		}
		_ = workspaces[selectedIdx]

		// TODO: list envs via apiClient.GetEnvironments(ctx, token, chosen.ID),
		// run tui.Select on env names, then write the manifest.

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
