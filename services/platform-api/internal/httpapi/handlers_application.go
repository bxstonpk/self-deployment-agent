// Package httpapi is the Business API surface described in
// docs/13_API_Requirements.md. This file covers the Draft-state endpoints:
//
//	POST   /applications
//	GET    /applications
//	GET    /applications/{id}
//	PATCH  /applications/{id}
//	GET    /applications/{id}/owners
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type ApplicationHandler struct {
	svc *service.ApplicationService
}

func NewApplicationHandler(svc *service.ApplicationService) *ApplicationHandler {
	return &ApplicationHandler{svc: svc}
}

type registerApplicationRequest struct {
	Name                string `json:"name"`
	Description         string `json:"description"`
	OwningDepartmentID  string `json:"owning_department_id"`
	DeploymentYAMLDraft string `json:"deployment_yaml_draft"`
}

type applicationResponse struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	OwningDepartmentID  string `json:"owning_department_id"`
	CreatedBy           string `json:"created_by"`
	LifecycleStatus     string `json:"lifecycle_status"`
	DeploymentYAMLDraft string `json:"deployment_yaml_draft,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

func toApplicationResponse(a domain.Application) applicationResponse {
	return applicationResponse{
		ID:                  a.ID,
		Name:                a.Name,
		Description:         a.Description,
		OwningDepartmentID:  a.OwningDepartmentID,
		CreatedBy:           a.CreatedBy,
		LifecycleStatus:     string(a.LifecycleStatus),
		DeploymentYAMLDraft: a.DeploymentYAMLDraft,
		CreatedAt:           a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:           a.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// Register handles POST /applications — FR-011.
func (h *ApplicationHandler) Register(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}

	var req registerApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if req.Name == "" || req.OwningDepartmentID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "name and owning_department_id are required")
		return
	}

	app, err := h.svc.Register(r.Context(), service.RegisterApplicationInput{
		Name:                req.Name,
		Description:         req.Description,
		OwningDepartmentID:  req.OwningDepartmentID,
		DeploymentYAMLDraft: req.DeploymentYAMLDraft,
		RegisteredBy:        caller,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toApplicationResponse(app))
}

// List handles GET /applications.
func (h *ApplicationHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	apps, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list applications")
		return
	}
	out := make([]applicationResponse, 0, len(apps))
	for _, a := range apps {
		out = append(out, toApplicationResponse(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

// Get handles GET /applications/{id}.
func (h *ApplicationHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	app, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
}

type updateMetadataRequest struct {
	Description string `json:"description"`
}

// UpdateMetadata handles PATCH /applications/{id} — FR-013.
func (h *ApplicationHandler) UpdateMetadata(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	var req updateMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	app, err := h.svc.UpdateMetadata(r.Context(), id, caller.ID, req.Description)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
}

type ownerResponse struct {
	UserID        string `json:"user_id"`
	OwnershipRole string `json:"ownership_role"`
	Status        string `json:"status"`
	AssignedAt    string `json:"assigned_at"`
}

// ListOwners handles GET /applications/{id}/owners — supports FR-015's
// acceptance criterion that ownership is queryable at all times.
func (h *ApplicationHandler) ListOwners(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	owners, err := h.svc.ListOwners(r.Context(), id)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	out := make([]ownerResponse, 0, len(owners))
	for _, o := range owners {
		out = append(out, ownerResponse{
			UserID:        o.UserID,
			OwnershipRole: string(o.OwnershipRole),
			Status:        o.Status,
			AssignedAt:    o.AssignedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"owners": out})
}

func writeApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, domain.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name", err.Error())
	case errors.Is(err, domain.ErrDepartmentUnknown):
		writeError(w, http.StatusBadRequest, "unknown_department", err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
