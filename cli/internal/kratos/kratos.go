package kratos

import (
	"context"
	"fmt"
	"net/http"

	ory "github.com/ory/client-go"
)

type Client struct {
	sdk *ory.APIClient
}

func NewClient(baseURL string) *Client {
	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: baseURL}}
	return &Client{sdk: ory.NewAPIClient(cfg)}
}

// Logout revokes a session token server-side via Kratos's native logout.
func (c *Client) Logout(ctx context.Context, sessionToken string) error {
	resp, err := c.sdk.FrontendAPI.PerformNativeLogout(ctx).
		PerformNativeLogoutBody(ory.PerformNativeLogoutBody{SessionToken: sessionToken}).
		Execute()
	if err != nil {
		// 403 means the token is already invalid/expired/unknown — nothing left to revoke.
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			return nil
		}
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}
