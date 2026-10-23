package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
)

func TestNeedsTrailingSlash(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/run/leave/frontend", true},
		{http.MethodHead, "/run/leave/frontend", true},
		{http.MethodGet, "/run/leave/frontend/", false},
		{http.MethodGet, "/run/leave/frontend/assets/app.js", false},
		{http.MethodGet, "/run/leave/api/items", false},
		// An API call to the bare address is answered, not redirected: a
		// redirected POST is one many clients won't follow.
		{http.MethodPost, "/run/leave/api", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := needsTrailingSlash(r); got != c.want {
			t.Errorf("%s %s: needsTrailingSlash = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestProxyRedirectsBareServiceAddressKeepingQuery(t *testing.T) {
	h := NewProxyHandler(nil, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/run/leave/frontend?tab=2", nil))
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/run/leave/frontend/?tab=2" {
		t.Fatalf("unexpected Location %q", loc)
	}
}

// fakeAppScale resolves application "leave", which has a frontend and an
// api, to one upstream server for both.
type fakeAppScale struct {
	port     int
	resolved []string // service names EnsureRunningByName was asked for
}

func (f *fakeAppScale) ServiceForPath(ctx context.Context, appName, firstSegment string) (string, bool, error) {
	if appName != "leave" {
		return "", false, domain.ErrApplicationNotFound
	}
	if firstSegment == "api" || firstSegment == "frontend" {
		return firstSegment, true, nil
	}
	return "frontend", false, nil
}

func (f *fakeAppScale) EnsureRunningByName(ctx context.Context, appName, serviceName string) (domain.ResolvedService, error) {
	f.resolved = append(f.resolved, serviceName)
	return domain.ResolvedService{ApplicationID: "app-1", ServiceName: serviceName, HostPort: f.port}, nil
}

type nopTraffic struct{}

func (nopTraffic) Record(string, string, domain.Environment, int, time.Duration) {}

func newAppProxy(t *testing.T) (*ProxyHandler, *fakeAppScale) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.RequestURI())
	}))
	t.Cleanup(upstream.Close)
	port, _ := strconv.Atoi(upstream.URL[strings.LastIndex(upstream.URL, ":")+1:])
	scale := &fakeAppScale{port: port}
	h := NewProxyHandler(scale, nopTraffic{})
	h.upstreamHost = "127.0.0.1"
	return h, scale
}

func TestServeApp_RoutesByApplicationName(t *testing.T) {
	cases := []struct {
		method, path, wantService, wantUpstream string
	}{
		{http.MethodGet, "/app/leave/", "frontend", "/"},
		{http.MethodGet, "/app/leave/assets/index.js", "frontend", "/assets/index.js"},
		{http.MethodGet, "/app/leave/api/items?x=1", "api", "/items?x=1"},
		{http.MethodGet, "/app/leave/api/", "api", "/"},
		{http.MethodPost, "/app/leave/api", "api", "/"}, // not redirected: see needsTrailingSlash
		{http.MethodGet, "/app/leave/frontend/", "frontend", "/"},
	}
	for _, c := range cases {
		h, scale := newAppProxy(t)
		w := httptest.NewRecorder()
		h.ServeApp(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: status %d (%s)", c.method, c.path, w.Code, w.Body.String())
			continue
		}
		if len(scale.resolved) != 1 || scale.resolved[0] != c.wantService {
			t.Errorf("%s %s: routed to %v, want %q", c.method, c.path, scale.resolved, c.wantService)
		}
		if got := w.Body.String(); got != c.wantUpstream {
			t.Errorf("%s %s: upstream saw %q, want %q", c.method, c.path, got, c.wantUpstream)
		}
	}
}

func TestServeApp_RedirectsBareAddresses(t *testing.T) {
	cases := map[string]string{
		"/app/leave":          "/leave/",
		"/app/leave?tab=2":    "/leave/?tab=2",
		"/app/leave/api":      "/leave/api/",
		"/app/leave/frontend": "/leave/frontend/",
	}
	for path, want := range cases {
		h, _ := newAppProxy(t)
		w := httptest.NewRecorder()
		h.ServeApp(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != want {
			t.Errorf("GET %s: got %d -> %q, want 308 -> %q", path, w.Code, w.Header().Get("Location"), want)
		}
	}
}

func TestServeApp_UnknownApplicationIs404(t *testing.T) {
	h, _ := newAppProxy(t)
	w := httptest.NewRecorder()
	h.ServeApp(w, httptest.NewRequest(http.MethodGet, "/app/nope/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
