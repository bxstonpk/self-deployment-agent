package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type DeploymentRepo struct {
	pool *pgxpool.Pool
}

func NewDeploymentRepo(pool *pgxpool.Pool) *DeploymentRepo {
	return &DeploymentRepo{pool: pool}
}

const deploymentColumns = `id, application_id, build_id, environment, requested_by, status,
	scan_passed, scan_critical_count, scan_high_count, scan_reports,
	rejection_reason, failure_reason, containers, created_at, updated_at, completed_at`

func (r *DeploymentRepo) Create(ctx context.Context, applicationID, buildID, requestedBy string, environment domain.Environment) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		INSERT INTO deployments (application_id, build_id, requested_by, environment, status)
		VALUES ($1, $2, $3, $4, 'scanning')
		RETURNING `+deploymentColumns,
		applicationID, buildID, requestedBy, string(environment))
}

func (r *DeploymentRepo) UpdateScanResult(ctx context.Context, deploymentID string, reports map[string]domain.ScanReport) (domain.Deployment, error) {
	reportsJSON, err := json.Marshal(reports)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("marshal scan reports: %w", err)
	}
	passed := true
	critical, high := 0, 0
	for _, rep := range reports {
		if !rep.Passed {
			passed = false
		}
		critical += rep.CriticalCount
		high += rep.HighCount
	}
	return r.scanOne(ctx, `
		UPDATE deployments SET scan_passed = $2, scan_critical_count = $3, scan_high_count = $4,
		       scan_reports = $5, updated_at = now()
		WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID, passed, critical, high, reportsJSON)
}

func (r *DeploymentRepo) SetStatus(ctx context.Context, deploymentID string, status domain.DeploymentStatus) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		UPDATE deployments SET status = $2, updated_at = now() WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID, string(status))
}

func (r *DeploymentRepo) SetFailed(ctx context.Context, deploymentID, reason string) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		UPDATE deployments SET status = 'failed', failure_reason = $2, updated_at = now(), completed_at = now()
		WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID, reason)
}

func (r *DeploymentRepo) SetRejected(ctx context.Context, deploymentID, reason string) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		UPDATE deployments SET status = 'rejected', rejection_reason = $2, updated_at = now(), completed_at = now()
		WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID, reason)
}

func (r *DeploymentRepo) SetRunning(ctx context.Context, deploymentID string, containers map[string]domain.RunningContainer) (domain.Deployment, error) {
	containersJSON, err := json.Marshal(containers)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("marshal containers: %w", err)
	}
	return r.scanOne(ctx, `
		UPDATE deployments SET status = 'running', containers = $2, updated_at = now(), completed_at = now()
		WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID, containersJSON)
}

func (r *DeploymentRepo) SetSuperseded(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		UPDATE deployments SET status = 'superseded', updated_at = now() WHERE id = $1
		RETURNING `+deploymentColumns,
		deploymentID)
}

func (r *DeploymentRepo) GetByID(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	return r.scanOne(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, deploymentID)
}

func (r *DeploymentRepo) LatestForApplication(ctx context.Context, applicationID string) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		SELECT `+deploymentColumns+` FROM deployments
		WHERE application_id = $1 ORDER BY created_at DESC LIMIT 1
	`, applicationID)
}

// CurrentRunning finds the (at most one, by construction — see
// deploy_service.go's supersede logic) deployment currently serving live
// traffic for an application. Used by the scale-to-zero proxy to route a
// public request by application name to the right deployment.
func (r *DeploymentRepo) CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error) {
	d, err := r.scanOne(ctx, `
		SELECT `+deploymentColumns+` FROM deployments
		WHERE application_id = $1 AND status = 'running'
		ORDER BY created_at DESC LIMIT 1
	`, applicationID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, domain.ErrNoRunningDeployment
	}
	return d, err
}

func (r *DeploymentRepo) PreviousRunning(ctx context.Context, applicationID, excludeDeploymentID string) (domain.Deployment, error) {
	return r.scanOne(ctx, `
		SELECT `+deploymentColumns+` FROM deployments
		WHERE application_id = $1 AND status = 'running' AND id != $2
		ORDER BY created_at DESC LIMIT 1
	`, applicationID, excludeDeploymentID)
}

func (r *DeploymentRepo) scanOne(ctx context.Context, query string, args ...any) (domain.Deployment, error) {
	var d domain.Deployment
	var environment, status string
	var scanReportsJSON, containersJSON []byte

	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&d.ID, &d.ApplicationID, &d.BuildID, &environment, &d.RequestedBy, &status,
		&d.ScanPassed, &d.ScanCriticalCount, &d.ScanHighCount, &scanReportsJSON,
		&d.RejectionReason, &d.FailureReason, &containersJSON,
		&d.CreatedAt, &d.UpdatedAt, &d.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Deployment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("query deployment: %w", err)
	}

	d.Environment = domain.Environment(environment)
	d.Status = domain.DeploymentStatus(status)
	if len(scanReportsJSON) > 0 {
		if err := json.Unmarshal(scanReportsJSON, &d.ScanReports); err != nil {
			return domain.Deployment{}, fmt.Errorf("unmarshal scan reports: %w", err)
		}
	}
	if len(containersJSON) > 0 {
		if err := json.Unmarshal(containersJSON, &d.Containers); err != nil {
			return domain.Deployment{}, fmt.Errorf("unmarshal containers: %w", err)
		}
	}
	return d, nil
}
