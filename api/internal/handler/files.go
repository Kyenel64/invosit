package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kyenel64/invosit/api/internal/httpx"
	"github.com/kyenel64/invosit/api/internal/ids"
	"github.com/kyenel64/invosit/api/internal/storage"
)

const maxBatchSize = 100

type fileMeta struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Path          string    `json:"path"`
	ContentHash   string    `json:"content_hash"`
	Size          int64     `json:"size"`
	PushedBy      string    `json:"pushed_by"`
	PushedAt      time.Time `json:"pushed_at"`
	Status        string    `json:"status"`
}

// --- Create Files ----------------------------------------------------------

type createFileRequest struct {
	Path        string `json:"path"         validate:"required,max=1024"`
	ContentHash string `json:"content_hash" validate:"required,len=64"`
	Size        int64  `json:"size"         validate:"required,gt=0"`
}

type createFilesRequest struct {
	Files []createFileRequest `json:"files" validate:"required,dive"`
}

type createFilesResult struct {
	Path            string     `json:"path"`
	Status          string     `json:"status"`
	File            *fileMeta  `json:"file,omitempty"`
	UploadURL       string     `json:"upload_url,omitempty"`
	UploadExpiresAt *time.Time `json:"upload_expires_at,omitempty"`
	Code            string     `json:"code,omitempty"`
	Message         string     `json:"message,omitempty"`
}

type createFilesResponse struct {
	Results []createFilesResult `json:"results"`
}

// CreateFiles batch creates db metadata entries for the files requested and returns a list of signed urls for file upload.
// files are entered in db as 'pending' while the client is uploading file blob to s3.
// client must call /files:complete after successful s3 upload using the signed url.
func (h *Handler) CreateFiles(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserID(r.Context())
	if uid == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())
	if httpx.WorkspaceRole(r.Context()) == "viewer" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "write permission required")
		return
	}

	var req createFilesRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid create files request")
		return
	}
	if len(req.Files) == 0 {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch must contain at least one file")
		return
	}
	if len(req.Files) > maxBatchSize {
		httpx.RespondError(w, http.StatusBadRequest, "BATCH_TOO_LARGE", "batch exceeds maximum size")
		return
	}

	results := make([]createFilesResult, 0, len(req.Files))
	for _, entry := range req.Files {
		results = append(results, h.createOne(r.Context(), workspaceID, envID, uid, entry))
	}

	httpx.WriteJSON(w, http.StatusOK, createFilesResponse{Results: results})
}

// createOne creates a single file entry in the db and constructs an s3 signed put url
// returns file result with 'pending' status
func (h *Handler) createOne(ctx context.Context, workspaceID, envID, uid string, entry createFileRequest) createFilesResult {
	path := strings.TrimSpace(entry.Path)
	if err := validateFilePath(path); err != nil {
		return createErrorResult(path, "INVALID_REQUEST", "invalid path")
	}
	if err := validateSha256Hex(entry.ContentHash); err != nil {
		return createErrorResult(path, "INVALID_REQUEST", "invalid content hash")
	}

	pushedAt := time.Now().UTC()
	fileID := ids.File()

	if err := h.db.QueryRowContext(ctx,
		`INSERT INTO files (id, workspace_id, environment_id, path, content_hash, size, pushed_by, pushed_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending')
		 ON CONFLICT (environment_id, path) DO UPDATE
		   SET content_hash = EXCLUDED.content_hash,
		       size         = EXCLUDED.size,
		       pushed_by    = EXCLUDED.pushed_by,
		       pushed_at    = EXCLUDED.pushed_at,
		       status       = 'pending'
		 RETURNING id`,
		fileID, workspaceID, envID, path, entry.ContentHash, entry.Size, uid, pushedAt,
	).Scan(&fileID); err != nil {
		log.Printf("req=%s create_files_upsert_failed err=%v", httpx.RequestID(ctx), err)
		return createErrorResult(path, "INTERNAL_ERROR", "internal error")
	}

	uploadURL, err := h.blobs.SignedPutURL(ctx, workspaceID+"/"+entry.ContentHash, storage.MaxSignedURLExpiry)
	if err != nil {
		log.Printf("req=%s create_files_presign_failed err=%v", httpx.RequestID(ctx), err)
		return createErrorResult(path, "INTERNAL_ERROR", "internal error")
	}

	expiresAt := time.Now().UTC().Add(storage.MaxSignedURLExpiry)
	return createFilesResult{
		Path:   path,
		Status: "ok",
		File: &fileMeta{
			ID:            fileID,
			EnvironmentID: envID,
			Path:          path,
			ContentHash:   entry.ContentHash,
			Size:          entry.Size,
			PushedBy:      uid,
			PushedAt:      pushedAt,
			Status:        "pending",
		},
		UploadURL:       uploadURL,
		UploadExpiresAt: &expiresAt,
	}
}

func createErrorResult(path, code, message string) createFilesResult {
	return createFilesResult{
		Path:    path,
		Status:  "error",
		Code:    code,
		Message: message,
	}
}

// --- Complete Files ----------------------------------------------------------

type completeFilesRequest struct {
	FileIDs []string `json:"file_ids" validate:"required,dive,required"`
}

type completeFilesResult struct {
	ID      string    `json:"id"`
	Status  string    `json:"status"`
	File    *fileMeta `json:"file,omitempty"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
}

type completeFilesResponse struct {
	Results []completeFilesResult `json:"results"`
}

func (h *Handler) CompleteFiles(w http.ResponseWriter, r *http.Request) {
	if httpx.UserID(r.Context()) == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	envID := httpx.EnvironmentID(r.Context())
	if httpx.WorkspaceRole(r.Context()) == "viewer" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "write permission required")
		return
	}

	var req completeFilesRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid complete files request")
		return
	}
	if len(req.FileIDs) == 0 {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch must contain at least one file id")
		return
	}
	if len(req.FileIDs) > maxBatchSize {
		httpx.RespondError(w, http.StatusBadRequest, "BATCH_TOO_LARGE", "batch exceeds maximum size")
		return
	}

	results := make([]completeFilesResult, 0, len(req.FileIDs))
	for _, fileID := range req.FileIDs {
		results = append(results, h.completeOne(r.Context(), envID, fileID))
	}

	httpx.WriteJSON(w, http.StatusOK, completeFilesResponse{Results: results})
}

func (h *Handler) completeOne(ctx context.Context, envID, fileID string) completeFilesResult {
	if fileID == "" {
		return completeErrorResult(fileID, "FORBIDDEN", "access denied")
	}

	var (
		path, hash, status string
		size               int64
		pushedBy           sql.NullString
		pushedAt           time.Time
	)
	err := h.db.QueryRowContext(ctx,
		`UPDATE files
		    SET status = 'committed'
		  WHERE id = $1 AND environment_id = $2
		  RETURNING path, content_hash, size, pushed_by, pushed_at, status`,
		fileID, envID,
	).Scan(&path, &hash, &size, &pushedBy, &pushedAt, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return completeErrorResult(fileID, "FORBIDDEN", "access denied")
		}
		log.Printf("req=%s complete_files_update_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}

	return completeFilesResult{
		ID:     fileID,
		Status: "ok",
		File: &fileMeta{
			ID:            fileID,
			EnvironmentID: envID,
			Path:          path,
			ContentHash:   hash,
			Size:          size,
			PushedBy:      pushedBy.String,
			PushedAt:      pushedAt,
			Status:        status,
		},
	}
}

func completeErrorResult(fileID, code, message string) completeFilesResult {
	return completeFilesResult{
		ID:      fileID,
		Status:  "error",
		Code:    code,
		Message: message,
	}
}

// ──- List Files ───────────────────────────────────--------------------------

type listedFileMeta struct {
	fileMeta
	DownloadURL       string    `json:"download_url"`
	DownloadExpiresAt time.Time `json:"download_expires_at"`
}

type listFilesResponse struct {
	Files []listedFileMeta `json:"files"`
}

func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	if httpx.UserID(r.Context()) == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, path, content_hash, size, pushed_by, pushed_at
		   FROM files
		  WHERE environment_id = $1 AND status = 'committed'
		  ORDER BY path ASC`,
		envID,
	)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	files := []listedFileMeta{}
	for rows.Next() {
		var (
			id, path, hash string
			size           int64
			pushedBy       sql.NullString
			pushedAt       time.Time
		)
		if err := rows.Scan(&id, &path, &hash, &size, &pushedBy, &pushedAt); err != nil {
			httpx.InternalError(w, r, err)
			return
		}
		downloadURL, err := h.blobs.SignedGetURL(r.Context(), workspaceID+"/"+hash, storage.MaxSignedURLExpiry)
		if err != nil {
			httpx.InternalError(w, r, err)
			return
		}
		files = append(files, listedFileMeta{
			fileMeta: fileMeta{
				ID:            id,
				EnvironmentID: envID,
				Path:          path,
				ContentHash:   hash,
				Size:          size,
				PushedBy:      pushedBy.String,
				PushedAt:      pushedAt,
				Status:        "committed",
			},
			DownloadURL:       downloadURL,
			DownloadExpiresAt: time.Now().UTC().Add(storage.MaxSignedURLExpiry),
		})
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, listFilesResponse{Files: files})
}

func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request) {
	if httpx.UserID(r.Context()) == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())
	fileID := r.PathValue("fileId")
	if fileID == "" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
		return
	}

	var (
		path, hash string
		size       int64
		pushedBy   sql.NullString
		pushedAt   time.Time
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT path, content_hash, size, pushed_by, pushed_at
		   FROM files
		  WHERE id = $1 AND environment_id = $2 AND status = 'committed'`,
		fileID, envID,
	).Scan(&path, &hash, &size, &pushedBy, &pushedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	downloadURL, err := h.blobs.SignedGetURL(r.Context(), workspaceID+"/"+hash, storage.MaxSignedURLExpiry)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, listedFileMeta{
		fileMeta: fileMeta{
			ID:            fileID,
			EnvironmentID: envID,
			Path:          path,
			ContentHash:   hash,
			Size:          size,
			PushedBy:      pushedBy.String,
			PushedAt:      pushedAt,
			Status:        "committed",
		},
		DownloadURL:       downloadURL,
		DownloadExpiresAt: time.Now().UTC().Add(storage.MaxSignedURLExpiry),
	})
}

// --- Delete File ------------------------------------------------------------

// DeleteFile removes the files row (cascades to wrapped_deks) and
// best-effort deletes the orphaned blob.
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	if httpx.UserID(r.Context()) == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())
	role := httpx.WorkspaceRole(r.Context())
	fileID := r.PathValue("fileId")
	if fileID == "" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
		return
	}
	if role == "viewer" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "write permission required")
		return
	}

	var contentHash string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT content_hash FROM files WHERE id = $1 AND environment_id = $2`,
		fileID, envID,
	).Scan(&contentHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM files WHERE id = $1 AND environment_id = $2`,
		fileID, envID,
	)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	affected, err := res.RowsAffected()
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	if affected == 0 {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
		return
	}

	blobKey := workspaceID + "/" + contentHash
	if err := h.blobs.Delete(r.Context(), blobKey); err != nil {
		// Orphan blob is recoverable via a sweep; failing the request would
		// suggest the DB row is still there, which would be misleading.
		log.Printf("req=%s blob_delete_failed key=%q err=%v",
			httpx.RequestID(r.Context()), blobKey, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Validation -------------------------------------------------------------

// reject "..", absolute paths, null bytes, and leading separators.
func validateFilePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("null byte in path")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return errors.New("absolute path")
	}
	// Treat both separators as path-segment delimiters so Windows-style
	// inputs can't sneak ".." past the check.
	for _, sep := range []string{"/", "\\"} {
		for _, seg := range strings.Split(p, sep) {
			if seg == ".." {
				return errors.New("path traversal")
			}
		}
	}
	return nil
}

// validateSha256Hex requires exactly 64 lowercase hex chars. The lowercase
// requirement is part of the wire contract: the same bytes must always hash
// to the same blob key, so accepting mixed case would let two clients push
// the same file to two different blob keys.
func validateSha256Hex(s string) error {
	if len(s) != 64 {
		return errors.New("invalid hash length")
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return errors.New("invalid hash format")
		}
	}
	return nil
}
