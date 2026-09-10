// Database implements Module N's service layer
// (docs/02_Functional_Requirements.md FR-061/062/063/065). See
// internal/domain/database.go's package comment for the scope this slice
// covers — in particular that FR-063's at-rest half is NOT satisfied,
// because Module O (Secret Management) doesn't exist to satisfy it.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"

	"platform-api/internal/domain"
)

// postgresImage is the engine image every provisioned database runs. Pinned
// rather than floating on :latest, for the same reason build_service.go's
// base images are: a deployment that worked yesterday should not start
// failing because an upstream tag moved underneath it.
const postgresImage = "postgres:16-alpine"

const postgresPort = 5432

type ProvisionedDatabaseRepository interface {
	Create(ctx context.Context, d domain.ProvisionedDatabase) (domain.ProvisionedDatabase, error)
	GetLiveForApplication(ctx context.Context, applicationID string) (domain.ProvisionedDatabase, error)
	MarkDeprovisioned(ctx context.Context, id string) error
}

// DatabaseRuntime is the narrow seam into the Runtime Platform that
// Module N needs — deliberately separate from RuntimeEngine (the
// application-container seam) so a caller that only starts applications
// isn't handed database-provisioning powers it has no business with.
type DatabaseRuntime interface {
	CreateNetwork(ctx context.Context, name string) (string, error)
	RemoveNetwork(ctx context.Context, networkID string) error
	StartDatabase(ctx context.Context, spec domain.DatabaseSpec) (string, error)
	Stop(ctx context.Context, containerID string) error
}

type DatabaseService struct {
	repo    ProvisionedDatabaseRepository
	runtime DatabaseRuntime
}

func NewDatabaseService(repo ProvisionedDatabaseRepository, runtime DatabaseRuntime) *DatabaseService {
	return &DatabaseService{repo: repo, runtime: runtime}
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
		// still attaching to it.
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
		Password:     password,
	})
}

// ConnectionEnv implements FR-063's delivery half: the connection details
// the application's runtime needs, as environment variables it can read
// without anyone having written them down anywhere.
//
// DATABASE_URL is the whole DSN (what most frameworks read directly); the
// individual parts are provided too, since plenty of libraries want host
// and port separately rather than parsing a URL.
func (s *DatabaseService) ConnectionEnv(db domain.ProvisionedDatabase) []string {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		db.Username, db.Password, db.Host, db.Port, db.DatabaseName)
	return []string{
		"DATABASE_URL=" + dsn,
		"DATABASE_HOST=" + db.Host,
		fmt.Sprintf("DATABASE_PORT=%d", db.Port),
		"DATABASE_NAME=" + db.DatabaseName,
		"DATABASE_USER=" + db.Username,
		"DATABASE_PASSWORD=" + db.Password,
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
func (s *DatabaseService) WiringFor(ctx context.Context, applicationID string) (RuntimeWiring, error) {
	db, err := s.repo.GetLiveForApplication(ctx, applicationID)
	if errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		return RuntimeWiring{}, nil
	}
	if err != nil {
		return RuntimeWiring{}, err
	}
	return RuntimeWiring{Env: s.ConnectionEnv(db), NetworkID: db.NetworkID}, nil
}

// Deprovision implements FR-065: no live database instance survives a
// Deleted application. Best-effort on the runtime teardown itself — a
// container or network that's already gone is a success, not a failure,
// so a repeated or partially-completed deletion can always finish.
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
