package cmd

import (
	"errors"
	"fmt"

	"github.com/kyenel64/invosit/cli/internal/credstore"
	"github.com/kyenel64/invosit/cli/internal/keystore"
)

// loadCredentials reads the stored session from the default filestore.
func loadCredentials() (credstore.Credentials, error) {
	fileStore, err := credstore.NewFileStore("")
	if err != nil {
		return credstore.Credentials{}, fmt.Errorf("failed to create new filestore: %w", err)
	}

	creds, err := fileStore.Load()
	if err != nil {
		if errors.Is(err, credstore.ErrNotFound) {
			return credstore.Credentials{}, errors.New("not logged in. Run 'invosit login'")
		}
		return credstore.Credentials{}, fmt.Errorf("failed to load credentials: %w", err)
	}

	return creds, nil
}

// loadPrivateKey reads the user's long-term private key from the default keystore.
func loadPrivateKey(userID string) ([]byte, error) {
	keyStore, err := keystore.NewFileStore("")
	if err != nil {
		return nil, fmt.Errorf("failed to create key store: %w", err)
	}

	privateKey, err := keyStore.Load(userID)
	if err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return nil, errors.New("no encryption key found for this account. run `invosit login`")
		}
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	return privateKey, nil
}
