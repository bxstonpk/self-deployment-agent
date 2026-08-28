package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type BaseImageRepo struct {
	pool *pgxpool.Pool
}

func NewBaseImageRepo(pool *pgxpool.Pool) *BaseImageRepo {
	return &BaseImageRepo{pool: pool}
}

func (r *BaseImageRepo) GetForRuntime(ctx context.Context, runtime string) (domain.BaseImage, error) {
	var b domain.BaseImage
	err := r.pool.QueryRow(ctx, `
		SELECT id, runtime, image_reference, status FROM base_images WHERE runtime = $1
	`, runtime).Scan(&b.ID, &b.Runtime, &b.ImageReference, &b.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BaseImage{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BaseImage{}, fmt.Errorf("query base image: %w", err)
	}
	return b, nil
}
