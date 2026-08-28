package buildengine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// makeTarGz builds an in-memory tar.gz from a map of path -> content, for
// exercising buildServiceContext without touching the filesystem or Docker.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatalf("write header for %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content for %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func readTarEntries(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading tar entry %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(content)
	}
	return out
}

func TestBuildServiceContext_ExtractsOnlyTheMatchingServiceSubtree(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"frontend/":             "",
		"frontend/package.json": `{"name":"frontend"}`,
		"frontend/src/index.js": "console.log('hi')",
		"api/":                  "",
		"api/main.go":           "package main",
	})

	ctx, err := buildServiceContext(archive, "frontend", service.BuildServiceSpec{Runtime: "react", BaseImage: "node:20-alpine", Port: 3000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := readTarEntries(t, ctx)
	if _, ok := entries["package.json"]; !ok {
		t.Errorf("expected package.json at the root of the extracted context, got entries: %+v", keys(entries))
	}
	if _, ok := entries["src/index.js"]; !ok {
		t.Errorf("expected src/index.js preserved under its relative path, got entries: %+v", keys(entries))
	}
	if _, ok := entries["main.go"]; ok {
		t.Errorf("did not expect the api/ service's files to leak into frontend's build context")
	}
	dockerfile, ok := entries["Dockerfile"]
	if !ok {
		t.Fatal("expected a generated Dockerfile injected into the build context")
	}
	if !strings.Contains(dockerfile, "node:20-alpine") {
		t.Errorf("expected the generated Dockerfile to reference the resolved base image, got:\n%s", dockerfile)
	}
}

func TestBuildServiceContext_NoMatchingServiceDirectory_Errors(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"api/main.go": "package main",
	})

	_, err := buildServiceContext(archive, "frontend", service.BuildServiceSpec{Runtime: "react", BaseImage: "node:20-alpine"})
	if err == nil {
		t.Fatal("expected an error when the archive has no top-level directory matching the service name")
	}
}

func TestBuildServiceContext_InvalidGzip_Errors(t *testing.T) {
	_, err := buildServiceContext([]byte("not gzip data"), "frontend", service.BuildServiceSpec{Runtime: "react"})
	if err == nil {
		t.Fatal("expected an error for a non-gzip source archive")
	}
}

func TestDrainBuildOutput_SuccessStream_NoError(t *testing.T) {
	stream := `{"stream":"Step 1/4 : FROM node:20-alpine\n"}
{"stream":"Successfully built abc123\n"}
`
	if err := drainBuildOutput(strings.NewReader(stream)); err != nil {
		t.Errorf("expected no error for a clean build stream, got: %v", err)
	}
}

func TestDrainBuildOutput_ErrorLine_ReturnsError(t *testing.T) {
	stream := `{"stream":"Step 1/4 : FROM node:20-alpine\n"}
{"stream":"npm ERR! missing script: start\n"}
{"errorDetail":{"message":"The command '/bin/sh -c npm install' returned a non-zero code: 1"},"error":"The command '/bin/sh -c npm install' returned a non-zero code: 1"}
`
	err := drainBuildOutput(strings.NewReader(stream))
	if err == nil {
		t.Fatal("expected an error when the build stream contains an error line")
	}
	if !strings.Contains(err.Error(), "returned a non-zero code") {
		t.Errorf("expected the error to carry the daemon's summary message, got: %v", err)
	}
	// FR-038: the generic Docker summary alone isn't actionable — the
	// preceding stream output (the actual npm error) must be included too.
	if !strings.Contains(err.Error(), "npm ERR! missing script: start") {
		t.Errorf("expected the error to include the actual failure output from the stream, got: %v", err)
	}
}

func TestClassifyBuildError(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want domain.ErrorCategory
	}{
		{
			name: "failing RUN instruction is the employee's fault",
			err:  "The command '/bin/sh -c go build -o /app/server .' returned a non-zero code: 1",
			want: domain.ErrorCategorySource,
		},
		{
			name: "base image pull failure is a platform problem",
			err:  `failed to copy: httpReadSeeker: failed open: failed to do request: Get "https://registry-1.docker.io/v2/library/golang/manifests/...": net/http: TLS handshake timeout`,
			want: domain.ErrorCategoryPlatform,
		},
		{
			name: "unrecognized daemon error defaults to platform, not blaming the employee",
			err:  "unexpected daemon error",
			want: domain.ErrorCategoryPlatform,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyBuildError(errors.New(c.err))
			if got != c.want {
				t.Errorf("classifyBuildError(%q) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestGenerateDockerfile_PerRuntimeConventions(t *testing.T) {
	cases := []struct {
		runtime string
		want    string
	}{
		{"go", "go build"},
		{"nodejs", `CMD ["npm", "start"]`},
		{"python", `CMD ["python", "app.py"]`},
		{"react", `CMD ["serve", "-s", "build"`},
		{"vue", `CMD ["serve", "-s", "dist"`},
		{"nextjs", "npm run build"},
	}
	for _, c := range cases {
		out := GenerateDockerfile(c.runtime, "base:image", 8080)
		if !strings.Contains(out, "FROM base:image") {
			t.Errorf("runtime %s: expected FROM base:image, got:\n%s", c.runtime, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("runtime %s: expected Dockerfile to contain %q, got:\n%s", c.runtime, c.want, out)
		}
	}
}

func TestSanitizeTag(t *testing.T) {
	if got := sanitizeTag("My App!!"); got != "my-app" {
		t.Errorf("expected sanitized tag 'my-app', got %q", got)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
