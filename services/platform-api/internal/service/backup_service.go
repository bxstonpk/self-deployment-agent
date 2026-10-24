// Backup implements FR-064 with DEC-025's policy — see
// internal/domain/backup.go for the policy itself.
package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/backupdelta"
	"platform-api/internal/domain"
)

type BackupRepository interface {
	Create(ctx context.Context, b domain.DatabaseBackup) (domain.DatabaseBackup, error)
	Get(ctx context.Context, id string) (domain.DatabaseBackup, error)
	ListForApplication(ctx context.Context, applicationID string) ([]domain.DatabaseBackup, error)
	LatestFull(ctx context.Context, databaseID string) (domain.DatabaseBackup, error)
	LastAttempt(ctx context.Context, databaseID string, since time.Time) (time.Time, domain.BackupStatus, bool, error)
	OlderThan(ctx context.Context, databaseID string, t time.Time) ([]domain.DatabaseBackup, error)
	AllForApplication(ctx context.Context, applicationID string) ([]domain.DatabaseBackup, error)
	MarkDeleted(ctx context.Context, id string) error
}

type BackupDatabases interface {
	ListLive(ctx context.Context) ([]domain.ProvisionedDatabase, error)
	GetLiveForApplication(ctx context.Context, applicationID string) (domain.ProvisionedDatabase, error)
}

// BackupRuntime is the part of the Runtime Platform backups need.
type BackupRuntime interface {
	DumpDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec) ([]byte, error)
	RestoreDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec, dump []byte) error
}

// Sealer is internal/secretbox as backups use it.
type Sealer interface {
	KeyID() string
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(sealed []byte, keyID string, aad []byte) ([]byte, error)
}

type BackupNotifier interface {
	NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
}

// BackupSchedule is when scheduled backups run: each day from Hour
// (local to Location), with the full backup on FullWeekday.
type BackupSchedule struct {
	Location    *time.Location
	Hour        int
	FullWeekday time.Weekday
	// RetryAfter: a failed scheduled backup is tried again after this long
	// the same day, rather than on every sweep.
	RetryAfter time.Duration
}

type BackupService struct {
	backups   BackupRepository
	databases BackupDatabases
	apps      ApplicationLifecycleRepository
	owners    ApplicationOwnerRepository
	secrets   DatabaseSecretStore
	runtime   BackupRuntime
	sealer    Sealer
	notifier  BackupNotifier
	audit     AuditRecorder
	dir       string
	schedule  BackupSchedule
	now       func() time.Time
	wake      func(ctx context.Context, applicationID string) error
}

func NewBackupService(backups BackupRepository, databases BackupDatabases, apps ApplicationLifecycleRepository,
	owners ApplicationOwnerRepository, secrets DatabaseSecretStore, runtime BackupRuntime, sealer Sealer,
	notifier BackupNotifier, audit AuditRecorder, dir string, schedule BackupSchedule) *BackupService {
	if schedule.Location == nil {
		schedule.Location = time.UTC
	}
	if schedule.RetryAfter <= 0 {
		schedule.RetryAfter = time.Hour
	}
	return &BackupService{backups: backups, databases: databases, apps: apps, owners: owners, secrets: secrets,
		runtime: runtime, sealer: sealer, notifier: notifier, audit: audit, dir: dir, schedule: schedule, now: time.Now}
}

func (s *BackupService) Schedule() BackupSchedule { return s.schedule }

// SetNotifier tells owners about failed backups; set after construction
// because the notification service is built later in main.
func (s *BackupService) SetNotifier(n BackupNotifier) { s.notifier = n }

// SetWaker wakes a sleeping database before anything reads or writes it
// (DatabaseService.Wake).
func (s *BackupService) SetWaker(wake func(ctx context.Context, applicationID string) error) { s.wake = wake }

func (s *BackupService) wakeDB(ctx context.Context, applicationID string) error {
	if s.wake == nil {
		return nil
	}
	return s.wake(ctx, applicationID)
}

// BackupPolicyFor reads database.backup from the application's
// deployment.yaml — the one the platform deploys from. Unset means weekly.
func BackupPolicyFor(app domain.Application) string {
	var parsed domain.DeploymentYAML
	if err := yaml.Unmarshal([]byte(app.DeploymentYAMLDraft), &parsed); err != nil {
		return domain.BackupPolicyWeekly
	}
	if strings.TrimSpace(parsed.Database.Backup) == domain.BackupPolicyNone {
		return domain.BackupPolicyNone
	}
	return domain.BackupPolicyWeekly
}

func (s *BackupService) requireOwner(ctx context.Context, applicationID, userID string) error {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return nil
		}
	}
	return domain.ErrUnauthorized
}

func newBackupID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

func (s *BackupService) path(b domain.DatabaseBackup) string {
	return filepath.Join(s.dir, b.ApplicationID, b.FileName)
}

// aad binds a sealed file to its own record: a file copied over another
// backup's name fails to open instead of restoring the wrong data.
func backupAAD(id, applicationID string) []byte {
	return []byte("database-backup:" + applicationID + ":" + id)
}

func gz(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (s *BackupService) writeFile(b domain.DatabaseBackup, data []byte) error {
	p := s.path(b)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// payload reads and opens one backup's own file: a whole dump for a full,
// a delta for a differential.
func (s *BackupService) payload(b domain.DatabaseBackup) ([]byte, error) {
	sealed, err := os.ReadFile(s.path(b))
	if err != nil {
		return nil, fmt.Errorf("read backup file: %w", err)
	}
	compressed, err := s.sealer.Open(sealed, b.KeyID, backupAAD(b.ID, b.ApplicationID))
	if err != nil {
		return nil, fmt.Errorf("open backup file: %w", err)
	}
	return gunzip(compressed)
}

// dumpOf rebuilds the complete dump a backup restores to, and checks it
// against the hash taken when the backup was made.
func (s *BackupService) dumpOf(ctx context.Context, b domain.DatabaseBackup) ([]byte, error) {
	data, err := s.payload(b)
	if err != nil {
		return nil, err
	}
	if b.Kind == domain.BackupDifferential {
		base, err := s.backups.Get(ctx, b.BaseBackupID)
		if err != nil {
			return nil, fmt.Errorf("the full backup this differential needs: %w", err)
		}
		baseDump, err := s.payload(base)
		if err != nil {
			return nil, fmt.Errorf("the full backup this differential needs: %w", err)
		}
		if data, err = backupdelta.Apply(baseDump, data); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != b.DumpSHA256 {
		return nil, domain.ErrBackupIntegrity
	}
	return data, nil
}

func (s *BackupService) spec(ctx context.Context, db domain.ProvisionedDatabase) (domain.DatabaseSpec, error) {
	password, err := s.secrets.ManagedValue(ctx, db.ApplicationID, domain.DatabasePasswordSecret)
	if err != nil {
		return domain.DatabaseSpec{}, fmt.Errorf("database password: %w", err)
	}
	return domain.DatabaseSpec{Engine: db.Engine, DatabaseName: db.DatabaseName, Username: db.Username, Password: password}, nil
}

// take makes one backup of db. kind "" chooses: a differential against
// the latest full if there is one, otherwise a full. A full that succeeds
// removes every older backup of the database (DEC-025's retention).
func (s *BackupService) take(ctx context.Context, db domain.ProvisionedDatabase, kind domain.BackupKind,
	trigger domain.BackupTrigger, requestedBy string) (domain.DatabaseBackup, error) {
	id, err := newBackupID()
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	record := domain.DatabaseBackup{
		ID: id, ApplicationID: db.ApplicationID, DatabaseID: db.ID, Kind: kind, Trigger: trigger,
		RequestedBy: requestedBy, TakenAt: s.now(), FileName: id + ".bak", KeyID: s.sealer.KeyID(),
	}
	fail := func(err error) (domain.DatabaseBackup, error) {
		record.Status, record.Error, record.FileName = domain.BackupFailed, err.Error(), ""
		if record.Kind == "" {
			record.Kind = domain.BackupFull
		}
		record.BaseBackupID = ""
		if _, rerr := s.backups.Create(ctx, record); rerr != nil {
			log.Printf("backup: record failure for %s: %v", db.ApplicationID, rerr)
		}
		return record, err
	}

	var base domain.DatabaseBackup
	if kind != domain.BackupFull {
		latest, err := s.backups.LatestFull(ctx, db.ID)
		switch {
		case err == nil:
			base, record.Kind, record.BaseBackupID = latest, domain.BackupDifferential, latest.ID
		case errors.Is(err, domain.ErrBackupNotFound):
			record.Kind = domain.BackupFull // nothing to be different from yet
		default:
			return fail(err)
		}
	}

	spec, err := s.spec(ctx, db)
	if err != nil {
		return fail(err)
	}
	// A database asleep with its application wakes for its backup, and
	// sleeps again once the idle timeout passes.
	if err := s.wakeDB(ctx, db.ApplicationID); err != nil {
		return fail(err)
	}
	dump, err := s.runtime.DumpDatabase(ctx, db.ContainerID, spec)
	if err != nil {
		return fail(err)
	}
	sum := sha256.Sum256(dump)
	record.DumpSHA256, record.DumpBytes = hex.EncodeToString(sum[:]), int64(len(dump))

	data := dump
	if record.Kind == domain.BackupDifferential {
		baseDump, err := s.dumpOf(ctx, base)
		if err != nil {
			return fail(fmt.Errorf("read the full backup to compare against: %w", err))
		}
		data = backupdelta.Diff(baseDump, dump)
	}
	compressed, err := gz(data)
	if err != nil {
		return fail(err)
	}
	sealed, err := s.sealer.Seal(compressed, backupAAD(record.ID, record.ApplicationID))
	if err != nil {
		return fail(err)
	}
	record.StoredBytes = int64(len(sealed))
	if err := s.writeFile(record, sealed); err != nil {
		return fail(fmt.Errorf("write backup file: %w", err))
	}
	record.Status = domain.BackupSucceeded
	saved, err := s.backups.Create(ctx, record)
	if err != nil {
		_ = os.Remove(s.path(record))
		return domain.DatabaseBackup{}, err
	}

	if saved.Kind == domain.BackupFull {
		s.dropOlder(ctx, saved)
	}
	return saved, nil
}

// dropOlder is DEC-025's retention: once a new full has succeeded, the
// previous set — full and differentials — goes. Only after success, so a
// failed Sunday never leaves the database without a backup.
func (s *BackupService) dropOlder(ctx context.Context, full domain.DatabaseBackup) {
	older, err := s.backups.OlderThan(ctx, full.DatabaseID, full.TakenAt)
	if err != nil {
		log.Printf("backup: list backups to retire for %s: %v", full.ApplicationID, err)
		return
	}
	for _, b := range older {
		s.remove(ctx, b)
	}
}

func (s *BackupService) remove(ctx context.Context, b domain.DatabaseBackup) {
	if b.FileName != "" {
		if err := os.Remove(s.path(b)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("backup: delete file %s: %v", b.ID, err)
			return
		}
	}
	if err := s.backups.MarkDeleted(ctx, b.ID); err != nil {
		log.Printf("backup: mark %s deleted: %v", b.ID, err)
	}
}

// RunScheduled takes every backup that's due: for each live database whose
// application hasn't opted out, once a day from the scheduled hour — a
// full on FullWeekday (or when there is no full yet), a differential
// otherwise. Returns how many succeeded.
func (s *BackupService) RunScheduled(ctx context.Context) (int, error) {
	now := s.now().In(s.schedule.Location)
	if now.Hour() < s.schedule.Hour {
		return 0, nil
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.schedule.Location)
	dbs, err := s.databases.ListLive(ctx)
	if err != nil {
		return 0, err
	}
	taken := 0
	for _, db := range dbs {
		app, err := s.apps.GetByID(ctx, db.ApplicationID)
		if err != nil || BackupPolicyFor(app) == domain.BackupPolicyNone {
			continue
		}
		at, status, attempted, err := s.backups.LastAttempt(ctx, db.ID, midnight)
		if err != nil {
			log.Printf("backup: %s: %v", app.Name, err)
			continue
		}
		if attempted && (status == domain.BackupSucceeded || now.Sub(at) < s.schedule.RetryAfter) {
			continue
		}
		kind := domain.BackupDifferential
		if now.Weekday() == s.schedule.FullWeekday {
			kind = domain.BackupFull
		}
		b, err := s.take(ctx, db, kind, domain.BackupScheduled, "")
		if err != nil {
			log.Printf("backup: %s: %v", app.Name, err)
			if !attempted && s.notifier != nil { // once a day, not on every retry
				s.notifier.NotifyOwners(ctx, app.ID, domain.NotificationDatabaseBackup,
					app.Name+": database backup failed", err.Error(), "application", app.ID)
			}
			continue
		}
		log.Printf("backup: %s: %s backup %s (%d bytes stored for a %d-byte dump)", app.Name, b.Kind, b.ID, b.StoredBytes, b.DumpBytes)
		taken++
	}
	return taken, nil
}

// BackupOverview is what an owner sees: the policy that applies and the
// backups that exist.
type BackupOverview struct {
	Policy   string
	Schedule BackupSchedule
	Backups  []domain.DatabaseBackup
}

func (s *BackupService) List(ctx context.Context, applicationID, requesterID string) (BackupOverview, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return BackupOverview{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return BackupOverview{}, err
	}
	backups, err := s.backups.ListForApplication(ctx, applicationID)
	if err != nil {
		return BackupOverview{}, err
	}
	return BackupOverview{Policy: BackupPolicyFor(app), Schedule: s.schedule, Backups: backups}, nil
}

func (s *BackupService) record(ctx context.Context, actor string, action domain.AuditAction, resourceID string, outcome domain.AuditOutcome, detail string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, domain.AuditEntry{ActorUserID: actor, Action: action, ResourceType: "application",
		ResourceID: resourceID, Outcome: outcome, Detail: detail}); err != nil {
		log.Printf("backup: audit %s: %v", action, err)
	}
}

// BackupNow takes a backup on an owner's request — before a risky change,
// say. kind "" lets the platform choose (a differential if a full exists).
// A manual full replaces the week's set exactly as Sunday's does.
func (s *BackupService) BackupNow(ctx context.Context, applicationID, requesterID string, kind domain.BackupKind) (domain.DatabaseBackup, error) {
	if kind != "" && kind != domain.BackupFull && kind != domain.BackupDifferential {
		return domain.DatabaseBackup{}, domain.ErrInvalidBackupKind
	}
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.DatabaseBackup{}, err
	}
	db, err := s.databases.GetLiveForApplication(ctx, applicationID)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	b, err := s.take(ctx, db, kind, domain.BackupManual, requesterID)
	if err != nil {
		s.record(ctx, requesterID, domain.AuditActionBackupDatabase, applicationID, domain.AuditFailure, err.Error())
		return domain.DatabaseBackup{}, err
	}
	s.record(ctx, requesterID, domain.AuditActionBackupDatabase, applicationID, domain.AuditSuccess,
		fmt.Sprintf("%s backup %s", b.Kind, b.ID))
	return b, nil
}

// Restore replaces the database's contents with a backup's. It first takes
// a backup of what is there now (a differential, so it doesn't retire the
// week's set), so a restore is itself undoable.
func (s *BackupService) Restore(ctx context.Context, applicationID, backupID, requesterID, confirmName string) (domain.DatabaseBackup, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.DatabaseBackup{}, err
	}
	if strings.TrimSpace(confirmName) != app.Name {
		return domain.DatabaseBackup{}, domain.ErrRestoreConfirmation
	}
	b, err := s.backups.Get(ctx, backupID)
	if err != nil || b.ApplicationID != applicationID {
		return domain.DatabaseBackup{}, domain.ErrBackupNotFound
	}
	if b.Status != domain.BackupSucceeded {
		return domain.DatabaseBackup{}, domain.ErrBackupNotRestorable
	}
	db, err := s.databases.GetLiveForApplication(ctx, applicationID)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	dump, err := s.dumpOf(ctx, b)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	safety, err := s.take(ctx, db, domain.BackupDifferential, domain.BackupPreRestore, requesterID)
	if err != nil {
		return domain.DatabaseBackup{}, fmt.Errorf("back up the current contents before restoring: %w", err)
	}
	spec, err := s.spec(ctx, db)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := s.runtime.RestoreDatabase(ctx, db.ContainerID, spec, dump); err != nil {
		s.record(ctx, requesterID, domain.AuditActionRestoreDatabase, applicationID, domain.AuditFailure, err.Error())
		return domain.DatabaseBackup{}, err
	}
	s.record(ctx, requesterID, domain.AuditActionRestoreDatabase, applicationID, domain.AuditSuccess,
		fmt.Sprintf("restored %s backup %s from %s; the previous contents are backup %s",
			b.Kind, b.ID, b.TakenAt.UTC().Format(time.RFC3339), safety.ID))
	return safety, nil
}

// HasDatabase says whether an application has a live database.
func (s *BackupService) HasDatabase(ctx context.Context, applicationID string) (bool, error) {
	if _, err := s.databases.GetLiveForApplication(ctx, applicationID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CopyDatabase replaces to's database contents with from's: a dev copy's
// data, taken into production (DEC-029 part 3). What it replaces is backed
// up first, as before a restore.
func (s *BackupService) CopyDatabase(ctx context.Context, fromApplicationID, toApplicationID, actorID string) error {
	from, err := s.databases.GetLiveForApplication(ctx, fromApplicationID)
	if err != nil {
		return fmt.Errorf("the dev copy's database: %w", err)
	}
	to, err := s.databases.GetLiveForApplication(ctx, toApplicationID)
	if err != nil {
		return fmt.Errorf("production's database: %w", err)
	}
	if from.Engine != to.Engine {
		return fmt.Errorf("the dev copy's database is %s and production's is %s", from.Engine, to.Engine)
	}
	if err := s.wakeDB(ctx, fromApplicationID); err != nil {
		return err
	}
	if err := s.wakeDB(ctx, toApplicationID); err != nil {
		return err
	}
	fromSpec, err := s.spec(ctx, from)
	if err != nil {
		return err
	}
	toSpec, err := s.spec(ctx, to)
	if err != nil {
		return err
	}
	dump, err := s.runtime.DumpDatabase(ctx, from.ContainerID, fromSpec)
	if err != nil {
		return fmt.Errorf("read the dev copy's data: %w", err)
	}
	if _, err := s.take(ctx, to, domain.BackupFull, domain.BackupPreRestore, actorID); err != nil {
		return fmt.Errorf("back up production's database before copying into it: %w", err)
	}
	if err := s.runtime.RestoreDatabase(ctx, to.ContainerID, toSpec, dump); err != nil {
		return fmt.Errorf("copy the data into production: %w", err)
	}
	return nil
}

// PurgeApplication deletes every backup of a deleted application, along
// with its database (FR-065: purge).
func (s *BackupService) PurgeApplication(ctx context.Context, applicationID string) error {
	backups, err := s.backups.AllForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, b := range backups {
		s.remove(ctx, b)
	}
	_ = os.Remove(filepath.Join(s.dir, applicationID))
	return nil
}
