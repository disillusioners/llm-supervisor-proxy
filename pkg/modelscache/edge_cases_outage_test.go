package modelscache

// edge_cases_outage_test.go — outage-mode edge-case coverage (TEST-ONLY).
//
// Pins the four corners of the failure-mode matrix that the existing
// outage_test.go / contract_test.go / tokens_test.go suites do not
// already exercise end-to-end over the REAL SQLite stack:
//
//   (a) EMPTY DB at boot (no models/tokens rows) → boot priming
//       succeeds, healthy=true, GetModels returns empty (not error).
//   (b) DECRYPT-FAILED in the SCAN path → list scan returns
//       ErrDecryptionFailureInScan, the row never appears in the
//       partial slice, boot/swap does not poison the cache with
//       raw ciphertext (planner ruling J).
//   (c) TOKEN CREATED DURING OUTAGE → unknown token + DB down
//       fails-closed (401-equivalent) AND a token created via
//       out-of-band SQL while the DB is up revalidates after the
//       negative TTL expires (no stale-lockout beyond TTL).
//   (d) MODEL RENAMED DURING OUTAGE → UpdateModel fails cleanly,
//       the cache keeps serving the old name; after recovery the
//       reconciler converges to the new name.
//
// The whole file is bounded (<5s wall-clock on a developer box) and
// reuses the outage_test.go helpers (outageStack / outageModelsSource /
// outageTokenStore / fakeClock) so the test class stays consistent
// with the headline outage simulation already shipped.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─── (a) EMPTY DB at boot ───────────────────────────────────────────────────

// TestEdgeCase_EmptyDBAtBoot_SucceedsHealthy asserts the boot-priming
// path over a fully-migrated but COMPLETELY EMPTY SQLite store:
// no models, no credentials, no tokens. Boot must succeed (no error),
// healthy=true, and every read returns an empty slice — never nil,
// never an error.
func TestEdgeCase_EmptyDBAtBoot_SucceedsHealthy(t *testing.T) {
	// Build a fresh SQLite stack the same way outage_test does, but
	// skip the seed step so the DB is genuinely empty.
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	// The empty-DB invariant: schema is migrated, all tables exist,
	// zero rows in models / credentials / auth_tokens.
	for _, table := range []string{"models", "credentials", "auth_tokens"} {
		var n int
		if err := dbStore.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("expected empty %s table, got %d rows", table, n)
		}
	}

	c, err := NewCachedModelsConfig(mgr, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("boot priming over empty DB must succeed; got %v", err)
	}
	t.Cleanup(c.Stop)

	if !c.Healthy() {
		t.Fatal("boot over an empty DB must be healthy=true (the schema is reachable, just empty)")
	}
	if got := c.GetModels(); len(got) != 0 {
		t.Errorf("GetModels over empty DB must return empty slice, got %d entries", len(got))
	}
	if got := c.GetEnabledModels(); len(got) != 0 {
		t.Errorf("GetEnabledModels over empty DB must return empty slice, got %d entries", len(got))
	}
	if got := c.GetCredentials(); len(got) != 0 {
		t.Errorf("GetCredentials over empty DB must return empty slice, got %d entries", len(got))
	}
	if m := c.GetModel("anything"); m != nil {
		t.Errorf("GetModel over empty DB must return nil, got %+v", m)
	}

	// AddModel after boot is the first read-write; it must succeed
	// (boot state is not "marked down" by virtue of being empty).
	// Use an external model — internal=true would require a
	// credential_id which the empty DB doesn't have yet (chicken/egg).
	first := models.ModelConfig{ID: "first", Name: "first", Enabled: true}
	if err := c.AddModel(first); err != nil {
		t.Fatalf("AddModel after empty-DB boot must succeed; got %v", err)
	}
	if got := c.GetModel("first"); got == nil || got.ID != "first" {
		t.Errorf("AddModel write-through must make the model visible; got %+v", got)
	}
}

// ─── (b) DECRYPT-FAILED in the SCAN path ────────────────────────────────────

// TestEdgeCase_DecryptFailedInScan_AbortsList pins planner ruling J:
// a per-row decrypt failure inside GetCredentialsStrict aborts the
// whole scan and returns ErrDecryptionFailureInScan — the offending
// row MUST NOT appear in the partial slice, and the cache (which
// uses GetCredentialsStrict as the boot/scan path) MUST NOT install
// a swap with raw ciphertext.
func TestEdgeCase_DecryptFailedInScan_AbortsList(t *testing.T) {
	// Enable real encryption so a garbage ciphertext actually fails
	// Decrypt (with no key configured, Decrypt is pass-through).
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	crypto.ResetEncryptionState()
	t.Setenv(crypto.EnvEncryptionKey, key)
	if err := crypto.InitEncryption(); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	t.Cleanup(func() { crypto.ResetEncryptionState() })

	dbPath := filepath.Join(t.TempDir(), "decrypt-scan.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	// Add a properly-encrypted credential first.
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "cred-good", Provider: "openai", APIKey: "sk-good", BaseURL: "https://good.example.com",
	}); err != nil {
		t.Fatalf("AddCredential(cred-good): %v", err)
	}
	// Raw-insert an undecryptable credential row (bypasses
	// encrypt-on-write so its api_key stays as the literal garbage).
	if _, err := dbStore.DB.Exec(
		`INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, '', datetime('now'), datetime('now'))`,
		"cred-bad-cipher", "openai", "not-a-valid-ciphertext"); err != nil {
		t.Fatalf("raw insert bad cipher: %v", err)
	}

	// Direct call against the production manager — the source of truth.
	creds, err := mgr.GetCredentialsStrict(context.Background())
	if !errors.Is(err, database.ErrDecryptionFailureInScan) {
		t.Fatalf("GetCredentialsStrict must return ErrDecryptionFailureInScan on per-row decrypt failure; got err=%v, creds=%d", err, len(creds))
	}
	// The partial slice MUST NOT contain the undecryptable row.
	for _, c := range creds {
		if c.ID == "cred-bad-cipher" {
			t.Fatalf("undecryptable row must never appear in the partial slice: %+v", c)
		}
		// Nor may any cached credential carry raw ciphertext as plaintext.
		if c.APIKey == "not-a-valid-ciphertext" {
			t.Fatalf("ciphertext leaked into the partial slice as a plaintext APIKey: %+v", c)
		}
	}

	// Boot via the decorator must abort (the boot snapshot is a
	// GetCredentialsStrict scan, and a per-row decrypt failure is
	// not boot-recoverable per planner ruling J).
	if _, err := NewCachedModelsConfig(mgr, Options{ReconcileInterval: time.Hour}); !errors.Is(err, database.ErrDecryptionFailureInScan) {
		t.Fatalf("boot must abort on ErrDecryptionFailureInScan; got %v", err)
	}
}

// ─── (c) TOKEN CREATED DURING OUTAGE ────────────────────────────────────────

// TestEdgeCase_TokenCreatedDuringOutage_BecomesValidAfterTTL pins
// the "no stale-lockout beyond TTL" guarantee: a token that does
// not exist when the cache first encounters it goes through the
// negative verdict (60s TTL); if the token is created in the DB
// during that window, the next validate AFTER the TTL window must
// revalidate and find it. During the outage window the unknown
// token must surface an infra error (401-equivalent) — but only
// for the FIRST cold-miss; once the negative verdict is cached
// (within the TTL) it replays fail-closed (never stale-served).
func TestEdgeCase_TokenCreatedDuringOutage_BecomesValidAfterTTL(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	st := newOutageStack(t)
	tokCache := NewCachedTokenStore(st.tokSrc, Options{Clock: clk.Now})
	t.Cleanup(tokCache.Stop)

	plaintext, hash, _ := auth.GenerateToken()

	// Phase 1: DB up, unknown token → negative verdict cached.
	if _, err := tokCache.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("phase 1: unknown token must surface ErrTokenNotFound; got %v", err)
	}

	// Phase 2: DB goes down. Within the negative TTL the negative
	// verdict is replayed fail-closed — no infra error leaks out,
	// and no inner call is made (the cached verdict is the source
	// of truth during this window).
	st.tokSrc.down.Store(true)
	if _, err := tokCache.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("phase 2 (DB down, within TTL): negative verdict must replay, not be replaced by infra; got %v", err)
	}

	// Phase 3: DB restored; inject the token via out-of-band SQL so
	// the reconciler / write-through path can pick it up once the
	// negative TTL expires. The plaintext-to-hash mapping is fixed
	// by auth.HashToken; we insert directly with the computed hash.
	st.tokSrc.down.Store(false)
	clk.Advance(61 * time.Second) // past the 60s negative TTL
	if _, err := st.dbStore.DB.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by, ultimate_model_enabled, allowed_models) VALUES (?, ?, 'late-create', datetime('now'), 'edge-test', 0, '[]')`,
		"late-token-id", hash); err != nil {
		t.Fatalf("late-create insert: %v", err)
	}

	// Phase 4: validate after TTL — the revalidation reaches the DB
	// (now up), finds the row, and serves the token. No stale-lockout.
	tok, err := tokCache.ValidateToken(context.Background(), plaintext)
	if err != nil || tok == nil || tok.ID != "late-token-id" {
		t.Fatalf("phase 4: token created during outage must validate after TTL; got tok=%+v err=%v", tok, err)
	}
}

// ─── (d) MODEL RENAMED DURING OUTAGE ────────────────────────────────────────

// TestEdgeCase_ModelRenamedDuringOutage_OldNameSurvivesUntilRecovery
// pins the write-through semantics during a DB outage: a mutator
// that reaches the DB fails when the DB is down (the cache's inner
// call returns the error), so the cache state is unchanged. The
// OLD name keeps serving until the reconciler converges to the new
// state.
func TestEdgeCase_ModelRenamedDuringOutage_OldNameSurvivesUntilRecovery(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	st := newOutageStack(t)
	c, err := NewCachedModelsConfig(st.src, Options{Clock: clk.Now, ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(c.Stop)

	// Pre-condition: int-single is configured and reachable.
	if m := c.GetModel("int-single"); m == nil {
		t.Fatal("precondition: int-single must be cached")
	}
	if m := c.GetModelByName("int-single"); m == nil {
		t.Fatal("precondition: int-single must be reachable by name")
	}

	// Phase 1: cut the DB and try to rename int-single → int-renamed.
	st.src.down.Store(true)
	rename := models.ModelConfig{
		ID: "int-single", Name: "int-renamed", Enabled: true,
		Internal: true, InternalModel: "renamed-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-a", Weight: 1, Position: 0}},
	}
	if err := c.UpdateModel("int-single", rename); err == nil {
		t.Fatal("UpdateModel must surface the DB-down error (the inner call returned nil?!)")
	}
	// Cache state unchanged: old name still resolvable.
	if m := c.GetModelByName("int-single"); m == nil {
		t.Error("phase 1: old name must still serve while DB is down")
	}
	if m := c.GetModelByName("int-renamed"); m != nil {
		t.Errorf("phase 1: new name must NOT be visible during outage, got %+v", m)
	}

	// Phase 2: DB restored + reconciler converges → new name appears.
	st.src.down.Store(false)
	// First make the DB match the desired rename so the reconciler
	// picks it up; without this the real stack's source-of-truth is
	// still "int-single".
	if err := st.mgr.UpdateModel("int-single", rename); err != nil {
		t.Fatalf("UpdateModel on real DB: %v", err)
	}
	clk.Advance(61 * time.Second)
	c.reconcileOnce()
	if m := c.GetModelByName("int-renamed"); m == nil {
		t.Errorf("phase 2: reconciler must converge to the renamed model; got nil")
	}
	if m := c.GetModelByName("int-single"); m != nil {
		t.Errorf("phase 2: old name must NOT survive after recovery (rename is authoritative), got %+v", m)
	}
}
