// Endpoints (Module O — docs/02_Functional_Requirements.md FR-066/070):
//
//	GET    /applications/{id}/secrets          names and metadata, never values
//	PUT    /applications/{id}/secrets/{name}   sets or replaces a value; write-only
//	DELETE /applications/{id}/secrets/{name}
//
// There is deliberately no endpoint that returns a stored value. FR-070's
// "never display a stored secret's plaintext value back to a human
// requester after initial submission" is enforced by that absence, not by
// a permission check that could one day be misconfigured. Values leave the
// store in exactly one direction: into a container's environment as it
// starts.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type SecretStore interface {
	List(ctx context.Context, applicationID, requesterID string) ([]domain.SecretMetadata, error)
	Set(ctx context.Context, applicationID, requesterID, name, value string) (domain.SecretMetadata, error)
	Delete(ctx context.Context, applicationID, requesterID, name string) error
}

type SecretHandler struct {
	svc SecretStore
}

func NewSecretHandler(svc SecretStore) *SecretHandler {
	return &SecretHandler{svc: svc}
}

// maxSecretRequestBytes leaves room for JSON escaping around the largest
// value domain.MaxSecretValueBytes allows.
const maxSecretRequestBytes = 4 * domain.MaxSecretValueBytes

type setSecretRequest struct {
	Value *string `json:"value"`
}

type secretResponse struct {
	Name      string  `json:"name"`
	ManagedBy string  `json:"managed_by"`
	Version   int     `json:"version"`
	UpdatedBy *string `json:"updated_by"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

func toSecretResponse(m domain.SecretMetadata) secretResponse {
	return secretResponse{
		Name: m.Name, ManagedBy: string(m.ManagedBy), Version: m.Version, UpdatedBy: m.UpdatedBy,
		CreatedAt: m.CreatedAt.Format(time.RFC3339), UpdatedAt: m.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *SecretHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	secrets, err := h.svc.List(r.Context(), chi.URLParam(r, "id"), caller.ID)
	if err != nil {
		writeSecretError(w, err)
		return
	}
	out := make([]secretResponse, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, toSecretResponse(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": out})
}

// Set handles PUT /applications/{id}/secrets/{name}. The response is the
// secret's metadata — the value is never echoed back, not even to the
// person who just sent it.
func (h *SecretHandler) Set(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSecretRequestBytes)
	var req setSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Value == nil {
		// Deliberately generic: a parse error message could quote part of
		// the body, and the body is a secret.
		writeError(w, http.StatusBadRequest, "invalid_body", `request body must be JSON of the form {"value": "..."}`)
		return
	}
	meta, err := h.svc.Set(r.Context(), chi.URLParam(r, "id"), caller.ID, chi.URLParam(r, "name"), *req.Value)
	if err != nil {
		writeSecretError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSecretResponse(meta))
}

func (h *SecretHandler) Delete(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id"), caller.ID, chi.URLParam(r, "name")); err != nil {
		writeSecretError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSecretError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrSecretNotFound):
		writeError(w, http.StatusNotFound, "secret_not_found", err.Error())
	case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrInvalidSecretName):
		writeError(w, http.StatusBadRequest, "invalid_secret_name", err.Error())
	case errors.Is(err, domain.ErrReservedSecretName):
		writeError(w, http.StatusBadRequest, "reserved_secret_name", err.Error())
	case errors.Is(err, domain.ErrInvalidSecretValue):
		writeError(w, http.StatusBadRequest, "invalid_secret_value", err.Error())
	case errors.Is(err, domain.ErrSecretManagedByPlatform):
		writeError(w, http.StatusConflict, "secret_managed_by_platform", err.Error())
	case errors.Is(err, domain.ErrInvalidLifecycleTransition):
		writeError(w, http.StatusConflict, "invalid_lifecycle_transition", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
