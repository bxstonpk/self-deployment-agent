// Endpoints (FR-064, DEC-025 — see internal/domain/backup.go):
//
//	GET  /applications/{id}/backups                        the policy, the schedule and the backups that exist
//	POST /applications/{id}/backups                        take one now: {"kind": "full" | "differential" | ""}
//	POST /applications/{id}/backups/{backupId}/restore     {"confirm": "<application name>"}
//
// All owner-only. Restoring replaces the database's contents, so it needs
// the application's exact name, and first backs up what it replaces.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type BackupHandler struct {
	svc *service.BackupService
}

func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

type backupResponse struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	BaseBackupID string `json:"base_backup_id,omitempty"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	StoredBytes  int64  `json:"stored_bytes"`
	DumpBytes    int64  `json:"dump_bytes"`
	TakenAt      string `json:"taken_at"`
}

func toBackupResponse(b domain.DatabaseBackup) backupResponse {
	return backupResponse{
		ID: b.ID, Kind: string(b.Kind), BaseBackupID: b.BaseBackupID, Trigger: string(b.Trigger),
		Status: string(b.Status), Error: b.Error, StoredBytes: b.StoredBytes, DumpBytes: b.DumpBytes,
		TakenAt: b.TakenAt.UTC().Format(time.RFC3339),
	}
}

func writeBackupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "application not found")
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, domain.ErrBackupNotFound):
		writeError(w, http.StatusNotFound, "backup_not_found", err.Error())
	case errors.Is(err, domain.ErrDatabaseNotProvisioned):
		writeError(w, http.StatusNotFound, "database_not_provisioned", err.Error())
	case errors.Is(err, domain.ErrInvalidBackupKind):
		writeError(w, http.StatusBadRequest, "invalid_backup_kind", err.Error())
	case errors.Is(err, domain.ErrRestoreConfirmation):
		writeError(w, http.StatusBadRequest, "confirmation_required", err.Error())
	case errors.Is(err, domain.ErrBackupNotRestorable):
		writeError(w, http.StatusConflict, "backup_not_restorable", err.Error())
	case errors.Is(err, domain.ErrBackupIntegrity):
		writeError(w, http.StatusConflict, "backup_integrity", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "backup_failed", err.Error())
	}
}

func (h *BackupHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	overview, err := h.svc.List(r.Context(), chi.URLParam(r, "id"), caller.ID)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	out := make([]backupResponse, 0, len(overview.Backups))
	for _, b := range overview.Backups {
		out = append(out, toBackupResponse(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policy": overview.Policy,
		"schedule": map[string]any{
			"full_weekday": overview.Schedule.FullWeekday.String(),
			"hour":         overview.Schedule.Hour,
			"timezone":     overview.Schedule.Location.String(),
			"retention":    "the previous week's backups are deleted once a new full backup succeeds",
		},
		"backups": out,
	})
}

func (h *BackupHandler) BackupNow(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Kind string `json:"kind"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", `request body must be JSON of the form {"kind": "full" | "differential"}`)
			return
		}
	}
	b, err := h.svc.BackupNow(r.Context(), chi.URLParam(r, "id"), caller.ID, domain.BackupKind(req.Kind))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"backup": toBackupResponse(b)})
}

func (h *BackupHandler) Restore(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", `request body must be JSON of the form {"confirm": "<application name>"}`)
		return
	}
	backupID := chi.URLParam(r, "backupId")
	safety, err := h.svc.Restore(r.Context(), chi.URLParam(r, "id"), backupID, caller.ID, req.Confirm)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"restored_backup_id": backupID,
		"pre_restore_backup": toBackupResponse(safety),
	})
}
