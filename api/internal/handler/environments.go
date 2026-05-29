package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/kyenel64/invosit/api/internal/httpx"
	"github.com/kyenel64/invosit/api/internal/ids"
)

type createEnvironmentRequest struct {
	Name string `json:"name" validate:"required,min=1,max=64"`
}

// Creates a new environment in the workspace. Admin only
func (h *Handler) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	workspaceID := httpx.WorkspaceID(r.Context())
	role := httpx.WorkspaceRole(r.Context())

	if role != "admin" {
		httpx.RespondError(w, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}

	var req createEnvironmentRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid environment name")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid environment name")
		return
	}
	// env_ is reserved for ids so a name can't shadow an id in the file path.
	if strings.HasPrefix(strings.ToLower(name), "env_") {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid environment name")
		return
	}

	envID := ids.Environment()
	createdAt := time.Now().UTC()

	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO environments (id, workspace_id, name, created_at) VALUES ($1, $2, $3, $4)`,
		envID, workspaceID, name, createdAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			httpx.RespondError(w, http.StatusConflict, "CONFLICT", "environment with that name already exists")
			return
		}
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":           envID,
		"workspace_id": workspaceID,
		"name":         name,
		"created_at":   createdAt,
	})
}

func (h *Handler) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	workspaceID := httpx.WorkspaceID(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, name, created_at FROM environments
		 WHERE workspace_id = $1 ORDER BY created_at ASC`,
		workspaceID,
	)
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	envs := []map[string]any{}
	for rows.Next() {
		var (
			id, name  string
			createdAt time.Time
		)
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			httpx.InternalError(w, r, err)
			return
		}
		envs = append(envs, map[string]any{
			"id":         id,
			"name":       name,
			"created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"environments": envs})
}

// isUniqueViolation matches Postgres SQLSTATE 23505 without pulling pgx
// type assertions into the handler layer. The driver returns errors whose
// message embeds the SQLSTATE code; checking the substring keeps this
// driver-agnostic across pgx and lib/pq.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "23505")
}
