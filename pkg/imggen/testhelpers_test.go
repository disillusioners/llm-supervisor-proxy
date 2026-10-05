package imggen

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// openSQLiteForUsage creates a fresh in-memory SQLite with the
// token_hourly_usage + model_hourly_usage tables (image_count
// column included per migration 029) for the handler test
// suite's metering assertions. Returns the *sql.DB which the
// usage.Counter can use directly with the "sqlite" dialect.
func openSQLiteForUsage() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS token_hourly_usage (
		token_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		image_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (token_id, hour_bucket)
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_hourly_usage (
		model_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		image_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (model_id, hour_bucket)
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
