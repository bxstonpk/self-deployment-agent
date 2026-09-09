package service_test

import (
	"context"
	"sync"

	"platform-api/internal/domain"
)

// fakeNotificationRecorder implements service.NotificationRecorder for
// every other service's tests — they only need to know whether/who got
// notified, not exercise notification_service.go's own logic (that's
// notification_service_test.go's job).
// Implements both service.NotificationRecorder (NotifyOwners) and
// service.SingleUserNotifier (NotifyUser) — one fake covering both narrow
// interfaces is simpler than two, and no test needs them to vary
// independently.
type fakeNotificationRecorder struct {
	mu        sync.Mutex
	calls     []notifyCall
	userCalls []notifyUserCall
}

type notifyCall struct {
	ApplicationID string
	Category      domain.NotificationCategory
	Title         string
	Detail        string
	ResourceType  string
	ResourceID    string
}

type notifyUserCall struct {
	RecipientUserID string
	Category        domain.NotificationCategory
	Title           string
	Detail          string
	ResourceType    string
	ResourceID      string
}

func newFakeNotificationRecorder() *fakeNotificationRecorder {
	return &fakeNotificationRecorder{}
}

func (f *fakeNotificationRecorder) NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifyCall{
		ApplicationID: applicationID, Category: category, Title: title, Detail: detail,
		ResourceType: resourceType, ResourceID: resourceID,
	})
}

func (f *fakeNotificationRecorder) NotifyUser(ctx context.Context, recipientUserID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCalls = append(f.userCalls, notifyUserCall{
		RecipientUserID: recipientUserID, Category: category, Title: title, Detail: detail,
		ResourceType: resourceType, ResourceID: resourceID,
	})
}

func (f *fakeNotificationRecorder) all() []notifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notifyCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeNotificationRecorder) allUserCalls() []notifyUserCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notifyUserCall, len(f.userCalls))
	copy(out, f.userCalls)
	return out
}
