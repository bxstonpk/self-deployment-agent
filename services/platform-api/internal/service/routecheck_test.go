package service_test

import (
	"context"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

const hashRoutedApp = `import { HashRouter } from 'react-router-dom'
// import { createHashRouter } from 'react-router-dom'  (commented out: not counted)
export const App = () => <HashRouter><Pages /></HashRouter>
`

func TestScanHashRouting_FindsTheHashModesInTheApplicationsOwnSource(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"frontend/src/main.jsx":                       hashRoutedApp,
		"frontend/src/router.ts":                      "const router = createHashRouter(routes)\n",
		"web/src/router.js":                           "import { createRouter, createWebHashHistory } from 'vue-router'\n",
		"frontend/node_modules/react-router/index.js": "export function HashRouter() {}\n",
		"frontend/build/assets/index.js":              "HashRouter\n",
		"frontend/src/ok.jsx":                         "import { BrowserRouter } from 'react-router-dom'\n",
		"frontend/README.md":                          "HashRouter is mentioned here\n",
		"api/main.go":                                 "// nothing to see\n",
	})
	found, err := service.ScanHashRouting(archive)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range found {
		got[f.File+":"+f.API] = true
	}
	want := map[string]bool{
		"frontend/src/main.jsx:HashRouter":       true,
		"frontend/src/router.ts:createHashRouter": true,
		"web/src/router.js:createWebHashHistory":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing %s (found %v)", k, got)
		}
	}
	for _, f := range found {
		if f.File == "frontend/src/main.jsx" && f.Line == 2 {
			t.Error("a commented-out line counted")
		}
	}
}

func TestBuild_RefusesHashRouting(t *testing.T) {
	g := newGovernance(t, false)
	ctx := context.Background()
	src := map[string]string{"frontend/package.json": `{}`, "frontend/src/main.jsx": hashRoutedApp}

	b, err := g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != domain.BuildFailed || b.ErrorDetail == nil {
		t.Fatalf("want a failed build, got %s", b.Status)
	}
	detail := *b.ErrorDetail
	if !strings.Contains(detail, "HashRouter in frontend/src/main.jsx:1") || !strings.Contains(detail, "BrowserRouter") ||
		!strings.Contains(detail, "?q=") {
		t.Fatalf("the detail should say where and what to do instead: %s", detail)
	}
	if g.engine.called {
		t.Fatal("nothing is built from a source that fails the check")
	}

	check, err := g.svc.CheckSource(ctx, govOwner, "app-1", tarGz(t, src))
	if err != nil {
		t.Fatal(err)
	}
	// The import (line 1) and its use (line 3); the commented line 2 isn't.
	if len(check.HashRouting) != 2 || check.HashRouting[0].Line != 1 || check.HashRouting[1].Line != 3 {
		t.Fatalf("check_project should report it too: %+v", check.HashRouting)
	}
}

func TestBuild_AnApprovedExceptionAllowsHashRouting(t *testing.T) {
	g := newGovernance(t, false)
	ctx := context.Background()
	src := map[string]string{"frontend/package.json": `{}`, "frontend/src/main.jsx": hashRoutedApp}
	e, _, err := g.svc.RequestException(ctx, govOwner, "app-1", "other", domain.HashRoutingSubject, "an embedded kiosk page")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	b, err := g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != domain.BuildSucceeded || !g.engine.called {
		t.Fatalf("approved: want built, got %s", b.Status)
	}
}
