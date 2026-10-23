//go:build !linux

package hostdisk

import "errors"

// UsedPercent is only measured on Linux, where the platform runs; elsewhere
// (a developer's Windows or macOS build) the disk rule is skipped.
func UsedPercent(path string) (float64, error) {
	return 0, errors.New("host disk usage is only measured on Linux")
}
