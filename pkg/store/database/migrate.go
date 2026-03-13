package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

// migration represents a single database migration
type migration struct {
	version string
	name    string
}

// migrations is the ordered list of all migrations
var migrations = []migration{
	{"001", "001_initial.up"},
	{"002", "002_add_buffer.up"},
	{"003", "003_add_internal_fields.up"},
	{"004", "004_add_auth_tokens.up"},
	{"005", "005_add_credentials.up"},
	{"006", "006_add_upstream_credential.up"},
	{"007", "007_add_tool_repair.up"},
	{"008", "008_add_release_stream_chunk_deadline.up"},
}

// RunMigrations executes database schema migrations
func (s *Store) RunMigrations(ctx context.Context) error {
	// Ensure migrations tracking table exists
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	// Run each migration in order
	for _, m := range migrations {
		if err := s.runMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// runMigration executes a single migration if not already applied
func (s *Store) runMigration(ctx context.Context, m migration) error {
	applied, err := s.isMigrationApplied(ctx, m.version)
	if err != nil {
		return fmt.Errorf("failed to check migration %s: %w", m.version, err)
	}
	if applied {
		return nil
	}

	// Load SQL from file
	sql, err := s.loadMigrationSQL(m.name)
	if err != nil {
		return fmt.Errorf("failed to load migration %s: %w", m.version, err)
	}

	// Execute migration
	if _, err := s.DB.ExecContext(ctx, sql); err != nil {
		return fmt.Errorf("migration %s failed: %w", m.version, err)
	}

	// Record migration as applied
	if err := s.recordMigration(ctx, m.version); err != nil {
		return err
	}

	return nil
}

// loadMigrationSQL reads the migration SQL file for the current dialect
func (s *Store) loadMigrationSQL(name string) (string, error) {
	var fs embed.FS
	var dir string

	switch s.Dialect {
	case SQLite:
		fs = sqliteMigrations
		dir = "migrations/sqlite"
	case PostgreSQL:
		fs = postgresMigrations
		dir = "migrations/postgres"
	default:
		return "", fmt.Errorf("unsupported dialect: %s", s.Dialect)
	}

	filename := fmt.Sprintf("%s/%s.sql", dir, name)
	data, err := fs.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read migration file %s: %w", filename, err)
	}

	return string(data), nil
}

// isMigrationApplied checks if a migration version has already been applied
func (s *Store) isMigrationApplied(ctx context.Context, version string) (bool, error) {
	var query string

	switch s.Dialect {
	case PostgreSQL:
		query = `SELECT COUNT(*) FROM schema_migrations WHERE version = $1`
	default:
		query = `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`
	}

	// First, ensure the table exists (for edge cases)
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return false, err
	}

	var count int64
	err := s.DB.QueryRowContext(ctx, query, version).Scan(&count)
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
	var query string

	switch s.Dialect {
	case SQLite:
		timestamp = time.Now().UTC().Format("2006-01-02 15:04:05")
		query = `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`
	case PostgreSQL:
		timestamp = time.Now().UTC().Format(time.RFC3339)
		query = `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`
	default:
		timestamp = time.Now().UTC().Format(time.RFC3339)
		query = `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`
	}

	_, err := s.DB.ExecContext(ctx, query, version, timestamp)
	if err != nil {
		return fmt.Errorf("failed to record migration %s: %w", version, err)
	}

	return nil
}

// ensureMigrationsTable creates the schema_migrations table if it doesn't exist
func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	var query string

	switch s.Dialect {
	case SQLite:
		query = `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at TEXT NOT NULL
			)
		`
	case PostgreSQL:
		query = `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL
			)
		`
	default:
		return fmt.Errorf("unsupported dialect: %s", s.Dialect)
	}

	_, err := s.DB.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	return nil
}

// GetAppliedMigrations returns a list of applied migration versions
func (s *Store) GetAppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}

	return versions, nil
}
