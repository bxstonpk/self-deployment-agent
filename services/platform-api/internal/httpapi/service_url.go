// The address an application is served on is the platform's own, not its
// container's: /run/{app}/{service}, the one route that survives the
// container being replaced (Module L, FR-053 — see handlers_proxy.go).
//
// Every deployment response used to carry `http://localhost:<hostPort>`,
// the port Docker published for that particular container. It broke the
// moment the container was replaced — a restart, a scale-to-zero cold
// start, a redeploy — because the next container gets a different port. It
// also sent traffic straight past the proxy, which is what cold-starts a
// scaled-to-zero service (FR-053) and what counts requests, errors and
// latency (Module T, FR-090): an employee following that URL got no
// cold start and left no trace in their own metrics.
//
// The published port is still reported as `host_port`, which is honest
// and useful when debugging from the host machine itself.
package httpapi

import (
	"context"
	"net/url"
	"strings"

	"platform-api/internal/domain"
)

// ApplicationNamer resolves the name the proxy route is keyed by. The
// deployment record carries only the application's id.
type ApplicationNamer interface {
	GetByID(ctx context.Context, id string) (domain.Application, error)
}

// serviceURL is the stable public address of one service of one
// application. Empty when the application's name isn't known: a response
// that says nothing beats one advertising an address that won't work.
func serviceURL(base, appName, serviceName string) string {
	if base == "" || appName == "" || serviceName == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/run/" + url.PathEscape(appName) + "/" + url.PathEscape(serviceName)
}

// withPublicURLs returns the containers with each URL replaced by the
// stable one. Everything else about them is left alone.
func withPublicURLs(containers map[string]domain.RunningContainer, base, appName string) map[string]domain.RunningContainer {
	if len(containers) == 0 {
		return containers
	}
	out := make(map[string]domain.RunningContainer, len(containers))
	for serviceName, c := range containers {
		c.URL = serviceURL(base, appName, serviceName)
		out[serviceName] = c
	}
	return out
}

// applicationName is best effort: a response is still worth returning
// without the URL, and every path here already had the deployment.
func applicationName(ctx context.Context, apps ApplicationNamer, applicationID string) string {
	if apps == nil {
		return ""
	}
	app, err := apps.GetByID(ctx, applicationID)
	if err != nil {
		return ""
	}
	return app.Name
}
