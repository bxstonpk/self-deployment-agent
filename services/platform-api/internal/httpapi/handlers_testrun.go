// Tests run by the platform (DEC-029 part 2):
//
//	POST /applications/{id}/test-runs          the body is the source tar.gz, as for a build
//	GET  /applications/{id}/test-runs/{runId}
//
// A deployment names a passed run with test_run_id (handlers_deploy.go).
package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type TestRunHandler struct {
	svc *service.TestRunService
}

func NewTestRunHandler(svc *service.TestRunService) *TestRunHandler {
	return &TestRunHandler{svc: svc}
}

func toTestRun(t domain.TestRun, results []domain.ServiceTestResult) map[string]any {
	out := map[string]any{
		"id": t.ID, "application_id": t.ApplicationID, "status": t.Status, "source_digest": t.SourceDigest,
		"report": t.Report, "started_at": rfc3339(t.StartedAt), "finished_at": nil,
	}
	if t.FinishedAt != nil {
		out["finished_at"] = rfc3339(*t.FinishedAt)
	}
	if results != nil {
		services := make([]map[string]any, 0, len(results))
		for _, r := range results {
			services = append(services, map[string]any{"service": r.Service, "runtime": r.Runtime, "status": r.Status})
		}
		out["services"] = services
	}
	return out
}

func writeTestRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application or test run not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", "only the application's owners can run its tests")
	case errors.Is(err, domain.ErrNoSourceArchive), errors.Is(err, domain.ErrUnreadableSource):
		writeError(w, http.StatusBadRequest, "unreadable_source", err.Error())
	case errors.Is(err, service.ErrNoServicesToTest):
		writeError(w, http.StatusConflict, "not_validated", err.Error())
	case errors.Is(err, domain.ErrTestRunBusy):
		writeError(w, http.StatusTooManyRequests, "test_runner_busy", err.Error())
	case errors.Is(err, domain.ErrNoBaseImageForRuntime):
		writeError(w, http.StatusUnprocessableEntity, "no_base_image", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected error")
	}
}

func (h *TestRunHandler) Run(w http.ResponseWriter, r *http.Request) {
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
	run, results, err := h.svc.Run(r.Context(), chi.URLParam(r, "id"), caller.ID, archive)
	if err != nil {
		writeTestRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"test_run": toTestRun(run, results)})
}

func (h *TestRunHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	run, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"), caller.ID, chi.URLParam(r, "runId"))
	if err != nil {
		writeTestRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"test_run": toTestRun(run, nil)})
}
