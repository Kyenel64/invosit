package handler

import (
	"encoding/base64"
	"net/http"

	"github.com/kyenel64/invosit/api/internal/httpx"
)

type registerPublicKeyRequest struct {
	PublicKey []byte `json:"public_key" validate:"required,len=32"`
}

// RegisterPublicKey stores the caller's x25519 public key on their users row.
// Re-uploading the same key is a no-op; a different key is rejected because
// silently replacing it would orphan every existing wrapped DEK.
func (h *Handler) RegisterPublicKey(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserID(r.Context())
	if uid == "" {
		httpx.RespondError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return
	}

	var req registerPublicKeyRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid public key")
		return
	}

	encoded := base64.StdEncoding.EncodeToString(req.PublicKey)
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET public_key = $2 WHERE id = $1 AND (public_key IS NULL OR public_key = $2)`,
		uid, encoded,
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
		httpx.RespondError(w, http.StatusConflict, "CONFLICT", "a different public key is already registered")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
