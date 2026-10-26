package domain

import (
	"errors"
	"time"
)

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

	// Hold is set while an administrator's suspension is on.
	Hold *AdminHold
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

// AdminHold is an administrator's suspension of an application: while it
// is on, only an administrator can resume the application.
type AdminHold struct {
	ApplicationID string
	Reason        string
	HeldBy        string // the administrator's user id
	HeldByEmail   string
	HeldAt        time.Time
}

var (
	// ErrAdminHold: the application was suspended by an administrator, so
	// its owners can't resume, archive or delete it.
	ErrAdminHold = errors.New("a platform administrator suspended this application; only an administrator can resume it")
	// ErrNotAdminHeld: an administrator's resume of an application that no
	// administrator suspended.
	ErrNotAdminHeld = errors.New("this application was not suspended by an administrator")
	// ErrHoldReasonRequired: an administrator's suspension must say why.
	ErrHoldReasonRequired = errors.New("a reason is required: the owners are told it")
)
