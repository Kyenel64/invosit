package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/httpx"
	"github.com/kyenel64/invosit/api/internal/kratos"
)

const fakeIdentityID = "00000000-0000-0000-0000-000000000001"

func newWhoamiServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
}

func TestRequireKratosSession_MissingAuth(t *testing.T) {
	c := kratos.NewClient("http://unused")
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	called := false
	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("downstream handler should not be called")
	}
}

func TestRequireKratosSession_OK(t *testing.T) {
	srv := newWhoamiServer(t, http.StatusOK, `{
		"id":"sess","active":true,
		"identity":{"id":"`+fakeIdentityID+`","schema_id":"default","schema_url":"http://x","traits":{"email":"e@x"}}
	}`)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM users WHERE kratos_identity_id = \$1`).
		WithArgs(fakeIdentityID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("usr_abc"))

	gotUserID := ""
	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = httpx.UserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if gotUserID != "usr_abc" {
		t.Errorf("UserID = %q, want usr_abc", gotUserID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestRequireKratosSession_NoLocalUser(t *testing.T) {
	srv := newWhoamiServer(t, http.StatusOK, `{
		"id":"sess","active":true,
		"identity":{"id":"`+fakeIdentityID+`","schema_id":"default","schema_url":"http://x","traits":{"email":"e@x"}}
	}`)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM users WHERE kratos_identity_id = \$1`).
		WithArgs(fakeIdentityID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireKratosSession_KratosUnauthorized(t *testing.T) {
	srv := newWhoamiServer(t, http.StatusUnauthorized, "")
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireKratosSession_InactiveSession(t *testing.T) {
	srv := newWhoamiServer(t, http.StatusOK, `{
		"id":"s","active":false,
		"identity":{"id":"`+fakeIdentityID+`","schema_id":"default","schema_url":"http://x","traits":{"email":"e@x"}}
	}`)
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireKratosSession_KratosError(t *testing.T) {
	srv := newWhoamiServer(t, http.StatusInternalServerError, "")
	defer srv.Close()

	c := kratos.NewClient(srv.URL)
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := RequireKratosSession(c, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
