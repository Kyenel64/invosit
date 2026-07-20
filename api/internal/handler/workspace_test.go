package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

func TestCreateWorkspace_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO workspaces`).
		WithArgs(sqlmock.AnyArg(), "team-alpha", "usr_abc", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO workspace_members`).
		WithArgs(sqlmock.AnyArg(), "usr_abc", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"team-alpha"}`))
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))

	rec := httptest.NewRecorder()
	h.CreateWorkspace(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["name"] != "team-alpha" {
		t.Errorf("name = %v", got["name"])
	}
	if got["created_by"] != "usr_abc" {
		t.Errorf("created_by = %v", got["created_by"])
	}
	if id, _ := got["id"].(string); !strings.HasPrefix(id, "ws_") {
		t.Errorf("id = %v, want ws_ prefix", got["id"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateWorkspace_TrimsName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO workspaces`).
		WithArgs(sqlmock.AnyArg(), "team-alpha", "usr_abc", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO workspace_members`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"  team-alpha  "}`))
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	rec := httptest.NewRecorder()
	h.CreateWorkspace(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateWorkspace_NoUserID(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	h.CreateWorkspace(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCreateWorkspace_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		ctx  context.Context
	}{
		{"missing name", `{}`, httpx.WithUserID(context.Background(), "usr_abc")},
		{"whitespace only name", `{"name":"   "}`, httpx.WithUserID(context.Background(), "usr_abc")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			h := &Handler{db: db}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
				strings.NewReader(tt.body))
			req = req.WithContext(tt.ctx)
			rec := httptest.NewRecorder()
			h.CreateWorkspace(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestCreateWorkspace_TransactionErrors(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
	}{
		{
			"workspace insert failure",
			func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(`INSERT INTO workspaces`).
					WillReturnError(errors.New("boom"))
				mock.ExpectRollback()
			},
		},
		{
			"member insert failure",
			func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(`INSERT INTO workspaces`).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(`INSERT INTO workspace_members`).
					WillReturnError(errors.New("boom"))
				mock.ExpectRollback()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()

			tt.setupMock(mock)
			h := &Handler{db: db}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
				strings.NewReader(`{"name":"x"}`))
			req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
			rec := httptest.NewRecorder()
			h.CreateWorkspace(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rec.Code)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestListWorkspaces_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	created := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT w\.id, w\.name, w\.created_by, w\.created_at, m\.role`).
		WithArgs("usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_by", "created_at", "role"}).
			AddRow("ws_one", "alpha", "usr_abc", created, "admin").
			AddRow("ws_two", "beta", "usr_xyz", created, "member"))

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	rec := httptest.NewRecorder()
	h.ListWorkspaces(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Workspaces) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Workspaces))
	}
	if got.Workspaces[0]["id"] != "ws_one" || got.Workspaces[0]["role"] != "admin" {
		t.Errorf("row 0 = %+v", got.Workspaces[0])
	}
	if got.Workspaces[1]["id"] != "ws_two" || got.Workspaces[1]["role"] != "member" {
		t.Errorf("row 1 = %+v", got.Workspaces[1])
	}
}

func TestListWorkspaces_NoUserID(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rec := httptest.NewRecorder()
	h.ListWorkspaces(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestListWorkspaces_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT w\.id`).
		WithArgs("usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_by", "created_at", "role"}))

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req = req.WithContext(httpx.WithUserID(context.Background(), "usr_abc"))
	rec := httptest.NewRecorder()
	h.ListWorkspaces(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Workspaces == nil || len(got.Workspaces) != 0 {
		t.Errorf("workspaces = %+v, want empty slice (not null)", got.Workspaces)
	}
}

func TestGetWorkspace_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	created := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT name, created_by, created_at FROM workspaces WHERE id = \$1`).
		WithArgs("ws_abc").
		WillReturnRows(sqlmock.NewRows([]string{"name", "created_by", "created_at"}).
			AddRow("alpha", "usr_owner", created))

	h := &Handler{db: db}

	ctx := httpx.WithUserID(context.Background(), "usr_abc")
	ctx = httpx.WithWorkspaceID(ctx, "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "member")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.GetWorkspace(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != "ws_abc" || got["name"] != "alpha" || got["role"] != "member" {
		t.Errorf("got = %+v", got)
	}
}

// Middleware confirmed membership, but the workspace was deleted between
// the membership check and the row read. Treat as 403 (not 404) to keep
// existence opaque.
func TestGetWorkspace_ConcurrentDeleteReturns403(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT name, created_by, created_at FROM workspaces`).
		WithArgs("ws_abc").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db}
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.GetWorkspace(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestDeleteWorkspace_AdminSucceeds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM workspaces WHERE id = \$1`).
		WithArgs("ws_abc").
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{db: db}
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_abc", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.DeleteWorkspace(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestDeleteWorkspace_NonAdminGets403(t *testing.T) {
	for _, role := range []string{"member", "viewer", "no_access"} {
		t.Run(role, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()

			h := &Handler{db: db}
			ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
			ctx = httpx.WithWorkspaceRole(ctx, role)

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_abc", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			h.DeleteWorkspace(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestDeleteWorkspace_ConcurrentDeleteReturns403(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM workspaces`).
		WithArgs("ws_abc").
		WillReturnResult(sqlmock.NewResult(0, 0))

	h := &Handler{db: db}
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_abc", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.DeleteWorkspace(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
