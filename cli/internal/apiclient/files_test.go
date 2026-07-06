package apiclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
)

func TestListFilesSuccess(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"files": [{
				"id": "file_abc",
				"environment_id": "env_xyz",
				"path": "config/secret.env",
				"content_hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
				"size": 12,
				"version": 5,
				"pushed_by": "usr_1",
				"pushed_at": "2026-01-02T15:04:05Z",
				"download_url": "https://blob.example/get?sig=1",
				"download_expires_at": "2026-01-02T15:19:05Z",
				"wrapped_dek": "d3JhcHBlZC1kZWs="
			}]
		}`))
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	res, err := c.ListFiles(context.Background(), "tok_xyz", "ws_1", "env_xyz")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/workspaces/ws_1/environments/env_xyz/files" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok_xyz" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if len(res) != 1 {
		t.Fatalf("files = %d, want 1", len(res))
	}
	f0 := res[0]
	if f0.ID != "file_abc" || f0.Path != "config/secret.env" || f0.Size != 12 {
		t.Errorf("file decode mismatch: %+v", f0)
	}
	if f0.Version != 5 {
		t.Errorf("version = %d, want 5", f0.Version)
	}
	if f0.ContentHash != strings.Repeat("a", 64) {
		t.Errorf("content_hash should be lowercased, got %q", f0.ContentHash)
	}
	if f0.DownloadURL != "https://blob.example/get?sig=1" {
		t.Errorf("download_url = %q", f0.DownloadURL)
	}
	if f0.DownloadExpiresAt.IsZero() || f0.PushedAt.IsZero() {
		t.Errorf("timestamps should be parsed, got %+v", f0)
	}
	if !bytes.Equal(f0.WrappedDEK, []byte("wrapped-dek")) {
		t.Errorf("wrapped_dek = %q, want base64-decoded %q", f0.WrappedDEK, "wrapped-dek")
	}
}

func TestListFilesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.ListFiles(context.Background(), "bad", "ws", "env")
	if !errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestListFilesUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := apiclient.NewClient(srv.URL)
	_, err := c.ListFiles(context.Background(), "tok", "ws", "env")
	if err == nil {
		t.Fatal("want error on 500")
	}
	if errors.Is(err, apiclient.ErrUnauthorized) {
		t.Errorf("500 should not map to a sentinel error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

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
					"content_hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					"size": 12,
					"version": 1,
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
		BaseVersion: 2,
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
	if gotBody.Files[0].BaseVersion != 2 {
		t.Errorf("base_version = %d, want 2 (sent as the push base)", gotBody.Files[0].BaseVersion)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	r0 := res[0]
	if r0.Status != "ok" || r0.File == nil || r0.File.ID != "file_abc" || r0.UploadURL != "https://blob.example/put?sig=1" {
		t.Errorf("response decode mismatch: %+v", r0)
	}
	if r0.File.Version != 1 {
		t.Errorf("file.version = %d, want 1", r0.File.Version)
	}
	if r0.File.ContentHash != strings.Repeat("a", 64) {
		t.Errorf("content_hash should be lowercased, got %q", r0.File.ContentHash)
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
					"content_hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
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
	if res[0].File == nil || res[0].File.ContentHash != strings.Repeat("a", 64) {
		t.Errorf("content_hash should be lowercased, got %+v", res[0].File)
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
