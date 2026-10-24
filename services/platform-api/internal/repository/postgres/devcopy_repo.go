package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// DevCopyRepo stores dev copies (migration 0026).
type DevCopyRepo struct {
	pool *pgxpool.Pool
}

func NewDevCopyRepo(pool *pgxpool.Pool) *DevCopyRepo {
	return &DevCopyRepo{pool: pool}
}

const devCopySelect = `
	SELECT c.application_id, c.parent_id, c.created_by, c.created_at, c.ended_at, c.test_data, c.test_data_note,
	       a.name, parent.name
	FROM dev_copies c
	JOIN applications a ON a.id = c.application_id
	JOIN applications parent ON parent.id = c.parent_id`

func scanDevCopy(row pgx.Row) (domain.DevCopy, error) {
	var c domain.DevCopy
	err := row.Scan(&c.ApplicationID, &c.ParentID, &c.CreatedBy, &c.CreatedAt, &c.EndedAt, &c.TestData, &c.TestDataNote,
		&c.Name, &c.ParentName)
	return c, err
}

func (r *DevCopyRepo) one(ctx context.Context, where string, arg any) (domain.DevCopy, error) {
	c, err := scanDevCopy(r.pool.QueryRow(ctx, devCopySelect+" WHERE "+where, arg))
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.DevCopy{}, e
		}
		return domain.DevCopy{}, fmt.Errorf("read dev copy: %w", err)
	}
	return c, nil
}

func (r *DevCopyRepo) Live(ctx context.Context, parentID string) (domain.DevCopy, error) {
	return r.one(ctx, "c.parent_id = $1 AND c.ended_at IS NULL", parentID)
}

func (r *DevCopyRepo) Of(ctx context.Context, applicationID string) (domain.DevCopy, error) {
	return r.one(ctx, "c.application_id = $1", applicationID)
}

func (r *DevCopyRepo) Create(ctx context.Context, c domain.DevCopy) (domain.DevCopy, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO dev_copies (application_id, parent_id, created_by) VALUES ($1, $2, $3) RETURNING created_at`,
		c.ApplicationID, c.ParentID, c.CreatedBy).Scan(&c.CreatedAt)
	if err != nil {
		return domain.DevCopy{}, fmt.Errorf("create dev copy: %w", err)
	}
	return c, nil
}

func (r *DevCopyRepo) End(ctx context.Context, applicationID string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE dev_copies SET ended_at = now() WHERE application_id = $1 AND ended_at IS NULL`,
		applicationID); err != nil {
		return fmt.Errorf("end dev copy: %w", err)
	}
	return nil
}

func (r *DevCopyRepo) Orphaned(ctx context.Context) ([]domain.DevCopy, error) {
	rows, err := r.pool.Query(ctx, devCopySelect+`
		WHERE c.ended_at IS NULL AND parent.lifecycle_status IN ('archived', 'deleted')`)
	if err != nil {
		return nil, fmt.Errorf("list orphaned dev copies: %w", err)
	}
	defer rows.Close()
	var out []domain.DevCopy
	for rows.Next() {
		c, err := scanDevCopy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dev copy: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *DevCopyRepo) AddTestData(ctx context.Context, applicationID, note string) (domain.DevCopy, error) {
	if _, err := r.pool.Exec(ctx, `
		UPDATE dev_copies SET test_data = true,
		       test_data_note = CASE WHEN test_data_note = '' THEN $2 ELSE test_data_note || E'\n' || $2 END
		WHERE application_id = $1`, applicationID, note); err != nil {
		return domain.DevCopy{}, fmt.Errorf("record test data: %w", err)
	}
	return r.Of(ctx, applicationID)
}
