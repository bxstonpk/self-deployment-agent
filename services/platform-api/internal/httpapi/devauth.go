package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"platform-api/internal/domain"
)

// DevHeaderAuthenticator is a TEMPORARY stand-in for real SSO/IdP
// authentication (DEC-001, docs/17_Decision_Log.md — still Open). It trusts
// two request headers outright and must never be reachable from a
// production/staging deployment; DevOnlyGuard middleware enforces that by
// refusing to start unless PLATFORM_ENV=dev.
//
// Callers identify themselves with:
//
//	X-Dev-User-Email: alice@example.com
//	X-Dev-User-Name:  Alice Employee   (optional, defaults to the email local-part)
//	X-Dev-Department: Engineering      (optional, defaults to "Unassigned")
type DevHeaderAuthenticator struct {
	users interface {
		GetOrCreateByEmail(ctx context.Context, email, fullName, departmentID string) (domain.User, error)
	}
	departments interface {
		GetOrCreateByName(ctx context.Context, name string) (domain.Department, error)
	}
}

func NewDevHeaderAuthenticator(
	users interface {
		GetOrCreateByEmail(ctx context.Context, email, fullName, departmentID string) (domain.User, error)
	},
	departments interface {
		GetOrCreateByName(ctx context.Context, name string) (domain.Department, error)
	},
) *DevHeaderAuthenticator {
	return &DevHeaderAuthenticator{users: users, departments: departments}
}

func (a *DevHeaderAuthenticator) Authenticate(r *http.Request) (domain.User, error) {
	email := strings.TrimSpace(r.Header.Get("X-Dev-User-Email"))
	if email == "" {
		return domain.User{}, errors.New("missing X-Dev-User-Email header (dev-mode auth stub — see DEC-001)")
	}

	fullName := strings.TrimSpace(r.Header.Get("X-Dev-User-Name"))
	if fullName == "" {
		fullName = strings.SplitN(email, "@", 2)[0]
	}

	deptName := strings.TrimSpace(r.Header.Get("X-Dev-Department"))
	if deptName == "" {
		deptName = "Unassigned"
	}

	dept, err := a.departments.GetOrCreateByName(r.Context(), deptName)
	if err != nil {
		return domain.User{}, err
	}

	return a.users.GetOrCreateByEmail(r.Context(), email, fullName, dept.ID)
}

// DevOnlyGuard refuses to serve any request unless the service was started
// with PLATFORM_ENV=dev, so DevHeaderAuthenticator can never accidentally
// authenticate real traffic.
func DevOnlyGuard(platformEnv string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if platformEnv != "dev" {
				writeError(w, http.StatusServiceUnavailable, "dev_auth_disabled",
					"dev-mode header authentication is disabled outside PLATFORM_ENV=dev")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
