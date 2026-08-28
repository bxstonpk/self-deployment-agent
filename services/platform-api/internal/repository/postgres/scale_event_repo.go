package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type ScaleEventRepo struct {
	pool *pgxpool.Pool
}

func NewScaleEventRepo(pool *pgxpool.Pool) *ScaleEventRepo {
	return &ScaleEventRepo{pool: pool}
}

// Record implements FR-056. Per FR-056's exception flow, callers should
// treat a failure here as best-effort (log it, don't fail the scaling
// action itself that triggered it) rather than propagating it as a hard
// error — see ScaleService, which does exactly that.
func (r *ScaleEventRepo) Record(ctx context.Context, deploymentID, serviceName string, direction domain.ScaleDirection, triggerReason string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO scale_events (deployment_id, service_name, direction, trigger_reason)
		VALUES ($1, $2, $3, $4)
	`, deploymentID, serviceName, string(direction), triggerReason)
	if err != nil {
		return fmt.Errorf("record scale event: %w", err)
	}
	return nil
}

func (r *ScaleEventRepo) ListForApplication(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error) {
	if len(deploymentIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, deployment_id, service_name, direction, trigger_reason, occurred_at
		FROM scale_events
		WHERE deployment_id = ANY($1)
		ORDER BY occurred_at DESC
		LIMIT $2
	`, deploymentIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("list scale events: %w", err)
	}
	defer rows.Close()

	var out []domain.ScaleEvent
	for rows.Next() {
		var e domain.ScaleEvent
		var direction string
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.ServiceName, &direction, &e.TriggerReason, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan scale event row: %w", err)
		}
		e.Direction = domain.ScaleDirection(direction)
		out = append(out, e)
	}
	return out, rows.Err()
}
