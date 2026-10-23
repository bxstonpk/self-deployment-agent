package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

// AuthRepo stores what internal/domain/auth.go describes: passwords on
// users, sessions and API keys in credentials, and device authorizations.
// It never sees a token — only the hash the service computed.
type AuthRepo struct {
	pool *pgxpool.Pool
}

func NewAuthRepo(pool *pgxpool.Pool) *AuthRepo {
	return &AuthRepo{pool: pool}
}

const userColumns = `id, full_name, email, COALESCE(department_id::text, ''), status, created_at`

// UserByEmail matches case-insensitively and, if the old exact-match
// header identity left two rows differing only in case, picks the
// earliest — the account that existed first. passwordHash is "" for an
// account that has never set one.
func (r *AuthRepo) UserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	var u domain.User
	var hash *string
	err := r.pool.QueryRow(ctx, `
		SELECT `+userColumns+`, password_hash
		FROM users WHERE lower(email) = lower($1)
		ORDER BY created_at LIMIT 1
	`, email).Scan(&u.ID, &u.FullName, &u.Email, &u.DepartmentID, &u.Status, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("get user by email: %w", err)
	}
	if hash == nil {
		return u, "", nil
	}
	return u, *hash, nil
}

// CreateUser registers a new account with its password already set.
func (r *AuthRepo) CreateUser(ctx context.Context, email, fullName, departmentID, passwordHash string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (full_name, email, department_id, password_hash, password_set_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING `+userColumns,
		fullName, email, departmentID, passwordHash,
	).Scan(&u.ID, &u.FullName, &u.Email, &u.DepartmentID, &u.Status, &u.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// ClaimUser sets the first password on an account that has none — only
// if it still has none, so two simultaneous claims can't both win.
func (r *AuthRepo) ClaimUser(ctx context.Context, userID, passwordHash string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET password_hash = $2, password_set_at = now(), last_modified_at = now()
		WHERE id = $1 AND password_hash IS NULL
	`, userID, passwordHash)
	if err != nil {
		return false, fmt.Errorf("claim user: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *AuthRepo) RecordLogin(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)
	return err
}

const credentialColumns = `id, user_id, kind, name, token_prefix, created_at, expires_at, last_used_at, revoked_at`

func scanCredential(row rowScanner, c *domain.Credential) error {
	var kind string
	if err := row.Scan(&c.ID, &c.UserID, &kind, &c.Name, &c.Prefix, &c.CreatedAt, &c.ExpiresAt, &c.LastUsedAt, &c.RevokedAt); err != nil {
		return err
	}
	c.Kind = domain.CredentialKind(kind)
	return nil
}

func (r *AuthRepo) CreateCredential(ctx context.Context, c domain.Credential, tokenHash string) (domain.Credential, error) {
	var out domain.Credential
	err := scanCredential(r.pool.QueryRow(ctx, `
		INSERT INTO credentials (user_id, kind, name, token_prefix, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+credentialColumns,
		c.UserID, string(c.Kind), c.Name, c.Prefix, tokenHash, c.ExpiresAt,
	), &out)
	if err != nil {
		return domain.Credential{}, fmt.Errorf("create credential: %w", err)
	}
	return out, nil
}

// CredentialByHash returns the credential and its user, whether or not
// the credential is still active — the service decides that, so the rule
// lives in one place.
func (r *AuthRepo) CredentialByHash(ctx context.Context, tokenHash string) (domain.Credential, domain.User, error) {
	var c domain.Credential
	var u domain.User
	var kind string
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.user_id, c.kind, c.name, c.token_prefix, c.created_at, c.expires_at, c.last_used_at, c.revoked_at,
		       u.id, u.full_name, u.email, COALESCE(u.department_id::text, ''), u.status, u.created_at
		FROM credentials c JOIN users u ON u.id = c.user_id
		WHERE c.token_hash = $1
	`, tokenHash).Scan(
		&c.ID, &c.UserID, &kind, &c.Name, &c.Prefix, &c.CreatedAt, &c.ExpiresAt, &c.LastUsedAt, &c.RevokedAt,
		&u.ID, &u.FullName, &u.Email, &u.DepartmentID, &u.Status, &u.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Credential{}, domain.User{}, domain.ErrInvalidToken
	}
	if err != nil {
		return domain.Credential{}, domain.User{}, fmt.Errorf("look up credential: %w", err)
	}
	c.Kind = domain.CredentialKind(kind)
	return c, u, nil
}

func (r *AuthRepo) TouchCredential(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE credentials SET last_used_at = now() WHERE id = $1`, id)
	return err
}

func (r *AuthRepo) ListCredentials(ctx context.Context, userID string, kind domain.CredentialKind) ([]domain.Credential, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+credentialColumns+` FROM credentials
		WHERE user_id = $1 AND kind = $2
		ORDER BY created_at DESC
	`, userID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	out := []domain.Credential{}
	for rows.Next() {
		var c domain.Credential
		if err := scanCredential(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RevokeCredential revokes one of the user's own credentials. Someone
// else's id is "not found", same as one that doesn't exist.
func (r *AuthRepo) RevokeCredential(ctx context.Context, userID, id string) (domain.Credential, error) {
	var out domain.Credential
	err := scanCredential(r.pool.QueryRow(ctx, `
		UPDATE credentials SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1 AND user_id = $2
		RETURNING `+credentialColumns, id, userID), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Credential{}, domain.ErrCredentialNotFound
	}
	if err != nil {
		return domain.Credential{}, fmt.Errorf("revoke credential: %w", err)
	}
	return out, nil
}

const deviceColumns = `id, user_code, client_name, status, COALESCE(user_id::text, ''),
	COALESCE(credential_id::text, ''), created_at, expires_at, decided_at`

func scanDevice(row rowScanner, d *domain.DeviceAuthorization) error {
	var status string
	if err := row.Scan(&d.ID, &d.UserCode, &d.ClientName, &status, &d.UserID, &d.CredentialID,
		&d.CreatedAt, &d.ExpiresAt, &d.DecidedAt); err != nil {
		return err
	}
	d.Status = domain.DeviceStatus(status)
	return nil
}

func (r *AuthRepo) CreateDevice(ctx context.Context, d domain.DeviceAuthorization, deviceCodeHash string) (domain.DeviceAuthorization, error) {
	var out domain.DeviceAuthorization
	err := scanDevice(r.pool.QueryRow(ctx, `
		INSERT INTO device_authorizations (device_code_hash, user_code, client_name, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+deviceColumns, deviceCodeHash, d.UserCode, d.ClientName, d.ExpiresAt), &out)
	if err != nil {
		return domain.DeviceAuthorization{}, fmt.Errorf("create device authorization: %w", err)
	}
	return out, nil
}

func (r *AuthRepo) DeviceByCodeHash(ctx context.Context, deviceCodeHash string) (domain.DeviceAuthorization, error) {
	var out domain.DeviceAuthorization
	err := scanDevice(r.pool.QueryRow(ctx, `
		SELECT `+deviceColumns+` FROM device_authorizations WHERE device_code_hash = $1
	`, deviceCodeHash), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeviceAuthorization{}, domain.ErrDeviceExpired
	}
	if err != nil {
		return domain.DeviceAuthorization{}, fmt.Errorf("get device authorization: %w", err)
	}
	return out, nil
}

// PendingDeviceByUserCode finds the live request a user code names.
func (r *AuthRepo) PendingDeviceByUserCode(ctx context.Context, userCode string) (domain.DeviceAuthorization, error) {
	var out domain.DeviceAuthorization
	err := scanDevice(r.pool.QueryRow(ctx, `
		SELECT `+deviceColumns+` FROM device_authorizations
		WHERE user_code = $1 AND status = 'pending' AND expires_at > now()
	`, userCode), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeviceAuthorization{}, domain.ErrUserCodeNotFound
	}
	if err != nil {
		return domain.DeviceAuthorization{}, fmt.Errorf("get device authorization: %w", err)
	}
	return out, nil
}

// DecideDevice approves or denies a still-pending, unexpired request.
func (r *AuthRepo) DecideDevice(ctx context.Context, id, userID string, approve bool) (domain.DeviceAuthorization, error) {
	status := domain.DeviceDenied
	if approve {
		status = domain.DeviceApproved
	}
	var out domain.DeviceAuthorization
	err := scanDevice(r.pool.QueryRow(ctx, `
		UPDATE device_authorizations SET status = $3, user_id = $2, decided_at = now()
		WHERE id = $1 AND status = 'pending' AND expires_at > now()
		RETURNING `+deviceColumns, id, userID, string(status)), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeviceAuthorization{}, domain.ErrUserCodeNotFound
	}
	if err != nil {
		return domain.DeviceAuthorization{}, fmt.Errorf("decide device authorization: %w", err)
	}
	return out, nil
}

// ClaimApprovedDevice marks an approved request consumed and returns who
// approved it. Exactly one poll can win, so a device code yields exactly
// one key however many times it's presented.
func (r *AuthRepo) ClaimApprovedDevice(ctx context.Context, id string) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx, `
		UPDATE device_authorizations SET status = 'consumed'
		WHERE id = $1 AND status = 'approved'
		RETURNING user_id::text
	`, id).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrDeviceExpired
	}
	if err != nil {
		return "", fmt.Errorf("claim device authorization: %w", err)
	}
	return userID, nil
}

func (r *AuthRepo) SetDeviceCredential(ctx context.Context, id, credentialID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE device_authorizations SET credential_id = $2 WHERE id = $1`, id, credentialID)
	return err
}
