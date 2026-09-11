package domain

import (
	"errors"
	"time"
)

// ServiceRuntimeState is the live, mutable scale state of one service
// within a Running deployment — separate from Deployment.Containers (a
// point-in-time snapshot taken at activation) because scale-to-zero makes
// this genuinely dynamic over the deployment's lifetime.
type ServiceRuntimeState struct {
	DeploymentID  string
	ServiceName   string
	ImageRef      string
	ContainerPort int
	Eligible      bool
	ContainerID   *string // nil => currently scaled to zero
	HostPort      *int
	LastActiveAt  time.Time
	UpdatedAt     time.Time
}

func (s ServiceRuntimeState) ScaledToZero() bool {
	return s.ContainerID == nil
}

type ScaleDirection string

const (
	ScaledUp     ScaleDirection = "scaled_up"
	ScaledToZero ScaleDirection = "scaled_to_zero"
)

const (
	TriggerColdStart         = "cold_start"
	TriggerIdleTimeout       = "idle_timeout"
	TriggerInitialActivation = "initial_activation"
)

type ScaleEvent struct {
	ID            string
	DeploymentID  string
	ServiceName   string
	Direction     ScaleDirection
	TriggerReason string
	OccurredAt    time.Time
}

// ResolvedService is what the proxy learns while routing one request:
// where it goes, and whose it is — Module T counts the request against
// that application, service and environment.
type ResolvedService struct {
	ApplicationID string
	DeploymentID  string
	Environment   Environment
	ServiceName   string
	HostPort      int
}

var (
	ErrApplicationNotFound  = errors.New("application not found")
	ErrNoRunningDeployment  = errors.New("application has no running deployment")
	ErrServiceStateNotFound = errors.New("no runtime state for this service")
)
