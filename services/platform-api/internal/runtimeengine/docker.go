// Package runtimeengine is the Runtime Platform / Deployment Controller
// (MOD-06) implementation, using the Docker daemon directly. This is not a
// stand-in for a future Kubernetes migration: per DEC-004's 2026-09-11
// revision (docs/17_Decision_Log.md), production runs on Docker + Docker
// Compose on a single existing on-prem host — this package IS the
// production implementation, not a placeholder for one. It's still built
// behind the RuntimeEngine interface (NFR-046, "replaceable
// container-platform implementation") so it could be swapped later if the
// deployment context ever changes; service.DeploymentService doesn't know
// or care which implementation is behind it.
package runtimeengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
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

	// Module S collection (logs.go); nil until EnableLogCollection.
	logs      LogSink
	// logRetention bounds how far back a resumed collection reaches, so a
	// container silent for longer than the retention period doesn't have
	// already-purged output collected again. Zero: no bound.
	logRetention time.Duration
	followMu  sync.Mutex
	followers map[string]*follower
}

func NewDockerRuntime(cli *client.Client) *DockerRuntime {
	return &DockerRuntime{cli: cli, followers: map[string]*follower{}}
}

// EnableLogCollection turns on Module S: from here on, every application
// container this runtime starts is followed from its first line, and Stop
// waits for a container's last lines before removing it.
// SetLogRetention tells collection how long logs are kept (DEC-027).
func (r *DockerRuntime) SetLogRetention(d time.Duration) { r.logRetention = d }

func (r *DockerRuntime) EnableLogCollection(sink LogSink) {
	r.logs = sink
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
//
// PORT is always set to the port the platform will route to — the
// service's declared port, or the platform default — so an application
// can listen on whatever it's told instead of hard-coding a number that
// has to match deployment.yaml. Appended last, so it wins over a secret
// of the same name: the platform only ever routes to this one.
func (r *DockerRuntime) StartContainer(ctx context.Context, spec domain.ContainerSpec) (domain.RunningContainer, error) {
	portKey := nat.Port(fmt.Sprintf("%d/tcp", spec.ContainerPort))
	env := append(append([]string{}, spec.Env...), fmt.Sprintf("PORT=%d", spec.ContainerPort))
	cfg := &container.Config{
		Image:        spec.ImageRef,
		Env:          env,
		ExposedPorts: nat.PortSet{portKey: struct{}{}},
	}
	if spec.Log.Collect() {
		cfg.Labels = containerLabels(spec.Log, spec.Env)
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
	// From here on, a failure must not leave the container behind. The
	// usual one is a container that exits before its port is published —
	// it crashed on start — which used to stay there, exited, for good.
	fail := func(err error) (domain.RunningContainer, error) {
		r.discard(created.ID)
		return domain.RunningContainer{}, err
	}
	// Attached as an ADDITIONAL network, not a replacement: the container
	// keeps its default-bridge connectivity, which is what publishes its
	// host port and lets the platform health-check it.
	if spec.NetworkID != "" {
		if err := r.cli.NetworkConnect(ctx, spec.NetworkID, created.ID, nil); err != nil {
			return fail(fmt.Errorf("attach container to its application network: %w", err))
		}
	}
	if err := r.cli.ContainerStart(ctx, created.ID, types.ContainerStartOptions{}); err != nil {
		return fail(fmt.Errorf("start container: %w", err))
	}
	// Module S: followed from its very first line, before the health check
	// — so a container that crashes on start still leaves its output.
	if r.logs != nil && spec.Log.Collect() {
		r.follow(created.ID, spec.Log, redactionsFor(spec.Env, secretEnvKeys(spec.Env)), time.Time{})
	}

	inspected, err := r.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return fail(fmt.Errorf("inspect container: %w", err))
	}
	if inspected.State != nil && !inspected.State.Running {
		return fail(fmt.Errorf("container exited as it started (exit code %d); what it printed is in the application's logs", inspected.State.ExitCode))
	}
	bindings, ok := inspected.NetworkSettings.Ports[portKey]
	if !ok || len(bindings) == 0 {
		return fail(fmt.Errorf("container started but published no host port for %s", portKey))
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return fail(fmt.Errorf("parse published host port: %w", err))
	}

	return domain.RunningContainer{
		ContainerID: created.ID,
		HostPort:    hostPort,
		URL:         fmt.Sprintf("http://localhost:%d", hostPort),
	}, nil
}

// discard gets rid of a container StartContainer created but can't hand
// back, the way Stop does: stopped, its last lines stored, removed. On a
// fresh context, because the request's may be what just failed.
func (r *DockerRuntime) discard(containerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Stop(ctx, containerID); err != nil {
		log.Printf("runtime: could not remove container %s after a failed start: %v", shortContainerID(containerID), err)
	}
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
	// Module S: the log stream ends when the container does. Wait for its
	// last lines to be stored before the container — and with it Docker's
	// own copy of its output — is gone.
	r.waitForLogs(containerID)
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

// ensureImage pulls ref if the daemon doesn't already have it. Found by
// the first real MySQL deploy, not assumed: ContainerCreate does not pull,
// so provisioning only ever worked for an engine whose image happened to
// be on the host already — Postgres, because the platform's own database
// runs on the same image.
func (r *DockerRuntime) ensureImage(ctx context.Context, ref string) error {
	if _, _, err := r.cli.ImageInspectWithRaw(ctx, ref); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return fmt.Errorf("inspect image %s: %w", ref, err)
	}
	rc, err := r.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer rc.Close()
	// The pull runs as long as its progress stream is being read.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	return nil
}

// databaseEnv is how each engine's official image is told which database,
// account and password to create on first start.
//
// MySQL's root account gets a random password nobody records, and may
// only sign in from inside its own container (MYSQL_ROOT_HOST=localhost):
// the application has its own account, and nothing the platform does
// needs root.
func databaseEnv(spec domain.DatabaseSpec) ([]string, error) {
	switch spec.Engine {
	case "", "postgres":
		return []string{
			"POSTGRES_DB=" + spec.DatabaseName,
			"POSTGRES_USER=" + spec.Username,
			"POSTGRES_PASSWORD=" + spec.Password,
		}, nil
	case "mysql":
		return []string{
			"MYSQL_DATABASE=" + spec.DatabaseName,
			"MYSQL_USER=" + spec.Username,
			"MYSQL_PASSWORD=" + spec.Password,
			"MYSQL_RANDOM_ROOT_PASSWORD=yes",
			"MYSQL_ROOT_HOST=localhost",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported database engine %q", spec.Engine)
	}
}

// StartDatabase implements FR-061: a real, dedicated PostgreSQL or MySQL
// container for one application.
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
	env, err := databaseEnv(spec)
	if err != nil {
		return "", err
	}
	if err := r.ensureImage(ctx, spec.ImageRef); err != nil {
		return "", err
	}
	cfg := &container.Config{Image: spec.ImageRef, Env: env}
	// unless-stopped: Docker starts it again when the host reboots. Nothing
	// else would — the health sweeper replaces application containers, not
	// databases — so after the first reboot of a real host every application
	// came back healthy but disconnected from its stopped database, and its
	// backups failed. A database is only ever stopped by being removed, so
	// this never revives one on purpose. Application containers get no policy:
	// restarted, they'd come up on a new host port the platform doesn't
	// route to, and the health sweeper already replaces them.
	hostCfg := &container.HostConfig{
		NetworkMode:   container.NetworkMode(spec.NetworkID),
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}
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

// WaitDatabaseReady blocks until a just-started database accepts TCP
// connections, or the timeout passes. It asks the database container
// itself (pg_isready, via exec) because platform-api is deliberately not
// on the application's private network, and so cannot dial the database.
//
// "-h 127.0.0.1" matters: during initdb the official image runs a
// temporary server that listens only on its Unix socket. pg_isready
// without -h would report that temporary server ready a moment before it
// shuts down to make way for the real one.
func (r *DockerRuntime) WaitDatabaseReady(ctx context.Context, containerID string, spec domain.DatabaseSpec, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := []string{"pg_isready", "-q", "-h", "127.0.0.1", "-p", "5432", "-U", spec.Username, "-d", spec.DatabaseName}
	var env []string
	if spec.Engine == "mysql" {
		// MySQL's entrypoint has the same temporary-server phase, with
		// networking switched off, so TCP to 127.0.0.1 only answers once the
		// real server is up. Signing in as the application's own account
		// also proves the entrypoint finished creating it. The password
		// travels in the exec's environment, never on its command line.
		cmd = []string{"mysql", "-h", "127.0.0.1", "-P", "3306", "-u", spec.Username, "-D", spec.DatabaseName, "-e", "SELECT 1"}
		env = []string{"MYSQL_PWD=" + spec.Password}
	}
	var lastErr error
	for time.Now().Before(deadline) {
		exitCode, err := r.execExitCode(ctx, containerID, cmd, env)
		if err == nil && exitCode == 0 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s exited %d", cmd[0], exitCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("database did not accept connections within %s: %w", timeout, lastErr)
}

// databaseIdentifier and databasePassword bound what SetDatabasePassword
// will splice into SQL: the platform's own role/database names and its
// base64url passwords (see service.generatePassword) — nothing else.
var (
	databaseIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	databasePassword   = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

// SetDatabasePassword changes the application account's password inside
// its database container — FR-068's rotation for the credential Module N
// generates. psql runs over the container's Unix socket, which the
// official image trusts (as it also trusts 127.0.0.1), so no current
// password is needed. Every connection an application makes arrives over
// its private network instead, where the password is enforced — which is
// also why rotation has to be verified from another container: inside the
// database's own network namespace, any password "works".
//
// The new password travels in the exec's environment, not on its command
// line, and is checked against the platform's own alphabet first because
// it ends up inside a SQL string literal.
func (r *DockerRuntime) SetDatabasePassword(ctx context.Context, containerID string, spec domain.DatabaseSpec) error {
	if !databaseIdentifier.MatchString(spec.Username) || !databaseIdentifier.MatchString(spec.DatabaseName) {
		return errors.New("refusing to rotate: unexpected database role or name")
	}
	if !databasePassword.MatchString(spec.Password) {
		return errors.New("refusing to rotate: the new password is outside the platform's password alphabet")
	}
	script := `psql -q -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" -c "ALTER ROLE \"$DB_USER\" PASSWORD '$NEW_PASSWORD'"`
	env := []string{"DB_USER=" + spec.Username, "DB_NAME=" + spec.DatabaseName, "NEW_PASSWORD=" + spec.Password}
	statement := "ALTER ROLE"
	if spec.Engine == "mysql" {
		// MySQL trusts no socket: the account signs in with its current
		// password (MYSQL_PWD) and changes its own. That needs no
		// privilege beyond the account itself — root stays unused.
		if !databasePassword.MatchString(spec.CurrentPassword) {
			return errors.New("refusing to rotate: the current password is outside the platform's password alphabet")
		}
		script = `mysql -h 127.0.0.1 -P 3306 -u "$DB_USER" -D "$DB_NAME" -e "ALTER USER CURRENT_USER() IDENTIFIED BY '$NEW_PASSWORD'"`
		env = append(env, "MYSQL_PWD="+spec.CurrentPassword)
		statement = "ALTER USER"
	}
	exitCode, err := r.execExitCode(ctx, containerID, []string{"sh", "-c", script}, env)
	if err != nil {
		return fmt.Errorf("run %s: %w", statement, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("%s exited %d", statement, exitCode)
	}
	return nil
}

// execExitCode runs cmd inside a container and returns its exit code.
func (r *DockerRuntime) execExitCode(ctx context.Context, containerID string, cmd, env []string) (int, error) {
	created, err := r.cli.ContainerExecCreate(ctx, containerID, types.ExecConfig{Cmd: cmd, Env: env})
	if err != nil {
		return -1, fmt.Errorf("create exec: %w", err)
	}
	if err := r.cli.ContainerExecStart(ctx, created.ID, types.ExecStartCheck{Detach: true}); err != nil {
		return -1, fmt.Errorf("start exec: %w", err)
	}
	// Bounded: pg_isready answers in milliseconds, so ten seconds means
	// something is wrong with the exec itself rather than the database.
	for i := 0; i < 100; i++ {
		inspected, err := r.cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return -1, fmt.Errorf("inspect exec: %w", err)
		}
		// Pid 0 means it hasn't started yet; not running with a pid means
		// it has finished.
		if !inspected.Running && inspected.Pid != 0 {
			return inspected.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return -1, fmt.Errorf("exec %v did not finish", cmd)
}
