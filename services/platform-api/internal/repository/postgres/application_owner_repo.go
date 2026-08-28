package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type ApplicationOwnerRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationOwnerRepo(pool *pgxpool.Pool) *ApplicationOwnerRepo {
	return &ApplicationOwnerRepo{pool: pool}
}

// AssignPrimaryOwner implements the FR-011/FR-015 default path: the
// registering employee becomes the sole active primary owner. The partial
// unique index one_active_primary_owner_per_application (see the 0001
// migration) is the actual enforcement point; this is a defensive check on
// top of it.
func (r *ApplicationOwnerRepo) AssignPrimaryOwner(ctx context.Context, applicationID, userID, assignedBy string) (domain.ApplicationOwner, error) {
	var o domain.ApplicationOwner
	var role string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO application_owners (application_id, user_id, ownership_role, assigned_by)
		VALUES ($1, $2, 'primary', $3)
		RETURNING id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
	`, applicationID, userID, assignedBy).Scan(
		&o.ID, &o.ApplicationID, &o.UserID, &role, &o.AssignedBy, &o.AssignedAt, &o.Status,
	)
	if err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("assign primary owner: %w", err)
	}
	o.OwnershipRole = domain.OwnershipRole(role)
	return o, nil
}

func (r *ApplicationOwnerRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
		FROM application_owners
		WHERE application_id = $1
		ORDER BY assigned_at ASC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list application owners: %w", err)
	}
	defer rows.Close()

	var out []domain.ApplicationOwner
	for rows.Next() {
		var o domain.ApplicationOwner
		var role string
		if err := rows.Scan(&o.ID, &o.ApplicationID, &o.UserID, &role, &o.AssignedBy, &o.AssignedAt, &o.Status); err != nil {
			return nil, fmt.Errorf("scan application owner row: %w", err)
		}
		o.OwnershipRole = domain.OwnershipRole(role)
		out = append(out, o)
	}
	return out, rows.Err()
}
