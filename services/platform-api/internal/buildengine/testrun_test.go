package buildengine

import (
	"strings"
	"testing"
)

func TestGenerateTestDockerfile_RunsEachRuntimesChecksAfterTheCacheBreak(t *testing.T) {
	cases := map[string][]string{
		"go":         {"go mod download", "go vet ./...", "go test ./..."},
		"python":     {"pip install", "compileall", "pytest"},
		"typescript": {"npm install", "npm run build", "CI=true npm test"},
		"react":      {"npm install", "npm run build", "npm test"},
	}
	for runtime, steps := range cases {
		df := GenerateTestDockerfile(runtime, "base:1")
		if !strings.HasPrefix(df, "FROM base:1\n") {
			t.Errorf("%s: starts from the governed image: %q", runtime, df)
		}
		arg := strings.Index(df, "ARG PLATFORM_TEST_RUN")
		if arg < 0 {
			t.Fatalf("%s: no cache break", runtime)
		}
		// Installing dependencies comes before the break (cached); the
		// checks come after it (run every time).
		if i := strings.Index(df, steps[0]); i < 0 || i > arg {
			t.Errorf("%s: %q should be before the cache break", runtime, steps[0])
		}
		for _, step := range steps[1:] {
			if i := strings.LastIndex(df, step); i < arg {
				t.Errorf("%s: %q should be after the cache break", runtime, step)
			}
		}
	}
}

func TestGenerateTestDockerfile_NpmsPlaceholderTestScriptIsNotATest(t *testing.T) {
	df := GenerateTestDockerfile("nodejs", "node:22")
	if !strings.Contains(df, "no test specified") || !strings.Contains(df, "no tests to run") {
		t.Fatalf("npm init's failing placeholder must count as no tests, and say so: %s", df)
	}
}

func TestDrainTestOutput_KeepsWhatTheStepsPrintedNotTheBuildersBookkeeping(t *testing.T) {
	stream := strings.Join([]string{
		`{"stream":"Step 1/6 : FROM golang:1.25-alpine\n"}`,
		`{"stream":" ---> 1234abcd\n"}`,
		`{"stream":"Step 5/6 : RUN echo \"== go test ./...\" && go test ./...\n"}`,
		`{"stream":" ---> Running in 99887766\n"}`,
		`{"stream":"== go test ./...\n"}`,
		`{"stream":"--- FAIL: TestLeave (0.00s)\n    main_test.go:9: วันลาไม่ถูกต้อง\n"}`,
		`{"stream":"\u001b[91mFAIL\u001b[0m\n"}`,
		`{"stream":"Removing intermediate container 99887766\n"}`,
		`{"errorDetail":{"code":1},"error":"The command '/bin/sh -c go test ./...' returned a non-zero code: 1"}`,
	}, "\n")
	out, err := drainTestOutput(strings.NewReader(stream))
	if err == nil || classifyBuildError(err) != "source" {
		t.Fatalf("a failing test step is the source's failure: %v", err)
	}
	want := "== go test ./...\n--- FAIL: TestLeave (0.00s)\n    main_test.go:9: วันลาไม่ถูกต้อง\nFAIL"
	if out != want {
		t.Fatalf("output\n got %q\nwant %q", out, want)
	}
}

func TestDrainTestOutput_KeepsTheEndOfALongRun(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxOutputLines+50; i++ {
		b.WriteString(`{"stream":"line\n"}` + "\n")
	}
	b.WriteString(`{"stream":"the summary\n"}`)
	out, err := drainTestOutput(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "(51 earlier lines not kept)\n") || !strings.HasSuffix(out, "the summary") {
		t.Fatalf("keeps the end and says what it dropped: %q…%q", out[:40], out[len(out)-20:])
	}
}
