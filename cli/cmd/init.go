package cmd

import (
	"errors"
	"fmt"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
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
				return errors.New("Unauthorized request to GET /workspaces")
			}
			return err
		}

		out := cmd.OutOrStdout()
		for _, workspace := range workspaces {
			fmt.Fprintf(out, "- %s\n", workspace.Name)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
