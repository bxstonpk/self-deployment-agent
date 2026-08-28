// Endpoints:
//
//	POST /applications/{id}/suspend  (FR-047)
//	POST /applications/{id}/resume   (FR-048)
//	POST /applications/{id}/restart  (FR-048)
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type LifecycleActor interface {
	Suspend(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
	Resume(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
	Restart(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
}

type LifecycleHandler struct {
	svc LifecycleActor
}

func NewLifecycleHandler(svc LifecycleActor) *LifecycleHandler {
	return &LifecycleHandler{svc: svc}
}

func (h *LifecycleHandler) Suspend(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.svc.Suspend)
}

func (h *LifecycleHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.svc.Resume)
}

func (h *LifecycleHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.svc.Restart)
}

func (h *LifecycleHandler) act(w http.ResponseWriter, r *http.Request, action func(context.Context, string, string) (domain.Deployment, error)) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	deployment, err := action(r.Context(), id, caller.ID)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentResponse(deployment))
}

func writeLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrApplicationNotRunning):
		writeError(w, http.StatusConflict, "not_running", err.Error())
	case errors.Is(err, domain.ErrApplicationNotSuspended):
		writeError(w, http.StatusConflict, "not_suspended", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
