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
	alerts    AlertHistoryPurger
	retention time.Duration
	now       func() time.Time
}

// AlertHistoryPurger is alert history (FR-092), kept as long as logs.
type AlertHistoryPurger interface {
	DeleteEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// SetAlertHistory purges alert history with the same retention as logs.
func (s *RetentionService) SetAlertHistory(p AlertHistoryPurger) { s.alerts = p }

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
	cutoff := s.now().Add(-s.retention)
	n, err := s.logs.DeleteOlderThan(ctx, cutoff)
	if err != nil || s.alerts == nil {
		return n, err
	}
	m, err := s.alerts.DeleteEventsOlderThan(ctx, cutoff)
	return n + m, err
}
