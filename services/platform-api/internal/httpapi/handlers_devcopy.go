// Dev copies (DEC-029 part 3):
//
//	POST   /applications/{id}/dev             the body is the source tar.gz; deploys it to the dev copy
//	GET    /applications/{id}/dev
//	DELETE /applications/{id}/dev             resets it: removes its code and data
//	POST   /applications/{id}/dev/test-data   {"note"}: Claude's record of test data it added
//
// {id} is the application, or its dev copy.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type DevCopyHandler struct {
	svc           *service.DevCopyService
	publicBaseURL string
}

func NewDevCopyHandler(svc *service.DevCopyService, publicBaseURL string) *DevCopyHandler {
	return &DevCopyHandler{svc: svc, publicBaseURL: publicBaseURL}
}

func (h *DevCopyHandler) toDevCopy(c domain.DevCopy) map[string]any {
	return map[string]any{
		"application_id": c.ApplicationID, "name": c.Name, "copy_of": c.ParentName, "copy_of_id": c.ParentID,
		"url": serviceURL(h.publicBaseURL, c.Name, "frontend", true), "created_at": rfc3339(c.CreatedAt),
		"test_data": c.TestData, "test_data_note": c.TestDataNote,
	}
}

func writeDevCopyError(w http.ResponseWriter, err error) {
	var bf *domain.BuildFailure
	switch {
	case errors.Is(err, domain.ErrNoDevCopy):
		writeError(w, http.StatusNotFound, "no_dev_copy", err.Error())
	case errors.Is(err, domain.ErrAdminHold):
		writeError(w, http.StatusConflict, "admin_hold", err.Error())
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only the application's owners can do this")
	case errors.Is(err, domain.ErrDevCopyOfDevCopy), errors.Is(err, domain.ErrDevCopyNameTooLong),
		errors.Is(err, domain.ErrDevCopyParentInactive), errors.Is(err, service.ErrNoServicesToTest),
		errors.Is(err, domain.ErrNotValidated):
		writeError(w, http.StatusConflict, "not_validated", err.Error())
	case errors.Is(err, domain.ErrInvalidLifecycleTransition):
		writeError(w, http.StatusConflict, "invalid_lifecycle_transition", err.Error())
	case errors.Is(err, domain.ErrDatabaseChangeLosesData):
		writeError(w, http.StatusConflict, "database_change_loses_data", err.Error())
	case errors.Is(err, domain.ErrInvalidYAML):
		writeError(w, http.StatusBadRequest, "invalid_yaml", err.Error())
	case errors.Is(err, domain.ErrProfileRequired):
		writeError(w, http.StatusConflict, "profile_required", err.Error())
	case errors.Is(err, domain.ErrNoSourceArchive), errors.Is(err, domain.ErrInvalidTestDataNote):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, domain.ErrBuildAlreadyInFlight), errors.Is(err, domain.ErrDeploymentAlreadyInFlight):
		writeError(w, http.StatusConflict, "in_flight", err.Error())
	case errors.As(err, &bf):
		writeError(w, http.StatusUnprocessableEntity, "build_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func (h *DevCopyHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSourceArchiveBytes)
	archive, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "source_archive_too_large", "source archive exceeds the maximum allowed size")
		return
	}
	res, err := h.svc.Deploy(r.Context(), caller, chi.URLParam(r, "id"), archive)
	if err != nil {
		writeDevCopyError(w, err)
		return
	}
	out := map[string]any{"dev_copy": h.toDevCopy(res.Copy), "created": res.Created, "build": toBuildResponse(res.Build)}
	if res.Deployment.ID != "" {
		out["deployment"] = map[string]any{
			"id": res.Deployment.ID, "status": res.Deployment.Status, "environment": res.Deployment.Environment,
			"failure_reason": res.Deployment.FailureReason,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *DevCopyHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	c, err := h.svc.Get(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writeDevCopyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dev_copy": h.toDevCopy(c)})
}

func (h *DevCopyHandler) Reset(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	c, err := h.svc.Reset(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writeDevCopyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": c.Name})
}

func (h *DevCopyHandler) RecordTestData(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	c, err := h.svc.RecordTestData(r.Context(), caller, chi.URLParam(r, "id"), req.Note)
	if err != nil {
		writeDevCopyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dev_copy": h.toDevCopy(c)})
}
