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

const provisionedDatabaseColumns = `id, application_id, engine, container_id, network_id, host, port,
	database_name, username, password, status, provisioned_at, deprovisioned_at`

func scanProvisionedDatabase(row rowScanner, d *domain.ProvisionedDatabase) error {
	var status string
	if err := row.Scan(
		&d.ID, &d.ApplicationID, &d.Engine, &d.ContainerID, &d.NetworkID, &d.Host, &d.Port,
		&d.DatabaseName, &d.Username, &d.Password, &status, &d.ProvisionedAt, &d.DeprovisionedAt,
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
			(application_id, engine, container_id, network_id, host, port, database_name, username, password)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+provisionedDatabaseColumns,
		d.ApplicationID, d.Engine, d.ContainerID, d.NetworkID, d.Host, d.Port,
		d.DatabaseName, d.Username, d.Password,
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
// call can never resurrect and re-close an already-closed record.
func (r *ProvisionedDatabaseRepo) MarkDeprovisioned(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE provisioned_databases SET status = 'deprovisioned', deprovisioned_at = now()
		WHERE id = $1 AND status = 'provisioned'
	`, id); err != nil {
		return fmt.Errorf("mark database deprovisioned: %w", err)
	}
	return nil
}
