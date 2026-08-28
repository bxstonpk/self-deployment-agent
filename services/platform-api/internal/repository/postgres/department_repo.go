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

// GetOrCreateByName supports the dev-mode authenticator (no IdP yet — see
// DEC-001 in docs/17_Decision_Log.md), which needs a Department row to
// attach a locally-provisioned dev User to.
func (r *DepartmentRepo) GetOrCreateByName(ctx context.Context, name string) (domain.Department, error) {
	var d domain.Department
	err := r.pool.QueryRow(ctx, `
		INSERT INTO departments (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, COALESCE(cost_center_code, ''), status, created_at
	`, name).Scan(&d.ID, &d.Name, &d.CostCenterCode, &d.Status, &d.CreatedAt)
	if err != nil && err != pgx.ErrNoRows {
		return domain.Department{}, fmt.Errorf("get or create department: %w", err)
	}
	return d, nil
}
