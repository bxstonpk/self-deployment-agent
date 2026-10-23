// Alert implements FR-092 (alerting) with the thresholds and notification
// rhythm the platform owner chose on 2026-10-23 (DEC-028,
// docs/17_Decision_Log.md):
//
//   - an alert is announced once when it starts, once more if it gets
//     worse (warning -> critical), and once when it clears;
//   - while it lasts, a reminder at most once an hour;
//   - thresholds are set B (see AlertThresholds' defaults), evaluated over
//     the platform's own measurements (Module T) and the host's disk.
//
// Delivery is the in-app notification inbox (Module X): no external
// channel has been chosen (DEC-018).
package domain

import "time"

type AlertLevel string

const (
	AlertWarning  AlertLevel = "warning"
	AlertCritical AlertLevel = "critical"
)

// Rank orders levels: "" (fine) < warning < critical.
func (l AlertLevel) Rank() int {
	switch l {
	case AlertWarning:
		return 1
	case AlertCritical:
		return 2
	}
	return 0
}

type AlertRule string

const (
	AlertErrorRate AlertRule = "error_rate"
	AlertLatency   AlertRule = "latency"
	AlertCPU       AlertRule = "cpu"
	AlertMemory    AlertRule = "memory"
	AlertDisk      AlertRule = "host_disk"
)

// AlertState is one alert that is currently firing. Its Key identifies
// what it is about: an application's service and a rule, or the platform
// itself for host-level rules.
type AlertState struct {
	Key            string
	ApplicationID  string // empty for a platform-level alert
	Service        string
	Rule           AlertRule
	Level          AlertLevel
	Value          float64
	Message        string
	StartedAt      time.Time
	LastNotifiedAt time.Time
}

type AlertEventKind string

const (
	AlertFired     AlertEventKind = "fired"
	AlertEscalated AlertEventKind = "escalated"
	AlertReminded  AlertEventKind = "reminded"
	AlertResolved  AlertEventKind = "resolved"
)

// AlertEvent is the history: every notification an alert produced.
type AlertEvent struct {
	ID            string
	Key           string
	ApplicationID string
	Service       string
	Rule          AlertRule
	Level         AlertLevel // the level at the time; empty for resolved
	Kind          AlertEventKind
	Value         float64
	Message       string
	At            time.Time
}

// AlertThresholds are DEC-028's numbers. A warning and a critical level
// per rule, the window each is judged over, and the minimum traffic below
// which a rate isn't judged at all (two failures out of three requests is
// not an outage).
type AlertThresholds struct {
	TrafficWindow      time.Duration
	MinRequestsForRate int64
	ErrorRateWarning   float64 // fraction, e.g. 0.05
	ErrorRateCritical  float64
	MinRequestsLatency int64
	LatencyWarningMs   float64
	LatencyCriticalMs  float64
	CPUWindow          time.Duration
	CPUWarning         float64 // percent, sustained over CPUWindow
	CPUCritical        float64
	MemoryWindow       time.Duration
	MemoryWarning      float64 // percent of the container's memory limit (the host's RAM when unlimited)
	MemoryCritical     float64
	DiskWarning        float64 // percent used of the filesystem holding backups and logs
	DiskCritical       float64
	ReminderInterval   time.Duration
}

// DefaultAlertThresholds is set B ("balanced"), the one chosen.
func DefaultAlertThresholds() AlertThresholds {
	return AlertThresholds{
		TrafficWindow:      5 * time.Minute,
		MinRequestsForRate: 20,
		ErrorRateWarning:   0.05,
		ErrorRateCritical:  0.20,
		MinRequestsLatency: 5,
		LatencyWarningMs:   1000,
		LatencyCriticalMs:  3000,
		CPUWindow:          10 * time.Minute,
		CPUWarning:         85,
		CPUCritical:        95,
		MemoryWindow:       5 * time.Minute,
		MemoryWarning:      80,
		MemoryCritical:     95,
		DiskWarning:        85,
		DiskCritical:       95,
		ReminderInterval:   time.Hour,
	}
}

// TrafficWindowStats is one service's traffic over the window.
type TrafficWindowStats struct {
	ApplicationID string
	Service       string
	Requests      int64
	Errors        int64
	LatencyMsSum  float64
}

// ResourceWindowStats is one service's resource use over a window.
type ResourceWindowStats struct {
	ApplicationID string
	Service       string
	Samples       int
	// MinCPUPercent is the lowest reading in the window: CPU counts as
	// "sustained" only if even the lowest sample was over the line.
	MinCPUPercent float64
	// AvgMemoryPercent is memory used as a share of the limit.
	AvgMemoryPercent float64
	// Span is the time between the window's first and last sample — a
	// sustained rule needs readings covering most of its window.
	Span time.Duration
}
