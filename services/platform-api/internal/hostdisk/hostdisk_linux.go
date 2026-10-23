//go:build linux

// Package hostdisk reports how full the filesystem holding a path is —
// for the platform's own disk alert (FR-092, DEC-028), measured on the
// backup directory, which lives on the Docker host's data disk alongside
// the databases and logs.
package hostdisk

import "syscall"

// UsedPercent is the share of the filesystem's space in use, as df counts
// it (space reserved for root counts as unavailable).
func UsedPercent(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	used := float64(st.Blocks-st.Bfree) * float64(st.Bsize)
	avail := float64(st.Bavail) * float64(st.Bsize)
	if used+avail == 0 {
		return 0, nil
	}
	return 100 * used / (used + avail), nil
}
