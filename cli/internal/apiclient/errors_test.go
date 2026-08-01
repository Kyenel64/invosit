package apiclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
)

func errorServer(t *testing.T, status int, body string) *apiclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return apiclient.NewClient(srv.URL)
}

func TestAPIErrorSurfacedByEveryMethod(t *testing.T) {
	calls := []struct {
		name string
		call func(context.Context, *apiclient.Client) error
	}{
		{"Me", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.Me(ctx, "tok")
			return err
		}},
		{"RegisterPublicKey", func(ctx context.Context, c *apiclient.Client) error {
			return c.RegisterPublicKey(ctx, "tok", make([]byte, 32))
		}},
		{"GetWorkspaces", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.GetWorkspaces(ctx, "tok")
			return err
		}},
		{"CreateWorkspace", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.CreateWorkspace(ctx, "tok", "name")
			return err
		}},
		{"GetEnvironments", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.GetEnvironments(ctx, "tok", "ws_1")
			return err
		}},
		{"CreateEnvironment", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.CreateEnvironment(ctx, "tok", "ws_1", "prod")
			return err
		}},
		{"ListFiles", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.ListFiles(ctx, "tok", "ws_1", "env_1")
			return err
		}},
		{"CreateFiles", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.CreateFiles(ctx, "tok", "ws_1", "env_1", nil)
			return err
		}},
		{"CompleteFiles", func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.CompleteFiles(ctx, "tok", "ws_1", "env_1", nil)
			return err
		}},
	}

	const body = `{"error":"invalid workspace name","code":"INVALID_REQUEST"}`

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			client := errorServer(t, http.StatusBadRequest, body)

			err := tc.call(context.Background(), client)
			if err == nil {
				t.Fatal("want error on 400, got nil")
			}
			if errors.Is(err, apiclient.ErrUnauthorized) {
				t.Errorf("400 should not map to ErrUnauthorized")
			}

			var apiErr *apiclient.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *APIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
			}
			if apiErr.Code != "INVALID_REQUEST" {
				t.Errorf("Code = %q, want INVALID_REQUEST", apiErr.Code)
			}
			if apiErr.Message != "invalid workspace name" {
				t.Errorf("Message = %q, want %q", apiErr.Message, "invalid workspace name")
			}
			want := "INVALID_REQUEST: invalid workspace name (status 400)"
			if apiErr.Error() != want {
				t.Errorf("Error() = %q, want %q", apiErr.Error(), want)
			}
		})
	}
}

func TestAPIErrorBodyShapes(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantCode    string
		wantMessage string
		wantText    string
	}{
		{
			name:        "full envelope",
			status:      http.StatusForbidden,
			body:        `{"error":"forbidden","code":"FORBIDDEN"}`,
			wantCode:    "FORBIDDEN",
			wantMessage: "forbidden",
			wantText:    "FORBIDDEN: forbidden (status 403)",
		},
		{
			name:        "message only",
			status:      http.StatusNotFound,
			body:        `{"error":"resource not found"}`,
			wantMessage: "resource not found",
			wantText:    "resource not found (status 404)",
		},
		{
			name:     "code only",
			status:   http.StatusConflict,
			body:     `{"code":"CONFLICT"}`,
			wantCode: "CONFLICT",
			wantText: "CONFLICT (status 409)",
		},
		{
			name:     "empty body",
			status:   http.StatusInternalServerError,
			body:     "",
			wantText: "unexpected status: 500",
		},
		{
			name:     "not json",
			status:   http.StatusBadGateway,
			body:     "<html>gateway</html>",
			wantText: "unexpected status: 502",
		},
		{
			name:     "json of another shape",
			status:   http.StatusInternalServerError,
			body:     `{"detail":"boom"}`,
			wantText: "unexpected status: 500",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := errorServer(t, tc.status, tc.body)

			_, err := client.Me(context.Background(), "tok")

			var apiErr *apiclient.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *APIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
			if apiErr.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tc.wantMessage)
			}
			if apiErr.Error() != tc.wantText {
				t.Errorf("Error() = %q, want %q", apiErr.Error(), tc.wantText)
			}
		})
	}
}
