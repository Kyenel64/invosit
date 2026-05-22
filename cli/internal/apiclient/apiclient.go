package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
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

func (c *Client) GetWorkspaces(ctx context.Context, token string) ([]Workspace, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/workspaces", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build /workspaces request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed request to /workspaces: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var envelope struct {
			Workspaces []Workspace `json:"workspaces"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("failed to decode /workspaces response: %w", err)
		}
		return envelope.Workspaces, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}
