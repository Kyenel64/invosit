package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

type addMemberRequest struct {
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role"  validate:"required,oneof=admin member viewer no_access"`
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	workspaceID := httpx.WorkspaceID(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT m.user_id, u.email, m.role, m.joined_at, m.expires_at
		FROM workspace_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1
		ORDER BY m.joined_at ASC`,
		workspaceID,
	)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	members := []map[string]any{}
	for rows.Next() {
		var (
			userID, email, role string
			joinedAt            time.Time
			expiresAt           *time.Time
		)
		if err := rows.Scan(&userID, &email, &role, &joinedAt, &expiresAt); err != nil {
			httpx.InternalError(w, r, err)
			return
		}
		member := map[string]any{
			"user_id":   userID,
			"email":     email,
			"role":      role,
			"joined_at": joinedAt,
		}
		if expiresAt != nil {
			member["expires_at"] = *expiresAt
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	workspaceID := httpx.WorkspaceID(r.Context())
	role := httpx.WorkspaceRole(r.Context())

	if role != "admin" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}

	var req addMemberRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid email or role")
		return
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(req.Email))
	if normalizedEmail == "" {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid email or role")
		return
	}

	var userID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM users WHERE LOWER(email) = $1`,
		normalizedEmail,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.RespondError(w, http.StatusNotFound, "USER_NOT_FOUND", "no user with that email")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	joinedAt := time.Now().UTC()
	_, err = h.db.ExecContext(r.Context(),
		`INSERT INTO workspace_members(workspace_id, user_id, role, joined_at)
		VALUES($1, $2, $3, $4)`,
		workspaceID, userID, req.Role, joinedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			httpx.RespondError(w, http.StatusConflict, "CONFLICT", "member already exists")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user_id":   userID,
		"email":     normalizedEmail,
		"role":      req.Role,
		"joined_at": joinedAt,
	})
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	workspaceID := httpx.WorkspaceID(r.Context())
	role := httpx.WorkspaceRole(r.Context())
	userIDToRemove := r.PathValue("userId")

	if role != "admin" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}

	if userIDToRemove == "" {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "user ID required")
		return
	}

	transaction, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = transaction.Rollback() }()

	var adminCount int
	err = transaction.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM workspace_members
		WHERE workspace_id = $1 AND role = 'admin'`,
		workspaceID,
	).Scan(&adminCount)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	var targetRole string
	err = transaction.QueryRowContext(r.Context(),
		`SELECT role FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userIDToRemove,
	).Scan(&targetRole)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.RespondError(w, http.StatusNotFound, "NOT_FOUND", "member not found")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	if targetRole == "admin" && adminCount <= 1 {
		httpx.RespondError(w, http.StatusConflict, "LAST_ADMIN", "cannot remove the last admin")
		return
	}

	res, err := transaction.ExecContext(r.Context(),
		`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userIDToRemove,
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
		httpx.RespondError(w, http.StatusNotFound, "NOT_FOUND", "member not found")
		return
	}

	if err := transaction.Commit(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
