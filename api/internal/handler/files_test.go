package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
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
	"github.com/lib/pq"
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

var validWrappedDEK = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, wrappedDEKLen))

func wrappedDEKsJSON(uid string) string {
	return `[{"user_id":"` + uid + `","encrypted_dek":"` + validWrappedDEK + `"}]`
}

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
	wrappedDEK := []byte("wrapped-dek-for-caller")
	mock.ExpectQuery(`SELECT f.id, f.path, f.content_hash, f.size, f.version, f.pushed_by, f.pushed_at, wd.encrypted_dek\s+FROM files f\s+JOIN wrapped_deks wd ON wd.file_id = f.id AND wd.user_id = \$2\s+WHERE f.environment_id = \$1`).
		WithArgs("env_abc", "usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "path", "content_hash", "size", "version", "pushed_by", "pushed_at", "encrypted_dek"}).
			AddRow("file_a", "a.env", validHash, int64(10), int64(1), "usr_abc", pushedAt, wrappedDEK).
			AddRow("file_b", "b.env", validHash, int64(20), int64(4), "usr_xyz", pushedAt, wrappedDEK))

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
	if got.Files[0]["version"] != float64(1) {
		t.Errorf("files[0] version = %v, want 1", got.Files[0]["version"])
	}
	// The INNER JOIN read path only returns files the caller holds a wrapped
	// DEK for, so every listed file carries one.
	for i, f := range got.Files {
		if f["wrapped_dek"] != base64.StdEncoding.EncodeToString(wrappedDEK) {
			t.Errorf("files[%d] wrapped_dek = %v, want base64 of caller DEK", i, f["wrapped_dek"])
		}
	}
}

func TestListFiles_Empty(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT f.id, f.path, f.content_hash, f.size, f.version, f.pushed_by, f.pushed_at, wd.encrypted_dek\s+FROM files f\s+JOIN wrapped_deks wd ON wd.file_id = f.id AND wd.user_id = \$2\s+WHERE f.environment_id = \$1`).
		WithArgs("env_abc", "usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "path", "content_hash", "size", "version", "pushed_by", "pushed_at", "encrypted_dek"}))

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
	wrappedDEK := []byte("wrapped-dek-for-caller")
	mock.ExpectQuery(`SELECT f.path, f.content_hash, f.size, f.version, f.pushed_by, f.pushed_at, wd.encrypted_dek\s+FROM files f\s+JOIN wrapped_deks wd ON wd.file_id = f.id AND wd.user_id = \$3\s+WHERE f.id = \$1 AND f.environment_id = \$2`).
		WithArgs("file_xyz", "env_abc", "usr_abc").
		WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "version", "pushed_by", "pushed_at", "encrypted_dek"}).
			AddRow("a.env", validHash, int64(123), int64(2), "usr_abc", pushedAt, wrappedDEK))

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
	if got["wrapped_dek"] != base64.StdEncoding.EncodeToString(wrappedDEK) {
		t.Errorf("wrapped_dek = %v, want base64 of caller DEK", got["wrapped_dek"])
	}
	if stub.getCalls != 1 {
		t.Errorf("getCalls = %d, want 1", stub.getCalls)
	}
}

// With the caller holding no wrapped_deks row, the INNER JOIN yields no row
// and the file is a 403 — indistinguishable from a nonexistent id, and no
// signed URL is ever issued without an authorising DEK.
func TestGetFile_NoDEKReturns403(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT f.path, f.content_hash`).
		WithArgs("file_xyz", "env_abc", "usr_abc").
		WillReturnError(sql.ErrNoRows)

	stub := &stubStorage{getURL: "https://signed/get"}
	h := &Handler{db: db, blobs: stub}
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(pushCtx())
	req.SetPathValue("fileId", "file_xyz")
	rec := httptest.NewRecorder()
	h.GetFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body = %s)", rec.Code, rec.Body.String())
	}
	if stub.getCalls != 0 {
		t.Errorf("getCalls = %d, want 0 (no DEK row must mean no signed URL)", stub.getCalls)
	}
}

// A pending-only file has no row in `files` (it lives in pending_files),
// so GET /files/{id} returns 403 naturally — same response as a genuinely
// unknown id. The caller can't tell whether an in-progress upload exists.
func TestGetFile_PendingHiddenReturns403(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT f.path, f.content_hash`).
		WithArgs("file_pending", "env_abc", "usr_abc").
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

	mock.ExpectQuery(`SELECT f.path, f.content_hash`).
		WithArgs("file_missing", "env_abc", "usr_abc").
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

// expectPendingInsert matches the advisory version pre-check followed by the
// transaction createOne runs for a new file (base 0, no existing row): INSERT
// into pending_files plus the caller's pending_file_deks row. Argument is
// ignored — kept on the helper signature so existing call sites read naturally.
func expectPendingInsert(mock sqlmock.Sqlmock, _ string) {
	mock.ExpectQuery(`SELECT version FROM files WHERE environment_id = \$1 AND path = \$2`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO pending_files`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO pending_file_deks`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestCreateFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectPendingInsert(mock, "file_a")
	expectPendingInsert(mock, "file_b")

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}

	body := `{"files":[
		{"path":"a.env","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `},
		{"path":"b.env","content_hash":"` + validHash + `","size":2,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}
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
		{"path":"a.env","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `},
		{"path":"../escape","content_hash":"` + validHash + `","size":2,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `},
		{"path":"c.env","content_hash":"` + validHash + `","size":3,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}
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
					{"path": "ok.env", "content_hash": validHash, "size": 1, "base_version": 0,
						"wrapped_deks": []map[string]any{{"user_id": "usr_abc", "encrypted_dek": bytes.Repeat([]byte{1}, wrappedDEKLen)}}},
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

	mock.ExpectQuery(`SELECT version FROM files WHERE environment_id = \$1 AND path = \$2`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO pending_files`).
		WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	h := &Handler{db: db, blobs: &stubStorage{}}
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}]}`
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
	body := `{"files":[{"path":"a","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}]}`
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

// A push entry without base_version is rejected per-entry — every push must
// declare the version it is based on.
func TestCreateFiles_MissingBaseVersion(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}
	body := `{"files":[{"path":"a.env","content_hash":"` + validHash + `","size":1}]}`
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
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INVALID_REQUEST" {
		t.Errorf("result = %+v, want error/INVALID_REQUEST", got.Results[0])
	}
	if stub.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (no base = no presign)", stub.putCalls)
	}
}

// The advisory pre-check fails an obviously stale push before issuing a signed
// URL: base_version no longer matches the committed file.
func TestCreateFiles_StaleBaseConflict(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT version FROM files WHERE environment_id = \$1 AND path = \$2`).
		WithArgs("env_abc", "a.env").
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(int64(3)))

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}
	body := `{"files":[{"path":"a.env","content_hash":"` + validHash + `","size":1,"base_version":2,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}]}`
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
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "CONFLICT" {
		t.Errorf("result = %+v, want error/CONFLICT", got.Results[0])
	}
	if stub.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0 (stale base skips presign)", stub.putCalls)
	}
}

// Every push entry must wrap the DEK for the caller — missing, foreign,
// duplicate, or wrong-sized wrapped DEKs fail per-entry before any DB write.
func TestCreateFiles_RejectsBadWrappedDEKs(t *testing.T) {
	shortDEK := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, wrappedDEKLen-1))
	cases := map[string]string{
		"missing":             `[]`,
		"foreign recipient":   `[{"user_id":"usr_other","encrypted_dek":"` + validWrappedDEK + `"}]`,
		"duplicate recipient": `[{"user_id":"usr_abc","encrypted_dek":"` + validWrappedDEK + `"},{"user_id":"usr_abc","encrypted_dek":"` + validWrappedDEK + `"}]`,
		"wrong length":        `[{"user_id":"usr_abc","encrypted_dek":"` + shortDEK + `"}]`,
	}
	for name, deks := range cases {
		t.Run(name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()

			stub := &stubStorage{putURL: "https://signed/put"}
			h := &Handler{db: db, blobs: stub}
			body := `{"files":[{"path":"a.env","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + deks + `}]}`
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
			if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INVALID_REQUEST" {
				t.Errorf("result = %+v, want error/INVALID_REQUEST", got.Results[0])
			}
			if stub.putCalls != 0 {
				t.Errorf("putCalls = %d, want 0 (bad wrapped deks skip presign)", stub.putCalls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("no SQL expected: %v", err)
			}
		})
	}
}

// A failure inserting the pending DEK rows rolls the pending file back too —
// phase 1 never leaves a pending row without its wrapped DEKs.
func TestCreateFiles_DEKInsertFailureRollsBack(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery(`SELECT version FROM files WHERE environment_id = \$1 AND path = \$2`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO pending_files`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO pending_file_deks`).
		WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	stub := &stubStorage{putURL: "https://signed/put"}
	h := &Handler{db: db, blobs: stub}
	body := `{"files":[{"path":"a.env","content_hash":"` + validHash + `","size":1,"base_version":0,"wrapped_deks":` + wrappedDEKsJSON("usr_abc") + `}]}`
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
	if stub.putCalls != 0 {
		t.Errorf("putCalls = %d, want 0", stub.putCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// ── CompleteFiles (Phase 2: move pending_files row → files) ──

const pendingSelectCols = `SELECT workspace_id, path, content_hash, size, pushed_by, pushed_at, completed_file_id, expected_version\s+FROM pending_files`

// pendingRow builds the pending_files result row in the column order completeOne
// scans. completedFileID is the soft-delete pointer (nil until completed).
func pendingRow(path, hash string, completedFileID any, expectedVersion int64) *sqlmock.Rows {
	pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	return sqlmock.NewRows([]string{"workspace_id", "path", "content_hash", "size", "pushed_by", "pushed_at", "completed_file_id", "expected_version"}).
		AddRow("ws_abc", path, hash, int64(100), "usr_abc", pushedAt, completedFileID, expectedVersion)
}

// expectCompleteCreate mocks completeOne committing a pending row as a NEW file
// (base 0, no committed row): BEGIN → SELECT pending → SELECT current (none) →
// INSERT files → move wrapped DEKs → mark pending completed → COMMIT.
func expectCompleteCreate(mock sqlmock.Sqlmock, fileID, path, hash string) {
	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs(fileID, "env_abc").WillReturnRows(pendingRow(path, hash, nil, 0))
	mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", path).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(fileID))
	mock.ExpectExec(`DELETE FROM wrapped_deks WHERE file_id = \$1`).
		WithArgs(fileID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO wrapped_deks \(file_id, user_id, encrypted_dek\)\s+SELECT \$1, user_id, encrypted_dek FROM pending_file_deks WHERE pending_file_id = \$2`).
		WithArgs(fileID, fileID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE pending_files SET completed_file_id = \$1 WHERE id = \$2`).
		WithArgs(fileID, fileID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

// expectCompleteUpdate mocks completeOne overwriting an existing file whose
// committed version still matches the base: BEGIN → SELECT pending (base) →
// SELECT current (row at base) → UPDATE files (version base+1) → move wrapped
// DEKs (keyed by the COMMITTED id, pending id only as the copy source) → mark
// pending completed → COMMIT. committedID is the existing files row id (≠ pendingID).
func expectCompleteUpdate(mock sqlmock.Sqlmock, pendingID, committedID, path, hash string, base int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs(pendingID, "env_abc").WillReturnRows(pendingRow(path, hash, nil, base))
	mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", path).
		WillReturnRows(sqlmock.NewRows([]string{"id", "version"}).AddRow(committedID, base))
	mock.ExpectExec(`UPDATE files\s+SET content_hash`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM wrapped_deks WHERE file_id = \$1`).
		WithArgs(committedID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO wrapped_deks \(file_id, user_id, encrypted_dek\)\s+SELECT \$1, user_id, encrypted_dek FROM pending_file_deks WHERE pending_file_id = \$2`).
		WithArgs(committedID, pendingID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE pending_files SET completed_file_id = \$1 WHERE id = \$2`).
		WithArgs(committedID, pendingID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectCompleteNoPending(mock sqlmock.Sqlmock, fileID, path, hash string, committed bool) {
	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs(fileID, "env_abc").WillReturnError(sql.ErrNoRows)
	committedSelect := mock.ExpectQuery(`SELECT path, content_hash, size, version, pushed_by, pushed_at\s+FROM files`).
		WithArgs(fileID, "env_abc")
	if committed {
		pushedAt := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
		committedSelect.WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "version", "pushed_by", "pushed_at"}).
			AddRow(path, hash, int64(100), int64(1), "usr_abc", pushedAt))
	} else {
		committedSelect.WillReturnError(sql.ErrNoRows)
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
	mock.ExpectQuery(pendingSelectCols).WithArgs(pendingID, "env_abc").WillReturnRows(pendingRow(path, hash, committedID, 0))
	mock.ExpectQuery(`SELECT path, content_hash, size, version, pushed_by, pushed_at\s+FROM files`).
		WithArgs(committedID, "env_abc").
		WillReturnRows(sqlmock.NewRows([]string{"path", "content_hash", "size", "version", "pushed_by", "pushed_at"}).
			AddRow(path, hash, int64(100), int64(2), "usr_abc", pushedAt))
	mock.ExpectRollback()
}

func TestCompleteFiles_Success(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteCreate(mock, "file_a", "a.env", validHash)
	expectCompleteCreate(mock, "file_b", "b.env", validHash)

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
		if file["version"] != float64(1) {
			t.Errorf("results[%d] version = %v, want 1 (new file)", i, file["version"])
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

// Overwriting a file whose committed version still matches the base bumps the
// version and returns the existing committed id.
func TestCompleteFiles_UpdateBumpsVersion(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	expectCompleteUpdate(mock, "file_new", "file_existing", "a.env", validHash, 2)

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
		t.Fatalf("status = %v, want ok (body = %s)", got.Results[0]["status"], rec.Body.String())
	}
	file := got.Results[0]["file"].(map[string]any)
	if file["id"] != "file_existing" {
		t.Errorf("file.id = %v, want file_existing", file["id"])
	}
	if file["version"] != float64(3) {
		t.Errorf("file.version = %v, want 3 (base 2 + 1)", file["version"])
	}
}

// expectCompleteConflict mocks completeOne detecting a CAS conflict and rolling
// back without writing: BEGIN → SELECT pending (base) → SELECT current →
// ROLLBACK. currentVersion < 0 means the committed row is absent.
func expectCompleteConflict(mock sqlmock.Sqlmock, pendingID, path, hash string, base, currentVersion int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs(pendingID, "env_abc").WillReturnRows(pendingRow(path, hash, nil, base))
	cur := mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", path)
	if currentVersion < 0 {
		cur.WillReturnError(sql.ErrNoRows)
	} else {
		cur.WillReturnRows(sqlmock.NewRows([]string{"id", "version"}).AddRow("file_existing", currentVersion))
	}
	mock.ExpectRollback()
}

func assertCompleteConflict(t *testing.T, h *Handler) {
	t.Helper()
	body := `{"file_ids":["file_new"]}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(pushCtx())
	rec := httptest.NewRecorder()
	h.CompleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body = %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "CONFLICT" {
		t.Errorf("result = %+v, want error/CONFLICT", got.Results[0])
	}
}

// Updating against a base the committed version has moved past → CONFLICT.
func TestCompleteFiles_ConflictVersionMismatch(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectCompleteConflict(mock, "file_new", "a.env", validHash, 2, 5)
	assertCompleteConflict(t, &Handler{db: db, blobs: &stubStorage{}})
}

// Updating a file that no longer exists (deleted out from under the base) → CONFLICT.
func TestCompleteFiles_ConflictExpectedMissing(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectCompleteConflict(mock, "file_new", "a.env", validHash, 2, -1)
	assertCompleteConflict(t, &Handler{db: db, blobs: &stubStorage{}})
}

// Creating (base 0) when a row already exists → CONFLICT.
func TestCompleteFiles_ConflictCreateCollision(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	expectCompleteConflict(mock, "file_new", "a.env", validHash, 0, 3)
	assertCompleteConflict(t, &Handler{db: db, blobs: &stubStorage{}})
}

// Two concurrent creates of the same path: the second loses the unique-index
// race on INSERT and is mapped to CONFLICT, not a 500.
func TestCompleteFiles_ConflictCreateRace(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs("file_new", "env_abc").WillReturnRows(pendingRow("a.env", validHash, nil, 0))
	mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", "a.env").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectRollback()

	assertCompleteConflict(t, &Handler{db: db, blobs: &stubStorage{}})
}

// A pending row with no pending_file_deks rows (pre-encryption leftover or a
// bug) must not commit — a file nobody can decrypt would be invisible under
// the INNER JOIN read path. The whole transaction rolls back.
func TestCompleteFiles_MissingWrappedDEKsRollsBack(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs("file_new", "env_abc").WillReturnRows(pendingRow("a.env", validHash, nil, 0))
	mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", "a.env").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO files`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("file_new"))
	mock.ExpectExec(`DELETE FROM wrapped_deks WHERE file_id = \$1`).
		WithArgs("file_new").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO wrapped_deks`).
		WithArgs("file_new", "file_new").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

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
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INVALID_REQUEST" {
		t.Errorf("result = %+v, want error/INVALID_REQUEST", got.Results[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// files + wrapped_deks commit atomically: a failure moving the DEKs rolls
// back the files write in the same transaction.
func TestCompleteFiles_DEKMoveFailureRollsBack(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(pendingSelectCols).WithArgs("file_new", "env_abc").WillReturnRows(pendingRow("a.env", validHash, nil, 2))
	mock.ExpectQuery(`SELECT id, version FROM files WHERE environment_id = \$1 AND path = \$2 FOR UPDATE`).
		WithArgs("env_abc", "a.env").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version"}).AddRow("file_existing", int64(2)))
	mock.ExpectExec(`UPDATE files\s+SET content_hash`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM wrapped_deks WHERE file_id = \$1`).
		WithArgs("file_existing").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO wrapped_deks`).
		WithArgs("file_existing", "file_new").
		WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

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
	if got.Results[0]["status"] != "error" || got.Results[0]["code"] != "INTERNAL_ERROR" {
		t.Errorf("result = %+v, want error/INTERNAL_ERROR", got.Results[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
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

	expectCompleteCreate(mock, "file_a", "a.env", validHash)
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
