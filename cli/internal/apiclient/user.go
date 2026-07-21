package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrPublicKeyMismatch is returned when the account already has a different
// public key registered. This means only one user at any given time.
// Multi-user login will come in the future.
var ErrPublicKeyMismatch = errors.New("a different public key is already registered")

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) Me(ctx context.Context, token string) (User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/auth/me", nil)
	if err != nil {
		return User{}, fmt.Errorf("failed to build /auth/me request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("failed request to /auth/me: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var user User
		if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
			return User{}, fmt.Errorf("failed to decode /auth/me response: %w", err)
		}
		return user, nil
	case http.StatusUnauthorized:
		return User{}, ErrUnauthorized
	default:
		return User{}, parseAPIError(res)
	}
}

// RegisterPublicKey uploads the caller's x25519 public key. Re-registering
// the same key is idempotent
func (c *Client) RegisterPublicKey(ctx context.Context, token string, publicKey []byte) error {
	body, err := json.Marshal(map[string][]byte{"public_key": publicKey})
	if err != nil {
		return fmt.Errorf("failed to encode public key request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/v1/auth/public-key", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build /auth/public-key request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed request to /auth/public-key: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusConflict:
		return ErrPublicKeyMismatch
	default:
		return parseAPIError(res)
	}
}
