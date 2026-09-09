// OwnershipTransfer implements Module E's FR-016 (Transfer Application
// Ownership): the current primary owner nominates a new one, the nominee
// is notified (Module X) and must explicitly accept before the change
// takes effect, and the prior owner's row is retained (status 'revoked'
// on application_owners) rather than deleted, for audit history.
//
// Scope adaptation: FR-016's alternative flow ("Administrator performs a
// forced transfer without new-owner acceptance during offboarding") is
// not implemented — this platform has no Platform Administrator role
// (blocked on DEC-002, same gap every other admin-only flow in this
// codebase already documents). Only the owner-initiated, nominee-accepted
// main flow is built.
package domain

import (
	"errors"
	"time"
)

type TransferStatus string

const (
	TransferPending  TransferStatus = "pending"
	TransferAccepted TransferStatus = "accepted"
	TransferExpired  TransferStatus = "expired"
)

type OwnershipTransfer struct {
	ID            string
	ApplicationID string
	FromUserID    string
	ToUserID      string
	Status        TransferStatus
	InitiatedAt   time.Time
	ExpiresAt     time.Time
	ResolvedAt    *time.Time
}

var (
	// ErrTransferAlreadyPending mirrors application_owners' own
	// one-pending-transfer-per-application constraint (migration 0010) —
	// a second nomination while one is already outstanding is rejected
	// rather than silently superseding it.
	ErrTransferAlreadyPending = errors.New("a transfer is already pending for this application")
	ErrTransferNotFound       = errors.New("ownership transfer not found")
	// ErrTransferNotPending covers both "already accepted" and "already
	// expired" — AcceptTransfer only ever succeeds from pending.
	ErrTransferNotPending = errors.New("this ownership transfer is no longer pending")
	// ErrTransferExpired is the FR-016 exception-flow-specific outcome:
	// distinct from ErrTransferNotPending so a caller can tell "expired"
	// from "someone already accepted/it's otherwise resolved" — surfaced
	// via AcceptTransfer's lazy expiry check (no background sweeper).
	ErrTransferExpired    = errors.New("this ownership transfer's acceptance window has expired")
	ErrNotTransferNominee = errors.New("only the nominated new owner may accept this transfer")
)
