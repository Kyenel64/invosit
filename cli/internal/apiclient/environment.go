package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Environment struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
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

func (c *Client) CreateEnvironment(ctx context.Context, token, workspaceID, name string) (Environment, error) {
	body, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	if err != nil {
		return Environment{}, fmt.Errorf("encode create environment request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/workspaces/%s/environments", c.baseURL, workspaceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Environment{}, fmt.Errorf("failed to build /workspaces/%s/environments request: %w", workspaceID, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return Environment{}, fmt.Errorf("failed request to /workspaces/%s/environments: %w", workspaceID, err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusCreated:
		var env Environment
		if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
			return Environment{}, fmt.Errorf("failed to decode /workspaces/%s/environments response: %w", workspaceID, err)
		}
		return env, nil
	case http.StatusUnauthorized:
		return Environment{}, ErrUnauthorized
	default:
		return Environment{}, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}
