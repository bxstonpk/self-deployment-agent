// Endpoints:
//
//	GET /reports/application-inventory   (FR-127)
//	GET /reports/deployment-activity     (FR-128, ?from=&to= RFC3339)
package httpapi

import (
	"context"
	"net/http"
	"time"

	"platform-api/internal/domain"
)

type Reporter interface {
	ApplicationInventory(ctx context.Context, requesterID string) ([]domain.InventoryRow, error)
	DeploymentActivity(ctx context.Context, requesterID string, from, to time.Time) (domain.DeploymentActivityReport, error)
}

type ReportHandler struct {
	svc Reporter
}

func NewReportHandler(svc Reporter) *ReportHandler {
	return &ReportHandler{svc: svc}
}

type inventoryRowResponse struct {
	ApplicationID   string   `json:"application_id"`
	Name            string   `json:"name"`
	DepartmentID    string   `json:"department_id"`
	DepartmentName  string   `json:"department_name"`
	LifecycleStatus string   `json:"lifecycle_status"`
	OwnerUserIDs    []string `json:"owner_user_ids"`
	Runtimes        []string `json:"runtimes"`
	Environment     string   `json:"environment"`
	CreatedAt       string   `json:"created_at"`
}

// ApplicationInventory handles GET /reports/application-inventory — FR-127.
func (h *ReportHandler) ApplicationInventory(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}

	rows, err := h.svc.ApplicationInventory(r.Context(), caller.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate the application inventory")
		return
	}
	out := make([]inventoryRowResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, inventoryRowResponse{
			ApplicationID: row.ApplicationID, Name: row.Name,
			DepartmentID: row.DepartmentID, DepartmentName: row.DepartmentName,
			LifecycleStatus: string(row.LifecycleStatus),
			OwnerUserIDs:    orEmpty(row.OwnerUserIDs), Runtimes: orEmpty(row.Runtimes),
			Environment: row.Environment, CreatedAt: row.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

// orEmpty keeps a nil slice out of the JSON as `null` — a report consumer
// iterating `runtimes` shouldn't have to special-case "this application
// has no parseable deployment.yaml yet" differently from "it has none".
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type outcomeCountsResponse struct {
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	RolledBack int `json:"rolled_back"`
}

type activityReportResponse struct {
	From          string                           `json:"from"`
	To            string                           `json:"to"`
	AvailableFrom *string                          `json:"available_from,omitempty"`
	Total         outcomeCountsResponse            `json:"total"`
	ByEnvironment map[string]outcomeCountsResponse `json:"by_environment"`
	ByDepartment  map[string]outcomeCountsResponse `json:"by_department"`
}

func toCounts(c domain.DeploymentOutcomeCounts) outcomeCountsResponse {
	return outcomeCountsResponse{Succeeded: c.Succeeded, Failed: c.Failed, RolledBack: c.RolledBack}
}

// DeploymentActivity handles GET /reports/deployment-activity — FR-128.
// `from`/`to` are optional RFC3339 timestamps; the default window is the
// last 30 days, a reporting convention rather than a platform policy
// number (nothing in FR-128 specifies a default range).
func (h *ReportHandler) DeploymentActivity(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}

	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_range", "from must be an RFC3339 timestamp")
			return
		}
		from = parsed
	}
	if v := r.URL.Query().Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_range", "to must be an RFC3339 timestamp")
			return
		}
		to = parsed
	}
	if to.Before(from) {
		writeError(w, http.StatusBadRequest, "invalid_range", "to must not be earlier than from")
		return
	}

	report, err := h.svc.DeploymentActivity(r.Context(), caller.ID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate the deployment activity report")
		return
	}

	resp := activityReportResponse{
		From: report.From.Format(time.RFC3339), To: report.To.Format(time.RFC3339),
		Total:         toCounts(report.Total),
		ByEnvironment: map[string]outcomeCountsResponse{},
		ByDepartment:  map[string]outcomeCountsResponse{},
	}
	if report.AvailableFrom != nil {
		available := report.AvailableFrom.Format(time.RFC3339)
		resp.AvailableFrom = &available
	}
	for env, counts := range report.ByEnvironment {
		resp.ByEnvironment[env] = toCounts(counts)
	}
	for dept, counts := range report.ByDepartment {
		resp.ByDepartment[dept] = toCounts(counts)
	}
	writeJSON(w, http.StatusOK, resp)
}
