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

type BuildRepo struct {
	pool *pgxpool.Pool
}

func NewBuildRepo(pool *pgxpool.Pool) *BuildRepo {
	return &BuildRepo{pool: pool}
}

const buildColumns = `id, application_id, triggered_by, status, error_category, error_detail, image_refs, started_at, completed_at`

func (r *BuildRepo) Create(ctx context.Context, applicationID, triggeredBy string) (domain.Build, error) {
	return r.scanOne(ctx, `
		INSERT INTO builds (application_id, triggered_by, status)
		VALUES ($1, $2, 'queued')
		RETURNING `+buildColumns,
		applicationID, triggeredBy)
}

func (r *BuildRepo) MarkInProgress(ctx context.Context, buildID string) (domain.Build, error) {
	return r.scanOne(ctx, `
		UPDATE builds SET status = 'in_progress' WHERE id = $1
		RETURNING `+buildColumns, buildID)
}

func (r *BuildRepo) MarkSucceeded(ctx context.Context, buildID string, imageRefs map[string]string) (domain.Build, error) {
	refsJSON, err := json.Marshal(imageRefs)
	if err != nil {
		return domain.Build{}, fmt.Errorf("marshal image refs: %w", err)
	}
	return r.scanOne(ctx, `
		UPDATE builds SET status = 'succeeded', image_refs = $2, completed_at = now() WHERE id = $1
		RETURNING `+buildColumns, buildID, refsJSON)
}

func (r *BuildRepo) MarkFailed(ctx context.Context, buildID string, category domain.ErrorCategory, detail string) (domain.Build, error) {
	return r.scanOne(ctx, `
		UPDATE builds SET status = 'failed', error_category = $2, error_detail = $3, completed_at = now() WHERE id = $1
		RETURNING `+buildColumns, buildID, string(category), detail)
}

func (r *BuildRepo) GetByID(ctx context.Context, buildID string) (domain.Build, error) {
	return r.scanOne(ctx, `SELECT `+buildColumns+` FROM builds WHERE id = $1`, buildID)
}

func (r *BuildRepo) LatestForApplication(ctx context.Context, applicationID string) (domain.Build, error) {
	return r.scanOne(ctx, `
		SELECT `+buildColumns+`
		FROM builds WHERE application_id = $1
		ORDER BY started_at DESC LIMIT 1
	`, applicationID)
}

// ListForApplication is every build of the application, newest first.
func (r *BuildRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.Build, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+buildColumns+`
		FROM builds WHERE application_id = $1
		ORDER BY started_at DESC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list builds: %w", err)
	}
	defer rows.Close()
	out := []domain.Build{}
	for rows.Next() {
		b, err := scanBuildRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BuildRepo) scanOne(ctx context.Context, query string, args ...any) (domain.Build, error) {
	return scanBuildRow(r.pool.QueryRow(ctx, query, args...))
}

func scanBuildRow(row rowScanner) (domain.Build, error) {
	var b domain.Build
	var status string
	var errorCategory, errorDetail *string
	var imageRefsJSON []byte

	err := row.Scan(
		&b.ID, &b.ApplicationID, &b.TriggeredBy, &status, &errorCategory, &errorDetail,
		&imageRefsJSON, &b.StartedAt, &b.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Build{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Build{}, fmt.Errorf("query build: %w", err)
	}

	b.Status = domain.BuildStatus(status)
	if errorCategory != nil {
		cat := domain.ErrorCategory(*errorCategory)
		b.ErrorCategory = &cat
	}
	b.ErrorDetail = errorDetail
	if len(imageRefsJSON) > 0 {
		if err := json.Unmarshal(imageRefsJSON, &b.ImageRefs); err != nil {
			return domain.Build{}, fmt.Errorf("unmarshal image refs: %w", err)
		}
	}
	return b, nil
}

// FailUnfinished closes the builds a restart interrupted. A build runs in
// platform-api's own process, so one still queued or in progress when it
// starts can never finish, and while it is the application's latest build
// every new one is refused as already in flight: found on the LAN host when
// a platform deploy restarted platform-api mid-build (2026-11-16) and the
// employee's dev copy could not be built again. An application whose live
// deployment kept serving goes back to Running, as a failed rebuild leaves
// it (BuildService.TriggerBuild); one with none stays at Build, which a new
// build starts from.
func (r *BuildRepo) FailUnfinished(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `
		WITH failed AS (
			UPDATE builds SET status = 'failed', error_category = 'platform',
				error_detail = 'the platform restarted during this build; nothing was built. Build again.',
				completed_at = now()
			WHERE status IN ('queued', 'in_progress')
			RETURNING application_id
		), restored AS (
			UPDATE applications a SET lifecycle_status = 'running', updated_at = now()
			WHERE a.id IN (SELECT application_id FROM failed) AND a.lifecycle_status = 'build'
				AND EXISTS (SELECT 1 FROM deployments d WHERE d.application_id = a.id AND d.status = 'running')
			RETURNING a.id
		)
		SELECT count(*) FROM failed`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("close interrupted builds: %w", err)
	}
	return n, nil
}
