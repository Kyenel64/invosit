package apiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
)

func TestCreateWorkspaceSuccess(t *testing.T) {
	var gotAuth, gotMethod, gotPath, gotContentType string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ws_abc","name":"My WS","created_at":"2026-01-02T15:04:05Z"}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	workspace, err := c.CreateWorkspace(context.Background(), "tok_xyz", "My WS")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/workspaces" {
		t.Errorf("path = %q, want /api/v1/workspaces", gotPath)
	}
	if gotAuth != "Bearer tok_xyz" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok_xyz")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["name"] != "My WS" {
		t.Errorf("body name = %q, want %q", gotBody["name"], "My WS")
	}
	if workspace.ID != "ws_abc" {
		t.Errorf("ID = %q, want ws_abc", workspace.ID)
	}
	if workspace.Name != "My WS" {
		t.Errorf("Name = %q, want My WS", workspace.Name)
	}
}

func TestCreateWorkspaceUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateWorkspace(context.Background(), "bad_token", "My WS")
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestCreateWorkspaceUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateWorkspace(context.Background(), "tok", "My WS")
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestCreateWorkspaceInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateWorkspace(context.Background(), "tok", "My WS")
	if err == nil {
		t.Fatal("want decode error, got nil")
	}
}

func TestCreateWorkspaceTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateWorkspace(context.Background(), "tok", "My WS")
	if err == nil {
		t.Fatal("want transport error, got nil")
	}
}
