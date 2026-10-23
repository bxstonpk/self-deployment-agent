package httpapi

import (
	"context"
	"net/http"
	"strings"

	"platform-api/internal/domain"
)

// TokenVerifier is the part of service.AuthService the authenticator
// needs.
type TokenVerifier interface {
	Authenticate(ctx context.Context, token string) (domain.User, domain.Credential, error)
}

// TokenAuthenticator is the platform's real authentication (DEC-001/
// DEC-003 revised 2026-10-23): `Authorization: Bearer <token>`, where the
// token is a Portal session or a machine's API key. Nothing about the
// caller's identity is taken from the request except that token.
type TokenAuthenticator struct {
	verifier TokenVerifier
}

func NewTokenAuthenticator(verifier TokenVerifier) *TokenAuthenticator {
	return &TokenAuthenticator{verifier: verifier}
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (a *TokenAuthenticator) Authenticate(r *http.Request) (domain.User, error) {
	user, _, err := a.AuthenticateCredential(r)
	return user, err
}

func (a *TokenAuthenticator) AuthenticateCredential(r *http.Request) (domain.User, domain.Credential, error) {
	token := bearerToken(r)
	if token == "" {
		return domain.User{}, domain.Credential{}, domain.ErrInvalidToken
	}
	return a.verifier.Authenticate(r.Context(), token)
}

// CredentialAuthenticator is implemented by an Authenticator that knows
// which credential a request used; RequireAuth then puts it in the
// request context, which is how session-only endpoints tell a person
// signed in to the Portal from a machine's API key.
type CredentialAuthenticator interface {
	AuthenticateCredential(r *http.Request) (domain.User, domain.Credential, error)
}

const credentialCtxKey ctxKey = iota + 100

func withCredential(ctx context.Context, c domain.Credential) context.Context {
	return context.WithValue(ctx, credentialCtxKey, c)
}

// CredentialFromContext returns the credential the request authenticated
// with. Absent under the dev-header authenticator, which has none.
func CredentialFromContext(ctx context.Context) (domain.Credential, bool) {
	c, ok := ctx.Value(credentialCtxKey).(domain.Credential)
	return c, ok
}
