package buildengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/docker/docker/api/types"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// Tests run as a throwaway image build of a test recipe: the same sandbox
// a build runs in (no Docker socket, nothing mounted from the host), with
// memory and CPU limits, and the image removed afterwards. Dependencies are
// installed before the PLATFORM_TEST_RUN build argument, so the daemon's
// layer cache keeps them between runs, while every step after it runs
// again each time (a cached step would print nothing to report).

// npmScriptIs exits 0 when package.json has the named script (npm's own
// placeholder test script, which only fails, doesn't count).
func npmScriptIs(name string) string {
	return fmt.Sprintf(`node -e "const s=(require('./package.json').scripts||{})['%s']; process.exit(s && !/no test specified/.test(s) ? 0 : 1)"`, name)
}

// GenerateTestDockerfile is the test recipe for a runtime:
//   - go: go vet ./... and go test ./...
//   - python: requirements installed, every file compiled, then pytest
//     when there are test files (test_*.py, *_test.py)
//   - Node runtimes: dependencies installed, the build script when there
//     is one (a TypeScript or bundler compile is a check too), then the
//     test script when there is one
//
// "Nothing to run" is reported as such, never as a pass of tests that
// don't exist.
func GenerateTestDockerfile(runtime, baseImage string) string {
	switch runtime {
	case "go":
		return fmt.Sprintf(`FROM %s
ENV CGO_ENABLED=0
WORKDIR /app
COPY . .
RUN go mod download
ARG PLATFORM_TEST_RUN
RUN echo "== go vet ./..." && go vet ./...
RUN echo "== go test ./..." && go test ./...
`, baseImage)
	case "python":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN if [ -f requirements.txt ]; then pip install --no-cache-dir --disable-pip-version-check -r requirements.txt; fi
ARG PLATFORM_TEST_RUN
RUN echo "== python -m compileall ." && python -m compileall -q .
RUN if find . -name 'test_*.py' -o -name '*_test.py' | grep -q .; then echo "== python -m pytest" && pip install --no-cache-dir --disable-pip-version-check -q pytest && python -m pytest -q; else echo "== no test files (test_*.py, *_test.py): no tests to run"; fi
`, baseImage)
	case "nodejs", "typescript", "react", "vue", "nextjs":
		return fmt.Sprintf(`FROM %s
ENV NPM_CONFIG_UPDATE_NOTIFIER=false NO_COLOR=1
WORKDIR /app
COPY . .
RUN npm install --no-audit --no-fund
ARG PLATFORM_TEST_RUN
RUN if %s; then echo "== npm run build" && npm run build; else echo "== no build script"; fi
RUN if %s; then echo "== npm test" && CI=true npm test; else echo "== no test script in package.json: no tests to run"; fi
`, baseImage, npmScriptIs("build"), npmScriptIs("test"))
	default:
		return fmt.Sprintf(`FROM %s
RUN echo "== no test recipe for runtime %s"
`, baseImage, runtime)
	}
}

// maxOutputLines is how much of a service's test output is kept: the end,
// where failures and summaries are.
const maxOutputLines = 250

func (e *DockerEngine) RunTests(ctx context.Context, req service.TestEngineRequest) []domain.ServiceTestResult {
	names := make([]string, 0, len(req.Services))
	for name := range req.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]domain.ServiceTestResult, 0, len(names))
	for _, name := range names {
		spec := req.Services[name]
		res := domain.ServiceTestResult{Service: name, Runtime: spec.Runtime}
		buildContext, err := serviceContext(req.SourceArchive, name, GenerateTestDockerfile(spec.Runtime, spec.BaseImage))
		if err != nil {
			res.Status, res.Output = domain.TestRunFailed, err.Error()
			results = append(results, res)
			continue
		}
		tag := fmt.Sprintf("platform-test/%s-%s:%s", sanitizeTag(req.ApplicationName), sanitizeTag(name), shortID(req.RunID))
		runID := req.RunID
		opts := types.ImageBuildOptions{
			Tags:        []string{tag},
			Dockerfile:  "Dockerfile",
			Remove:      true,
			ForceRemove: true,
			BuildArgs:   map[string]*string{"PLATFORM_TEST_RUN": &runID},
		}
		if req.Limits.MemoryBytes > 0 {
			opts.Memory = req.Limits.MemoryBytes
			opts.MemorySwap = req.Limits.MemoryBytes
		}
		if req.Limits.CPUs > 0 {
			opts.CPUPeriod = 100000
			opts.CPUQuota = int64(req.Limits.CPUs * 100000)
		}
		resp, err := e.cli.ImageBuild(ctx, buildContext, opts)
		if err != nil {
			res.Status, res.Output = domain.TestRunError, "the platform couldn't start the test run: "+err.Error()
			results = append(results, res)
			continue
		}
		output, runErr := drainTestOutput(resp.Body)
		_ = resp.Body.Close()
		switch {
		case runErr == nil:
			res.Status = domain.TestRunPassed
			_, _ = e.cli.ImageRemove(context.WithoutCancel(ctx), tag, types.ImageRemoveOptions{Force: true, PruneChildren: true})
		case ctx.Err() != nil:
			res.Status = domain.TestRunError
			output += "\n== stopped: the test run took longer than the platform allows"
		case classifyBuildError(runErr) == domain.ErrorCategorySource:
			res.Status = domain.TestRunFailed
			output += "\n== failed: " + firstLine(runErr.Error())
		default:
			res.Status = domain.TestRunError
			output += "\n== the platform couldn't run the tests: " + firstLine(runErr.Error())
		}
		res.Output = output
		results = append(results, res)
	}
	return results
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// drainTestOutput keeps what the steps printed, without the builder's own
// bookkeeping lines, bounded to the last maxOutputLines.
func drainTestOutput(r io.Reader) (string, error) {
	dec := json.NewDecoder(r)
	var lines []string
	var runErr string
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return strings.Join(lines, "\n"), fmt.Errorf("reading test output: %w", err)
		}
		if msg.Error != "" {
			runErr = msg.Error
		}
		for _, line := range strings.Split(strings.TrimRight(ansiEscape.ReplaceAllString(msg.Stream, ""), "\n"), "\n") {
			if keepTestLine(line) {
				lines = append(lines, line)
			}
		}
	}
	dropped := 0
	if len(lines) > maxOutputLines {
		dropped = len(lines) - maxOutputLines
		lines = lines[dropped:]
	}
	out := strings.Join(lines, "\n")
	if dropped > 0 {
		out = fmt.Sprintf("(%d earlier lines not kept)\n%s", dropped, out)
	}
	if runErr != "" {
		return out, errors.New(runErr)
	}
	return out, nil
}

// ansiEscape is a terminal colour or cursor code: noise in a report.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func keepTestLine(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case t == "",
		strings.HasPrefix(t, "---> "),
		strings.HasPrefix(t, "Step "),
		strings.HasPrefix(t, "Removing intermediate container"),
		strings.HasPrefix(t, "Successfully built"),
		strings.HasPrefix(t, "Successfully tagged"),
		strings.HasPrefix(t, "Sending build context"):
		return false
	}
	return true
}
