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

// GovernanceRepo stores development governance (migration 0024).
type GovernanceRepo struct {
	pool *pgxpool.Pool
}

func NewGovernanceRepo(pool *pgxpool.Pool) *GovernanceRepo {
	return &GovernanceRepo{pool: pool}
}

// notFoundOnBadID makes a malformed (non-UUID) id read as "no such row",
// which is what it is to the caller.
func notFoundOnBadID(err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
		return domain.ErrNotFound
	}
	return err
}

const profileColumns = `application_id, purpose, users_description, handles_personal_data, data_description,
	frontend, backend, database_engine, updated_by, updated_at`

func scanProfile(row pgx.Row) (domain.ApplicationProfile, error) {
	var p domain.ApplicationProfile
	err := row.Scan(&p.ApplicationID, &p.Purpose, &p.UsersDescription, &p.HandlesPersonalData, &p.DataDescription,
		&p.Frontend, &p.Backend, &p.Database, &p.UpdatedBy, &p.UpdatedAt)
	return p, err
}

func (r *GovernanceRepo) GetProfile(ctx context.Context, applicationID string) (domain.ApplicationProfile, error) {
	p, err := scanProfile(r.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM application_profiles WHERE application_id = $1`, applicationID))
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.ApplicationProfile{}, e
		}
		return domain.ApplicationProfile{}, fmt.Errorf("get application profile: %w", err)
	}
	return p, nil
}

func (r *GovernanceRepo) PutProfile(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error) {
	saved, err := scanProfile(r.pool.QueryRow(ctx, `
		INSERT INTO application_profiles (application_id, purpose, users_description, handles_personal_data,
			data_description, frontend, backend, database_engine, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (application_id) DO UPDATE SET
			purpose = EXCLUDED.purpose, users_description = EXCLUDED.users_description,
			handles_personal_data = EXCLUDED.handles_personal_data, data_description = EXCLUDED.data_description,
			frontend = EXCLUDED.frontend, backend = EXCLUDED.backend, database_engine = EXCLUDED.database_engine,
			updated_by = EXCLUDED.updated_by, updated_at = now()
		RETURNING `+profileColumns,
		p.ApplicationID, p.Purpose, p.UsersDescription, p.HandlesPersonalData, p.DataDescription,
		p.Frontend, p.Backend, p.Database, p.UpdatedBy))
	if err != nil {
		return domain.ApplicationProfile{}, fmt.Errorf("save application profile: %w", err)
	}
	return saved, nil
}

func (r *GovernanceRepo) ListDependencyRules(ctx context.Context) ([]domain.DependencyRule, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.ecosystem, d.package, d.reason, d.created_by, d.created_at, COALESCE(u.email, '')
		FROM dependency_rules d LEFT JOIN users u ON u.id = d.created_by
		ORDER BY d.ecosystem, d.package`)
	if err != nil {
		return nil, fmt.Errorf("list dependency rules: %w", err)
	}
	defer rows.Close()
	out := []domain.DependencyRule{}
	for rows.Next() {
		var d domain.DependencyRule
		if err := rows.Scan(&d.ID, &d.Ecosystem, &d.Package, &d.Reason, &d.CreatedBy, &d.CreatedAt, &d.CreatedByEmail); err != nil {
			return nil, fmt.Errorf("scan dependency rule: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *GovernanceRepo) AddDependencyRule(ctx context.Context, d domain.DependencyRule) (domain.DependencyRule, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO dependency_rules (ecosystem, package, reason, created_by) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, d.Ecosystem, d.Package, d.Reason, d.CreatedBy).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.DependencyRule{}, domain.ErrDependencyRuleExists
		}
		return domain.DependencyRule{}, fmt.Errorf("add dependency rule: %w", err)
	}
	return d, nil
}

func (r *GovernanceRepo) DeleteDependencyRule(ctx context.Context, id string) (domain.DependencyRule, error) {
	var d domain.DependencyRule
	err := r.pool.QueryRow(ctx, `
		DELETE FROM dependency_rules WHERE id = $1
		RETURNING id, ecosystem, package, reason, created_by, created_at`, id,
	).Scan(&d.ID, &d.Ecosystem, &d.Package, &d.Reason, &d.CreatedBy, &d.CreatedAt)
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.DependencyRule{}, e
		}
		return domain.DependencyRule{}, fmt.Errorf("delete dependency rule: %w", err)
	}
	return d, nil
}

const exceptionSelect = `
	SELECT e.id, e.application_id, e.requested_by, e.kind, e.subject, e.reason, e.status, e.decided_by,
		e.decision_reason, e.created_at, e.decided_at, a.name, COALESCE(ru.email, ''), COALESCE(du.email, '')
	FROM standard_exceptions e
	JOIN applications a ON a.id = e.application_id
	LEFT JOIN users ru ON ru.id = e.requested_by
	LEFT JOIN users du ON du.id = e.decided_by`

func scanException(row pgx.Row) (domain.StandardException, error) {
	var e domain.StandardException
	err := row.Scan(&e.ID, &e.ApplicationID, &e.RequestedBy, &e.Kind, &e.Subject, &e.Reason, &e.Status, &e.DecidedBy,
		&e.DecisionReason, &e.CreatedAt, &e.DecidedAt, &e.ApplicationName, &e.RequestedByEmail, &e.DecidedByEmail)
	return e, err
}

func (r *GovernanceRepo) queryExceptions(ctx context.Context, where string, arg any) ([]domain.StandardException, error) {
	rows, err := r.pool.Query(ctx, exceptionSelect+" WHERE "+where+" ORDER BY e.created_at DESC", arg)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return []domain.StandardException{}, nil
		}
		return nil, fmt.Errorf("list standard exceptions: %w", err)
	}
	defer rows.Close()
	out := []domain.StandardException{}
	for rows.Next() {
		e, err := scanException(rows)
		if err != nil {
			return nil, fmt.Errorf("scan standard exception: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *GovernanceRepo) CreateException(ctx context.Context, e domain.StandardException) (domain.StandardException, error) {
	var id string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO standard_exceptions (application_id, requested_by, kind, subject, reason)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		e.ApplicationID, e.RequestedBy, e.Kind, e.Subject, e.Reason).Scan(&id); err != nil {
		return domain.StandardException{}, fmt.Errorf("request standard exception: %w", err)
	}
	return r.GetException(ctx, id)
}

func (r *GovernanceRepo) GetException(ctx context.Context, id string) (domain.StandardException, error) {
	e, err := scanException(r.pool.QueryRow(ctx, exceptionSelect+" WHERE e.id = $1", id))
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.StandardException{}, e
		}
		return domain.StandardException{}, fmt.Errorf("get standard exception: %w", err)
	}
	return e, nil
}

func (r *GovernanceRepo) ListExceptionsForApplication(ctx context.Context, applicationID string) ([]domain.StandardException, error) {
	return r.queryExceptions(ctx, "e.application_id = $1", applicationID)
}

// ListExceptionsByStatus is the administrators' queue and history. A
// deleted application (kept as a row, lifecycle_status 'deleted') has
// nothing left to decide, so its pending requests leave the queue; its
// decided ones stay in the history.
func (r *GovernanceRepo) ListExceptionsByStatus(ctx context.Context, status string) ([]domain.StandardException, error) {
	return r.queryExceptions(ctx, "e.status = $1 AND (e.status <> 'pending' OR a.lifecycle_status <> 'deleted')", status)
}

func (r *GovernanceRepo) DecideException(ctx context.Context, id, status, decidedBy, reason string) (domain.StandardException, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE standard_exceptions SET status = $2, decided_by = $3, decision_reason = $4, decided_at = now()
		WHERE id = $1 AND status = 'pending'`, id, status, decidedBy, reason)
	if err != nil {
		if e := notFoundOnBadID(err); errors.Is(e, domain.ErrNotFound) {
			return domain.StandardException{}, e
		}
		return domain.StandardException{}, fmt.Errorf("decide standard exception: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := r.GetException(ctx, id); err != nil {
			return domain.StandardException{}, err
		}
		return domain.StandardException{}, domain.ErrExceptionNotPending
	}
	return r.GetException(ctx, id)
}
