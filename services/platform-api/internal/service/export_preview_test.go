package service

import "time"

// SetPreviewClockForTest fixes a PreviewService's clock and name suffix.
func SetPreviewClockForTest(s *PreviewService, now func() time.Time, suffix func() string) {
	s.now, s.suffix = now, suffix
}
