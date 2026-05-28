package kratos_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/kratos"
)

func TestLogoutSuccess(t *testing.T) {
	var gotToken string
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /self-service/logout/api", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SessionToken string `json:"session_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			t.Errorf("decode body: %v", err)
		}
		gotToken = body.SessionToken
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	if err := c.Logout(context.Background(), "ory_st_abc"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if gotToken != "ory_st_abc" {
		t.Errorf("session_token = %q, want ory_st_abc", gotToken)
	}
}

func TestLogoutForbiddenIsNoError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /self-service/logout/api", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	if err := c.Logout(context.Background(), "stale"); err != nil {
		t.Errorf("Logout on 403 = %v, want nil (already logged out)", err)
	}
}

func TestLogoutServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /self-service/logout/api", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	if err := c.Logout(context.Background(), "tok"); err == nil {
		t.Fatal("want error on 500, got nil")
	}
}
