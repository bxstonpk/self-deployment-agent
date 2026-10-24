// Previews (DEC-029 part 3):
//
//	POST   /applications/{id}/preview   the body is the source tar.gz; starts (or replaces) the preview
//	GET    /applications/{id}/preview
//	DELETE /applications/{id}/preview   tears it down now
package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type PreviewHandler struct {
	svc           *service.PreviewService
	publicBaseURL string
}

func NewPreviewHandler(svc *service.PreviewService, publicBaseURL string) *PreviewHandler {
	return &PreviewHandler{svc: svc, publicBaseURL: publicBaseURL}
}

func (h *PreviewHandler) toPreview(p domain.Preview) map[string]any {
	return map[string]any{
		"application_id": p.ApplicationID, "name": p.Name, "previews": p.ParentName, "previews_id": p.ParentID,
		"url": serviceURL(h.publicBaseURL, p.Name, "frontend", true), "created_at": rfc3339(p.CreatedAt),
		"expires_at": rfc3339(p.ExpiresAt),
	}
}

func writePreviewError(w http.ResponseWriter, err error) {
	var bf *domain.BuildFailure
	switch {
	case errors.Is(err, domain.ErrNoPreview):
		writeError(w, http.StatusNotFound, "no_preview", err.Error())
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only the application's owners can preview it")
	case errors.Is(err, domain.ErrPreviewOfPreview), errors.Is(err, domain.ErrPreviewNameTooLong),
		errors.Is(err, domain.ErrPreviewParentInactive), errors.Is(err, service.ErrNoServicesToTest),
		errors.Is(err, domain.ErrNotValidated):
		writeError(w, http.StatusConflict, "not_validated", err.Error())
	case errors.Is(err, domain.ErrProfileRequired):
		writeError(w, http.StatusConflict, "profile_required", err.Error())
	case errors.Is(err, domain.ErrNoSourceArchive):
		writeError(w, http.StatusBadRequest, "no_source_archive", err.Error())
	case errors.As(err, &bf):
		writeError(w, http.StatusUnprocessableEntity, "build_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func (h *PreviewHandler) Start(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.svc.Start(r.Context(), caller, chi.URLParam(r, "id"), archive)
	if err != nil {
		writePreviewError(w, err)
		return
	}
	out := map[string]any{"preview": h.toPreview(res.Preview), "build": toBuildResponse(res.Build)}
	if res.Deployment.ID != "" {
		out["deployment"] = map[string]any{
			"id": res.Deployment.ID, "status": res.Deployment.Status, "environment": res.Deployment.Environment,
			"failure_reason": res.Deployment.FailureReason,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *PreviewHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	p, err := h.svc.Get(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writePreviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": h.toPreview(p)})
}

func (h *PreviewHandler) Stop(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	p, err := h.svc.Stop(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writePreviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": p.Name})
}
