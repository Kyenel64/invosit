package apiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
)

func TestMeSuccess(t *testing.T) {
	var gotAuth, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"usr_abc","email":"test@example.com","created_at":"2026-01-02T15:04:05Z"}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	user, err := c.Me(context.Background(), "tok_xyz")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}

	if gotPath != "/api/v1/auth/me" {
		t.Errorf("path = %q, want /api/v1/auth/me", gotPath)
	}
	if gotAuth != "Bearer tok_xyz" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok_xyz")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if user.ID != "usr_abc" {
		t.Errorf("ID = %q, want usr_abc", user.ID)
	}
	if user.Email != "test@example.com" {
		t.Errorf("Email = %q, want test@example.com", user.Email)
	}
	if user.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be parsed, got zero")
	}
}

func TestMeUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.Me(context.Background(), "bad_token")
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestMeUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.Me(context.Background(), "tok")
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("500 should not map to ErrUnauthorized")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestMeInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.Me(context.Background(), "tok")
	if err == nil {
		t.Fatal("want decode error, got nil")
	}
}

func TestMeTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.Me(context.Background(), "tok")
	if err == nil {
		t.Fatal("want transport error, got nil")
	}
}

func TestMeContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := apiclient.NewClient(srv.URL)
	_, err := c.Me(ctx, "tok")
	if err == nil {
		t.Fatal("want context error, got nil")
	}
}

func TestPushFileSuccess(t *testing.T) {
	var (
		gotMethod, gotPath, gotAuth, gotContentType string
		gotBody                                     apiclient.PushFileRequest
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
			"id":"file_abc",
			"environment_id":"env_xyz",
			"path":"secret.env",
			"content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"size":12,
			"pushed_by":"usr_1",
			"pushed_at":"2026-01-02T15:04:05Z",
			"upload_url":"https://blob.example/put?sig=1",
			"upload_expires_at":"2026-01-02T15:19:05Z"
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	res, err := c.PushFile(context.Background(), "tok_xyz", "ws_1", "env_xyz", apiclient.PushFileRequest{
		Path:        "secret.env",
		ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        12,
	})
	if err != nil {
		t.Fatalf("PushFile: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/workspaces/ws_1/environments/env_xyz/files" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok_xyz" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotBody.Path != "secret.env" || gotBody.Size != 12 {
		t.Errorf("request body roundtrip mismatch: %+v", gotBody)
	}
	if res.ID != "file_abc" || res.UploadURL != "https://blob.example/put?sig=1" {
		t.Errorf("response decode mismatch: %+v", res)
	}
	if res.PushedAt.IsZero() || res.UploadExpiresAt.IsZero() {
		t.Errorf("timestamps should be parsed, got %+v", res)
	}
}

func TestPushFileUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.PushFile(context.Background(), "bad", "ws", "env", apiclient.PushFileRequest{})
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestPushFileUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.PushFile(context.Background(), "tok", "ws", "env", apiclient.PushFileRequest{})
	if err == nil {
		t.Fatal("want error on 500")
	}
	if errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("500 should not map to ErrUnauthorized")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}
