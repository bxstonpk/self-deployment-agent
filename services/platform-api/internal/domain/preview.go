package domain

import (
	"errors"
	"time"
)

// Previews (DEC-029 part 3, migration 0026): a short-lived copy of an
// application to try a change on before deploying it. It is its own
// application, "<name>-preview-<4 hex>", linked to the one it previews.

const EnvironmentPreview Environment = "preview"

type Preview struct {
	ApplicationID string // the preview's own application
	ParentID      string // the application it previews
	CreatedBy     string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	EndedAt       *time.Time

	// Filled by reads, for whoever shows it.
	Name       string
	ParentName string
}

const (
	AuditActionPreviewStart AuditAction = "application.preview_start"
	AuditActionPreviewEnd   AuditAction = "application.preview_end"

	// MaxPreviewParentName leaves room for "-preview-xxxx" in a 63-character
	// application name.
	MaxPreviewParentName = 50
)

var (
	ErrNoPreview              = errors.New("this application has no preview running")
	ErrPreviewOfPreview       = errors.New("a preview can't have a preview of its own; preview the application it was made from")
	ErrPreviewOnlyEnvironment = errors.New(`a preview deploys only to the "preview" environment, and only a preview does`)
	ErrPreviewNameTooLong     = errors.New("the application's name is too long for a preview address (50 characters at most)")
	ErrPreviewParentInactive  = errors.New("an archived or deleted application can't have a preview")
)
