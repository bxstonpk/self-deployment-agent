package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type RouterConfig struct {
	Authenticator Authenticator
	Applications  *ApplicationHandler
	Validation    *ValidationHandler
	Stacks        *StackHandler
	// PlatformEnv gates DevOnlyGuard. The only Authenticator implementation
	// today is DevHeaderAuthenticator, so this is always enforced until a
	// real one lands per DEC-001 (docs/17_Decision_Log.md).
	PlatformEnv string
}

func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/applications", func(r chi.Router) {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
		r.Use(RequireAuth(cfg.Authenticator))
		r.Post("/", cfg.Applications.Register)
		r.Get("/", cfg.Applications.List)
		r.Get("/{id}", cfg.Applications.Get)
		r.Patch("/{id}", cfg.Applications.UpdateMetadata)
		r.Get("/{id}/owners", cfg.Applications.ListOwners)
		r.Put("/{id}/deployment-yaml", cfg.Validation.SaveDeploymentYAML)
		r.Post("/{id}/validate", cfg.Validation.Validate)
	})

	r.Route("/supported-stacks", func(r chi.Router) {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Stacks.List)
	})

	return r
}
