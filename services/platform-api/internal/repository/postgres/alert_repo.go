package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// AlertRepo keeps FR-092's alert states and history, and reads the
// windows of Module T's measurements that alert rules are judged on.
type AlertRepo struct {
	pool *pgxpool.Pool
}

func NewAlertRepo(pool *pgxpool.Pool) *AlertRepo {
	return &AlertRepo{pool: pool}
}

// TrafficSince sums each running service's traffic since t.
func (r *AlertRepo) TrafficSince(ctx context.Context, t time.Time) ([]domain.TrafficWindowStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.application_id, b.service, SUM(b.requests), SUM(b.errors), SUM(b.latency_ms_total)
		FROM request_buckets b JOIN applications a ON a.id = b.application_id
		WHERE b.minute >= date_trunc('minute', $1::timestamptz) AND a.lifecycle_status = 'running'
		GROUP BY b.application_id, b.service
	`, t)
	if err != nil {
		return nil, fmt.Errorf("traffic window: %w", err)
	}
	defer rows.Close()
	out := []domain.TrafficWindowStats{}
	for rows.Next() {
		var s domain.TrafficWindowStats
		if err := rows.Scan(&s.ApplicationID, &s.Service, &s.Requests, &s.Errors, &s.LatencyMsSum); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResourcesSince summarises each running service's resource samples since t.
func (r *AlertRepo) ResourcesSince(ctx context.Context, t time.Time) ([]domain.ResourceWindowStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.application_id, s.service, COUNT(*), MIN(s.cpu_percent),
		       AVG(CASE WHEN s.memory_limit_bytes > 0 THEN 100.0 * s.memory_bytes / s.memory_limit_bytes ELSE 0 END),
		       EXTRACT(EPOCH FROM MAX(s.sampled_at) - MIN(s.sampled_at))
		FROM resource_samples s JOIN applications a ON a.id = s.application_id
		WHERE s.sampled_at >= $1 AND a.lifecycle_status = 'running'
		GROUP BY s.application_id, s.service
	`, t)
	if err != nil {
		return nil, fmt.Errorf("resource window: %w", err)
	}
	defer rows.Close()
	out := []domain.ResourceWindowStats{}
	for rows.Next() {
		var s domain.ResourceWindowStats
		var spanSeconds float64
		if err := rows.Scan(&s.ApplicationID, &s.Service, &s.Samples, &s.MinCPUPercent, &s.AvgMemoryPercent, &spanSeconds); err != nil {
			return nil, err
		}
		s.Span = time.Duration(spanSeconds * float64(time.Second))
		out = append(out, s)
	}
	return out, rows.Err()
}

const alertStateColumns = `key, COALESCE(application_id::text, ''), service, rule, level, value, message, started_at, last_notified_at`

func (r *AlertRepo) States(ctx context.Context) ([]domain.AlertState, error) {
	return r.statesWhere(ctx, ``)
}

func (r *AlertRepo) StatesForApplication(ctx context.Context, applicationID string) ([]domain.AlertState, error) {
	return r.statesWhere(ctx, `WHERE application_id = $1`, applicationID)
}

func (r *AlertRepo) statesWhere(ctx context.Context, where string, args ...any) ([]domain.AlertState, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+alertStateColumns+` FROM alert_states `+where+` ORDER BY started_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("list alert states: %w", err)
	}
	defer rows.Close()
	out := []domain.AlertState{}
	for rows.Next() {
		var s domain.AlertState
		var rule, level string
		if err := rows.Scan(&s.Key, &s.ApplicationID, &s.Service, &rule, &level, &s.Value, &s.Message, &s.StartedAt, &s.LastNotifiedAt); err != nil {
			return nil, err
		}
		s.Rule, s.Level = domain.AlertRule(rule), domain.AlertLevel(level)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *AlertRepo) PutState(ctx context.Context, s domain.AlertState) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO alert_states (key, application_id, service, rule, level, value, message, started_at, last_notified_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (key) DO UPDATE SET level = EXCLUDED.level, value = EXCLUDED.value, message = EXCLUDED.message,
			last_notified_at = EXCLUDED.last_notified_at
	`, s.Key, nullable(s.ApplicationID), s.Service, string(s.Rule), string(s.Level), s.Value, s.Message, s.StartedAt, s.LastNotifiedAt)
	if err != nil {
		return fmt.Errorf("save alert state: %w", err)
	}
	return nil
}

func (r *AlertRepo) DeleteState(ctx context.Context, key string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM alert_states WHERE key = $1`, key)
	return err
}

func (r *AlertRepo) AddEvent(ctx context.Context, e domain.AlertEvent) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO alert_events (key, application_id, service, rule, level, kind, value, message, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, e.Key, nullable(e.ApplicationID), e.Service, string(e.Rule), string(e.Level), string(e.Kind), e.Value, e.Message, e.At)
	if err != nil {
		return fmt.Errorf("record alert event: %w", err)
	}
	return nil
}

func (r *AlertRepo) EventsForApplication(ctx context.Context, applicationID string, since time.Time) ([]domain.AlertEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, key, COALESCE(application_id::text, ''), service, rule, level, kind, value, message, at
		FROM alert_events WHERE application_id = $1 AND at >= $2 ORDER BY at DESC LIMIT 200
	`, applicationID, since)
	if err != nil {
		return nil, fmt.Errorf("list alert events: %w", err)
	}
	defer rows.Close()
	out := []domain.AlertEvent{}
	for rows.Next() {
		var e domain.AlertEvent
		var rule, level, kind string
		if err := rows.Scan(&e.ID, &e.Key, &e.ApplicationID, &e.Service, &rule, &level, &kind, &e.Value, &e.Message, &e.At); err != nil {
			return nil, err
		}
		e.Rule, e.Level, e.Kind = domain.AlertRule(rule), domain.AlertLevel(level), domain.AlertEventKind(kind)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEventsOlderThan purges alert history with the same retention as
// application logs (DEC-027).
func (r *AlertRepo) DeleteEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM alert_events WHERE at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge alert events: %w", err)
	}
	return tag.RowsAffected(), nil
}
