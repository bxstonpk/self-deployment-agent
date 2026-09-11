// Secret implements Module O's service layer (FR-066/067/069/070). See
// internal/domain/secret.go for the scope, and internal/secretbox for the
// encryption and what it does not solve.
package service

import (
	"context"
	"fmt"

	"platform-api/internal/domain"
)

// ApplicationGetter is all Module O needs to know about an application:
// whether it exists, and whether it has been deleted.
type ApplicationGetter interface {
	GetByID(ctx context.Context, id string) (domain.Application, error)
}

type SecretRepository interface {
	Upsert(ctx context.Context, s domain.ApplicationSecret) (domain.ApplicationSecret, error)
	Get(ctx context.Context, applicationID, name string) (domain.ApplicationSecret, error)
	ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationSecret, error)
	Delete(ctx context.Context, applicationID, name string, managedBy domain.SecretManagedBy) (bool, error)
	DeleteAllForApplication(ctx context.Context, applicationID string) (int64, error)
}

// SecretCipher is the seam into internal/secretbox — or, once DEC-006 is
// decided, into whatever backend replaces it.
type SecretCipher interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(sealed []byte, keyID string, aad []byte) ([]byte, error)
	KeyID() string
}

type SecretService struct {
	apps   ApplicationGetter
	owners ApplicationOwnerRepository
	repo   SecretRepository
	cipher SecretCipher
	audit  AuditRecorder
}

func NewSecretService(apps ApplicationGetter, owners ApplicationOwnerRepository, repo SecretRepository, cipher SecretCipher, audit AuditRecorder) *SecretService {
	return &SecretService{apps: apps, owners: owners, repo: repo, cipher: cipher, audit: audit}
}

// secretAAD binds a ciphertext to the one application and name it was
// sealed for. It is what makes FR-069 a property of the store itself
// rather than only of the queries in front of it: a ciphertext copied into
// another application's row — by a bug, a bad migration, or anyone with
// write access to the database — fails to decrypt there instead of being
// injected into the wrong application.
func secretAAD(applicationID, name string) []byte {
	return []byte("application/" + applicationID + "/secret/" + name)
}

// requireOwner: any active owner — primary, co-owner or contributor — may
// manage an application's secrets, the same bar as deploying it
// (docs/11_Security_Requirements.md AUTHZ-4).
func (s *SecretService) requireOwner(ctx context.Context, applicationID, userID string) error {
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

// Set implements FR-066's registration path: an owner submits a value, the
// platform encrypts it, and from then on it is write-only (FR-070). This
// returns metadata; nothing anywhere returns the value.
//
// A new value reaches the application's containers at their next start —
// a Restart applies it to a running application at once. That is as far
// as rotation goes today; see domain/secret.go on FR-068.
func (s *SecretService) Set(ctx context.Context, applicationID, requesterID, name, value string) (meta domain.SecretMetadata, err error) {
	if err := domain.ValidateSecretName(name); err != nil {
		return domain.SecretMetadata{}, err
	}
	if err := domain.ValidateSecretValue(value); err != nil {
		return domain.SecretMetadata{}, err
	}
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.SecretMetadata{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.SecretMetadata{}, err
	}
	// Its secrets were purged with it (FR-050); accepting new ones would
	// create values nothing will ever inject or clean up.
	if app.LifecycleStatus == domain.StatusDeleted {
		return domain.SecretMetadata{}, domain.ErrInvalidLifecycleTransition
	}

	// FR-070: the audit entry names the secret and its version — never its
	// value.
	defer func() {
		outcome, detail := domain.AuditSuccess, fmt.Sprintf("set secret %s (version %d)", name, meta.Version)
		if err != nil {
			outcome, detail = domain.AuditFailure, fmt.Sprintf("set secret %s: %v", name, err)
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionSetSecret,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("secret stored but audit trail failed to record: %w", auditErr)
		}
	}()

	sealed, err := s.cipher.Seal([]byte(value), secretAAD(applicationID, name))
	if err != nil {
		return domain.SecretMetadata{}, err
	}
	author := requesterID
	stored, err := s.repo.Upsert(ctx, domain.ApplicationSecret{
		ApplicationID: applicationID, Name: name, ManagedBy: domain.SecretManagedByEmployee,
		Ciphertext: sealed, KeyID: s.cipher.KeyID(), CreatedBy: &author, UpdatedBy: &author,
	})
	if err != nil {
		return domain.SecretMetadata{}, err
	}
	return stored.Metadata(), nil
}

// List returns names and history only, including platform-managed secrets
// — an owner can see that their database password exists and when it was
// last set, just not what it is.
func (s *SecretService) List(ctx context.Context, applicationID, requesterID string) ([]domain.SecretMetadata, error) {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return nil, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListForApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SecretMetadata, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Metadata())
	}
	return out, nil
}

// Delete removes an employee-managed secret. A platform-managed one is
// refused: something else depends on it, and it goes when that thing does.
func (s *SecretService) Delete(ctx context.Context, applicationID, requesterID, name string) (err error) {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return err
	}
	existing, err := s.repo.Get(ctx, applicationID, name)
	if err != nil {
		return err
	}
	if existing.ManagedBy != domain.SecretManagedByEmployee {
		return domain.ErrSecretManagedByPlatform
	}

	defer func() {
		outcome, detail := domain.AuditSuccess, fmt.Sprintf("deleted secret %s", name)
		if err != nil {
			outcome, detail = domain.AuditFailure, fmt.Sprintf("delete secret %s: %v", name, err)
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionDeleteSecret,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("secret deleted but audit trail failed to record: %w", auditErr)
		}
	}()

	deleted, err := s.repo.Delete(ctx, applicationID, name, domain.SecretManagedByEmployee)
	if err != nil {
		return err
	}
	if !deleted {
		return domain.ErrSecretNotFound
	}
	return nil
}

// PutManaged stores a value the platform generated itself — today, Module
// N's database password. No owner check: no human is acting, and the value
// never came from one.
//
// Not separately audited: audit_log requires a human actor (see the scope
// note in domain/audit.go), and the deploy that provisions a database is
// already audited against the person who triggered it.
func (s *SecretService) PutManaged(ctx context.Context, applicationID, name, value string) error {
	sealed, err := s.cipher.Seal([]byte(value), secretAAD(applicationID, name))
	if err != nil {
		return err
	}
	_, err = s.repo.Upsert(ctx, domain.ApplicationSecret{
		ApplicationID: applicationID, Name: name, ManagedBy: domain.SecretManagedByPlatform,
		Ciphertext: sealed, KeyID: s.cipher.KeyID(),
	})
	return err
}

// ManagedValue reads a platform-managed value back for the module that owns
// it. It will not read an employee's secret: those leave the store only
// through EnvFor, into a container.
func (s *SecretService) ManagedValue(ctx context.Context, applicationID, name string) (string, error) {
	row, err := s.repo.Get(ctx, applicationID, name)
	if err != nil {
		return "", err
	}
	if row.ManagedBy != domain.SecretManagedByPlatform {
		return "", domain.ErrSecretNotFound
	}
	return s.open(row)
}

func (s *SecretService) open(row domain.ApplicationSecret) (string, error) {
	plaintext, err := s.cipher.Open(row.Ciphertext, row.KeyID, secretAAD(row.ApplicationID, row.Name))
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", domain.ErrSecretUnreadable, row.Name, err)
	}
	return string(plaintext), nil
}

// EnvFor implements FR-067: every employee-managed secret, as NAME=value,
// for a container about to start. Platform-managed values are left to the
// module that owns them — Module N builds DATABASE_URL from its password
// itself.
//
// All or nothing, and it fails closed — FR-067's exception flow: an
// application with a secret that can't be decrypted does not start without
// it. It is never handed a partial environment.
//
// Not audited per injection, for the same reason as PutManaged: a
// scale-to-zero cold start has no human actor to attribute it to.
func (s *SecretService) EnvFor(ctx context.Context, applicationID string) ([]string, error) {
	rows, err := s.repo.ListForApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	var env []string
	for _, row := range rows {
		if row.ManagedBy != domain.SecretManagedByEmployee {
			continue
		}
		value, err := s.open(row)
		if err != nil {
			return nil, err
		}
		env = append(env, row.Name+"="+value)
	}
	return env, nil
}

// DeleteAllForApplication implements FR-050's "revokes/deletes secrets
// (Module O)": nothing of a deleted application survives in the store.
// Revoking a third-party credential at the third party is not something
// the platform can do, and nothing here pretends otherwise.
func (s *SecretService) DeleteAllForApplication(ctx context.Context, applicationID string) error {
	_, err := s.repo.DeleteAllForApplication(ctx, applicationID)
	return err
}
