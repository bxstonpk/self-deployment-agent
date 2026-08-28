package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type DeploymentApprovalRepo struct {
	pool *pgxpool.Pool
}

func NewDeploymentApprovalRepo(pool *pgxpool.Pool) *DeploymentApprovalRepo {
	return &DeploymentApprovalRepo{pool: pool}
}

const approvalColumns = `id, deployment_id, requested_by, decided_by, decision, reason, created_at, decided_at`

func (r *DeploymentApprovalRepo) Create(ctx context.Context, deploymentID, requestedBy string) (domain.DeploymentApproval, error) {
	return r.scanOne(ctx, `
		INSERT INTO deployment_approvals (deployment_id, requested_by)
		VALUES ($1, $2)
		RETURNING `+approvalColumns,
		deploymentID, requestedBy)
}

func (r *DeploymentApprovalRepo) Decide(ctx context.Context, deploymentID, decidedBy string, decision domain.ApprovalDecision, reason string) (domain.DeploymentApproval, error) {
	return r.scanOne(ctx, `
		UPDATE deployment_approvals
		SET decided_by = $2, decision = $3, reason = $4, decided_at = now()
		WHERE deployment_id = $1
		RETURNING `+approvalColumns,
		deploymentID, decidedBy, string(decision), reason)
}

func (r *DeploymentApprovalRepo) scanOne(ctx context.Context, query string, args ...any) (domain.DeploymentApproval, error) {
	var a domain.DeploymentApproval
	var decision string
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&a.ID, &a.DeploymentID, &a.RequestedBy, &a.DecidedBy, &decision, &a.Reason, &a.CreatedAt, &a.DecidedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeploymentApproval{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.DeploymentApproval{}, fmt.Errorf("query deployment approval: %w", err)
	}
	a.Decision = domain.ApprovalDecision(decision)
	return a, nil
}
