package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/credstore"
	"github.com/kyenel64/invosit/cli/internal/kratos"
	"github.com/spf13/cobra"
)

var defaultBaseURL = "http://127.0.0.1:8000" // override in env

func baseURL() string {
	if v := os.Getenv("INVOSIT_BASE_URL"); v != "" {
		return v
	}
	return defaultBaseURL
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to invosit",
	RunE: func(cmd *cobra.Command, args []string) error {
		fileStore, err := credstore.NewFileStore("")
		if err != nil {
			return fmt.Errorf("failed to create new filestore: %w", err)
		}

		base := baseURL()
		kratosURL := base + "/kratos"
		apiURL := base
		uiURL := base

		token, err := runBrowserLogin(cmd.Context(), kratosURL, uiURL, cmd.ErrOrStderr())
		if err != nil {
			return err
		}

		// Check we got a valid session token and user exists in invosit db.
		// We also need this to get our email and user id.
		apiClient := apiclient.NewClient(apiURL)
		user, err := apiClient.Me(cmd.Context(), token)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("login succeeded but server doesn't recognize this user. Check the registration webhook")
			}
			return err
		}

		// Save our credentials to os config path (or override path)
		err = fileStore.Save(credstore.Credentials{
			Version:      credstore.SchemaVersion,
			Email:        user.Email,
			UserID:       user.ID,
			SessionToken: token,
			KratosURL:    kratosURL,
			APIURL:       apiURL,
			SavedAt:      time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to save credentials: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s\n", user.Email)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func runBrowserLogin(ctx context.Context, kratosURL, uiURL string, stderr io.Writer) (string, error) {
	client := kratos.NewClient(kratosURL)
	token, err := client.BrowserLogin(ctx, kratos.BrowserLoginOpts{
		UIBaseURL: uiURL,
		Stderr:    stderr,
	})
	if err != nil {
		if errors.Is(err, kratos.ErrBrowserLoginTimeout) {
			return "", errors.New("browser sign-in timed out, try again")
		}
		return "", err
	}
	return token, nil
}
