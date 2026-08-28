package buildengine

import "fmt"

// GenerateDockerfile implements FR-037: the employee/agent never supplies
// or chooses a base image (deployment.yaml has no field for one) — it's
// always the IT-governed image for the declared runtime, templated here.
//
// v1 convention (documented, not auto-detected): each runtime has ONE
// fixed build recipe rather than arbitrary custom build/start commands,
// since deployment.yaml's schema (docs/promt.md Section 5) has no field for
// those either. This mirrors common buildpack conventions:
//   - go:      go.mod + buildable `.` package  -> `go build`
//   - nodejs:  package.json with a "start" script
//   - python:  requirements.txt + app.py
//   - react:   package.json with a "build" script, output in build/ (Create React App convention)
//   - vue:     package.json with a "build" script, output in dist/ (Vite convention)
//   - nextjs:  package.json with a "build" script, served via `next start`
func GenerateDockerfile(runtime, baseImage string, port int) string {
	if port <= 0 {
		port = 3000
	}
	switch runtime {
	case "go":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o /app/server .
EXPOSE %d
CMD ["/app/server"]
`, baseImage, port)
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
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN npm install && npm run build
RUN npm install -g serve
EXPOSE %d
CMD ["serve", "-s", "build", "-l", "%d"]
`, baseImage, port, port)
	case "vue":
		return fmt.Sprintf(`FROM %s
WORKDIR /app
COPY . .
RUN npm install && npm run build
RUN npm install -g serve
EXPOSE %d
CMD ["serve", "-s", "dist", "-l", "%d"]
`, baseImage, port, port)
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
