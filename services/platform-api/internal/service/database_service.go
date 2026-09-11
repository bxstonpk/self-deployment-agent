// Database implements Module N's service layer
// (docs/02_Functional_Requirements.md FR-061/062/063/065). See
// internal/domain/database.go's package comment for the scope this slice
// covers. The passwords it generates live in Module O's secret store
// (secret_service.go), never in this module's own table.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"platform-api/internal/domain"
)

// postgresImage is the engine image every provisioned database runs. Pinned
// rather than floating on :latest, for the same reason build_service.go's
// base images are: a deployment that worked yesterday should not start
// failing because an upstream tag moved underneath it.
const postgresImage = "postgres:16-alpine"

const postgresPort = 5432

// databaseReadyTimeout bounds how long a freshly started database gets to
// begin accepting connections. The official image runs initdb and restarts
// itself once before it does — a few seconds on a warm host.
const databaseReadyTimeout = 60 * time.Second

type ProvisionedDatabaseRepository interface {
	Create(ctx context.Context, d domain.ProvisionedDatabase) (domain.ProvisionedDatabase, error)
	GetLiveForApplication(ctx context.Context, applicationID string) (domain.ProvisionedDatabase, error)
	MarkDeprovisioned(ctx context.Context, id string) error
	ListLegacyPlaintextPasswords(ctx context.Context) ([]domain.LegacyDatabasePassword, error)
	ClearPlaintextPassword(ctx context.Context, id string) error
}

// DatabaseRuntime is the narrow seam into the Runtime Platform that
// Module N needs — deliberately separate from RuntimeEngine (the
// application-container seam) so a caller that only starts applications
// isn't handed database-provisioning powers it has no business with.
type DatabaseRuntime interface {
	CreateNetwork(ctx context.Context, name string) (string, error)
	RemoveNetwork(ctx context.Context, networkID string) error
	StartDatabase(ctx context.Context, spec domain.DatabaseSpec) (string, error)
	WaitDatabaseReady(ctx context.Context, containerID string, spec domain.DatabaseSpec, timeout time.Duration) error
	SetDatabasePassword(ctx context.Context, containerID string, spec domain.DatabaseSpec) error
	Stop(ctx context.Context, containerID string) error
}

// DatabaseSecretStore is the part of Module O this module needs:
// somewhere to keep the password it generates that is not its own table
// (FR-063).
type DatabaseSecretStore interface {
	PutManaged(ctx context.Context, applicationID, name, value string) error
	ManagedValue(ctx context.Context, applicationID, name string) (string, error)
}

type DatabaseService struct {
	repo    ProvisionedDatabaseRepository
	runtime DatabaseRuntime
	secrets DatabaseSecretStore
}

func NewDatabaseService(repo ProvisionedDatabaseRepository, runtime DatabaseRuntime, secrets DatabaseSecretStore) *DatabaseService {
	return &DatabaseService{repo: repo, runtime: runtime, secrets: secrets}
}

// generatePassword produces the credential FR-063 requires the platform to
// generate rather than the employee to author. crypto/rand, not math/rand
// — this is a credential, and the difference between the two is the whole
// point.
func generatePassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate database password: %w", err)
	}
	// URL-safe so it survives being embedded in a connection string
	// without escaping — a password containing '@' or '/' would otherwise
	// silently corrupt the DSN.
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EnsureProvisioned implements FR-061, idempotently: an application that
// already has a live database keeps it (a redeploy must not replace the
// database and lose its data), and one that declares none gets nothing.
//
// Returns ErrDatabaseNotProvisioned for the "declares no database" case
// rather than a nil-and-no-error pair, so callers can't accidentally treat
// "none wanted" as "one exists".
//
// The order of the steps is the point. The password is sealed into the
// secret store before the database that uses it starts, so there is never
// a running database whose password exists only in this process's memory;
// and the database is recorded as provisioned only once it actually
// accepts connections.
func (s *DatabaseService) EnsureProvisioned(ctx context.Context, app domain.Application, declaredType string) (domain.ProvisionedDatabase, error) {
	declaredType = strings.TrimSpace(strings.ToLower(declaredType))
	if declaredType == "" {
		return domain.ProvisionedDatabase{}, domain.ErrDatabaseNotProvisioned
	}
	// FR-061's exception flow. validate_application's stack check is the
	// first line of defence; this is the one that actually gates
	// provisioning, so an unsupported type can never reach the runtime.
	if declaredType != "postgres" {
		return domain.ProvisionedDatabase{}, domain.ErrUnsupportedDatabaseType
	}

	existing, err := s.repo.GetLiveForApplication(ctx, app.ID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		return domain.ProvisionedDatabase{}, err
	}

	networkName := fmt.Sprintf("platform-net-%s-%s", sanitizeName(app.Name), shortID(app.ID))
	networkID, err := s.runtime.CreateNetwork(ctx, networkName)
	if err != nil {
		return domain.ProvisionedDatabase{}, err
	}

	password, err := generatePassword()
	if err != nil {
		return domain.ProvisionedDatabase{}, err
	}
	if err := s.secrets.PutManaged(ctx, app.ID, domain.DatabasePasswordSecret, password); err != nil {
		return domain.ProvisionedDatabase{}, fmt.Errorf("store the generated database password: %w", err)
	}

	containerName := fmt.Sprintf("platform-db-%s-%s", sanitizeName(app.Name), shortID(app.ID))
	spec := domain.DatabaseSpec{
		Name:         containerName,
		ImageRef:     postgresImage,
		NetworkID:    networkID,
		DatabaseName: "appdb",
		Username:     "appuser",
		Password:     password,
	}
	containerID, err := s.runtime.StartDatabase(ctx, spec)
	if err != nil {
		// Leave the network in place: CreateNetwork is idempotent, so the
		// retry reuses it, and removing it here would race any container
		// still attaching to it. The stored password is simply replaced by
		// the retry's new one.
		return domain.ProvisionedDatabase{}, err
	}

	// Found by a real deployment, not assumed: StartDatabase returns the
	// moment the container starts, but Postgres's official image runs initdb
	// and restarts itself once before it accepts connections. Without this
	// wait the platform reported an application Running while its database
	// still refused connections — so an application that connects at
	// startup, and exits if it can't, would fail its first deploy and
	// succeed on the retry.
	if err := s.runtime.WaitDatabaseReady(ctx, containerID, spec, databaseReadyTimeout); err != nil {
		if stopErr := s.runtime.Stop(ctx, containerID); stopErr != nil {
			log.Printf("database: failed to stop unready container %s for application %s: %v", containerID, app.ID, stopErr)
		}
		return domain.ProvisionedDatabase{}, err
	}

	return s.repo.Create(ctx, domain.ProvisionedDatabase{
		ApplicationID: app.ID,
		Engine:        "postgres",
		ContainerID:   containerID,
		NetworkID:     networkID,
		// The container's own name: Docker resolves it for anything on the
		// same private network, and for nothing outside it.
		Host:         containerName,
		Port:         postgresPort,
		DatabaseName: spec.DatabaseName,
		Username:     spec.Username,
	})
}

// ConnectionEnv implements FR-063's delivery half: the connection details
// the application's runtime needs, as environment variables it can read
// without anyone having written them down anywhere.
//
// DATABASE_URL is the whole DSN (what most frameworks read directly); the
// individual parts are provided too, since plenty of libraries want host
// and port separately rather than parsing a URL.
func (s *DatabaseService) ConnectionEnv(db domain.ProvisionedDatabase, password string) []string {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		db.Username, password, db.Host, db.Port, db.DatabaseName)
	return []string{
		"DATABASE_URL=" + dsn,
		"DATABASE_HOST=" + db.Host,
		fmt.Sprintf("DATABASE_PORT=%d", db.Port),
		"DATABASE_NAME=" + db.DatabaseName,
		"DATABASE_USER=" + db.Username,
		"DATABASE_PASSWORD=" + password,
	}
}

// RuntimeWiring is what every container-starting path needs in order to
// connect an application to its database: the environment to inject and
// the network to join. Both are zero-valued for an application without
// one, which is what makes the call sites uniform — they don't branch on
// whether a database exists.
type RuntimeWiring struct {
	Env       []string
	NetworkID string
}

// WiringFor is the single helper every start path (deploy, resume,
// restart, cold start) calls. Getting this wrong in even one of them
// would mean an application silently losing its database on that path —
// which is why it's one function rather than four copies of the lookup.
//
// This is where the database password is decrypted: at container start
// (FR-067), and nowhere else. A password that can't be read fails the
// start rather than handing the application a connection string that
// won't authenticate.
func (s *DatabaseService) WiringFor(ctx context.Context, applicationID string) (RuntimeWiring, error) {
	db, err := s.repo.GetLiveForApplication(ctx, applicationID)
	if errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		return RuntimeWiring{}, nil
	}
	if err != nil {
		return RuntimeWiring{}, err
	}
	password, err := s.secrets.ManagedValue(ctx, applicationID, domain.DatabasePasswordSecret)
	if err != nil {
		return RuntimeWiring{}, fmt.Errorf("database password: %w", err)
	}
	return RuntimeWiring{Env: s.ConnectionEnv(db, password), NetworkID: db.NetworkID}, nil
}

// Deprovision implements FR-065: no live database instance survives a
// Deleted application. Best-effort on the runtime teardown itself — a
// container or network that's already gone is a success, not a failure,
// so a repeated or partially-completed deletion can always finish. The
// password in the secret store goes with the application's other secrets
// (see ApplicationResources.Deprovision).
//
// Known gap: FR-065's "per policy: purge or retain-then-purge" is purged,
// full stop. Retention would need the data-retention policy FR-065 defers
// to, which doesn't exist; a final backup would need FR-064, which isn't
// built. Documented rather than approximated with an invented policy.
func (s *DatabaseService) Deprovision(ctx context.Context, applicationID string) error {
	db, err := s.repo.GetLiveForApplication(ctx, applicationID)
	if errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		return nil
	}
	if err != nil {
		return err
	}

	if err := s.runtime.Stop(ctx, db.ContainerID); err != nil {
		log.Printf("database: failed to stop container %s for application %s: %v", db.ContainerID, applicationID, err)
	}
	if err := s.runtime.RemoveNetwork(ctx, db.NetworkID); err != nil {
		log.Printf("database: failed to remove network %s for application %s: %v", db.NetworkID, applicationID, err)
	}
	return s.repo.MarkDeprovisioned(ctx, db.ID)
}

// MigrateLegacyPlaintextPasswords moves every live database password that
// Module N stored in plaintext — before Module O existed — into the secret
// store, then clears the plaintext copy. Runs once at startup (it needs
// the encryption key, which a SQL migration doesn't have).
//
// Idempotent and safe to interrupt: the plaintext is cleared only after
// the sealed copy is stored, so the worst an interruption can do is store
// the same password twice.
func (s *DatabaseService) MigrateLegacyPlaintextPasswords(ctx context.Context) (int, error) {
	legacy, err := s.repo.ListLegacyPlaintextPasswords(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, l := range legacy {
		if err := s.secrets.PutManaged(ctx, l.ApplicationID, domain.DatabasePasswordSecret, l.Password); err != nil {
			return moved, fmt.Errorf("move the password for database %s into the secret store: %w", l.DatabaseID, err)
		}
		if err := s.repo.ClearPlaintextPassword(ctx, l.DatabaseID); err != nil {
			return moved, fmt.Errorf("clear the plaintext password for database %s: %w", l.DatabaseID, err)
		}
		moved++
	}
	return moved, nil
}

// RotatePassword implements FR-068 for the credential Module N generates.
// The new password is set on the database first and stored second; if
// storing fails, the database is put back to the old password, so the
// database and the secret store never disagree about which one is current.
// (If even that revert fails, the error says so: the database then holds a
// password nothing records, and that must not be quiet.)
//
// Postgres keeps one password per role, so there is no overlap window:
// connections already open keep working, but a new connection from an
// instance still holding the old password fails until that instance is
// restarted — which RotationService does straight afterwards.
func (s *DatabaseService) RotatePassword(ctx context.Context, applicationID string) error {
	db, err := s.repo.GetLiveForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	oldPassword, err := s.secrets.ManagedValue(ctx, applicationID, domain.DatabasePasswordSecret)
	if err != nil {
		return fmt.Errorf("read the current database password: %w", err)
	}
	newPassword, err := generatePassword()
	if err != nil {
		return err
	}
	spec := domain.DatabaseSpec{Username: db.Username, DatabaseName: db.DatabaseName, Password: newPassword}
	if err := s.runtime.SetDatabasePassword(ctx, db.ContainerID, spec); err != nil {
		return fmt.Errorf("set the new password on the database: %w", err)
	}
	if err := s.secrets.PutManaged(ctx, applicationID, domain.DatabasePasswordSecret, newPassword); err != nil {
		spec.Password = oldPassword
		if revertErr := s.runtime.SetDatabasePassword(ctx, db.ContainerID, spec); revertErr != nil {
			return fmt.Errorf("store the rotated password: %v — and putting the database back to the previous one also failed: %w", err, revertErr)
		}
		return fmt.Errorf("store the rotated password (the database was put back to the previous one): %w", err)
	}
	return nil
}
