package apiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
)

func TestCreateEnvironmentSuccess(t *testing.T) {
	var (
		gotMethod, gotPath, gotAuth, gotContentType string
		gotBody                                     struct {
			Name string `json:"name"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "env_abc",
			"workspace_id": "ws_1",
			"name": "staging",
			"created_at": "2026-01-02T15:04:05Z"
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	env, err := c.CreateEnvironment(context.Background(), "tok_xyz", "ws_1", "staging")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/workspaces/ws_1/environments" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok_xyz" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotBody.Name != "staging" {
		t.Errorf("request body name = %q, want staging", gotBody.Name)
	}
	if env.ID != "env_abc" || env.Name != "staging" || env.WorkspaceID != "ws_1" {
		t.Errorf("response decode mismatch: %+v", env)
	}
	if env.CreatedAt.IsZero() {
		t.Errorf("created_at should be parsed, got %+v", env)
	}
}

func TestCreateEnvironmentStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, apiclient.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, apiclient.ErrForbidden},
		{"conflict", http.StatusConflict, apiclient.ErrConflict},
		{"invalid", http.StatusBadRequest, apiclient.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := apiclient.NewClient(srv.URL)
			_, err := c.CreateEnvironment(context.Background(), "tok", "ws_1", "staging")
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d: want %v, got %v", tc.status, tc.want, err)
			}
		})
	}
}

func TestCreateEnvironmentUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateEnvironment(context.Background(), "tok", "ws_1", "staging")
	if err == nil {
		t.Fatal("want error on 500")
	}
	if errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("500 should not map to ErrUnauthorized")
	}
}

func TestGetEnvironmentsSuccess(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"environments": [
				{"id": "env_1", "name": "development", "created_at": "2026-01-02T15:04:05Z"},
				{"id": "env_2", "name": "production", "created_at": "2026-01-03T15:04:05Z"}
			]
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	envs, err := c.GetEnvironments(context.Background(), "tok", "ws_1")
	if err != nil {
		t.Fatalf("GetEnvironments: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/workspaces/ws_1/environments" {
		t.Errorf("path = %q", gotPath)
	}
	if len(envs) != 2 || envs[0].ID != "env_1" || envs[1].Name != "production" {
		t.Errorf("response mismatch: %+v", envs)
	}
}

func TestGetEnvironmentsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.GetEnvironments(context.Background(), "bad", "ws_1")
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}
