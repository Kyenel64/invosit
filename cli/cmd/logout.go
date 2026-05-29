package cmd

import (
	"errors"
	"fmt"

	"github.com/kyenel64/invosit/cli/internal/credstore"
	"github.com/kyenel64/invosit/cli/internal/kratos"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout of invosit",
	RunE: func(cmd *cobra.Command, args []string) error {
		fileStore, err := credstore.NewFileStore("")
		if err != nil {
			return fmt.Errorf("failed to create new filestore: %w", err)
		}

		creds, err := fileStore.Load()
		if err != nil {
			if errors.Is(err, credstore.ErrNotFound) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "already logged out")
				return nil
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "could not read credentials: %v; clearing local credentials anyway\n", err)
			if cErr := fileStore.Clear(); cErr != nil {
				return fmt.Errorf("failed to clear credentials: %w", cErr)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
		}

		if creds.SessionToken != "" {
			kc := kratos.NewClient(creds.KratosURL)
			if rErr := kc.Logout(cmd.Context(), creds.SessionToken); rErr != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "could not revoke session server-side (offline?): %v; clearing local credentials anyway\n", rErr)
			}
		}

		if err := fileStore.Clear(); err != nil {
			return fmt.Errorf("failed to clear credentials: %w", err)
		}

		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logged out")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
