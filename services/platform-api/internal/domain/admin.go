package domain

import "time"

// AdminApplication is one row of the administrators' dashboard: an
// application, who owns it, and what it is using right now.
type AdminApplication struct {
	ID           string
	Name         string
	Status       string
	Department   string
	OwnerEmail   string
	Environments string // the environments it's running in, e.g. "dev, production"
	Instances    int    // containers serving it right now

	CPUPercent   float64 // summed over its containers; 100 = one core
	MemoryBytes  int64
	Requests     int64 // over the dashboard's traffic window
	Errors       int64
	LatencyMsAvg float64
}

// AdminUsage is an application's latest resource reading, summed over the
// containers serving it now.
type AdminUsage struct {
	CPUPercent  float64
	MemoryBytes int64
	Containers  int
}

// AdminTraffic is an application's traffic over a window.
type AdminTraffic struct {
	Requests       int64
	Errors         int64
	LatencyMsTotal float64
}

// TrafficPoint is one minute of traffic across every application.
type TrafficPoint struct {
	Minute   time.Time
	Requests int64
	Errors   int64
}
