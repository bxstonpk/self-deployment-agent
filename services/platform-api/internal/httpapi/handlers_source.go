// Source history: every build's kept source, for the application's owners,
// administrators and approvers to read at any time.
//
//	GET /applications/{id}/sources            the builds of the application and its dev copy, newest first
//	GET /applications/{id}/sources/{buildId}  one build's source archive (tar.gz), exactly as uploaded
package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type SourceHandler struct {
	svc *service.SourceHistoryService
}

func NewSourceHandler(svc *service.SourceHistoryService) *SourceHandler {
	return &SourceHandler{svc: svc}
}

func writeSourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application, build or kept source not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only the application's owners, administrators and approvers can read its source")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}

func (h *SourceHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	entries, err := h.svc.List(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		deployments := make([]map[string]any, 0, len(e.Deployments))
		for _, d := range e.Deployments {
			deployments = append(deployments, map[string]any{"id": d.ID, "environment": d.Environment, "status": d.Status})
		}
		item := map[string]any{
			"build_id":         e.Build.ID,
			"application_id":   e.Build.ApplicationID,
			"application_name": e.ApplicationName,
			"dev_copy":         e.DevCopy,
			"status":           e.Build.Status,
			"started_at":       rfc3339(e.Build.StartedAt),
			"triggered_by":     e.TriggeredBy,
			"kept":             e.Kept,
			"source_bytes":     e.SourceBytes,
			"deployments":      deployments,
		}
		if e.Build.CompletedAt != nil {
			item["completed_at"] = rfc3339(*e.Build.CompletedAt)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"builds": out})
}

func (h *SourceHandler) Source(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	f, size, name, err := h.svc.Source(r.Context(), caller, chi.URLParam(r, "id"), chi.URLParam(r, "buildId"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
