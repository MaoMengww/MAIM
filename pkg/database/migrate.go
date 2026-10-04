package database

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// RunMigrations reads embedded SQL migration files from the given filesystem,
// applies any that have not yet been recorded in schema_migrations, and records
// each newly applied migration. All SQL files must use IF NOT EXISTS / IF EXISTS
// so they are safe to re-run. Startup callers share a transaction-scoped lock:
// user-service and message-service may start concurrently against the same DB.
func RunMigrations(db *gorm.DB, src fs.FS) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(4278605, 1)").Error; err != nil {
			return fmt.Errorf("lock schema migrations: %w", err)
		}
		return runMigrations(tx, src)
	})
}

func runMigrations(db *gorm.DB, src fs.FS) error {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return fmt.Errorf("read migration dir: %w", err)
	}

	// Sort lexically: 000_xxx.sql < 001_xxx.sql < …
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// Ensure tracking table exists (public schema — shared by all services)
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS public.schema_migrations (
		version    VARCHAR(255) PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`).Error; err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	// Upgrade column width for databases created before VARCHAR(16) was widened.
	if err := db.Exec(`ALTER TABLE public.schema_migrations ALTER COLUMN version TYPE VARCHAR(255)`).Error; err != nil {
		return fmt.Errorf("upgrade schema_migrations: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		// Skip rollback scripts (files ending in .down.sql)
		if strings.HasSuffix(name, ".down.sql") {
			continue
		}
		version := strings.TrimSuffix(name, ".sql")

		// Check if already applied
		var count int64
		if err := db.Raw(
			"SELECT COUNT(*) FROM public.schema_migrations WHERE version = ?", version,
		).Scan(&count).Error; err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if count > 0 {
			continue
		}

		content, err := fs.ReadFile(src, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if err := db.Exec(string(content)).Error; err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if err := db.Exec(
			"INSERT INTO public.schema_migrations (version) VALUES (?)", version,
		).Error; err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}
