// Endpoint (Module T — docs/02_Functional_Requirements.md FR-091):
//
//	GET /applications/{id}/metrics?from=&to=&service=&environment=
//
// from/to are RFC 3339 and default to the last hour. Owner-only, and
// anyone else gets the same 404 as a nonexistent application, exactly as
// for logs (FR-091's business rule). The response always carries a
// "collection" block saying whether sampling is keeping up, so an empty
// window is never mistaken for a quiet application.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type MetricsQuerier interface {
	Query(ctx context.Context, requesterID string, q domain.MetricsQuery) (domain.MetricsSnapshot, error)
}

type MetricsHandler struct {
	svc MetricsQuerier
}

func NewMetricsHandler(svc MetricsQuerier) *MetricsHandler {
	return &MetricsHandler{svc: svc}
}

type resourcePoint struct {
	Timestamp   string  `json:"timestamp"`
	Service     string  `json:"service"`
	Instance    string  `json:"instance"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes int64   `json:"memory_bytes"`
	MemoryLimit int64   `json:"memory_limit_bytes"`
}

type trafficPoint struct {
	Minute        string  `json:"minute"`
	Service       string  `json:"service"`
	Environment   string  `json:"environment"`
	Requests      int64   `json:"requests"`
	Errors        int64   `json:"errors"`
	ErrorRate     float64 `json:"error_rate"`
	LatencyMsMean float64 `json:"latency_ms_mean"`
	LatencyMsMax  float64 `json:"latency_ms_max"`
}

func (h *MetricsHandler) Query(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	params := r.URL.Query()
	q := domain.MetricsQuery{
		ApplicationID: chi.URLParam(r, "id"),
		Service:       params.Get("service"),
		Environment:   params.Get("environment"),
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if v := params.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_time", p.name+" must be an RFC 3339 timestamp")
				return
			}
			*p.dst = t
		}
	}

	snapshot, err := h.svc.Query(r.Context(), caller.ID, q)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "application not found")
		case errors.Is(err, domain.ErrInvalidMetricsWindow):
			writeError(w, http.StatusBadRequest, "invalid_window", "from must be before to")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to read metrics")
		}
		return
	}

	resource := make([]resourcePoint, 0, len(snapshot.Resource))
	var latestCPU, latestMemory float64
	for i, s := range snapshot.Resource {
		instance := s.ContainerID
		if len(instance) > 12 {
			instance = instance[:12]
		}
		if i == 0 { // newest first
			latestCPU, latestMemory = s.CPUPercent, float64(s.MemoryBytes)
		}
		resource = append(resource, resourcePoint{
			Timestamp: s.SampledAt.UTC().Format(time.RFC3339Nano), Service: s.Service, Instance: instance,
			CPUPercent: round2(s.CPUPercent), MemoryBytes: s.MemoryBytes, MemoryLimit: s.MemoryLimit,
		})
	}

	traffic := make([]trafficPoint, 0, len(snapshot.Traffic))
	var requests, failed int64
	var latencyTotal, latencyMax float64
	for _, b := range snapshot.Traffic {
		requests += b.Requests
		failed += b.Errors
		latencyTotal += b.LatencyMsTotal
		if b.LatencyMsMax > latencyMax {
			latencyMax = b.LatencyMsMax
		}
		traffic = append(traffic, trafficPoint{
			Minute: b.Minute.UTC().Format(time.RFC3339), Service: b.Service, Environment: string(b.Environment),
			Requests: b.Requests, Errors: b.Errors, ErrorRate: rate(b.Errors, b.Requests),
			LatencyMsMean: mean(b.LatencyMsTotal, b.Requests), LatencyMsMax: round2(b.LatencyMsMax),
		})
	}

	instances := make([]map[string]any, 0, len(snapshot.Instances))
	for _, i := range snapshot.Instances {
		instances = append(instances, map[string]any{
			"service": i.Service, "instances": i.Instances, "scaled_to_zero": i.ScaledToZero,
		})
	}
	var lastScale map[string]any
	if e := snapshot.LastScaleEvent; e != nil {
		lastScale = map[string]any{
			"service": e.ServiceName, "direction": string(e.Direction), "reason": e.TriggerReason,
			"occurred_at": e.OccurredAt.UTC().Format(time.RFC3339),
		}
	}
	var lastSample *string
	if snapshot.LastSampleAt != nil {
		s := snapshot.LastSampleAt.UTC().Format(time.RFC3339Nano)
		lastSample = &s
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from": snapshot.From.UTC().Format(time.RFC3339),
		"to":   snapshot.To.UTC().Format(time.RFC3339),
		"collection": map[string]any{
			"collecting": snapshot.Collecting, "last_sample_at": lastSample, "note": snapshot.Note,
		},
		"instances":        instances,
		"last_scale_event": lastScale,
		"resource":         resource,
		"traffic":          traffic,
		"summary": map[string]any{
			"requests": requests, "errors": failed, "error_rate": rate(failed, requests),
			"latency_ms_mean": mean(latencyTotal, requests), "latency_ms_max": round2(latencyMax),
			"cpu_percent_latest": round2(latestCPU), "memory_bytes_latest": int64(latestMemory),
		},
	})
}

func rate(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return round2(float64(part) / float64(whole))
}

func mean(total float64, count int64) float64 {
	if count == 0 {
		return 0
	}
	return round2(total / float64(count))
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
