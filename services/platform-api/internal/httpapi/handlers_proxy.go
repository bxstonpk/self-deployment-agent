// ProxyHandler serves the stable, public-facing URL for a Running
// application (Module L / FR-053's "ingress/routing layer"): a request to
// /run/{appName}/{serviceName}/* is resolved to the service's current
// container, cold-starting it first if it's scaled to zero. This is what
// makes scale-to-zero possible at all: the raw Docker-published host port
// changes every time a service scales 0->1, so nothing can hand that out
// as "the" URL — this route is the one address that stays constant across
// scale events. See docs/10_System_Architecture.md's note that Docker
// stands in for the eventual K3s+Knative ingress+activator here.
//
// Deliberately NOT behind DevOnlyGuard/RequireAuth: this is the deployed
// APPLICATION's own traffic path, not a platform management endpoint — a
// real end user hitting a deployed internal tool shouldn't need platform
// employee credentials to reach it. Any auth the application itself wants
// is the application's own concern.
package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"platform-api/internal/domain"
)

type ScaleResolver interface {
	EnsureRunningByName(ctx context.Context, appName, serviceName string) (hostPort int, err error)
}

type ProxyHandler struct {
	scale  ScaleResolver
	client *http.Client
}

func NewProxyHandler(scale ScaleResolver) *ProxyHandler {
	return &ProxyHandler{scale: scale, client: &http.Client{Timeout: 30 * time.Second}}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appName, serviceName, remainder, ok := parseRunPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "expected /run/{application}/{service}/...")
		return
	}

	hostPort, err := h.scale.EnsureRunningByName(r.Context(), appName, serviceName)
	if err != nil {
		writeProxyError(w, err)
		return
	}

	// From inside platform-api's own container, "localhost" would mean
	// itself — same reasoning as the deploy pipeline's health check (see
	// deploy_service.go). host.docker.internal reaches the sibling
	// container that was just ensured running.
	target := "http://host.docker.internal:" + strconv.Itoa(hostPort) + remainder
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy_error", "failed to build upstream request")
		return
	}
	proxyReq.Header = r.Header.Clone()

	resp, err := h.client.Do(proxyReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_unreachable", "the application did not respond")
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// parseRunPath splits "/run/{app}/{service}/rest/of/path" into its parts.
// The remainder (including its leading slash, or "/" if none) is forwarded
// to the upstream service as-is.
func parseRunPath(path string) (appName, serviceName, remainder string, ok bool) {
	const prefix = "/run/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	remainder = "/"
	if len(parts) == 3 {
		remainder = "/" + parts[2]
	}
	return parts[0], parts[1], remainder, true
}

func writeProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrNoRunningDeployment), errors.Is(err, domain.ErrServiceStateNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no running application/service at this address")
	default:
		writeError(w, http.StatusBadGateway, "cold_start_failed", "failed to start the application: "+err.Error())
	}
}
