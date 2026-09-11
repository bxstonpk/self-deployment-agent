// Module S (Logging), as the Runtime Platform sees it — FR-086 collection:
// follow every application container's output from its first line, and
// don't let a container go until its last line is stored. See
// internal/domain/logs.go for the module's scope.
package runtimeengine

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"

	"platform-api/internal/domain"
)

// LogSink is where collected lines go: the platform's central log store.
type LogSink interface {
	Append(ctx context.Context, lines []domain.LogLine) error
	LastLoggedAt(ctx context.Context, containerID string) (time.Time, bool, error)
}

// Container labels carrying a container's LogSource, so collection can be
// picked back up after a platform-api restart from the containers alone.
const (
	labelApplication = "platform.application_id"
	labelDeployment  = "platform.deployment_id"
	labelService     = "platform.service"
	// labelSecretEnvKeys names the injected variables whose values are
	// redacted — the names only; the values stay in the container's env.
	labelSecretEnvKeys = "platform.secret_env_keys"
)

// Injected variables whose values are connection details rather than
// secrets. Redacting "appdb" or "5432" from every line would only garble
// the logs.
var nonSecretEnv = map[string]bool{
	"DATABASE_HOST": true, "DATABASE_PORT": true, "DATABASE_NAME": true, "DATABASE_USER": true,
}

// minRedactable: anything shorter is not a plausible secret, and replacing
// every occurrence of, say, "1" would wreck the logs.
const minRedactable = 6

const (
	logFlushInterval = 500 * time.Millisecond
	logFlushLines    = 200
	// logBufferMax bounds what's held while the store is unreachable
	// (FR-086's exception flow: buffered locally, shipped once it's back).
	logBufferMax     = 20000
	stopDrainTimeout = 10 * time.Second
	maxLineBytes     = 64 * 1024
)

type redaction struct {
	name  string
	value string
}

// secretEnvKeys returns the names of the injected variables to redact.
func secretEnvKeys(env []string) []string {
	var keys []string
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && !nonSecretEnv[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// redactionsFor pairs each named key with its value in env. Longest value
// first, so DATABASE_URL is replaced whole before the password inside it.
func redactionsFor(env, keys []string) []redaction {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []redaction
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && want[k] && len(v) >= minRedactable {
			out = append(out, redaction{name: k, value: v})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].value) > len(out[j].value) })
	return out
}

func redact(message string, rs []redaction) string {
	for _, r := range rs {
		message = strings.ReplaceAll(message, r.value, "[REDACTED:"+r.name+"]")
	}
	return message
}

// parseLogLine splits Docker's timestamped line
// ("2026-09-11T04:05:06.123456789Z message").
func parseLogLine(raw string) (time.Time, string) {
	if ts, msg, ok := strings.Cut(raw, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return t, msg
		}
	}
	return time.Now().UTC(), raw
}

// cleanMessage makes a line storable: Postgres text holds neither NUL nor
// invalid UTF-8.
func cleanMessage(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(strings.TrimRight(s, "\r"), "\x00", ""), "�")
}

func containerLabels(src domain.LogSource, env []string) map[string]string {
	return map[string]string{
		labelApplication:   src.ApplicationID,
		labelDeployment:    src.DeploymentID,
		labelService:       src.Service,
		labelSecretEnvKeys: strings.Join(secretEnvKeys(env), ","),
	}
}

func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// collection is one container's pipeline: lines in, batches out.
type collection struct {
	sink        LogSink
	src         domain.LogSource
	containerID string
	redactions  []redaction
	after       time.Time // resume: lines at or before this are already stored

	mu      sync.Mutex
	pending []domain.LogLine
	flushMu sync.Mutex // one flush at a time, so no batch is sent twice
}

func (c *collection) add(stream domain.LogStream, raw string) {
	ts, msg := parseLogLine(raw)
	// Postgres keeps microseconds. Truncating here makes what's compared on
	// resume exactly what was stored.
	ts = ts.Truncate(time.Microsecond)
	if !c.after.IsZero() && !ts.After(c.after) {
		return
	}
	line := domain.LogLine{
		ApplicationID: c.src.ApplicationID, DeploymentID: c.src.DeploymentID, Service: c.src.Service,
		ContainerID: c.containerID, Stream: stream, LoggedAt: ts,
		Message: redact(cleanMessage(msg), c.redactions),
	}
	c.mu.Lock()
	c.pending = append(c.pending, line)
	c.mu.Unlock()
}

// flush sends everything pending, in small batches. On failure it keeps
// what's left for the next attempt, dropping only the oldest lines beyond
// logBufferMax — and saying so.
func (c *collection) flush() bool {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	for {
		c.mu.Lock()
		n := len(c.pending)
		if n == 0 {
			c.mu.Unlock()
			return true
		}
		if n > logFlushLines {
			n = logFlushLines
		}
		batch := append([]domain.LogLine(nil), c.pending[:n]...)
		c.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.sink.Append(ctx, batch)
		cancel()
		c.mu.Lock()
		if err != nil {
			if over := len(c.pending) - logBufferMax; over > 0 {
				c.pending = c.pending[over:]
				log.Printf("logs: store unreachable; dropped the oldest %d line(s) from %s", over, shortContainerID(c.containerID))
			}
			c.mu.Unlock()
			log.Printf("logs: storing lines from %s failed, keeping them for the next attempt: %v", shortContainerID(c.containerID), err)
			return false
		}
		c.pending = c.pending[n:]
		c.mu.Unlock()
	}
}

// lineWriter turns one stream's bytes into lines.
type lineWriter struct {
	c      *collection
	stream domain.LogStream
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.c.add(w.stream, string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	// A line longer than this is stored in pieces rather than held forever.
	if len(w.buf) > maxLineBytes {
		w.c.add(w.stream, string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) finish() {
	if len(w.buf) > 0 {
		w.c.add(w.stream, string(w.buf))
		w.buf = nil
	}
}

type follower struct {
	done chan struct{}
}

// follow reads a container's output until it stops, storing it as it goes.
func (r *DockerRuntime) follow(containerID string, src domain.LogSource, rs []redaction, after time.Time) {
	f := &follower{done: make(chan struct{})}
	r.followMu.Lock()
	r.followers[containerID] = f
	r.followMu.Unlock()

	c := &collection{sink: r.logs, src: src, containerID: containerID, redactions: rs, after: after}
	go func() {
		defer func() {
			r.followMu.Lock()
			delete(r.followers, containerID)
			r.followMu.Unlock()
			close(f.done)
		}()
		opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true}
		if !after.IsZero() {
			opts.Since = fmt.Sprintf("%d.%09d", after.Unix(), after.Nanosecond())
		}
		stream, err := r.cli.ContainerLogs(context.Background(), containerID, opts)
		if err != nil {
			log.Printf("logs: cannot follow %s: %v", shortContainerID(containerID), err)
			return
		}
		defer stream.Close()

		stopTicker := make(chan struct{})
		go func() {
			t := time.NewTicker(logFlushInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					c.flush()
				case <-stopTicker:
					return
				}
			}
		}()

		stdout := &lineWriter{c: c, stream: domain.LogStdout}
		stderr := &lineWriter{c: c, stream: domain.LogStderr}
		if _, err := stdcopy.StdCopy(stdout, stderr, stream); err != nil {
			log.Printf("logs: reading %s: %v", shortContainerID(containerID), err)
		}
		stdout.finish()
		stderr.finish()
		close(stopTicker)
		// The container has stopped writing. Store what's left — this is
		// what Stop waits on before removing the container.
		for attempt := 0; attempt < 5 && !c.flush(); attempt++ {
			time.Sleep(time.Second)
		}
	}()
}

// waitForLogs blocks until a container's follower has stored its last
// lines, or stopDrainTimeout passes.
func (r *DockerRuntime) waitForLogs(containerID string) {
	r.followMu.Lock()
	f := r.followers[containerID]
	r.followMu.Unlock()
	if f == nil {
		return
	}
	select {
	case <-f.done:
	case <-time.After(stopDrainTimeout):
		log.Printf("logs: %s's last lines were not all stored within %s", shortContainerID(containerID), stopDrainTimeout)
	}
}

// ResumeLogCollection picks collection back up for application containers
// that outlived a platform-api restart, from where each one left off. That
// includes containers that exited while platform-api was down but haven't
// been removed yet: Docker still holds their output.
func (r *DockerRuntime) ResumeLogCollection(ctx context.Context) (int, error) {
	if r.logs == nil {
		return 0, nil
	}
	list, err := r.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", labelApplication))})
	if err != nil {
		return 0, fmt.Errorf("list application containers: %w", err)
	}
	resumed := 0
	for _, c := range list {
		r.followMu.Lock()
		_, already := r.followers[c.ID]
		r.followMu.Unlock()
		if already {
			continue
		}
		inspected, err := r.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			log.Printf("logs: cannot resume %s: %v", shortContainerID(c.ID), err)
			continue
		}
		after, _, err := r.logs.LastLoggedAt(ctx, c.ID)
		if err != nil {
			log.Printf("logs: cannot resume %s: %v", shortContainerID(c.ID), err)
			continue
		}
		src := domain.LogSource{ApplicationID: c.Labels[labelApplication], DeploymentID: c.Labels[labelDeployment], Service: c.Labels[labelService]}
		r.follow(c.ID, src, redactionsFor(inspected.Config.Env, strings.Split(c.Labels[labelSecretEnvKeys], ",")), after)
		resumed++
	}
	return resumed, nil
}
