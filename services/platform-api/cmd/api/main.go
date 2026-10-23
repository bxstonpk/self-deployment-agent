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
	// BACKUP_TIMEZONE needs the zone database, and the runtime image
	// has none.
	_ "time/tzdata"

	dockerclient "github.com/docker/docker/client"

	"platform-api/internal/buildengine"
	"platform-api/internal/config"
	"platform-api/internal/db"
	"platform-api/internal/hostdisk"
	"platform-api/internal/httpapi"
	"platform-api/internal/imagescan"
	"platform-api/internal/repository/postgres"
	"platform-api/internal/runtimeengine"
	"platform-api/internal/secretbox"
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
	auditRepo := postgres.NewAuditRepo(pool)
	notificationRepo := postgres.NewNotificationRepo(pool)
	transferRepo := postgres.NewOwnershipTransferRepo(pool)
	databaseRepo := postgres.NewProvisionedDatabaseRepo(pool)
	secretRepo := postgres.NewApplicationSecretRepo(pool)
	logRepo := postgres.NewLogRepo(pool)
	metricRepo := postgres.NewMetricRepo(pool)

	dockerCli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("create docker client: %v", err)
	}
	dockerEngine := buildengine.NewDockerEngine(dockerCli)
	scanner := imagescan.NewTrivyScanner(dockerCli)
	runtime := runtimeengine.NewDockerRuntime(dockerCli)
	// Module S: collect every application container's output (FR-086), and
	// pick collection back up for containers that outlived a restart.
	runtime.SetLogRetention(cfg.LogRetention)
	runtime.EnableLogCollection(logRepo)
	if resumed, err := runtime.ResumeLogCollection(ctx); err != nil {
		log.Printf("logs: could not resume collection: %v", err)
	} else if resumed > 0 {
		log.Printf("logs: resumed collection for %d application container(s)", resumed)
	}

	auditService := service.NewAuditService(auditRepo, ownerRepo, deploymentRepo, buildRepo)
	secretBox, err := secretbox.FromBase64(cfg.SecretKey)
	if err != nil {
		log.Fatalf("PLATFORM_SECRET_KEY: %v", err)
	}
	log.Printf("secret store: sealing with key %s", secretBox.KeyID())
	secretService := service.NewSecretService(applicationRepo, ownerRepo, secretRepo, secretBox, auditService)
	databaseService := service.NewDatabaseService(databaseRepo, runtime, secretService)
	// FR-063's at-rest half for databases provisioned before Module O
	// existed, whose passwords were stored in plaintext. Fatal on failure:
	// starting anyway would leave them there.
	moved, err := databaseService.MigrateLegacyPlaintextPasswords(ctx)
	if err != nil {
		log.Fatalf("secret store: migrate legacy database passwords: %v", err)
	}
	if moved > 0 {
		log.Printf("secret store: moved %d legacy plaintext database password(s) into the encrypted store", moved)
	}
	resources := service.NewApplicationResources(databaseService, secretService)
	backupService := service.NewBackupService(postgres.NewBackupRepo(pool), databaseRepo, applicationRepo, ownerRepo,
		secretService, runtime, secretBox, nil, auditService, cfg.BackupDir, service.BackupSchedule{
			Location: cfg.BackupLocation, Hour: cfg.BackupHour, FullWeekday: cfg.BackupFullWeekday,
		})
	resources.SetBackups(backupService)
	notificationService := service.NewNotificationService(notificationRepo, ownerRepo)
	backupService.SetNotifier(notificationService)
	admins := service.NewEmailAdminDirectory(userRepo, cfg.AdminEmails)
	alertRepo := postgres.NewAlertRepo(pool)
	alertService := service.NewAlertService(alertRepo, applicationRepo, ownerRepo, notificationService, admins,
		cfg.Alerts, func() (float64, error) { return hostdisk.UsedPercent(cfg.BackupDir) })
	applicationService := service.NewApplicationService(
		applicationRepo, ownerRepo, departmentRepo, userRepo, transferRepo, notificationService, cfg.OwnershipTransferWindow, auditService,
	)
	validationService := service.NewValidationService(applicationRepo, ownerRepo, stackRepo, auditService)
	buildService := service.NewBuildService(applicationRepo, ownerRepo, buildRepo, baseImageRepo, dockerEngine, auditService)
	scaleService := service.NewScaleService(applicationRepo, deploymentRepo, serviceStateRepo, scaleEventRepo, stackRepo, runtime, resources)
	deployService := service.NewDeploymentService(applicationRepo, ownerRepo, buildRepo, deploymentRepo, approvalRepo, scanner, runtime, scaleService, auditService, notificationService, resources)
	lifecycleService := service.NewLifecycleService(applicationRepo, ownerRepo, deploymentRepo, serviceStateRepo, runtime, auditService, resources, buildRepo)
	rotationService := service.NewRotationService(applicationRepo, ownerRepo, secretService, databaseService, lifecycleService, auditService)
	reportingService := service.NewReportingService(applicationRepo, ownerRepo, departmentRepo, deploymentRepo, auditRepo)
	logService := service.NewLogService(applicationRepo, ownerRepo, logRepo, auditService)
	// Module R: continuously re-check every already-Running instance's
	// health (FR-084) and restart one that starts failing it (FR-085) —
	// on top of the checks deploy/resume/restart/cold-start already run.
	healthService := service.NewHealthMonitorService(applicationRepo, deploymentRepo, serviceStateRepo, resources, runtime, notificationService,
		deployService, cfg.PostActivationRollbackWindow)
	// Module T: the proxy counts every request it forwards, and the
	// sampler below reads each running container's CPU and memory.
	trafficRecorder := service.NewTrafficRecorder()
	metricsService := service.NewMetricsService(applicationRepo, ownerRepo, metricRepo, runtime, trafficRecorder,
		deploymentRepo, serviceStateRepo, scaleEventRepo, cfg.MetricsSampleInterval)
	// DEC-001/DEC-003 (revised 2026-10-23): real authentication unless the
	// dev-header mode was asked for explicitly — config.Load already
	// refused that outside PLATFORM_ENV=dev.
	var authenticator httpapi.Authenticator
	var authHandler *httpapi.AuthHandler
	if cfg.AuthMode == "dev-header" {
		log.Printf("auth: DEV-HEADER mode — identities are self-declared and unverified (local testing only)")
		authenticator = httpapi.NewDevHeaderAuthenticator(userRepo, departmentRepo)
	} else {
		policy := service.DefaultAuthPolicy()
		policy.EnrollmentCode = cfg.EnrollmentCode
		policy.AllowedEmailDomains = cfg.AllowedEmailDomains
		policy.SessionTTL = cfg.SessionTTL
		policy.APIKeyTTL = cfg.APIKeyTTL
		policy.APIKeyMaxTTL = cfg.APIKeyMaxTTL
		authService := service.NewAuthService(postgres.NewAuthRepo(pool), userRepo, departmentRepo, auditService, policy)
		authenticator = httpapi.NewTokenAuthenticator(authService)
		authHandler = httpapi.NewAuthHandler(authService, cfg.PortalURL)
		log.Printf("auth: token mode — sessions %s, API keys %s (max %s), devices approved at %s/device",
			cfg.SessionTTL, cfg.APIKeyTTL, cfg.APIKeyMaxTTL, cfg.PortalURL)
	}

	// DEC-017: who may approve, and which environments need it.
	deployService.SetApprovalEnvironments(cfg.ApprovalEnvironments)
	deployHandler := httpapi.NewDeployHandler(deployService, applicationRepo, cfg.PublicBaseURL)
	var approvalHandler *httpapi.ApprovalHandler
	if len(cfg.ApproverKeys) > 0 {
		approvalService := service.NewApprovalService(postgres.NewDeploymentApprovalRepo(pool), service.ApprovalLookups{
			Deployments: deploymentRepo, Builds: buildRepo, Apps: applicationRepo, Users: userRepo,
		}, deployService, notificationService, cfg.ApproverKeys)
		deployService.RequireSignedApprovals(approvalService.NotifyApprovers)
		approvalHandler = httpapi.NewApprovalHandler(approvalService, deployHandler)
		for _, k := range cfg.ApproverKeys {
			log.Printf("approvals: %v deployments need a decision signed by an approver key; %s holds %s",
				cfg.ApprovalEnvironments, k.Email, k.Fingerprint)
		}
	} else {
		log.Printf("approvals: no PLATFORM_APPROVER_KEYS — an application's own owner approves its %v deployments", cfg.ApprovalEnvironments)
	}

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Authenticator:      authenticator,
		Applications:       httpapi.NewApplicationHandler(applicationService),
		Departments:        httpapi.NewDepartmentHandler(departmentRepo),
		Validation:         httpapi.NewValidationHandler(validationService),
		Stacks:             httpapi.NewStackHandler(stackRepo),
		Builds:             httpapi.NewBuildHandler(buildService),
		Deploys:            deployHandler,
		ScaleEvents:        httpapi.NewScaleEventsHandler(deployService, scaleService),
		Proxy:              httpapi.NewProxyHandler(scaleService, trafficRecorder),
		Lifecycle:          httpapi.NewLifecycleHandler(lifecycleService, applicationRepo, cfg.PublicBaseURL),
		Audit:              httpapi.NewAuditHandler(auditService),
		Notifications:      httpapi.NewNotificationHandler(notificationService),
		Reports:            httpapi.NewReportHandler(reportingService),
		Secrets:            httpapi.NewSecretHandler(secretService, rotationService),
		Logs:               httpapi.NewLogHandler(logService),
		Metrics:            httpapi.NewMetricsHandler(metricsService),
		Backups:            httpapi.NewBackupHandler(backupService),
		Alerts:             httpapi.NewAlertHandler(alertService),
		Approvals:          approvalHandler,
		Auth:               authHandler,
		AuthMode:           cfg.AuthMode,
		PlatformEnv:        cfg.PlatformEnv,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
	})

	go runScaleSweeper(ctx, scaleService, cfg.ScaleSweepInterval, cfg.ScaleToZeroIdleTimeout)
	go runMetricsSampler(ctx, metricsService, cfg.MetricsSampleInterval)
	go runHealthSweeper(ctx, healthService, cfg.HealthSweepInterval)
	if cfg.BackupEnabled {
		go runBackupScheduler(ctx, backupService, cfg.BackupSweep)
	} else {
		log.Printf("backups: scheduled database backups are switched off (BACKUP_ENABLED=false)")
	}
	retention := service.NewRetentionService(logRepo, cfg.LogRetention)
	retention.SetAlertHistory(alertRepo)
	go runRetentionSweeper(ctx, retention, time.Hour)
	go runAlertEvaluator(ctx, alertService, cfg.AlertEvalInterval, admins.Emails())

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

// runMetricsSampler implements FR-090's collection loop: one reading of
// every running application container, and whatever traffic the proxy has
// counted since the last sweep. Runs until ctx is cancelled.
// runAlertEvaluator judges DEC-028's alert rules every interval (FR-092).
func runAlertEvaluator(ctx context.Context, alerts *service.AlertService, interval time.Duration, admins []string) {
	t := alerts.Thresholds()
	log.Printf("alerts: every %s — errors %.0f%%/%.0f%%, latency %.0f/%.0f ms, CPU %.0f/%.0f%%, memory %.0f/%.0f%%, disk %.0f/%.0f%%, reminders every %s; platform alerts to %v",
		interval, 100*t.ErrorRateWarning, 100*t.ErrorRateCritical, t.LatencyWarningMs, t.LatencyCriticalMs,
		t.CPUWarning, t.CPUCritical, t.MemoryWarning, t.MemoryCritical, t.DiskWarning, t.DiskCritical, t.ReminderInterval, admins)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := alerts.Evaluate(ctx); err != nil {
				log.Printf("alerts: %v", err)
			} else if n > 0 {
				log.Printf("alerts: sent %d notification(s)", n)
			}
		}
	}
}

// runBackupScheduler takes the database backups that are due (DEC-025),
// checking every interval. Each database is backed up once a day, so the
// interval only bounds how long after the scheduled hour a backup starts.
func runBackupScheduler(ctx context.Context, backups *service.BackupService, interval time.Duration) {
	s := backups.Schedule()
	log.Printf("backups: full every %s, differential other days, from %02d:00 %s; checked every %s",
		s.FullWeekday, s.Hour, s.Location, interval)
	run := func() {
		if _, err := backups.RunScheduled(ctx); err != nil {
			log.Printf("backups: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// runRetentionSweeper purges application logs past their retention
// (DEC-027) once at startup and then every interval. Hourly is plenty for
// a period measured in days.
func runRetentionSweeper(ctx context.Context, retention *service.RetentionService, interval time.Duration) {
	if retention.Retention() <= 0 {
		log.Printf("log retention: keeping application logs forever (LOG_RETENTION_DAYS=0)")
		return
	}
	log.Printf("log retention: deleting application logs older than %s, checked every %s", retention.Retention(), interval)
	sweep := func() {
		if n, err := retention.Sweep(ctx); err != nil {
			log.Printf("log retention: %v", err)
		} else if n > 0 {
			log.Printf("log retention: deleted %d log line(s)", n)
		}
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func runMetricsSampler(ctx context.Context, metricsService *service.MetricsService, interval time.Duration) {
	log.Printf("metrics sampler running every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := metricsService.Collect(ctx); err != nil {
				log.Printf("metrics sampler: %v", err)
			}
		}
	}
}

// runHealthSweeper implements FR-084's continuous polling loop:
// periodically re-checks every already-Running instance's health and
// remediates one that's begun failing it (FR-085). Runs until ctx is
// cancelled.
func runHealthSweeper(ctx context.Context, healthService *service.HealthMonitorService, interval time.Duration) {
	log.Printf("health sweeper running every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, remediated, err := healthService.Sweep(ctx)
			if err != nil {
				log.Printf("health sweeper: %v", err)
				continue
			}
			if remediated > 0 {
				log.Printf("health sweeper: remediated %d unhealthy instance(s)", remediated)
			}
		}
	}
}
