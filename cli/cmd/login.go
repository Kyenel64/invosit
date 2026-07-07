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
	"github.com/kyenel64/invosit/cli/internal/keys"
	"github.com/kyenel64/invosit/cli/internal/keystore"
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

		keyStore, err := keystore.NewFileStore("")
		if err != nil {
			return fmt.Errorf("failed to create key store: %w", err)
		}
		if err := ensureKeypairRegistered(cmd.Context(), keyStore, apiClient, token, user.ID, cmd.ErrOrStderr()); err != nil {
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

// ensureKeypairRegistered makes sure the logged-in user can encrypt and be
// encrypted for: load the private key from the keystore (generate and save
// one on first login), then upload the public half to the API. If the account
// already registered a different key — usually a login from another machine —
// warn and continue: push/pull on this machine still work with the local
// keypair, only cross-machine decryption is affected.
func ensureKeypairRegistered(ctx context.Context, keyStore *keystore.FileStore, apiClient *apiclient.Client, token, userID string, stderr io.Writer) error {
	privateKey, err := keyStore.Load(userID)
	if errors.Is(err, keystore.ErrNotFound) {
		keypair, genErr := keys.Generate()
		if genErr != nil {
			return fmt.Errorf("failed to generate keypair: %w", genErr)
		}
		if saveErr := keyStore.Save(userID, keypair.Private); saveErr != nil {
			return fmt.Errorf("failed to save private key: %w", saveErr)
		}
		privateKey = keypair.Private
	} else if err != nil {
		return fmt.Errorf("failed to load private key: %w", err)
	}

	publicKey, err := keys.PublicFromPrivate(privateKey)
	if err != nil {
		return fmt.Errorf("failed to derive public key: %w", err)
	}

	switch err := apiClient.RegisterPublicKey(ctx, token, publicKey); {
	case errors.Is(err, apiclient.ErrPublicKeyMismatch):
		_, _ = fmt.Fprintf(stderr, "warning: this account already has a different public key registered (from another machine?).\nfiles pushed from other machines can't be decrypted here; copy %s from the original machine to fix\n", keyStore.Path(userID))
		return nil
	case err != nil:
		return fmt.Errorf("failed to register public key: %w", err)
	}
	return nil
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
