// Command api starts the Platform API HTTP server for the Draft-state
// slice of the Company AI Application Deployment Platform.
// See docs/13_API_Requirements.md for the Business API this implements and
// docs/10_System_Architecture.md for how it sits in the Control Plane.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	dockerclient "github.com/docker/docker/client"

	"platform-api/internal/buildengine"
	"platform-api/internal/config"
	"platform-api/internal/db"
	"platform-api/internal/httpapi"
	"platform-api/internal/imagescan"
	"platform-api/internal/repository/postgres"
	"platform-api/internal/runtimeengine"
	"platform-api/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	departmentRepo := postgres.NewDepartmentRepo(pool)
	userRepo := postgres.NewUserRepo(pool)
	applicationRepo := postgres.NewApplicationRepo(pool)
	ownerRepo := postgres.NewApplicationOwnerRepo(pool)
	stackRepo := postgres.NewStackRepo(pool)
	buildRepo := postgres.NewBuildRepo(pool)
	baseImageRepo := postgres.NewBaseImageRepo(pool)
	deploymentRepo := postgres.NewDeploymentRepo(pool)
	approvalRepo := postgres.NewDeploymentApprovalRepo(pool)
	serviceStateRepo := postgres.NewServiceRuntimeStateRepo(pool)
	scaleEventRepo := postgres.NewScaleEventRepo(pool)

	dockerCli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("create docker client: %v", err)
	}
	dockerEngine := buildengine.NewDockerEngine(dockerCli)
	scanner := imagescan.NewTrivyScanner(dockerCli)
	runtime := runtimeengine.NewDockerRuntime(dockerCli)

	applicationService := service.NewApplicationService(applicationRepo, ownerRepo, departmentRepo)
	validationService := service.NewValidationService(applicationRepo, ownerRepo, stackRepo)
	buildService := service.NewBuildService(applicationRepo, ownerRepo, buildRepo, baseImageRepo, dockerEngine)
	scaleService := service.NewScaleService(applicationRepo, deploymentRepo, serviceStateRepo, scaleEventRepo, stackRepo, runtime)
	deployService := service.NewDeploymentService(applicationRepo, ownerRepo, buildRepo, deploymentRepo, approvalRepo, scanner, runtime, scaleService)
	lifecycleService := service.NewLifecycleService(applicationRepo, ownerRepo, deploymentRepo, serviceStateRepo, runtime)
	authenticator := httpapi.NewDevHeaderAuthenticator(userRepo, departmentRepo)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Authenticator: authenticator,
		Applications:  httpapi.NewApplicationHandler(applicationService),
		Departments:   httpapi.NewDepartmentHandler(departmentRepo),
		Validation:    httpapi.NewValidationHandler(validationService),
		Stacks:        httpapi.NewStackHandler(stackRepo),
		Builds:        httpapi.NewBuildHandler(buildService),
		Deploys:       httpapi.NewDeployHandler(deployService),
		ScaleEvents:   httpapi.NewScaleEventsHandler(deployService, scaleService),
		Proxy:         httpapi.NewProxyHandler(scaleService),
		Lifecycle:     httpapi.NewLifecycleHandler(lifecycleService),
		PlatformEnv:        cfg.PlatformEnv,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
	})

	go runScaleSweeper(ctx, scaleService, cfg.ScaleSweepInterval, cfg.ScaleToZeroIdleTimeout)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("platform-api listening on :%s (PLATFORM_ENV=%s)", cfg.Port, cfg.PlatformEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

// runScaleSweeper implements FR-052's idle-detection loop: periodically
// scales down every eligible service that's had no activity for the
// platform's idle timeout. Runs until ctx is cancelled (server shutdown).
func runScaleSweeper(ctx context.Context, scaleService *service.ScaleService, interval, idleTimeout time.Duration) {
	log.Printf("scale-to-zero sweeper running every %s, idle timeout %s", interval, idleTimeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scaledDown, err := scaleService.SweepIdle(ctx, idleTimeout)
			if err != nil {
				log.Printf("scale sweeper: %v", err)
				continue
			}
			if scaledDown > 0 {
				log.Printf("scale sweeper: scaled %d service(s) to zero", scaledDown)
			}
		}
	}
}
