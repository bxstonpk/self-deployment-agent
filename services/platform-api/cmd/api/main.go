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

	"platform-api/internal/buildengine"
	"platform-api/internal/config"
	"platform-api/internal/db"
	"platform-api/internal/httpapi"
	"platform-api/internal/repository/postgres"
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

	dockerEngine, err := buildengine.NewDockerEngine()
	if err != nil {
		log.Fatalf("build engine: %v", err)
	}

	applicationService := service.NewApplicationService(applicationRepo, ownerRepo, departmentRepo)
	validationService := service.NewValidationService(applicationRepo, ownerRepo, stackRepo)
	buildService := service.NewBuildService(applicationRepo, ownerRepo, buildRepo, baseImageRepo, dockerEngine)
	authenticator := httpapi.NewDevHeaderAuthenticator(userRepo, departmentRepo)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Authenticator: authenticator,
		Applications:  httpapi.NewApplicationHandler(applicationService),
		Validation:    httpapi.NewValidationHandler(validationService),
		Stacks:        httpapi.NewStackHandler(stackRepo),
		Builds:        httpapi.NewBuildHandler(buildService),
		PlatformEnv:   cfg.PlatformEnv,
	})

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
