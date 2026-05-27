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

// stubStorage records calls and returns canned values so handler tests can
// assert behaviour without spinning up R2/S3.
type stubStorage struct {
	putURL, getURL string
	putErr         error
	getErr         error
	deleteErr      error
	deletedKey     string
	deleteCalls    int
	putCalls       int
	getCalls       int
	// Optional per-call hooks for tests that need different behaviour on
	// successive calls (e.g. fail the 2nd presign). Receive the 1-indexed
	// call number; a non-nil return overrides putErr / getErr.
	putErrFunc func(call int) error
	getErrFunc func(call int) error
}

func (s *stubStorage) SignedPutURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	s.putCalls++
	if s.putErrFunc != nil {
		if err := s.putErrFunc(s.putCalls); err != nil {
			return "", err
		}
	}
	return s.putURL, s.putErr
}
func (s *stubStorage) SignedGetURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	s.getCalls++
	if s.getErrFunc != nil {
		if err := s.getErrFunc(s.getCalls); err != nil {
			return "", err
		}
	}
	return s.getURL, s.getErr
}
func (s *stubStorage) Delete(_ context.Context, key string) error {
	s.deleteCalls++
	s.deletedKey = key
	return s.deleteErr
}

const validHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func pushCtx() context.Context {
	ctx := httpx.WithUserID(context.Background(), "usr_abc")
	ctx = httpx.WithWorkspaceID(ctx, "ws_abc")
	ctx = httpx.WithEnvironmentID(ctx, "env_abc")
	ctx = httpx.WithWorkspaceRole(ctx, "member")
	return ctx
}

// ── ListFiles ───────────────────────────────

func TestListFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE environment_id = \$1 AND status = 'committed'`).
		WithArgs("env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "path", "content_hash", "size", "pushed_by", "pushed_at"}).
			AddRow("file_a", "a.env", validHash, int64(10), "usr_abc", pushedAt).
			AddRow("file_b", "b.env", validHash, int64(20), "usr_xyz", pushedAt))

	stub := &stubStorage{getURL: "https://signed/get"}
	h := &Handler{db: db, blobs: stub}
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.ListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("len = %d", len(got.Files))
	}
	for i, f := range got.Files {
		if f["download_url"] != "https://signed/get" {
			t.Errorf("files[%d] download_url = %v", i, f["download_url"])
		}
		if f["status"] != "committed" {
			t.Errorf("files[%d] status = %v", i, f["status"])
		}
	}
	if stub.getCalls != 2 {
		t.Errorf("getCalls = %d, want 2 (one per row)", stub.getCalls)
	}
}

func TestListFiles_Empty(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id, path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE environment_id = \$1 AND status = 'committed'`).
		WithArgs("env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "path", "content_hash", "size", "pushed_by", "pushed_at"}))

	stub := &stubStorage{}
	h := &Handler{db: db, blobs: stub}
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.ListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Files == nil || len(got.Files) != 0 {
		t.Errorf("files = %+v, want empty slice (not null)", got.Files)
	}
	if stub.getCalls != 0 {
		t.Errorf("getCalls = %d, want 0 for empty list", stub.getCalls)
	}
}

// ── GetFile ─────────────────────────────────

func TestGetFile_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE id = \$1 AND environment_id = \$2 AND status = 'committed'`).
		WithArgs("file_xyz", "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "pushed_by", "pushed_at"}).
			AddRow("a.env", validHash, int64(123), "usr_abc", pushedAt))

	stub := &stubStorage{getURL: "https://signed/get"}
	h := &Handler{db: db, blobs: stub}

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_xyz")
	rec := httptest.NewRecorder()
	h.GetFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["path"] != "a.env" {
		t.Errorf("path = %v", got["path"])
	}
	if got["download_url"] != "https://signed/get" {
		t.Errorf("download_url = %v", got["download_url"])
	}
	if got["status"] != "committed" {
		t.Errorf("status = %v, want committed", got["status"])
	}
	if stub.getCalls != 1 {
		t.Errorf("getCalls = %d, want 1", stub.getCalls)
	}
}

// A pending row is filtered out by the WHERE status='committed' clause —
// looks like 403/not-found to the caller, so we don't leak that an
// in-progress upload exists.
func TestGetFile_PendingHiddenReturns403(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT path, content_hash`).
		WithArgs("file_pending", "env_abc").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db, blobs: &stubStorage{}}
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_pending")
	rec := httptest.NewRecorder()
	h.GetFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestGetFile_NotFoundReturns403(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT path, content_hash`).
		WithArgs("file_missing", "env_abc").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db, blobs: &stubStorage{}}
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_missing")
	rec := httptest.NewRecorder()
	h.GetFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// ── DeleteFile ──────────────────────────────

func TestDeleteFile_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`DELETE FROM files WHERE id = \$1 AND environment_id = \$2`).
		WithArgs("file_xyz", "env_abc").
		WillReturnResult(sqlmock.NewResult(0, 1))

	stub := &stubStorage{}
	h := &Handler{db: db, blobs: stub}
	req := httptest.NewRequest(http.MethodDelete, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_xyz")
	rec := httptest.NewRecorder()
	h.DeleteFile(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (body = %s)", rec.Code, rec.Body.String())
	}
	// Blob deletion is deferred to the sweep (issue #62) — content-addressed
	// blobs can be shared across rows, so DeleteFile must not delete inline.
	if stub.deleteCalls != 0 {
		t.Errorf("storage.Delete calls = %d, want 0 (blob cleanup is the sweep's job)", stub.deleteCalls)
	}
}

func TestDeleteFile_ViewerForbidden(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db, blobs: &stubStorage{}}
	ctx := pushCtx()
	ctx = httpx.WithWorkspaceRole(ctx, "viewer")

	req := httptest.NewRequest(http.MethodDelete, "/x", nil).WithContext(ctx)
	req.SetPathValue("fileId", "file_xyz")
	rec := httptest.NewRecorder()
	h.DeleteFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestDeleteFile_MissingReturns403(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`DELETE FROM files WHERE id = \$1 AND environment_id = \$2`).
		WithArgs("file_missing", "env_abc").
		WillReturnResult(sqlmock.NewResult(0, 0))

	h := &Handler{db: db, blobs: &stubStorage{}}
	req := httptest.NewRequest(http.MethodDelete, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_missing")
	rec := httptest.NewRecorder()
	h.DeleteFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// ── CreateFiles (Phase 1: pending row + signed upload URL) ──

func expectPendingUpsert(mock sqlmock.Sqlmock, returnedID string) {
	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(returnedID))
}

func TestCreateFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectPendingUpsert(mock, "file_a")
	expectPendingUpsert(mock, "file_b")

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}

	body := `{"files":[
		{"path":"a.env","content_hash":"` + validHash + `","size":1},
		{"path":"b.env","content_hash":"` + validHash + `","size":2}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Results))
	}
	for i, r := range got.Results {
		if r["status"] != "ok" {
			t.Errorf("results[%d] status = %v", i, r["status"])
		}
		if r["upload_url"] != "https://signed/put" {
			t.Errorf("results[%d] upload_url = %v", i, r["upload_url"])
		}
		file, ok := r["file"].(map[string]any)
		if !ok {
			t.Errorf("results[%d] file missing", i)
			continue
		}
		if file["status"] != "pending" {
			t.Errorf("results[%d] file status = %v, want pending", i, file["status"])
		}
	}
	if stub.putCalls != 2 {
		t.Errorf("putCalls = %d, want 2", stub.putCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCreateFiles_MixedSuccess(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	// Bad path short-circuits before any DB call. Two valid entries reach
	// the DB.
	expectPendingUpsert(mock, "file_a")
	expectPendingUpsert(mock, "file_c")

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}

	body := `{"files":[
		{"path":"a.env","content_hash":"` + validHash + `","size":1},
		{"path":"../escape","content_hash":"` + validHash + `","size":2},
		{"path":"c.env","content_hash":"` + validHash + `","size":3}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "ok" || got.Results[2]["status"] != "ok" {
		t.Errorf("entries 0 and 2 should succeed, got %+v", got.Results)
	}
	if got.Results[1]["status"] != "error" || got.Results[1]["code"] != "INVALID_REQUEST" {
		t.Errorf("entry 1 = %+v, want error/INVALID_REQUEST", got.Results[1])
	}
	if stub.putCalls != 2 {
		t.Errorf("putCalls = %d, want 2 (bad entry skips presign)", stub.putCalls)
	}
}

func TestCreateFiles_OversizeBatch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	entries := make([]string, 0, 101)
	for range 101 {
		entries = append(entries, `{"path":"f","content_hash":"`+validHash+`","size":1}`)
	}
	body := `{"files":[` + strings.Join(entries, ",") + `]}`

	h := &Handler{db: db, blobs: &stubStorage{}}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body = %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["code"] != "BATCH_TOO_LARGE" {
		t.Errorf("code = %v, want BATCH_TOO_LARGE", got["code"])
	}
}

func TestCreateFiles_ViewerForbidden(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	ctx := pushCtx()
	ctx = httpx.WithWorkspaceRole(ctx, "viewer")

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestCreateFiles_EmptyBatch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"files":[]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body = %s)", rec.Code, rec.Body.String())
	}
}

func TestCreateFiles_NoUserID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCreateFiles_RejectsBadPaths(t *testing.T) {
	bad := []string{
		"/abs/path",
		"\\windows\\abs",
		"../escape",
		"a/../b",
		"a\\..\\b",
		"with\x00null",
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			db, _, _ := sqlmock.New()
			defer db.Close()
			h := &Handler{db: db, blobs: &stubStorage{}}

			body, _ := json.Marshal(map[string]any{
				"files": []map[string]any{{"path": p, "content_hash": validHash, "size": 1}},
			})
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(body))).WithContext(pushCtx())
			rec := httptest.NewRecorder()
			h.CreateFiles(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
			}
			var got struct {
				Results []map[string]any `json:"results"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INVALID_REQUEST" {
				t.Errorf("result = %+v, want error/INVALID_REQUEST", got.Results[0])
			}
		})
	}
}

func TestCreateFiles_RejectsBadHashes(t *testing.T) {
	bad := []string{
		strings.Repeat("z", 64),                                 // not hex
		strings.Repeat("A", 64),                                 // uppercase — blob key would diverge
		"ABCDEF" + strings.Repeat("a", 58),                      // mixed case at start
		strings.Repeat("a", 30) + "F" + strings.Repeat("a", 33), // single uppercase in middle
	}
	for _, hash := range bad {
		t.Run(hash, func(t *testing.T) {
			db, _, _ := sqlmock.New()
			defer db.Close()
			h := &Handler{db: db, blobs: &stubStorage{}}

			body, _ := json.Marshal(map[string]any{
				"files": []map[string]any{{"path": "a.txt", "content_hash": hash, "size": 1}},
			})
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(body))).WithContext(pushCtx())
			rec := httptest.NewRecorder()
			h.CreateFiles(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
			}
			var got struct {
				Results []map[string]any `json:"results"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INVALID_REQUEST" {
				t.Errorf("result = %+v, want error/INVALID_REQUEST", got.Results[0])
			}
		})
	}
}

func TestCreateFiles_UpsertFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnError(errors.New("boom"))

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INTERNAL_ERROR" {
		t.Errorf("result = %+v, want error/INTERNAL_ERROR", got.Results[0])
	}
}

func TestCreateFiles_PresignFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectPendingUpsert(mock, "file_xyz")

	h := &Handler{db: db, blobs: &stubStorage{putErr: errors.New("storage down")}}
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CreateFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INTERNAL_ERROR" {
		t.Errorf("result = %+v, want error/INTERNAL_ERROR", got.Results[0])
	}
}

// ── CompleteFiles (Phase 2: transition pending → committed) ──

func expectCompleteUpdate(mock sqlmock.Sqlmock, fileID, path, hash string) {
	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`UPDATE files\s+SET status = 'committed'\s+WHERE id = \$1 AND environment_id = \$2`).
		WithArgs(fileID, "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "pushed_by", "pushed_at", "status"}).
			AddRow(path, hash, int64(100), "usr_abc", pushedAt, "committed"))
}

func TestCompleteFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteUpdate(mock, "file_a", "a.env", validHash)
	expectCompleteUpdate(mock, "file_b", "b.env", validHash)

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_a","file_b"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Results) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Results))
	}
	for i, r := range got.Results {
		if r["status"] != "ok" {
			t.Errorf("results[%d] status = %v", i, r["status"])
		}
		file, ok := r["file"].(map[string]any)
		if !ok {
			t.Errorf("results[%d] file missing", i)
			continue
		}
		if file["status"] != "committed" {
			t.Errorf("results[%d] file status = %v, want committed", i, file["status"])
		}
	}
}

// Completing an already-committed file is a no-op success — the UPDATE
// still matches and returns the row (idempotency).
func TestCompleteFiles_Idempotent(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteUpdate(mock, "file_xyz", "a.env", validHash)

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_xyz"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "ok" {
		t.Errorf("status = %v, want ok", got.Results[0]["status"])
	}
}

func TestCompleteFiles_NotFoundReturnsForbidden(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`UPDATE files`).
		WithArgs("file_missing", "env_abc").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_missing"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "FORBIDDEN" {
		t.Errorf("result = %+v, want error/FORBIDDEN", got.Results[0])
	}
}

func TestCompleteFiles_MixedSuccess(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteUpdate(mock, "file_a", "a.env", validHash)
	mock.ExpectQuery(`UPDATE files`).
		WithArgs("file_missing", "env_abc").
		WillReturnError(sql.ErrNoRows)

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_a","file_missing"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "ok" {
		t.Errorf("results[0] status = %v, want ok", got.Results[0]["status"])
	}
	if got.Results[1]["status"] != "error" || got.Results[1]["code"] != "FORBIDDEN" {
		t.Errorf("results[1] = %+v, want error/FORBIDDEN", got.Results[1])
	}
}

func TestCompleteFiles_OversizeBatch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	ids := make([]string, 0, 101)
	for range 101 {
		ids = append(ids, `"file_x"`)
	}
	body := `{"file_ids":[` + strings.Join(ids, ",") + `]}`

	h := &Handler{db: db, blobs: &stubStorage{}}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body = %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["code"] != "BATCH_TOO_LARGE" {
		t.Errorf("code = %v, want BATCH_TOO_LARGE", got["code"])
	}
}

func TestCompleteFiles_ViewerForbidden(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	ctx := pushCtx()
	ctx = httpx.WithWorkspaceRole(ctx, "viewer")

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_a"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestCompleteFiles_EmptyBatch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":[]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCompleteFiles_NoUserID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_a"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
