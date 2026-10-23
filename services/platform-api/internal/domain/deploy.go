package domain

import (
	"context"
	"errors"
	"time"
)

type DeploymentStatus string

const (
	DeploymentScanning        DeploymentStatus = "scanning"
	DeploymentPendingApproval DeploymentStatus = "pending_approval"
	DeploymentDeploying       DeploymentStatus = "deploying"
	DeploymentHealthCheck     DeploymentStatus = "health_check"
	DeploymentRunning         DeploymentStatus = "running"
	DeploymentFailed          DeploymentStatus = "failed"
	DeploymentRejected        DeploymentStatus = "rejected"
	DeploymentSuperseded      DeploymentStatus = "superseded"
	DeploymentSuspended       DeploymentStatus = "suspended"
	DeploymentArchived        DeploymentStatus = "archived"
)

// DefaultContainerPort is the port a service listens on when
// deployment.yaml doesn't declare one (frontend-kind services aren't
// required to per Module H's validation). Shared between
// internal/buildengine (which generates a Dockerfile EXPOSEing it) and
// internal/service's deploy orchestration (which must publish the SAME
// port) so the two can't drift apart.
const DefaultContainerPort = 3000

type Environment string

const (
	EnvironmentDev        Environment = "dev"
	EnvironmentProduction Environment = "production"
)

type RunningContainer struct {
	ContainerID string `json:"container_id"`
	HostPort    int    `json:"host_port"`
	URL         string `json:"url"`
}

type ScanFinding struct {
	Severity        string `json:"severity"`
	VulnerabilityID string `json:"vulnerability_id"`
	Package         string `json:"package"`
	Title           string `json:"title"`
}

// ScanReport implements FR-041's scan result. Passed is false whenever
// CriticalCount > 0 — the platform's blocking-severity policy (see
// internal/imagescan's package doc for why CRITICAL-only, for now).
type ScanReport struct {
	ImageRef      string        `json:"image_ref"`
	Passed        bool          `json:"passed"`
	CriticalCount int           `json:"critical_count"`
	HighCount     int           `json:"high_count"`
	TopFindings   []ScanFinding `json:"top_findings,omitempty"`
}

type Deployment struct {
	ID                string
	ApplicationID     string
	BuildID           string
	Environment       Environment
	RequestedBy       string
	Status            DeploymentStatus
	ScanPassed        *bool
	ScanCriticalCount *int
	ScanHighCount     *int
	ScanReports       map[string]ScanReport // service name -> report
	RejectionReason   *string
	FailureReason     *string
	Containers        map[string]RunningContainer // service name -> running container
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CompletedAt       *time.Time
}

type ApprovalDecision string

const (
	ApprovalPending  ApprovalDecision = "pending"
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalRejected ApprovalDecision = "rejected"
)

type DeploymentApproval struct {
	ID           string
	DeploymentID string
	RequestedBy  string
	DecidedBy    *string
	Decision     ApprovalDecision
	Reason       *string
	CreatedAt    time.Time
	DecidedAt    *time.Time
	// TestReport is what the requester attached for the approver; see
	// approval_signing.go. SignerFingerprint and SignedAt record a signed
	// decision.
	TestReport        string
	SignerFingerprint *string
	SignedAt          *time.Time
}

type testReportKey struct{}

// WithTestReport carries a deployment request's test report down to where
// its approval record is created, so the record is complete the moment it
// exists — an approver notified of it never reads it half-written.
func WithTestReport(ctx context.Context, report string) context.Context {
	return context.WithValue(ctx, testReportKey{}, report)
}

func TestReportFrom(ctx context.Context) string {
	s, _ := ctx.Value(testReportKey{}).(string)
	return s
}

var (
	ErrNoSuccessfulBuild            = errors.New("application has no successful build to deploy")
	ErrDeploymentAlreadyInFlight    = errors.New("a deployment is already in progress for this application")
	ErrDeploymentNotPendingApproval = errors.New("deployment is not awaiting approval")
	ErrInvalidEnvironment           = errors.New(`environment must be "dev" or "production"`)
	ErrApplicationNotRunning        = errors.New("application must be in the Running state for this operation")
	ErrApplicationNotSuspended      = errors.New("application must be in the Suspended state to resume")
	ErrInvalidRollbackTarget        = errors.New("rollback target is not a valid, retained, previously-successful deployment for this application")
	ErrDeleteNotConfirmed           = errors.New(`deletion is irreversible and requires explicit confirmation ("confirm": true)`)
)
