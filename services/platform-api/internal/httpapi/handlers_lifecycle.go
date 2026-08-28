// Endpoints:
//
//	POST /applications/{id}/suspend  (FR-047)
//	POST /applications/{id}/resume   (FR-048)
//	POST /applications/{id}/restart  (FR-048)
//	POST /applications/{id}/archive  (FR-049)
//	POST /applications/{id}/delete   (FR-050)
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type LifecycleActor interface {
	Suspend(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
	Resume(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
	Restart(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
	Archive(ctx context.Context, applicationID, requesterID string) (domain.Application, error)
	Delete(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error)
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

func (h *LifecycleHandler) Archive(w http.ResponseWriter, r *http.Request) {
	h.actApp(w, r, h.svc.Archive)
}

type deleteRequest struct {
	Confirm bool `json:"confirm"`
}

// Delete handles POST /applications/{id}/delete — FR-050. Unlike the other
// lifecycle actions, this reads a body: FR-050's main flow requires the
// requester to explicitly confirm, acknowledging irreversibility, not just
// hit an endpoint.
func (h *LifecycleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	var req deleteRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
			return
		}
	}

	app, err := h.svc.Delete(r.Context(), id, caller.ID, req.Confirm)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
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

func (h *LifecycleHandler) actApp(w http.ResponseWriter, r *http.Request, action func(context.Context, string, string) (domain.Application, error)) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	app, err := action(r.Context(), id, caller.ID)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApplicationResponse(app))
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
	case errors.Is(err, domain.ErrInvalidLifecycleTransition):
		writeError(w, http.StatusConflict, "invalid_lifecycle_transition", err.Error())
	case errors.Is(err, domain.ErrDeploymentAlreadyInFlight):
		writeError(w, http.StatusConflict, "deployment_in_flight", err.Error())
	case errors.Is(err, domain.ErrDeleteNotConfirmed):
		writeError(w, http.StatusBadRequest, "confirmation_required", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
