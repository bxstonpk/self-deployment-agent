package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type ProvisionedDatabaseRepo struct {
	pool *pgxpool.Pool
}

func NewProvisionedDatabaseRepo(pool *pgxpool.Pool) *ProvisionedDatabaseRepo {
	return &ProvisionedDatabaseRepo{pool: pool}
}

// No password column here: since Module O, a database's password lives in
// application_secrets, sealed. The column still exists only for rows that
// predate that — see ListLegacyPlaintextPasswords.
const provisionedDatabaseColumns = `id, application_id, engine, container_id, network_id, host, port,
	database_name, username, status, provisioned_at, deprovisioned_at`

func scanProvisionedDatabase(row rowScanner, d *domain.ProvisionedDatabase) error {
	var status string
	if err := row.Scan(
		&d.ID, &d.ApplicationID, &d.Engine, &d.ContainerID, &d.NetworkID, &d.Host, &d.Port,
		&d.DatabaseName, &d.Username, &status, &d.ProvisionedAt, &d.DeprovisionedAt,
	); err != nil {
		return err
	}
	d.Status = domain.DatabaseStatus(status)
	return nil
}

func (r *ProvisionedDatabaseRepo) Create(ctx context.Context, d domain.ProvisionedDatabase) (domain.ProvisionedDatabase, error) {
	var out domain.ProvisionedDatabase
	err := scanProvisionedDatabase(r.pool.QueryRow(ctx, `
		INSERT INTO provisioned_databases
			(application_id, engine, container_id, network_id, host, port, database_name, username)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+provisionedDatabaseColumns,
		d.ApplicationID, d.Engine, d.ContainerID, d.NetworkID, d.Host, d.Port,
		d.DatabaseName, d.Username,
	), &out)
	if err != nil {
		return domain.ProvisionedDatabase{}, fmt.Errorf("record provisioned database: %w", err)
	}
	return out, nil
}

// GetLiveForApplication returns the application's currently-provisioned
// database, or ErrDatabaseNotProvisioned — which is an ordinary answer
// (most applications declare no database), not an exceptional one.
func (r *ProvisionedDatabaseRepo) GetLiveForApplication(ctx context.Context, applicationID string) (domain.ProvisionedDatabase, error) {
	var out domain.ProvisionedDatabase
	err := scanProvisionedDatabase(r.pool.QueryRow(ctx, `
		SELECT `+provisionedDatabaseColumns+` FROM provisioned_databases
		WHERE application_id = $1 AND status = 'provisioned'
	`, applicationID), &out)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProvisionedDatabase{}, domain.ErrDatabaseNotProvisioned
		}
		return domain.ProvisionedDatabase{}, fmt.Errorf("get provisioned database: %w", err)
	}
	return out, nil
}

// MarkDeprovisioned retains the row as history rather than deleting it —
// same reasoning as revoked ownership rows. Guarded on status so a repeat
// call can never resurrect and re-close an already-closed record. Clears
// any legacy plaintext password on the way out: a deprovisioned database's
// password protects nothing.
func (r *ProvisionedDatabaseRepo) MarkDeprovisioned(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE provisioned_databases
		SET status = 'deprovisioned', deprovisioned_at = now(), password = NULL
		WHERE id = $1 AND status = 'provisioned'
	`, id); err != nil {
		return fmt.Errorf("mark database deprovisioned: %w", err)
	}
	return nil
}

// ListLegacyPlaintextPasswords returns live databases whose password Module
// N stored in plaintext before Module O existed. After the startup
// migration has run once, this is always empty.
func (r *ProvisionedDatabaseRepo) ListLegacyPlaintextPasswords(ctx context.Context) ([]domain.LegacyDatabasePassword, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, application_id, password FROM provisioned_databases
		WHERE password IS NOT NULL AND status = 'provisioned'
	`)
	if err != nil {
		return nil, fmt.Errorf("list legacy database passwords: %w", err)
	}
	defer rows.Close()

	var out []domain.LegacyDatabasePassword
	for rows.Next() {
		var l domain.LegacyDatabasePassword
		if err := rows.Scan(&l.DatabaseID, &l.ApplicationID, &l.Password); err != nil {
			return nil, fmt.Errorf("scan legacy database password: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ClearPlaintextPassword is the second half of moving a legacy password,
// called only once the sealed copy is safely in the secret store.
func (r *ProvisionedDatabaseRepo) ClearPlaintextPassword(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE provisioned_databases SET password = NULL WHERE id = $1`, id); err != nil {
		return fmt.Errorf("clear legacy database password: %w", err)
	}
	return nil
}
