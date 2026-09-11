package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// ApplicationSecretRepo stores Module O's secrets. It only ever sees
// ciphertext: sealing and opening happen in the service layer, so nothing
// in this file could leak a value even if it tried.
type ApplicationSecretRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationSecretRepo(pool *pgxpool.Pool) *ApplicationSecretRepo {
	return &ApplicationSecretRepo{pool: pool}
}

const applicationSecretColumns = `id, application_id, name, managed_by, ciphertext, key_id, version,
	created_by, updated_by, created_at, updated_at`

func scanApplicationSecret(row rowScanner, s *domain.ApplicationSecret) error {
	var managedBy string
	if err := row.Scan(
		&s.ID, &s.ApplicationID, &s.Name, &managedBy, &s.Ciphertext, &s.KeyID, &s.Version,
		&s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return err
	}
	s.ManagedBy = domain.SecretManagedBy(managedBy)
	return nil
}

// Upsert stores a value, replacing any previous one of the same name and
// bumping its version. The WHERE on the conflict branch means a write can
// only replace a secret with the same manager: nothing an employee submits
// can overwrite a value the platform depends on, even if the service
// layer's reserved-name check were somehow bypassed. When that guard stops
// a write, no row comes back — reported as ErrSecretManagedByPlatform,
// since reserved names make the reverse case unreachable.
func (r *ApplicationSecretRepo) Upsert(ctx context.Context, s domain.ApplicationSecret) (domain.ApplicationSecret, error) {
	var out domain.ApplicationSecret
	err := scanApplicationSecret(r.pool.QueryRow(ctx, `
		INSERT INTO application_secrets (application_id, name, managed_by, ciphertext, key_id, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (application_id, name) DO UPDATE SET
			ciphertext = EXCLUDED.ciphertext,
			key_id     = EXCLUDED.key_id,
			version    = application_secrets.version + 1,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		WHERE application_secrets.managed_by = EXCLUDED.managed_by
		RETURNING `+applicationSecretColumns,
		s.ApplicationID, s.Name, string(s.ManagedBy), s.Ciphertext, s.KeyID, s.UpdatedBy,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationSecret{}, domain.ErrSecretManagedByPlatform
	}
	if err != nil {
		return domain.ApplicationSecret{}, fmt.Errorf("store secret: %w", err)
	}
	return out, nil
}

func (r *ApplicationSecretRepo) Get(ctx context.Context, applicationID, name string) (domain.ApplicationSecret, error) {
	var out domain.ApplicationSecret
	err := scanApplicationSecret(r.pool.QueryRow(ctx, `
		SELECT `+applicationSecretColumns+` FROM application_secrets
		WHERE application_id = $1 AND name = $2
	`, applicationID, name), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationSecret{}, domain.ErrSecretNotFound
	}
	if err != nil {
		return domain.ApplicationSecret{}, fmt.Errorf("get secret: %w", err)
	}
	return out, nil
}

// ListForApplication is always scoped by application id — there is no
// query in this file that reads secrets across applications (FR-069).
func (r *ApplicationSecretRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationSecret, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+applicationSecretColumns+` FROM application_secrets
		WHERE application_id = $1
		ORDER BY name
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	defer rows.Close()

	var out []domain.ApplicationSecret
	for rows.Next() {
		var s domain.ApplicationSecret
		if err := scanApplicationSecret(rows, &s); err != nil {
			return nil, fmt.Errorf("scan secret: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Delete removes one secret, but only one with the given manager — the same
// guard as Upsert, for the same reason.
func (r *ApplicationSecretRepo) Delete(ctx context.Context, applicationID, name string, managedBy domain.SecretManagedBy) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM application_secrets
		WHERE application_id = $1 AND name = $2 AND managed_by = $3
	`, applicationID, name, string(managedBy))
	if err != nil {
		return false, fmt.Errorf("delete secret: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *ApplicationSecretRepo) DeleteAllForApplication(ctx context.Context, applicationID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM application_secrets WHERE application_id = $1`, applicationID)
	if err != nil {
		return 0, fmt.Errorf("delete application secrets: %w", err)
	}
	return tag.RowsAffected(), nil
}
