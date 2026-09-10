package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type ApplicationOwnerRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationOwnerRepo(pool *pgxpool.Pool) *ApplicationOwnerRepo {
	return &ApplicationOwnerRepo{pool: pool}
}

// AssignPrimaryOwner implements the FR-011/FR-015 default path: the
// registering employee becomes the sole active primary owner. The partial
// unique index one_active_primary_owner_per_application (see the 0001
// migration) is the actual enforcement point; this is a defensive check on
// top of it.
func (r *ApplicationOwnerRepo) AssignPrimaryOwner(ctx context.Context, applicationID, userID, assignedBy string) (domain.ApplicationOwner, error) {
	var o domain.ApplicationOwner
	var role string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO application_owners (application_id, user_id, ownership_role, assigned_by)
		VALUES ($1, $2, 'primary', $3)
		RETURNING id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
	`, applicationID, userID, assignedBy).Scan(
		&o.ID, &o.ApplicationID, &o.UserID, &role, &o.AssignedBy, &o.AssignedAt, &o.Status,
	)
	if err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("assign primary owner: %w", err)
	}
	o.OwnershipRole = domain.OwnershipRole(role)
	return o, nil
}

// AddOwner implements FR-017's main flow (grant co-owner/contributor
// access). Upserts on the (application_id, user_id, ownership_role) unique
// constraint rather than a plain INSERT: re-granting the same role to
// someone whose access was previously revoked must re-activate that row,
// not fail on a stale unique-constraint violation from the original grant.
func (r *ApplicationOwnerRepo) AddOwner(ctx context.Context, applicationID, userID string, role domain.OwnershipRole, assignedBy string) (domain.ApplicationOwner, error) {
	var o domain.ApplicationOwner
	var roleStr string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO application_owners (application_id, user_id, ownership_role, assigned_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (application_id, user_id, ownership_role)
		DO UPDATE SET status = 'active', assigned_by = $4, assigned_at = now()
		RETURNING id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
	`, applicationID, userID, string(role), assignedBy).Scan(
		&o.ID, &o.ApplicationID, &o.UserID, &roleStr, &o.AssignedBy, &o.AssignedAt, &o.Status,
	)
	if err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("add owner: %w", err)
	}
	o.OwnershipRole = domain.OwnershipRole(roleStr)
	return o, nil
}

// Revoke implements FR-017's alternative flow (remove a co-owner/
// contributor). Deliberately excludes ownership_role = 'primary' from its
// WHERE clause — revoking the primary owner is FR-016's Transfer
// Ownership flow (see ReplacePrimaryOwner below), never a side effect of
// this path. Returns the number of rows revoked so the caller can
// distinguish "nothing to revoke" (0) from a real revocation.
func (r *ApplicationOwnerRepo) Revoke(ctx context.Context, applicationID, userID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE application_owners SET status = 'revoked'
		WHERE application_id = $1 AND user_id = $2 AND status = 'active' AND ownership_role != 'primary'
	`, applicationID, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke owner: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ReplacePrimaryOwner implements FR-016's accepted-transfer effect: the
// prior primary owner's row is revoked — retained for audit history, per
// FR-016's business rule ("prior owner is retained in history"), never
// deleted — and the new primary owner's row is upserted (same UPSERT
// semantics as AddOwner, so a user newly promoted from an existing
// co-owner/contributor role re-activates cleanly). Any other active,
// now-redundant role the new primary previously held on this same
// application (e.g. they were a co-owner being promoted) is revoked too,
// so they don't end up listed twice under two simultaneous roles.
//
// Runs its three statements inside a real database transaction — the one
// place in this codebase that does, and deliberately so. Revoking the
// prior primary has to happen before inserting the new one (otherwise the
// insert trips one_active_primary_owner_per_application), which means an
// unguarded sequence leaves a window where the application has *zero*
// active primary owners. That is not a theoretical concern: an
// application in that state is permanently stranded, because every
// owner-gated action — including the transfer and grant paths that could
// repair it — requires an active owner to authorize it. Verified by
// replaying these statements unguarded against a real database and
// watching the application become untouchable by anyone, including the
// employee who created it. FR-018's "orphaned application", exactly.
//
// The prior primary's row is revoked, never deleted — retained for audit
// history, per FR-016's own business rule. Any other active,
// now-redundant role the new primary held on this same application (e.g.
// they were a co-owner being promoted) is revoked too, so they aren't
// listed twice under two simultaneous roles.
func (r *ApplicationOwnerRepo) ReplacePrimaryOwner(ctx context.Context, applicationID, oldPrimaryUserID, newPrimaryUserID, assignedBy string) (domain.ApplicationOwner, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("begin ownership transfer: %w", err)
	}
	// No-op once Commit has succeeded; on any early return below this is
	// what puts the prior owner back.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE application_owners SET status = 'revoked'
		WHERE application_id = $1 AND user_id = $2 AND ownership_role = 'primary' AND status = 'active'
	`, applicationID, oldPrimaryUserID); err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("revoke prior primary owner: %w", err)
	}

	var o domain.ApplicationOwner
	var roleStr string
	err = tx.QueryRow(ctx, `
		INSERT INTO application_owners (application_id, user_id, ownership_role, assigned_by)
		VALUES ($1, $2, 'primary', $3)
		ON CONFLICT (application_id, user_id, ownership_role)
		DO UPDATE SET status = 'active', assigned_by = $3, assigned_at = now()
		RETURNING id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
	`, applicationID, newPrimaryUserID, assignedBy).Scan(
		&o.ID, &o.ApplicationID, &o.UserID, &roleStr, &o.AssignedBy, &o.AssignedAt, &o.Status,
	)
	if err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("assign new primary owner: %w", err)
	}
	o.OwnershipRole = domain.OwnershipRole(roleStr)

	if _, err := tx.Exec(ctx, `
		UPDATE application_owners SET status = 'revoked'
		WHERE application_id = $1 AND user_id = $2 AND ownership_role != 'primary' AND status = 'active'
	`, applicationID, newPrimaryUserID); err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("clean up new primary owner's prior role: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationOwner{}, fmt.Errorf("commit ownership transfer: %w", err)
	}
	return o, nil
}

// ListApplicationIDsForUser is the reverse of ListForApplication: every
// application this user currently holds any active ownership role on
// (primary, secondary or technical alike — Module AB's reports scope to
// "applications you own" without distinguishing which kind, the same way
// every requireOwner check already does). Added for Module AB (Reporting);
// nothing needed this direction before, which is why the table's
// idx_application_owners_user index existed unused until now.
func (r *ApplicationOwnerRepo) ListApplicationIDsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT application_id
		FROM application_owners
		WHERE user_id = $1 AND status = 'active'
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list applications for user: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan application id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *ApplicationOwnerRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, application_id, user_id, ownership_role, COALESCE(assigned_by::text, ''), assigned_at, status
		FROM application_owners
		WHERE application_id = $1
		ORDER BY assigned_at ASC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list application owners: %w", err)
	}
	defer rows.Close()

	var out []domain.ApplicationOwner
	for rows.Next() {
		var o domain.ApplicationOwner
		var role string
		if err := rows.Scan(&o.ID, &o.ApplicationID, &o.UserID, &role, &o.AssignedBy, &o.AssignedAt, &o.Status); err != nil {
			return nil, fmt.Errorf("scan application owner row: %w", err)
		}
		o.OwnershipRole = domain.OwnershipRole(role)
		out = append(out, o)
	}
	return out, rows.Err()
}
