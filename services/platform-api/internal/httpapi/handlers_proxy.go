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
	EnsureRunningByName(ctx context.Context, appName, serviceName string) (domain.ResolvedService, error)
	ServiceForPath(ctx context.Context, appName, firstSegment string) (serviceName string, consumesSegment bool, err error)
}

// TrafficRecorder is Module T's traffic half (FR-090): every request to a
// deployed application passes through here, so the platform can count
// requests, errors and latency without the application instrumenting
// anything. Recording happens in memory, on the request's own goroutine.
type TrafficRecorder interface {
	Record(applicationID, service string, environment domain.Environment, status int, latency time.Duration)
}

type ProxyHandler struct {
	scale   ScaleResolver
	traffic TrafficRecorder
	client  *http.Client
	// upstreamHost is where application containers' published ports are
	// reached from inside platform-api's container. A field only so tests
	// can point it at a local server.
	upstreamHost string
}

func NewProxyHandler(scale ScaleResolver, traffic TrafficRecorder) *ProxyHandler {
	return &ProxyHandler{scale: scale, traffic: traffic, client: &http.Client{Timeout: 30 * time.Second}, upstreamHost: "host.docker.internal"}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	appName, serviceName, remainder, ok := parseRunPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "expected /run/{application}/{service}/...")
		return
	}
	if needsTrailingSlash(r) {
		target := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
		return
	}

	h.forward(w, r, appName, serviceName, remainder)
}

// ServeApp serves the short application address, /{app}/... — what the
// gateway turns every path that isn't the Portal's, /api/, /run/ or
// /downloads/ into (as /app/{app}/...; see deploy/gateway/nginx.conf).
// /{app}/ is the application's frontend and /{app}/{service}/ any other of
// its services: see ScaleService.ServiceForPath. The bare /{app} redirects
// to /{app}/ for the same reason needsTrailingSlash gives, as does a bare
// /{app}/{service}.
func (h *ProxyHandler) ServeApp(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, appPathPrefix)
	appName, after, hasSlash := strings.Cut(rest, "/")
	if appName == "" || rest == r.URL.Path {
		writeError(w, http.StatusNotFound, "not_found", "no application at this address")
		return
	}
	isPage := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !hasSlash && isPage {
		redirectWithQuery(w, r, "/"+appName+"/")
		return
	}
	first, tail, segmentHasSlash := strings.Cut(after, "/")
	serviceName, consumes, err := h.scale.ServiceForPath(r.Context(), appName, first)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	remainder := "/" + after
	if consumes {
		if !segmentHasSlash && isPage {
			redirectWithQuery(w, r, "/"+appName+"/"+first+"/")
			return
		}
		remainder = "/" + tail
	}
	h.forward(w, r, appName, serviceName, remainder)
}

// appPathPrefix is where the gateway sends the short application address.
const appPathPrefix = "/app/"

// redirectWithQuery sends a permanent redirect to the public path target,
// keeping the query string.
func redirectWithQuery(w http.ResponseWriter, r *http.Request, target string) {
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusPermanentRedirect)
}

// forward sends one request on to the service's current container,
// cold-starting it first if it's scaled to zero.
func (h *ProxyHandler) forward(w http.ResponseWriter, r *http.Request, appName, serviceName, remainder string) {
	resolved, err := h.scale.EnsureRunningByName(r.Context(), appName, serviceName)
	if err != nil {
		writeProxyError(w, err)
		return
	}

	// From inside platform-api's own container, "localhost" would mean
	// itself — same reasoning as the deploy pipeline's health check (see
	// deploy_service.go). host.docker.internal reaches the sibling
	// container that was just ensured running.
	target := "http://" + h.upstreamHost + ":" + strconv.Itoa(resolved.HostPort) + remainder
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy_error", "failed to build upstream request")
		return
	}
	proxyReq.Header = r.Header.Clone()

	// Timed around the application's own answer, not the cold start before
	// it: waiting for a container to start is a scale event (FR-056), not
	// the application being slow.
	started := time.Now()
	resp, err := h.client.Do(proxyReq)
	if err != nil {
		h.traffic.Record(resolved.ApplicationID, resolved.ServiceName, resolved.Environment, http.StatusBadGateway, time.Since(started))
		writeError(w, http.StatusBadGateway, "upstream_unreachable", "the application did not respond")
		return
	}
	h.traffic.Record(resolved.ApplicationID, resolved.ServiceName, resolved.Environment, resp.StatusCode, time.Since(started))
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// needsTrailingSlash reports a page request for a service's bare address,
// /run/{app}/{service} with no slash after it. A browser resolves a page's
// relative links against the URL up to its last slash, so a frontend built
// with relative asset paths — the only kind that works under a /run/...
// prefix it can't know in advance — would load its assets from
// /run/{app}/ instead of /run/{app}/{service}/. Redirecting once to the
// slashed address fixes every relative URL on the page. Only GET and HEAD:
// an API client posting to the bare address gets what it asked for.
func needsTrailingSlash(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, "/run/")
	return !strings.HasSuffix(rest, "/") && strings.Count(rest, "/") == 1
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
