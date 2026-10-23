// Retention implements FR-088's purge half for application logs: lines
// older than the retention period (90 days, DEC-027) are deleted. Audit
// entries are not touched — they are immutable by design (FR-106) and
// their retention is DEC-010, still open — and neither are metrics, whose
// retention (NFR-032) has not been decided.
package service

import (
	"context"
	"time"
)

type LogPurger interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

type RetentionService struct {
	logs      LogPurger
	retention time.Duration
	now       func() time.Time
}

// NewRetentionService keeps logs for retention; zero keeps them forever.
func NewRetentionService(logs LogPurger, retention time.Duration) *RetentionService {
	return &RetentionService{logs: logs, retention: retention, now: time.Now}
}

func (s *RetentionService) Retention() time.Duration { return s.retention }

// Sweep deletes every log line older than the retention period and
// reports how many it removed.
func (s *RetentionService) Sweep(ctx context.Context) (int64, error) {
	if s.retention <= 0 {
		return 0, nil
	}
	return s.logs.DeleteOlderThan(ctx, s.now().Add(-s.retention))
}
