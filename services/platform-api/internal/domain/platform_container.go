package domain

// PlatformContainer is one application container (platform-run-*) as Docker
// labels it: which deployment started it.
type PlatformContainer struct {
	ID           string
	Name         string
	DeploymentID string
}
