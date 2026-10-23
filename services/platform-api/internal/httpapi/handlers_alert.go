// GET /applications/{id}/alerts — FR-092: what is firing for this
// application now, and the last seven days of alert notifications.
// Owner-only, like its metrics.
package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type AlertHandler struct {
	svc *service.AlertService
}

func NewAlertHandler(svc *service.AlertService) *AlertHandler {
	return &AlertHandler{svc: svc}
}

func (h *AlertHandler) ForApplication(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	active, events, err := h.svc.ForApplication(r.Context(), chi.URLParam(r, "id"), caller.ID)
	switch {
	case errors.Is(err, domain.ErrUnauthorized), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "could not read alerts")
		return
	}
	activeOut := make([]map[string]any, 0, len(active))
	for _, a := range active {
		activeOut = append(activeOut, map[string]any{
			"service": a.Service, "rule": a.Rule, "level": a.Level, "value": a.Value, "message": a.Message,
			"started_at": a.StartedAt.UTC().Format(time.RFC3339),
		})
	}
	eventsOut := make([]map[string]any, 0, len(events))
	for _, e := range events {
		eventsOut = append(eventsOut, map[string]any{
			"service": e.Service, "rule": e.Rule, "level": e.Level, "kind": e.Kind, "value": e.Value,
			"message": e.Message, "at": e.At.UTC().Format(time.RFC3339),
		})
	}
	t := h.svc.Thresholds()
	writeJSON(w, http.StatusOK, map[string]any{
		"active": activeOut,
		"recent": eventsOut,
		"thresholds": map[string]any{
			"error_rate_percent": []float64{100 * t.ErrorRateWarning, 100 * t.ErrorRateCritical},
			"latency_ms":         []float64{t.LatencyWarningMs, t.LatencyCriticalMs},
			"cpu_percent":        []float64{t.CPUWarning, t.CPUCritical},
			"memory_percent":     []float64{t.MemoryWarning, t.MemoryCritical},
			"reminder_every":     t.ReminderInterval.String(),
		},
	})
}
