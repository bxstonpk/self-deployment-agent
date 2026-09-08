package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type NotificationRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{pool: pool}
}

func (r *NotificationRepo) Create(ctx context.Context, n domain.Notification) (domain.Notification, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO notifications (recipient_user_id, category, title, detail, resource_type, resource_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, n.RecipientUserID, string(n.Category), n.Title, n.Detail, n.ResourceType, n.ResourceID,
	).Scan(&n.ID, &n.CreatedAt)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("create notification: %w", err)
	}
	return n, nil
}

func (r *NotificationRepo) ListForRecipient(ctx context.Context, recipientUserID string, unreadOnly bool, limit int) ([]domain.Notification, error) {
	query := `
		SELECT id, recipient_user_id, category, title, detail, resource_type, resource_id, read_at, created_at
		FROM notifications
		WHERE recipient_user_id = $1
	`
	if unreadOnly {
		query += " AND read_at IS NULL"
	}
	query += " ORDER BY created_at DESC LIMIT $2"

	rows, err := r.pool.Query(ctx, query, recipientUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		var category string
		if err := rows.Scan(&n.ID, &n.RecipientUserID, &category, &n.Title, &n.Detail, &n.ResourceType, &n.ResourceID, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan notification row: %w", err)
		}
		n.Category = domain.NotificationCategory(category)
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead only succeeds for the notification's own recipient — matched in
// the WHERE clause, not checked separately, so there is no way to mark
// another user's notification read even by guessing its id.
func (r *NotificationRepo) MarkRead(ctx context.Context, id, recipientUserID string) (domain.Notification, error) {
	var n domain.Notification
	var category string
	err := r.pool.QueryRow(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE id = $1 AND recipient_user_id = $2
		RETURNING id, recipient_user_id, category, title, detail, resource_type, resource_id, read_at, created_at
	`, id, recipientUserID).Scan(&n.ID, &n.RecipientUserID, &category, &n.Title, &n.Detail, &n.ResourceType, &n.ResourceID, &n.ReadAt, &n.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Notification{}, domain.ErrNotFound
		}
		return domain.Notification{}, fmt.Errorf("mark notification read: %w", err)
	}
	n.Category = domain.NotificationCategory(category)
	return n, nil
}
