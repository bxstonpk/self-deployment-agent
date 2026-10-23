package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"platform-api/internal/domain"
)

type DepartmentLister interface {
	List(ctx context.Context) ([]domain.Department, error)
	GetOrCreateByName(ctx context.Context, name string) (domain.Department, error)
	ListSummaries(ctx context.Context) ([]domain.DepartmentSummary, error)
}

type DepartmentHandler struct {
	repo DepartmentLister
}

func NewDepartmentHandler(repo DepartmentLister) *DepartmentHandler {
	return &DepartmentHandler{repo: repo}
}

// List handles GET /departments — lets a caller resolve a department name
// to the UUID Applications.Register requires. Added for the MCP server's
// create_application tool (docs/07_MCP_Requirements.md Section 13.4, which
// takes a department NAME, not a UUID), but is a generally useful listing
// endpoint on its own.
func (h *DepartmentHandler) List(w http.ResponseWriter, r *http.Request) {
	departments, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list departments")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"departments": departments})
}

// Create is POST /departments {"name": ...}: any signed-in employee can add
// a department, the way registering with a new department name always
// could — so an application can belong to whichever department it's for,
// not only to departments someone happened to register under. An existing
// department of that name (any case) is returned instead of a duplicate.
func (h *DepartmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "expected {\"name\": \"...\"}")
		return
	}
	d, err := h.repo.GetOrCreateByName(r.Context(), req.Name)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidDepartmentName) {
			writeError(w, http.StatusBadRequest, "invalid_department_name", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to add the department")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"department": d})
}

type departmentSummaryResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	Users        int       `json:"users"`
	Applications int       `json:"applications"`
	CreatedAt    time.Time `json:"created_at"`
}

// Summary is GET /departments/summary: every department with how many
// people and applications belong to it.
func (h *DepartmentHandler) Summary(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.repo.ListSummaries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list departments")
		return
	}
	out := make([]departmentSummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, departmentSummaryResponse{
			ID: s.ID, Name: s.Name, Status: s.Status, Users: s.Users, Applications: s.Applications, CreatedAt: s.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"departments": out})
}
