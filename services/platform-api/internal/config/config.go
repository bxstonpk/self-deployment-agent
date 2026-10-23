package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	Port        string

	// PublicBaseURL is how callers outside this process reach it, and so
	// the base of the stable /run/{app}/{service} address an application
	// is served on (see internal/httpapi/service_url.go). It cannot be
	// derived from Port: under Docker the API listens on 8080 inside its
	// container and is published on a different port outside.
	PublicBaseURL string
	PlatformEnv   string // "dev" is required for the dev-header auth mode; see AuthMode

	// AuthMode is "token" (the default: Portal sessions and per-machine API
	// keys, DEC-001/DEC-003 revised 2026-10-23) or "dev-header" (the old
	// self-declared identity, for local test scripts only — refused unless
	// PlatformEnv is "dev").
	AuthMode string
	// EnrollmentCode is the shared code IT gives employees so they can
	// create an account. Required in token mode: without it, anyone on the
	// LAN could register.
	EnrollmentCode string
	// AllowedEmailDomains optionally limits registration (e.g. sti-th.com).
	AllowedEmailDomains []string
	SessionTTL          time.Duration
	APIKeyTTL           time.Duration
	APIKeyMaxTTL        time.Duration
	// PortalURL is the Admin Portal's address, where an employee approves
	// a machine's request for an API key (/device).
	PortalURL string

	// CORSAllowedOrigins lets apps/admin-portal (a browser client, its own
	// origin) call this API — see httpapi.RouterConfig's doc comment.
	// Comma-separated; empty disables CORS.
	CORSAllowedOrigins []string

	// ScaleToZeroIdleTimeout / ScaleSweepInterval implement FR-052's
	// business rule that the idle threshold is a "platform-defined
	// default... TBD", not something deployment.yaml exposes to employees.
	// Defaults are a starting point pending a real Decision Log entry, not
	// a claimed-correct value; overridable via env for testing without
	// waiting minutes for a real idle window.
	ScaleToZeroIdleTimeout time.Duration
	ScaleSweepInterval     time.Duration

	// MetricsSampleInterval is how often Module T reads each running
	// container's CPU and memory (FR-090). An engineering default rather
	// than a policy value: nothing in the requirements sets a sampling
	// rate, and NFR-032's retention durations — the part that is policy —
	// are TBD, so nothing is purged either.
	MetricsSampleInterval time.Duration

	// HealthSweepInterval is how often Module R re-checks every already-
	// Running instance's health (FR-084), on top of the checks already run
	// at deploy/resume/restart/cold-start gate points. Same status as
	// MetricsSampleInterval above: an engineering default, not a value the
	// requirements specify.
	HealthSweepInterval time.Duration

	// PostActivationRollbackWindow is FR-099's "shortly after Traffic
	// Activation" — how long after a deployment reaches Running its own
	// health-check exhaustion is treated as "this version regressed"
	// (triggering an automatic rollback) rather than "an established
	// instance flaked" (Module R's existing restart-in-place/escalate
	// handling). Same engineering-default status as the other timeouts
	// above.
	PostActivationRollbackWindow time.Duration

	// OwnershipTransferWindow implements FR-016's "policy window" for a
	// nominated new owner to accept a transfer before it expires — exact
	// value TBD (docs/17_Decision_Log.md), same "starting point, not a
	// ratified value" status as the scale-to-zero timeouts above.
	OwnershipTransferWindow time.Duration

	// SecretKey is Module O's encryption key (FR-066), base64 — see
	// internal/secretbox. Required with no fallback, for the same reason
	// as DatabaseURL: a default would be a key published in source code.
	SecretKey string
}

// Load reads configuration from the environment. DatabaseURL has no
// hardcoded fallback — it always carries credentials, so it must come from
// the environment (docker-compose's .env locally; a real secret store in
// any shared environment, per DEC-006 in docs/17_Decision_Log.md) rather
// than a value baked into source code.
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (no default — see .env.example at the repo root)")
	}
	secretKey := os.Getenv("PLATFORM_SECRET_KEY")
	if secretKey == "" {
		return Config{}, fmt.Errorf("PLATFORM_SECRET_KEY is required (no default — generate one with `openssl rand -base64 32`; see .env.example at the repo root)")
	}
	idleTimeout, err := getEnvSeconds("SCALE_TO_ZERO_IDLE_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	sweepInterval, err := getEnvSeconds("SCALE_SWEEP_INTERVAL_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	transferWindow, err := getEnvSeconds("OWNERSHIP_TRANSFER_WINDOW_SECONDS", 7*24*3600) // 7 days
	if err != nil {
		return Config{}, err
	}
	metricsInterval, err := getEnvSeconds("METRICS_SAMPLE_INTERVAL_SECONDS", 15)
	if err != nil {
		return Config{}, err
	}
	healthSweepInterval, err := getEnvSeconds("HEALTH_SWEEP_INTERVAL_SECONDS", 15)
	if err != nil {
		return Config{}, err
	}
	postActivationRollbackWindow, err := getEnvSeconds("POST_ACTIVATION_ROLLBACK_WINDOW_SECONDS", 300) // 5 minutes
	if err != nil {
		return Config{}, err
	}
	authMode := getEnv("PLATFORM_AUTH_MODE", "token")
	platformEnv := getEnv("PLATFORM_ENV", "dev")
	enrollmentCode := os.Getenv("PLATFORM_ENROLLMENT_CODE")
	switch authMode {
	case "token":
		if len(enrollmentCode) < 8 {
			return Config{}, fmt.Errorf("PLATFORM_ENROLLMENT_CODE is required (at least 8 characters) — employees need it to create an account; see .env.example at the repo root")
		}
	case "dev-header":
		if platformEnv != "dev" {
			return Config{}, fmt.Errorf("PLATFORM_AUTH_MODE=dev-header trusts self-declared identity headers and is refused unless PLATFORM_ENV=dev")
		}
	default:
		return Config{}, fmt.Errorf("PLATFORM_AUTH_MODE must be \"token\" or \"dev-header\", got %q", authMode)
	}
	sessionTTL, err := getEnvSeconds("SESSION_TTL_SECONDS", 12*3600)
	if err != nil {
		return Config{}, err
	}
	apiKeyTTL, err := getEnvSeconds("API_KEY_TTL_SECONDS", 30*24*3600)
	if err != nil {
		return Config{}, err
	}
	apiKeyMaxTTL, err := getEnvSeconds("API_KEY_MAX_TTL_SECONDS", 90*24*3600)
	if err != nil {
		return Config{}, err
	}
	port := getEnv("PORT", "8080")
	return Config{
		AuthMode:                     authMode,
		EnrollmentCode:               enrollmentCode,
		AllowedEmailDomains:          getEnvList("PLATFORM_ALLOWED_EMAIL_DOMAINS", ""),
		SessionTTL:                   sessionTTL,
		APIKeyTTL:                    apiKeyTTL,
		APIKeyMaxTTL:                 apiKeyMaxTTL,
		PortalURL:                    strings.TrimRight(getEnv("PLATFORM_PORTAL_URL", "http://localhost:5173"), "/"),
		DatabaseURL:                  databaseURL,
		Port:                         port,
		PublicBaseURL:                strings.TrimRight(getEnv("PLATFORM_PUBLIC_BASE_URL", "http://localhost:"+port), "/"),
		PlatformEnv:                  platformEnv,
		CORSAllowedOrigins:           getEnvList("CORS_ALLOWED_ORIGINS", "http://localhost:5173"),
		ScaleToZeroIdleTimeout:       idleTimeout,
		ScaleSweepInterval:           sweepInterval,
		MetricsSampleInterval:        metricsInterval,
		HealthSweepInterval:          healthSweepInterval,
		PostActivationRollbackWindow: postActivationRollbackWindow,
		OwnershipTransferWindow:      transferWindow,
		SecretKey:                    secretKey,
	}, nil
}

func getEnvList(key, fallback string) []string {
	raw := getEnv(key, fallback)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvSeconds(key string, fallbackSeconds int) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(fallbackSeconds) * time.Second, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer number of seconds, got %q", key, v)
	}
	return time.Duration(n) * time.Second, nil
}
