// Endpoints:
//
//	PUT  /applications/{id}/deployment-yaml   (FR-023)
//	POST /applications/{id}/validate          (FR-029..034)
//	GET  /supported-stacks                    (FR-019, mirrors MCP get_supported_stacks)
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/service"
)

type ValidationHandler struct {
	svc *service.ValidationService
}

func NewValidationHandler(svc *service.ValidationService) *ValidationHandler {
	return &ValidationHandler{svc: svc}
}

type saveDeploymentYAMLRequest struct {
	DeploymentYAML string `json:"deployment_yaml"`
}

// SaveDeploymentYAML handles PUT /applications/{id}/deployment-yaml — FR-023.
func (h *ValidationHandler) SaveDeploymentYAML(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	var req saveDeploymentYAMLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	app, err := h.svc.SaveDeploymentYAML(r.Context(), id, caller.ID, req.DeploymentYAML)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
}

// Validate handles POST /applications/{id}/validate — FR-029..034.
func (h *ValidationHandler) Validate(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	report, app, err := h.svc.Validate(r.Context(), id, caller.ID)
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	status := http.StatusOK
	writeJSON(w, status, map[string]any{
		"application": toApplicationResponse(app),
		"report":      report,
	})
}

type StackHandler struct {
	repo service.StackRepository
}

func NewStackHandler(repo service.StackRepository) *StackHandler {
	return &StackHandler{repo: repo}
}

// List handles GET /supported-stacks — FR-019's queryable-catalog
// acceptance criterion, at the Business API level (the MCP tool
// get_supported_stacks, docs/07_MCP_Requirements.md, calls through to this
// same capability rather than reading the catalog directly).
func (h *StackHandler) List(w http.ResponseWriter, r *http.Request) {
	stacks, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list supported stacks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stacks": stacks})
}
