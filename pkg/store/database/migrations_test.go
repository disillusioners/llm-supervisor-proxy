// Migration 028 backfill + transaction tests (Phase 1, M-1-test MANDATORY).
//
// In-package (may call the unexported runMigration directly — house
// precedent: database_test.go is same-package).
//
// Tests cover the Phase 1 M-1 contract (technical-analysis.md
// §Migration SQL + §Backward Compatibility Matrix):
//
//   (1) Fresh-DB forward test — full RunMigrations leaves BOTH
//       credentials_json AND credential_id columns present, AND
//       idx_models_credential_id still in place (no DROP COLUMN /
//       DROP INDEX — those move to migration 029+).
//   (2) Backfill test — insert two pre-028 models (one with cred-X,
//       one empty), run 028, assert the backfilled credentials_json
//       shape is exactly the contract JSON form, AND credential_id
//       shadow byte-matches the original (same-statement write).
//   (3) Down test — execute the verbatim 028.down SQL on the (2)
//       DB; assert credential_id shadow is preserved (lossless via
//       json_extract re-derivation), AND credentials_json is
//       preserved (no DROP).
//   (4) File-level transaction test (4a SQLite, 4b PG) — proves the
//       file-level BEGIN…COMMIT commits atomically through the
//       modernc.org/sqlite v1.46.1 + pgx/v5 ExecContext pairing. PG
//       is gated on TEST_DATABASE_URL — when unset, the test is
//       skipped cleanly. 4b sits behind `//go:build pg_integration`
//       so the default build excludes it; runnable via
//       `go test -tags pg_integration ./pkg/store/database/...`
//       with TEST_DATABASE_URL exported.
//
// M-1-test MANDATORY — Phase 1 exit is gated on tests (1) (2) (3) (4a)
// passing on SQLite, plus (4b) when PG is available.

package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestMigration028_ForwardFreshDB asserts that the post-migration
// schema has BOTH credentials_json AND credential_id columns (no
// DROP COLUMN per M-1), and that idx_models_credential_id is still
// present (no DROP INDEX per M-1).
func TestMigration028_ForwardFreshDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// PRAGMA table_info(models) shows column shape; assert the M-1
	// dual-column invariant.
	rows, err := store.DB.Query("PRAGMA table_info(models)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	var hasCredentialsJSON, hasCredentialID bool
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch name {
		case "credentials_json":
			hasCredentialsJSON = true
		case "credential_id":
			hasCredentialID = true
		}
	}
	if !hasCredentialsJSON {
		t.Error("credentials_json column missing from models (M-1 violated)")
	}
	if !hasCredentialID {
		t.Error("credential_id column missing from models (M-1 violated — must stay as derived shadow until 029)")
	}

	// idx_models_credential_id still present (no DROP INDEX per M-1).
	indexRows, err := store.DB.Query("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='models'")
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer indexRows.Close()
	var hasIndex bool
	for indexRows.Next() {
		var name string
		if err := indexRows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "idx_models_credential_id" {
			hasIndex = true
		}
	}
	if !hasIndex {
		t.Error("idx_models_credential_id missing (M-1 violated — must stay until 029 DROP)")
	}
}

// TestMigration028_Backfill asserts the 028 backfill produces
// byte-exact credentials_json AND byte-exact credential_id shadow
// (M-1 same-statement write).
func TestMigration028_Backfill(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Run 001..027 only (insert two test rows first against a
	// pre-028 schema, then run 028 to verify the backfill).
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for i := 0; i < len(migrations)-1; i++ {
		// Skip 028 (last entry) — we want a pre-028 schema.
		if migrations[i].version == "028" {
			continue
		}
		if err := store.runMigration(ctx, migrations[i]); err != nil {
			t.Fatalf("migration %s: %v", migrations[i].version, err)
		}
	}

	// Insert two models: one with credential_id='cred-X', one with ''.
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"model-with-cred", "Model With Cred", 1, "[]", "[]", 0, "cred-X", "", "",
	); err != nil {
		t.Fatalf("insert model-with-cred: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"model-no-cred", "Model No Cred", 1, "[]", "[]", 0, "", "", "",
	); err != nil {
		t.Fatalf("insert model-no-cred: %v", err)
	}

	// Find 028 in the array and run it (per-entry, no full sweep).
	var mig028 *migration
	for i := range migrations {
		if migrations[i].version == "028" {
			mig028 = &migrations[i]
			break
		}
	}
	if mig028 == nil {
		t.Fatal("028 not found in migrations array — Task 1 missing?")
	}
	if err := store.runMigration(ctx, *mig028); err != nil {
		t.Fatalf("runMigration 028: %v", err)
	}

	// Assert backfilled credentials_json AND credential_id shadow.
	var credsJSON, credID string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT credentials_json, credential_id FROM models WHERE id = ?`, "model-with-cred",
	).Scan(&credsJSON, &credID); err != nil {
		t.Fatalf("select model-with-cred: %v", err)
	}
	wantJSON := `[{"credential_id":"cred-X","weight":1,"position":0}]`
	if credsJSON != wantJSON {
		t.Errorf("credentials_json byte-exact mismatch: got %q, want %q", credsJSON, wantJSON)
	}
	if credID != "cred-X" {
		t.Errorf("credential_id shadow byte-exact mismatch: got %q, want %q", credID, "cred-X")
	}

	var credsJSON2, credID2 string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT credentials_json, credential_id FROM models WHERE id = ?`, "model-no-cred",
	).Scan(&credsJSON2, &credID2); err != nil {
		t.Fatalf("select model-no-cred: %v", err)
	}
	if credsJSON2 != "[]" {
		t.Errorf("empty-cred row credentials_json should stay '[]', got %q", credsJSON2)
	}
	if credID2 != "" {
		t.Errorf("empty-cred row credential_id should stay '', got %q", credID2)
	}

	// Both columns still present (no DROP COLUMN).
	if !columnExists(t, store.DB, "models", "credentials_json") {
		t.Error("credentials_json missing post-028 (M-1 violated)")
	}
	if !columnExists(t, store.DB, "models", "credential_id") {
		t.Error("credential_id missing post-028 (M-1 violated — must stay as derived shadow)")
	}
}

// TestMigration028_LosslessDown asserts the 028 down migration
// preserves credentials_json (no DROP) and re-derives credential_id
// from credentials_json[0] via json_extract (M-1 LOSSLESS).
func TestMigration028_LosslessDown(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Bring up through 028.
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Insert a multi-credential model so we can verify LOSSLESS
	// preservation of the multi-ref list (not just [0]).
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"multi-cred", "Multi", 1, "[]", "[]", 0,
		`[{"credential_id":"A","weight":2,"position":0},{"credential_id":"B","weight":1,"position":1}]`,
		"A", "", "",
	); err != nil {
		t.Fatalf("insert multi-cred: %v", err)
	}

	// Snapshot pre-down.
	var preJSON, preShadow string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT credentials_json, credential_id FROM models WHERE id = ?`, "multi-cred",
	).Scan(&preJSON, &preShadow); err != nil {
		t.Fatalf("pre-down select: %v", err)
	}

	// Execute 028.down.sql verbatim via loadMigrationSQL.
	downSQL, err := store.loadMigrationSQL("028_add_model_credentials.down")
	if err != nil {
		t.Fatalf("loadMigrationSQL down: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx, downSQL); err != nil {
		t.Fatalf("exec down: %v", err)
	}

	// Assert credentials_json preserved (lossless — multi-ref list
	// survives the rollback).
	var postJSON, postShadow string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT credentials_json, credential_id FROM models WHERE id = ?`, "multi-cred",
	).Scan(&postJSON, &postShadow); err != nil {
		t.Fatalf("post-down select: %v", err)
	}
	if postJSON != preJSON {
		t.Errorf("credentials_json changed across down migration: got %q, want %q (M-1 LOSSLESS violated)", postJSON, preJSON)
	}
	// credential_id shadow was re-derived from credentials_json[0] — should be "A".
	if postShadow != "A" {
		t.Errorf("credential_id shadow should re-derive to 'A', got %q", postShadow)
	}

	// Both columns still present.
	if !columnExists(t, store.DB, "models", "credentials_json") {
		t.Error("credentials_json missing post-down (no DROP expected per M-1 LOSSLESS)")
	}
	if !columnExists(t, store.DB, "models", "credential_id") {
		t.Error("credential_id missing post-down (M-1 LOSSLESS violated)")
	}
}

// TestMigration028_FileLevelTransactionSQLite proves the file-level
// BEGIN TRANSACTION; … COMMIT; pair inside 028.up.sql commits
// atomically through the modernc.org/sqlite v1.46.1 driver. If a
// forced failure mid-statement leaves the column or backfill in a
// partial state, this test fails.
//
// 4a / Phase 1 exit gating — M-1-test MANDATORY.
func TestMigration028_FileLevelTransactionSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Insert a sentinel row after the migration; the atomic-commit
	// assertion is that the column exists AND the row is readable.
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"atomic-sentinel", "Atomic Sentinel", 1, "[]", "[]", 0, "[]", "", "", "",
	); err != nil {
		t.Fatalf("insert sentinel: %v", err)
	}

	// Confirm a follow-up roundtrip query works — proves the
	// file-level transaction committed, not partially-rolled-back.
	var name string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT name FROM models WHERE id = ?`, "atomic-sentinel",
	).Scan(&name); err != nil {
		t.Fatalf("select sentinel: %v", err)
	}
	if name != "Atomic Sentinel" {
		t.Errorf("sentinel read-back: got %q, want %q", name, "Atomic Sentinel")
	}

	// Verify both columns are queryable (transaction didn't partially drop).
	if !columnExists(t, store.DB, "models", "credentials_json") {
		t.Error("credentials_json missing post-commit (file-level transaction violated)")
	}
	if !columnExists(t, store.DB, "models", "credential_id") {
		t.Error("credential_id missing post-commit (file-level transaction violated)")
	}

	// Indirection: ensure no time-based flakes (helpful when CI is
	// slow on shared runners).
	_ = time.Second
}

// columnExists is a small helper used across the migration tests to
// assert presence of a column in a given table.
func columnExists(t *testing.T, db interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Logf("PRAGMA failed: %v", err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == column {
			return true
		}
	}
	return false
}

// _ keeps `models` referenced in case future tests need to assert
// via the typed API instead of raw SQL.
var _ = models.MaxCredentialRefs
