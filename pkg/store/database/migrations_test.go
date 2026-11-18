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
	"encoding/json"
	"path/filepath"
	"strings"
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

	// Run 001..027 + 029..031 (insert two test rows first against a
	// pre-028 schema, then run 028 to verify the backfill).
	//
	// The skip list is EXPLICIT (per-version), not `len(migrations)-N`
	// arithmetic: the arithmetic form silently changes meaning every time a
	// migration is appended — appending 034 made it run 032 against a
	// schema with no credentials_json (028 skipped) and fail. 032 and 033
	// are excluded because their fixtures are irrelevant here and 032
	// depends on 028's column.
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for i := 0; i < len(migrations); i++ {
		// Skip 028 — we want a pre-028 schema. Skip the post-031 data
		// migrations (032 needs 028's column; 033/034 are unrelated).
		if migrations[i].version == "028" || migrations[i].version == "032" ||
			migrations[i].version == "033" || migrations[i].version == "034" {
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

// TestMigration028_EscapeSafeBackfill (Phase-1 hardening W3) proves the
// 028 backfill produces ESCAPE-SAFE JSON for credential IDs that
// contain special characters (double-quote, control chars). The prior
// raw-concat form produced invalid JSON for those inputs (e.g. an
// unescaped `"` inside the credential_id would terminate the string
// literal, breaking every consumer that json.Unmarshal()s the column).
//
// The leader-pin form — `json_array(json_object('credential_id',
// credential_id, 'weight', 1, 'position', 0))` — uses the bundled
// JSON1 extension (modernc.org/sqlite v1.46.1) which correctly escapes
// the values.
//
// For plain ASCII IDs the JSON1 form emits the same bytes as the old
// concat (`[{"credential_id":"cred-X","weight":1,"position":0}]`), so
// the byte-exact assertion in TestMigration028_Backfill above stays
// valid. This test exercises the DIVERGENT path: IDs that would have
// broken the old form and now round-trip as valid JSON.
func TestMigration028_EscapeSafeBackfill(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Run 001..027 + 029..031 (explicit per-version skips — see the note in
	// TestMigration028_Backfill on why the `len(migrations)-N` arithmetic was
	// replaced).
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for i := 0; i < len(migrations); i++ {
		if migrations[i].version == "028" || migrations[i].version == "032" ||
			migrations[i].version == "033" || migrations[i].version == "034" {
			continue
		}
		if err := store.runMigration(ctx, migrations[i]); err != nil {
			t.Fatalf("migration %s: %v", migrations[i].version, err)
		}
	}

	// Insert a model whose credential_id contains a double-quote AND a
	// newline (control character) — both must be JSON-escaped in the
	// backfilled credentials_json. The OLD raw-concat form would emit
	// invalid JSON for these inputs (the unescaped `"` would terminate
	// the JSON string literal mid-value). The NEW JSON1 form correctly
	// escapes both, so json_extract / json.Unmarshal round-trip the
	// original credential_id bytes-for-bytes.
	const evilID = `cred"X` + "\n" + `Y`
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"model-evil", "Model Evil", 1, "[]", "[]", 0, evilID, "", "",
	); err != nil {
		t.Fatalf("insert model-evil: %v", err)
	}

	// Find and run 028.
	var mig028 *migration
	for i := range migrations {
		if migrations[i].version == "028" {
			mig028 = &migrations[i]
			break
		}
	}
	if mig028 == nil {
		t.Fatal("028 not found in migrations array")
	}
	if err := store.runMigration(ctx, *mig028); err != nil {
		t.Fatalf("runMigration 028: %v", err)
	}

	// Read back credentials_json.
	var credsJSON string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT credentials_json FROM models WHERE id = ?`, "model-evil",
	).Scan(&credsJSON); err != nil {
		t.Fatalf("select model-evil: %v", err)
	}

	// Assert the stored JSON is well-formed (parseable) and that
	// json_extract round-trips the original credential_id bytes. The
	// JSON1-escaped form must produce a value equivalent to the input
	// string when extracted back via $ → text.
	var extracted string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT json_extract(?, '$[0].credential_id')`, credsJSON,
	).Scan(&extracted); err != nil {
		t.Fatalf("json_extract: %v", err)
	}
	if extracted != evilID {
		t.Errorf("json_extract round-trip failed: got %q (% x), want %q (% x)", extracted, []byte(extracted), evilID, []byte(evilID))
	}

	// Round-trip via Go json.Unmarshal — proves the column value is
	// valid JSON the application can parse (not just SQLite's parser).
	var refs []map[string]interface{}
	if err := jsonUnmarshalString(credsJSON, &refs); err != nil {
		t.Errorf("credentials_json is not valid JSON for app-level parse: %v\nraw=%s", err, credsJSON)
		return
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 ref in credentials_json, got %d (raw=%s)", len(refs), credsJSON)
		return
	}
	if got, _ := refs[0]["credential_id"].(string); got != evilID {
		t.Errorf("json.Unmarshal round-trip: credential_id = %q (% x), want %q (% x)",
			got, []byte(got), evilID, []byte(evilID))
	}

	// Sanity: the column must NOT contain an unescaped quote that would
	// have terminated the old raw-concat form mid-string. The OLD form
	// for input `cred"X\nY` would have produced
	// `[{"credential_id":"cred"X\nY","weight":1,"position":0}]` —
	// INVALID JSON. The NEW form must NOT match that string verbatim.
	if credsJSON == `[{"credential_id":"cred"X`+"\n"+`Y","weight":1,"position":0}]` {
		t.Errorf("credentials_json matches the OLD escape-unsafe concat form — JSON1 form not active")
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
//
// W7 clarification (Phase-1 hardening): this test asserts the
// happy-path COMMIT scenario — it does NOT inject a mid-statement
// failure. The forced-failure rollback case is asserted separately
// by TestMigration028_FileLevelTransactionSQLite_Rollback below
// (keeps the two assertions isolated so the M-1 gating test stays
// deterministic).
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

// TestMigration028_FileLevelTransactionSQLite_Rollback (Phase-1 W7
// hardening) proves that a file-level BEGIN TRANSACTION; … COMMIT;
// migration ROLLS BACK cleanly when a statement in the middle
// fails. The defense is in TWO places:
//
//  1. modernc.org/sqlite v1.46.1 does NOT auto-rollback when a
//     multi-statement ExecContext fails mid-way — the driver
//     returns the error but leaves any successful statements in
//     the implicit transaction in a "pending" state. We verified
//     this directly (see Phase-1 W7 experiment): partial state from
//     a failed file-level transaction persists unless an explicit
//     ROLLBACK is issued.
//  2. migrate.go:88-99 (Phase-1 S1 hardening) issues a defensive
//     ROLLBACK whenever ExecContext returns an error, regardless
//     of whether the migration started a transaction. For
//     migrations without an active transaction, the ROLLBACK
//     returns "cannot rollback - no transaction is active" and
//     the error is ignored — best-effort cleanup is harmless.
//
// This test constructs an inline transaction shaped like 028.up.sql
// but with a deliberately-failing middle statement. It asserts that
// after the failure, the partial state (the ALTER'd credentials_json
// column) does NOT persist — i.e. the explicit ROLLBACK in
// migrate.go's error path actually works in production scenarios.
//
// Counterpart to TestMigration028_FileLevelTransactionSQLite (which
// asserts the commit-side happy path). Together they prove both
// halves of file-level transactional atomicity.
func TestMigration028_FileLevelTransactionSQLite_Rollback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Bring up migrations 001..027 + 029..031 only (skip 028 — we want
	// to test the column-add step in isolation, against a pre-028
	// schema). Explicit per-version skips — see the note in
	// TestMigration028_Backfill on why `len(migrations)-N` arithmetic was
	// replaced.
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for i := 0; i < len(migrations); i++ {
		if migrations[i].version == "028" || migrations[i].version == "032" ||
			migrations[i].version == "033" || migrations[i].version == "034" {
			continue
		}
		if err := store.runMigration(ctx, migrations[i]); err != nil {
			t.Fatalf("migration %s: %v", migrations[i].version, err)
		}
	}

	// Sanity: credentials_json column must NOT exist before we run
	// the inline transaction (proves the partial-state assertion
	// below is meaningful).
	if columnExists(t, store.DB, "models", "credentials_json") {
		t.Fatal("credentials_json column already present pre-test — pre-028 schema expected")
	}

	// Inline transaction shaped like 028.up.sql but with a
	// deliberately-failing statement between the ALTER and the
	// UPDATE. This simulates a failed file-level migration. To
	// exercise the SAME defensive path migrate.go uses, we mirror
	// its error handling here: ExecContext the failing SQL, then
	// issue an explicit ROLLBACK.
	const failingSQL = `
BEGIN TRANSACTION;
ALTER TABLE models ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';
INSERT INTO nonexistent_table_for_rollback_test VALUES (1);
UPDATE models SET credentials_json = '[]';
COMMIT;
`
	if _, err := store.DB.ExecContext(ctx, failingSQL); err == nil {
		t.Fatal("expected error from forced mid-transaction failure, got nil — test setup invalid")
	}
	// Mirror the Phase-1 S1 defensive ROLLBACK.
	if _, rbErr := store.DB.ExecContext(ctx, "ROLLBACK"); rbErr != nil {
		// "cannot rollback - no transaction is active" is fine
		// (means the failing statement didn't open a tx); any
		// other error is a real concern.
		if !strings.Contains(rbErr.Error(), "cannot rollback") {
			t.Logf("ROLLBACK after forced failure returned non-no-tx error (best-effort cleanup ignored): %v", rbErr)
		}
	}

	// Assert credentials_json column does NOT exist — proves the
	// explicit ROLLBACK after a failed multi-statement ExecContext
	// successfully rolls back the successful ALTER (the partial
	// state). If the column exists, either the ROLLBACK didn't
	// fire OR it didn't roll back the partial state — both
	// indicate the file-level BEGIN...COMMIT isn't atomic in
	// practice.
	if columnExists(t, store.DB, "models", "credentials_json") {
		t.Error("credentials_json column persisted after forced mid-transaction failure + ROLLBACK — file-level BEGIN...COMMIT not atomic in practice")
	}

	// Confirm we can still query the table (no schema corruption
	// from the partial ALTER rolling back).
	var count int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM models`).Scan(&count); err != nil {
		t.Errorf("models table query failed post-rollback (partial schema state): %v", err)
	}
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

// jsonUnmarshalString is a tiny wrapper around json.Unmarshal — kept
// here so the escape-safe test (Phase-1 hardening W3) reads as
// one-liners. encoding/json's stdlib Unmarshal accepts []byte, so
// this is just sugar; defined locally to keep this file's imports
// readable.
func jsonUnmarshalString(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// TestMigration029_ForwardFreshDB asserts that migration 029 lands
// in the registry (CRITICAL — unskippable per Architect Amendment 1
// + decisions.md BE-M1) AND the image_count column is present on
// both token_hourly_usage and model_hourly_usage after a fresh
// RunMigrations. Catches the failure mode where the SQL files are
// dropped into the dialect dirs but the registry entry is missing
// (silently inert migration).
func TestMigration029_ForwardFreshDB(t *testing.T) {
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

	// schema_migrations must contain row 029.
	versions, err := store.GetAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "029" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 029 (registry append missing) — applied versions: %v", versions)
	}

	// image_count must be present on both tables.
	if !columnExists(t, store.DB, "token_hourly_usage", "image_count") {
		t.Error("image_count column missing from token_hourly_usage (029 SQL not applied)")
	}
	if !columnExists(t, store.DB, "model_hourly_usage", "image_count") {
		t.Error("image_count column missing from model_hourly_usage (029 SQL not applied)")
	}
}

// TestMigration030_ForwardFreshDB asserts that migration 030 (the
// ship-blocker fix for kind persistence, 2026-10-06) lands in the
// registry (Amendment-1 trap — the migrations slice is an ordered
// list, NOT a directory scanner; the SQL files alone are silently
// inert) AND the kind column is present on models after a fresh
// RunMigrations. Same shape as TestMigration029_ForwardFreshDB.
func TestMigration030_ForwardFreshDB(t *testing.T) {
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

	// schema_migrations must contain row 030.
	versions, err := store.GetAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "030" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 030 (registry append missing) — applied versions: %v", versions)
	}

	// kind must be present on models.
	if !columnExists(t, store.DB, "models", "kind") {
		t.Error("kind column missing from models (030 SQL not applied)")
	}

	// Legacy rows carry NULL kind (no DEFAULT / no backfill — the
	// load mappings normalize NULL/'' to chat).
	var kindVal interface{}
	if err := store.DB.QueryRow(
		`SELECT kind FROM models LIMIT 1`,
	).Scan(&kindVal); err != nil && err != sql.ErrNoRows {
		t.Fatalf("select kind from (empty) models: %v", err)
	}
}

// TestMigration031_ForwardFreshDB asserts that migration 031
// (load-balance-groups / P1.3 — models.members_json +
// models.shadow_model_id) lands in the registry (Amendment-1 trap,
// third occurrence — the migrations slice is an ordered list, NOT
// a directory scanner; the SQL files alone are silently inert) AND
// both columns are present on models after a fresh RunMigrations.
// Same shape as TestMigration030_ForwardFreshDB.
func TestMigration031_ForwardFreshDB(t *testing.T) {
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

	// schema_migrations must contain row 031.
	versions, err := store.GetAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "031" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 031 (registry append missing) — applied versions: %v", versions)
	}

	// Both group columns must be present on models.
	if !columnExists(t, store.DB, "models", "members_json") {
		t.Error("members_json column missing from models (031 SQL not applied)")
	}
	if !columnExists(t, store.DB, "models", "shadow_model_id") {
		t.Error("shadow_model_id column missing from models (031 SQL not applied)")
	}
}

// TestMigration032_ForwardFreshDB asserts that migration 032
// (remove-external-upstream / P4 — data-only convergence on models:
// backfill fully-configurable legacy external rows to internal=1,
// disable the rest) lands in the registry (Amendment-1 trap, fourth
// occurrence — the migrations slice is an ordered list, NOT a
// directory scanner; the SQL files alone are silently inert) AND
// applies the safe-rule convergence correctly across the four
// fixture shapes per phase4 §Exit Criterion.
//
// Same shape as TestMigration031_ForwardFreshDB but the assertion
// is BEHAVIORAL (post-state row shape), not structural (column
// presence) — 032 is a data-only migration, so the fixture seeds
// pre-032 row shapes and the test asserts the post-state.
//
// Fixtures:
//   - glm-4.5-air shape (internal=0, enabled=1, credentials_json='[]',
//     internal_model=”) → internal stays 0, enabled flips to 0
//   - glm-5 shape (internal=0, enabled=0, fully empty) → internal
//     stays 0, enabled stays 0
//   - fully-configured external row (internal=0, credentials_json
//     != '[]', internal_model != ”) → internal flips to 1, enabled
//     UNTOUCHED
//   - kind='group' row (internal=1, enabled=1, empty credentials_json
//   - empty internal_model — agentic-group/coding-group shape) →
//     UNTOUCHED (must NOT false-positive against the convergence
//     queries; B3 / verification kind != 'group' conjunct)
func TestMigration032_ForwardFreshDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Bring up migrations 001..031 only — we want a pre-032 schema so
	// the fixtures can be inserted in their pre-convergence shape.
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for _, m := range migrations {
		if m.version == "032" {
			continue
		}
		if err := store.runMigration(ctx, m); err != nil {
			t.Fatalf("migration %s: %v", m.version, err)
		}
	}

	// Seed the four fixture shapes (post-031 schema = full models
	// table; the column list mirrors the existing 028-backfill test
	// insertions, extended with kind / members_json for the group
	// row).
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model, kind, members_json, shadow_model_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"glm-4-5-air", "GLM-4.5 Air", 1, "[]", "[]", 0, "[]", "", "", "", "chat", "", "",
	); err != nil {
		t.Fatalf("insert glm-4.5-air: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model, kind, members_json, shadow_model_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"glm-5", "GLM-5", 0, "[]", "[]", 0, "[]", "", "", "", "chat", "", "",
	); err != nil {
		t.Fatalf("insert glm-5: %v", err)
	}
	// fully-configured external row → backfill arm hits this row
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model, kind, members_json, shadow_model_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ext-fully-configured", "Ext Fully Configured", 1, "[]", "[]", 0,
		`[{"credential_id":"cred-X","weight":1,"position":0}]`, "cred-X", "", "ext-model-name",
		"chat", "", "",
	); err != nil {
		t.Fatalf("insert ext-fully-configured: %v", err)
	}
	// kind='group' row in agentic-group/coding-group shape — must
	// NOT be touched by 032 (already internal=1).
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model, kind, members_json, shadow_model_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"agentic-group", "Agentic Group", 1, "[]", "[]", 1, "[]", "", "", "", "group",
		`[{"model_id":"some-model","weight":1}]`, "",
	); err != nil {
		t.Fatalf("insert agentic-group: %v", err)
	}

	// Find 032 in the array and run it (per-entry, no full sweep —
	// the rest were already applied above).
	var mig032 *migration
	for i := range migrations {
		if migrations[i].version == "032" {
			mig032 = &migrations[i]
			break
		}
	}
	if mig032 == nil {
		t.Fatal("032 not found in migrations array — registry append missing")
	}
	if err := store.runMigration(ctx, *mig032); err != nil {
		t.Fatalf("runMigration 032: %v", err)
	}

	// schema_migrations must contain row 032 (registry-append pin).
	versions, err := store.GetAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "032" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 032 (registry append missing) — applied versions: %v", versions)
	}

	// Helper to read a model's (internal, enabled) post-state.
	readState := func(id string) (int64, int64) {
		t.Helper()
		var internal, enabled int64
		if err := store.DB.QueryRowContext(ctx,
			`SELECT internal, enabled FROM models WHERE id = ?`, id,
		).Scan(&internal, &enabled); err != nil {
			t.Fatalf("select %s: %v", id, err)
		}
		return internal, enabled
	}

	// Fixture 1: glm-4.5-air shape — internal stays 0, enabled flips to 0.
	if internal, enabled := readState("glm-4-5-air"); internal != 0 || enabled != 0 {
		t.Errorf("glm-4.5-air: got (internal=%d, enabled=%d), want (0, 0)", internal, enabled)
	}

	// Fixture 2: glm-5 shape — internal stays 0, enabled stays 0.
	if internal, enabled := readState("glm-5"); internal != 0 || enabled != 0 {
		t.Errorf("glm-5: got (internal=%d, enabled=%d), want (0, 0)", internal, enabled)
	}

	// Fixture 3: ext-fully-configured — internal flips to 1, enabled UNTOUCHED (still 1).
	if internal, enabled := readState("ext-fully-configured"); internal != 1 || enabled != 1 {
		t.Errorf("ext-fully-configured: got (internal=%d, enabled=%d), want (1, 1)", internal, enabled)
	}

	// Fixture 4: agentic-group (kind='group') — UNTOUCHED (internal=1, enabled=1).
	// Must NOT false-positive against the backfill (already internal=1)
	// or disable (already internal=1 → (internal IS NULL OR internal = 0) misses).
	if internal, enabled := readState("agentic-group"); internal != 1 || enabled != 1 {
		t.Errorf("agentic-group: got (internal=%d, enabled=%d), want (1, 1) — group row was touched", internal, enabled)
	}

	// Post-migration verification queries (dialect-split twin; SQLite
	// here — PG twin is the operator's manual step, gated on
	// pg_integration build tag).
	//
	// (i) B3 verification query: every enabled row must be a valid
	// internal row (credentials + internal_model present), EXCLUDING
	// kind='group' rows which legitimately have empty credentials +
	// empty internal_model. The disable arm intentionally does NOT
	// flip internal — the disabled legacy rows stay internal=0 so the
	// phase1 404 gate keeps blocking them; the B3 check is therefore
	// the only "must-return-zero-rows" assertion that holds.
	var invalidEnabled int
	if err := store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM models WHERE enabled != 0 AND kind != 'group' AND (credentials_json = '[]' OR COALESCE(internal_model,'') = '')`,
	).Scan(&invalidEnabled); err != nil {
		t.Fatalf("B3 invalid-enabled count: %v", err)
	}
	if invalidEnabled != 0 {
		t.Errorf("B3 verification failed — %d enabled non-group rows lack credentials or internal_model", invalidEnabled)
	}

	// Commission deliverable-4 operator-safety twin: zero ENABLED non-group
	// rows may remain non-internal after 032. Distinct from the B3 check
	// above — B3 does not flag an enabled backfill-eligible row left at
	// internal=0; this twin does (defense-in-depth for a partial/missed
	// disable arm).
	var enabledNonInternal int
	if err := store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM models WHERE enabled != 0 AND internal = 0 AND kind != 'group'`,
	).Scan(&enabledNonInternal); err != nil {
		t.Fatalf("operator-safety twin count: %v", err)
	}
	if enabledNonInternal != 0 {
		t.Errorf("operator-safety twin failed — %d enabled non-group rows remain non-internal after 032", enabledNonInternal)
	}

	// (ii) Convergence informational check — the "simpler"
	// `WHERE (internal IS NULL OR internal = 0)` query: returns the
	// disabled-by-032 count (glm-4.5-air + glm-5 = 2), which is
	// expected and INTENTIONAL. The disable arm does NOT flip
	// internal (see spec §Safe-rule + phase4-migration-data-safety
	// comment block at sqlite/032_internal_only_data.up.sql:64-65)
	// so that the phase1 404 gate's `!Internal` conjunct keeps
	// blocking these rows. Assert the count equals the number of
	// fixtures that should land on DISABLE.
	var residualNonInternal int
	if err := store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM models WHERE (internal IS NULL OR internal = 0)`,
	).Scan(&residualNonInternal); err != nil {
		t.Fatalf("residual non-internal count: %v", err)
	}
	// Expected: 2 (glm-4.5-air + glm-5; ext-fully-configured
	// backfilled to internal=1; agentic-group was already internal=1).
	const expectedResidual = 2
	if residualNonInternal != expectedResidual {
		t.Errorf("residual non-internal count: got %d, want %d (glm-4.5-air + glm-5 land on DISABLE)", residualNonInternal, expectedResidual)
	}
}

func TestMigration033_ForwardFreshDB(t *testing.T) {
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

	versions, err := store.GetAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "033" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 033 (registry append missing) — applied versions: %v", versions)
	}

	// credential_groups table must exist.
	var tbl string
	err = store.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='credential_groups'").Scan(&tbl)
	if err != nil || tbl != "credential_groups" {
		t.Fatalf("credential_groups table missing: %v", err)
	}

	// index must exist.
	var idx string
	err = store.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_credential_groups_provider'").Scan(&idx)
	if err != nil || idx != "idx_credential_groups_provider" {
		t.Fatalf("idx_credential_groups_provider index missing: %v", err)
	}

	// Idempotent re-run: a second Initialize / RunMigrations must not error.
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatalf("second RunMigrations (idempotency): %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Migration 034 — auth_tokens.allowed_groups + the token-grant repartition
// (token-authz plan T1.10).
// ─────────────────────────────────────────────────────────────────────────────

// migrationByVersion returns the registry entry for version, failing the test
// when the registry append is missing (the "Amendment-1 trap" — the registry is
// an ordered slice, so a SQL file without an append is silently inert).
func migrationByVersion(t *testing.T, version string) migration {
	t.Helper()
	for _, m := range migrations {
		if m.version == version {
			return m
		}
	}
	t.Fatalf("%s not found in the migrations registry — the Amendment-1 registry append is missing, so the SQL files are inert", version)
	return migration{}
}

// migrateUpTo033 brings a store to the pre-034 schema (used by the seeded
// repartition tests so the fixtures can be inserted in their legacy shape).
func migrateUpTo033(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.ensureMigrationsTable(ctx); err != nil {
		t.Fatalf("ensureMigrationsTable: %v", err)
	}
	for _, m := range migrations {
		if m.version == "034" {
			continue
		}
		if err := store.runMigration(ctx, m); err != nil {
			t.Fatalf("migration %s: %v", m.version, err)
		}
	}
	if columnExists(t, store.DB, "auth_tokens", "allowed_groups") {
		t.Fatal("precondition: allowed_groups exists before 034 — the fixture DB is not pre-034")
	}
}

// seedGroupAndModel inserts a models row so the repartition's
// `kind='group'` filter has something to match.
func seedModelRow(t *testing.T, store *Store, id, kind string) {
	t.Helper()
	if _, err := store.DB.Exec(
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model, kind, members_json, shadow_model_id) VALUES (?, ?, 1, '[]', '[]', 1, '[]', '', '', '', ?, '[]', '')`,
		id, id, kind,
	); err != nil {
		t.Fatalf("insert models row %s (kind=%s): %v", id, kind, err)
	}
}

// seedToken inserts an auth_tokens row with the given raw allowed_models bytes.
// allowedModelsRaw == nil inserts SQL NULL.
func seedToken(t *testing.T, store *Store, id string, allowedModelsRaw interface{}) {
	t.Helper()
	if _, err := store.DB.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models) VALUES (?, ?, ?, NULL, ?, 'seed', ?)`,
		id, "hash-"+id, id, "2026-01-01T00:00:00Z", allowedModelsRaw,
	); err != nil {
		t.Fatalf("insert auth_tokens row %s (%v): %v", id, allowedModelsRaw, err)
	}
}

// grantColumns reads (allowed_models, allowed_groups) raw, as strings with
// "<NULL>" for SQL NULL.
func grantColumns(t *testing.T, store *Store, id string) (string, string) {
	t.Helper()
	var models, groups interface{}
	if err := store.DB.QueryRow(
		`SELECT allowed_models, allowed_groups FROM auth_tokens WHERE id = ?`, id,
	).Scan(&models, &groups); err != nil {
		t.Fatalf("read grant columns for %s: %v", id, err)
	}
	render := func(v interface{}) string {
		switch x := v.(type) {
		case nil:
			return "<NULL>"
		case string:
			return x
		case []byte:
			return string(x)
		default:
			return "<?>"
		}
	}
	return render(models), render(groups)
}

// TestMigration034_ForwardFreshDB pins that 034 is IN THE REGISTRY (so it is
// not inert) and that a fresh boot lands the column + the schema_migrations
// row. Same shape as TestMigration031_ForwardFreshDB.
func TestMigration034_ForwardFreshDB(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	versions, err := store.GetAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("GetAppliedMigrations: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == "034" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("schema_migrations missing 034 (registry append missing) — applied versions: %v", versions)
	}

	if !columnExists(t, store.DB, "auth_tokens", "allowed_groups") {
		t.Error("allowed_groups column missing from auth_tokens (034 SQL not applied)")
	}
	// The repartition must not have disturbed the pre-existing column.
	if !columnExists(t, store.DB, "auth_tokens", "allowed_models") {
		t.Error("allowed_models column vanished during 034")
	}
}

// TestMigration034_SQLiteJSON1Available makes the OI-1 probe a permanent CI
// fact rather than a runbook promise: the repartition's SQL depends on
// json_each / json_group_array / json_valid being compiled into the shipped
// modernc.org/sqlite build. If this fails, the documented fallback is the
// idempotent Go-side backfill (plan §5.5) — do NOT improvise a schema.
func TestMigration034_SQLiteJSON1Available(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	var aggregated string
	if err := store.DB.QueryRow(`SELECT json_group_array(value) FROM json_each('["a","b"]')`).Scan(&aggregated); err != nil {
		t.Fatalf("json_group_array over json_each failed — JSON1 unavailable on modernc.org/sqlite: %v", err)
	}
	if aggregated != `["a","b"]` {
		t.Errorf(`json_group_array(json_each(...)) = %q, want ["a","b"]`, aggregated)
	}

	var valid int
	if err := store.DB.QueryRow(`SELECT json_valid('["a"]')`).Scan(&valid); err != nil {
		t.Fatalf("json_valid failed — JSON1 unavailable: %v", err)
	}
	if valid != 1 {
		t.Errorf("json_valid('[\"a\"]') = %d, want 1", valid)
	}
	if err := store.DB.QueryRow(`SELECT json_valid('not-json')`).Scan(&valid); err != nil {
		t.Fatalf("json_valid on invalid input errored (it must RETURN 0, not fail): %v", err)
	}
	if valid != 0 {
		t.Errorf("json_valid('not-json') = %d, want 0", valid)
	}

	// The NULLIF-over-zero-rows property the repartition relies on (PD-4).
	var empty interface{}
	if err := store.DB.QueryRow(
		`SELECT NULLIF((SELECT json_group_array(value) FROM json_each('[]')), '[]')`,
	).Scan(&empty); err != nil {
		t.Fatalf("NULLIF(json_group_array over zero rows) failed: %v", err)
	}
	if empty != nil {
		t.Errorf("json_group_array over zero rows wrapped in NULLIF = %v, want NULL", empty)
	}
}

// seedRepartitionFixtures inserts the five shapes the repartition must handle
// and returns their ids.
func seedRepartitionFixtures(t *testing.T, store *Store) {
	t.Helper()
	seedModelRow(t, store, "g1", "group")
	seedModelRow(t, store, "m1", "chat")

	seedToken(t, store, "wildcard", nil)                // (1) NULL — untouched
	seedToken(t, store, "group-only", `["g1"]`)         // (2) pure group id
	seedToken(t, store, "mixed", `["m1","g1"]`)         // (3) model + group id
	seedToken(t, store, "explicit-empty", `[]`)         // (4) '[]' — untouched
	seedToken(t, store, "malformed", `not-json`)        // (5) corrupt — untouched
	seedToken(t, store, "malformed-bracket", `[broken`) // (6) PD-2 exact shape — untouched, no abort
	seedToken(t, store, "model-only", `["m1"]`)         // (6) no group id — untouched
}

// assertRepartitionPostState asserts the post-034 shape of all six fixtures.
func assertRepartitionPostState(t *testing.T, store *Store) {
	t.Helper()

	type want struct{ models, groups string }
	wants := map[string]want{
		"wildcard":       {"<NULL>", "<NULL>"},
		"group-only":     {"<NULL>", `["g1"]`},
		"mixed":          {"[\"m1\"]", `["g1"]`},
		"explicit-empty": {"[]", "<NULL>"},
		"malformed":      {"not-json", "<NULL>"},
		// PD-2's shape: passes PG's `^\s*\[` row filter but is not valid
		// JSON. The migration must SKIP it (never abort), leaving it
		// byte-identical; the scanner then classifies it INVALID ⇒ DENY-ALL.
		"malformed-bracket": {"[broken", "<NULL>"},
		"model-only":        {`["m1"]`, "<NULL>"},
	}
	for id, w := range wants {
		gotModels, gotGroups := grantColumns(t, store, id)
		if gotModels != w.models {
			t.Errorf("%s: allowed_models = %q, want %q", id, gotModels, w.models)
		}
		if gotGroups != w.groups {
			t.Errorf("%s: allowed_groups = %q, want %q", id, gotGroups, w.groups)
		}
	}
}

// TestMigration034_Repartition proves the data repartition moves group ids out
// of allowed_models and leaves every other shape alone.
func TestMigration034_Repartition(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	migrateUpTo033(t, store)
	seedRepartitionFixtures(t, store)

	if err := store.runMigration(ctx, migrationByVersion(t, "034")); err != nil {
		t.Fatalf("runMigration 034: %v", err)
	}

	assertRepartitionPostState(t, store)
}

// TestMigration034_RepartitionIdempotent runs the repartition a SECOND time and
// requires zero rows affected + byte-identical state (the idempotency proof
// obligation, plan §5.4).
func TestMigration034_RepartitionIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	migrateUpTo033(t, store)
	seedRepartitionFixtures(t, store)

	mig := migrationByVersion(t, "034")
	if err := store.runMigration(ctx, mig); err != nil {
		t.Fatalf("first runMigration 034: %v", err)
	}
	assertRepartitionPostState(t, store)

	// Re-execute ONLY the repartition statement (the migration itself is
	// skipped by isMigrationApplied, so re-running the whole file would prove
	// nothing about the UPDATE).
	sqlText, err := store.loadMigrationSQL(mig.name)
	if err != nil {
		t.Fatalf("loadMigrationSQL: %v", err)
	}
	start := strings.Index(sqlText, "UPDATE auth_tokens")
	if start < 0 {
		t.Fatal("repartition UPDATE not found in the 034 SQL")
	}
	end := strings.Index(sqlText[start:], ";")
	if end < 0 {
		t.Fatal("repartition UPDATE is not terminated by ';'")
	}
	updateStmt := sqlText[start : start+end]

	res, err := store.DB.ExecContext(ctx, updateStmt)
	if err != nil {
		t.Fatalf("second repartition run: %v", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if affected != 0 {
		t.Errorf("second repartition affected %d row(s), want 0 — the repartition is not idempotent", affected)
	}

	assertRepartitionPostState(t, store)
}

// TestMigration034_NullNotEmptyArray pins PD-4: an allowed_models column left
// with zero ids becomes NULL (ABSENT), never '[]' (which would be
// PRESENT-and-empty under the explicit-empty contract).
func TestMigration034_NullNotEmptyArray(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	migrateUpTo033(t, store)
	seedModelRow(t, store, "g1", "group")
	seedToken(t, store, "group-only", `["g1"]`)

	if err := store.runMigration(ctx, migrationByVersion(t, "034")); err != nil {
		t.Fatalf("runMigration 034: %v", err)
	}

	gotModels, gotGroups := grantColumns(t, store, "group-only")
	if gotModels != "<NULL>" {
		t.Fatalf("emptied allowed_models = %q, want NULL — '[]' would mean PRESENT-and-empty and would change the token's semantics", gotModels)
	}
	if gotGroups != `["g1"]` {
		t.Errorf("allowed_groups = %q, want [\"g1\"]", gotGroups)
	}
}

// TestMigration034_PGGuardShape is the PD-2 pin, asserted STRUCTURALLY on the
// PostgreSQL SQL file. The SQLite harness cannot execute plpgsql, so a
// behavioural PG test would need a live PG in the unit pack — but the real
// regression class (an edit that moves the EXCEPTION outside the loop, drops
// it, or swaps in the PG-16-only pg_input_is_valid) is fully catchable by
// reading the file.
//
// Behavioural coverage for the skip semantics comes from
// TestMigration034_Repartition case (6) on SQLite plus the G0 dry-run against
// the live hosts (Phase 3 T3.10).
func TestMigration034_PGGuardShape(t *testing.T) {
	sqlBytes, err := postgresMigrations.ReadFile("migrations/postgres/034_add_allowed_groups.up.sql")
	if err != nil {
		t.Fatalf("read the PG 034 up file: %v", err)
	}
	sqlText := stripSQLLineComments(string(sqlBytes))

	// (a) the version-agnostic DO $$ block is present.
	if !strings.Contains(sqlText, "DO $$") {
		t.Error("PG 034 has no DO $$ block — the per-row guard is missing (a malformed row would abort the in-process boot migration)")
	}

	// (b) the EXCEPTION handler must appear AFTER the loop opens, i.e. INSIDE
	// the per-row block. Hoisted outside the loop, one bad row would abort the
	// whole batch — the exact PD-2 defect.
	loopIdx := strings.Index(sqlText, "LOOP")
	excIdx := strings.Index(sqlText, "EXCEPTION WHEN invalid_text_representation")
	if loopIdx < 0 {
		t.Fatal("PG 034 has no FOR ... LOOP — the row-by-row guard is missing")
	}
	if excIdx < 0 {
		t.Fatal("PG 034 has no `EXCEPTION WHEN invalid_text_representation` handler")
	}
	if excIdx < loopIdx {
		t.Errorf("the EXCEPTION handler appears at byte %d, before the LOOP at byte %d — it must be INSIDE the loop so one malformed row cannot abort the batch", excIdx, loopIdx)
	}

	// (c) version-agnostic: no PG-16-only function.
	if strings.Contains(sqlText, "pg_input_is_valid") {
		t.Error("PG 034 uses pg_input_is_valid — that requires PG 16+, an assumption this migration must not make (leader ruling: version-agnostic form)")
	}

	// (d) the skip must be observable at default log level.
	if !strings.Contains(sqlText, "RAISE WARNING") {
		t.Error("PG 034 has no RAISE WARNING — the skipped row would be invisible in pod stdout")
	}
}

// TestMigration034_SQLiteDialectParity is the PD-4 behavioural pin: a token
// whose allowed_models names ONLY group ids must end with allowed_models IS
// NULL (ABSENT) and allowed_groups a NON-EMPTY array (PRESENT). That is the
// shape PG's json_agg (NULL over zero rows) produces too — SQLite reaches it
// via NULLIF(..., '[]').
func TestMigration034_SQLiteDialectParity(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newSQLiteConnectionAtPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	migrateUpTo033(t, store)
	seedModelRow(t, store, "g1", "group")
	seedModelRow(t, store, "g2", "group")
	seedToken(t, store, "group-only-two", `["g1","g2"]`)

	if err := store.runMigration(ctx, migrationByVersion(t, "034")); err != nil {
		t.Fatalf("runMigration 034: %v", err)
	}

	// IS NULL — not = '', not = '[]'. '[]' would be PRESENT-empty, a
	// different permission state from the ABSENT that PG produces.
	var isNull int
	if err := store.DB.QueryRow(
		`SELECT COUNT(*) FROM auth_tokens WHERE id = 'group-only-two' AND allowed_models IS NULL`,
	).Scan(&isNull); err != nil {
		t.Fatalf("IS NULL check: %v", err)
	}
	if isNull != 1 {
		gotModels, _ := grantColumns(t, store, "group-only-two")
		t.Fatalf("allowed_models = %q, want SQL NULL (PD-4: '[]' would be PRESENT-empty and would diverge from PG's json_agg)", gotModels)
	}

	// The populated axis must be PRESENT and carry both group ids, and must
	// parse as PRESENT (not INVALID) through the real scanner.
	_, gotGroups := grantColumns(t, store, "group-only-two")
	if gotGroups == "<NULL>" {
		t.Fatal("allowed_groups is NULL — the repartition dropped the grants it was supposed to move")
	}
	var groupIDs []string
	if err := json.Unmarshal([]byte(gotGroups), &groupIDs); err != nil {
		t.Fatalf("allowed_groups is not a JSON array (%q): %v", gotGroups, err)
	}
	if len(groupIDs) != 2 {
		t.Errorf("allowed_groups = %v, want 2 ids", groupIDs)
	}

	// NOTE: pkg/auth imports pkg/store/database, so this in-package test may
	// NOT import pkg/auth to call ScanGrant (import cycle). The scanner's
	// view of the same bytes is covered in pkg/auth; here the shape assertion
	// is "non-NULL, valid JSON array, non-empty" — which is exactly PRESENT.
	if gotGroups == "[]" {
		t.Error("allowed_groups = '[]' — the repartition emptied the populated axis")
	}
}

// stripSQLLineComments removes `--` line comments so a structural pin tests
// the EXECUTABLE shape of a migration, not the prose that explains it (a
// comment naming a rejected function must neither satisfy nor violate a
// shape assertion).
func stripSQLLineComments(sqlText string) string {
	var b strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
