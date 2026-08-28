// Package runtimeengine is a stand-in Runtime Platform / Deployment
// Controller (MOD-06) implementation using the Docker daemon directly.
// Production runs on self-hosted K3s + Knative (DEC-004,
// docs/17_Decision_Log.md) — this package exists so the Deploying state's
// pipeline (deploy, health-check, traffic-activate) is real and testable
// today without that infrastructure being set up yet. The shape it returns
// (container id, host port, reachable URL) is deliberately what a
// Kubernetes-backed implementation would also need to report, per NFR-046
// ("replaceable container-platform implementation") — callers in
// service.DeploymentService don't know or care which one is behind the
// RuntimeEngine interface.
package runtimeengine

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"platform-api/internal/domain"
)

type DockerRuntime struct {
	cli *client.Client
}

func NewDockerRuntime(cli *client.Client) *DockerRuntime {
	return &DockerRuntime{cli: cli}
}

// StartContainer implements the Deployment pipeline step: starts one
// container for a built image, publishing a host port Docker picks freely
// (host port 0) so multiple services/deployments never collide.
func (r *DockerRuntime) StartContainer(ctx context.Context, name, imageRef string, containerPort int) (domain.RunningContainer, error) {
	portKey := nat.Port(fmt.Sprintf("%d/tcp", containerPort))
	cfg := &container.Config{
		Image:        imageRef,
		ExposedPorts: nat.PortSet{portKey: struct{}{}},
	}
	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			portKey: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}

	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return domain.RunningContainer{}, fmt.Errorf("create container: %w", err)
	}
	if err := r.cli.ContainerStart(ctx, created.ID, types.ContainerStartOptions{}); err != nil {
		return domain.RunningContainer{}, fmt.Errorf("start container: %w", err)
	}

	inspected, err := r.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return domain.RunningContainer{}, fmt.Errorf("inspect container: %w", err)
	}
	bindings, ok := inspected.NetworkSettings.Ports[portKey]
	if !ok || len(bindings) == 0 {
		return domain.RunningContainer{}, fmt.Errorf("container started but published no host port for %s", portKey)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return domain.RunningContainer{}, fmt.Errorf("parse published host port: %w", err)
	}

	return domain.RunningContainer{
		ContainerID: created.ID,
		HostPort:    hostPort,
		URL:         fmt.Sprintf("http://localhost:%d", hostPort),
	}, nil
}

// HealthCheck implements the Health Check pipeline gate by polling the
// container's URL until it responds or the timeout elapses. This is a
// simplified stand-in for Module R (Health Check Manager, not yet
// implemented as its own module) — a real implementation would use a
// configurable health path; this polls "/".
func (r *DockerRuntime) HealthCheck(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 2 * time.Second}

	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("build health check request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("health check returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("health check did not succeed within %s: %w", timeout, lastErr)
}

// Stop tears down a container — used when a successful redeploy supersedes
// a previously Running deployment (see service.DeploymentService).
func (r *DockerRuntime) Stop(ctx context.Context, containerID string) error {
	timeout := 5
	if err := r.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	if err := r.cli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}
