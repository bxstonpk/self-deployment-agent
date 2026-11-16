// IT handoff packages (DEC-033), for administrators and approvers:
//
//	GET  /handoffs                     every application's current package
//	POST /applications/{id}/handoffs   make the application's package again, of what runs in production now
//	GET  /handoffs/{packageId}/file    download a ready package (.tar); audited
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

type HandoffHandler struct {
	svc *service.HandoffService
}

func NewHandoffHandler(svc *service.HandoffService) *HandoffHandler {
	return &HandoffHandler{svc: svc}
}

func writeHandoffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only administrators and approvers can hand applications to IT")
	case errors.Is(err, domain.ErrHandoffNotFound), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application or handoff package not found")
	case errors.Is(err, domain.ErrHandoffNotReady):
		writeError(w, http.StatusConflict, "not_ready", err.Error())
	case errors.Is(err, domain.ErrHandoffInProgress):
		writeError(w, http.StatusConflict, "in_progress", err.Error())
	case errors.Is(err, domain.ErrHandoffNotProduction):
		writeError(w, http.StatusConflict, "not_in_production", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}

func toHandoffResponse(p *domain.HandoffPackage) map[string]any {
	if p == nil {
		return nil
	}
	out := map[string]any{
		"id":            p.ID,
		"deployment_id": p.DeploymentID,
		"trigger":       p.Trigger,
		"status":        p.Status,
		"created_at":    rfc3339(p.CreatedAt),
	}
	if p.RequestedBy != "" {
		out["requested_by"] = p.RequestedBy
	}
	if p.Error != "" {
		out["error"] = p.Error
	}
	if p.Status == domain.HandoffReady {
		out["file_name"], out["size_bytes"], out["sha256"] = p.FileName, p.SizeBytes, p.SHA256
	}
	if p.CompletedAt != nil {
		out["completed_at"] = rfc3339(*p.CompletedAt)
	}
	return out
}

func (h *HandoffHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	entries, err := h.svc.List(r.Context(), caller)
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"application_id":   e.ApplicationID,
			"application_name": e.ApplicationName,
			"ready":            toHandoffResponse(e.Ready),
			"latest":           toHandoffResponse(e.Latest),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoffs": out})
}

func (h *HandoffHandler) Rebuild(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	p, err := h.svc.Rebuild(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"handoff": toHandoffResponse(&p)})
}

func (h *HandoffHandler) Download(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	f, size, name, err := h.svc.Open(r.Context(), caller, chi.URLParam(r, "packageId"))
	if err != nil {
		writeHandoffError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
