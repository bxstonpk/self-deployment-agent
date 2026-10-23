package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
