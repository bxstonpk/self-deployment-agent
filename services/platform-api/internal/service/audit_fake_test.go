package service_test

import (
	"context"
	"sync"

	"platform-api/internal/domain"
)

// fakeAuditRecorder implements service.AuditRecorder for every other
// service's tests — they only need to know an entry was (or wasn't)
// recorded, not exercise audit_service.go's own logic (that's
// audit_service_test.go's job).
type fakeAuditRecorder struct {
	mu       sync.Mutex
	entries  []domain.AuditEntry
	failNext bool
}

func newFakeAuditRecorder() *fakeAuditRecorder {
	return &fakeAuditRecorder{}
}

func (f *fakeAuditRecorder) Record(ctx context.Context, entry domain.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errAuditWriteFailed
	}
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAuditRecorder) all() []domain.AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AuditEntry, len(f.entries))
	copy(out, f.entries)
	return out
}

var errAuditWriteFailed = &fakeAuditError{"simulated audit write failure"}

type fakeAuditError struct{ msg string }

func (e *fakeAuditError) Error() string { return e.msg }
