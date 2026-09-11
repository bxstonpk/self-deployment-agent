package domain

import (
	"testing"
	"time"
)

func TestLogCursor_RoundTrips(t *testing.T) {
	at := time.Date(2026, 9, 11, 4, 5, 6, 123456000, time.UTC)
	c, err := ParseLogCursor(LogCursor{LoggedAt: at, ID: 42}.String())
	if err != nil || !c.LoggedAt.Equal(at) || c.ID != 42 {
		t.Fatalf("got %+v, %v", c, err)
	}
	for _, bad := range []string{"", "abc", "1.x", "1", "-1.2", "1.0"} {
		if _, err := ParseLogCursor(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestClampLogLimit(t *testing.T) {
	for in, want := range map[int]int{0: DefaultLogQueryLimit, -5: DefaultLogQueryLimit, 7: 7, MaxLogQueryLimit + 1: MaxLogQueryLimit} {
		if got := ClampLogLimit(in); got != want {
			t.Errorf("ClampLogLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
