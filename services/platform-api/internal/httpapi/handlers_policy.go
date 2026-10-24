package httpapi

import (
	"net/http"

	"platform-api/internal/domain"
)

// PolicyHandler tells a client what this platform's deployment rules are,
// from its own configuration, so Claude doesn't have to be told them by a
// document that can drift: which environments wait for approval
// (APPROVAL_REQUIRED_ENVIRONMENTS) and whether that approval is a signed
// one from an approver key (PLATFORM_APPROVER_KEYS) or an owner's.
type PolicyHandler struct {
	environments []string
	approvers    []string
	// devCopies: a dev deployment made through the MCP goes to the
	// application's dev copy, which never waits for approval (DEC-029).
	devCopies bool
}

// SetDevCopies says dev deployments go to dev copies.
func (h *PolicyHandler) SetDevCopies(on bool) { h.devCopies = on }

func NewPolicyHandler(environments []domain.Environment, keys []domain.ApproverKey) *PolicyHandler {
	h := &PolicyHandler{environments: []string{}, approvers: []string{}}
	for _, e := range environments {
		h.environments = append(h.environments, string(e))
	}
	for _, k := range keys {
		h.approvers = append(h.approvers, k.Email)
	}
	return h
}

// Get is GET /policy.
func (h *PolicyHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"approval_required_environments": h.environments,
		"signed_approval":                len(h.approvers) > 0,
		"approvers":                      h.approvers,
		"dev_copies":                     h.devCopies,
	})
}
