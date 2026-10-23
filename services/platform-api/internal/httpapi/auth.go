package httpapi

import (
	"context"
	"net/http"

	"platform-api/internal/domain"
)

// Authenticator resolves an inbound request to the caller's identity.
// This seam is what let real authentication (TokenAuthenticator, per
// DEC-001/DEC-003 revised 2026-10-23, docs/17_Decision_Log.md) replace the
// self-declared header identity without touching any handler — see NFR-051 in
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
			if ca, ok := authenticator.(CredentialAuthenticator); ok {
				user, cred, err := ca.AuthenticateCredential(r)
				if err != nil {
					writeUnauthorized(w, err)
					return
				}
				next.ServeHTTP(w, r.WithContext(withCredential(withUser(r.Context(), user), cred)))
				return
			}
			user, err := authenticator.Authenticate(r)
			if err != nil {
				writeUnauthorized(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
		})
	}
}

// writeUnauthorized answers 401 with a challenge, so a client can tell
// "sign in" apart from "you may not".
func writeUnauthorized(w http.ResponseWriter, err error) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="company-platform"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
}
