package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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

const approvalColumns = `id, deployment_id, requested_by, decided_by, decision, reason, created_at, decided_at,
	test_report, review_document, signer_fingerprint, signed_at`

// Create records a deployment awaiting approval, with the test report the
// request carried (domain.WithTestReport), if any.
func (r *DeploymentApprovalRepo) Create(ctx context.Context, deploymentID, requestedBy string) (domain.DeploymentApproval, error) {
	return r.scanOne(ctx, `
		INSERT INTO deployment_approvals (deployment_id, requested_by, test_report, review_document)
		VALUES ($1, $2, $3, $4)
		RETURNING `+approvalColumns,
		deploymentID, requestedBy, domain.TestReportFrom(ctx), domain.ReviewDocumentFrom(ctx))
}

func (r *DeploymentApprovalRepo) Get(ctx context.Context, deploymentID string) (domain.DeploymentApproval, error) {
	return r.scanOne(ctx, `SELECT `+approvalColumns+` FROM deployment_approvals WHERE deployment_id = $1`, deploymentID)
}

// ListPending is every approval still waiting for a decision, oldest first.
func (r *DeploymentApprovalRepo) ListPending(ctx context.Context) ([]domain.DeploymentApproval, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+approvalColumns+` FROM deployment_approvals WHERE decision = 'pending' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}
	defer rows.Close()
	out := []domain.DeploymentApproval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RecordSignature keeps the proof of a signed decision with it.
func (r *DeploymentApprovalRepo) RecordSignature(ctx context.Context, deploymentID, fingerprint string, signedAt time.Time, signature string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deployment_approvals SET signer_fingerprint = $2, signed_at = $3, signature = $4
		WHERE deployment_id = $1
	`, deploymentID, fingerprint, signedAt, signature)
	return err
}

func scanApproval(row rowScanner) (domain.DeploymentApproval, error) {
	var a domain.DeploymentApproval
	var decision string
	err := row.Scan(&a.ID, &a.DeploymentID, &a.RequestedBy, &a.DecidedBy, &decision, &a.Reason, &a.CreatedAt, &a.DecidedAt,
		&a.TestReport, &a.ReviewDocument, &a.SignerFingerprint, &a.SignedAt)
	a.Decision = domain.ApprovalDecision(decision)
	return a, err
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
	a, err := scanApproval(r.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeploymentApproval{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.DeploymentApproval{}, fmt.Errorf("query deployment approval: %w", err)
	}
	return a, nil
}
