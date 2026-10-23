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

type DepartmentRepo struct {
	pool *pgxpool.Pool
}

func NewDepartmentRepo(pool *pgxpool.Pool) *DepartmentRepo {
	return &DepartmentRepo{pool: pool}
}

func (r *DepartmentRepo) Exists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM departments WHERE id = $1)`, id,
	).Scan(&exists)
	if err != nil {
		// A malformed (non-UUID) id is, from the caller's perspective, just
		// "not a real department" — not a server error. Postgres reports
		// this as invalid_text_representation (22P02).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return false, nil
		}
		return false, fmt.Errorf("check department exists: %w", err)
	}
	return exists, nil
}

// List returns every department, ordered by name. Needed so a caller (the
// MCP server's create_application tool in particular — see
// services/mcp-server — which only receives a department NAME per
// docs/07_MCP_Requirements.md Section 13.4) can resolve a department name
// to the UUID Applications.Register actually requires.
func (r *DepartmentRepo) List(ctx context.Context) ([]domain.Department, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, COALESCE(cost_center_code, ''), status, created_at
		FROM departments ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	defer rows.Close()

	var departments []domain.Department
	for rows.Next() {
		var d domain.Department
		if err := rows.Scan(&d.ID, &d.Name, &d.CostCenterCode, &d.Status, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan department: %w", err)
		}
		departments = append(departments, d)
	}
	return departments, rows.Err()
}

// GetOrCreateByName returns the department called name, creating it if
// there is none: how registration, the dev-mode authenticator and anyone
// adding a department (POST /departments, the MCP create_application tool)
// all reach the same row. The name is normalized first and matched without
// regard to case, so "innovation" finds "Innovation" rather than creating
// a second one.
func (r *DepartmentRepo) GetOrCreateByName(ctx context.Context, name string) (domain.Department, error) {
	name, err := domain.NormalizeDepartmentName(name)
	if err != nil {
		return domain.Department{}, err
	}
	var d domain.Department
	err = r.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(cost_center_code, ''), status, created_at
		FROM departments WHERE lower(name) = lower($1)
		ORDER BY created_at LIMIT 1
	`, name).Scan(&d.ID, &d.Name, &d.CostCenterCode, &d.Status, &d.CreatedAt)
	if err == nil {
		return d, nil
	}
	if err != pgx.ErrNoRows {
		return domain.Department{}, fmt.Errorf("find department: %w", err)
	}
	err = r.pool.QueryRow(ctx, `
		INSERT INTO departments (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, COALESCE(cost_center_code, ''), status, created_at
	`, name).Scan(&d.ID, &d.Name, &d.CostCenterCode, &d.Status, &d.CreatedAt)
	if err != nil && err != pgx.ErrNoRows {
		return domain.Department{}, fmt.Errorf("get or create department: %w", err)
	}
	return d, nil
}

// ListSummaries is every department with how many people and (not deleted)
// applications belong to it, for the Portal's departments page.
func (r *DepartmentRepo) ListSummaries(ctx context.Context) ([]domain.DepartmentSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.name, COALESCE(d.cost_center_code, ''), d.status, d.created_at,
		       (SELECT COUNT(*) FROM users u WHERE u.department_id = d.id),
		       (SELECT COUNT(*) FROM applications a WHERE a.owning_department_id = d.id AND a.lifecycle_status <> 'deleted')
		FROM departments d ORDER BY d.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list department summaries: %w", err)
	}
	defer rows.Close()

	var out []domain.DepartmentSummary
	for rows.Next() {
		var s domain.DepartmentSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.CostCenterCode, &s.Status, &s.CreatedAt, &s.Users, &s.Applications); err != nil {
			return nil, fmt.Errorf("scan department summary: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
