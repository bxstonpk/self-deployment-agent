// Endpoints:
//
//	GET  /notifications             (FR-107/FR-108: list the caller's own notifications)
//	POST /notifications/{id}/read   (mark one of the caller's own notifications read)
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"platform-api/internal/domain"
)

type NotificationLister interface {
	List(ctx context.Context, recipientUserID string, unreadOnly bool) ([]domain.Notification, error)
	MarkRead(ctx context.Context, id, recipientUserID string) (domain.Notification, error)
}

type NotificationHandler struct {
	svc NotificationLister
}

func NewNotificationHandler(svc NotificationLister) *NotificationHandler {
	return &NotificationHandler{svc: svc}
}

type notificationResponse struct {
	ID           string  `json:"id"`
	Category     string  `json:"category"`
	Title        string  `json:"title"`
	Detail       string  `json:"detail"`
	ResourceType string  `json:"resource_type"`
	ResourceID   string  `json:"resource_id"`
	ReadAt       *string `json:"read_at"`
	CreatedAt    string  `json:"created_at"`
}

func toNotificationResponse(n domain.Notification) notificationResponse {
	resp := notificationResponse{
		ID: n.ID, Category: string(n.Category), Title: n.Title, Detail: n.Detail,
		ResourceType: n.ResourceType, ResourceID: n.ResourceID, CreatedAt: n.CreatedAt.Format(time.RFC3339),
	}
	if n.ReadAt != nil {
		readAt := n.ReadAt.Format(time.RFC3339)
		resp.ReadAt = &readAt
	}
	return resp
}

// List handles GET /notifications?unread_only=true — always scoped to the
// authenticated caller as recipient; there is no "list someone else's
// notifications" capability.
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	unreadOnly := r.URL.Query().Get("unread_only") == "true"

	notifications, err := h.svc.List(r.Context(), caller.ID, unreadOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list notifications")
		return
	}
	out := make([]notificationResponse, 0, len(notifications))
	for _, n := range notifications {
		out = append(out, toNotificationResponse(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	caller, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing authenticated caller")
		return
	}
	id := chi.URLParam(r, "id")

	n, err := h.svc.MarkRead(r.Context(), id, caller.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "notification not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to mark notification read")
		return
	}
	writeJSON(w, http.StatusOK, toNotificationResponse(n))
}
