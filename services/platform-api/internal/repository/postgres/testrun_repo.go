package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// TestRunRepo stores tests run by the platform (migration 0025).
type TestRunRepo struct {
	pool *pgxpool.Pool
}

func NewTestRunRepo(pool *pgxpool.Pool) *TestRunRepo {
	return &TestRunRepo{pool: pool}
}

const testRunColumns = `id, application_id, requested_by, status, source_digest, report, started_at, finished_at`

func scanTestRun(row pgx.Row) (domain.TestRun, error) {
	var t domain.TestRun
	var status string
	err := row.Scan(&t.ID, &t.ApplicationID, &t.RequestedBy, &status, &t.SourceDigest, &t.Report, &t.StartedAt, &t.FinishedAt)
	t.Status = domain.TestRunStatus(status)
	return t, err
}

func (r *TestRunRepo) Create(ctx context.Context, applicationID, requestedBy, sourceDigest string) (domain.TestRun, error) {
	t, err := scanTestRun(r.pool.QueryRow(ctx, `
		INSERT INTO test_runs (application_id, requested_by, source_digest) VALUES ($1, $2, $3)
		RETURNING `+testRunColumns, applicationID, requestedBy, sourceDigest))
	if err != nil {
		return domain.TestRun{}, fmt.Errorf("create test run: %w", err)
	}
	return t, nil
}

func (r *TestRunRepo) Finish(ctx context.Context, id string, status domain.TestRunStatus, report string) (domain.TestRun, error) {
	t, err := scanTestRun(r.pool.QueryRow(ctx, `
		UPDATE test_runs SET status = $2, report = $3, finished_at = now()
		WHERE id = $1 AND status = 'running'
		RETURNING `+testRunColumns, id, string(status), report))
	if err != nil {
		return domain.TestRun{}, fmt.Errorf("finish test run: %w", err)
	}
	return t, nil
}

func (r *TestRunRepo) Get(ctx context.Context, id string) (domain.TestRun, error) {
	t, err := scanTestRun(r.pool.QueryRow(ctx, `SELECT `+testRunColumns+` FROM test_runs WHERE id = $1`, id))
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.TestRun{}, e
		}
		return domain.TestRun{}, fmt.Errorf("get test run: %w", err)
	}
	return t, nil
}

// FailUnfinished closes runs a restart interrupted: nothing finishes them
// otherwise.
func (r *TestRunRepo) FailUnfinished(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE test_runs SET status = 'error', finished_at = now(),
			report = report || E'\n== interrupted: the platform restarted during the run'
		WHERE status = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("close interrupted test runs: %w", err)
	}
	return tag.RowsAffected(), nil
}
