package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// MetricRepo stores Module T's two series: resource samples per container,
// and per-minute traffic buckets per service.
type MetricRepo struct {
	pool *pgxpool.Pool
}

func NewMetricRepo(pool *pgxpool.Pool) *MetricRepo {
	return &MetricRepo{pool: pool}
}

func (r *MetricRepo) AppendResourceSamples(ctx context.Context, samples []domain.ResourceSample) error {
	if len(samples) == 0 {
		return nil
	}
	const columns = 8
	values := make([]string, 0, len(samples))
	args := make([]any, 0, len(samples)*columns)
	for i, s := range samples {
		var deployment any
		if s.DeploymentID != "" {
			deployment = s.DeploymentID
		}
		n := i * columns
		values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8))
		args = append(args, s.ApplicationID, deployment, s.Service, s.ContainerID, s.SampledAt,
			s.CPUPercent, s.MemoryBytes, s.MemoryLimit)
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO resource_samples (application_id, deployment_id, service, container_id, sampled_at,
		                              cpu_percent, memory_bytes, memory_limit_bytes)
		VALUES `+strings.Join(values, ", "), args...); err != nil {
		return fmt.Errorf("store resource samples: %w", err)
	}
	return nil
}

// AddRequestBuckets accumulates into the minute rows rather than replacing
// them: a flush landing in a minute already recorded adds to it.
func (r *MetricRepo) AddRequestBuckets(ctx context.Context, buckets []domain.RequestBucket) error {
	for _, b := range buckets {
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO request_buckets (application_id, service, environment, minute, requests, errors,
			                             latency_ms_total, latency_ms_max)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (application_id, service, environment, minute) DO UPDATE SET
				requests = request_buckets.requests + EXCLUDED.requests,
				errors = request_buckets.errors + EXCLUDED.errors,
				latency_ms_total = request_buckets.latency_ms_total + EXCLUDED.latency_ms_total,
				latency_ms_max = GREATEST(request_buckets.latency_ms_max, EXCLUDED.latency_ms_max)`,
			b.ApplicationID, b.Service, string(b.Environment), b.Minute, b.Requests, b.Errors,
			b.LatencyMsTotal, b.LatencyMsMax); err != nil {
			return fmt.Errorf("store traffic for %s/%s: %w", b.ApplicationID, b.Service, err)
		}
	}
	return nil
}

func (r *MetricRepo) QueryResource(ctx context.Context, q domain.MetricsQuery) ([]domain.ResourceSample, error) {
	where := []string{"s.application_id = $1", "s.sampled_at >= $2", "s.sampled_at <= $3"}
	args := []any{q.ApplicationID, q.From, q.To}
	if q.Service != "" {
		args = append(args, q.Service)
		where = append(where, fmt.Sprintf("s.service = $%d", len(args)))
	}
	if q.Environment != "" {
		args = append(args, q.Environment)
		where = append(where, fmt.Sprintf("d.environment = $%d", len(args)))
	}
	args = append(args, domain.MaxResourceSamples)

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT s.application_id, COALESCE(s.deployment_id::text, ''), s.service, s.container_id, s.sampled_at,
		       s.cpu_percent, s.memory_bytes, s.memory_limit_bytes
		FROM resource_samples s
		LEFT JOIN deployments d ON d.id = s.deployment_id
		WHERE %s
		ORDER BY s.sampled_at DESC, s.id DESC
		LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("query resource samples: %w", err)
	}
	defer rows.Close()

	var out []domain.ResourceSample
	for rows.Next() {
		var s domain.ResourceSample
		if err := rows.Scan(&s.ApplicationID, &s.DeploymentID, &s.Service, &s.ContainerID, &s.SampledAt,
			&s.CPUPercent, &s.MemoryBytes, &s.MemoryLimit); err != nil {
			return nil, fmt.Errorf("scan resource sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *MetricRepo) QueryTraffic(ctx context.Context, q domain.MetricsQuery) ([]domain.RequestBucket, error) {
	where := []string{"application_id = $1", "minute >= $2", "minute <= $3"}
	args := []any{q.ApplicationID, q.From, q.To}
	if q.Service != "" {
		args = append(args, q.Service)
		where = append(where, fmt.Sprintf("service = $%d", len(args)))
	}
	if q.Environment != "" {
		args = append(args, q.Environment)
		where = append(where, fmt.Sprintf("environment = $%d", len(args)))
	}
	args = append(args, domain.MaxRequestBuckets)

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT application_id, service, environment, minute, requests, errors, latency_ms_total, latency_ms_max
		FROM request_buckets
		WHERE %s
		ORDER BY minute DESC
		LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("query traffic: %w", err)
	}
	defer rows.Close()

	var out []domain.RequestBucket
	for rows.Next() {
		var b domain.RequestBucket
		var environment string
		if err := rows.Scan(&b.ApplicationID, &b.Service, &environment, &b.Minute, &b.Requests, &b.Errors,
			&b.LatencyMsTotal, &b.LatencyMsMax); err != nil {
			return nil, fmt.Errorf("scan traffic bucket: %w", err)
		}
		b.Environment = domain.Environment(environment)
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastResourceSampleAt says when collection last succeeded for one
// application: what tells a caller whether an empty window means "quiet"
// or "not being watched".
func (r *MetricRepo) LastResourceSampleAt(ctx context.Context, applicationID string) (time.Time, bool, error) {
	var last *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT max(sampled_at) FROM resource_samples WHERE application_id = $1`, applicationID).Scan(&last); err != nil {
		return time.Time{}, false, fmt.Errorf("find the last sample for %s: %w", applicationID, err)
	}
	if last == nil {
		return time.Time{}, false, nil
	}
	return *last, true, nil
}
