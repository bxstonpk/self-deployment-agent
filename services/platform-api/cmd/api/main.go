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
	"strings"
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
	"platform-api/internal/hostinfo"
	"platform-api/internal/httpapi"
	"platform-api/internal/imagescan"
	"platform-api/internal/repository/postgres"
	"platform-api/internal/runtimeengine"
	"platform-api/internal/secretbox"
	"platform-api/internal/service"
	"platform-api/internal/sourcestore"
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
	// Databases sleep when their application does, and wake when it's needed
	// (the platform owner, 2026-10-24).
	databaseService.SetSleeper(runtime, applicationRepo, cfg.ScaleToZeroIdleTimeout)
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
	// Every build's source, kept for the approver to review (and deleted
	// with its application).
	sources := sourcestore.New(cfg.SourceDir)
	resources.SetSources(sources)
	backupService := service.NewBackupService(postgres.NewBackupRepo(pool), databaseRepo, applicationRepo, ownerRepo,
		secretService, runtime, secretBox, nil, auditService, cfg.BackupDir, service.BackupSchedule{
			Location: cfg.BackupLocation, Hour: cfg.BackupHour, FullWeekday: cfg.BackupFullWeekday,
		})
	resources.SetBackups(backupService)
	notificationService := service.NewNotificationService(notificationRepo, ownerRepo)
	backupService.SetNotifier(notificationService)
	backupService.SetWaker(databaseService.Wake)
	admins := service.NewEmailAdminDirectory(userRepo, cfg.AdminEmails)
	alertRepo := postgres.NewAlertRepo(pool)
	alertService := service.NewAlertService(alertRepo, applicationRepo, ownerRepo, notificationService, admins,
		cfg.Alerts, func() (float64, error) { return hostdisk.UsedPercent(cfg.BackupDir) })
	applicationService := service.NewApplicationService(
		applicationRepo, ownerRepo, departmentRepo, userRepo, transferRepo, notificationService, cfg.OwnershipTransferWindow, auditService,
	)
	validationService := service.NewValidationService(applicationRepo, ownerRepo, stackRepo, auditService)
	buildService := service.NewBuildService(applicationRepo, ownerRepo, buildRepo, baseImageRepo, dockerEngine, auditService)
	buildService.SetSourceStore(sources)
	// Development governance: the company standard from the start (a
	// profile, allowed libraries only, exceptions decided by an admin).
	governanceService := service.NewGovernanceService(postgres.NewGovernanceRepo(pool), applicationRepo, ownerRepo, stackRepo,
		admins, notificationService, auditService, cfg.RequireProfile)
	buildService.SetGovernance(governanceService)
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
		authHandler = httpapi.NewAuthHandler(authService, cfg.PortalURL, admins.IsAdmin, func(email string) bool {
			for _, k := range cfg.ApproverKeys {
				if strings.EqualFold(k.Email, email) {
					return true
				}
			}
			return false
		})
		log.Printf("auth: token mode — sessions %s, API keys %s (max %s), devices approved at %s/device",
			cfg.SessionTTL, cfg.APIKeyTTL, cfg.APIKeyMaxTTL, cfg.PortalURL)
	}

	// DEC-017: who may approve, and which environments need it.
	deployService.SetApprovalEnvironments(cfg.ApprovalEnvironments)
	deployHandler := httpapi.NewDeployHandler(deployService, applicationRepo, cfg.PublicBaseURL)
	// Tests run by the platform (DEC-029 part 2), in the build sandbox.
	testRunRepo := postgres.NewTestRunRepo(pool)
	if n, err := testRunRepo.FailUnfinished(ctx); err != nil {
		log.Printf("test runs: %v", err)
	} else if n > 0 {
		log.Printf("test runs: closed %d run(s) a restart interrupted", n)
	}
	testRunService := service.NewTestRunService(applicationRepo, ownerRepo, testRunRepo, baseImageRepo, buildRepo, sources,
		dockerEngine, auditService, service.TestLimits{MemoryBytes: int64(cfg.TestRunMemoryMB) << 20, CPUs: cfg.TestRunCPUs},
		cfg.TestRunTimeout, cfg.TestRunConcurrent)
	deployHandler.SetTestRuns(testRunService)
	// Dev copies (DEC-029 part 3): each application's long-lived copy to try
	// changes on, its own application, made of the same steps an owner's
	// calls go through.
	governanceRepo := postgres.NewGovernanceRepo(pool)
	devCopyRepo := postgres.NewDevCopyRepo(pool)
	devCopyService := service.NewDevCopyService(applicationRepo, ownerRepo, devCopyRepo, service.DevCopySteps{
		Register:     applicationService.Register,
		SaveYAML:     validationService.SaveDeploymentYAML,
		Validate:     validationService.Validate,
		Build:        buildService.TriggerBuild,
		Deploy:       deployService.InitiateDeploy,
		Archive:      lifecycleService.Archive,
		Delete:       lifecycleService.Delete,
		GetProfile:   governanceRepo.GetProfile,
		PutProfile:   governanceRepo.PutProfile,
		HasDatabase:  backupService.HasDatabase,
		CopyDatabase: backupService.CopyDatabase,
	}, auditService)
	deployService.SetDevCopies(devCopyService)
	governanceService.SetDevCopies(devCopyService)
	log.Printf("test runs: %d at a time, each service %d MB and %.1f CPUs, at most %s a run",
		cfg.TestRunConcurrent, cfg.TestRunMemoryMB, cfg.TestRunCPUs, cfg.TestRunTimeout)
	var approvalHandler *httpapi.ApprovalHandler
	if len(cfg.ApproverKeys) > 0 {
		approvalService := service.NewApprovalService(postgres.NewDeploymentApprovalRepo(pool), service.ApprovalLookups{
			Deployments: deploymentRepo, Builds: buildRepo, Apps: applicationRepo, Users: userRepo,
		}, deployService, notificationService, cfg.ApproverKeys)
		approvalService.SetSourceStore(sources)
		deployService.RequireSignedApprovals(approvalService.NotifyApprovers)
		approvalHandler = httpapi.NewApprovalHandler(approvalService, deployHandler)
		for _, k := range cfg.ApproverKeys {
			log.Printf("approvals: %v deployments need a decision signed by an approver key; %s holds %s",
				cfg.ApprovalEnvironments, k.Email, k.Fingerprint)
		}
	} else {
		log.Printf("approvals: no PLATFORM_APPROVER_KEYS — an application's own owner approves its %v deployments", cfg.ApprovalEnvironments)
	}

	// The administrators' dashboard: the host's own CPU and memory, read
	// every 5s and kept for 15 minutes.
	hostSampler := hostinfo.NewSampler("/proc", 180)
	adminService := service.NewAdminOverviewService(postgres.NewAdminRepo(pool), hostSampler, admins,
		func() (float64, error) { return hostdisk.UsedPercent(cfg.BackupDir) }, cfg.MetricsSampleInterval)

	policyHandler := httpapi.NewPolicyHandler(cfg.ApprovalEnvironments, cfg.ApproverKeys)
	policyHandler.SetDevCopies(true)
	proxyHandler := httpapi.NewProxyHandler(scaleService, trafficRecorder)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Authenticator:      authenticator,
		Admin:              httpapi.NewAdminHandler(adminService, cfg.PublicBaseURL),
		Policy:             policyHandler,
		Applications:       httpapi.NewApplicationHandler(applicationService),
		Departments:        httpapi.NewDepartmentHandler(departmentRepo),
		Validation:         httpapi.NewValidationHandler(validationService),
		Stacks:             httpapi.NewStackHandler(stackRepo),
		Builds:             httpapi.NewBuildHandler(buildService),
		Deploys:            deployHandler,
		ScaleEvents:        httpapi.NewScaleEventsHandler(deployService, scaleService),
		Proxy:              proxyHandler,
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
		Governance:         httpapi.NewGovernanceHandler(governanceService),
		TestRuns:           httpapi.NewTestRunHandler(testRunService),
		DevCopies:          httpapi.NewDevCopyHandler(devCopyService, cfg.PublicBaseURL),
		Auth:               authHandler,
		AuthMode:           cfg.AuthMode,
		PlatformEnv:        cfg.PlatformEnv,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
	})

	go runScaleSweeper(ctx, scaleService, cfg.ScaleSweepInterval, cfg.ScaleToZeroIdleTimeout)
	go runMetricsSampler(ctx, metricsService, cfg.MetricsSampleInterval)
	go runHostSampler(ctx, hostSampler, 5*time.Second)
	go runHealthSweeper(ctx, healthService, cfg.HealthSweepInterval)
	go runDevCopySweeper(ctx, devCopyService, time.Minute)
	// Failed applications that never ran are removed, their names freed.
	failedCleanup := service.NewFailedCleanupService(applicationRepo, service.FailedCleanupLookups{
		Deployments: deploymentRepo, Builds: buildRepo, Owners: ownerRepo, DevCopy: devCopyRepo.Live, Apps: applicationRepo,
	}, lifecycleService.Delete, notificationService, auditService)
	go runEvery(ctx, "failed cleanup", 30*time.Second, failedCleanup.Sweep, "removed %d application(s) that never ran")
	go runEvery(ctx, "databases", cfg.ScaleSweepInterval, func(ctx context.Context) (int, error) {
		return databaseService.SleepIdle(ctx, time.Now())
	}, "put %d idle database(s) to sleep")
	go func() {
		if n, err := databaseService.TuneAll(ctx); err != nil {
			log.Printf("databases: %v", err)
		} else if n > 0 {
			log.Printf("databases: gave %d existing MySQL database(s) the platform's configuration", n)
		}
	}()
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
// runEvery runs one sweep every interval until the platform stops,
// logging what it did.
func runEvery(ctx context.Context, name string, interval time.Duration, sweep func(context.Context) (int, error), did string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := sweep(ctx); err != nil {
				log.Printf("%s: %v", name, err)
			} else if n > 0 {
				log.Printf("%s: "+did, name, n)
			}
		}
	}
}

// runDevCopySweeper removes the dev copies of archived and deleted
// applications.
func runDevCopySweeper(ctx context.Context, copies *service.DevCopyService, interval time.Duration) {
	log.Printf("dev copies: checked every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := copies.Sweep(ctx); err != nil {
				log.Printf("dev copies: %v", err)
			} else if n > 0 {
				log.Printf("dev copies: removed %d", n)
			}
		}
	}
}

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

// runHostSampler reads the host's CPU and memory for the administrators'
// dashboard. Without /proc (not Linux) it says so once and stops.
func runHostSampler(ctx context.Context, sampler *hostinfo.Sampler, interval time.Duration) {
	if err := sampler.Sample(time.Now()); err != nil {
		log.Printf("host sampler: host CPU and memory unavailable on this machine: %v", err)
		return
	}
	log.Printf("host sampler: reading host CPU and memory every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if err := sampler.Sample(t); err != nil {
				log.Printf("host sampler: %v", err)
			}
		}
	}
}
