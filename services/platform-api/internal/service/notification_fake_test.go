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
type fakeNotificationRecorder struct {
	mu    sync.Mutex
	calls []notifyCall
}

type notifyCall struct {
	ApplicationID string
	Category      domain.NotificationCategory
	Title         string
	Detail        string
	ResourceType  string
	ResourceID    string
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

func (f *fakeNotificationRecorder) all() []notifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notifyCall, len(f.calls))
	copy(out, f.calls)
	return out
}
