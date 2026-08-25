package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	hookSecret     = "shhh-very-secret"
	hookIdentityID = "00000000-0000-0000-0000-000000000001"
	hookEmail      = "alice@example.com"
	hookCreatedAt  = "2026-05-07T12:00:00Z"
	validHookBody  = `{"identity_id":"` + hookIdentityID + `","email":"` + hookEmail + `","created_at":"` + hookCreatedAt + `"}`
)

func TestAfterRegistration_AuthErrors(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"missing secret", ""},
		{"wrong secret", "wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()

			h := &Handler{db: db, webhookKey: hookSecret}
			req := httptest.NewRequest(http.MethodPost, "/internal/hooks/kratos/after-registration",
				bytes.NewReader([]byte(validHookBody)))
			if tt.secret != "" {
				req.Header.Set("X-Kratos-Webhook-Secret", tt.secret)
			}
			rec := httptest.NewRecorder()
			h.AfterRegistration(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestAfterRegistration_InsertsUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`INSERT INTO users`).
		WithArgs(sqlmock.AnyArg(), hookEmail, hookIdentityID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{db: db, webhookKey: hookSecret}
	req := httptest.NewRequest(http.MethodPost, "/internal/hooks/kratos/after-registration",
		bytes.NewReader([]byte(validHookBody)))
	req.Header.Set("X-Kratos-Webhook-Secret", hookSecret)
	rec := httptest.NewRecorder()
	h.AfterRegistration(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestAfterRegistration_DuplicateIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`INSERT INTO users`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	h := &Handler{db: db, webhookKey: hookSecret}
	req := httptest.NewRequest(http.MethodPost, "/internal/hooks/kratos/after-registration",
		bytes.NewReader([]byte(validHookBody)))
	req.Header.Set("X-Kratos-Webhook-Secret", hookSecret)
	rec := httptest.NewRecorder()
	h.AfterRegistration(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAfterRegistration_InvalidPayload(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := &Handler{db: db, webhookKey: hookSecret}
	req := httptest.NewRequest(http.MethodPost, "/internal/hooks/kratos/after-registration",
		bytes.NewReader([]byte(`{"identity_id":"not-a-uuid","email":"bad"}`)))
	req.Header.Set("X-Kratos-Webhook-Secret", hookSecret)
	rec := httptest.NewRecorder()
	h.AfterRegistration(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
