package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

func registerPublicKeyReq(t *testing.T, uid string, body any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/public-key", bytes.NewReader(payload))
	if uid != "" {
		req = req.WithContext(httpx.WithUserID(context.Background(), uid))
	}
	return req
}

func testPublicKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return key, base64.StdEncoding.EncodeToString(key)
}

func TestRegisterPublicKey_SetReturns204(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	key, encoded := testPublicKey(t)
	mock.ExpectExec(`UPDATE users SET public_key = \$2 WHERE id = \$1 AND \(public_key IS NULL OR public_key = \$2\)`).
		WithArgs("usr_abc", encoded).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{db: db}
	rec := httptest.NewRecorder()
	h.RegisterPublicKey(rec, registerPublicKeyReq(t, "usr_abc", map[string]any{"public_key": key}))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestRegisterPublicKey_DifferentKeyReturns409(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	key, encoded := testPublicKey(t)
	mock.ExpectExec(`UPDATE users SET public_key = \$2 WHERE id = \$1 AND \(public_key IS NULL OR public_key = \$2\)`).
		WithArgs("usr_abc", encoded).
		WillReturnResult(sqlmock.NewResult(0, 0))

	h := &Handler{db: db}
	rec := httptest.NewRecorder()
	h.RegisterPublicKey(rec, registerPublicKeyReq(t, "usr_abc", map[string]any{"public_key": key}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["code"] != "CONFLICT" {
		t.Errorf("code = %v, want CONFLICT", got["code"])
	}
}

func TestRegisterPublicKey_ValidationErrors(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		body           any
		expectedStatus int
	}{
		{"wrong length", "usr_abc", map[string]any{"public_key": []byte("short")}, http.StatusBadRequest},
		{"invalid base64", "usr_abc", map[string]any{"public_key": "not-base64!!"}, http.StatusBadRequest},
		{"no user id", "", map[string]any{"public_key": make([]byte, 32)}, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()

			h := &Handler{db: db}
			rec := httptest.NewRecorder()
			h.RegisterPublicKey(rec, registerPublicKeyReq(t, tt.userID, tt.body))

			if rec.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d, body = %s", rec.Code, tt.expectedStatus, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("no SQL expected: %v", err)
			}
		})
	}
}
