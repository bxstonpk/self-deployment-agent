package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	Port        string
	PlatformEnv string // "dev" enables the temporary header-based auth stub; see DEC-001
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
	return Config{
		DatabaseURL: databaseURL,
		Port:        getEnv("PORT", "8080"),
		PlatformEnv: getEnv("PLATFORM_ENV", "dev"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
