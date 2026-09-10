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
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
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
//
// spec.Env and spec.NetworkID carry Module N's database wiring: the
// generated connection string as an environment variable, and the
// application's own private Docker network so it can reach its database
// (see StartDatabase). Both are empty for an application that declares no
// database, in which case this behaves exactly as it did before Module N.
func (r *DockerRuntime) StartContainer(ctx context.Context, spec domain.ContainerSpec) (domain.RunningContainer, error) {
	portKey := nat.Port(fmt.Sprintf("%d/tcp", spec.ContainerPort))
	cfg := &container.Config{
		Image:        spec.ImageRef,
		Env:          spec.Env,
		ExposedPorts: nat.PortSet{portKey: struct{}{}},
	}
	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			portKey: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}

	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, spec.Name)
	if err != nil {
		return domain.RunningContainer{}, fmt.Errorf("create container: %w", err)
	}
	// Attached as an ADDITIONAL network, not a replacement: the container
	// keeps its default-bridge connectivity, which is what publishes its
	// host port and lets the platform health-check it.
	if spec.NetworkID != "" {
		if err := r.cli.NetworkConnect(ctx, spec.NetworkID, created.ID, nil); err != nil {
			return domain.RunningContainer{}, fmt.Errorf("attach container to its application network: %w", err)
		}
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

// --- Module N (Database Management) ---------------------------------------

// CreateNetwork implements FR-062's isolation mechanism: one private
// Docker network per application. The application's own containers join it
// (StartContainer's spec.NetworkID) and so does its database
// (StartDatabase) — nothing else can, so another application cannot reach
// or even resolve it. That enforcement is at the network layer, not in
// application code, exactly as FR-062 requires ("even a misconfigured or
// malicious application cannot reach another's database").
//
// Idempotent: an existing network with this name is reused rather than
// duplicated, so a redeploy of an application that already has one is a
// no-op instead of an error.
func (r *DockerRuntime) CreateNetwork(ctx context.Context, name string) (string, error) {
	existing, err := r.cli.NetworkList(ctx, types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("name", name)),
	})
	if err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	for _, n := range existing {
		if n.Name == name {
			return n.ID, nil
		}
	}

	created, err := r.cli.NetworkCreate(ctx, name, types.NetworkCreate{Driver: "bridge"})
	if err != nil {
		return "", fmt.Errorf("create application network: %w", err)
	}
	return created.ID, nil
}

// RemoveNetwork is FR-065's counterpart to CreateNetwork. An already-gone
// network is not an error — deprovisioning has to be safely repeatable.
func (r *DockerRuntime) RemoveNetwork(ctx context.Context, networkID string) error {
	if networkID == "" {
		return nil
	}
	if err := r.cli.NetworkRemove(ctx, networkID); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove application network: %w", err)
	}
	return nil
}

// StartDatabase implements FR-061: a real, dedicated PostgreSQL container
// for one application.
//
// It deliberately publishes NO host port. The only way to reach it is from
// inside spec.NetworkID — which is FR-061's business rule that databases
// are "never directly reachable by the employee/agent for raw admin
// commands", enforced rather than asked for politely. The application
// reaches it by container name, which Docker resolves within that network.
//
// The private network is attached at CREATE time, via NetworkMode +
// EndpointsConfig, and that detail is the whole of FR-062. Creating the
// container first and connecting the network afterwards — which is what
// StartContainer does, for the reason documented there — would leave the
// database on Docker's default bridge as well, where every other
// application's container also sits. The first end-to-end run of
// scripts/verify_module_n.py caught exactly that: the database was on
// ["bridge", "platform-net-..."], so the isolation this function's own
// comment claimed was not real.
func (r *DockerRuntime) StartDatabase(ctx context.Context, spec domain.DatabaseSpec) (string, error) {
	cfg := &container.Config{
		Image: spec.ImageRef,
		Env: []string{
			"POSTGRES_DB=" + spec.DatabaseName,
			"POSTGRES_USER=" + spec.Username,
			"POSTGRES_PASSWORD=" + spec.Password,
		},
	}
	hostCfg := &container.HostConfig{NetworkMode: container.NetworkMode(spec.NetworkID)}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			spec.NetworkID: {NetworkID: spec.NetworkID},
		},
	}

	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return "", fmt.Errorf("create database container: %w", err)
	}
	if err := r.cli.ContainerStart(ctx, created.ID, types.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("start database container: %w", err)
	}

	// Verified rather than assumed: if this container is ever on more than
	// its own network, FR-062 is not being enforced, and the platform must
	// say so loudly instead of provisioning a database that quietly isn't
	// isolated.
	inspected, err := r.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return "", fmt.Errorf("inspect database container: %w", err)
	}
	if len(inspected.NetworkSettings.Networks) != 1 {
		attached := make([]string, 0, len(inspected.NetworkSettings.Networks))
		for name := range inspected.NetworkSettings.Networks {
			attached = append(attached, name)
		}
		_ = r.Stop(ctx, created.ID)
		return "", fmt.Errorf("database container is not isolated: attached to %v, expected only its application network", attached)
	}

	return created.ID, nil
}
