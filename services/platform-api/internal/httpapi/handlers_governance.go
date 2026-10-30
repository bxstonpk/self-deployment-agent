// Development governance: the company standard from the moment an
// application is started (see internal/service/governance_service.go).
//
//	GET    /standards                              the standard: rules, stacks, dependency rules
//	POST   /standards/dependency-rules             administrators: add a library the company doesn't allow
//	DELETE /standards/dependency-rules/{id}        administrators: remove one
//	GET    /standards/exceptions?status=pending    administrators: the exception queue (or history)
//	POST   /standards/exceptions/{id}/decision     administrators: approve or reject
//	PUT    /applications/{id}/profile              owners: what the application is for (start_project)
//	GET    /applications/{id}/profile
//	POST   /applications/{id}/exceptions           owners: ask to step outside the standard
//	GET    /applications/{id}/exceptions
//	POST   /applications/{id}/standards-check      owners: the build's dependency check, without building
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type GovernanceHandler struct {
	svc *service.GovernanceService
}

func NewGovernanceHandler(svc *service.GovernanceService) *GovernanceHandler {
	return &GovernanceHandler{svc: svc}
}

func writeGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only the application's owners, or a platform administrator, can do this")
	case errors.Is(err, domain.ErrInvalidProfile), errors.Is(err, domain.ErrInvalidDependencyRule),
		errors.Is(err, domain.ErrInvalidException), errors.Is(err, domain.ErrRejectionReasonRequired):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, domain.ErrUnreadableSource):
		writeError(w, http.StatusBadRequest, "unreadable_source", err.Error())
	case errors.Is(err, domain.ErrDependencyRuleExists), errors.Is(err, domain.ErrExceptionNotPending):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func toDependencyRule(d domain.DependencyRule) map[string]any {
	return map[string]any{
		"id": d.ID, "ecosystem": d.Ecosystem, "package": d.Package, "reason": d.Reason,
		"created_by": d.CreatedByEmail, "created_at": rfc3339(d.CreatedAt),
	}
}

func toException(e domain.StandardException) map[string]any {
	out := map[string]any{
		"id": e.ID, "application_id": e.ApplicationID, "application_name": e.ApplicationName,
		"kind": e.Kind, "subject": e.Subject, "reason": e.Reason, "status": e.Status,
		"requested_by": e.RequestedByEmail, "requested_at": rfc3339(e.CreatedAt),
		"decided_by": e.DecidedByEmail, "decision_reason": e.DecisionReason, "decided_at": nil,
	}
	if e.DecidedAt != nil {
		out["decided_at"] = rfc3339(*e.DecidedAt)
	}
	return out
}

func toProfile(p domain.ApplicationProfile) map[string]any {
	return map[string]any{
		"application_id": p.ApplicationID, "purpose": p.Purpose, "users": p.UsersDescription,
		"handles_personal_data": p.HandlesPersonalData, "data_description": p.DataDescription,
		"frontend": p.Frontend, "backend": p.Backend, "database": p.Database, "updated_at": rfc3339(p.UpdatedAt),
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return false
	}
	return true
}

func (h *GovernanceHandler) Standards(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Standards(r.Context())
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	stacks := make([]map[string]any, 0, len(st.Stacks))
	for _, s := range st.Stacks {
		stacks = append(stacks, map[string]any{"kind": s.Kind, "name": s.Name, "status": s.Status})
	}
	rules := make([]map[string]any, 0, len(st.DependencyRules))
	for _, d := range st.DependencyRules {
		rules = append(rules, toDependencyRule(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rules": st.Rules, "stacks": stacks, "dependency_rules": rules,
		"profile_required_to_build": st.RequireProfile,
		"exception_kinds":           []string{"dependency", "stack", "other"},
	})
}

func (h *GovernanceHandler) AddDependencyRule(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Ecosystem string `json:"ecosystem"`
		Package   string `json:"package"`
		Reason    string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	rule, err := h.svc.AddDependencyRule(r.Context(), caller, req.Ecosystem, req.Package, req.Reason)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	rule.CreatedByEmail = caller.Email
	writeJSON(w, http.StatusCreated, map[string]any{"dependency_rule": toDependencyRule(rule)})
}

func (h *GovernanceHandler) RemoveDependencyRule(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	if err := h.svc.RemoveDependencyRule(r.Context(), caller, chi.URLParam(r, "ruleId")); err != nil {
		writeGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (h *GovernanceHandler) ExceptionQueue(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = domain.ExceptionPending
	}
	list, err := h.svc.ExceptionsByStatus(r.Context(), caller, status)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, toException(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": out})
}

func (h *GovernanceHandler) DecideException(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Decision != "approve" && req.Decision != "reject" {
		writeError(w, http.StatusBadRequest, "invalid_request", `decision is "approve" or "reject"`)
		return
	}
	e, err := h.svc.DecideException(r.Context(), caller, chi.URLParam(r, "exceptionId"), req.Decision == "approve", req.Reason)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exception": toException(e)})
}

func (h *GovernanceHandler) PutProfile(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Purpose             string `json:"purpose"`
		Users               string `json:"users"`
		HandlesPersonalData *bool  `json:"handles_personal_data"`
		DataDescription     string `json:"data_description"`
		Frontend            string `json:"frontend"`
		Backend             string `json:"backend"`
		Database            string `json:"database"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.HandlesPersonalData == nil {
		writeGovernanceError(w, domain.ErrInvalidProfile)
		return
	}
	p, err := h.svc.PutProfile(r.Context(), caller, domain.ApplicationProfile{
		ApplicationID: chi.URLParam(r, "id"), Purpose: req.Purpose, UsersDescription: req.Users,
		HandlesPersonalData: *req.HandlesPersonalData, DataDescription: req.DataDescription,
		Frontend: req.Frontend, Backend: req.Backend, Database: req.Database,
	})
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": toProfile(p)})
}

func (h *GovernanceHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	p, err := h.svc.GetProfile(r.Context(), caller, chi.URLParam(r, "id"))
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"profile": nil})
		return
	}
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": toProfile(p)})
}

func (h *GovernanceHandler) RequestException(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	e, created, err := h.svc.RequestException(r.Context(), caller, chi.URLParam(r, "id"), req.Kind, req.Subject, req.Reason)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"exception": toException(e), "created": created})
}

func (h *GovernanceHandler) ListExceptions(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	list, err := h.svc.ListExceptions(r.Context(), caller, chi.URLParam(r, "id"))
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, toException(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": out})
}

// CheckSource takes the same tar.gz a build does.
func (h *GovernanceHandler) CheckSource(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.svc.CheckSource(r.Context(), caller, chi.URLParam(r, "id"), archive)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	dep := func(d domain.Dependency) map[string]any {
		return map[string]any{"ecosystem": d.Ecosystem, "package": d.Package, "service": d.Service, "manifest": d.Manifest}
	}
	violations := make([]map[string]any, 0, len(res.Violations))
	for _, v := range res.Violations {
		m := dep(v.Dependency)
		m["reason"] = v.Rule.Reason
		m["exception_subject"] = domain.DependencySubject(v.Dependency.Ecosystem, v.Dependency.Package)
		violations = append(violations, m)
	}
	excepted := make([]map[string]any, 0, len(res.Excepted))
	for _, d := range res.Excepted {
		excepted = append(excepted, dep(d))
	}
	routing := make([]map[string]any, 0, len(res.HashRouting))
	for _, f := range res.HashRouting {
		routing = append(routing, map[string]any{"service": f.Service, "file": f.File, "line": f.Line, "api": f.API,
			"exception_subject": domain.HashRoutingSubject})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"passed":               len(res.Violations) == 0 && len(res.HashRouting) == 0 && (res.HasProfile || !res.ProfileRequired),
		"hash_routing":         routing,
		"has_profile":          res.HasProfile,
		"profile_required":     res.ProfileRequired,
		"dependencies_found":   len(res.Dependencies),
		"violations":           violations,
		"allowed_by_exception": excepted,
	})
}
