package handler

import (
	"net/http"

	"github.com/kyenel64/invosit/api/internal/middleware"
)

func AddRoutes(mux *http.ServeMux, h *Handler) {
	// No Auth
	mux.HandleFunc("GET /api/v1/health", h.Health)

	// Internal
	mux.HandleFunc("POST /internal/hooks/kratos/after-registration", h.AfterRegistration)

	// Auth
	authed := middleware.RequireKratosSession(h.kratos, h.db)
	mux.Handle("GET /api/v1/auth/me", authed(http.HandlerFunc(h.Me)))

	mux.Handle("GET /api/v1/workspaces", authed(http.HandlerFunc(h.ListWorkspaces)))
	mux.Handle("POST /api/v1/workspaces", authed(http.HandlerFunc(h.CreateWorkspace)))

	// Auth + workspace verification.
	wsMember := middleware.Chain(authed, middleware.WorkspaceMember(h.db)) // Must come after authed
	mux.Handle("GET /api/v1/workspaces/{workspaceId}", wsMember(http.HandlerFunc(h.GetWorkspace)))
	mux.Handle("DELETE /api/v1/workspaces/{workspaceId}", wsMember(http.HandlerFunc(h.DeleteWorkspace)))

	mux.Handle("GET /api/v1/workspaces/{workspaceId}/environments", wsMember(http.HandlerFunc(h.ListEnvironments)))
	mux.Handle("POST /api/v1/workspaces/{workspaceId}/environments", wsMember(http.HandlerFunc(h.CreateEnvironment)))

	// Auth + workspace verification + environment verification.
	envScoped := middleware.Chain(authed, middleware.WorkspaceMember(h.db), middleware.EnvironmentScoped(h.db))
	mux.Handle("POST   /api/v1/workspaces/{workspaceId}/environments/{environmentId}/files",
		envScoped(http.HandlerFunc(h.PushFile)))
	mux.Handle("GET    /api/v1/workspaces/{workspaceId}/environments/{environmentId}/files",
		envScoped(http.HandlerFunc(h.ListFiles)))
	mux.Handle("GET    /api/v1/workspaces/{workspaceId}/environments/{environmentId}/files/{fileId}",
		envScoped(http.HandlerFunc(h.GetFile)))
	mux.Handle("DELETE /api/v1/workspaces/{workspaceId}/environments/{environmentId}/files/{fileId}",
		envScoped(http.HandlerFunc(h.DeleteFile)))
}
