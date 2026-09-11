package runtimeengine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
)

type fakeSink struct {
	batches [][]domain.LogLine
	failFor int // fail this many Append calls first
}

func (f *fakeSink) Append(ctx context.Context, lines []domain.LogLine) error {
	if f.failFor > 0 {
		f.failFor--
		return errors.New("store unreachable")
	}
	f.batches = append(f.batches, append([]domain.LogLine(nil), lines...))
	return nil
}

func (f *fakeSink) LastLoggedAt(ctx context.Context, containerID string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (f *fakeSink) stored() []domain.LogLine {
	var out []domain.LogLine
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

const password = "q7Rk2mZ9xVb4nT1s"

var env = []string{
	"DATABASE_URL=postgres://appuser:" + password + "@platform-db-x:5432/appdb?sslmode=disable",
	"DATABASE_HOST=platform-db-x", "DATABASE_PORT=5432", "DATABASE_NAME=appdb", "DATABASE_USER=appuser",
	"DATABASE_PASSWORD=" + password,
	"API_KEY=sk-live-0123456789abcdef",
	"FEATURE_FLAG=1",
}

func TestSecretEnvKeys_SkipsConnectionDetails(t *testing.T) {
	got := secretEnvKeys(env)
	want := []string{"API_KEY", "DATABASE_PASSWORD", "DATABASE_URL", "FEATURE_FLAG"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The connection string contains the password; replacing the password
// first would leave "postgres://appuser:[REDACTED...]@..." — replacing the
// longer value first removes the whole thing.
func TestRedact_TheWholeURLBeforeThePasswordInsideIt(t *testing.T) {
	rs := redactionsFor(env, secretEnvKeys(env))
	url := strings.TrimPrefix(env[0], "DATABASE_URL=")
	got := redact("connecting to "+url+" with "+password, rs)
	if strings.Contains(got, password) || strings.Contains(got, "postgres://") {
		t.Fatalf("a secret survived: %q", got)
	}
	if got != "connecting to [REDACTED:DATABASE_URL] with [REDACTED:DATABASE_PASSWORD]" {
		t.Fatalf("unexpected redaction: %q", got)
	}
}

func TestRedact_ShortAndNonSecretValuesLeftAlone(t *testing.T) {
	rs := redactionsFor(env, secretEnvKeys(env))
	got := redact("flag=1 db=appdb port=5432 user=appuser", rs)
	if got != "flag=1 db=appdb port=5432 user=appuser" {
		t.Fatalf("over-redacted: %q", got)
	}
}

func TestParseLogLine(t *testing.T) {
	ts, msg := parseLogLine("2026-09-11T04:05:06.123456789Z listening on :8080")
	if msg != "listening on :8080" || ts.Nanosecond() != 123456789 || ts.Second() != 6 {
		t.Fatalf("got %v %q", ts, msg)
	}
	_, msg = parseLogLine("no timestamp here")
	if msg != "no timestamp here" {
		t.Fatalf("a line without a timestamp lost text: %q", msg)
	}
}

func TestCleanMessage(t *testing.T) {
	if got := cleanMessage("a\x00b\xffc\r"); got != "ab�c" {
		t.Fatalf("got %q", got)
	}
}

func newTestCollection(sink LogSink) *collection {
	return &collection{
		sink: sink, src: domain.LogSource{ApplicationID: "app-1", DeploymentID: "dep-1", Service: "api"},
		containerID: "c1", redactions: redactionsFor(env, secretEnvKeys(env)),
	}
}

// Writes arrive in arbitrary chunks; lines must come out whole, tagged and
// redacted, and a final unterminated line must not be lost.
func TestLineWriter_ReassemblesLinesAcrossWrites(t *testing.T) {
	sink := &fakeSink{}
	c := newTestCollection(sink)
	w := &lineWriter{c: c, stream: domain.LogStderr}
	for _, chunk := range []string{"2026-09-11T04:05:06Z us", "ing key sk-live-0123456789abcdef\n2026-09-11T04:05:07Z bye"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.finish()
	c.flush()

	lines := sink.stored()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %+v", lines)
	}
	if lines[0].Message != "using key [REDACTED:API_KEY]" || lines[0].Stream != domain.LogStderr ||
		lines[0].ApplicationID != "app-1" || lines[0].Service != "api" || lines[0].ContainerID != "c1" {
		t.Fatalf("first line wrong: %+v", lines[0])
	}
	if lines[1].Message != "bye" {
		t.Fatalf("the unterminated last line was lost: %+v", lines[1])
	}
}

// After a platform-api restart, collection resumes from the last stored
// line. Anything at or before it is a repeat, however Docker's own "since"
// filter rounds.
func TestCollection_SkipsLinesAlreadyStored(t *testing.T) {
	sink := &fakeSink{}
	c := newTestCollection(sink)
	c.after, _ = time.Parse(time.RFC3339Nano, "2026-09-11T04:05:06.5Z")
	c.add(domain.LogStdout, "2026-09-11T04:05:06.2Z already stored")
	// Docker reports nanoseconds; the store kept this one as .500000.
	c.add(domain.LogStdout, "2026-09-11T04:05:06.500000400Z already stored too")
	c.add(domain.LogStdout, "2026-09-11T04:05:06.7Z new")
	c.flush()
	if lines := sink.stored(); len(lines) != 1 || lines[0].Message != "new" {
		t.Fatalf("expected only the new line, got %+v", lines)
	}
}

// FR-086's exception flow: an unreachable store must not lose lines.
func TestCollection_KeepsLinesWhileTheStoreIsDown(t *testing.T) {
	sink := &fakeSink{failFor: 1}
	c := newTestCollection(sink)
	c.add(domain.LogStdout, "2026-09-11T04:05:06Z one")
	c.add(domain.LogStdout, "2026-09-11T04:05:07Z two")

	if c.flush() {
		t.Fatal("the first flush should report failure")
	}
	if !c.flush() {
		t.Fatal("the retry should succeed")
	}
	lines := sink.stored()
	if len(lines) != 2 || lines[0].Message != "one" || lines[1].Message != "two" {
		t.Fatalf("lines lost or reordered across a failed flush: %+v", lines)
	}
}

func TestCollection_FlushesInSmallBatches(t *testing.T) {
	sink := &fakeSink{}
	c := newTestCollection(sink)
	for i := 0; i < logFlushLines*2+5; i++ {
		c.add(domain.LogStdout, "2026-09-11T04:05:06Z line")
	}
	c.flush()
	if len(sink.batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(sink.batches))
	}
	for _, b := range sink.batches {
		if len(b) > logFlushLines {
			t.Fatalf("a batch of %d exceeds %d", len(b), logFlushLines)
		}
	}
}
