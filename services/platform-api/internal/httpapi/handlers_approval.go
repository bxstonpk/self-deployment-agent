// Signed approvals (DEC-017) — for platform administrators holding an
// approver key; see internal/domain/approval_signing.go.
//
//	GET  /approvals/pending                     what is waiting
//	GET  /approvals/{deploymentId}              one request, and the text to sign
//	POST /approvals/{deploymentId}/decision     {decision, reason, signed_at, signature}
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type ApprovalHandler struct {
	svc     *service.ApprovalService
	deploys *DeployHandler
}

func NewApprovalHandler(svc *service.ApprovalService, deploys *DeployHandler) *ApprovalHandler {
	return &ApprovalHandler{svc: svc, deploys: deploys}
}

func writeApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotAnApprover):
		writeError(w, http.StatusForbidden, "not_an_approver", err.Error())
	case errors.Is(err, domain.ErrApprovalSignature):
		writeError(w, http.StatusForbidden, "invalid_signature", err.Error())
	case errors.Is(err, domain.ErrApprovalSignatureStale):
		writeError(w, http.StatusForbidden, "stale_signature", err.Error())
	case errors.Is(err, domain.ErrDeploymentNotPendingApproval):
		writeError(w, http.StatusConflict, "not_pending_approval", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no approval request for this deployment")
	default:
		writeError(w, http.StatusBadRequest, "approval_failed", err.Error())
	}
}

func toApprovalRequest(r service.ApprovalRequest) map[string]any {
	scans := map[string]any{}
	for svc, rep := range r.ScanReports {
		scans[svc] = map[string]any{"passed": rep.Passed, "critical": rep.CriticalCount, "high": rep.HighCount}
	}
	return map[string]any{
		"deployment_id":      r.Subject.DeploymentID,
		"application_id":     r.Subject.ApplicationID,
		"application_name":   r.Subject.ApplicationName,
		"environment":        r.Subject.Environment,
		"build_id":           r.Subject.BuildID,
		"images":             r.Subject.ImageRefs,
		"requested_by":       r.RequestedBy,
		"requested_at":       r.RequestedAt.UTC().Format(time.RFC3339),
		"status":             r.Status,
		"decision":           r.Decision,
		"scans":              scans,
		"test_report":        r.Subject.TestReport,
		"dev_data":           r.Subject.DevData,
		"test_report_sha256": domain.TestReportSHA256(r.Subject.TestReport),
		"review_document":    r.Subject.ReviewDocument,
		"review_sha256":      domain.TestReportSHA256(r.Subject.ReviewDocument),
		"source_sha256":      r.Subject.SourceSHA256,
		"source_bytes":       r.SourceBytes,
		"signing_template":   r.SigningTemplate,
	}
}

func (h *ApprovalHandler) Pending(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	pending, err := h.svc.Pending(r.Context(), caller)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(pending))
	for _, p := range pending {
		out = append(out, toApprovalRequest(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": out})
}

func (h *ApprovalHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	req, err := h.svc.Request(r.Context(), caller, chi.URLParam(r, "deploymentId"))
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toApprovalRequest(req))
}

// Source is GET /approvals/{deploymentId}/source: the exact source archive
// the build was made from, for the approver to review before signing. Its
// SHA-256 is in the request and in what they sign.
func (h *ApprovalHandler) Source(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	f, size, name, err := h.svc.Source(r.Context(), caller, chi.URLParam(r, "deploymentId"))
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func (h *ApprovalHandler) Decide(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, domain.ErrInvalidToken)
		return
	}
	var body struct {
		Decision  string `json:"decision"`
		Reason    string `json:"reason"`
		SignedAt  string `json:"signed_at"`
		Signature string `json:"signature"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be JSON")
		return
	}
	deployment, fingerprint, err := h.svc.Decide(r.Context(), caller, chi.URLParam(r, "deploymentId"),
		body.Decision, body.Reason, body.SignedAt, body.Signature)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deployment":         h.deploys.response(r.Context(), deployment),
		"signer_fingerprint": fingerprint,
	})
}
