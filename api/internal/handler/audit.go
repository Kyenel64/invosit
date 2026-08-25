package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

const (
	auditLogDefaultLimit = 50
	auditLogMaxLimit     = 200
)

type auditLogEntry struct {
	ID          string     `json:"id"`
	UserID      *string    `json:"user_id"`
	WorkspaceID string     `json:"workspace_id"`
	Action      string     `json:"action"`
	FileID      *string    `json:"file_id"`
	IP          *string    `json:"ip"`
	Timestamp   time.Time  `json:"timestamp"`
}

type listAuditLogsResponse struct {
	Logs []auditLogEntry `json:"logs"`
}

func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if httpx.UserID(r.Context()) == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}
	workspaceID := httpx.WorkspaceID(r.Context())

	limit := auditLogDefaultLimit
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid limit parameter")
			return
		}
		limit = parsedLimit
		if limit > auditLogMaxLimit {
			limit = auditLogMaxLimit
		}
	}

	var beforeTime time.Time
	if beforeStr := r.URL.Query().Get("before"); beforeStr != "" {
		parsed, err := time.Parse(time.RFC3339, beforeStr)
		if err != nil {
			httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid before parameter")
			return
		}
		beforeTime = parsed
	}

	query := `SELECT id, user_id, workspace_id, action, file_id, ip, timestamp
	          FROM audit_logs
	          WHERE workspace_id = $1`
	args := []any{workspaceID}

	if !beforeTime.IsZero() {
		query += ` AND timestamp < $2`
		args = append(args, beforeTime)
	}

	query += ` ORDER BY timestamp DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	logs := []auditLogEntry{}
	for rows.Next() {
		var entry auditLogEntry
		var userID, fileID, ip *string
		if err := rows.Scan(&entry.ID, &userID, &entry.WorkspaceID, &entry.Action, &fileID, &ip, &entry.Timestamp); err != nil {
			httpx.InternalError(w, r, err)
			return
		}
		entry.UserID = userID
		entry.FileID = fileID
		entry.IP = ip
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, listAuditLogsResponse{Logs: logs})
}
