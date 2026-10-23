package service

import (
	"context"
	"testing"
	"time"
)

type fakeLogPurger struct {
	cutoffs []time.Time
}

func (f *fakeLogPurger) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	return 3, nil
}

func TestRetentionSweep_PurgesExactlyNinetyDaysBack(t *testing.T) {
	purger := &fakeLogPurger{}
	svc := NewRetentionService(purger, 90*24*time.Hour)
	now := time.Date(2026, 10, 23, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	n, err := svc.Sweep(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("unexpected result %d, %v", n, err)
	}
	if want := time.Date(2026, 7, 25, 2, 0, 0, 0, time.UTC); len(purger.cutoffs) != 1 || !purger.cutoffs[0].Equal(want) {
		t.Fatalf("expected a cutoff of %s, got %v", want, purger.cutoffs)
	}
}

func TestRetentionSweep_ZeroKeepsEverything(t *testing.T) {
	purger := &fakeLogPurger{}
	if _, err := NewRetentionService(purger, 0).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(purger.cutoffs) != 0 {
		t.Fatalf("a zero retention must purge nothing, got %v", purger.cutoffs)
	}
}
