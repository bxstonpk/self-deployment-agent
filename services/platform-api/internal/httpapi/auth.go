package httpapi

import (
	"context"
	"net/http"

	"platform-api/internal/domain"
)

// Authenticator resolves an inbound request to the caller's identity.
// This seam exists specifically so the auth mechanism can be swapped once
// DEC-001 (Identity Provider / SSO integration, docs/17_Decision_Log.md) is
// resolved, without touching any handler — see NFR-051 in
// docs/03_Non_Functional_Requirements.md ("frontend/backend implementation
// independence") and the same "never trust a single hardcoded mechanism"
// spirit applied to the identity layer.
type Authenticator interface {
	Authenticate(r *http.Request) (domain.User, error)
}

type ctxKey int

const userCtxKey ctxKey = iota

func withUser(ctx context.Context, u domain.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFromContext returns the authenticated caller. Handlers behind
// RequireAuth can rely on this always being present.
func UserFromContext(ctx context.Context) (domain.User, bool) {
	u, ok := ctx.Value(userCtxKey).(domain.User)
	return u, ok
}

func RequireAuth(authenticator Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := authenticator.Authenticate(r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
				return
			}
			next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
		})
	}
}
