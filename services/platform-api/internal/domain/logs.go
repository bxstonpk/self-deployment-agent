// Logs implements the core types for Module S (Logging) —
// docs/02_Functional_Requirements.md FR-086/087/089. Scope, stated plainly:
//
//   - FR-086 collection: every application container the platform starts is
//     followed from its first line, and each stdout/stderr line is stored
//     centrally (the platform's own Postgres), tagged with its application,
//     deployment, service and container. Lines are captured as they are
//     written, so a container the platform later removes — a failed deploy,
//     a restart, a scale-down — keeps its history: Stop waits for a
//     container's last lines before removing it. Collection resumes after a
//     platform-api restart for containers that are still there.
//   - Secret values the platform injected into a container are redacted
//     from its lines before they are stored ("[REDACTED:NAME]"). Only those:
//     anything else an application prints — PII included — is stored as
//     written, because the platform can't know what else is sensitive.
//   - FR-087/FR-089 access: owners only, and anyone else gets the same 404 a
//     nonexistent application does, so a refusal doesn't reveal that the
//     application exists. Every successful read is audited
//     (application.read_logs).
//   - FR-088 (retention and purge) is not implemented: the retention period
//     is TBD in the requirement itself, and nothing is purged yet.
//   - Log levels aren't parsed; each line carries its stream (stdout or
//     stderr) instead.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LogSource is who a container's output belongs to. Zero for a container
// nobody asked to collect — the database containers, for instance.
type LogSource struct {
	ApplicationID string
	DeploymentID  string
	Service       string
}

// Collect reports whether a container with this source is followed.
func (s LogSource) Collect() bool {
	return s.ApplicationID != ""
}

type LogStream string

const (
	LogStdout LogStream = "stdout"
	LogStderr LogStream = "stderr"
)

// LogLine is one line an application container wrote, already redacted.
type LogLine struct {
	ID            int64
	ApplicationID string
	DeploymentID  string
	Service       string
	ContainerID   string
	Stream        LogStream
	LoggedAt      time.Time
	Message       string
}

// LogQuery is FR-087's filter set. Results come back newest first.
type LogQuery struct {
	ApplicationID string
	Service       string
	Environment   string
	Contains      string
	Since         *time.Time
	Until         *time.Time
	// Before continues a previous page: only lines older than its last one.
	Before *LogCursor
	Limit  int
}

const (
	DefaultLogQueryLimit = 200
	MaxLogQueryLimit     = 1000
)

// ClampLogLimit is how many lines a query returns at most, for a requested
// limit (zero meaning "the default").
func ClampLogLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultLogQueryLimit
	case n > MaxLogQueryLimit:
		return MaxLogQueryLimit
	}
	return n
}

var ErrInvalidLogCursor = errors.New("invalid log cursor")

// LogCursor is where a page of results ended; the next page is everything
// strictly older. A timestamp alone can't mark that — lines written in the
// same microsecond share one — so the line's id breaks the tie.
type LogCursor struct {
	LoggedAt time.Time
	ID       int64
}

func (c LogCursor) String() string {
	return fmt.Sprintf("%d.%d", c.LoggedAt.UnixMicro(), c.ID)
}

func ParseLogCursor(s string) (LogCursor, error) {
	micros, id, ok := strings.Cut(s, ".")
	if !ok {
		return LogCursor{}, ErrInvalidLogCursor
	}
	m, err1 := strconv.ParseInt(micros, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil || m < 0 || n <= 0 {
		return LogCursor{}, ErrInvalidLogCursor
	}
	return LogCursor{LoggedAt: time.UnixMicro(m).UTC(), ID: n}, nil
}
