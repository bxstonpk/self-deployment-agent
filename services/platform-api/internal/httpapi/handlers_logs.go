// Endpoint (Module S — docs/02_Functional_Requirements.md FR-087):
//
//	GET /applications/{id}/logs?service=&environment=&contains=&since=&until=&limit=&cursor=
//
// Newest first. since/until are RFC 3339 (until exclusive); contains is a
// case-insensitive literal match. A full page comes with a next_cursor:
// pass it back as cursor for the next, older page. Owner-only, and anyone
// else gets the same 404 as a nonexistent application — see
// LogService.Query.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type LogQuerier interface {
	Query(ctx context.Context, requesterID string, q domain.LogQuery) ([]domain.LogLine, error)
}

type LogHandler struct {
	svc LogQuerier
}

func NewLogHandler(svc LogQuerier) *LogHandler {
	return &LogHandler{svc: svc}
}

type logEntryResponse struct {
	Timestamp    string `json:"timestamp"`
	Service      string `json:"service"`
	Stream       string `json:"stream"`
	Message      string `json:"message"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Instance     string `json:"instance"`
}

func (h *LogHandler) Query(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	params := r.URL.Query()
	q := domain.LogQuery{
		ApplicationID: chi.URLParam(r, "id"),
		Service:       params.Get("service"),
		Environment:   params.Get("environment"),
		Contains:      params.Get("contains"),
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if v := params.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_time", p.name+" must be an RFC 3339 timestamp")
				return
			}
			*p.dst = &t
		}
	}
	if v := params.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		q.Limit = n
	}
	if v := params.Get("cursor"); v != "" {
		c, err := domain.ParseLogCursor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a next_cursor from a previous response")
			return
		}
		q.Before = &c
	}

	lines, err := h.svc.Query(r.Context(), caller.ID, q)
	if err != nil {
		if errors.Is(err, domain.ErrApplicationNotFound) || errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "application not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to read logs")
		return
	}
	out := make([]logEntryResponse, 0, len(lines))
	for _, l := range lines {
		instance := l.ContainerID
		if len(instance) > 12 {
			instance = instance[:12]
		}
		out = append(out, logEntryResponse{
			Timestamp: l.LoggedAt.UTC().Format(time.RFC3339Nano), Service: l.Service, Stream: string(l.Stream),
			Message: l.Message, DeploymentID: l.DeploymentID, Instance: instance,
		})
	}
	var next *string
	if len(lines) > 0 && len(lines) == domain.ClampLogLimit(q.Limit) {
		last := lines[len(lines)-1]
		c := domain.LogCursor{LoggedAt: last.LoggedAt, ID: last.ID}.String()
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "next_cursor": next})
}
