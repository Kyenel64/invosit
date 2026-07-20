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

func TestListMembers_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"user_id", "email", "role", "joined_at", "expires_at"}).
		AddRow("usr_abc", "admin@example.com", "admin", now, nil).
		AddRow("usr_def", "member@example.com", "member", now, nil)

	mock.ExpectQuery(`SELECT m.user_id, u.email, m.role, m.joined_at, m.expires_at`).
		WithArgs("ws_123").
		WillReturnRows(rows)

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_123/members", nil)
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	members, ok := got["members"].([]any)
	if !ok || len(members) != 2 {
		t.Errorf("members = %v, want 2 items", got["members"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestAddMember_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM users WHERE LOWER\(email\) = \$1`).
		WithArgs("teammate@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("usr_new"))

	mock.ExpectExec(`INSERT INTO workspace_members`).
		WithArgs("ws_123", "usr_new", "member", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws_123/members",
		strings.NewReader(`{"email":"teammate@example.com","role":"member"}`))
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.AddMember(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["user_id"] != "usr_new" {
		t.Errorf("user_id = %v", got["user_id"])
	}
	if got["email"] != "teammate@example.com" {
		t.Errorf("email = %v", got["email"])
	}
	if got["role"] != "member" {
		t.Errorf("role = %v", got["role"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestAddMember_NonAdmin(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws_123/members",
		strings.NewReader(`{"email":"teammate@example.com","role":"member"}`))
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "member")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.AddMember(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "FORBIDDEN" {
		t.Errorf("code = %v", got["code"])
	}
}

func TestAddMember_UserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM users WHERE LOWER\(email\) = \$1`).
		WithArgs("unknown@example.com").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws_123/members",
		strings.NewReader(`{"email":"unknown@example.com","role":"member"}`))
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.AddMember(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "USER_NOT_FOUND" {
		t.Errorf("code = %v", got["code"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestAddMember_Duplicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id FROM users WHERE LOWER\(email\) = \$1`).
		WithArgs("teammate@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("usr_new"))

	mock.ExpectExec(`INSERT INTO workspace_members`).
		WithArgs("ws_123", "usr_new", "member", sqlmock.AnyArg()).
		WillReturnError(errors.New("duplicate key value violates unique constraint"))

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws_123/members",
		strings.NewReader(`{"email":"teammate@example.com","role":"member"}`))
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.AddMember(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "CONFLICT" {
		t.Errorf("code = %v", got["code"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestAddMember_InvalidRole(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws_123/members",
		strings.NewReader(`{"email":"teammate@example.com","role":"invalid"}`))
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.AddMember(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "INVALID_REQUEST" {
		t.Errorf("code = %v", got["code"])
	}
}

func TestRemoveMember_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM workspace_members`).
		WithArgs("ws_123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT role FROM workspace_members`).
		WithArgs("ws_123", "usr_def").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
	mock.ExpectExec(`DELETE FROM workspace_members`).
		WithArgs("ws_123", "usr_def").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_123/members/usr_def", nil)
	req.SetPathValue("userId", "usr_def")
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestRemoveMember_NonAdmin(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_123/members/usr_def", nil)
	req.SetPathValue("userId", "usr_def")
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "member")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "FORBIDDEN" {
		t.Errorf("code = %v", got["code"])
	}
}

func TestRemoveMember_LastAdmin(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM workspace_members`).
		WithArgs("ws_123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT role FROM workspace_members`).
		WithArgs("ws_123", "usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))
	mock.ExpectRollback()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_123/members/usr_abc", nil)
	req.SetPathValue("userId", "usr_abc")
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "LAST_ADMIN" {
		t.Errorf("code = %v", got["code"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestRemoveMember_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM workspace_members`).
		WithArgs("ws_123").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT role FROM workspace_members`).
		WithArgs("ws_123", "usr_xyz").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws_123/members/usr_xyz", nil)
	req.SetPathValue("userId", "usr_xyz")
	ctx := httpx.WithWorkspaceID(context.Background(), "ws_123")
	ctx = httpx.WithWorkspaceRole(ctx, "admin")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["code"] != "NOT_FOUND" {
		t.Errorf("code = %v", got["code"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
