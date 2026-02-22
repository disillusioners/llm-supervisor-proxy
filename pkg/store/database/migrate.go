package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RunMigrations executes database schema migrations
func (s *Store) RunMigrations(ctx context.Context) error {
	switch s.Dialect {
	case SQLite:
		return s.runSQLiteMigrations(ctx)
	case PostgreSQL:
		return s.runPostgreSQLMigrations(ctx)
	default:
		return fmt.Errorf("unsupported dialect: %s", s.Dialect)
	}
}

// isMigrationApplied checks if a migration version has already been applied
func (s *Store) isMigrationApplied(ctx context.Context, version string) (bool, error) {
	var count int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = $1`, version,
	).Scan(&count)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return count > 0, nil
}

// recordMigration records a migration as applied with timestamp
func (s *Store) recordMigration(ctx context.Context, version string) error {
	var timestamp string
	switch s.Dialect {
	case SQLite:
		timestamp = time.Now().UTC().Format("2006-01-02 15:04:05")
	case PostgreSQL:
		timestamp = time.Now().UTC().Format(time.RFC3339)
	default:
		timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`, version, timestamp,
	)
	if err != nil {
		return fmt.Errorf("failed to record migration %s: %w", version, err)
	}
	return nil
}

// ensureMigrationsTable creates the schema_migrations table if it doesn't exist
func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	switch s.Dialect {
	case SQLite:
		_, err := s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at TEXT NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create schema_migrations table: %w", err)
		}
	case PostgreSQL:
		_, err := s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create schema_migrations table: %w", err)
		}
	}
	return nil
}

func (s *Store) runSQLiteMigrations(ctx context.Context) error {
	// Ensure migrations tracking table exists (for first run compatibility)
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	// Migration 001: Create configs and models tables
	const migration001 = "001"
	applied, err := s.isMigrationApplied(ctx, migration001)
	if err != nil {
		return fmt.Errorf("failed to check migration %s: %w", migration001, err)
	}

	if !applied {
		// Create configs table
		_, err := s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS configs (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				version TEXT NOT NULL DEFAULT '1.0',
				upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
				port INTEGER NOT NULL DEFAULT 4321,
				idle_timeout_ms INTEGER NOT NULL DEFAULT 60000,
				max_generation_time_ms INTEGER NOT NULL DEFAULT 300000,
				max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
				max_idle_retries INTEGER NOT NULL DEFAULT 2,
				max_generation_retries INTEGER NOT NULL DEFAULT 1,
				loop_detection_json TEXT NOT NULL DEFAULT '{}',
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create configs table: %w", err)
		}

		// Create models table
		_, err = s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS models (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				fallback_chain_json TEXT NOT NULL DEFAULT '[]',
				truncate_params_json TEXT NOT NULL DEFAULT '[]',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create models table: %w", err)
		}

		// Create index
		_, err = s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled)`)
		if err != nil {
			return fmt.Errorf("failed to create models index: %w", err)
		}

		// Ensure default config row exists
		_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO configs (id) VALUES (1)`)
		if err != nil {
			return fmt.Errorf("failed to insert default config: %w", err)
		}

		// Record the migration
		if err := s.recordMigration(ctx, migration001); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) runPostgreSQLMigrations(ctx context.Context) error {
	// Ensure migrations tracking table exists (for first run compatibility)
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	// Migration 001: Create configs and models tables
	const migration001 = "001"
	applied, err := s.isMigrationApplied(ctx, migration001)
	if err != nil {
		return fmt.Errorf("failed to check migration %s: %w", migration001, err)
	}

	if !applied {
		// Create configs table with PostgreSQL syntax
		_, err := s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS configs (
				id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
				version TEXT NOT NULL DEFAULT '1.0',
				upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
				port INTEGER NOT NULL DEFAULT 4321,
				idle_timeout_ms BIGINT NOT NULL DEFAULT 60000,
				max_generation_time_ms BIGINT NOT NULL DEFAULT 300000,
				max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
				max_idle_retries INTEGER NOT NULL DEFAULT 2,
				max_generation_retries INTEGER NOT NULL DEFAULT 1,
				loop_detection_json JSONB NOT NULL DEFAULT '{}',
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create configs table: %w", err)
		}

		// Create models table
		_, err = s.DB.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS models (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				enabled BOOLEAN NOT NULL DEFAULT true,
				fallback_chain_json JSONB NOT NULL DEFAULT '[]',
				truncate_params_json JSONB NOT NULL DEFAULT '[]',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create models table: %w", err)
		}

		// Create index
		_, err = s.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled) WHERE enabled = true`)
		if err != nil {
			return fmt.Errorf("failed to create models index: %w", err)
		}

		// Ensure default config row exists
		_, err = s.DB.ExecContext(ctx, `
			INSERT INTO configs (id) VALUES (1)
			ON CONFLICT (id) DO NOTHING
		`)
		if err != nil {
			return fmt.Errorf("failed to insert default config: %w", err)
		}

		// Record the migration
		if err := s.recordMigration(ctx, migration001); err != nil {
			return err
		}
	}

	return nil
}

// IsEmpty checks if the database has no data (for migration purposes)
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	var count int64
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM configs WHERE id = 1`).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// HasModels checks if any models exist in the database
func (s *Store) HasModels(ctx context.Context) (bool, error) {
	var count int64
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM models`).Scan(&count)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return count > 0, nil
}
