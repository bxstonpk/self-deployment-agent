package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// PreviewRepo stores previews (migration 0026).
type PreviewRepo struct {
	pool *pgxpool.Pool
}

func NewPreviewRepo(pool *pgxpool.Pool) *PreviewRepo {
	return &PreviewRepo{pool: pool}
}

const previewSelect = `
	SELECT p.application_id, p.parent_id, p.created_by, p.created_at, p.expires_at, p.ended_at, a.name, parent.name
	FROM application_previews p
	JOIN applications a ON a.id = p.application_id
	JOIN applications parent ON parent.id = p.parent_id`

func scanPreview(row pgx.Row) (domain.Preview, error) {
	var p domain.Preview
	err := row.Scan(&p.ApplicationID, &p.ParentID, &p.CreatedBy, &p.CreatedAt, &p.ExpiresAt, &p.EndedAt, &p.Name, &p.ParentName)
	return p, err
}

func (r *PreviewRepo) one(ctx context.Context, where string, arg any) (domain.Preview, error) {
	p, err := scanPreview(r.pool.QueryRow(ctx, previewSelect+" WHERE "+where, arg))
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.Preview{}, e
		}
		return domain.Preview{}, fmt.Errorf("read preview: %w", err)
	}
	return p, nil
}

func (r *PreviewRepo) Live(ctx context.Context, parentID string) (domain.Preview, error) {
	return r.one(ctx, "p.parent_id = $1 AND p.ended_at IS NULL", parentID)
}

func (r *PreviewRepo) Of(ctx context.Context, applicationID string) (domain.Preview, error) {
	return r.one(ctx, "p.application_id = $1", applicationID)
}

func (r *PreviewRepo) Create(ctx context.Context, p domain.Preview) (domain.Preview, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO application_previews (application_id, parent_id, created_by, expires_at) VALUES ($1, $2, $3, $4)
		RETURNING created_at`, p.ApplicationID, p.ParentID, p.CreatedBy, p.ExpiresAt).Scan(&p.CreatedAt)
	if err != nil {
		return domain.Preview{}, fmt.Errorf("create preview: %w", err)
	}
	return p, nil
}

func (r *PreviewRepo) End(ctx context.Context, applicationID string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE application_previews SET ended_at = now() WHERE application_id = $1 AND ended_at IS NULL`,
		applicationID); err != nil {
		return fmt.Errorf("end preview: %w", err)
	}
	return nil
}

func (r *PreviewRepo) Due(ctx context.Context, now time.Time) ([]domain.Preview, error) {
	rows, err := r.pool.Query(ctx, previewSelect+`
		WHERE p.ended_at IS NULL AND (p.expires_at <= $1 OR parent.lifecycle_status IN ('archived', 'deleted'))
		ORDER BY p.expires_at`, now)
	if err != nil {
		return nil, fmt.Errorf("list due previews: %w", err)
	}
	defer rows.Close()
	var out []domain.Preview
	for rows.Next() {
		p, err := scanPreview(rows)
		if err != nil {
			return nil, fmt.Errorf("scan preview: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
