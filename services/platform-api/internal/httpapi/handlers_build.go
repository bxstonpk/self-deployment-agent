// Endpoints:
//
//	POST /applications/{id}/build          (FR-035)
//	GET  /applications/{id}/builds/latest  (FR-036)
package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// maxSourceArchiveBytes bounds the uploaded build context. 50MiB is
// generous for a small internal tool's source (no node_modules/vendor
// directories expected — those should be excluded client-side) while
// keeping a single request from exhausting server memory, since the whole
// archive is buffered before being handed to the build engine.
const maxSourceArchiveBytes = 50 * 1024 * 1024

type BuildHandler struct {
	svc *service.BuildService
}

func NewBuildHandler(svc *service.BuildService) *BuildHandler {
	return &BuildHandler{svc: svc}
}

type buildResponse struct {
	ID            string            `json:"id"`
	ApplicationID string            `json:"application_id"`
	Status        string            `json:"status"`
	ErrorCategory string            `json:"error_category,omitempty"`
	ErrorDetail   string            `json:"error_detail,omitempty"`
	ImageRefs     map[string]string `json:"image_refs,omitempty"`
	StartedAt     string            `json:"started_at"`
	CompletedAt   string            `json:"completed_at,omitempty"`
}

func toBuildResponse(b domain.Build) buildResponse {
	resp := buildResponse{
		ID: b.ID, ApplicationID: b.ApplicationID, Status: string(b.Status),
		ImageRefs: b.ImageRefs, StartedAt: b.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if b.ErrorCategory != nil {
		resp.ErrorCategory = string(*b.ErrorCategory)
	}
	if b.ErrorDetail != nil {
		resp.ErrorDetail = *b.ErrorDetail
	}
	if b.CompletedAt != nil {
		resp.CompletedAt = b.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

// TriggerBuild handles POST /applications/{id}/build — FR-035. The request
// body IS the source archive: a gzip-compressed tar with a top-level
// directory per declared service name (see build_service.go package doc).
func (h *BuildHandler) TriggerBuild(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, maxSourceArchiveBytes)
	archive, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "source_archive_too_large", "source archive exceeds the maximum allowed size")
		return
	}

	build, err := h.svc.TriggerBuild(r.Context(), id, caller.ID, archive)
	if err != nil {
		writeBuildError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBuildResponse(build))
}

// LatestBuild handles GET /applications/{id}/builds/latest — FR-036.
func (h *BuildHandler) LatestBuild(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	build, err := h.svc.LatestBuild(r.Context(), id)
	if err != nil {
		writeBuildError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBuildResponse(build))
}

func writeBuildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application or build not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrNotValidated):
		writeError(w, http.StatusConflict, "not_validated", err.Error())
	case errors.Is(err, domain.ErrNoSourceArchive):
		writeError(w, http.StatusBadRequest, "no_source_archive", err.Error())
	case errors.Is(err, domain.ErrBuildAlreadyInFlight):
		writeError(w, http.StatusConflict, "build_in_flight", err.Error())
	case errors.Is(err, domain.ErrNoBaseImageForRuntime):
		writeError(w, http.StatusUnprocessableEntity, "no_base_image", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
