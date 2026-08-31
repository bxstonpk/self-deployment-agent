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
	PlatformEnv string // "dev" enables the temporary header-based auth stub; see DEC-001

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
	idleTimeout, err := getEnvSeconds("SCALE_TO_ZERO_IDLE_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	sweepInterval, err := getEnvSeconds("SCALE_SWEEP_INTERVAL_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	return Config{
		DatabaseURL:            databaseURL,
		Port:                   getEnv("PORT", "8080"),
		PlatformEnv:            getEnv("PLATFORM_ENV", "dev"),
		CORSAllowedOrigins:     getEnvList("CORS_ALLOWED_ORIGINS", "http://localhost:5173"),
		ScaleToZeroIdleTimeout: idleTimeout,
		ScaleSweepInterval:     sweepInterval,
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
