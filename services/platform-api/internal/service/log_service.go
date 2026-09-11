// Log implements Module S's read side (FR-087/FR-089). See
// internal/domain/logs.go for what is collected, and how.
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"platform-api/internal/domain"
)

type LogReader interface {
	Query(ctx context.Context, q domain.LogQuery) ([]domain.LogLine, error)
}

type LogService struct {
	apps   ApplicationGetter
	owners ApplicationOwnerRepository
	logs   LogReader
	audit  AuditRecorder
}

func NewLogService(apps ApplicationGetter, owners ApplicationOwnerRepository, logs LogReader, audit AuditRecorder) *LogService {
	return &LogService{apps: apps, owners: owners, logs: logs, audit: audit}
}

func (s *LogService) isOwner(ctx context.Context, applicationID, userID string) (bool, error) {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return false, err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

// Query implements FR-087. Anyone who isn't an owner gets exactly what they
// would for an application that doesn't exist — FR-087's "rejected without
// leaking whether the application even exists".
//
// That is also why a refused read is not audited. The audit log shows
// people their own actions, so an entry for the refusal would tell them
// what the 404 was careful not to. Every read that returns lines is
// audited — the filters, never the text searched for, which could itself
// be something sensitive.
func (s *LogService) Query(ctx context.Context, requesterID string, q domain.LogQuery) (lines []domain.LogLine, err error) {
	if _, err := s.apps.GetByID(ctx, q.ApplicationID); err != nil {
		return nil, err
	}
	owner, err := s.isOwner(ctx, q.ApplicationID, requesterID)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, domain.ErrApplicationNotFound
	}
	q.Limit = domain.ClampLogLimit(q.Limit)

	defer func() {
		outcome, detail := domain.AuditSuccess, fmt.Sprintf("read %d log line(s)%s", len(lines), describeLogQuery(q))
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionReadLogs,
			ResourceType: "application", ResourceID: q.ApplicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("logs read but audit trail failed to record: %w", auditErr)
		}
	}()

	return s.logs.Query(ctx, q)
}

func describeLogQuery(q domain.LogQuery) string {
	var parts []string
	if q.Service != "" {
		parts = append(parts, "service="+q.Service)
	}
	if q.Environment != "" {
		parts = append(parts, "environment="+q.Environment)
	}
	if q.Since != nil {
		parts = append(parts, "since="+q.Since.UTC().Format(time.RFC3339))
	}
	if q.Until != nil {
		parts = append(parts, "until="+q.Until.UTC().Format(time.RFC3339))
	}
	if q.Contains != "" {
		parts = append(parts, "with a text filter")
	}
	if q.Before != nil {
		parts = append(parts, "a further page")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
