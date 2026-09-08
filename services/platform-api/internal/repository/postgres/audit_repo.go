package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"platform-api/internal/domain"
)

type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

// auditChainLockKey is an arbitrary, fixed Postgres advisory-lock key that
// serializes every Record call against every other one. Without it, two
// concurrent writers could both read the same "latest" entry_hash as their
// prev_hash and each insert believing they extend the chain, silently
// forking it — pg_advisory_xact_lock makes the read-latest-hash +ω insert
// pair atomic across the whole table, not just one row.
const auditChainLockKey int64 = 0x41554449544c4f47 // ASCII "AUDITLOG", just needs to be a stable constant

// auditGenesisHash is the fixed prev_hash of the very first entry ever
// recorded — there is no real previous entry to chain from.
const auditGenesisHash = "genesis"

func newAuditEntryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// computeEntryHash implements FR-106's chain: sha256 over the previous
// entry's hash and this entry's own immutable content, joined with the
// ASCII unit-separator (0x1f) rather than a printable delimiter so no
// combination of field values can be crafted to collide across a field
// boundary.
func computeEntryHash(prevHash, id string, occurredAt time.Time, actorUserID string, action domain.AuditAction, resourceType, resourceID string, outcome domain.AuditOutcome, detail string) string {
	fields := []string{
		prevHash, id, occurredAt.UTC().Format(time.RFC3339Nano), actorUserID,
		string(action), resourceType, resourceID, string(outcome), detail,
	}
	h := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return hex.EncodeToString(h[:])
}

// Record implements FR-103's write path and FR-106's chain-building.
func (r *AuditRepo) Record(ctx context.Context, entry domain.AuditEntry) (domain.AuditEntry, error) {
	id, err := newAuditEntryID()
	if err != nil {
		return domain.AuditEntry{}, fmt.Errorf("generate audit entry id: %w", err)
	}
	// Truncated to microseconds BEFORE hashing, not just before insert:
	// Postgres's timestamptz column only stores microsecond precision, so a
	// hash computed here over the untruncated nanosecond-precision value
	// would never match the hash VerifyChain recomputes after reading the
	// (silently rounded) value back — every entry would report as "tampered"
	// from the moment it's written. Found for real: the very first entry
	// ever recorded failed its own integrity check on a live database.
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AuditEntry{}, fmt.Errorf("begin audit tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit below succeeds

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditChainLockKey); err != nil {
		return domain.AuditEntry{}, fmt.Errorf("acquire audit chain lock: %w", err)
	}

	prevHash := auditGenesisHash
	err = tx.QueryRow(ctx, `SELECT entry_hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&prevHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.AuditEntry{}, fmt.Errorf("read previous audit hash: %w", err)
	}

	entryHash := computeEntryHash(prevHash, id, occurredAt, entry.ActorUserID, entry.Action, entry.ResourceType, entry.ResourceID, entry.Outcome, entry.Detail)

	var seq int64
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_log (id, occurred_at, actor_user_id, action, resource_type, resource_id, outcome, detail, prev_hash, entry_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING seq
	`, id, occurredAt, entry.ActorUserID, string(entry.Action), entry.ResourceType, entry.ResourceID, string(entry.Outcome), entry.Detail, prevHash, entryHash,
	).Scan(&seq)
	if err != nil {
		return domain.AuditEntry{}, fmt.Errorf("insert audit entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.AuditEntry{}, fmt.Errorf("commit audit tx: %w", err)
	}

	entry.ID, entry.Seq, entry.OccurredAt = id, seq, occurredAt
	entry.PrevHash, entry.EntryHash = prevHash, entryHash
	return entry, nil
}

// Query implements FR-104: filter by any combination of actor, resource,
// action, and time range, newest first. All filter VALUES are bound as
// query parameters ($N placeholders) — only column names/keywords below are
// literal, so this is not vulnerable to injection despite being built up
// conditionally.
func (r *AuditRepo) Query(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEntry, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var conditions []string
	var args []any
	bind := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.ActorUserID != "" {
		conditions = append(conditions, "actor_user_id = "+bind(q.ActorUserID))
	}
	if q.ResourceType != "" {
		conditions = append(conditions, "resource_type = "+bind(q.ResourceType))
	}
	if q.ResourceID != "" {
		conditions = append(conditions, "resource_id = "+bind(q.ResourceID))
	}
	if q.Action != "" {
		conditions = append(conditions, "action = "+bind(string(q.Action)))
	}
	if q.From != nil {
		conditions = append(conditions, "occurred_at >= "+bind(*q.From))
	}
	if q.To != nil {
		conditions = append(conditions, "occurred_at <= "+bind(*q.To))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	limitParam := bind(limit)

	sql := fmt.Sprintf(`
		SELECT id, seq, occurred_at, actor_user_id, action, resource_type, resource_id, outcome, detail, prev_hash, entry_hash
		FROM audit_log
		%s
		ORDER BY seq DESC
		LIMIT %s
	`, where, limitParam)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var out []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var action, outcome string
		if err := rows.Scan(&e.ID, &e.Seq, &e.OccurredAt, &e.ActorUserID, &action, &e.ResourceType, &e.ResourceID, &outcome, &e.Detail, &e.PrevHash, &e.EntryHash); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		e.Action, e.Outcome = domain.AuditAction(action), domain.AuditOutcome(outcome)
		out = append(out, e)
	}
	return out, rows.Err()
}

// VerifyChain implements FR-106's detective control: walks the whole table
// oldest-first, recomputing each entry's hash and confirming it both
// matches what's stored AND correctly chains from the previous entry. The
// first entry where either check fails is reported as the tamper point.
func (r *AuditRepo) VerifyChain(ctx context.Context) (ok bool, brokenAtSeq int64, err error) {
	rows, err := r.pool.Query(ctx, `
		SELECT seq, id, occurred_at, actor_user_id, action, resource_type, resource_id, outcome, detail, prev_hash, entry_hash
		FROM audit_log ORDER BY seq ASC
	`)
	if err != nil {
		return false, 0, fmt.Errorf("verify audit chain: %w", err)
	}
	defer rows.Close()

	expectedPrev := auditGenesisHash
	for rows.Next() {
		var seq int64
		var id, actorUserID, action, resourceType, resourceID, outcome, detail, prevHash, entryHash string
		var occurredAt time.Time
		if err := rows.Scan(&seq, &id, &occurredAt, &actorUserID, &action, &resourceType, &resourceID, &outcome, &detail, &prevHash, &entryHash); err != nil {
			return false, 0, fmt.Errorf("scan audit row: %w", err)
		}
		if prevHash != expectedPrev {
			return false, seq, nil
		}
		recomputed := computeEntryHash(prevHash, id, occurredAt, actorUserID, domain.AuditAction(action), resourceType, resourceID, domain.AuditOutcome(outcome), detail)
		if recomputed != entryHash {
			return false, seq, nil
		}
		expectedPrev = entryHash
	}
	if err := rows.Err(); err != nil {
		return false, 0, err
	}
	return true, 0, nil
}
