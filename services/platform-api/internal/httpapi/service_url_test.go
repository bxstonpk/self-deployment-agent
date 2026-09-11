package httpapi

import (
	"context"
	"errors"
	"testing"

	"platform-api/internal/domain"
)

func TestServiceURL_IsThePlatformsOwnAddress(t *testing.T) {
	got := serviceURL("http://localhost:8090", "overtime", "api")
	if want := "http://localhost:8090/run/overtime/api"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := serviceURL("http://localhost:8090/", "overtime", "api"); got != "http://localhost:8090/run/overtime/api" {
		t.Fatalf("a trailing slash on the base produced %q", got)
	}
}

// Nothing may be advertised that wouldn't work: without a name there is no
// route, and a URL is better omitted than guessed.
func TestServiceURL_EmptyWhenAnythingIsMissing(t *testing.T) {
	for _, args := range [][3]string{
		{"", "overtime", "api"},
		{"http://localhost:8090", "", "api"},
		{"http://localhost:8090", "overtime", ""},
	} {
		if got := serviceURL(args[0], args[1], args[2]); got != "" {
			t.Errorf("serviceURL(%q, %q, %q) = %q, want empty", args[0], args[1], args[2], got)
		}
	}
}

func TestServiceURL_EscapesEachSegment(t *testing.T) {
	if got := serviceURL("http://localhost:8090", "over time", "api/v2"); got != "http://localhost:8090/run/over%20time/api%2Fv2" {
		t.Fatalf("got %q", got)
	}
}

// The published port stays: it is real, and it is what someone debugging
// from the host machine needs. Only the URL changes.
func TestWithPublicURLs_ReplacesOnlyTheURL(t *testing.T) {
	containers := map[string]domain.RunningContainer{
		"api":      {ContainerID: "c-api", HostPort: 32770, URL: "http://localhost:32770"},
		"frontend": {ContainerID: "c-front", HostPort: 32771, URL: "http://localhost:32771"},
	}

	out := withPublicURLs(containers, "http://localhost:8090", "overtime")

	if out["api"].URL != "http://localhost:8090/run/overtime/api" || out["frontend"].URL != "http://localhost:8090/run/overtime/frontend" {
		t.Fatalf("unexpected URLs: %+v", out)
	}
	if out["api"].HostPort != 32770 || out["api"].ContainerID != "c-api" {
		t.Fatalf("the rest of the container was changed: %+v", out["api"])
	}
	if containers["api"].URL != "http://localhost:32770" {
		t.Fatal("the caller's own map was modified")
	}
}

type stubNamer struct {
	app domain.Application
	err error
}

func (s stubNamer) GetByID(ctx context.Context, id string) (domain.Application, error) {
	return s.app, s.err
}

func TestApplicationName_MissingApplicationIsNotFatal(t *testing.T) {
	if got := applicationName(context.Background(), stubNamer{app: domain.Application{Name: "overtime"}}, "app-1"); got != "overtime" {
		t.Fatalf("got %q", got)
	}
	if got := applicationName(context.Background(), stubNamer{err: errors.New("gone")}, "app-1"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := applicationName(context.Background(), nil, "app-1"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
