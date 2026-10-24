package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"platform-api/internal/domain"
)

func policyOf(t *testing.T, h *PolicyHandler) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.Get(w, httptest.NewRequest(http.MethodGet, "/policy", nil))
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPolicy_ReportsTheConfiguredApprovalRules(t *testing.T) {
	got := policyOf(t, NewPolicyHandler(
		[]domain.Environment{domain.EnvironmentDev, domain.EnvironmentProduction},
		[]domain.ApproverKey{{Email: "prasit.k@sti-th.com"}},
	))
	if !reflect.DeepEqual(got["approval_required_environments"], []any{"dev", "production"}) {
		t.Errorf("environments: %v", got["approval_required_environments"])
	}
	if got["signed_approval"] != true || !reflect.DeepEqual(got["approvers"], []any{"prasit.k@sti-th.com"}) {
		t.Errorf("approvers: %v %v", got["signed_approval"], got["approvers"])
	}
}

// Empty lists, not null: a client can iterate without checking.
func TestPolicy_NothingGatedIsEmptyListsNotNull(t *testing.T) {
	got := policyOf(t, NewPolicyHandler(nil, nil))
	if !reflect.DeepEqual(got["approval_required_environments"], []any{}) || !reflect.DeepEqual(got["approvers"], []any{}) || got["signed_approval"] != false {
		t.Errorf("got %v", got)
	}
}
