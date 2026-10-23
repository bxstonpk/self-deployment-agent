package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"platform-api/internal/domain"
)

// DevHeaderAuthenticator is the self-declared identity the platform used
// before it authenticated anyone (DEC-001/DEC-003, revised 2026-10-23,
// docs/17_Decision_Log.md). It trusts request headers outright, so it
// now runs only under PLATFORM_AUTH_MODE=dev-header — for local test
// scripts — and config.Load refuses that mode unless PLATFORM_ENV=dev;
// DevOnlyGuard enforces the same per request. TokenAuthenticator
// (tokenauth.go) is the real one.
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
