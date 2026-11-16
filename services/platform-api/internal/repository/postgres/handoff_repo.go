package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// HandoffRepo records IT handoff packages (DEC-033); the files are on the
// handoff volume.
type HandoffRepo struct {
	pool *pgxpool.Pool
}

func NewHandoffRepo(pool *pgxpool.Pool) *HandoffRepo {
	return &HandoffRepo{pool: pool}
}

const handoffColumns = `id, application_id, deployment_id, trigger, COALESCE(requested_by::text, ''), status, error,
	file_name, size_bytes, sha256, created_at, completed_at`

func scanHandoff(row rowScanner, p *domain.HandoffPackage) error {
	var trigger, status string
	if err := row.Scan(&p.ID, &p.ApplicationID, &p.DeploymentID, &trigger, &p.RequestedBy, &status, &p.Error,
		&p.FileName, &p.SizeBytes, &p.SHA256, &p.CreatedAt, &p.CompletedAt); err != nil {
		return err
	}
	p.Trigger, p.Status = domain.HandoffTrigger(trigger), domain.HandoffStatus(status)
	return nil
}

func (r *HandoffRepo) Create(ctx context.Context, p domain.HandoffPackage) (domain.HandoffPackage, error) {
	var out domain.HandoffPackage
	err := scanHandoff(r.pool.QueryRow(ctx, `
		INSERT INTO handoff_packages (application_id, deployment_id, trigger, requested_by, status)
		VALUES ($1, $2, $3, $4, 'building')
		RETURNING `+handoffColumns,
		p.ApplicationID, p.DeploymentID, string(p.Trigger), nullable(p.RequestedBy)), &out)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "one_building_handoff_per_application" {
		return domain.HandoffPackage{}, domain.ErrHandoffInProgress
	}
	if err != nil {
		return domain.HandoffPackage{}, fmt.Errorf("record handoff package: %w", err)
	}
	return out, nil
}

func (r *HandoffRepo) Complete(ctx context.Context, id, fileName string, size int64, sha256 string) (domain.HandoffPackage, error) {
	var out domain.HandoffPackage
	err := scanHandoff(r.pool.QueryRow(ctx, `
		UPDATE handoff_packages SET status = 'ready', file_name = $2, size_bytes = $3, sha256 = $4, completed_at = now()
		WHERE id = $1 AND status = 'building'
		RETURNING `+handoffColumns, id, fileName, size, sha256), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HandoffPackage{}, domain.ErrHandoffNotFound
	}
	if err != nil {
		return domain.HandoffPackage{}, fmt.Errorf("complete handoff package: %w", err)
	}
	return out, nil
}

func (r *HandoffRepo) Fail(ctx context.Context, id, reason string) error {
	if reason == "" {
		reason = "failed"
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE handoff_packages SET status = 'failed', error = $2, completed_at = now()
		WHERE id = $1 AND status = 'building'`, id, reason)
	if err != nil {
		return fmt.Errorf("fail handoff package: %w", err)
	}
	return nil
}

func (r *HandoffRepo) ReplaceOlder(ctx context.Context, applicationID, keepID string) ([]domain.HandoffPackage, error) {
	return r.list(ctx, `
		UPDATE handoff_packages SET status = 'replaced'
		WHERE application_id = $1 AND id <> $2 AND status = 'ready'
		RETURNING `+handoffColumns, applicationID, keepID)
}

func (r *HandoffRepo) GetByID(ctx context.Context, id string) (domain.HandoffPackage, error) {
	var out domain.HandoffPackage
	err := scanHandoff(r.pool.QueryRow(ctx, `SELECT `+handoffColumns+` FROM handoff_packages WHERE id = $1`, id), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HandoffPackage{}, domain.ErrHandoffNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" { // not a UUID
		return domain.HandoffPackage{}, domain.ErrHandoffNotFound
	}
	if err != nil {
		return domain.HandoffPackage{}, fmt.Errorf("get handoff package: %w", err)
	}
	return out, nil
}

func (r *HandoffRepo) Current(ctx context.Context) ([]domain.HandoffPackage, error) {
	return r.list(ctx, `
		SELECT `+handoffColumns+` FROM handoff_packages p
		WHERE p.status = 'ready'
		   OR p.id = (SELECT q.id FROM handoff_packages q WHERE q.application_id = p.application_id
		              ORDER BY q.created_at DESC LIMIT 1)
		ORDER BY p.application_id, p.created_at DESC`)
}

func (r *HandoffRepo) FailUnfinished(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE handoff_packages SET status = 'failed', error = 'the platform restarted while the package was being made',
			completed_at = now()
		WHERE status = 'building'`)
	if err != nil {
		return 0, fmt.Errorf("close interrupted handoff packages: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *HandoffRepo) list(ctx context.Context, query string, args ...any) ([]domain.HandoffPackage, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list handoff packages: %w", err)
	}
	defer rows.Close()
	out := []domain.HandoffPackage{}
	for rows.Next() {
		var p domain.HandoffPackage
		if err := scanHandoff(rows, &p); err != nil {
			return nil, fmt.Errorf("scan handoff package: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
