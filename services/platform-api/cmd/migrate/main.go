// Command migrate applies pending schema migrations and exits. The api
// binary also self-migrates on boot for local-dev convenience; this
// standalone command exists for CI/CD pipelines and manual operations where
// running migrations as a distinct, auditable step is preferred.
package main

import (
	"context"
	"log"

	"platform-api/internal/config"
	"platform-api/internal/db"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	log.Println("migrations applied successfully")
}
