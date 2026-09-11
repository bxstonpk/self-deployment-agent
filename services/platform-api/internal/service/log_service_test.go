package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeLogReader struct {
	lines []domain.LogLine
	last  domain.LogQuery
}

func (f *fakeLogReader) Query(ctx context.Context, q domain.LogQuery) ([]domain.LogLine, error) {
	f.last = q
	return f.lines, nil
}

func newLogFixture() (*service.LogService, *fakeLogReader, *fakeAuditRecorder) {
	apps := newFakeLifecycleRepo(
		domain.Application{ID: "app-1", Name: "overtime", LifecycleStatus: domain.StatusRunning},
		domain.Application{ID: "app-2", Name: "leave-tracker", LifecycleStatus: domain.StatusRunning},
	)
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	owners.owners["app-2"] = []domain.ApplicationOwner{{ApplicationID: "app-2", UserID: "owner-2", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	reader := &fakeLogReader{lines: []domain.LogLine{{ApplicationID: "app-1", Service: "api", Message: "listening"}}}
	audit := newFakeAuditRecorder()
	return service.NewLogService(apps, owners, reader, audit), reader, audit
}

func TestLogs_OwnerReadsTheirApplicationsLines(t *testing.T) {
	svc, reader, _ := newLogFixture()

	lines, err := svc.Query(context.Background(), "owner-1", domain.LogQuery{ApplicationID: "app-1", Service: "api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || reader.last.ApplicationID != "app-1" || reader.last.Service != "api" {
		t.Fatalf("unexpected result %+v for query %+v", lines, reader.last)
	}
}

// FR-087: a refusal must look exactly like "no such application".
func TestLogs_SomeoneElsesApplicationLooksNonexistent(t *testing.T) {
	svc, reader, audit := newLogFixture()

	_, refused := svc.Query(context.Background(), "owner-2", domain.LogQuery{ApplicationID: "app-1"})
	_, missing := svc.Query(context.Background(), "owner-2", domain.LogQuery{ApplicationID: "no-such-app"})
	if !errors.Is(refused, domain.ErrApplicationNotFound) {
		t.Fatalf("expected ErrApplicationNotFound for another owner's application, got %v", refused)
	}
	if missing == nil {
		t.Fatal("expected an error for a nonexistent application")
	}
	if reader.last.ApplicationID != "" {
		t.Fatal("the store was queried for a refused request")
	}
	// An audit entry for the refusal would show up in the requester's own
	// view of the audit log and confirm the application exists.
	if len(audit.all()) != 0 {
		t.Fatalf("a refused read was audited: %+v", audit.all())
	}
}

func TestLogs_LimitIsDefaultedAndCapped(t *testing.T) {
	svc, reader, _ := newLogFixture()
	ctx := context.Background()

	if _, err := svc.Query(ctx, "owner-1", domain.LogQuery{ApplicationID: "app-1"}); err != nil {
		t.Fatal(err)
	}
	if reader.last.Limit != domain.DefaultLogQueryLimit {
		t.Fatalf("expected the default limit, got %d", reader.last.Limit)
	}
	if _, err := svc.Query(ctx, "owner-1", domain.LogQuery{ApplicationID: "app-1", Limit: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	if reader.last.Limit != domain.MaxLogQueryLimit {
		t.Fatalf("expected the limit capped at %d, got %d", domain.MaxLogQueryLimit, reader.last.Limit)
	}
}

// FR-089 / §13.9: reading logs is itself an audited event — but the text
// someone searched for might be sensitive, so only the fact of a filter is.
func TestLogs_EveryReadIsAudited_WithoutTheSearchText(t *testing.T) {
	svc, _, audit := newLogFixture()

	if _, err := svc.Query(context.Background(), "owner-1", domain.LogQuery{ApplicationID: "app-1", Service: "api", Contains: "sk-live-secret"}); err != nil {
		t.Fatal(err)
	}
	entries := audit.all()
	if len(entries) != 1 || entries[0].Action != domain.AuditActionReadLogs || entries[0].ActorUserID != "owner-1" || entries[0].ResourceID != "app-1" {
		t.Fatalf("expected one application.read_logs entry, got %+v", entries)
	}
	if strings.Contains(entries[0].Detail, "sk-live-secret") {
		t.Fatalf("the search text was written to the audit trail: %q", entries[0].Detail)
	}
	if !strings.Contains(entries[0].Detail, "service=api") || !strings.Contains(entries[0].Detail, "text filter") {
		t.Fatalf("the audit entry doesn't describe the read: %q", entries[0].Detail)
	}
}
