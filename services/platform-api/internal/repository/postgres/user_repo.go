package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		SELECT id, full_name, email, COALESCE(department_id::text, ''), status, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.FullName, &u.Email, &u.DepartmentID, &u.Status, &u.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by id: %w", domain.ErrNotFound)
	}
	return u, nil
}

// GetOrCreateByEmail backs the dev-mode authenticator: until an IdP is
// wired up (DEC-001, docs/17_Decision_Log.md), a caller identifies
// themselves via the X-Dev-User-Email header and is upserted here.
// Note: department_id is only set on first creation, not updated on
// subsequent calls — acceptable for a dev-mode stub, not for the real
// SSO-backed path this will be replaced by.
func (r *UserRepo) GetOrCreateByEmail(ctx context.Context, email, fullName, departmentID string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (full_name, email, department_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (email) DO UPDATE SET last_modified_at = now()
		RETURNING id, full_name, email, COALESCE(department_id::text, ''), status, created_at
	`, fullName, email, departmentID).Scan(&u.ID, &u.FullName, &u.Email, &u.DepartmentID, &u.Status, &u.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("get or create user: %w", err)
	}
	return u, nil
}
