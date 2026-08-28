// Package buildengine is the real implementation of service.BuildEngine,
// talking to the Docker daemon (mounted socket) the same way MOD-05 Build
// Engine would talk to a real, IT-governed build cluster in production —
// employees and the AI agent never get this access; only this internal
// platform component does.
package buildengine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type DockerEngine struct {
	cli *client.Client
}

// NewDockerEngine wraps a shared *client.Client — see
// cmd/api/main.go, which constructs one Docker client and hands it to this
// package, internal/imagescan, and internal/runtimeengine alike, rather
// than each opening its own connection to the daemon.
func NewDockerEngine(cli *client.Client) *DockerEngine {
	return &DockerEngine{cli: cli}
}

func (e *DockerEngine) Build(ctx context.Context, req service.BuildEngineRequest) (map[string]string, error) {
	imageRefs := make(map[string]string, len(req.Services))

	for name, spec := range req.Services {
		buildContext, err := buildServiceContext(req.SourceArchive, name, spec)
		if err != nil {
			return nil, &domain.BuildFailure{Category: domain.ErrorCategorySource, Service: name, Detail: err.Error()}
		}

		tag := fmt.Sprintf("platform-build/%s-%s:%s", sanitizeTag(req.ApplicationName), sanitizeTag(name), shortID(req.BuildID))

		resp, err := e.cli.ImageBuild(ctx, buildContext, types.ImageBuildOptions{
			Tags:       []string{tag},
			Dockerfile: "Dockerfile",
			Remove:     true,
		})
		if err != nil {
			return nil, &domain.BuildFailure{Category: domain.ErrorCategoryPlatform, Service: name, Detail: err.Error()}
		}
		buildErr := drainBuildOutput(resp.Body)
		_ = resp.Body.Close()
		if buildErr != nil {
			return nil, &domain.BuildFailure{Category: classifyBuildError(buildErr), Service: name, Detail: buildErr.Error()}
		}

		imageRefs[name] = tag
	}

	return imageRefs, nil
}

// buildServiceContext extracts the `<serviceName>/` subtree of the uploaded
// tar.gz (the documented monorepo convention — see build_service.go's
// package doc) into a fresh, uncompressed tar buffer, and injects the
// generated Dockerfile alongside it as the build context Docker receives.
func buildServiceContext(sourceArchive []byte, serviceName string, spec service.BuildServiceSpec) (*bytes.Reader, error) {
	gz, err := gzip.NewReader(bytes.NewReader(sourceArchive))
	if err != nil {
		return nil, fmt.Errorf("source archive is not valid gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var out bytes.Buffer
	tw := tar.NewWriter(&out)

	prefix := serviceName + "/"
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading source archive: %w", err)
		}
		if !strings.HasPrefix(hdr.Name, prefix) {
			continue
		}
		newName := strings.TrimPrefix(hdr.Name, prefix)
		if newName == "" {
			continue // the directory entry for the service root itself
		}
		found = true
		hdr.Name = newName
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("writing build context: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tw, tr); err != nil { //nolint:gosec // build context size is bounded by the caller's upload limit
				return nil, fmt.Errorf("copying %s into build context: %w", newName, err)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("uploaded source archive has no top-level directory %q matching this application's declared service name", serviceName)
	}

	dockerfile := GenerateDockerfile(spec.Runtime, spec.BaseImage, spec.Port)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile))}); err != nil {
		return nil, fmt.Errorf("writing generated Dockerfile: %w", err)
	}
	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return nil, fmt.Errorf("writing generated Dockerfile: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing build context: %w", err)
	}

	return bytes.NewReader(out.Bytes()), nil
}

// drainBuildOutput reads Docker's streamed build-log JSON lines. The
// ImageBuild HTTP call itself returns 200 even when the build fails inside
// the daemon — a failure only shows up as an `error` field in the stream,
// so it must be read to completion to know the real outcome.
func drainBuildOutput(r io.Reader) error {
	dec := json.NewDecoder(r)
	var buildError string
	// FR-038 requires *actionable* failure detail, e.g. the actual compiler
	// error — not just Docker's generic "command returned a non-zero code"
	// summary. Keep a bounded tail of the preceding `stream` output so a
	// real compile/dependency error is included in what gets reported back.
	const tailSize = 20
	tail := make([]string, 0, tailSize)

	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("reading build output: %w", err)
		}
		if msg.Stream != "" {
			line := strings.TrimRight(msg.Stream, "\n")
			if line != "" {
				tail = append(tail, line)
				if len(tail) > tailSize {
					tail = tail[1:]
				}
			}
		}
		if msg.Error != "" {
			buildError = msg.Error
		}
	}

	if buildError != "" {
		if len(tail) > 0 {
			return fmt.Errorf("%s\n--- build output (last %d lines) ---\n%s", buildError, len(tail), strings.Join(tail, "\n"))
		}
		return errors.New(buildError)
	}
	return nil
}

// classifyBuildError implements FR-038's source-vs-platform distinction
// for failures that happen *during* the Docker build (as opposed to
// context-preparation failures, already source, or request-setup
// failures, already platform — see the call sites in Build). Found for
// real, not assumed: an early version categorized every build-stream
// error as "source", which meant a transient base-image pull failure
// (a TLS handshake timeout pulling from the registry) was reported as if
// it were the employee's fault. Docker's own phrasing distinguishes the
// two cases reliably: a failing RUN/CMD instruction always says "returned
// a non-zero code"; anything else during the build (pulling the base
// image, daemon-side errors) doesn't.
func classifyBuildError(err error) domain.ErrorCategory {
	if strings.Contains(err.Error(), "returned a non-zero code") {
		return domain.ErrorCategorySource
	}
	return domain.ErrorCategoryPlatform
}

var nonTagChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func sanitizeTag(s string) string {
	s = strings.ToLower(s)
	s = nonTagChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
