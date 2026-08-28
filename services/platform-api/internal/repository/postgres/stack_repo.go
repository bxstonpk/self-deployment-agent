package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type StackRepo struct {
	pool *pgxpool.Pool
}

func NewStackRepo(pool *pgxpool.Pool) *StackRepo {
	return &StackRepo{pool: pool}
}

func (r *StackRepo) List(ctx context.Context) ([]domain.SupportedStack, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, kind, name, status FROM supported_stacks ORDER BY kind, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list supported stacks: %w", err)
	}
	defer rows.Close()

	var out []domain.SupportedStack
	for rows.Next() {
		var s domain.SupportedStack
		var kind string
		if err := rows.Scan(&s.ID, &kind, &s.Name, &s.Status); err != nil {
			return nil, fmt.Errorf("scan supported stack row: %w", err)
		}
		s.Kind = domain.StackKind(kind)
		out = append(out, s)
	}
	return out, rows.Err()
}

// FindKind returns the kind a runtime name is registered under (checking
// frontend then backend) and whether it is currently active. Used by
// validation to determine whether a declared service needs a `port`
// (backend/API runtimes do, frontend runtimes don't) without requiring the
// author to redundantly declare the kind in deployment.yaml.
func (r *StackRepo) FindKind(ctx context.Context, name string) (kind domain.StackKind, allowed bool, err error) {
	for _, k := range []domain.StackKind{domain.StackKindFrontend, domain.StackKindBackend} {
		ok, err := r.IsAllowed(ctx, k, name)
		if err != nil {
			return "", false, err
		}
		if ok {
			return k, true, nil
		}
	}
	return "", false, nil
}

// IsAllowed implements the FR-021/FR-030 check: is `name` present in the
// catalog for `kind` with status='active'? Deprecated/blocked entries
// deliberately do NOT count as allowed (FR-022: blocking prevents new
// deployments outright; a fuller implementation would distinguish
// "deprecated: allowed with a warning" from "blocked: rejected", but that
// warning/notification path needs Module X, not yet implemented).
func (r *StackRepo) IsAllowed(ctx context.Context, kind domain.StackKind, name string) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM supported_stacks WHERE kind = $1 AND name = $2 AND status = 'active'
		)
	`, string(kind), name).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check stack allowed: %w", err)
	}
	return allowed, nil
}
