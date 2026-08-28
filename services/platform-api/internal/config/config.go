package config

import "os"

type Config struct {
	DatabaseURL string
	Port        string
	PlatformEnv string // "dev" enables the temporary header-based auth stub; see DEC-001
}

func Load() Config {
	return Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/platform?sslmode=disable"),
		Port:        getEnv("PORT", "8080"),
		PlatformEnv: getEnv("PLATFORM_ENV", "dev"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
