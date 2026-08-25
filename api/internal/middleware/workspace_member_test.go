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

func TestWorkspaceMember_NoUserID(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	called := false
	h := WorkspaceMember(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.SetPathValue("workspaceId", "ws_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("downstream handler should not run")
	}
}

func TestWorkspaceMember_MemberOK(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT role\s+FROM workspace_members`).
		WithArgs("ws_abc", "usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

	var (
		gotWorkspaceID string
		gotRole        string
		gotUserID      string
	)
	h := WorkspaceMember(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspaceID = httpx.WorkspaceID(r.Context())
		gotRole = httpx.WorkspaceRole(r.Context())
		gotUserID = httpx.UserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	req.SetPathValue("workspaceId", "ws_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotWorkspaceID != "ws_abc" {
		t.Errorf("WorkspaceID = %q, want ws_abc", gotWorkspaceID)
	}
	if gotRole != "admin" {
		t.Errorf("WorkspaceRole = %q, want admin", gotRole)
	}
	if gotUserID != "usr_abc" {
		t.Errorf("UserID = %q, want usr_abc (must remain in context)", gotUserID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// "Not a member", "membership expired", and "role = no_access" all surface
// as sql.ErrNoRows from the query (filtered in the WHERE clause), so they
// share a single 403 path through the middleware.
func TestWorkspaceMember_NotAMemberReturnsForbidden(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT role\s+FROM workspace_members`).
		WithArgs("ws_abc", "usr_abc").
		WillReturnError(sql.ErrNoRows)

	called := false
	h := WorkspaceMember(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	req.SetPathValue("workspaceId", "ws_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Error("downstream handler should not run")
	}
}

func TestWorkspaceMember_EmptyWorkspaceIDReturnsForbidden(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	called := false
	h := WorkspaceMember(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	// Path value left unset; r.PathValue("workspaceId") returns "".
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Error("downstream handler should not run")
	}
}

func TestWorkspaceMember_DBErrorReturns500(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT role\s+FROM workspace_members`).
		WithArgs("ws_abc", "usr_abc").
		WillReturnError(errors.New("boom"))

	h := WorkspaceMember(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("downstream handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	req.SetPathValue("workspaceId", "ws_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
