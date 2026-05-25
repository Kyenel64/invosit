package handler

import (
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

type pushFileRequest struct {
	Path        string `json:"path"         validate:"required,max=1024"`
	ContentHash string `json:"content_hash" validate:"required,len=64"`
	Size        int64  `json:"size"         validate:"required,gt=0"`
}

func (h *Handler) PushFile(w http.ResponseWriter, r *http.Request) {
	// These httpx values are set through middleware
	uid := httpx.UserID(r.Context())
	if uid == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())
	envID := httpx.EnvironmentID(r.Context())
	role := httpx.WorkspaceRole(r.Context())
	if role == "viewer" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "write permission required")
		return
	}

	var req pushFileRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid push request")
		return
	}
	path := strings.TrimSpace(req.Path)
	if err := validateFilePath(path); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid path")
		return
	}
	if err := validateSha256Hex(req.ContentHash); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid content hash")
		return
	}

	blobKey := workspaceID + "/" + req.ContentHash
	pushedAt := time.Now().UTC()
	fileID := ids.File()

	transaction, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = transaction.Rollback() }()

	// Capture the prior content_hash (if any)
	var priorHash sql.NullString
	if err := transaction.QueryRowContext(r.Context(),
		`SELECT content_hash FROM files WHERE environment_id = $1 AND path = $2 FOR UPDATE`,
		envID, path,
	).Scan(&priorHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.InternalError(w, r, err)
		return
	}

	// Upsert the files row. On conflict (env_id, path) the existing row is
	// updated to point at the new content.
	err = transaction.QueryRowContext(r.Context(),
		`INSERT INTO files (id, workspace_id, environment_id, path, content_hash, size, pushed_by, pushed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (environment_id, path) DO UPDATE
		   SET content_hash = EXCLUDED.content_hash,
		       size         = EXCLUDED.size,
		       pushed_by    = EXCLUDED.pushed_by,
		       pushed_at    = EXCLUDED.pushed_at
		 RETURNING id`,
		fileID, workspaceID, envID, path, req.ContentHash, req.Size, uid, pushedAt,
	).Scan(&fileID)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	// Presign before committing. If signing fails (storage outage, mis-config),
	// the deferred Rollback discards the upsert so a subsequent pull doesn't
	// point at a blob the client was never given a chance to upload.
	uploadURL, err := h.blobs.SignedPutURL(r.Context(), blobKey, storage.MaxSignedURLExpiry)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	if err := transaction.Commit(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	// Best-effort delete of the orphaned prior blob.
	// Skip when the hash is unchanged (same blob)
	if priorHash.Valid && priorHash.String != req.ContentHash {
		priorKey := workspaceID + "/" + priorHash.String
		if err := h.blobs.Delete(r.Context(), priorKey); err != nil {
			log.Printf("req=%s prior_blob_delete_failed key=%q err=%v",
				httpx.RequestID(r.Context()), priorKey, err)
		}
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":                fileID,
		"environment_id":    envID,
		"path":              path,
		"content_hash":      req.ContentHash,
		"size":              req.Size,
		"pushed_by":         uid,
		"pushed_at":         pushedAt,
		"upload_url":        uploadURL,
		"upload_expires_at": pushedAt.Add(storage.MaxSignedURLExpiry),
	})
}

// ListFiles returns the current state of every file in the environment.
// The middleware chain has already confirmed env membership.
func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	envID := httpx.EnvironmentID(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, path, content_hash, size, pushed_by, pushed_at
		   FROM files
		  WHERE environment_id = $1
		  ORDER BY path ASC`,
		envID,
	)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	files := []map[string]any{}
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
		files = append(files, map[string]any{
			"id":           id,
			"path":         path,
			"content_hash": hash,
			"size":         size,
			"pushed_by":    pushedBy.String,
			"pushed_at":    pushedAt,
		})
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": files})
}

// GetFile returns metadata plus a short-lived signed GET URL for the
// current version's blob. Membership is already verified by middleware
func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request) {
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
		  WHERE id = $1 AND environment_id = $2`,
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

	blobKey := workspaceID + "/" + hash
	downloadURL, err := h.blobs.SignedGetURL(r.Context(), blobKey, storage.MaxSignedURLExpiry)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":                  fileID,
		"environment_id":      envID,
		"path":                path,
		"content_hash":        hash,
		"size":                size,
		"pushed_by":           pushedBy.String,
		"pushed_at":           pushedAt,
		"download_url":        downloadURL,
		"download_expires_at": time.Now().UTC().Add(storage.MaxSignedURLExpiry),
	})
}

// DeleteFile removes the files row (cascades to wrapped_deks) and
// best-effort deletes the orphaned blob.
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
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
