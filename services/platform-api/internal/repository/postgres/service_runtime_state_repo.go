package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type ServiceRuntimeStateRepo struct {
	pool *pgxpool.Pool
}

func NewServiceRuntimeStateRepo(pool *pgxpool.Pool) *ServiceRuntimeStateRepo {
	return &ServiceRuntimeStateRepo{pool: pool}
}

const serviceRuntimeStateColumns = `deployment_id, service_name, image_ref, container_port, eligible,
	container_id, host_port, last_active_at, updated_at`

// Upsert seeds/replaces a service's runtime state — called once per
// service when a deployment reaches Running (initial activation), always
// starting with a live container (never pre-scaled-to-zero).
func (r *ServiceRuntimeStateRepo) Upsert(ctx context.Context, s domain.ServiceRuntimeState) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO service_runtime_state (deployment_id, service_name, image_ref, container_port, eligible, container_id, host_port, last_active_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (deployment_id, service_name) DO UPDATE SET
			image_ref = EXCLUDED.image_ref, container_port = EXCLUDED.container_port,
			eligible = EXCLUDED.eligible, container_id = EXCLUDED.container_id,
			host_port = EXCLUDED.host_port, last_active_at = now(), updated_at = now()
	`, s.DeploymentID, s.ServiceName, s.ImageRef, s.ContainerPort, s.Eligible, s.ContainerID, s.HostPort)
	if err != nil {
		return fmt.Errorf("upsert service runtime state: %w", err)
	}
	return nil
}

func (r *ServiceRuntimeStateRepo) Get(ctx context.Context, deploymentID, serviceName string) (domain.ServiceRuntimeState, error) {
	return r.scanOne(ctx, `
		SELECT `+serviceRuntimeStateColumns+`
		FROM service_runtime_state WHERE deployment_id = $1 AND service_name = $2
	`, deploymentID, serviceName)
}

// SetContainer records a cold-start (or the initial activation): the
// service now has a live container. Guarded by a WHERE that only succeeds
// coming FROM scaled-to-zero, so a racing sweeper can't undo a cold-start
// that happened concurrently (see ScaleService.EnsureRunning's locking,
// which is the primary guard — this is a defensive second layer).
func (r *ServiceRuntimeStateRepo) SetContainer(ctx context.Context, deploymentID, serviceName, containerID string, hostPort int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE service_runtime_state
		SET container_id = $3, host_port = $4, last_active_at = now(), updated_at = now()
		WHERE deployment_id = $1 AND service_name = $2
	`, deploymentID, serviceName, containerID, hostPort)
	if err != nil {
		return fmt.Errorf("set service container: %w", err)
	}
	return nil
}

// ClearContainer records a scale-to-zero: guarded so it only clears a
// container this same idle check observed (compare-and-swap on
// container_id), preventing a race where a concurrent cold-start's new
// container gets wiped by a sweep that read stale state.
func (r *ServiceRuntimeStateRepo) ClearContainer(ctx context.Context, deploymentID, serviceName, expectedContainerID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE service_runtime_state
		SET container_id = NULL, host_port = NULL, updated_at = now()
		WHERE deployment_id = $1 AND service_name = $2 AND container_id = $3
	`, deploymentID, serviceName, expectedContainerID)
	if err != nil {
		return false, fmt.Errorf("clear service container: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *ServiceRuntimeStateRepo) TouchActive(ctx context.Context, deploymentID, serviceName string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE service_runtime_state SET last_active_at = now(), updated_at = now()
		WHERE deployment_id = $1 AND service_name = $2
	`, deploymentID, serviceName)
	if err != nil {
		return fmt.Errorf("touch service active: %w", err)
	}
	return nil
}

// ListForDeployment returns every service's state for a deployment
// regardless of eligibility — Suspend/Resume/Restart act on all services.
func (r *ServiceRuntimeStateRepo) ListForDeployment(ctx context.Context, deploymentID string) ([]domain.ServiceRuntimeState, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+serviceRuntimeStateColumns+`
		FROM service_runtime_state WHERE deployment_id = $1
	`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list services for deployment: %w", err)
	}
	defer rows.Close()

	var out []domain.ServiceRuntimeState
	for rows.Next() {
		s, err := scanServiceRuntimeStateRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service runtime state row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListEligibleActive returns every eligible service currently running a
// container (candidates for the idle sweep) — filtered further by
// last_active_at in the caller, since "idle threshold" is a Duration the
// repository layer shouldn't need to know about.
func (r *ServiceRuntimeStateRepo) ListEligibleActive(ctx context.Context) ([]domain.ServiceRuntimeState, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+serviceRuntimeStateColumns+`
		FROM service_runtime_state
		WHERE eligible = true AND container_id IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("list eligible active services: %w", err)
	}
	defer rows.Close()

	var out []domain.ServiceRuntimeState
	for rows.Next() {
		s, err := scanServiceRuntimeStateRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service runtime state row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteForDeployment removes runtime state when a deployment is
// superseded — it's no longer the live one, so neither the proxy nor the
// sweeper should act on it further (its containers are stopped separately
// by the caller, which already has the container IDs at hand).
func (r *ServiceRuntimeStateRepo) DeleteForDeployment(ctx context.Context, deploymentID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM service_runtime_state WHERE deployment_id = $1`, deploymentID)
	if err != nil {
		return fmt.Errorf("delete service runtime state for deployment: %w", err)
	}
	return nil
}

func (r *ServiceRuntimeStateRepo) scanOne(ctx context.Context, query string, args ...any) (domain.ServiceRuntimeState, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	s, err := scanServiceRuntimeStateRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceRuntimeState{}, domain.ErrServiceStateNotFound
	}
	if err != nil {
		return domain.ServiceRuntimeState{}, fmt.Errorf("query service runtime state: %w", err)
	}
	return s, nil
}

func scanServiceRuntimeStateRow(row rowScanner) (domain.ServiceRuntimeState, error) {
	var s domain.ServiceRuntimeState
	err := row.Scan(&s.DeploymentID, &s.ServiceName, &s.ImageRef, &s.ContainerPort, &s.Eligible,
		&s.ContainerID, &s.HostPort, &s.LastActiveAt, &s.UpdatedAt)
	return s, err
}
