package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type OwnershipTransferRepo struct {
	pool *pgxpool.Pool
}

func NewOwnershipTransferRepo(pool *pgxpool.Pool) *OwnershipTransferRepo {
	return &OwnershipTransferRepo{pool: pool}
}

func scanTransfer(row rowScanner, t *domain.OwnershipTransfer) error {
	var status string
	if err := row.Scan(&t.ID, &t.ApplicationID, &t.FromUserID, &t.ToUserID, &status, &t.InitiatedAt, &t.ExpiresAt, &t.ResolvedAt); err != nil {
		return err
	}
	t.Status = domain.TransferStatus(status)
	return nil
}

const transferColumns = `id, application_id, from_user_id, to_user_id, status, initiated_at, expires_at, resolved_at`

func (r *OwnershipTransferRepo) Create(ctx context.Context, applicationID, fromUserID, toUserID string, expiresAt time.Time) (domain.OwnershipTransfer, error) {
	var t domain.OwnershipTransfer
	err := scanTransfer(r.pool.QueryRow(ctx, `
		INSERT INTO ownership_transfers (application_id, from_user_id, to_user_id, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+transferColumns,
		applicationID, fromUserID, toUserID, expiresAt,
	), &t)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// one_pending_transfer_per_application (migration 0010).
			return domain.OwnershipTransfer{}, domain.ErrTransferAlreadyPending
		}
		return domain.OwnershipTransfer{}, fmt.Errorf("create ownership transfer: %w", err)
	}
	return t, nil
}

func (r *OwnershipTransferRepo) GetByID(ctx context.Context, id string) (domain.OwnershipTransfer, error) {
	var t domain.OwnershipTransfer
	err := scanTransfer(r.pool.QueryRow(ctx, `SELECT `+transferColumns+` FROM ownership_transfers WHERE id = $1`, id), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OwnershipTransfer{}, domain.ErrTransferNotFound
		}
		return domain.OwnershipTransfer{}, fmt.Errorf("get ownership transfer: %w", err)
	}
	return t, nil
}

// GetPendingForApplication implements the read side of the "one pending
// transfer at a time" invariant (migration 0010's partial unique index) —
// lets a caller check whether an application currently has one in flight.
func (r *OwnershipTransferRepo) GetPendingForApplication(ctx context.Context, applicationID string) (domain.OwnershipTransfer, error) {
	var t domain.OwnershipTransfer
	err := scanTransfer(r.pool.QueryRow(ctx, `
		SELECT `+transferColumns+` FROM ownership_transfers
		WHERE application_id = $1 AND status = 'pending'
	`, applicationID), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OwnershipTransfer{}, domain.ErrTransferNotFound
		}
		return domain.OwnershipTransfer{}, fmt.Errorf("get pending ownership transfer: %w", err)
	}
	return t, nil
}

// MarkResolved implements both AcceptTransfer's 'accepted' outcome and the
// lazy expiry check's 'expired' outcome — the only WHERE guard is
// status='pending', so this can never resolve an already-resolved
// transfer a second time regardless of which caller wins a race.
func (r *OwnershipTransferRepo) MarkResolved(ctx context.Context, id string, status domain.TransferStatus) (domain.OwnershipTransfer, error) {
	var t domain.OwnershipTransfer
	err := scanTransfer(r.pool.QueryRow(ctx, `
		UPDATE ownership_transfers SET status = $2, resolved_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING `+transferColumns,
		id, string(status),
	), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OwnershipTransfer{}, domain.ErrTransferNotPending
		}
		return domain.OwnershipTransfer{}, fmt.Errorf("resolve ownership transfer: %w", err)
	}
	return t, nil
}
