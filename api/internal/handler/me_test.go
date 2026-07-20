package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

func TestMe_Returns200WithUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	created := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT email, created_at FROM users WHERE id = \$1`).
		WithArgs("usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"email", "created_at"}).
			AddRow("alice@example.com", created))

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	ctx := httpx.WithUserID(context.Background(), "usr_abc")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Me(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != "usr_abc" {
		t.Errorf("id = %v", got["id"])
	}
	if got["email"] != "alice@example.com" {
		t.Errorf("email = %v", got["email"])
	}
}

func TestMe_NoUserIDReturns401(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := &Handler{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
