package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

// Databases sleep when their application does (the platform owner,
// 2026-10-24). Scale-to-zero already stops an idle application's
// containers; its database used to keep running — and holding its memory —
// regardless. Now, once none of an application's services that can use
// it (anything but a static frontend) has run for the idle timeout, its
// database is stopped too (not removed: its data is
// in the container's volume), and it is woken before anything that needs
// it: every container start (through WiringFor), backups, restores, a
// password rotation, a dev data copy.

// DatabaseSleeper is the runtime side of it (internal/runtimeengine).
type DatabaseSleeper interface {
	TuneDatabase(ctx context.Context, containerID, engine string) (bool, error)
	DatabaseRunning(ctx context.Context, containerID string) (bool, error)
	SleepDatabase(ctx context.Context, containerID string) error
	WakeDatabase(ctx context.Context, containerID string) error
	RunningServices(ctx context.Context, applicationID string) ([]string, error)
}

// staticFrontends are served as files by nginx and never touch the
// database. They are also never scaled to zero (FR-051), so counting them
// would keep every database with a frontend awake for good — found for
// real: the first local run's database never slept beside its React page.
var staticFrontends = map[string]bool{"react": true, "vue": true}

// liveDatabases is the provisioned databases' repository as the sweep
// reads it.
type liveDatabases interface {
	ListLive(ctx context.Context) ([]domain.ProvisionedDatabase, error)
}

// databaseWakeTimeout bounds waking a sleeping database (measured at ~4 s
// for MySQL 8.4; the first start of a new one is databaseReadyTimeout).
const databaseWakeTimeout = 2 * time.Minute

type databaseSleep struct {
	runtime DatabaseSleeper
	apps    ApplicationLifecycleRepository
	idleFor time.Duration
	mu      sync.Mutex
	// idleSince is when each database's application was first seen with
	// nothing running.
	idleSince map[string]time.Time
	// waking serialises waking one database, so two starts at once don't
	// both start it.
	waking map[string]*sync.Mutex
}

// SetSleeper makes databases sleep after their application has had
// nothing running for idleFor, and wake when needed. Optional: without
// it, databases run all the time, as before.
func (s *DatabaseService) SetSleeper(r DatabaseSleeper, apps ApplicationLifecycleRepository, idleFor time.Duration) {
	s.sleep = &databaseSleep{runtime: r, apps: apps, idleFor: idleFor, idleSince: map[string]time.Time{}, waking: map[string]*sync.Mutex{}}
}

// usingDatabase says whether any service that can use the application's
// database is running: anything but a static frontend.
func (d *databaseSleep) usingDatabase(ctx context.Context, applicationID string) (bool, error) {
	running, err := d.runtime.RunningServices(ctx, applicationID)
	if err != nil || len(running) == 0 {
		return false, err
	}
	runtimes := map[string]string{}
	if app, err := d.apps.GetByID(ctx, applicationID); err == nil {
		var parsed domain.DeploymentYAML
		if yaml.Unmarshal([]byte(app.DeploymentYAMLDraft), &parsed) == nil {
			for name, svc := range parsed.Services {
				runtimes[name] = svc.Runtime
			}
		}
	}
	for _, svc := range running {
		if !staticFrontends[runtimes[svc]] {
			return true, nil // unknown counts as using it: never sleep under a live service
		}
	}
	return false, nil
}

func (d *databaseSleep) lockFor(id string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.waking[id]
	if !ok {
		m = &sync.Mutex{}
		d.waking[id] = m
	}
	return m
}

// Wake makes sure an application's database is running and answering. An
// application without one, or a platform without sleeping databases, is
// nothing to do.
func (s *DatabaseService) Wake(ctx context.Context, applicationID string) error {
	if s.sleep == nil {
		return nil
	}
	db, err := s.repo.GetLiveForApplication(ctx, applicationID)
	if errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		return nil
	}
	if err != nil {
		return err
	}
	lock := s.sleep.lockFor(db.ID)
	lock.Lock()
	defer lock.Unlock()

	s.sleep.mu.Lock()
	delete(s.sleep.idleSince, db.ID)
	s.sleep.mu.Unlock()

	running, err := s.sleep.runtime.DatabaseRunning(ctx, db.ContainerID)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	password, err := s.secrets.ManagedValue(ctx, applicationID, domain.DatabasePasswordSecret)
	if err != nil {
		return fmt.Errorf("database password: %w", err)
	}
	started := time.Now()
	if err := s.sleep.runtime.WakeDatabase(ctx, db.ContainerID); err != nil {
		return err
	}
	spec := domain.DatabaseSpec{Engine: db.Engine, DatabaseName: db.DatabaseName, Username: db.Username, Password: password}
	if err := s.runtime.WaitDatabaseReady(ctx, db.ContainerID, spec, databaseWakeTimeout); err != nil {
		return fmt.Errorf("the application's database didn't wake: %w", err)
	}
	log.Printf("databases: woke %s's database (%s)", applicationID, time.Since(started).Round(100*time.Millisecond))
	return nil
}

// SleepIdle stops the databases of applications that have had nothing
// running for the idle timeout. Returns how many it put to sleep.
func (s *DatabaseService) SleepIdle(ctx context.Context, now time.Time) (int, error) {
	if s.sleep == nil {
		return 0, nil
	}
	lister, ok := s.repo.(liveDatabases)
	if !ok {
		return 0, nil
	}
	dbs, err := lister.ListLive(ctx)
	if err != nil {
		return 0, err
	}
	slept := 0
	for _, db := range dbs {
		busy, err := s.sleep.usingDatabase(ctx, db.ApplicationID)
		if err != nil {
			log.Printf("databases: %v", err)
			continue
		}
		s.sleep.mu.Lock()
		since, seen := s.sleep.idleSince[db.ID]
		switch {
		case busy:
			delete(s.sleep.idleSince, db.ID)
		case !seen:
			s.sleep.idleSince[db.ID] = now
		}
		s.sleep.mu.Unlock()
		if busy || !seen || now.Sub(since) < s.sleep.idleFor {
			continue
		}
		lock := s.sleep.lockFor(db.ID)
		lock.Lock()
		running, err := s.sleep.runtime.DatabaseRunning(ctx, db.ContainerID)
		if err == nil && running {
			// Checked again under the lock: a start may have just begun.
			if busy, _ := s.sleep.usingDatabase(ctx, db.ApplicationID); !busy {
				if err = s.sleep.runtime.SleepDatabase(ctx, db.ContainerID); err == nil {
					slept++
					log.Printf("databases: %s's database is asleep (nothing of it has run for %s)", db.ApplicationID, s.sleep.idleFor)
				}
			}
		}
		lock.Unlock()
		if err != nil {
			log.Printf("databases: %v", err)
		}
	}
	return slept, nil
}

// TuneAll gives every live MySQL database the platform's configuration
// (performance_schema off, a small buffer pool). A running one is
// restarted once for it, its data kept. Returns how many changed.
func (s *DatabaseService) TuneAll(ctx context.Context) (int, error) {
	if s.sleep == nil {
		return 0, nil
	}
	lister, ok := s.repo.(liveDatabases)
	if !ok {
		return 0, nil
	}
	dbs, err := lister.ListLive(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, db := range dbs {
		changed, err := s.sleep.runtime.TuneDatabase(ctx, db.ContainerID, db.Engine)
		if err != nil {
			log.Printf("databases: tune %s's database: %v", db.ApplicationID, err)
			continue
		}
		if changed {
			n++
		}
	}
	return n, nil
}
