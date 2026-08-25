//go:build pg_integration
// +build pg_integration

// Migration 028 PG transaction test (Task 11 test 4b — Phase 1
// M-1-test MANDATORY).
//
// Gated behind `//go:build pg_integration` so the default build
// skips it. Run with:
//
//	TEST_DATABASE_URL="postgres://user:pass@host:5432/db?sslmode=disable" \
//	  go test -tags pg_integration -run TestMigration028_PGFileLevelTransaction \
//	  ./pkg/store/database/...
//
// Proves the file-level BEGIN…COMMIT pair inside the PG 028.up.sql
// variant commits atomically through pgx/v5 ExecContext — the same
// runner path the repo uses for every other migration. 028 is the
// repo's first file-level transactional migration through pgx/v5
// (PG migration 024's BEGIN is inside PL/pgSQL only), so this test
// is the only proof that the runner + driver pairing honors the
// file-level pair.
//
// If TEST_DATABASE_URL is not set the test is skipped cleanly — no
// failure on machines without PG access. Documented in the test
// file's package-level comment per the Phase 1 exit-criterion
// gate (Risk P1-8).

package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func TestMigration028_PGFileLevelTransaction(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Skipping: TEST_DATABASE_URL not set. Set it to run PG migration tests.")
	}

	ctx := context.Background()
	store, err := newPostgreSQLConnection(ctx, dsn)
	if err != nil {
		t.Fatalf("PG connect: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations (PG): %v", err)
	}

	// Assert both columns present after file-level transaction commit.
	if !columnExistsPG(t, store.DB, "models", "credentials_json") {
		t.Error("PG: credentials_json missing post-028 (file-level transaction violated)")
	}
	if !columnExistsPG(t, store.DB, "models", "credential_id") {
		t.Error("PG: credential_id missing post-028 (M-1 derived shadow violated)")
	}

	// Insert + read-back sentinel proves the transaction committed.
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		"pg-atomic-sentinel", "PG Atomic Sentinel", true, "[]", "[]", false, "[]", "", "", "",
	); err != nil {
		t.Fatalf("insert sentinel: %v", err)
	}

	var name string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT name FROM models WHERE id = $1`, "pg-atomic-sentinel",
	).Scan(&name); err != nil {
		t.Fatalf("select sentinel: %v", err)
	}
	if name != "PG Atomic Sentinel" {
		t.Errorf("sentinel read-back: got %q, want %q", name, "PG Atomic Sentinel")
	}
}

// columnExistsPG mirrors the SQLite columnExists helper for PG.
// PG exposes catalog info via information_schema.columns.
func columnExistsPG(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
		table, column,
	).Scan(&count); err != nil {
		t.Logf("information_schema query failed: %v", err)
		return false
	}
	return count > 0
}
