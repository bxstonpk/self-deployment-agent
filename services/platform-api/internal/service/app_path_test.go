package service_test

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// newAppPathService is a ScaleService whose application "leave" has a
// running deployment with the given services.
func newAppPathService(services ...string) *service.ScaleService {
	states := newFakeServiceRuntimeStateRepo()
	for _, name := range services {
		_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{DeploymentID: "dep-1", ServiceName: name})
	}
	apps := &fakeAppByNameRepo{byName: map[string]domain.Application{"leave": {ID: "app-1", Name: "leave"}}}
	deployments := &fakeRunningDeploymentLookup{
		byApp: map[string]domain.Deployment{"app-1": {ID: "dep-1", ApplicationID: "app-1"}},
		byID:  map[string]domain.Deployment{},
	}
	return service.NewScaleService(apps, deployments, states, &fakeScaleEventRepo{}, newFakeStackRepo(), newHealthyRuntime(), newFakeDatabaseService())
}

func TestServiceForPath(t *testing.T) {
	svc := newAppPathService("frontend", "api")
	cases := []struct {
		segment      string
		wantService  string
		wantConsumes bool
	}{
		{"api", "api", true},            // /leave/api/items
		{"frontend", "frontend", true},  // /leave/frontend/ — the old explicit form still works
		{"", "frontend", false},         // /leave/
		{"assets", "frontend", false},   // /leave/assets/index.js
		{"api-docs", "frontend", false}, // a frontend page whose name only starts like a service
	}
	for _, c := range cases {
		got, consumes, err := svc.ServiceForPath(context.Background(), "leave", c.segment)
		if err != nil || got != c.wantService || consumes != c.wantConsumes {
			t.Errorf("segment %q: got (%q, %v, %v), want (%q, %v, nil)", c.segment, got, consumes, err, c.wantService, c.wantConsumes)
		}
	}
}

func TestServiceForPath_SingleServiceApplicationIsItsOwnDefault(t *testing.T) {
	svc := newAppPathService("web")
	if got, consumes, err := svc.ServiceForPath(context.Background(), "leave", "items"); err != nil || got != "web" || consumes {
		t.Fatalf("got (%q, %v, %v), want (\"web\", false, nil)", got, consumes, err)
	}
}

func TestServiceForPath_NoDefaultWithoutAFrontend(t *testing.T) {
	svc := newAppPathService("api", "worker")
	if _, _, err := svc.ServiceForPath(context.Background(), "leave", ""); !errors.Is(err, domain.ErrServiceStateNotFound) {
		t.Fatalf("expected ErrServiceStateNotFound, got %v", err)
	}
	if got, _, err := svc.ServiceForPath(context.Background(), "leave", "worker"); err != nil || got != "worker" {
		t.Fatalf("a named service is still reachable: got (%q, %v)", got, err)
	}
}

func TestServiceForPath_UnknownApplication(t *testing.T) {
	svc := newAppPathService("frontend")
	if _, _, err := svc.ServiceForPath(context.Background(), "nope", ""); !errors.Is(err, domain.ErrApplicationNotFound) {
		t.Fatalf("expected ErrApplicationNotFound, got %v", err)
	}
}

// The gateway sends every path that isn't one of its own or the Portal's to
// the applications. An application named after one of those would be
// registered and deployed, then never reached — so each must be reserved.
func TestReservedNames_CoverEveryGatewayPath(t *testing.T) {
	conf, err := os.ReadFile("../../../../deploy/gateway/nginx.conf")
	if err != nil {
		t.Fatalf("read the gateway's nginx.conf: %v", err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`location \^~ /([a-z-]+)/`).FindAllStringSubmatch(string(conf), -1) {
		names = append(names, m[1])
	}
	portal := regexp.MustCompile(`location ~ \^/\(([a-z|-]+)\)\(/\|\$\)`).FindStringSubmatch(string(conf))
	if portal == nil {
		t.Fatal("couldn't find the Portal's pages in nginx.conf")
	}
	names = append(names, strings.Split(portal[1], "|")...)
	if len(names) < 10 {
		t.Fatalf("found only %v in nginx.conf; has its layout changed?", names)
	}
	for _, name := range names {
		if !service.ReservedApplicationName(name) {
			t.Errorf("the gateway routes /%s/ itself, but %q isn't a reserved application name", name, name)
		}
	}
}

func TestDefaultService(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"api", "frontend"}, "frontend"},
		{[]string{"web"}, "web"},
		{[]string{"api", "worker"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := domain.DefaultService(c.names); got != c.want {
			t.Errorf("DefaultService(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}
