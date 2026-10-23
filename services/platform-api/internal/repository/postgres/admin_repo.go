package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// AdminRepo answers the administrators' dashboard: every application at
// once, rather than one an owner asked about.
type AdminRepo struct {
	pool *pgxpool.Pool
}

func NewAdminRepo(pool *pgxpool.Pool) *AdminRepo {
	return &AdminRepo{pool: pool}
}

// ApplicationsOverview lists every application that isn't deleted, with
// its department, primary owner, the environments it's running in and how
// many containers serve it now.
func (r *AdminRepo) ApplicationsOverview(ctx context.Context) ([]domain.AdminApplication, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.name, a.lifecycle_status, COALESCE(d.name, ''), COALESCE(u.email, ''),
		       COALESCE((SELECT string_agg(DISTINCT dep.environment, ', ')
		                 FROM deployments dep
		                 WHERE dep.application_id = a.id AND dep.status = 'running'), ''),
		       (SELECT COUNT(*)
		        FROM service_runtime_state s
		        JOIN deployments dep ON dep.id = s.deployment_id
		        WHERE dep.application_id = a.id AND dep.status = 'running' AND s.container_id IS NOT NULL)
		FROM applications a
		LEFT JOIN departments d ON d.id = a.owning_department_id
		LEFT JOIN application_owners o
		       ON o.application_id = a.id AND o.ownership_role = 'primary' AND o.status = 'active'
		LEFT JOIN users u ON u.id = o.user_id
		WHERE a.lifecycle_status <> 'deleted'
		ORDER BY a.name`)
	if err != nil {
		return nil, fmt.Errorf("list applications for the dashboard: %w", err)
	}
	defer rows.Close()

	var out []domain.AdminApplication
	for rows.Next() {
		var a domain.AdminApplication
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &a.Department, &a.OwnerEmail, &a.Environments, &a.Instances); err != nil {
			return nil, fmt.Errorf("scan dashboard application: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CurrentUsage is each application's latest reading per container, for
// the containers serving it now, sampled since since. A container already
// replaced isn't counted alongside its replacement.
func (r *AdminRepo) CurrentUsage(ctx context.Context, since time.Time) (map[string]domain.AdminUsage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT application_id::text, SUM(cpu_percent), SUM(memory_bytes), COUNT(*)
		FROM (
			SELECT DISTINCT ON (s.container_id) s.application_id, s.cpu_percent, s.memory_bytes
			FROM resource_samples s
			WHERE s.sampled_at >= $1
			  AND s.container_id IN (SELECT container_id FROM service_runtime_state WHERE container_id IS NOT NULL)
			ORDER BY s.container_id, s.sampled_at DESC
		) latest
		GROUP BY application_id`, since)
	if err != nil {
		return nil, fmt.Errorf("query current usage: %w", err)
	}
	defer rows.Close()

	out := map[string]domain.AdminUsage{}
	for rows.Next() {
		var id string
		var u domain.AdminUsage
		if err := rows.Scan(&id, &u.CPUPercent, &u.MemoryBytes, &u.Containers); err != nil {
			return nil, fmt.Errorf("scan current usage: %w", err)
		}
		out[id] = u
	}
	return out, rows.Err()
}

// TrafficSince is each application's traffic through the proxy since since.
func (r *AdminRepo) TrafficSince(ctx context.Context, since time.Time) (map[string]domain.AdminTraffic, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT application_id::text, SUM(requests), SUM(errors), SUM(latency_ms_total)
		FROM request_buckets
		WHERE minute >= $1
		GROUP BY application_id`, since)
	if err != nil {
		return nil, fmt.Errorf("query traffic: %w", err)
	}
	defer rows.Close()

	out := map[string]domain.AdminTraffic{}
	for rows.Next() {
		var id string
		var t domain.AdminTraffic
		if err := rows.Scan(&id, &t.Requests, &t.Errors, &t.LatencyMsTotal); err != nil {
			return nil, fmt.Errorf("scan traffic: %w", err)
		}
		out[id] = t
	}
	return out, rows.Err()
}

// TrafficByMinute is the traffic through the proxy across every
// application, one point per minute that had any, since since.
func (r *AdminRepo) TrafficByMinute(ctx context.Context, since time.Time) ([]domain.TrafficPoint, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT minute, SUM(requests), SUM(errors)
		FROM request_buckets
		WHERE minute >= $1
		GROUP BY minute
		ORDER BY minute`, since)
	if err != nil {
		return nil, fmt.Errorf("query traffic by minute: %w", err)
	}
	defer rows.Close()

	var out []domain.TrafficPoint
	for rows.Next() {
		var p domain.TrafficPoint
		if err := rows.Scan(&p.Minute, &p.Requests, &p.Errors); err != nil {
			return nil, fmt.Errorf("scan traffic minute: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
