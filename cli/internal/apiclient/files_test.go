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

func TestCreateFilesSuccess(t *testing.T) {
	var (
		gotMethod, gotPath, gotAuth, gotContentType string
		gotBody                                     struct {
			Files []apiclient.CreateFileEntry `json:"files"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [{
				"path": "secret.env",
				"status": "ok",
				"file": {
					"id": "file_abc",
					"environment_id": "env_xyz",
					"path": "secret.env",
					"content_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"size": 12,
					"pushed_by": "usr_1",
					"pushed_at": "2026-01-02T15:04:05Z"
				},
				"upload_url": "https://blob.example/put?sig=1",
				"upload_expires_at": "2026-01-02T15:19:05Z"
			}]
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	res, err := c.CreateFiles(context.Background(), "tok_xyz", "ws_1", "env_xyz", []apiclient.CreateFileEntry{{
		Path:        "secret.env",
		ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        12,
	}})
	if err != nil {
		t.Fatalf("CreateFiles: %v", err)
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
	if len(gotBody.Files) != 1 || gotBody.Files[0].Path != "secret.env" || gotBody.Files[0].Size != 12 {
		t.Errorf("request body roundtrip mismatch: %+v", gotBody)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	r0 := res[0]
	if r0.Status != "ok" || r0.File == nil || r0.File.ID != "file_abc" || r0.UploadURL != "https://blob.example/put?sig=1" {
		t.Errorf("response decode mismatch: %+v", r0)
	}
	if r0.UploadExpiresAt == nil || r0.UploadExpiresAt.IsZero() || r0.File.PushedAt.IsZero() {
		t.Errorf("timestamps should be parsed, got %+v", r0)
	}
}

func TestCreateFilesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateFiles(context.Background(), "bad", "ws", "env", nil)
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestCreateFilesUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CreateFiles(context.Background(), "tok", "ws", "env", nil)
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

func TestCompleteFilesSuccess(t *testing.T) {
	var (
		gotMethod, gotPath string
		gotBody            struct {
			FileIDs []string `json:"file_ids"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [{
				"id": "file_abc",
				"status": "ok",
				"file": {
					"id": "file_abc",
					"environment_id": "env_xyz",
					"path": "secret.env",
					"content_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"size": 12,
					"pushed_by": "usr_1",
					"pushed_at": "2026-01-02T15:04:05Z"
				}
			}]
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	res, err := c.CompleteFiles(context.Background(), "tok", "ws_1", "env_xyz", []string{"file_abc"})
	if err != nil {
		t.Fatalf("CompleteFiles: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/workspaces/ws_1/environments/env_xyz/files:complete" {
		t.Errorf("path = %q", gotPath)
	}
	if len(gotBody.FileIDs) != 1 || gotBody.FileIDs[0] != "file_abc" {
		t.Errorf("request body mismatch: %+v", gotBody)
	}
	if len(res) != 1 || res[0].Status != "ok" || res[0].ID != "file_abc" {
		t.Errorf("response mismatch: %+v", res)
	}
}

func TestCompleteFilesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.CompleteFiles(context.Background(), "bad", "ws", "env", nil)
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}
