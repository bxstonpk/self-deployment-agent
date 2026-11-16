package domain

import (
	"errors"
	"time"
)

// IT handoff packages (DEC-033): an application promoted to production on
// this development platform, packed as one .tar for IT to run for real.

type HandoffStatus string

const (
	HandoffBuilding HandoffStatus = "building"
	HandoffReady    HandoffStatus = "ready"
	HandoffFailed   HandoffStatus = "failed"
	// HandoffReplaced: a newer package of the application is ready; this
	// one's file is gone.
	HandoffReplaced HandoffStatus = "replaced"
)

type HandoffTrigger string

const (
	HandoffOnPromotion HandoffTrigger = "promotion"
	HandoffManual      HandoffTrigger = "manual"
)

type HandoffPackage struct {
	ID            string
	ApplicationID string
	DeploymentID  string
	Trigger       HandoffTrigger
	RequestedBy   string // "" for a promotion's
	Status        HandoffStatus
	Error         string
	FileName      string
	SizeBytes     int64
	SHA256        string
	CreatedAt     time.Time
	CompletedAt   *time.Time
}

const (
	AuditActionHandoffBuild    AuditAction = "handoff.build"
	AuditActionHandoffDownload AuditAction = "handoff.download"
)

var (
	ErrHandoffNotFound      = errors.New("handoff package not found")
	ErrHandoffNotReady      = errors.New("the handoff package is not ready")
	ErrHandoffInProgress    = errors.New("a handoff package for this application is already being made")
	ErrHandoffNotProduction = errors.New("the application has no production deployment running to hand off")
)
