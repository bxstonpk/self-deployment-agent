package domain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// Tests run by the platform (DEC-029 part 2, migration 0025): an
// application's tests run in a build sandbox on the platform, so a machine
// needs no toolchain, and the report the approver signs is the platform's
// own, not the requester's word.

type TestRunStatus string

const (
	TestRunRunning TestRunStatus = "running"
	TestRunPassed  TestRunStatus = "passed"
	TestRunFailed  TestRunStatus = "failed"
	// TestRunError is the platform failing to run them (a pull, the
	// daemon, a timeout), not the tests failing.
	TestRunError TestRunStatus = "error"
)

type TestRun struct {
	ID            string
	ApplicationID string
	RequestedBy   string
	Status        TestRunStatus
	// SourceDigest is SourceTreeDigest of the archive the tests ran on.
	SourceDigest string
	Report       string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

// ServiceTestResult is one service's part of a run.
type ServiceTestResult struct {
	Service string
	Runtime string
	Status  TestRunStatus
	// Output is what the test steps printed, trimmed to a bound.
	Output string
}

const AuditActionRunTests AuditAction = "application.run_tests"

// PlatformTestReportHeader starts every report of a test run;
// RequesterTestReportHeader starts what a requester wrote themselves, so
// the approver always knows which is which.
const (
	PlatformTestReportHeader  = "Tests run by the platform (not reported by the requester)"
	RequesterTestReportHeader = "--- from the requester (not run by the platform) ---"
)

var (
	ErrTestRunNotPassed      = errors.New("that test run didn't pass: fix the failures and run the tests again")
	ErrTestRunSourceMismatch = errors.New("that test run was for different source than this build: run the tests on the source you are deploying")
	ErrTestRunBusy           = errors.New("the platform is running other tests; try again in a few minutes")
)

// SourceTreeDigest identifies a source archive by what is in it: the
// sha256 over every regular file's path and content hash, sorted. Packing
// the same folder twice gives different gzip bytes (the header carries a
// time), but the same digest — so tests run on one upload can be matched
// to a build of another.
func SourceTreeDigest(archive []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", fmt.Errorf("not valid gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var lines []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		h := sha256.New()
		if _, err := io.Copy(h, tr); err != nil { //nolint:gosec // bounded by the upload limit
			return "", fmt.Errorf("reading %s: %w", hdr.Name, err)
		}
		lines = append(lines, fmt.Sprintf("%s\t%x\n", strings.TrimPrefix(path.Clean(hdr.Name), "./"), h.Sum(nil)))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return fmt.Sprintf("%x", sum), nil
}
