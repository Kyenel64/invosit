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

func (c *Client) GetEnvironments(ctx context.Context, token string, workspaceID string) ([]Environment, error) {
	url := fmt.Sprintf("/api/v1/workspaces/%s/environments", workspaceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build /workspaces/%s/environments request: %w", workspaceID, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed request to /workspaces/%s/environments: %w", workspaceID, err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var envelope struct {
			Environments []Environment `json:"environments"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("failed to decode /workspaces/%s/environments response: %w", workspaceID, err)
		}
		return envelope.Environments, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}

type PushFileRequest struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
}

type PushFileResponse struct {
	ID              string    `json:"id"`
	EnvironmentID   string    `json:"environment_id"`
	Path            string    `json:"path"`
	ContentHash     string    `json:"content_hash"`
	Size            int64     `json:"size"`
	PushedBy        string    `json:"pushed_by"`
	PushedAt        time.Time `json:"pushed_at"`
	UploadURL       string    `json:"upload_url"`
	UploadExpiresAt time.Time `json:"upload_expires_at"`
}

func (c *Client) PushFile(ctx context.Context, token, workspaceID, environmentID string, req PushFileRequest) (PushFileResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return PushFileResponse{}, fmt.Errorf("encode push request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/workspaces/%s/environments/%s/files", c.baseURL, workspaceID, environmentID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return PushFileResponse{}, fmt.Errorf("build push request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return PushFileResponse{}, fmt.Errorf("push request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusCreated:
		var decoded PushFileResponse
		if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
			return PushFileResponse{}, fmt.Errorf("decode push response: %w", err)
		}
		return decoded, nil
	case http.StatusUnauthorized:
		return PushFileResponse{}, ErrUnauthorized
	default:
		return PushFileResponse{}, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}
