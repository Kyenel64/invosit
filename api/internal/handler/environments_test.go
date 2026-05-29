package handler

import (
	"context"
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

func adminCtx() context.Context {
	ctx := httpx.WithUserID(context.Background(), "usr_abc")
	ctx = httpx.WithWorkspaceID(ctx, "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	return ctx
}

func TestCreateEnvironment_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO environments`).
		WithArgs(sqlmock.AnyArg(), "ws_abc", "staging", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"name":"staging"}`)).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	h.CreateEnvironment(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["name"] != "staging" || got["workspace_id"] != "ws_abc" {
		t.Errorf("got = %+v", got)
	}
	if id, _ := got["id"].(string); !strings.HasPrefix(id, "env_") {
		t.Errorf("id = %v, want env_ prefix", got["id"])
	}
}

func TestCreateEnvironment_NonAdminGets403(t *testing.T) {
	for _, role := range []string{"member", "viewer"} {
		t.Run(role, func(t *testing.T) {
			db, _, _ := sqlmock.New()
			defer db.Close()

			h := &Handler{db: db}
			ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
			ctx = httpx.WithWorkspaceRole(ctx, role)
			req := httptest.NewRequest(http.MethodPost, "/x",
				strings.NewReader(`{"name":"staging"}`)).WithContext(ctx)
			rec := httptest.NewRecorder()
			h.CreateEnvironment(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestCreateEnvironment_MissingName(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{}`)).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	h.CreateEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateEnvironment_RejectsEnvPrefixedName(t *testing.T) {
	for _, name := range []string{"env_foo", "Env_Bar", "  env_baz  "} {
		t.Run(name, func(t *testing.T) {
			db, _, _ := sqlmock.New()
			defer db.Close()

			h := &Handler{db: db}
			req := httptest.NewRequest(http.MethodPost, "/x",
				strings.NewReader(`{"name":"`+name+`"}`)).WithContext(adminCtx())
			rec := httptest.NewRecorder()
			h.CreateEnvironment(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestCreateEnvironment_DuplicateReturns409(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO environments`).
		WillReturnError(errors.New(`pq: duplicate key value violates unique constraint "environments_workspace_name_lower" (SQLSTATE 23505)`))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"name":"dev"}`)).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	h.CreateEnvironment(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestListEnvironments_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	created := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, name, created_at FROM environments`).
		WithArgs("ws_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_at"}).
			AddRow("env_one", "dev", created).
			AddRow("env_two", "prod", created))

	h := &Handler{db: db}
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "member")
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListEnvironments(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Environments) != 2 || got.Environments[0]["name"] != "dev" {
		t.Errorf("got = %+v", got.Environments)
	}
}

func TestListEnvironments_Empty(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id, name, created_at FROM environments`).
		WithArgs("ws_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_at"}))

	h := &Handler{db: db}
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_abc")
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListEnvironments(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Environments == nil || len(got.Environments) != 0 {
		t.Errorf("environments = %+v, want empty slice (not null)", got.Environments)
	}
}
