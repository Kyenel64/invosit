package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

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
		return User{}, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}
