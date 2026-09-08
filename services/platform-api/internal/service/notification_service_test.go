package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeNotificationRepo struct {
	entries   []domain.Notification
	next      int
	createErr error
}

func newFakeNotificationRepo() *fakeNotificationRepo {
	return &fakeNotificationRepo{}
}

func (f *fakeNotificationRepo) Create(ctx context.Context, n domain.Notification) (domain.Notification, error) {
	if f.createErr != nil {
		return domain.Notification{}, f.createErr
	}
	f.next++
	n.ID = "notif-" + string(rune('0'+f.next))
	n.CreatedAt = time.Now()
	f.entries = append(f.entries, n)
	return n, nil
}

func (f *fakeNotificationRepo) ListForRecipient(ctx context.Context, recipientUserID string, unreadOnly bool, limit int) ([]domain.Notification, error) {
	var out []domain.Notification
	for i := len(f.entries) - 1; i >= 0; i-- {
		n := f.entries[i]
		if n.RecipientUserID != recipientUserID {
			continue
		}
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeNotificationRepo) MarkRead(ctx context.Context, id, recipientUserID string) (domain.Notification, error) {
	for i, n := range f.entries {
		if n.ID == id && n.RecipientUserID == recipientUserID {
			now := time.Now()
			f.entries[i].ReadAt = &now
			return f.entries[i], nil
		}
	}
	return domain.Notification{}, domain.ErrNotFound
}

func newNotificationService() (*service.NotificationService, *fakeNotificationRepo, *fakeOwnerRepo) {
	repo := newFakeNotificationRepo()
	owners := newFakeOwnerRepo()
	return service.NewNotificationService(repo, owners), repo, owners
}

func TestNotifyOwners_CreatesOneEntryPerActiveOwner(t *testing.T) {
	svc, repo, owners := newNotificationService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
		{ApplicationID: "app-1", UserID: "owner-2", OwnershipRole: domain.OwnerRoleSecondary, Status: "active"},
	}

	svc.NotifyOwners(context.Background(), "app-1", domain.NotificationDeploymentStatus, "Deployment succeeded", "detail", "deployment", "dep-1")

	if len(repo.entries) != 2 {
		t.Fatalf("expected one notification per active owner, got %d", len(repo.entries))
	}
	recipients := map[string]bool{repo.entries[0].RecipientUserID: true, repo.entries[1].RecipientUserID: true}
	if !recipients["owner-1"] || !recipients["owner-2"] {
		t.Fatalf("expected both owners notified, got %+v", repo.entries)
	}
}

func TestNotifyOwners_SkipsRevokedOwners(t *testing.T) {
	svc, repo, owners := newNotificationService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
		{ApplicationID: "app-1", UserID: "former-owner", OwnershipRole: domain.OwnerRoleSecondary, Status: "revoked"},
	}

	svc.NotifyOwners(context.Background(), "app-1", domain.NotificationDeploymentStatus, "Deployment failed", "detail", "deployment", "dep-1")

	if len(repo.entries) != 1 || repo.entries[0].RecipientUserID != "owner-1" {
		t.Fatalf("expected only the active owner notified, got %+v", repo.entries)
	}
}

// FR-107's exception flow: notification delivery failure must not block
// the underlying deployment pipeline — NotifyOwners has no return value at
// all for a caller to check, so the only observable behavior is that it
// doesn't panic and simply logs.
func TestNotifyOwners_RepoFailure_DoesNotPanicOrBlock(t *testing.T) {
	svc, repo, owners := newNotificationService()
	owners.owners["app-1"] = []domain.ApplicationOwner{
		{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	repo.createErr = errors.New("db unavailable")

	svc.NotifyOwners(context.Background(), "app-1", domain.NotificationDeploymentStatus, "Deployment failed", "detail", "deployment", "dep-1")
	// No assertion beyond "this didn't panic" — that's the whole contract.
}

func TestNotificationList_ScopedToRecipient(t *testing.T) {
	svc, repo, _ := newNotificationService()
	repo.entries = []domain.Notification{
		{ID: "n1", RecipientUserID: "user-1", Title: "for user-1"},
		{ID: "n2", RecipientUserID: "user-2", Title: "for user-2"},
	}

	got, err := svc.List(context.Background(), "user-1", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "n1" {
		t.Fatalf("expected only user-1's own notification, got %+v", got)
	}
}

func TestNotificationMarkRead_OnlyAffectsOwnNotification(t *testing.T) {
	svc, repo, _ := newNotificationService()
	repo.entries = []domain.Notification{{ID: "n1", RecipientUserID: "user-1", Title: "mine"}}

	if _, err := svc.MarkRead(context.Background(), "n1", "user-2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound when a different user tries to mark it read, got %v", err)
	}

	updated, err := svc.MarkRead(context.Background(), "n1", "user-1")
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if updated.ReadAt == nil {
		t.Fatal("expected ReadAt to be set")
	}
}
