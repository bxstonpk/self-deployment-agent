// Endpoints:
//
//	GET /audit-log            (FR-104: query/search, scoped per AuditService.Query)
//	GET /audit-log/export     (FR-105: CSV export of a query result set)
//	GET /audit-log/integrity  (FR-106: tamper-evidence chain verification)
package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"platform-api/internal/domain"
)

type AuditQuerier interface {
	Query(ctx context.Context, requesterID string, q domain.AuditQuery) ([]domain.AuditEntry, error)
	Export(ctx context.Context, requesterID string, q domain.AuditQuery) ([]byte, error)
	VerifyIntegrity(ctx context.Context) (ok bool, brokenAtSeq int64, err error)
}

type AuditHandler struct {
	svc AuditQuerier
}

func NewAuditHandler(svc AuditQuerier) *AuditHandler {
	return &AuditHandler{svc: svc}
}

// parseAuditQuery implements FR-104's filter set from URL query parameters:
// actor_user_id, resource_type, resource_id, action, from, to (RFC3339),
// limit.
func parseAuditQuery(r *http.Request) domain.AuditQuery {
	q := domain.AuditQuery{
		ActorUserID:  r.URL.Query().Get("actor_user_id"),
		ResourceType: r.URL.Query().Get("resource_type"),
		ResourceID:   r.URL.Query().Get("resource_id"),
		Action:       domain.AuditAction(r.URL.Query().Get("action")),
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.From = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.To = &t
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	return q
}

type auditEntryResponse struct {
	ID           string `json:"id"`
	Seq          int64  `json:"seq"`
	OccurredAt   string `json:"occurred_at"`
	ActorUserID  string `json:"actor_user_id"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail"`
	EntryHash    string `json:"entry_hash"`
}

func toAuditEntryResponse(e domain.AuditEntry) auditEntryResponse {
	return auditEntryResponse{
		ID: e.ID, Seq: e.Seq, OccurredAt: e.OccurredAt.Format(time.RFC3339), ActorUserID: e.ActorUserID,
		Action: string(e.Action), ResourceType: e.ResourceType, ResourceID: e.ResourceID,
		Outcome: string(e.Outcome), Detail: e.Detail, EntryHash: e.EntryHash,
	}
}

func (h *AuditHandler) Query(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	entries, err := h.svc.Query(r.Context(), caller.ID, parseAuditQuery(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
		return
	}
	out := make([]auditEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, toAuditEntryResponse(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (h *AuditHandler) Export(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	csvBytes, err := h.svc.Export(r.Context(), caller.ID, parseAuditQuery(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}

func (h *AuditHandler) VerifyIntegrity(w http.ResponseWriter, r *http.Request) {
	if _, ok := UserFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	ok, brokenAtSeq, err := h.svc.VerifyIntegrity(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
		return
	}
	resp := map[string]any{"intact": ok}
	if !ok {
		resp["broken_at_seq"] = brokenAtSeq
	}
	writeJSON(w, http.StatusOK, resp)
}
