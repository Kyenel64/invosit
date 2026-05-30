package blob_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/blob"
)

func TestUploadSuccess(t *testing.T) {
	want := "hello world"

	var (
		gotMethod        string
		gotAuth          string
		gotContentLength int64
		gotBody          []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentLength = r.ContentLength
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := blob.Upload(context.Background(), srv.URL+"/put?sig=1", strings.NewReader(want), int64(len(want)))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotAuth != "" {
		t.Errorf("Authorization should be empty for signed-URL upload, got %q", gotAuth)
	}
	if gotContentLength != int64(len(want)) {
		t.Errorf("Content-Length = %d, want %d", gotContentLength, len(want))
	}
	if string(gotBody) != want {
		t.Errorf("body = %q, want %q", string(gotBody), want)
	}
}

func TestUploadNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := blob.Upload(context.Background(), srv.URL, strings.NewReader("body"), 4)
	if err == nil {
		t.Fatal("want error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestDownloadSuccess(t *testing.T) {
	want := "hello world"

	var (
		gotMethod string
		gotAuth   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(want))
	}))
	defer srv.Close()

	var buf strings.Builder
	err := blob.Download(context.Background(), srv.URL+"/get?sig=1", &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotAuth != "" {
		t.Errorf("Authorization should be empty for signed-URL download, got %q", gotAuth)
	}
	if buf.String() != want {
		t.Errorf("body = %q, want %q", buf.String(), want)
	}
}

func TestDownloadNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var buf strings.Builder
	err := blob.Download(context.Background(), srv.URL, &buf)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code, got %v", err)
	}
}
