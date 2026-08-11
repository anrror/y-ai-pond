// Command migrate applies PostgreSQL schema migrations.
// Usage: go run ./cmd/migrate/ [--dsn=<dsn>]
// Default DSN from config/config.yaml: postgres://pond:pond@localhost:5432/y-ai-pond?sslmode=disable
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anrror/y-ai-pond/pkg/store"
)

func main() {
	dsn := flag.String("dsn", "postgres://pond:pond@localhost:5432/y-ai-pond?sslmode=disable", "PostgreSQL DSN")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	migrations, err := store.LoadMigrations()
	if err != nil {
		logger.Error("failed to load migrations", "error", err)
		os.Exit(1)
	}

	if len(migrations) == 0 {
		logger.Warn("no migrations found")
		return
	}

	logger.Info("connecting to PostgreSQL", "dsn", maskPassword(*dsn))
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		logger.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if pingErr := pool.Ping(ctx); pingErr != nil {
		logger.Error("failed to ping PostgreSQL", "error", pingErr)
		os.Exit(1)
	}

	// Ensure tracking table exists.
	if _, execErr := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); execErr != nil {
		logger.Error("failed to create schema_migrations table", "error", execErr)
		os.Exit(1)
	}

	// Read already-applied migrations.
	applied := make(map[string]bool)
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		logger.Error("failed to query applied migrations", "error", err)
		os.Exit(1)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			logger.Error("failed to scan migration name", "error", err)
			os.Exit(1)
		}
		applied[name] = true
	}
	rows.Close()

	// Apply pending migrations.
	appliedCount := 0
	for _, mig := range migrations {
		if applied[mig.Name] {
			logger.Info("skipping (already applied)", "migration", mig.Name)
			continue
		}

		logger.Info("applying migration", "migration", mig.Name)
		tx, err := pool.Begin(ctx)
		if err != nil {
			logger.Error("failed to begin transaction", "migration", mig.Name, "error", err)
			os.Exit(1)
		}

		if _, err := tx.Exec(ctx, mig.SQL); err != nil {
			_ = tx.Rollback(ctx)
			logger.Error("failed to execute migration", "migration", mig.Name, "error", err)
			os.Exit(1)
		}

		insertSQL := fmt.Sprintf(
			`INSERT INTO schema_migrations (name) VALUES ('%s')`,
			store.SanitizeMigrationName(mig.Name),
		)
		if _, err := tx.Exec(ctx, insertSQL); err != nil {
			_ = tx.Rollback(ctx)
			logger.Error("failed to record migration", "migration", mig.Name, "error", err)
			os.Exit(1)
		}

		if err := tx.Commit(ctx); err != nil {
			logger.Error("failed to commit migration", "migration", mig.Name, "error", err)
			os.Exit(1)
		}
		appliedCount++
	}

	logger.Info("migrations complete", "migrations_found", len(migrations), "applied", appliedCount)
}

// maskPassword hides the password portion of a PostgreSQL DSN.
func maskPassword(dsn string) string {
	// postgres://user:password@host:port/db?params
	if idx := strings.Index(dsn, "://"); idx > 0 {
		rest := dsn[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx > 0 {
			userPart := rest[:atIdx]
			if colonIdx := strings.Index(userPart, ":"); colonIdx > 0 {
				return dsn[:idx+3+colonIdx+1] + "***" + dsn[idx+3+atIdx:]
			}
		}
	}
	return dsn
}
