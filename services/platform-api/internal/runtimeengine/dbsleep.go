package runtimeengine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// A database is sized for one small internal application, and sleeps when
// its application does (the platform owner, 2026-10-24: the LAN host has
// 3.3 GB, and MySQL with its defaults held ~350-460 MB each, idle or not).

// mysqlConfigPath is read by the official image (/etc/my.cnf includes
// /etc/mysql/conf.d/). Measured with mysql:8.4: an idle server took 430 MB
// with the defaults and 138 MB with this.
const mysqlConfigPath = "/etc/mysql/conf.d/platform.cnf"

const mysqlConfig = `# Written by the platform: sized for one small internal application.
[mysqld]
performance_schema = OFF
innodb_buffer_pool_size = 64M
innodb_log_buffer_size = 4M
max_connections = 40
table_open_cache = 256
table_definition_cache = 400
thread_cache_size = 4
tmp_table_size = 16M
max_heap_table_size = 16M
key_buffer_size = 8M
`

// writeMySQLConfig puts the platform's configuration into a database
// container — running or not; a running server reads it when it next
// starts. It lives in the container's own layer, so it stays across stops
// and starts.
func (r *DockerRuntime) writeMySQLConfig(ctx context.Context, containerID string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "platform.cnf", Mode: 0o644, Size: int64(len(mysqlConfig))}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(mysqlConfig)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := r.cli.CopyToContainer(ctx, containerID, "/etc/mysql/conf.d", &buf, types.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("write the database configuration: %w", err)
	}
	return nil
}

// TuneDatabase gives an existing MySQL container the platform's
// configuration if it doesn't have it yet, restarting a running one so it
// takes effect (seconds; its data is in its volume and stays). Reports
// whether anything changed. Other engines are left as they are.
func (r *DockerRuntime) TuneDatabase(ctx context.Context, containerID, engine string) (bool, error) {
	if engine != "mysql" {
		return false, nil
	}
	if _, err := r.cli.ContainerStatPath(ctx, containerID, mysqlConfigPath); err == nil {
		return false, nil
	} else if !client.IsErrNotFound(err) {
		return false, fmt.Errorf("look for the database configuration: %w", err)
	}
	if err := r.writeMySQLConfig(ctx, containerID); err != nil {
		return false, err
	}
	running, err := r.DatabaseRunning(ctx, containerID)
	if err != nil || !running {
		return true, err
	}
	timeout := 30
	if err := r.cli.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return true, fmt.Errorf("restart the database with its configuration: %w", err)
	}
	return true, nil
}

// DatabaseRunning says whether a database container is up.
func (r *DockerRuntime) DatabaseRunning(ctx context.Context, containerID string) (bool, error) {
	inspected, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("inspect the database container: %w", err)
	}
	return inspected.State != nil && inspected.State.Running, nil
}

// SleepDatabase stops a database container WITHOUT removing it: its data
// is in the container's volume, which goes only with the container
// (Stop removes both, for deletion). Docker's unless-stopped policy keeps
// a stopped one stopped across a host reboot, as it should be.
func (r *DockerRuntime) SleepDatabase(ctx context.Context, containerID string) error {
	timeout := 30
	if err := r.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop the database: %w", err)
	}
	return nil
}

// WakeDatabase starts a sleeping database container again.
func (r *DockerRuntime) WakeDatabase(ctx context.Context, containerID string) error {
	if err := r.cli.ContainerStart(ctx, containerID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start the database: %w", err)
	}
	return nil
}

// RunningServices is the services of an application with a container
// running (not its database).
func (r *DockerRuntime) RunningServices(ctx context.Context, applicationID string) ([]string, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", labelApplication+"="+applicationID), filters.Arg("status", "running")),
	})
	if err != nil {
		return nil, fmt.Errorf("list the application's containers: %w", err)
	}
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Labels[labelService])
	}
	return out, nil
}
