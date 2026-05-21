package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/credstore"
	"github.com/kyenel64/invosit/cli/internal/kratos"
	"github.com/spf13/cobra"
)

const (
	defaultKratosURL = "http://127.0.0.1:8000/kratos"
	defaultAPIURL    = "http://127.0.0.1:8000"
	defaultUIURL     = "http://127.0.0.1:8000"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to invosit",
	RunE: func(cmd *cobra.Command, args []string) error {
		fileStore, err := credstore.NewFileStore("")
		if err != nil {
			return fmt.Errorf("failed to create new filestore: %w", err)
		}

		token, err := runBrowserLogin(cmd.Context(), defaultKratosURL, cmd.ErrOrStderr())
		if err != nil {
			return err
		}

		// Check we got a valid session token and user exists in invosit db.
		// We also need this to get our email and user id.
		apiClient := apiclient.NewClient(defaultAPIURL)
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
			KratosURL:    defaultKratosURL,
			APIURL:       defaultAPIURL,
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

func runBrowserLogin(ctx context.Context, kratosURL string, stderr io.Writer) (string, error) {
	client := kratos.NewClient(kratosURL)
	token, err := client.BrowserLogin(ctx, kratos.BrowserLoginOpts{
		UIBaseURL: defaultUIURL,
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
