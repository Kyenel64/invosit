package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
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
