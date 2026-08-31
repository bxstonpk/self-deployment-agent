package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type RouterConfig struct {
	Authenticator Authenticator
	Applications  *ApplicationHandler
	Departments   *DepartmentHandler
	Validation    *ValidationHandler
	Stacks        *StackHandler
	Builds        *BuildHandler
	Deploys       *DeployHandler
	ScaleEvents   *ScaleEventsHandler
	Proxy         *ProxyHandler
	Lifecycle     *LifecycleHandler
	// PlatformEnv gates DevOnlyGuard. The only Authenticator implementation
	// today is DevHeaderAuthenticator, so this is always enforced until a
	// real one lands per DEC-001 (docs/17_Decision_Log.md).
	PlatformEnv string
	// CORSAllowedOrigins lets a browser-based client (apps/admin-portal,
	// served from its own dev-server origin) call this API cross-origin.
	// Empty disables CORS entirely (e.g. non-dev environments, or when no
	// browser client is in play) rather than defaulting to an open policy.
	CORSAllowedOrigins []string
}

func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if len(cfg.CORSAllowedOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.CORSAllowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "X-Dev-User-Email", "X-Dev-User-Name", "X-Dev-Department"},
			AllowCredentials: false,
			MaxAge:           300,
		}))
	}

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
		r.Post("/{id}/build", cfg.Builds.TriggerBuild)
		r.Get("/{id}/builds/latest", cfg.Builds.LatestBuild)
		r.Post("/{id}/deploy", cfg.Deploys.TriggerDeploy)
		r.Get("/{id}/deployments/latest", cfg.Deploys.LatestDeployment)
		r.Get("/{id}/deployments", cfg.Deploys.DeploymentHistory)
		r.Post("/{id}/rollback", cfg.Deploys.Rollback)
		r.Get("/{id}/scale-events", cfg.ScaleEvents.List)
		r.Post("/{id}/suspend", cfg.Lifecycle.Suspend)
		r.Post("/{id}/resume", cfg.Lifecycle.Resume)
		r.Post("/{id}/restart", cfg.Lifecycle.Restart)
		r.Post("/{id}/archive", cfg.Lifecycle.Archive)
		r.Post("/{id}/delete", cfg.Lifecycle.Delete)
	})

	r.Route("/supported-stacks", func(r chi.Router) {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Stacks.List)
	})

	r.Route("/departments", func(r chi.Router) {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Departments.List)
	})

	r.Route("/deployments", func(r chi.Router) {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/{deploymentId}", cfg.Deploys.GetDeployment)
		r.Post("/{deploymentId}/approve", cfg.Deploys.DecideApproval)
	})

	// Deliberately outside any auth middleware — see the package doc
	// comment on ProxyHandler for why.
	r.Handle("/run/*", cfg.Proxy)

	return r
}
