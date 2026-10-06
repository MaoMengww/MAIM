package database

import (
	"fmt"
	"io/fs"
	"strings"

	"gorm.io/gorm"
)

// RunMigrations applies the current UUID migration lineage once, sharing a
// transaction-scoped lock with direct PostgreSQL initialization. It refuses
// records from removed migration lineages rather than mapping or clearing old
// entity data. The baseline also rejects untracked legacy tables atomically.
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

	// fs.ReadDir returns names in lexical migration order.
	migrations := make([]string, 0, len(entries))
	available := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		migrations = append(migrations, name)
		available[strings.TrimSuffix(name, ".sql")] = struct{}{}
	}

	// Ensure tracking table exists (public schema — shared by all services)
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS public.schema_migrations (
		version    VARCHAR(255) PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`).Error; err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var applied []string
	if err := db.Raw("SELECT version FROM public.schema_migrations ORDER BY version").Scan(&applied).Error; err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	recorded := make(map[string]struct{}, len(applied))
	for _, version := range applied {
		if _, current := available[version]; !current {
			return fmt.Errorf("migration %s belongs to an unsupported schema lineage; initialize a new AIM database without mapping or clearing legacy data", version)
		}
		recorded[version] = struct{}{}
	}

	for _, name := range migrations {
		version := strings.TrimSuffix(name, ".sql")

		if _, applied := recorded[version]; applied {
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
			"INSERT INTO public.schema_migrations (version) VALUES (?) ON CONFLICT DO NOTHING", version,
		).Error; err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}
