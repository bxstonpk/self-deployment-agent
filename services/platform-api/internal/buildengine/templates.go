package buildengine

import (
	"fmt"

	"platform-api/internal/domain"
)

// runtimeStageImage is the fixed final-stage image for runtimes whose
// governed BUILD image (base_images catalog, FR-037) shouldn't also be the
// image application code actually RUNS on — discovered for real, not
// theoretically: golang:1.23.4-alpine3.21 alone scanned CRITICAL-clean, but
// a container built FROM it still carries the whole Go SDK/toolchain
// binaries, which Trivy flags for their own embedded-stdlib CVEs (confirmed
// via a real scan: 22 CRITICAL findings on `usr/local/go/bin/go`,
// `.../pkg/tool/...`, none of which are the application's own code). Only
// the compiled artifact needs to ship; the SDK that built it doesn't.
//
// This is a real gap in FR-037's model as implemented so far: the
// governance catalog has one image per runtime for BUILDING, but no
// governed second entry for RUNNING a compiled/static-output runtime. These
// are hardcoded platform constants for now rather than a catalog lookup —
// a reasonable Module F enhancement for later, not done here.
const (
	goRuntimeStageImage = "alpine:3.21" // confirmed 0 CRITICAL via a real Trivy scan when this was written
)

// GenerateDockerfile implements FR-037: the employee/agent never supplies
// or chooses a base image (deployment.yaml has no field for one) — it's
// always the IT-governed image for the declared runtime, templated here.
//
// v1 convention (documented, not auto-detected): each runtime has ONE
// fixed build recipe rather than arbitrary custom build/start commands,
// since deployment.yaml's schema (docs/promt.md Section 5) has no field for
// those either. This mirrors common buildpack conventions:
//   - go:      go.mod + buildable `.` package  -> `go build` (multi-stage;
//     see runtimeStageImage doc comment for why)
//   - nodejs:  package.json with a "start" script (single-stage: node/npm
//     are needed at runtime to run the app, not just to build it)
//   - python:  requirements.txt + app.py (single-stage, same reasoning as nodejs)
//   - react:   package.json with a "build" script, output in build/ (Create
//     React App convention); multi-stage — the final stage drops the
//     project's own node_modules/devDependencies, keeping only the static
//     build output plus a fresh `serve` install
//   - vue:     same as react, output in dist/ (Vite convention)
//   - nextjs:  package.json with a "build" script, served via `next start`
//     (single-stage — a properly optimized multi-stage Next.js build needs
//     its "standalone output" mode, which isn't implemented here; a
//     documented gap, not a silent one — see the service README)
func GenerateDockerfile(runtime, baseImage string, port int) string {
	if port <= 0 {
		port = domain.DefaultContainerPort
	}
	switch runtime {
	case "go":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /app/server .

FROM %s
COPY --from=build /app/server /app/server
EXPOSE %d
CMD ["/app/server"]
`, baseImage, goRuntimeStageImage, port)
	case "nodejs":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN npm install --omit=dev
EXPOSE %d
CMD ["npm", "start"]
`, baseImage, port)
	case "python":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN if [ -f requirements.txt ]; then pip install --no-cache-dir -r requirements.txt; fi
EXPOSE %d
CMD ["python", "app.py"]
`, baseImage, port)
	case "react":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install && npm run build

FROM %s
WORKDIR /app
COPY --from=build /app/build ./build
RUN npm install -g serve
EXPOSE %d
CMD ["serve", "-s", "build", "-l", "%d"]
`, baseImage, baseImage, port, port)
	case "vue":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install && npm run build

FROM %s
WORKDIR /app
COPY --from=build /app/dist ./dist
RUN npm install -g serve
EXPOSE %d
CMD ["serve", "-s", "dist", "-l", "%d"]
`, baseImage, baseImage, port, port)
	case "nextjs":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN npm install && npm run build
EXPOSE %d
CMD ["npm", "start"]
`, baseImage, port)
	default:
		// Unreachable in practice: the Validated state's stack-compliance
		// check (FR-030) already rejected any runtime not in the catalog
		// before a build could ever be triggered on it.
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
EXPOSE %d
`, baseImage, port)
	}
}
