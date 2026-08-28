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

type ApplicationRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationRepo(pool *pgxpool.Pool) *ApplicationRepo {
	return &ApplicationRepo{pool: pool}
}

func (r *ApplicationRepo) Create(ctx context.Context, app domain.Application) (domain.Application, error) {
	var out domain.Application
	var status string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO applications (name, description, owning_department_id, created_by, lifecycle_status, deployment_yaml_draft)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, description, owning_department_id, created_by, lifecycle_status,
		          COALESCE(deployment_yaml_draft, ''), created_at, updated_at
	`, app.Name, app.Description, app.OwningDepartmentID, app.CreatedBy, string(app.LifecycleStatus), app.DeploymentYAMLDraft,
	).Scan(&out.ID, &out.Name, &out.Description, &out.OwningDepartmentID, &out.CreatedBy, &status,
		&out.DeploymentYAMLDraft, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return domain.Application{}, fmt.Errorf("insert application: %w", err)
	}
	out.LifecycleStatus = domain.LifecycleStatus(status)
	return out, nil
}

func (r *ApplicationRepo) GetByID(ctx context.Context, id string) (domain.Application, error) {
	return r.scanOne(ctx, `
		SELECT id, name, description, owning_department_id, created_by, lifecycle_status,
		       COALESCE(deployment_yaml_draft, ''), created_at, updated_at
		FROM applications WHERE id = $1
	`, id)
}

func (r *ApplicationRepo) NameExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM applications WHERE name = $1)`, name,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check application name exists: %w", err)
	}
	return exists, nil
}

func (r *ApplicationRepo) List(ctx context.Context, limit, offset int) ([]domain.Application, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, owning_department_id, created_by, lifecycle_status,
		       COALESCE(deployment_yaml_draft, ''), created_at, updated_at
		FROM applications
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer rows.Close()

	var out []domain.Application
	for rows.Next() {
		var app domain.Application
		var status string
		if err := rows.Scan(&app.ID, &app.Name, &app.Description, &app.OwningDepartmentID, &app.CreatedBy,
			&status, &app.DeploymentYAMLDraft, &app.CreatedAt, &app.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan application row: %w", err)
		}
		app.LifecycleStatus = domain.LifecycleStatus(status)
		out = append(out, app)
	}
	return out, rows.Err()
}

func (r *ApplicationRepo) UpdateMetadata(ctx context.Context, id, description string) (domain.Application, error) {
	return r.scanOne(ctx, `
		UPDATE applications SET description = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, name, description, owning_department_id, created_by, lifecycle_status,
		          COALESCE(deployment_yaml_draft, ''), created_at, updated_at
	`, id, description)
}

func (r *ApplicationRepo) scanOne(ctx context.Context, query string, args ...any) (domain.Application, error) {
	var app domain.Application
	var status string
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&app.ID, &app.Name, &app.Description, &app.OwningDepartmentID, &app.CreatedBy,
		&status, &app.DeploymentYAMLDraft, &app.CreatedAt, &app.UpdatedAt,
	)
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		// 22P02 (invalid_text_representation): a malformed id can't match
		// any row, so treat it the same as "not found" rather than a 500.
		return domain.Application{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Application{}, fmt.Errorf("query application: %w", err)
	}
	app.LifecycleStatus = domain.LifecycleStatus(status)
	return app, nil
}
