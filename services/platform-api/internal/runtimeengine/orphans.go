package runtimeengine

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"platform-api/internal/domain"
)

// ListPlatformContainers lists every application container the platform
// started (platform-run-*, running or not) with the deployment it belongs
// to, for the orphan sweep (ScaleService.SweepOrphans). Databases
// (platform-db-*) and build or test containers are not among them.
func (r *DockerRuntime) ListPlatformContainers(ctx context.Context) ([]domain.PlatformContainer, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelDeployment)),
	})
	if err != nil {
		return nil, fmt.Errorf("list the application containers: %w", err)
	}
	out := make([]domain.PlatformContainer, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if !strings.HasPrefix(name, "platform-run-") {
			continue
		}
		out = append(out, domain.PlatformContainer{ID: c.ID, Name: name, DeploymentID: c.Labels[labelDeployment]})
	}
	return out, nil
}
