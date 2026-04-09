package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	_ "modernc.org/sqlite"             // Pure Go SQLite driver
)

// Dialect represents the database type
type Dialect string

const (
	SQLite     Dialect = "sqlite"
	PostgreSQL Dialect = "postgres"
)

// Default connection pool settings
const (
	DefaultMaxOpenConns    = 25
	DefaultMaxIdleConns    = 10
	DefaultConnMaxLifetime = 5 * time.Minute
	DefaultConnMaxIdleTime = 1 * time.Minute
)

// envInt parses an int from an environment variable, returning defaultVal if not set or invalid
func envInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

// envDuration parses a duration from an environment variable, returning defaultVal if not set or invalid
func envDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if durationVal, err := time.ParseDuration(val); err == nil {
			return durationVal
		}
	}
	return defaultVal
}

// configurePool applies connection pool settings to a database handle
func configurePool(db *sql.DB) {
	db.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS", DefaultMaxOpenConns))
	db.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS", DefaultMaxIdleConns))
	db.SetConnMaxLifetime(envDuration("DB_CONN_MAX_LIFETIME", DefaultConnMaxLifetime))
	db.SetConnMaxIdleTime(envDuration("DB_CONN_MAX_IDLE_TIME", DefaultConnMaxIdleTime))
}

// Store provides database access for both config and models
type Store struct {
	DB      *sql.DB
	Dialect Dialect
	dbPath  string // For SQLite, the path to the database file
}

// NewConnection creates a database connection based on DATABASE_URL env var
// If DATABASE_URL is empty or starts with "sqlite:", uses SQLite
// Otherwise uses PostgreSQL via pgx driver
func NewConnection(ctx context.Context) (*Store, error) {
	dbURL := os.Getenv("DATABASE_URL")

	// Default to SQLite if no DATABASE_URL
	if dbURL == "" {
		return newSQLiteConnection()
	}

	// Check if it's a SQLite URL
	if strings.HasPrefix(dbURL, "sqlite:") {
		path := strings.TrimPrefix(dbURL, "sqlite:")
		if path == "" {
			// Default SQLite path
			return newSQLiteConnection()
		}
		return newSQLiteConnectionAtPath(path)
	}

	// PostgreSQL connection
	return newPostgreSQLConnection(ctx, dbURL)
}

func newSQLiteConnection() (*Store, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}
	dbPath := filepath.Join(cfgDir, "llm-supervisor-proxy", "config.db")
	return newSQLiteConnectionAtPath(dbPath)
}

func newSQLiteConnectionAtPath(dbPath string) (*Store, error) {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Enable foreign keys for SQLite
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Configure connection pool
	configurePool(db)

	return &Store{DB: db, Dialect: SQLite, dbPath: dbPath}, nil
}

func newPostgreSQLConnection(ctx context.Context, dsn string) (*Store, error) {
	// Import pgx driver
	importPgxDriver()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	// Configure connection pool
	configurePool(db)

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return &Store{DB: db, Dialect: PostgreSQL, dbPath: "[postgresql]"}, nil
}

// importPgxDriver ensures the pgx driver is registered
// This is a separate function to allow conditional compilation if needed
func importPgxDriver() {
	// The pgx driver is imported via build tags or direct import
	// We use a simple reference to ensure the driver is linked
	_ = "pgx"
}

// Close closes the database connection
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

// Ping tests the database connection
func (s *Store) Ping(ctx context.Context) error {
	return s.DB.PingContext(ctx)
}
