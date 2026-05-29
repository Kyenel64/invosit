package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

func TestEnvironmentScoped_OK(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM environments WHERE id = \$1 AND workspace_id = \$2`).
		WithArgs("env_abc", "ws_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("env_abc"))

	var gotEnvID string
	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnvID = httpx.EnvironmentID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	req.SetPathValue("environmentId", "env_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotEnvID != "env_abc" {
		t.Errorf("EnvironmentID = %q", gotEnvID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A bare name (no env_ prefix) resolves by case-insensitive name lookup, and
// the resolved env_ id is what reaches the handler context.
func TestEnvironmentScoped_NameLookup_OK(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM environments WHERE LOWER\(name\) = LOWER\(\$1\) AND workspace_id = \$2`).
		WithArgs("production", "ws_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("env_abc"))

	var gotEnvID string
	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnvID = httpx.EnvironmentID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	req.SetPathValue("environmentId", "production")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotEnvID != "env_abc" {
		t.Errorf("EnvironmentID = %q, want resolved env_abc", gotEnvID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestEnvironmentScoped_NameLookup_NotFound(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM environments WHERE LOWER\(name\)`).
		WithArgs("nope", "ws_abc").
		WillReturnError(sql.ErrNoRows)

	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	req.SetPathValue("environmentId", "nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestEnvironmentScoped_NoWorkspaceIDInContext(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	called := false
	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.SetPathValue("environmentId", "env_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Error("handler should not run")
	}
}

func TestEnvironmentScoped_EmptyEnvIDReturnsForbidden(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// An env from a different workspace, or a nonexistent id, both surface as
// sql.ErrNoRows from the WHERE filter — and share a single 403 path.
func TestEnvironmentScoped_WrongWorkspaceReturnsForbidden(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM environments`).
		WithArgs("env_other", "ws_abc").
		WillReturnError(sql.ErrNoRows)

	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	req.SetPathValue("environmentId", "env_other")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestEnvironmentScoped_DBErrorReturns500(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM environments`).
		WithArgs("env_abc", "ws_abc").
		WillReturnError(errors.New("boom"))

	h := EnvironmentScoped(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(
		httpx.WithWorkspaceID(context.Background(), "ws_abc"),
	)
	req.SetPathValue("environmentId", "env_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
