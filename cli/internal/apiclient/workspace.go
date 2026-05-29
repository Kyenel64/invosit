package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) CreateWorkspace(ctx context.Context, token, name string) (Workspace, error) {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to encode workspace request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/workspaces", bytes.NewReader(body))
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to build /workspaces request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return Workspace{}, fmt.Errorf("failed request to /workspaces: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusCreated:
		var workspace Workspace
		if err := json.NewDecoder(res.Body).Decode(&workspace); err != nil {
			return Workspace{}, fmt.Errorf("failed to decode /workspaces response: %w", err)
		}
		return workspace, nil
	case http.StatusUnauthorized:
		return Workspace{}, ErrUnauthorized
	default:
		return Workspace{}, fmt.Errorf("unexpected status: %d", res.StatusCode)
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
