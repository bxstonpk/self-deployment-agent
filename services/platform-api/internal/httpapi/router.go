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
	Admin         *AdminHandler
	Departments   *DepartmentHandler
	Validation    *ValidationHandler
	Stacks        *StackHandler
	Builds        *BuildHandler
	Deploys       *DeployHandler
	ScaleEvents   *ScaleEventsHandler
	Proxy         *ProxyHandler
	Lifecycle     *LifecycleHandler
	Audit         *AuditHandler
	Notifications *NotificationHandler
	Reports       *ReportHandler
	Secrets       *SecretHandler
	Logs          *LogHandler
	Metrics       *MetricsHandler
	Backups       *BackupHandler
	Alerts        *AlertHandler
	Approvals     *ApprovalHandler
	// Auth serves /auth/*. Nil under the dev-header mode, which has no
	// accounts to sign in to.
	Auth *AuthHandler
	// AuthMode is "token" (real authentication: Portal sessions and
	// machine API keys) or "dev-header" (the old self-declared identity,
	// kept only for local test scripts and refused outside
	// PLATFORM_ENV=dev). See DEC-001/DEC-003 revised 2026-10-23.
	AuthMode string
	// PlatformEnv gates DevOnlyGuard under the dev-header mode.
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
			AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Dev-User-Email", "X-Dev-User-Name", "X-Dev-Department"},
			AllowCredentials: false,
			MaxAge:           300,
		}))
	}

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	if cfg.Auth != nil {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", cfg.Auth.Register)
			r.Post("/login", cfg.Auth.Login)
			r.Post("/device", cfg.Auth.StartDevice)
			r.Post("/device/token", cfg.Auth.DeviceToken)
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(cfg.Authenticator))
				r.Get("/me", cfg.Auth.Me)
				r.Post("/logout", cfg.Auth.Logout)
				r.Get("/api-keys", cfg.Auth.ListAPIKeys)
				r.Post("/api-keys", cfg.Auth.CreateAPIKey)
				r.Delete("/api-keys/{id}", cfg.Auth.RevokeAPIKey)
				r.Get("/device/{userCode}", cfg.Auth.GetDevice)
				r.Post("/device/{userCode}/approve", cfg.Auth.ApproveDevice)
				r.Post("/device/{userCode}/deny", cfg.Auth.DenyDevice)
			})
		})
	}

	if cfg.Admin != nil {
		r.Route("/admin", func(r chi.Router) {
			useDevGuard(r, cfg)
			r.Use(RequireAuth(cfg.Authenticator))
			r.Get("/overview", cfg.Admin.Overview)
		})
	}

	r.Route("/applications", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Post("/", cfg.Applications.Register)
		r.Get("/", cfg.Applications.List)
		r.Get("/{id}", cfg.Applications.Get)
		r.Patch("/{id}", cfg.Applications.UpdateMetadata)
		r.Get("/{id}/owners", cfg.Applications.ListOwners)
		r.Post("/{id}/owners", cfg.Applications.GrantOwner)
		r.Delete("/{id}/owners/{userId}", cfg.Applications.RevokeOwner)
		r.Post("/{id}/ownership-transfer", cfg.Applications.InitiateTransfer)
		r.Get("/{id}/ownership-transfer", cfg.Applications.GetPendingTransfer)
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
		r.Get("/{id}/secrets", cfg.Secrets.List)
		r.Put("/{id}/secrets/{name}", cfg.Secrets.Set)
		r.Delete("/{id}/secrets/{name}", cfg.Secrets.Delete)
		r.Post("/{id}/secrets/{name}/rotate", cfg.Secrets.Rotate)
		r.Get("/{id}/logs", cfg.Logs.Query)
		r.Get("/{id}/metrics", cfg.Metrics.Query)
		if cfg.Alerts != nil {
			r.Get("/{id}/alerts", cfg.Alerts.ForApplication)
		}
		if cfg.Backups != nil {
			r.Get("/{id}/backups", cfg.Backups.List)
			r.Post("/{id}/backups", cfg.Backups.BackupNow)
			r.Post("/{id}/backups/{backupId}/restore", cfg.Backups.Restore)
		}
	})

	r.Route("/supported-stacks", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Stacks.List)
	})

	r.Route("/departments", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Departments.List)
		r.Post("/", cfg.Departments.Create)
		r.Get("/summary", cfg.Departments.Summary)
	})

	r.Route("/audit-log", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Audit.Query)
		r.Get("/export", cfg.Audit.Export)
		r.Get("/integrity", cfg.Audit.VerifyIntegrity)
	})

	r.Route("/notifications", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/", cfg.Notifications.List)
		r.Post("/{id}/read", cfg.Notifications.MarkRead)
	})

	r.Route("/reports", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/application-inventory", cfg.Reports.ApplicationInventory)
		r.Get("/deployment-activity", cfg.Reports.DeploymentActivity)
	})

	r.Route("/ownership-transfers", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Post("/{transferId}/accept", cfg.Applications.AcceptTransfer)
	})

	if cfg.Approvals != nil {
		r.Route("/approvals", func(r chi.Router) {
			r.Use(RequireAuth(cfg.Authenticator))
			r.Get("/pending", cfg.Approvals.Pending)
			r.Get("/{deploymentId}", cfg.Approvals.Get)
			r.Post("/{deploymentId}/decision", cfg.Approvals.Decide)
		})
	}

	r.Route("/deployments", func(r chi.Router) {
		useDevGuard(r, cfg)
		r.Use(RequireAuth(cfg.Authenticator))
		r.Get("/{deploymentId}", cfg.Deploys.GetDeployment)
		r.Post("/{deploymentId}/approve", cfg.Deploys.DecideApproval)
	})

	// Deliberately outside any auth middleware — see the package doc
	// comment on ProxyHandler for why.
	r.Handle("/run/*", cfg.Proxy)
	r.Handle(appPathPrefix+"*", http.HandlerFunc(cfg.Proxy.ServeApp))

	return r
}

// useDevGuard applies DevOnlyGuard only under the dev-header mode: the
// self-declared identity must never serve outside PLATFORM_ENV=dev, while
// real token authentication has no such restriction.
func useDevGuard(r chi.Router, cfg RouterConfig) {
	if cfg.AuthMode == "dev-header" {
		r.Use(DevOnlyGuard(cfg.PlatformEnv))
	}
}
