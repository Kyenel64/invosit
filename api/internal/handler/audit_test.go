package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/kyenel64/invosit/api/internal/httpx"
)

func auditCtx() context.Context {
	ctx := httpx.WithUserID(context.Background(), "usr_abc")
	ctx = httpx.WithWorkspaceID(ctx, "ws_abc")
	return ctx
}

func TestListAuditLogs_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	timestamp := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	userID := "usr_abc"
	fileID := "file_xyz"
	ip := "192.168.1.1"

	mock.ExpectQuery(`SELECT id, user_id, workspace_id, action, file_id, ip, timestamp\s+FROM audit_logs\s+WHERE workspace_id = \$1\s+ORDER BY timestamp DESC LIMIT \$2`).
		WithArgs("ws_abc", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "workspace_id", "action", "file_id", "ip", "timestamp"}).
			AddRow("log_a", userID, "ws_abc", "push", fileID, ip, timestamp).
			AddRow("log_b", userID, "ws_abc", "pull", fileID, ip, timestamp))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit", nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestListAuditLogs_WithLimit(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	timestamp := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	userID := "usr_abc"
	fileID := "file_xyz"
	ip := "192.168.1.1"

	mock.ExpectQuery(`SELECT id, user_id, workspace_id, action, file_id, ip, timestamp\s+FROM audit_logs\s+WHERE workspace_id = \$1\s+ORDER BY timestamp DESC LIMIT \$2`).
		WithArgs("ws_abc", 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "workspace_id", "action", "file_id", "ip", "timestamp"}).
			AddRow("log_a", userID, "ws_abc", "push", fileID, ip, timestamp))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit?limit=10", nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestListAuditLogs_WithBefore(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	timestamp := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	beforeTime := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	userID := "usr_abc"
	fileID := "file_xyz"
	ip := "192.168.1.1"

	mock.ExpectQuery(`SELECT id, user_id, workspace_id, action, file_id, ip, timestamp\s+FROM audit_logs\s+WHERE workspace_id = \$1 AND timestamp < \$2\s+ORDER BY timestamp DESC LIMIT \$3`).
		WithArgs("ws_abc", beforeTime, 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "workspace_id", "action", "file_id", "ip", "timestamp"}).
			AddRow("log_a", userID, "ws_abc", "push", fileID, ip, timestamp))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit?before="+beforeTime.Format(time.RFC3339), nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestListAuditLogs_LimitCapped(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	timestamp := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	userID := "usr_abc"
	fileID := "file_xyz"
	ip := "192.168.1.1"

	mock.ExpectQuery(`SELECT id, user_id, workspace_id, action, file_id, ip, timestamp\s+FROM audit_logs\s+WHERE workspace_id = \$1\s+ORDER BY timestamp DESC LIMIT \$2`).
		WithArgs("ws_abc", 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "workspace_id", "action", "file_id", "ip", "timestamp"}).
			AddRow("log_a", userID, "ws_abc", "push", fileID, ip, timestamp))

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit?limit=500", nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestListAuditLogs_InvalidLimit(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit?limit=invalid", nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestListAuditLogs_InvalidBefore(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws_abc/audit?before=invalid", nil).WithContext(auditCtx())
	w := httptest.NewRecorder()

	h.ListAuditLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestRecordAudit_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO audit_logs`).
		WithArgs(sqlmock.AnyArg(), "usr_abc", "ws_abc", "push", "file_xyz", "192.168.1.1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := &Handler{db: db}
	h.recordAudit(context.Background(), "push", "ws_abc", "file_xyz", "usr_abc", "192.168.1.1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRecordAudit_NullableFileID(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO audit_logs`).
		WithArgs(sqlmock.AnyArg(), "usr_abc", "ws_abc", "delete", nil, "192.168.1.1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := &Handler{db: db}
	h.recordAudit(context.Background(), "delete", "ws_abc", "", "usr_abc", "192.168.1.1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRecordAudit_FailureLogsButDoesNotPanic(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO audit_logs`).
		WillReturnError(sqlmock.ErrCancelled)

	h := &Handler{db: db}
	h.recordAudit(context.Background(), "push", "ws_abc", "file_xyz", "usr_abc", "192.168.1.1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
