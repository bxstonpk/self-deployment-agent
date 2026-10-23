package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// BackupRepo is the platform's record of each database backup (FR-064).
// The backups themselves are files on the backup volume.
type BackupRepo struct {
	pool *pgxpool.Pool
}

func NewBackupRepo(pool *pgxpool.Pool) *BackupRepo {
	return &BackupRepo{pool: pool}
}

const backupColumns = `id, application_id, database_id, kind, COALESCE(base_backup_id::text, ''), trigger,
	COALESCE(requested_by::text, ''), status, error, file_name, stored_bytes, dump_bytes, dump_sha256, key_id,
	taken_at, deleted_at`

func scanBackup(row rowScanner, b *domain.DatabaseBackup) error {
	var kind, trigger, status string
	if err := row.Scan(&b.ID, &b.ApplicationID, &b.DatabaseID, &kind, &b.BaseBackupID, &trigger,
		&b.RequestedBy, &status, &b.Error, &b.FileName, &b.StoredBytes, &b.DumpBytes, &b.DumpSHA256, &b.KeyID,
		&b.TakenAt, &b.DeletedAt); err != nil {
		return err
	}
	b.Kind, b.Trigger, b.Status = domain.BackupKind(kind), domain.BackupTrigger(trigger), domain.BackupStatus(status)
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *BackupRepo) Create(ctx context.Context, b domain.DatabaseBackup) (domain.DatabaseBackup, error) {
	var out domain.DatabaseBackup
	err := scanBackup(r.pool.QueryRow(ctx, `
		INSERT INTO database_backups (id, application_id, database_id, kind, base_backup_id, trigger, requested_by,
			status, error, file_name, stored_bytes, dump_bytes, dump_sha256, key_id, taken_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING `+backupColumns,
		b.ID, b.ApplicationID, b.DatabaseID, string(b.Kind), nullable(b.BaseBackupID), string(b.Trigger),
		nullable(b.RequestedBy), string(b.Status), b.Error, b.FileName, b.StoredBytes, b.DumpBytes, b.DumpSHA256,
		b.KeyID, b.TakenAt,
	), &out)
	if err != nil {
		return domain.DatabaseBackup{}, fmt.Errorf("record backup: %w", err)
	}
	return out, nil
}

func (r *BackupRepo) Get(ctx context.Context, id string) (domain.DatabaseBackup, error) {
	var out domain.DatabaseBackup
	err := scanBackup(r.pool.QueryRow(ctx, `SELECT `+backupColumns+` FROM database_backups WHERE id = $1`, id), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseBackup{}, domain.ErrBackupNotFound
	}
	if err != nil {
		return domain.DatabaseBackup{}, fmt.Errorf("get backup: %w", err)
	}
	return out, nil
}

// ListForApplication returns the application's backups newest first,
// including failed ones but not deleted ones.
func (r *BackupRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.DatabaseBackup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+backupColumns+` FROM database_backups
		WHERE application_id = $1 AND status <> 'deleted'
		ORDER BY taken_at DESC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	out := []domain.DatabaseBackup{}
	for rows.Next() {
		var b domain.DatabaseBackup
		if err := scanBackup(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestFull is the newest succeeded full backup of this database — the
// base a differential is taken against.
func (r *BackupRepo) LatestFull(ctx context.Context, databaseID string) (domain.DatabaseBackup, error) {
	var out domain.DatabaseBackup
	err := scanBackup(r.pool.QueryRow(ctx, `
		SELECT `+backupColumns+` FROM database_backups
		WHERE database_id = $1 AND kind = 'full' AND status = 'succeeded'
		ORDER BY taken_at DESC LIMIT 1
	`, databaseID), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseBackup{}, domain.ErrBackupNotFound
	}
	if err != nil {
		return domain.DatabaseBackup{}, fmt.Errorf("latest full backup: %w", err)
	}
	return out, nil
}

// LastAttempt is when the newest backup of this database since `since`
// was attempted, and whether it succeeded.
func (r *BackupRepo) LastAttempt(ctx context.Context, databaseID string, since time.Time) (time.Time, domain.BackupStatus, bool, error) {
	var at time.Time
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT taken_at, status FROM database_backups
		WHERE database_id = $1 AND taken_at >= $2 AND trigger = 'scheduled'
		ORDER BY (status = 'succeeded') DESC, taken_at DESC LIMIT 1
	`, databaseID, since).Scan(&at, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, "", false, nil
	}
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("last backup attempt: %w", err)
	}
	return at, domain.BackupStatus(status), true, nil
}

// OlderThan lists this database's undeleted backups taken before t.
func (r *BackupRepo) OlderThan(ctx context.Context, databaseID string, t time.Time) ([]domain.DatabaseBackup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+backupColumns+` FROM database_backups
		WHERE database_id = $1 AND taken_at < $2 AND status <> 'deleted'
	`, databaseID, t)
	if err != nil {
		return nil, fmt.Errorf("list old backups: %w", err)
	}
	defer rows.Close()
	out := []domain.DatabaseBackup{}
	for rows.Next() {
		var b domain.DatabaseBackup
		if err := scanBackup(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AllForApplication lists every undeleted backup, of any database the
// application has had — what deleting the application removes.
func (r *BackupRepo) AllForApplication(ctx context.Context, applicationID string) ([]domain.DatabaseBackup, error) {
	return r.ListForApplication(ctx, applicationID)
}

func (r *BackupRepo) MarkDeleted(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE database_backups SET status = 'deleted', deleted_at = now()
		WHERE id = $1 AND status <> 'deleted'
	`, id)
	return err
}
