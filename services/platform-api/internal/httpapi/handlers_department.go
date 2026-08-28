package httpapi

import (
	"context"
	"net/http"

	"platform-api/internal/domain"
)

type DepartmentLister interface {
	List(ctx context.Context) ([]domain.Department, error)
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
