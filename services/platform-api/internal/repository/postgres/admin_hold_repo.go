package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// AdminHoldRepo keeps administrators' suspensions (migration 0029).
type AdminHoldRepo struct {
	pool *pgxpool.Pool
}

func NewAdminHoldRepo(pool *pgxpool.Pool) *AdminHoldRepo {
	return &AdminHoldRepo{pool: pool}
}

// Place puts a hold on an application, replacing one already there.
func (r *AdminHoldRepo) Place(ctx context.Context, applicationID, heldBy, reason string) (domain.AdminHold, error) {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO admin_holds (application_id, reason, held_by) VALUES ($1, $2, $3)
		ON CONFLICT (application_id) DO UPDATE SET reason = EXCLUDED.reason, held_by = EXCLUDED.held_by, held_at = now()`,
		applicationID, reason, heldBy); err != nil {
		return domain.AdminHold{}, fmt.Errorf("place admin hold: %w", err)
	}
	return r.Get(ctx, applicationID)
}

// Get is the application's hold, or domain.ErrNotFound if it has none.
func (r *AdminHoldRepo) Get(ctx context.Context, applicationID string) (domain.AdminHold, error) {
	var h domain.AdminHold
	err := r.pool.QueryRow(ctx, `
		SELECT h.application_id::text, h.reason, h.held_by::text, u.email, h.held_at
		FROM admin_holds h JOIN users u ON u.id = h.held_by
		WHERE h.application_id = $1`, applicationID).
		Scan(&h.ApplicationID, &h.Reason, &h.HeldBy, &h.HeldByEmail, &h.HeldAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminHold{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AdminHold{}, fmt.Errorf("get admin hold: %w", err)
	}
	return h, nil
}

// Release removes the application's hold.
func (r *AdminHoldRepo) Release(ctx context.Context, applicationID string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM admin_holds WHERE application_id = $1`, applicationID); err != nil {
		return fmt.Errorf("release admin hold: %w", err)
	}
	return nil
}
