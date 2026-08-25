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
	"github.com/lib/pq"
)

// recordAudit best-effort logs an audit event. Failures never abort the main action.
func (h *Handler) recordAudit(ctx context.Context, action, workspaceID, fileID, userID, ip string) {
	logID := ids.AuditLog()
	timestamp := time.Now().UTC()

	var fileIDArg any
	if fileID == "" {
		fileIDArg = nil
	} else {
		fileIDArg = fileID
	}

	_, err := h.db.ExecContext(ctx,
		`INSERT INTO audit_logs (id, user_id, workspace_id, action, file_id, ip, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		logID, userID, workspaceID, action, fileIDArg, ip, timestamp,
	)
	if err != nil {
		log.Printf("req=%s audit_log_failed action=%s err=%v", httpx.RequestID(ctx), action, err)
	}
}

const maxBatchSize = 100

const conflictMessage = "version conflict: file changed since the base version"

// 32-byte DEK + 48-byte x25519 anonymous-sealed-box overhead.
const wrappedDEKLen = 80

type fileMeta struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Path          string    `json:"path"`
	ContentHash   string    `json:"content_hash"`
	Size          int64     `json:"size"`
	Version       int64     `json:"version"`
	PushedBy      string    `json:"pushed_by"`
	PushedAt      time.Time `json:"pushed_at"`
	WrappedDEK    []byte    `json:"wrapped_dek,omitempty"`
}

// --- Create Files ----------------------------------------------------------

type wrappedDEKEntry struct {
	UserID       string `json:"user_id"`
	EncryptedDEK []byte `json:"encrypted_dek"` // never log these bytes.
}

type createFileRequest struct {
	Path        string            `json:"path"`
	ContentHash string            `json:"content_hash"`
	Size        int64             `json:"size"`
	BaseVersion *int64            `json:"base_version"`
	WrappedDEKs []wrappedDEKEntry `json:"wrapped_deks"`
}

type createFilesRequest struct {
	Files []createFileRequest `json:"files" validate:"required"`
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

// CreateFiles batch creates file metadata and returns s3 signed urls for each requested file for the client to upload.
// Each file metadata is first inserted to the pending_files table in the db until client uploads and calls /files:complete
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
	if entry.Size <= 0 {
		return createErrorResult(path, "INVALID_REQUEST", "invalid size")
	}
	if entry.BaseVersion == nil {
		return createErrorResult(path, "INVALID_REQUEST", "missing base version")
	}
	base := *entry.BaseVersion
	if err := validateWrappedDEKs(entry.WrappedDEKs, uid); err != nil {
		return createErrorResult(path, "INVALID_REQUEST", err.Error())
	}

	// Check version early incase theres an obvious conflict.
	// We check version on completeOne() as well incase version changes between each call.
	var currentVersion int64
	switch err := h.db.QueryRowContext(ctx,
		`SELECT version FROM files WHERE environment_id = $1 AND path = $2`,
		envID, path,
	).Scan(&currentVersion); {
	case errors.Is(err, sql.ErrNoRows):
		if base != 0 {
			return createErrorResult(path, "CONFLICT", conflictMessage)
		}
	case err != nil:
		log.Printf("req=%s create_files_version_check_failed err=%v", httpx.RequestID(ctx), err)
		return createErrorResult(path, "INTERNAL_ERROR", "internal error")
	default:
		if base != currentVersion {
			return createErrorResult(path, "CONFLICT", conflictMessage)
		}
	}

	pushedAt := time.Now().UTC()
	fileID := ids.File()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("req=%s create_files_begin_failed err=%v", httpx.RequestID(ctx), err)
		return createErrorResult(path, "INTERNAL_ERROR", "internal error")
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pending_files (id, workspace_id, environment_id, path, content_hash, size, pushed_by, pushed_at, expected_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		fileID, workspaceID, envID, path, entry.ContentHash, entry.Size, uid, pushedAt, base,
	); err != nil {
		log.Printf("req=%s create_files_insert_failed err=%v", httpx.RequestID(ctx), err)
		return createErrorResult(path, "INTERNAL_ERROR", "internal error")
	}

	for _, dek := range entry.WrappedDEKs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO pending_file_deks (pending_file_id, user_id, encrypted_dek)
			 VALUES ($1, $2, $3)`,
			fileID, dek.UserID, dek.EncryptedDEK,
		); err != nil {
			log.Printf("req=%s create_files_dek_insert_failed err=%v", httpx.RequestID(ctx), err)
			return createErrorResult(path, "INTERNAL_ERROR", "internal error")
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("req=%s create_files_commit_failed err=%v", httpx.RequestID(ctx), err)
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

// CompleteFiles moves files from the pending_files table to the files table
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
		results = append(results, h.completeOneWithAudit(r, envID, fileID))
	}

	httpx.WriteJSON(w, http.StatusOK, completeFilesResponse{Results: results})
}

func (h *Handler) completeOne(ctx context.Context, envID, fileID, auditWorkspaceID, auditUserID, auditIP string) completeFilesResult {
	if fileID == "" {
		return completeErrorResult(fileID, "FORBIDDEN", "access denied")
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("req=%s complete_files_begin_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}
	defer func() { _ = tx.Rollback() }()

	var (
		workspaceID, path, hash string
		size                    int64
		base                    int64
		pushedBy                sql.NullString
		pushedAt                time.Time
		completedFileID         sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT workspace_id, path, content_hash, size, pushed_by, pushed_at, completed_file_id, expected_version
		   FROM pending_files
		  WHERE id = $1 AND environment_id = $2
		    FOR UPDATE`,
		fileID, envID,
	).Scan(&workspaceID, &path, &hash, &size, &pushedBy, &pushedAt, &completedFileID, &base)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return h.completeAlreadyDone(ctx, envID, fileID)
		}
		log.Printf("req=%s complete_files_select_pending_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}

	if completedFileID.Valid {
		return h.completeAlreadyDone(ctx, envID, completedFileID.String)
	}

	// Compare-and-swap against the committed version. Lock the existing row (if
	// any) so concurrent completes on the same path serialize through here.
	var (
		currentID      string
		currentVersion int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, version FROM files WHERE environment_id = $1 AND path = $2 FOR UPDATE`,
		envID, path,
	).Scan(&currentID, &currentVersion)
	fileExists := true
	if errors.Is(err, sql.ErrNoRows) {
		fileExists = false
	} else if err != nil {
		log.Printf("req=%s complete_files_select_current_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}

	var (
		committedID string
		newVersion  int64
	)
	switch base {
	case 0: // expect absent → create
		if fileExists {
			h.recordAudit(ctx, "conflict", auditWorkspaceID, currentID, auditUserID, auditIP)
			return completeErrorResult(fileID, "CONFLICT", conflictMessage)
		}
		newVersion = 1
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO files (id, workspace_id, environment_id, path, content_hash, size, pushed_by, pushed_at, version)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING id`,
			fileID, workspaceID, envID, path, hash, size, pushedBy.String, pushedAt, newVersion,
		).Scan(&committedID); err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				h.recordAudit(ctx, "conflict", auditWorkspaceID, fileID, auditUserID, auditIP)
				return completeErrorResult(fileID, "CONFLICT", conflictMessage)
			}
			log.Printf("req=%s complete_files_insert_failed err=%v", httpx.RequestID(ctx), err)
			return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
		}
	default: // expect committed version == base → update
		if !fileExists || currentVersion != base {
			h.recordAudit(ctx, "conflict", auditWorkspaceID, currentID, auditUserID, auditIP)
			return completeErrorResult(fileID, "CONFLICT", conflictMessage)
		}
		newVersion = currentVersion + 1
		committedID = currentID
		if _, err := tx.ExecContext(ctx,
			`UPDATE files
			    SET content_hash = $1, size = $2, pushed_by = $3, pushed_at = $4, version = $5
			  WHERE id = $6`,
			hash, size, pushedBy.String, pushedAt, newVersion, committedID,
		); err != nil {
			log.Printf("req=%s complete_files_update_failed err=%v", httpx.RequestID(ctx), err)
			return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
		}
	}

	// Move wrapped DEKs from pending to official.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wrapped_deks WHERE file_id = $1`,
		committedID,
	); err != nil {
		log.Printf("req=%s complete_files_dek_delete_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}
	moveRes, err := tx.ExecContext(ctx,
		`INSERT INTO wrapped_deks (file_id, user_id, encrypted_dek)
		 SELECT $1, user_id, encrypted_dek FROM pending_file_deks WHERE pending_file_id = $2`,
		committedID, fileID,
	)
	if err != nil {
		log.Printf("req=%s complete_files_dek_move_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}
	moved, err := moveRes.RowsAffected()
	if err != nil {
		log.Printf("req=%s complete_files_dek_move_rows_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}
	if moved == 0 {
		return completeErrorResult(fileID, "INVALID_REQUEST", "missing wrapped deks")
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE pending_files SET completed_file_id = $1 WHERE id = $2`,
		committedID, fileID,
	); err != nil {
		log.Printf("req=%s complete_files_mark_completed_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}

	if err := tx.Commit(); err != nil {
		log.Printf("req=%s complete_files_commit_failed err=%v", httpx.RequestID(ctx), err)
		return completeErrorResult(fileID, "INTERNAL_ERROR", "internal error")
	}

	return completeFilesResult{
		ID:     committedID,
		Status: "ok",
		File: &fileMeta{
			ID:            committedID,
			EnvironmentID: envID,
			Path:          path,
			ContentHash:   hash,
			Size:          size,
			Version:       newVersion,
			PushedBy:      pushedBy.String,
			PushedAt:      pushedAt,
		},
	}
}

func (h *Handler) completeOneWithAudit(r *http.Request, envID, fileID string) completeFilesResult {
	uid := httpx.UserID(r.Context())
	workspaceID := httpx.WorkspaceID(r.Context())

	result := h.completeOne(r.Context(), envID, fileID, workspaceID, uid, r.RemoteAddr)

	if result.Status == "ok" {
		h.recordAudit(r.Context(), "push", workspaceID, result.ID, uid, r.RemoteAddr)
	}

	return result
}

func (h *Handler) completeAlreadyDone(ctx context.Context, envID, fileID string) completeFilesResult {
	var (
		path, hash string
		size       int64
		version    int64
		pushedBy   sql.NullString
		pushedAt   time.Time
	)
	err := h.db.QueryRowContext(ctx,
		`SELECT path, content_hash, size, version, pushed_by, pushed_at
		   FROM files
		  WHERE id = $1 AND environment_id = $2`,
		fileID, envID,
	).Scan(&path, &hash, &size, &version, &pushedBy, &pushedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return completeErrorResult(fileID, "FORBIDDEN", "access denied")
		}
		log.Printf("req=%s complete_files_select_committed_failed err=%v", httpx.RequestID(ctx), err)
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
			Version:       version,
			PushedBy:      pushedBy.String,
			PushedAt:      pushedAt,
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
	uid := httpx.UserID(r.Context())
	if uid == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT f.id, f.path, f.content_hash, f.size, f.version, f.pushed_by, f.pushed_at, wd.encrypted_dek
		   FROM files f
		   JOIN wrapped_deks wd ON wd.file_id = f.id AND wd.user_id = $2
		  WHERE f.environment_id = $1
		  ORDER BY f.path ASC`,
		envID, uid,
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
			version        int64
			pushedBy       sql.NullString
			pushedAt       time.Time
			wrappedDEK     []byte // never log these bytes.
		)
		if err := rows.Scan(&id, &path, &hash, &size, &version, &pushedBy, &pushedAt, &wrappedDEK); err != nil {
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
				Version:       version,
				PushedBy:      pushedBy.String,
				PushedAt:      pushedAt,
				WrappedDEK:    wrappedDEK,
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
	uid := httpx.UserID(r.Context())
	if uid == "" {
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
		version    int64
		pushedBy   sql.NullString
		pushedAt   time.Time
		wrappedDEK []byte // never log these bytes.
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT f.path, f.content_hash, f.size, f.version, f.pushed_by, f.pushed_at, wd.encrypted_dek
		   FROM files f
		   JOIN wrapped_deks wd ON wd.file_id = f.id AND wd.user_id = $3
		  WHERE f.id = $1 AND f.environment_id = $2`,
		fileID, envID, uid,
	).Scan(&path, &hash, &size, &version, &pushedBy, &pushedAt, &wrappedDEK)
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

	h.recordAudit(r.Context(), "pull", workspaceID, fileID, uid, r.RemoteAddr)

	httpx.WriteJSON(w, http.StatusOK, listedFileMeta{
		fileMeta: fileMeta{
			ID:            fileID,
			EnvironmentID: envID,
			Path:          path,
			ContentHash:   hash,
			Size:          size,
			Version:       version,
			PushedBy:      pushedBy.String,
			PushedAt:      pushedAt,
			WrappedDEK:    wrappedDEK,
		},
		DownloadURL:       downloadURL,
		DownloadExpiresAt: time.Now().UTC().Add(storage.MaxSignedURLExpiry),
	})
}

// --- Delete File ------------------------------------------------------------

// DeleteFile deletes the file in the db.
// Blob in S3 gets cleaned up separately when no other files reference the blob.
// (Multiple files can reference the same blob if content is the same)
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
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
	fileID := r.PathValue("fileId")
	if fileID == "" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "access denied")
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

	h.recordAudit(r.Context(), "delete", workspaceID, fileID, uid, r.RemoteAddr)

	w.WriteHeader(http.StatusNoContent)
}

// --- Validation -------------------------------------------------------------

// reject "..", absolute paths, null bytes, and leading separators.
func validateFilePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if len(p) > 1024 {
		return errors.New("path too long")
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

func validateWrappedDEKs(entries []wrappedDEKEntry, uid string) error {
	if len(entries) == 0 {
		return errors.New("missing wrapped deks")
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.UserID != uid {
			return errors.New("wrapped dek recipient must be the caller")
		}
		if _, dup := seen[entry.UserID]; dup {
			return errors.New("duplicate wrapped dek recipient")
		}
		seen[entry.UserID] = struct{}{}
		if len(entry.EncryptedDEK) != wrappedDEKLen {
			return errors.New("invalid wrapped dek")
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
