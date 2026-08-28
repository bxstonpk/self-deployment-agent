// Endpoints:
//
//	POST /applications/{id}/deploy               (FR-039)
//	GET  /applications/{id}/deployments/latest   (FR-043)
//	POST /deployments/{deploymentId}/approve     (FR-042)
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type DeployHandler struct {
	svc *service.DeploymentService
}

func NewDeployHandler(svc *service.DeploymentService) *DeployHandler {
	return &DeployHandler{svc: svc}
}

type deployRequest struct {
	Environment string `json:"environment"`
}

type deploymentResponse struct {
	ID                string                             `json:"id"`
	ApplicationID     string                             `json:"application_id"`
	BuildID           string                             `json:"build_id"`
	Environment       string                             `json:"environment"`
	Status            string                             `json:"status"`
	ScanPassed        *bool                              `json:"scan_passed,omitempty"`
	ScanCriticalCount *int                               `json:"scan_critical_count,omitempty"`
	ScanHighCount     *int                               `json:"scan_high_count,omitempty"`
	ScanReports       map[string]domain.ScanReport       `json:"scan_reports,omitempty"`
	RejectionReason   string                             `json:"rejection_reason,omitempty"`
	FailureReason     string                             `json:"failure_reason,omitempty"`
	Containers        map[string]domain.RunningContainer `json:"containers,omitempty"`
	CreatedAt         string                             `json:"created_at"`
	CompletedAt       string                             `json:"completed_at,omitempty"`
}

func toDeploymentResponse(d domain.Deployment) deploymentResponse {
	resp := deploymentResponse{
		ID: d.ID, ApplicationID: d.ApplicationID, BuildID: d.BuildID,
		Environment: string(d.Environment), Status: string(d.Status),
		ScanPassed: d.ScanPassed, ScanCriticalCount: d.ScanCriticalCount, ScanHighCount: d.ScanHighCount,
		ScanReports: d.ScanReports, Containers: d.Containers,
		CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if d.RejectionReason != nil {
		resp.RejectionReason = *d.RejectionReason
	}
	if d.FailureReason != nil {
		resp.FailureReason = *d.FailureReason
	}
	if d.CompletedAt != nil {
		resp.CompletedAt = d.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

// TriggerDeploy handles POST /applications/{id}/deploy — FR-039.
func (h *DeployHandler) TriggerDeploy(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	var req deployRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
			return
		}
	}
	environment := domain.Environment(req.Environment)
	if environment == "" {
		environment = domain.EnvironmentDev
	}

	deployment, err := h.svc.InitiateDeploy(r.Context(), id, caller.ID, environment)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentResponse(deployment))
}

// LatestDeployment handles GET /applications/{id}/deployments/latest — FR-043.
func (h *DeployHandler) LatestDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	deployment, err := h.svc.LatestDeployment(r.Context(), id)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentResponse(deployment))
}

type approveRequest struct {
	Decision string `json:"decision"` // "approve" | "reject"
	Reason   string `json:"reason"`
}

// DecideApproval handles POST /deployments/{deploymentId}/approve — FR-042.
func (h *DeployHandler) DecideApproval(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	deploymentID := chi.URLParam(r, "deploymentId")

	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	var approve bool
	switch req.Decision {
	case "approve":
		approve = true
	case "reject":
		approve = false
	default:
		writeError(w, http.StatusBadRequest, "invalid_decision", `decision must be "approve" or "reject"`)
		return
	}

	deployment, err := h.svc.DecideApproval(r.Context(), deploymentID, caller.ID, approve, req.Reason)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentResponse(deployment))
}

func writeDeployError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application or deployment not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrNoSuccessfulBuild):
		writeError(w, http.StatusConflict, "no_successful_build", err.Error())
	case errors.Is(err, domain.ErrDeploymentAlreadyInFlight):
		writeError(w, http.StatusConflict, "deployment_in_flight", err.Error())
	case errors.Is(err, domain.ErrDeploymentNotPendingApproval):
		writeError(w, http.StatusConflict, "not_pending_approval", err.Error())
	case errors.Is(err, domain.ErrInvalidEnvironment):
		writeError(w, http.StatusBadRequest, "invalid_environment", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
