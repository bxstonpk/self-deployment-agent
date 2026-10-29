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
// The same lesson repeated for static frontends: a React build served by
// `serve` on the Node image shipped npm too, and the first real deploy of
// the LAN starter templates was blocked by a CRITICAL in npm's own bundled
// `tar` (CVE-2026-59873) — again nothing the application wrote or ran.
// Static output now ships on nginx alone.
//
// This is a real gap in FR-037's model as implemented so far: the
// governance catalog has one image per runtime for BUILDING, but no
// governed second entry for RUNNING a compiled/static-output runtime. These
// are hardcoded platform constants for now rather than a catalog lookup —
// a reasonable Module F enhancement for later, not done here.
const (
	goRuntimeStageImage     = "alpine:3.21"       // confirmed 0 CRITICAL via a real Trivy scan when this was written
	staticRuntimeStageImage = "nginx:1.30-alpine" // confirmed 0 CRITICAL via a real Trivy scan when this was written
)

// removeNodePackageManagers strips npm, npx and corepack from a Node
// runtime stage. Running an application needs only `node`; the package
// managers are build tools, and they carry their own dependency trees
// into every image — which is exactly where the CRITICAL that blocked the
// starter templates was.
const removeNodePackageManagers = `RUN rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack \
    /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack`

// writeStartScript turns package.json's "start" script into
// /app/.platform-start.sh at build time, so the runtime stage can run it
// without npm. `exec` makes the application the container's main process,
// so it receives the platform's stop signal directly. A missing start
// script fails the build — a source problem the employee can fix — rather
// than producing an image that can't start.
const writeStartScript = `RUN node -e "const s = (require('./package.json').scripts || {}).start; if (!s) { console.error('package.json has no start script'); process.exit(1); } require('fs').writeFileSync('.platform-start.sh', 'exec ' + s + '\n');"`

// nodeRuntimeStage is the final stage every Node-run service shares:
// the build output and production dependencies, no package manager, and
// node_modules/.bin on PATH — what `npm start` would have provided.
func nodeRuntimeStage(baseImage string, port int) string {
	return fmt.Sprintf(`FROM %s
%s
WORKDIR /app
COPY --from=build /app ./
ENV NODE_ENV=production PATH=/app/node_modules/.bin:$PATH
EXPOSE %d
CMD ["sh", "/app/.platform-start.sh"]
`, baseImage, removeNodePackageManagers, port)
}

// upgradeAlpinePackages brings an Alpine image's OS packages up to the
// fixes Alpine has already published. Found for real on 2026-10-29:
// nginx:1.30-alpine (built 2026-10-21) carried brotli-libs 1.2.0-r1, and
// once Trivy's database listed CVE-2026-111497 (CRITICAL, fixed in
// 1.2.1-r0) every React and Vue build was refused, with no newer nginx
// image on Docker Hub yet. The build needs Alpine's package mirror.
const upgradeAlpinePackages = `RUN apk upgrade --no-cache`

// staticRuntimeStage serves a frontend's build output from nginx, falling
// back to index.html for client-side routes. The application is served
// under /run/{app}/{service}/, which the proxy strips, so nginx sees
// ordinary root-relative paths.
func staticRuntimeStage(outputDir string, port int) string {
	return fmt.Sprintf(`FROM %s
`+upgradeAlpinePackages+`
COPY --from=build /app/%s /usr/share/nginx/html
RUN printf 'server {\n  listen %d;\n  root /usr/share/nginx/html;\n  location / {\n    try_files $uri $uri/ /index.html;\n  }\n}\n' > /etc/nginx/conf.d/default.conf
EXPOSE %d
`, staticRuntimeStageImage, outputDir, port, port)
}

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
//   - nodejs:  package.json with a "start" script; production dependencies
//     only, run without npm (see nodeRuntimeStage)
//   - typescript: package.json with a "build" script (tsc, or any compiler)
//     and a "start" script that runs the compiled output; devDependencies
//     are pruned after the build, and it runs without npm like nodejs
//   - python:  requirements.txt + app.py (single-stage: the interpreter is
//     needed at runtime anyway)
//   - react:   package.json with a "build" script, output in build/ (Create
//     React App's default; Vite with build.outDir 'build'), served by
//     nginx — the final stage holds the static output and nothing else
//   - vue:     same as react, output in dist/ (Vite convention)
//   - nextjs:  package.json with a "build" script, served via its "start"
//     script (`next start`) without npm. Ships all of node_modules — a
//     properly optimized Next.js image needs its "standalone output" mode,
//     which isn't implemented here; a documented gap, not a silent one —
//     see the service README
//
// Every "start" script runs through sh without npm, so it must not itself
// call npm (`npm run ...`); `node file.js` or a dependency's binary works.
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
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install --omit=dev
%s

`, baseImage, writeStartScript) + nodeRuntimeStage(baseImage, port)
	case "typescript":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install && npm run build && npm prune --omit=dev
%s

`, baseImage, writeStartScript) + nodeRuntimeStage(baseImage, port)
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

`, baseImage) + staticRuntimeStage("build", port)
	case "vue":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install && npm run build

`, baseImage) + staticRuntimeStage("dist", port)
	case "nextjs":
		return fmt.Sprintf(`FROM %s AS build
WORKDIR /app
COPY . .
RUN npm install && npm run build
%s

`, baseImage, writeStartScript) + nodeRuntimeStage(baseImage, port)
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
