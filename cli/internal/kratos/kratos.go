package kratos

import (
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
