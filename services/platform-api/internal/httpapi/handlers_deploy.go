// Endpoints:
//
//	POST /applications/{id}/deploy               (FR-039)
//	GET  /applications/{id}/deployments/latest   (FR-043)
//	GET  /applications/{id}/deployments          (FR-095)
//	POST /applications/{id}/rollback             (FR-098)
//	POST /deployments/{deploymentId}/approve     (FR-042)
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type DeployHandler struct {
	svc *service.DeploymentService
	// apps and publicBaseURL turn a deployment's containers into the
	// address they are actually served on — see service_url.go.
	apps          ApplicationNamer
	publicBaseURL string
	// testRuns resolves a deployment's test_run_id to the platform's own
	// report (DEC-029 part 2). Nil: test_run_id is refused.
	testRuns TestRunReports
}

// TestRunReports is service.TestRunService's part in a deployment.
type TestRunReports interface {
	ReportForDeploy(ctx context.Context, applicationID, requesterID, runID string) (string, error)
}

// SetTestRuns lets a deployment carry a platform test run's report.
func (h *DeployHandler) SetTestRuns(t TestRunReports) { h.testRuns = t }

func NewDeployHandler(svc *service.DeploymentService, apps ApplicationNamer, publicBaseURL string) *DeployHandler {
	return &DeployHandler{svc: svc, apps: apps, publicBaseURL: publicBaseURL}
}

type deployRequest struct {
	Environment string `json:"environment"`
	// TestReport is what the requester tells the approver about the tests
	// they ran: shown to the approver and bound into what they sign.
	TestReport string `json:"test_report"`
	// ReviewDocument is the requester's REVIEW.md — workflow, structure,
	// paths, what changed — required for an environment that waits for
	// approval, shown to the approver and bound into what they sign.
	ReviewDocument string `json:"review_document"`
	// TestRunID names a passed platform test run of the same source as the
	// build being deployed; its report becomes the deployment's, with any
	// test_report given added after it as the requester's own notes.
	TestRunID string `json:"test_run_id"`
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
	UpdatedAt         string                             `json:"updated_at"`
	CompletedAt       string                             `json:"completed_at,omitempty"`
}

func toDeploymentResponse(d domain.Deployment, publicBaseURL, appName string) deploymentResponse {
	resp := deploymentResponse{
		ID: d.ID, ApplicationID: d.ApplicationID, BuildID: d.BuildID,
		Environment: string(d.Environment), Status: string(d.Status),
		ScanPassed: d.ScanPassed, ScanCriticalCount: d.ScanCriticalCount, ScanHighCount: d.ScanHighCount,
		ScanReports: d.ScanReports, Containers: withPublicURLs(d.Containers, publicBaseURL, appName),
		CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: d.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
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

// response maps one deployment for the wire, resolving the application's
// name so its services carry the stable address they're served on.
func (h *DeployHandler) response(ctx context.Context, d domain.Deployment) deploymentResponse {
	return toDeploymentResponse(d, h.publicBaseURL, applicationName(ctx, h.apps, d.ApplicationID))
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

	if req.TestRunID != "" {
		if h.testRuns == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "this platform doesn't run tests")
			return
		}
		report, err := h.testRuns.ReportForDeploy(r.Context(), id, caller.ID, req.TestRunID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "no such test run for this application")
			return
		case errors.Is(err, domain.ErrUnauthorized):
			writeError(w, http.StatusForbidden, "forbidden", err.Error())
			return
		case errors.Is(err, domain.ErrTestRunNotPassed), errors.Is(err, domain.ErrTestRunSourceMismatch):
			writeError(w, http.StatusConflict, "test_run_not_usable", err.Error())
			return
		case errors.Is(err, domain.ErrNoSuccessfulBuild):
			writeDeployError(w, err)
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if notes := strings.TrimSpace(req.TestReport); notes != "" {
			report += "\n" + domain.RequesterTestReportHeader + "\n" + notes + "\n"
		}
		req.TestReport = report
	} else if notes := strings.TrimSpace(req.TestReport); notes != "" {
		// Marked as the requester's, so no report can pass itself off as
		// one the platform ran: only a test run's starts with its header.
		req.TestReport = domain.RequesterTestReportHeader + "\n" + notes + "\n"
	}
	if len(req.TestReport) > domain.MaxTestReportBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "test_report_too_large", domain.ErrTestReportTooLarge.Error())
		return
	}
	if len(req.ReviewDocument) > domain.MaxReviewDocumentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "review_document_too_large", domain.ErrReviewDocumentTooLarge.Error())
		return
	}
	ctx := domain.WithReviewDocument(domain.WithTestReport(r.Context(), req.TestReport), req.ReviewDocument)
	deployment, err := h.svc.InitiateDeploy(ctx, id, caller.ID, environment)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.response(r.Context(), deployment))
}

// LatestDeployment handles GET /applications/{id}/deployments/latest — FR-043.
func (h *DeployHandler) LatestDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	deployment, err := h.svc.LatestDeployment(r.Context(), id)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.response(r.Context(), deployment))
}

// GetDeployment handles GET /deployments/{deploymentId} — the deployment_id
// -keyed lookup docs/07_MCP_Requirements.md Section 13.8's
// get_deployment_status tool polls by.
func (h *DeployHandler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID := chi.URLParam(r, "deploymentId")
	deployment, err := h.svc.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.response(r.Context(), deployment))
}

// DeploymentHistory handles GET /applications/{id}/deployments — FR-095:
// every deployment ever attempted for the application, newest first, which
// is what a caller picks a Rollback target from.
func (h *DeployHandler) DeploymentHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	history, err := h.svc.DeploymentHistory(r.Context(), id)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	resp := make([]deploymentResponse, len(history))
	name := applicationName(r.Context(), h.apps, chi.URLParam(r, "id"))
	for i, d := range history {
		resp[i] = toDeploymentResponse(d, h.publicBaseURL, name)
	}
	writeJSON(w, http.StatusOK, resp)
}

type rollbackRequest struct {
	TargetDeploymentID string `json:"target_deployment_id"`
}

// Rollback handles POST /applications/{id}/rollback — FR-098.
func (h *DeployHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if req.TargetDeploymentID == "" {
		writeError(w, http.StatusBadRequest, "missing_target", "target_deployment_id is required")
		return
	}

	deployment, err := h.svc.Rollback(r.Context(), id, caller.ID, req.TargetDeploymentID)
	if err != nil {
		writeDeployError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.response(r.Context(), deployment))
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
	writeJSON(w, http.StatusOK, h.response(r.Context(), deployment))
}

func writeDeployError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrPreviewOnlyEnvironment):
		writeError(w, http.StatusBadRequest, "invalid_environment", err.Error())
	case errors.Is(err, domain.ErrReviewDocumentRequired):
		writeError(w, http.StatusBadRequest, "review_document_required", err.Error())
	// FR-067's exception flow surfaced, not swallowed: the application was
	// deliberately not started without its secrets, and the owner should be
	// told why. The message names the secret and the cause (e.g. a changed
	// platform key) — never a value.
	case errors.Is(err, domain.ErrSecretUnreadable), errors.Is(err, domain.ErrSecretNotFound):
		writeError(w, http.StatusInternalServerError, "secret_unavailable", err.Error())
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
	case errors.Is(err, domain.ErrSignedApprovalRequired):
		writeError(w, http.StatusForbidden, "signed_approval_required", err.Error())
	case errors.Is(err, domain.ErrInvalidEnvironment):
		writeError(w, http.StatusBadRequest, "invalid_environment", err.Error())
	case errors.Is(err, domain.ErrInvalidLifecycleTransition):
		writeError(w, http.StatusConflict, "invalid_lifecycle_transition", err.Error())
	case errors.Is(err, domain.ErrInvalidRollbackTarget):
		writeError(w, http.StatusConflict, "invalid_rollback_target", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}
