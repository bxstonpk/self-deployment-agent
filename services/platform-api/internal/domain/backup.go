// Backup implements FR-064 (Backup Scheduling) with the policy the platform
// owner decided on 2026-10-23 (DEC-025, docs/17_Decision_Log.md):
//
//   - a **full** backup every Sunday;
//   - a **differential** backup Monday to Saturday — what changed since
//     that Sunday's full (internal/backupdelta);
//   - once a new Sunday full has succeeded, the previous week's set is
//     deleted;
//   - an application can opt out with `database: backup: none`.
//
// A backup is a logical dump (mysqldump / pg_dump), compressed and sealed
// with the platform's secret key (internal/secretbox) before it touches
// disk, so a copy of the backup volume alone yields no data.
package domain

import (
	"errors"
	"time"
)

type BackupKind string

const (
	BackupFull         BackupKind = "full"
	BackupDifferential BackupKind = "differential"
)

type BackupStatus string

const (
	BackupSucceeded BackupStatus = "succeeded"
	BackupFailed    BackupStatus = "failed"
	// BackupDeleted: removed by the weekly retention, or with its
	// application. The row stays as history; the file is gone.
	BackupDeleted BackupStatus = "deleted"
)

type BackupTrigger string

const (
	BackupScheduled BackupTrigger = "scheduled"
	BackupManual    BackupTrigger = "manual"
	// BackupPreRestore is taken automatically just before a restore, so
	// the state a restore overwrote can itself be restored.
	BackupPreRestore BackupTrigger = "pre_restore"
)

// Backup policies an application can declare in deployment.yaml.
const (
	BackupPolicyWeekly = "weekly" // the default
	BackupPolicyNone   = "none"
)

type DatabaseBackup struct {
	ID            string
	ApplicationID string
	DatabaseID    string
	Kind          BackupKind
	// BaseBackupID is the full backup a differential applies to.
	BaseBackupID string
	Trigger      BackupTrigger
	RequestedBy  string // empty for a scheduled backup
	Status       BackupStatus
	Error        string
	FileName     string
	StoredBytes  int64
	// DumpBytes and DumpSHA256 describe the complete dump this backup
	// restores to — checked before anything is restored.
	DumpBytes  int64
	DumpSHA256 string
	KeyID      string
	TakenAt    time.Time
	DeletedAt  *time.Time
}

var (
	ErrBackupNotFound        = errors.New("no such backup for this application")
	ErrBackupsDisabled       = errors.New("this application's deployment.yaml sets database.backup: none")
	ErrRestoreConfirmation   = errors.New("restoring replaces the database's current contents; confirm with the application's exact name")
	ErrBackupNotRestorable   = errors.New("only a succeeded backup can be restored")
	ErrBackupIntegrity       = errors.New("the rebuilt dump does not match what was backed up")
	ErrInvalidBackupKind     = errors.New(`backup kind must be "full", "differential" or empty`)
	ErrInvalidBackupPolicy   = errors.New(`database.backup must be "weekly" or "none"`)
	ErrBackupWithoutDatabase = errors.New("database.backup is set but no database.type is declared")
)
