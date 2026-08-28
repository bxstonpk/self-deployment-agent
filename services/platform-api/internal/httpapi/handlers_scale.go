// GET /applications/{id}/scale-events — FR-056: every scale-up
// (including cold-start) and scale-down (including scale-to-zero) event
// for an application's deployments, queryable for troubleshooting and
// cost/cold-start analysis.
package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type DeploymentLookup interface {
	LatestDeployment(ctx context.Context, applicationID string) (domain.Deployment, error)
}

type ScaleEventLister interface {
	ScaleEvents(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error)
}

type ScaleEventsHandler struct {
	deploys DeploymentLookup
	events  ScaleEventLister
}

func NewScaleEventsHandler(deploys DeploymentLookup, events ScaleEventLister) *ScaleEventsHandler {
	return &ScaleEventsHandler{deploys: deploys, events: events}
}

type scaleEventResponse struct {
	ServiceName   string `json:"service_name"`
	Direction     string `json:"direction"`
	TriggerReason string `json:"trigger_reason"`
	OccurredAt    string `json:"occurred_at"`
}

func (h *ScaleEventsHandler) List(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "id")

	deployment, err := h.deploys.LatestDeployment(r.Context(), applicationID)
	if err != nil {
		writeDeployError(w, err)
		return
	}

	events, err := h.events.ScaleEvents(r.Context(), []string{deployment.ID}, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list scale events")
		return
	}

	out := make([]scaleEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, scaleEventResponse{
			ServiceName: e.ServiceName, Direction: string(e.Direction), TriggerReason: e.TriggerReason,
			OccurredAt: e.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"scale_events": out})
}
