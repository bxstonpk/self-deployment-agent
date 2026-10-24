package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"platform-api/internal/domain"
)

type fakeDevLabels map[string]string

func (f fakeDevLabels) DevCopyLabel(ctx context.Context, applicationID string) (string, bool) {
	of, ok := f[applicationID]
	return of, ok
}

type envScale struct {
	port int
	env  domain.Environment
}

func (s envScale) EnsureRunningByName(ctx context.Context, appName, serviceName string) (domain.ResolvedService, error) {
	return domain.ResolvedService{ApplicationID: "a1", ServiceName: serviceName, HostPort: s.port, Environment: s.env}, nil
}

func (s envScale) ServiceForPath(ctx context.Context, appName, first string) (string, bool, error) {
	return "frontend", false, nil
}

func bannerFixture(t *testing.T, env domain.Environment, contentType string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var sawEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, "<html><body><h1>ลาพักร้อน</h1></BODY></html>")
	}))
	t.Cleanup(upstream.Close)
	port, _ := strconv.Atoi(upstream.URL[strings.LastIndex(upstream.URL, ":")+1:])
	h := NewProxyHandler(envScale{port: port, env: env}, nopTraffic{})
	h.upstreamHost = "127.0.0.1"
	h.SetDevCopies(fakeDevLabels{"a1": "leave"})
	req := httptest.NewRequest(http.MethodGet, "/run/leave-dev/frontend/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if env == domain.EnvironmentDev && sawEncoding != "identity" {
		t.Fatalf("a dev copy page is asked for uncompressed, got %q", sawEncoding)
	}
	return rec, req
}

func TestDevCopyPagesCarryTheBannerBeforeBodyEnds(t *testing.T) {
	rec, _ := bannerFixture(t, domain.EnvironmentDev, "text/html; charset=utf-8")
	body := rec.Body.String()
	if !strings.Contains(body, "DEV — สำเนาสำหรับทดลองของ leave ไม่ใช่ระบบจริง") {
		t.Fatalf("banner naming what it is a copy of: %s", body)
	}
	if !strings.Contains(body, "</div></BODY>") || !strings.Contains(body, "<h1>ลาพักร้อน</h1>") {
		t.Fatalf("inserted before </body>, the page intact: %s", body)
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length matches: %s vs %d", rec.Header().Get("Content-Length"), len(body))
	}
}

func TestOnlyDevCopyHTMLGetsTheBanner(t *testing.T) {
	if rec, _ := bannerFixture(t, domain.EnvironmentProduction, "text/html"); strings.Contains(rec.Body.String(), "DEV —") {
		t.Fatal("not on production")
	}
	if rec, _ := bannerFixture(t, domain.EnvironmentDev, "application/json"); strings.Contains(rec.Body.String(), "DEV —") {
		t.Fatal("not in an API's answers")
	}
}
