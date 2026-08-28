package database

// store_strict_test.go — Phase 1A (db-cache-layer) contract tests for
// the additive strict read methods. These prove the error
// discrimination the pkg/modelscache decorator depends on:
//   - sql.ErrNoRows (not-found) vs infrastructure errors (DB closed),
//   - decrypt-failure hardening (never serve ciphertext),
//   - the resolver variant making ZERO DB calls on a warm cache.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// newStrictTestStore opens a migrated SQLite test store closed via t.Cleanup.
func newStrictTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	return store
}

// newStrictTestManager builds a ModelsManager over the migrated store.
func newStrictTestManager(t *testing.T) *ModelsManager {
	t.Helper()
	store := newStrictTestStore(t)
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)
	return mgr
}

// withTestEncryptionKey configures a real 32-byte encryption key for the
// duration of one test so decrypt failures can be exercised. Restores
// the prior (typically unset) state afterwards.
func withTestEncryptionKey(t *testing.T) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	crypto.ResetEncryptionState()
	t.Setenv(crypto.EnvEncryptionKey, key)
	if err := crypto.InitEncryption(); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	t.Cleanup(func() {
		crypto.ResetEncryptionState()
	})
}

func strictTestModel(id string) models.ModelConfig {
	return models.ModelConfig{ID: id, Name: id + "-name", Enabled: true}
}

// ─── GetModelStrict ──────────────────────────────────────────────────────────

func TestGetModelStrict_SqlNoRowsVsConnRefused(t *testing.T) {
	mgr := newStrictTestManager(t)
	if err := mgr.AddModel(strictTestModel("m-strict")); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// Not-found: sql.ErrNoRows is BOTH ErrModelNotFound and sql.ErrNoRows.
	m, err := mgr.GetModelStrict(context.Background(), "does-not-exist")
	if m != nil {
		t.Fatalf("expected nil model, got %+v", m)
	}
	if err == nil {
		t.Fatal("expected error for missing model, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected errors.Is(err, ErrModelNotFound), got %v", err)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected errors.Is(err, sql.ErrNoRows), got %v", err)
	}

	// Happy path returns the model.
	m, err = mgr.GetModelStrict(context.Background(), "m-strict")
	if err != nil || m == nil {
		t.Fatalf("happy path: model=%v err=%v", m, err)
	}
	if m.ID != "m-strict" || m.Name != "m-strict-name" {
		t.Errorf("unexpected model: %+v", m)
	}

	// Infrastructure error: close the DB, then query. The driver error
	// must propagate unchanged — and must NOT satisfy sql.ErrNoRows.
	mgr.store.Close()
	m, err = mgr.GetModelStrict(context.Background(), "m-strict")
	if m != nil {
		t.Fatalf("expected nil model on infra error, got %+v", m)
	}
	if err == nil {
		t.Fatal("expected infra error after DB close, got nil")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Errorf("closed-DB error must not be classified as ErrNoRows: %v", err)
	}
	if errors.Is(err, ErrModelNotFound) {
		t.Errorf("closed-DB error must not be classified as ErrModelNotFound: %v", err)
	}
}

// ─── GetModelByNameStrict ────────────────────────────────────────────────────

func TestGetModelByNameStrict_SqlNoRowsVsConnRefused(t *testing.T) {
	mgr := newStrictTestManager(t)
	if err := mgr.AddModel(strictTestModel("m-byname")); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	m, err := mgr.GetModelByNameStrict(context.Background(), "no-such-name")
	if m != nil || err == nil {
		t.Fatalf("expected (nil, err), got (%v, %v)", m, err)
	}
	if !errors.Is(err, ErrModelNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("name-miss must be ErrModelNotFound wrapping sql.ErrNoRows, got %v", err)
	}

	m, err = mgr.GetModelByNameStrict(context.Background(), "m-byname-name")
	if err != nil || m == nil || m.ID != "m-byname" {
		t.Fatalf("happy path: model=%v err=%v", m, err)
	}

	mgr.store.Close()
	m, err = mgr.GetModelByNameStrict(context.Background(), "m-byname-name")
	if m != nil || err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("infra error must propagate un-conflated: (%v, %v)", m, err)
	}
}

// ─── GetCredentialStrict ─────────────────────────────────────────────────────

func TestGetCredentialStrict_CiphertextHardened(t *testing.T) {
	withTestEncryptionKey(t)
	mgr := newStrictTestManager(t)

	// A valid credential (encrypted properly by AddCredential).
	good := models.CredentialConfig{ID: "cred-good", Provider: "openai", APIKey: "sk-live-123"}
	if err := mgr.AddCredential(good); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// A credential whose stored api_key is garbage ciphertext, inserted
	// directly via SQL to bypass AddCredential's encrypt-on-write.
	_, err := mgr.store.DB.Exec(
		`INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, '', datetime('now'), datetime('now'))`,
		"cred-bad", "openai", "not-a-ciphertext-ENOENT-style")
	if err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	// Happy path: decrypted plaintext returned.
	cred, err := mgr.GetCredentialStrict(context.Background(), "cred-good")
	if err != nil || cred == nil {
		t.Fatalf("happy path: cred=%v err=%v", cred, err)
	}
	if cred.APIKey != "sk-live-123" {
		t.Errorf("expected decrypted key, got %q", cred.APIKey)
	}

	// Hardened path: decrypt failure → ErrDecryptionFailed, NEVER a
	// credential whose APIKey is the raw ciphertext.
	cred, err = mgr.GetCredentialStrict(context.Background(), "cred-bad")
	if cred != nil {
		t.Fatalf("expected nil credential on decrypt failure, got %+v", cred)
	}
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}

	// Not-found discrimination.
	cred, err = mgr.GetCredentialStrict(context.Background(), "no-such-cred")
	if cred != nil || !errors.Is(err, ErrCredentialNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("not-found: cred=%v err=%v", cred, err)
	}

	// Infra error propagates un-conflated.
	mgr.store.Close()
	cred, err = mgr.GetCredentialStrict(context.Background(), "cred-good")
	if cred != nil || err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("infra: cred=%v err=%v", cred, err)
	}
}

// ─── ResolveInternalConfigWithAffinityCached ─────────────────────────────────

func TestResolveInternalConfigWithAffinityCached_ZeroDBOnWarmCache(t *testing.T) {
	// Hand-constructed manager with a live engine and a NIL store: any
	// accidental DB read panics the test (store.DB nil-deref), proving
	// the resolver variant never touches the database.
	m := &ModelsManager{
		engine: credentiallb.NewEngine(credentiallb.DefaultBindingTTL, credentiallb.DefaultSweepInterval, credentiallb.DefaultCredentialSeed, credentiallb.DefaultCooldown),
	}

	creds := map[string]*models.CredentialConfig{
		"cred-a": {ID: "cred-a", Provider: "openai", APIKey: "key-a", BaseURL: "https://a.example.com"},
		"cred-b": {ID: "cred-b", Provider: "openai", APIKey: "key-b", BaseURL: "https://b.example.com"},
	}
	lookupCalls := 0
	credLookup := func(id string) (*models.CredentialConfig, bool) {
		lookupCalls++
		c, ok := creds[id]
		return c, ok
	}

	// Multi-credential model: engine path must be seeded.
	multiModel := &models.ModelConfig{
		ID:            "m-multi",
		Name:          "m-multi",
		Internal:      true,
		InternalModel: "gpt-multi",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-a", Weight: 1, Position: 0},
			{CredentialID: "cred-b", Weight: 1, Position: 1},
		},
	}
	m.engine.RebindFromStore(multiModel.ID, multiModel.Credentials)

	// Single-credential model: E-3 fast path.
	singleModel := &models.ModelConfig{
		ID:            "m-single",
		Name:          "m-single",
		Internal:      true,
		InternalModel: "gpt-single",
		Credentials:   []models.CredentialRef{{CredentialID: "cred-a", Weight: 1, Position: 0}},
	}

	// 1) single-credential fast path
	res, ok := m.ResolveInternalConfigWithAffinityCached(singleModel, "conv-1", credLookup)
	if !ok {
		t.Fatal("single-cred path: expected ok=true")
	}
	if res.CredentialID != "cred-a" || res.APIKey != "key-a" || res.Provider != "openai" {
		t.Errorf("single-cred resolution mismatch: %+v", res)
	}
	if res.NewlyBound {
		t.Error("fast path must never report NewlyBound")
	}

	// 2) 2+ credential engine path with a pinned conversation key
	res1, ok1 := m.ResolveInternalConfigWithAffinityCached(multiModel, "conv-affinity", credLookup)
	if !ok1 {
		t.Fatal("engine path: expected ok=true")
	}
	res2, ok2 := m.ResolveInternalConfigWithAffinityCached(multiModel, "conv-affinity", credLookup)
	if !ok2 || res1.CredentialID != res2.CredentialID {
		t.Fatalf("engine path must pin the conversation: %v vs %v", res1.CredentialID, res2.CredentialID)
	}
	if res1.InternalModel != "gpt-multi" || res1.APIKey == "" {
		t.Errorf("engine resolution mismatch: %+v", res1)
	}

	// 3) dangling reference heal: engine picks a credential the lookup
	// cannot serve → ok=false, and the engine rebinds to the survivor.
	creds["cred-zombie"] = nil
	m.engine.OnModelChanged("m-multi", append(multiModel.Credentials,
		models.CredentialRef{CredentialID: "cred-zombie", Weight: 1, Position: 2}))
	multiModel.Credentials = append(multiModel.Credentials,
		models.CredentialRef{CredentialID: "cred-zombie", Weight: 1, Position: 2})
	// Force the zombie as the pinned credential for a fresh key is not
	// directly possible via the public API, so instead exercise the
	// lookup-miss branch by asking for a credential that is absent.
	delete(creds, "cred-a")
	delete(creds, "cred-b")
	delete(creds, "cred-zombie")
	resZ, okZ := m.ResolveInternalConfigWithAffinityCached(multiModel, "conv-missing", credLookup)
	if okZ {
		t.Fatalf("lookup-miss must fail, got %+v", resZ)
	}

	if lookupCalls == 0 {
		t.Error("credLookup was never called — test did not exercise the closure path")
	}
}

func TestResolveInternalConfigWithAffinityCached_NilAndExternalGuards(t *testing.T) {
	m := &ModelsManager{} // nil engine
	lookup := func(id string) (*models.CredentialConfig, bool) { return nil, false }

	if _, ok := m.ResolveInternalConfigWithAffinityCached(nil, "k", lookup); ok {
		t.Error("nil model must resolve ok=false")
	}
	external := &models.ModelConfig{ID: "ext", Internal: false}
	if _, ok := m.ResolveInternalConfigWithAffinityCached(external, "k", lookup); ok {
		t.Error("non-internal model must resolve ok=false")
	}
	noCred := &models.ModelConfig{ID: "internal-nocred", Internal: true}
	if _, ok := m.ResolveInternalConfigWithAffinityCached(noCred, "k", lookup); ok {
		t.Error("internal model without primary credential must resolve ok=false")
	}
}

// ─── GetModelsStrict / GetEnabledModelsStrict ────────────────────────────────

func TestGetModelsStrict_TransientErrorReturnsNilSlice(t *testing.T) {
	mgr := newStrictTestManager(t)
	mgr.store.Close() // simulate infra failure

	result, err := mgr.GetModelsStrict(context.Background())
	if err == nil {
		t.Fatal("expected infra error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil slice on infra error, got %v", result)
	}

	result, err = mgr.GetEnabledModelsStrict(context.Background())
	if err == nil {
		t.Fatal("expected infra error (enabled variant), got nil")
	}
	if result != nil {
		t.Errorf("expected nil slice (enabled variant), got %v", result)
	}
}

func TestGetModelsStrict_EmptyResultLegit(t *testing.T) {
	mgr := newStrictTestManager(t) // fresh DB, no models

	result, err := mgr.GetModelsStrict(context.Background())
	if err != nil {
		t.Fatalf("legit-empty must not error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d models", len(result))
	}

	result, err = mgr.GetEnabledModelsStrict(context.Background())
	if err != nil || len(result) != 0 {
		t.Errorf("enabled legit-empty: result=%v err=%v", result, err)
	}

	// Populate and re-read.
	if err := mgr.AddModel(strictTestModel("m-a")); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	disabled := strictTestModel("m-b")
	disabled.Enabled = false
	if err := mgr.AddModel(disabled); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	result, err = mgr.GetModelsStrict(context.Background())
	if err != nil || len(result) != 2 {
		t.Errorf("GetModelsStrict: len=%d err=%v", len(result), err)
	}
	enabled, err := mgr.GetEnabledModelsStrict(context.Background())
	if err != nil || len(enabled) != 1 || enabled[0].ID != "m-a" {
		t.Errorf("GetEnabledModelsStrict: %+v err=%v", enabled, err)
	}
}

// ─── GetCredentialsStrict ────────────────────────────────────────────────────

func TestGetCredentialsStrict_TransientErrorReturnsNilSlice(t *testing.T) {
	mgr := newStrictTestManager(t)
	mgr.store.Close()
	result, err := mgr.GetCredentialsStrict(context.Background())
	if err == nil {
		t.Fatal("expected infra error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil slice on infra error, got %v", result)
	}
}

func TestGetCredentialsStrict_EmptyResultLegit(t *testing.T) {
	mgr := newStrictTestManager(t)
	result, err := mgr.GetCredentialsStrict(context.Background())
	if err != nil {
		t.Fatalf("legit-empty must not error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d credentials", len(result))
	}
}

func TestGetCredentialsStrict_DecryptFailureAbortsScan(t *testing.T) {
	withTestEncryptionKey(t)
	mgr := newStrictTestManager(t)

	// Two decryptable credentials inserted via AddCredential (properly
	// encrypted under the test key).
	for _, id := range []string{"cred-1", "cred-2"} {
		if err := mgr.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "sk-" + id}); err != nil {
			t.Fatalf("AddCredential(%s): %v", id, err)
		}
	}
	// Third row with undecryptable ciphertext (raw SQL, after the two
	// valid rows alphabetically? ORDER BY id: cred-1, cred-2, cred-x-bad).
	_, err := mgr.store.DB.Exec(
		`INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, '', datetime('now'), datetime('now'))`,
		"cred-x-bad", "openai", "not-a-ciphertext-ENOENT-style")
	if err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	partial, err := mgr.GetCredentialsStrict(context.Background())
	if !errors.Is(err, ErrDecryptionFailureInScan) {
		t.Fatalf("expected ErrDecryptionFailureInScan, got %v", err)
	}
	if len(partial) != 2 {
		t.Fatalf("expected 2 partial rows, got %d", len(partial))
	}
	for _, c := range partial {
		if strings.Contains(c.APIKey, "not-a-ciphertext") || c.ID == "cred-x-bad" {
			t.Errorf("offending row leaked into partial slice: %+v", c)
		}
	}
}

// legacy methods keep their silent-fallback shapes (regression guard
// for the C1 "byte-identical signatures" ruling).
func TestStrictAddition_LegacyMethodsUnchanged(t *testing.T) {
	mgr := newStrictTestManager(t)
	mgr.store.Close()

	// Legacy single reads: silent nil on error (unchanged).
	if mgr.GetModel("any") != nil {
		t.Error("legacy GetModel must return nil on error")
	}
	if mgr.GetModelByName("any") != nil {
		t.Error("legacy GetModelByName must return nil on error")
	}
	if mgr.GetCredential("any") != nil {
		t.Error("legacy GetCredential must return nil on error")
	}
	// Legacy list reads: silent empty slice on error (unchanged).
	if got := mgr.GetModels(); len(got) != 0 {
		t.Errorf("legacy GetModels must return empty slice, got %d", len(got))
	}
	if got := mgr.GetEnabledModels(); len(got) != 0 {
		t.Errorf("legacy GetEnabledModels must return empty slice, got %d", len(got))
	}
	if got := mgr.GetCredentials(); len(got) != 0 {
		t.Errorf("legacy GetCredentials must return empty slice, got %d", len(got))
	}
}
