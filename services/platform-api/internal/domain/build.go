package domain

import (
	"errors"
	"time"
)

type BuildStatus string

const (
	BuildQueued     BuildStatus = "queued"
	BuildInProgress BuildStatus = "in_progress"
	BuildSucceeded  BuildStatus = "succeeded"
	BuildFailed     BuildStatus = "failed"
)

// ErrorCategory implements FR-038's requirement to distinguish a
// source-code problem (the employee/agent can fix and resubmit) from a
// platform/infrastructure problem (not the employee's fault to debug).
type ErrorCategory string

const (
	ErrorCategorySource   ErrorCategory = "source"
	ErrorCategoryPlatform ErrorCategory = "platform"
)

type Build struct {
	ID            string
	ApplicationID string
	TriggeredBy   string
	Status        BuildStatus
	ErrorCategory *ErrorCategory
	ErrorDetail   *string
	ImageRefs     map[string]string // service name -> built image reference
	StartedAt     time.Time
	CompletedAt   *time.Time
}

type BaseImage struct {
	ID             string
	Runtime        string
	ImageReference string
	Status         string
}

var (
	ErrNotValidated          = errors.New("application must be Validated, Running, or Failed to build")
	ErrBuildAlreadyInFlight  = errors.New("a build is already queued or in progress for this application")
	ErrNoSourceArchive       = errors.New("a source archive (tar.gz) is required")
	ErrNoBaseImageForRuntime = errors.New("no governed base image is published for this runtime")
)

// BuildFailure is returned by a BuildEngine implementation to carry FR-038's
// category distinction back to the service layer, instead of an opaque
// error the caller would have to guess about.
type BuildFailure struct {
	Category ErrorCategory
	Service  string // which declared service failed, if known
	Detail   string
}

func (e *BuildFailure) Error() string {
	if e.Service != "" {
		return "build failed for service " + e.Service + ": " + e.Detail
	}
	return "build failed: " + e.Detail
}
