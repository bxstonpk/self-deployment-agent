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

const applicationColumns = `id, name, description, owning_department_id, created_by, lifecycle_status,
	COALESCE(deployment_yaml_draft, ''), created_at, updated_at, validated_at`

func (r *ApplicationRepo) Create(ctx context.Context, app domain.Application) (domain.Application, error) {
	return r.scanOne(ctx, `
		INSERT INTO applications (name, description, owning_department_id, created_by, lifecycle_status, deployment_yaml_draft)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+applicationColumns,
		app.Name, app.Description, app.OwningDepartmentID, app.CreatedBy, string(app.LifecycleStatus), app.DeploymentYAMLDraft,
	)
}

func (r *ApplicationRepo) GetByID(ctx context.Context, id string) (domain.Application, error) {
	return r.scanOne(ctx, `SELECT `+applicationColumns+` FROM applications WHERE id = $1`, id)
}

// GetByName supports the scale-to-zero proxy (internal/scaleproxy), which
// routes public traffic by application name (readable in a URL) rather
// than UUID.
func (r *ApplicationRepo) GetByName(ctx context.Context, name string) (domain.Application, error) {
	app, err := r.scanOne(ctx, `SELECT `+applicationColumns+` FROM applications WHERE name = $1`, name)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Application{}, domain.ErrApplicationNotFound
	}
	return app, err
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
		SELECT `+applicationColumns+`
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
		app, err := scanApplicationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan application row: %w", err)
		}
		out = append(out, app)
	}
	return out, rows.Err()
}

func (r *ApplicationRepo) UpdateMetadata(ctx context.Context, id, description string) (domain.Application, error) {
	return r.scanOne(ctx, `
		UPDATE applications SET description = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+applicationColumns,
		id, description)
}

// UpdateDeploymentYAML implements the save half of FR-023: persists a new
// draft. Per FR-024's implication that a config change invalidates any
// prior validation, this also clears validated_at and reverts a previously
// Validated application back to Draft — only Draft/Validated applications
// are editable this way (guarded by the WHERE clause; see
// domain.ErrInvalidLifecycleTransition).
func (r *ApplicationRepo) UpdateDeploymentYAML(ctx context.Context, id, yamlContent string) (domain.Application, error) {
	app, err := r.scanOne(ctx, `
		UPDATE applications
		SET deployment_yaml_draft = $2, validated_at = NULL, lifecycle_status = 'draft', updated_at = now()
		WHERE id = $1 AND lifecycle_status IN ('draft', 'validated')
		RETURNING `+applicationColumns,
		id, yamlContent)
	if errors.Is(err, domain.ErrNotFound) {
		// Distinguish "no such application" from "exists but not editable
		// in its current state" so the handler can return the right code.
		exists, existsErr := r.exists(ctx, id)
		if existsErr == nil && exists {
			return domain.Application{}, domain.ErrInvalidLifecycleTransition
		}
	}
	return app, err
}

// UpdateLifecycleStatus performs a guarded state transition: it only
// succeeds if the application is currently in `from`. This is the
// enforcement point for FR-045 (Lifecycle State Model Enforcement) as it
// applies to the Draft -> Validated edge implemented so far.
func (r *ApplicationRepo) UpdateLifecycleStatus(ctx context.Context, id string, from, to domain.LifecycleStatus, markValidated bool) (domain.Application, error) {
	query := `
		UPDATE applications
		SET lifecycle_status = $3, updated_at = now()`
	if markValidated {
		query += `, validated_at = now()`
	}
	query += `
		WHERE id = $1 AND lifecycle_status = $2
		RETURNING ` + applicationColumns

	app, err := r.scanOne(ctx, query, id, string(from), string(to))
	if errors.Is(err, domain.ErrNotFound) {
		exists, existsErr := r.exists(ctx, id)
		if existsErr == nil && exists {
			return domain.Application{}, domain.ErrInvalidLifecycleTransition
		}
	}
	return app, err
}

func (r *ApplicationRepo) exists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM applications WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

func (r *ApplicationRepo) scanOne(ctx context.Context, query string, args ...any) (domain.Application, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	app, err := scanApplicationRow(row)
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		// 22P02 (invalid_text_representation): a malformed id can't match
		// any row, so treat it the same as "not found" rather than a 500.
		return domain.Application{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Application{}, fmt.Errorf("query application: %w", err)
	}
	return app, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanApplicationRow(row rowScanner) (domain.Application, error) {
	var app domain.Application
	var status string
	err := row.Scan(&app.ID, &app.Name, &app.Description, &app.OwningDepartmentID, &app.CreatedBy,
		&status, &app.DeploymentYAMLDraft, &app.CreatedAt, &app.UpdatedAt, &app.ValidatedAt)
	if err != nil {
		return domain.Application{}, err
	}
	app.LifecycleStatus = domain.LifecycleStatus(status)
	return app, nil
}
