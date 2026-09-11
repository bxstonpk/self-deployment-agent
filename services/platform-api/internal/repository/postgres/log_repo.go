package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// LogRepo is Module S's central log store. It only ever receives lines the
// collector has already redacted — see internal/runtimeengine/logs.go.
type LogRepo struct {
	pool *pgxpool.Pool
}

func NewLogRepo(pool *pgxpool.Pool) *LogRepo {
	return &LogRepo{pool: pool}
}

// Append stores a batch of collected lines in one statement. The collector
// keeps batches small (see logFlushLines), well inside Postgres's
// parameter limit.
func (r *LogRepo) Append(ctx context.Context, lines []domain.LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	const columns = 7
	values := make([]string, 0, len(lines))
	args := make([]any, 0, len(lines)*columns)
	for i, l := range lines {
		var deployment any
		if l.DeploymentID != "" {
			deployment = l.DeploymentID
		}
		n := i * columns
		values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7))
		args = append(args, l.ApplicationID, deployment, l.Service, l.ContainerID, string(l.Stream), l.LoggedAt, l.Message)
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO application_logs (application_id, deployment_id, service, container_id, stream, logged_at, message)
		VALUES `+strings.Join(values, ", "), args...); err != nil {
		return fmt.Errorf("store log lines: %w", err)
	}
	return nil
}

// LastLoggedAt is where collection resumes for a container after a
// platform-api restart.
func (r *LogRepo) LastLoggedAt(ctx context.Context, containerID string) (time.Time, bool, error) {
	var last *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(logged_at) FROM application_logs WHERE container_id = $1`, containerID).Scan(&last); err != nil {
		return time.Time{}, false, fmt.Errorf("find where %s's logs left off: %w", containerID, err)
	}
	if last == nil {
		return time.Time{}, false, nil
	}
	return *last, true, nil
}

// Query implements FR-087: an application's lines, newest first. Always
// scoped by application id — there is no cross-application log query here.
func (r *LogRepo) Query(ctx context.Context, q domain.LogQuery) ([]domain.LogLine, error) {
	where := []string{"l.application_id = $1"}
	args := []any{q.ApplicationID}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if q.Service != "" {
		add("l.service = $%d", q.Service)
	}
	if q.Environment != "" {
		add("d.environment = $%d", q.Environment)
	}
	if q.Contains != "" {
		add(`l.message ILIKE '%%' || $%d || '%%' ESCAPE '\'`, escapeLike(q.Contains))
	}
	if q.Since != nil {
		add("l.logged_at >= $%d", *q.Since)
	}
	if q.Until != nil {
		add("l.logged_at < $%d", *q.Until)
	}
	if q.Before != nil {
		args = append(args, q.Before.LoggedAt, q.Before.ID)
		where = append(where, fmt.Sprintf("(l.logged_at, l.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, q.Limit)

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT l.id, l.application_id, COALESCE(l.deployment_id::text, ''), l.service, l.container_id,
		       l.stream, l.logged_at, l.message
		FROM application_logs l
		LEFT JOIN deployments d ON d.id = l.deployment_id
		WHERE %s
		ORDER BY l.logged_at DESC, l.id DESC
		LIMIT $%d`, strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var out []domain.LogLine
	for rows.Next() {
		var l domain.LogLine
		var stream string
		if err := rows.Scan(&l.ID, &l.ApplicationID, &l.DeploymentID, &l.Service, &l.ContainerID, &stream, &l.LoggedAt, &l.Message); err != nil {
			return nil, fmt.Errorf("scan log line: %w", err)
		}
		l.Stream = domain.LogStream(stream)
		out = append(out, l)
	}
	return out, rows.Err()
}

// escapeLike makes a text filter match literally: % and _ in what someone
// searches for are characters, not wildcards.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
