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
	"github.com/kyenel64/invosit/api/internal/storage"
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
func (s *stubStorage) List(_ context.Context, _ string, _ func(storage.Object) error) error {
	return nil
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
	mock.ExpectQuery(`SELECT id, path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE environment_id = \$1`).
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
		// `status` is no longer on the wire — pending vs committed is
		// internal to the server. Reads only return committed files
		// (pending uploads live in a separate pending_files table).
		if _, present := f["status"]; present {
			t.Errorf("files[%d] should not include a status field", i)
		}
	}
	if stub.getCalls != 2 {
		t.Errorf("getCalls = %d, want 2 (one per row)", stub.getCalls)
	}
}

func TestListFiles_Empty(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT id, path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE environment_id = \$1`).
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
	mock.ExpectQuery(`SELECT path, content_hash, size, pushed_by, pushed_at\s+FROM files\s+WHERE id = \$1 AND environment_id = \$2`).
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
	if _, present := got["status"]; present {
		t.Errorf("response should not include a status field")
	}
	if stub.getCalls != 1 {
		t.Errorf("getCalls = %d, want 1", stub.getCalls)
	}
}

// A pending-only file has no row in `files` (it lives in pending_files),
// so GET /files/{id} returns 403 naturally — same response as a genuinely
// unknown id. The caller can't tell whether an in-progress upload exists.
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

// ── CreateFiles (Phase 1: insert into pending_files + signed PUT URL) ──

// expectPendingInsert matches the single INSERT into pending_files that
// createOne issues. Argument is ignored — kept on the helper signature
// so existing call sites read naturally.
func expectPendingInsert(mock sqlmock.Sqlmock, _ string) {
	mock.ExpectExec(`INSERT INTO pending_files`).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestCreateFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectPendingInsert(mock, "file_a")
	expectPendingInsert(mock, "file_b")

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
		// `file` carries the pending state we just registered. There is
		// no longer a `status` field on the wire — pending vs committed
		// is internal to the server now.
		if _, present := file["status"]; present {
			t.Errorf("results[%d] file should not include a status field", i)
		}
		if file["content_hash"] != validHash {
			t.Errorf("results[%d] content_hash = %v", i, file["content_hash"])
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
	expectPendingInsert(mock, "file_a")
	expectPendingInsert(mock, "file_c")

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

// Structurally-bad entries (zero size, wrong-length hash, oversized
// path) must be reported as per-entry errors, not as a top-level 400 —
// otherwise a single bad entry would reject the whole batch.
func TestCreateFiles_StructurallyBadEntriesArePerEntry(t *testing.T) {
	cases := map[string]map[string]any{
		"zero size":         {"path": "a.env", "content_hash": validHash, "size": 0},
		"wrong-length hash": {"path": "a.env", "content_hash": "tooshort", "size": 1},
		"oversized path":    {"path": strings.Repeat("a/", 600), "content_hash": validHash, "size": 1},
	}
	for name, badEntry := range cases {
		t.Run(name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()
			expectPendingInsert(mock, "file_ok")

			h := &Handler{db: db, blobs: &stubStorage{putURL: "https://signed/put"}}
			body, _ := json.Marshal(map[string]any{
				"files": []map[string]any{
					{"path": "ok.env", "content_hash": validHash, "size": 1},
					badEntry,
				},
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
			if got.Results[0]["status"] != "ok" {
				t.Errorf("good entry should succeed, got %+v", got.Results[0])
			}
			if got.Results[1]["status"] != "error" || got.Results[1]["code"] != "INVALID_REQUEST" {
				t.Errorf("bad entry should be per-entry INVALID_REQUEST, got %+v", got.Results[1])
			}
		})
	}
}

func TestCreateFiles_InsertFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectExec(`INSERT INTO pending_files`).
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

	expectPendingInsert(mock, "file_xyz")

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

// ── CompleteFiles (Phase 2: move pending_files row → files) ──

// expectCompleteMove mocks the full transactional move performed by
// completeOne: BEGIN → SELECT pending → UPSERT files → DELETE pending →
// COMMIT.
func expectCompleteMove(mock sqlmock.Sqlmock, fileID, path, hash string) {
	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT workspace_id, path, content_hash, size, pushed_by, pushed_at, completed_file_id\s+FROM pending_files`).
		WithArgs(fileID, "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "path", "content_hash", "size", "pushed_by", "pushed_at", "completed_file_id"}).
			AddRow("ws_abc", path, hash, int64(100), "usr_abc", pushedAt, nil))
	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(fileID))
	mock.ExpectExec(`UPDATE pending_files SET completed_file_id = \$1 WHERE id = \$2`).
		WithArgs(fileID, fileID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectCompleteNoPending(mock sqlmock.Sqlmock, fileID, path, hash string, committed bool) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT workspace_id, path, content_hash, size, pushed_by, pushed_at, completed_file_id\s+FROM pending_files`).
		WithArgs(fileID, "env_abc").
		WillReturnError(sql.ErrNoRows)
	if committed {
		pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
		mock.ExpectQuery(`SELECT path, content_hash, size, pushed_by, pushed_at\s+FROM files`).
			WithArgs(fileID, "env_abc").
			WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "pushed_by", "pushed_at"}).
				AddRow(path, hash, int64(100), "usr_abc", pushedAt))
	} else {
		mock.ExpectQuery(`SELECT path, content_hash, size, pushed_by, pushed_at\s+FROM files`).
			WithArgs(fileID, "env_abc").
			WillReturnError(sql.ErrNoRows)
	}
	mock.ExpectRollback()
}

// expectCompleteRetry mocks the retry-after-replacement path: BEGIN →
// SELECT pending returns a row whose completed_file_id points at the
// committed file → fall back to SELECT files by that committed id →
// Rollback fires when completeOne returns.
func expectCompleteRetry(mock sqlmock.Sqlmock, pendingID, committedID, path, hash string) {
	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT workspace_id, path, content_hash, size, pushed_by, pushed_at, completed_file_id\s+FROM pending_files`).
		WithArgs(pendingID, "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "path", "content_hash", "size", "pushed_by", "pushed_at", "completed_file_id"}).
			AddRow("ws_abc", path, hash, int64(100), "usr_abc", pushedAt, committedID))
	mock.ExpectQuery(`SELECT path, content_hash, size, pushed_by, pushed_at\s+FROM files`).
		WithArgs(committedID, "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "pushed_by", "pushed_at"}).
			AddRow(path, hash, int64(100), "usr_abc", pushedAt))
	mock.ExpectRollback()
}

func TestCompleteFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteMove(mock, "file_a", "a.env", validHash)
	expectCompleteMove(mock, "file_b", "b.env", validHash)

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
		// `status` is internal — not on the wire.
		if _, present := file["status"]; present {
			t.Errorf("results[%d] file should not include a status field", i)
		}
	}
}

func TestCompleteFiles_Idempotent(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteNoPending(mock, "file_xyz", "a.env", validHash, true)

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

// Retrying :complete with the original pending id after a replacement
// upload (pending id != committed id) returns the committed file via the
// soft-deleted pending row's completed_file_id pointer.
func TestCompleteFiles_RetryAfterReplacement(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteRetry(mock, "file_new", "file_existing", "config/.env", validHash)

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"file_ids":["file_new"]}`
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
	file, ok := got.Results[0]["file"].(map[string]any)
	if !ok {
		t.Fatalf("file missing")
	}
	if file["id"] != "file_existing" {
		t.Errorf("file.id = %v, want file_existing (the committed id, not the pending id)", file["id"])
	}
}

func TestCompleteFiles_NotFoundReturnsForbidden(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteNoPending(mock, "file_missing", "", "", false)

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

	expectCompleteMove(mock, "file_a", "a.env", validHash)
	expectCompleteNoPending(mock, "file_missing", "", "", false)

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
