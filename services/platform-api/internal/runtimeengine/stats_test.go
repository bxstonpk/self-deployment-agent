package runtimeengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
)

// One core fully used over an interval in which the host had 4 cores'
// worth of time available: 100%, not 25% — the same number `docker stats`
// prints, so a 4-core host doesn't quietly report a saturated container as
// a quarter busy.
func TestCPUPercent_OneCoreOfFour(t *testing.T) {
	s := types.StatsJSON{}
	s.CPUStats.CPUUsage.TotalUsage = 1_000_000_000 // 1s of CPU time
	s.CPUStats.SystemUsage = 4_000_000_000         // over 1s on 4 cores
	s.CPUStats.OnlineCPUs = 4
	s.PreCPUStats.CPUUsage.TotalUsage = 0
	s.PreCPUStats.SystemUsage = 0

	if got := cpuPercent(s); got < 99.9 || got > 100.1 {
		t.Fatalf("got %.2f%%, want ~100%%", got)
	}
}

func TestCPUPercent_FallsBackToPerCPUCount(t *testing.T) {
	s := types.StatsJSON{}
	s.CPUStats.CPUUsage.TotalUsage = 500_000_000
	s.CPUStats.SystemUsage = 2_000_000_000
	s.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 2} // older daemons report no OnlineCPUs

	if got := cpuPercent(s); got < 49.9 || got > 50.1 {
		t.Fatalf("got %.2f%%, want ~50%%", got)
	}
}

// A first reading has nothing to compare against. Zero is what the caller
// stores, and it must not come out as a wild number instead.
func TestCPUPercent_WithoutAPreviousReading(t *testing.T) {
	s := types.StatsJSON{}
	s.CPUStats.CPUUsage.TotalUsage = 1_000_000_000
	s.CPUStats.OnlineCPUs = 2

	if got := cpuPercent(s); got != 0 {
		t.Fatalf("got %.2f%%, want 0 when there is no previous reading", got)
	}
}

// The kernel counts page cache it would drop under pressure as "usage";
// `docker stats` subtracts it, and so must this, or every application
// looks like it is leaking.
func TestMemoryUsed_SubtractsReclaimablePageCache(t *testing.T) {
	s := types.StatsJSON{}
	s.MemoryStats.Usage = 200 * 1024 * 1024
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 150 * 1024 * 1024}

	if got := memoryUsed(s); got != 50*1024*1024 {
		t.Fatalf("got %d bytes, want %d", got, 50*1024*1024)
	}
}

func TestMemoryUsed_CgroupV1Cache(t *testing.T) {
	s := types.StatsJSON{}
	s.MemoryStats.Usage = 100
	s.MemoryStats.Stats = map[string]uint64{"cache": 40}

	if got := memoryUsed(s); got != 60 {
		t.Fatalf("got %d, want 60", got)
	}
}

func TestMemoryUsed_NeverNegative(t *testing.T) {
	s := types.StatsJSON{}
	s.MemoryStats.Usage = 10
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 40}

	if got := memoryUsed(s); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

// A container that stops between being listed and being read closes its
// stats stream without a reading. That is a gap, not a fault, and it
// happens on every restart, scale-down and delete — so it must not look
// like one.
func TestDecodeUsage_AClosedStreamIsAGapNotAnError(t *testing.T) {
	_, err := decodeUsage(strings.NewReader(""))
	if !errors.Is(err, errStatsUnavailable) {
		t.Fatalf("got %v, want errStatsUnavailable", err)
	}
}

func TestDecodeUsage_MalformedStatsAreStillAnError(t *testing.T) {
	_, err := decodeUsage(strings.NewReader("{not json"))
	if err == nil || errors.Is(err, errStatsUnavailable) {
		t.Fatalf("got %v, want a real decode error", err)
	}
}

func TestDecodeUsage_ReadsTheNumbers(t *testing.T) {
	usage, err := decodeUsage(strings.NewReader(`{"cpu_stats":{"cpu_usage":{"total_usage":2000000000},"system_cpu_usage":4000000000,"online_cpus":2},
		"precpu_stats":{"cpu_usage":{"total_usage":1000000000},"system_cpu_usage":2000000000,"online_cpus":2},
		"memory_stats":{"usage":1048576,"limit":8388608,"stats":{"inactive_file":48576}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if usage.CPUPercent < 99.9 || usage.CPUPercent > 100.1 || usage.MemoryBytes != 1000000 || usage.MemoryLimit != 8388608 {
		t.Fatalf("unexpected reading %+v", usage)
	}
}
