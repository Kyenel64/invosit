package apiclient_test

import (
	"context"
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
