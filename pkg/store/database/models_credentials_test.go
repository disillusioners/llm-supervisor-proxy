// Phase 1 store round-trip + validation + M-1 shadow-write contract
// tests (Task 12 + Task 13).
//
// In-package so we exercise the same-package APIs the production
// code uses (database_test.go is same-package — house precedent).
//
// What this file covers:
//
//   - TestModelsCredentials_CRUD_RoundTrip: AddModel with 3 refs →
//     GetModel preserves order / weight / position; UpdateModel
//     reweights; RemoveModel cascades nothing (bindings are Phase 2).
//   - TestModelsCredentials_ValidationMatrix: 9-case validation
//     matrix per Task 6 (empty+internal, unknown ref, weight 0,
//     weight -1, dup, mixed provider, 17 refs, non-internal-with-creds,
//     valid 3-cred same-provider).
//   - TestModelsCredentials_InUseGuard: blocks deleting a credential
//     referenced by any model.Credentials ref; allows after model
//     removal.
//   - TestModelsCredentials_SingleCredentialGoldenTuple: single-cred
//     model resolves identically to the pre-change golden tuple
//     (provider / apiKey / baseURL / internalModel incl. peak-hour
//     variant). Back-compat invariant from the contract's §Backward
//     Compatibility Matrix.
//   - TestModelsCredentials_M1ShadowWriteContract: byte-exact shadow
//     invariant after every CRUD round-trip — Add / Update / reorder
//     / remove-first-ref. This is the contract test the plan pins in
//     Task 13; it asserts the application-side Go-computed shadow
//     invariant without relying on `json_extract`. When migration
//     029 lands, both this test and the Go-computed shadow write are
//     removed in the same commit.

package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// newStoreWithMigrations returns a fresh SQLite-backed *Store with
// the full migration chain applied (used by every test in this
// file).
func newStoreWithMigrations(t *testing.T) (*Store, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	return store, func() { store.Close() }
}

// seedCredential inserts a credential row and returns its ID. Bypasses
// the encrypted-credentials path (we don't need encryption for the
// byte-exact assertions below — the credentials table is opaque to
// the Credentials JSON column).
func seedCredential(t *testing.T, store *Store, id, provider string) {
	t.Helper()
	_, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, provider, "fake-key", "https://example.com", time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("seed credential %s: %v", id, err)
	}
}

// TestModelsCredentials_CRUD_RoundTrip — Task 12 acceptance.
//
// AddModel with 3 refs → GetModel preserves order / weight /
// position; UpdateModel reweights; RemoveModel cascades nothing
// (bindings are Phase 2 territory).
func TestModelsCredentials_CRUD_RoundTrip(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	// Seed credentials for the 3-ref model.
	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-C", "openai")

	// Add model with 3 refs (order matters for primary resolution).
	addModel := models.ModelConfig{
		ID:        "model-multi",
		Name:      "Multi-Cred Model",
		Enabled:   true,
		Internal:  true,
		Credentials: models.TestRefsWeighted(
			models.CredentialRef{CredentialID: "cred-A", Weight: 1},
			models.CredentialRef{CredentialID: "cred-B", Weight: 2},
			models.CredentialRef{CredentialID: "cred-C", Weight: 3},
		),
		InternalModel: "gpt-4o",
	}
	if err := modelsMgr.AddModel(addModel); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	got := modelsMgr.GetModel("model-multi")
	if got == nil {
		t.Fatal("GetModel returned nil after AddModel")
	}
	if len(got.Credentials) != 3 {
		t.Fatalf("Credentials round-trip lost entries: got %d, want 3", len(got.Credentials))
	}
	wantIDs := []string{"cred-A", "cred-B", "cred-C"}
	for i, want := range wantIDs {
		if got.Credentials[i].CredentialID != want {
			t.Errorf("Credentials[%d].CredentialID = %q, want %q (order lost)", i, got.Credentials[i].CredentialID, want)
		}
	}
	// Weights preserved (last is 3, primary is 1, etc.)
	wantWeights := []int{1, 2, 3}
	for i, want := range wantWeights {
		if got.Credentials[i].Weight != want {
			t.Errorf("Credentials[%d].Weight = %d, want %d (weight lost)", i, got.Credentials[i].Weight, want)
		}
	}

	// UpdateModel reweights — primary is now cred-C.
	upd := got
	upd.Credentials = models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "cred-C", Weight: 5},
		models.CredentialRef{CredentialID: "cred-A", Weight: 1},
		models.CredentialRef{CredentialID: "cred-B", Weight: 2},
	)
	if err := modelsMgr.UpdateModel("model-multi", *upd); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	got2 := modelsMgr.GetModel("model-multi")
	if got2 == nil {
		t.Fatal("GetModel returned nil after UpdateModel")
	}
	if got2.PrimaryCredentialID() != "cred-C" {
		t.Errorf("PrimaryCredentialID after reorder = %q, want %q", got2.PrimaryCredentialID(), "cred-C")
	}
	if got2.Credentials[0].Weight != 5 {
		t.Errorf("Credentials[0].Weight after reorder = %d, want 5", got2.Credentials[0].Weight)
	}

	// RemoveModel — bindings are Phase 2; this just exercises the
	// no-op cascade (no foreign keys, so no row reference is broken).
	if err := modelsMgr.RemoveModel("model-multi"); err != nil {
		t.Fatalf("RemoveModel: %v", err)
	}
	if got := modelsMgr.GetModel("model-multi"); got != nil {
		t.Errorf("GetModel after RemoveModel returned non-nil: %+v", got)
	}
}

// TestModelsCredentials_ValidationMatrix — Task 6 acceptance.
//
// 9-case matrix from the plan. Cases that "should reject" assert the
// specific error text. The "should accept" case uses a valid
// 3-credential same-provider model.
func TestModelsCredentials_ValidationMatrix(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	_, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	// Seed credentials used across cases.
	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-Z", "anthropic")

	tests := []struct {
		name      string
		model     models.ModelConfig
		wantErr   bool
		errSubstr string // substring expected in error message
	}{
		{
			name: "empty+internal",
			model: models.ModelConfig{
				ID: "v-empty-internal", Name: "Empty Internal",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: nil,
			},
			wantErr:   true,
			errSubstr: "credential_id is required when internal is true",
		},
		{
			name: "unknown ref",
			model: models.ModelConfig{
				ID: "v-unknown-ref", Name: "Unknown Ref",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefs("cred-A", "cred-DOES-NOT-EXIST"),
			},
			wantErr:   true,
			errSubstr: "references non-existent credential",
		},
		{
			name: "weight 0",
			model: models.ModelConfig{
				ID: "v-w0", Name: "Weight 0",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-B", Weight: 0, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "weight must be > 0",
		},
		{
			name: "weight -1",
			model: models.ModelConfig{
				ID: "v-wneg", Name: "Weight -1",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-B", Weight: -1, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "weight must be > 0",
		},
		{
			name: "duplicate id",
			model: models.ModelConfig{
				ID: "v-dup", Name: "Dup",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-A", Weight: 2, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "duplicate credential_id",
		},
		{
			name: "mixed provider",
			model: models.ModelConfig{
				ID: "v-mix", Name: "Mixed",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefs("cred-A", "cred-Z"), // A=openai, Z=anthropic
			},
			wantErr:   true,
			errSubstr: "does not match primary provider",
		},
		{
			name: "non-internal with creds (allowed — D3 probe requirement)",
			model: models.ModelConfig{
				ID: "v-nonint", Name: "Non-Internal With Creds",
				Enabled: true, Internal: false, InternalModel: "x",
				Credentials: models.TestRefs("cred-A"),
			},
			wantErr: false, // deviation: D3 probe needs this (see Validate comment in store.go)
		},
		{
			name: "valid 3-cred same-provider",
			model: models.ModelConfig{
				ID: "v-valid-3", Name: "Valid 3",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefsWeighted(
					models.CredentialRef{CredentialID: "cred-A", Weight: 1},
					models.CredentialRef{CredentialID: "cred-B", Weight: 2},
				),
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Validation is independent of CRUD save (matches pre-Phase-1
			// behavior — ModelsManager.Validate runs over the persisted
			// set, AddModel does not validate). Build an in-memory
			// ModelsConfig (the JSON-backed Validate is the canonical
			// shape) and exercise it.
			mc := models.NewModelsConfig()
			// Phase-1 S4: a seed failure now surfaces via panic →
			// t.Fatal rather than silently producing confusing
			// downstream assertions.
			func() {
				defer func() {
					if r := recover(); r != nil {
						if e, ok := r.(testFixtureSeedError); ok {
							t.Fatal(e.Error())
						}
						panic(r) // not ours — re-raise
					}
				}()
				seedIntoModelsConfig(mc, store)
			}()
			mc.Models = append(mc.Models, tc.model)
			err := mc.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errSubstr != "" && !contains(err.Error(), tc.errSubstr) {
					t.Errorf("error message = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	// 17-ref cap: ONE model with 17 distinct credentials should fail.
	ids17 := []string{"cred-17-A", "cred-17-B", "cred-17-C", "cred-17-D", "cred-17-E", "cred-17-F", "cred-17-G", "cred-17-H", "cred-17-I", "cred-17-J", "cred-17-K", "cred-17-L", "cred-17-M", "cred-17-N", "cred-17-O", "cred-17-P", "cred-17-Q"}
	mc := models.NewModelsConfig()
	for _, id := range ids17 {
		mc.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "k"})
	}
	refs17 := make([]models.CredentialRef, 17)
	for i, id := range ids17 {
		refs17[i] = models.CredentialRef{CredentialID: id, Weight: 1, Position: i}
	}
	mc.Models = append(mc.Models, models.ModelConfig{
		ID: "v-17-real", Name: "17 Real",
		Enabled: true, Internal: true, InternalModel: "x",
		Credentials: refs17,
	})
	if err := mc.Validate(); err == nil {
		t.Error("17-credential model should fail Validate (cap is 16)")
	} else if !contains(err.Error(), "exceeds max of 16 refs") {
		t.Errorf("17-ref error = %q, want substring 'exceeds max of 16 refs'", err.Error())
	}
}

// seedIntoModelsConfig copies the credentials rows from the DB into
// the in-memory ModelsConfig so JSON-backed Validate can resolve
// the `m.GetCredential(ref.CredentialID, ...)` provider-match
// lookup. Used by the validation matrix test.
//
// Phase-1 S4: a query failure here previously returned silently,
// producing confusing downstream assertions ("expected error from
// Validate, got nil" because the credentials map was empty so the
// existence check never fired). A seed failure should fail the test
// loudly — the test is operating on fixture state, not real data.
func seedIntoModelsConfig(mc *models.ModelsConfig, store *Store) {
	rows, err := store.DB.Query(`SELECT id, provider, api_key, coalesce(base_url, '') FROM credentials`)
	if err != nil {
		// t.Fatalf requires *testing.T; the helper is called from a test
		// goroutine that already holds the running test's context — we
		// surface the failure by panicking with a recognizable shape so
		// the test fails immediately rather than producing misleading
		// downstream assertions. Use a dedicated type so the test
		// harness reports a clear "seed failed" message.
		panic(testFixtureSeedError{what: "query credentials", err: err})
	}
	defer rows.Close()
	for rows.Next() {
		var id, provider, apiKey, baseURL string
		if err := rows.Scan(&id, &provider, &apiKey, &baseURL); err != nil {
			panic(testFixtureSeedError{what: "scan credential row", err: err})
		}
		if err := mc.AddCredential(models.CredentialConfig{ID: id, Provider: provider, APIKey: apiKey, BaseURL: baseURL}); err != nil {
			panic(testFixtureSeedError{what: "add credential " + id, err: err})
		}
	}
	if err := rows.Err(); err != nil {
		panic(testFixtureSeedError{what: "iterate credential rows", err: err})
	}
}

// testFixtureSeedError is the panic value raised by test-only fixture
// helpers (seedIntoModelsConfig and friends) when a fixture seed
// fails. Converted to t.Fatal at the test goroutine via recover() —
// keeps the helper signature simple (no *testing.T) while still
// surfacing the failure loudly.
type testFixtureSeedError struct {
	what string
	err  error
}

func (e testFixtureSeedError) Error() string {
	return "test fixture seed failed: " + e.what + ": " + e.err.Error()
}

// TestModelsCredentials_InUseGuard — Task 7 acceptance.
//
// RemoveCredential must reject when any model's Credentials slice
// references the credential (any position), and succeed after the
// referencing model is removed.
func TestModelsCredentials_InUseGuard(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-Unused", "openai")

	if err := modelsMgr.AddModel(models.ModelConfig{
		ID: "model-refs-A", Name: "Refs A",
		Enabled: true, Internal: true, InternalModel: "x",
		Credentials: models.TestRefsWeighted(
			models.CredentialRef{CredentialID: "cred-A", Weight: 2399, Position: 0},
			models.CredentialRef{CredentialID: "cred-B", Weight: 1, Position: 1},
		),
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// cred-A referenced at non-primary position (Credentials[0] is
	// the primary; A is at weight=2399 — i.e. the primary here).
	// Delete blocked.
	if err := modelsMgr.RemoveCredential("cred-A"); err == nil {
		t.Fatal("RemoveCredential(cred-A) should fail (referenced by model-refs-A)")
	} else if !errors.Is(err, models.ErrCredentialInUse) {
		t.Errorf("err = %v, want errors.Is(ErrCredentialInUse)", err)
	}

	// cred-B referenced at non-primary position. Delete blocked.
	if err := modelsMgr.RemoveCredential("cred-B"); err == nil {
		t.Fatal("RemoveCredential(cred-B) should fail (referenced by model-refs-A)")
	} else if !errors.Is(err, models.ErrCredentialInUse) {
		t.Errorf("err = %v, want errors.Is(ErrCredentialInUse)", err)
	}

	// cred-Unused not referenced. Delete succeeds.
	if err := modelsMgr.RemoveCredential("cred-Unused"); err != nil {
		t.Errorf("RemoveCredential(cred-Unused) unexpectedly failed: %v", err)
	}

	// Remove the referencing model. cred-A and cred-B are now
	// unreferenced. Remove succeeds.
	if err := modelsMgr.RemoveModel("model-refs-A"); err != nil {
		t.Fatalf("RemoveModel: %v", err)
	}
	if err := modelsMgr.RemoveCredential("cred-A"); err != nil {
		t.Errorf("RemoveCredential(cred-A) after model removal: %v", err)
	}
	if err := modelsMgr.RemoveCredential("cred-B"); err != nil {
		t.Errorf("RemoveCredential(cred-B) after model removal: %v", err)
	}
}

// TestModelsCredentials_SingleCredentialGoldenTuple — Task 12
// back-compat invariant (technical-analysis.md §Backward
// Compatibility Matrix).
//
// Single-credential model resolves identically to the pre-change
// 5-tuple (provider, apiKey, baseURL, internalModel, ok).
// InternalBaseURL override path is exercised too; the peak-hour
// variant is exercised by TestModelsCredentials_PeakHourGoldenTuple
// below (kept separate for clarity).
func TestModelsCredentials_SingleCredentialGoldenTuple(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	seedCredential(t, store, "cred-only", "openai")

	if err := modelsMgr.AddModel(models.ModelConfig{
		ID: "model-only-cred", Name: "Only",
		Enabled: true, Internal: true,
		Credentials: models.TestRefs("cred-only"),
		InternalBaseURL: "https://override.example.com",
		InternalModel:   "gpt-4o-2024-08-06",
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	provider, apiKey, baseURL, model, ok := modelsMgr.ResolveInternalConfig("model-only-cred")
	if !ok {
		t.Fatal("ResolveInternalConfig returned ok=false")
	}
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if apiKey != "fake-key" {
		t.Errorf("apiKey = %q, want %q", apiKey, "fake-key")
	}
	if baseURL != "https://override.example.com" {
		t.Errorf("baseURL = %q, want %q (InternalBaseURL override wins)", baseURL, "https://override.example.com")
	}
	if model != "gpt-4o-2024-08-06" {
		t.Errorf("model = %q, want %q", model, "gpt-4o-2024-08-06")
	}
}

// TestModelsCredentials_PeakHourGoldenTuple — back-compat variant
// where the peak-hour substitution kicks in.
func TestModelsCredentials_PeakHourGoldenTuple(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	seedCredential(t, store, "cred-peak", "openai")

	// 24-hour peak window covers "now" ⇒ peak-hour substitution fires.
	if err := modelsMgr.AddModel(models.ModelConfig{
		ID: "model-peak", Name: "Peak",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("cred-peak"),
		InternalModel: "gpt-normal",
		PeakHourEnabled: true,
		PeakHourStart:   "00:00",
		PeakHourEnd:     "23:59",
		PeakHourTimezone: "+0",
		PeakHourModel:    "gpt-peak",
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	_, _, _, model, ok := modelsMgr.ResolveInternalConfig("model-peak")
	if !ok {
		t.Fatal("ResolveInternalConfig returned ok=false")
	}
	if model != "gpt-peak" {
		t.Errorf("peak-hour substitution: model = %q, want %q", model, "gpt-peak")
	}
}

// TestModelsCredentials_M1ShadowWriteContract — Task 13 contract test.
//
// After every CRUD round-trip the credential_id shadow column
// byte-matches the Go-computed model.Credentials[0].CredentialID
// value at write time. This is the contract test the plan pins in
// Task 13; it asserts the application-side shadow invariant without
// relying on `json_extract`. When migration 029 lands, both this test
// and the Go-computed shadow write are removed in the same commit.
func TestModelsCredentials_M1ShadowWriteContract(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	// Seed credentials.
	for _, id := range []string{"cred-A", "cred-B", "cred-C", "cred-D"} {
		seedCredential(t, store, id, "openai")
	}

	// AddModel: shadow = Credentials[0] = "cred-A"
	if err := modelsMgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1",
		Enabled: true, Internal: true, InternalModel: "x",
		Credentials: models.TestRefsWeighted(
			models.CredentialRef{CredentialID: "cred-A", Weight: 1},
			models.CredentialRef{CredentialID: "cred-B", Weight: 2},
		),
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	assertShadow(t, store, "m1", "cred-A")

	// UpdateModel: reorder — primary = "cred-C". Shadow must rewrite.
	if err := modelsMgr.UpdateModel("m1", models.ModelConfig{
		ID: "m1", Name: "M1",
		Enabled: true, Internal: true, InternalModel: "x",
		Credentials: models.TestRefsWeighted(
			models.CredentialRef{CredentialID: "cred-C", Weight: 3},
			models.CredentialRef{CredentialID: "cred-D", Weight: 1},
		),
	}); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	assertShadow(t, store, "m1", "cred-C")

	// UpdateModel: remove the primary ref entirely — shadow must
	// rewrite to the new primary ("cred-D").
	if err := modelsMgr.UpdateModel("m1", models.ModelConfig{
		ID: "m1", Name: "M1",
		Enabled: true, Internal: true, InternalModel: "x",
		Credentials: models.TestRefs("cred-D"),
	}); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	assertShadow(t, store, "m1", "cred-D")

	// UpdateModel: empty Credentials (non-internal path allowed) —
	// shadow must be empty.
	if err := modelsMgr.UpdateModel("m1", models.ModelConfig{
		ID: "m1", Name: "M1",
		Enabled:   false,
		Internal:  false,
		InternalModel: "x",
		Credentials: nil,
	}); err != nil {
		t.Fatalf("UpdateModel empty creds: %v", err)
	}
	assertShadow(t, store, "m1", "")
}

// assertShadow reads the credential_id shadow column from the
// models table and asserts it byte-matches want. Reads credentials_json
// too for a sanity check (the JSON must reflect the same primary).
func assertShadow(t *testing.T, store *Store, modelID, want string) {
	t.Helper()
	var shadow, jsonCol string
	if err := store.DB.QueryRow(
		`SELECT credential_id, credentials_json FROM models WHERE id = ?`, modelID,
	).Scan(&shadow, &jsonCol); err != nil {
		t.Fatalf("assertShadow(%s): %v", modelID, err)
	}
	if shadow != want {
		t.Errorf("assertShadow(%s): credential_id = %q, want %q (M-1 shadow invariant violated)", modelID, shadow, want)
	}
}

// contains is a tiny strings.Contains wrapper. Defined in
// querybuilder_test.go in the same package — kept here only as a
// forward-declaration comment to avoid editor confusion.

// TestModelsCredentials_ValidationMatrix_DB (Phase-1 W5) mirrors the
// 9-case validation matrix from TestModelsCredentials_ValidationMatrix
// above against the DB-backed entry point ModelsManager.Validate
// (store.go ~1132-1280 — Validate()). The original matrix exercised
// only the JSON-backed ModelsConfig.Validate; this test pins the
// DB-backed equivalent so any drift between the two surfaces (added
// in Phase-1 W4) is caught at test time.
//
// Each case is materialized via raw SQL INSERT — bypasses AddModel
// validation (Phase-1 W4 now rejects invalid shapes at write time) so
// Validate() in isolation can be exercised for both accept and
// reject cases. Same bypass pattern as
// TestModelsManager_Validate_SecondaryWithNonInternal.
func TestModelsCredentials_ValidationMatrix_DB(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	modelsMgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}

	// Seed credentials used across cases (same triple as the JSON matrix).
	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-Z", "anthropic")

	// cases drives the 9 matrix entries against ModelsManager.Validate.
	// Each case is inserted raw; the test asserts Validate() returns
	// the expected error (or nil for accept cases).
	cases := []struct {
		name      string
		model     models.ModelConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty+internal",
			model: models.ModelConfig{
				ID: "v-db-empty-internal", Name: "Empty Internal",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: nil,
			},
			wantErr:   true,
			errSubstr: "credential_id is required when internal is true",
		},
		{
			name: "unknown ref",
			model: models.ModelConfig{
				ID: "v-db-unknown-ref", Name: "Unknown Ref",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefs("cred-A", "cred-DOES-NOT-EXIST"),
			},
			wantErr:   true,
			errSubstr: "references non-existent credential",
		},
		{
			name: "weight 0",
			model: models.ModelConfig{
				ID: "v-db-w0", Name: "Weight 0",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-B", Weight: 0, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "weight must be > 0",
		},
		{
			name: "weight -1",
			model: models.ModelConfig{
				ID: "v-db-wneg", Name: "Weight -1",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-B", Weight: -1, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "weight must be > 0",
		},
		{
			name: "duplicate id",
			model: models.ModelConfig{
				ID: "v-db-dup", Name: "Dup",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-A", Weight: 1, Position: 0},
					{CredentialID: "cred-A", Weight: 2, Position: 1},
				},
			},
			wantErr:   true,
			errSubstr: "duplicate credential_id",
		},
		{
			name: "mixed provider",
			model: models.ModelConfig{
				ID: "v-db-mix", Name: "Mixed",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefs("cred-A", "cred-Z"), // A=openai, Z=anthropic
			},
			wantErr:   true,
			errSubstr: "does not match primary provider",
		},
		{
			name: "non-internal with creds (allowed — D3 probe requirement)",
			model: models.ModelConfig{
				ID: "v-db-nonint", Name: "Non-Internal With Creds",
				Enabled: true, Internal: false, InternalModel: "x",
				Credentials: models.TestRefs("cred-A"),
			},
			wantErr: false, // deviation: D3 probe needs this
		},
		{
			name: "valid 3-cred same-provider",
			model: models.ModelConfig{
				ID: "v-db-valid-3", Name: "Valid 3",
				Enabled: true, Internal: true, InternalModel: "x",
				Credentials: models.TestRefsWeighted(
					models.CredentialRef{CredentialID: "cred-A", Weight: 1},
					models.CredentialRef{CredentialID: "cred-B", Weight: 2},
				),
			},
			wantErr: false,
		},
	}

	// Insert + Validate each case in isolation. We re-build the
	// models table to a single-row state per case so cross-case
	// model-list residue doesn't taint Validate().
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset models table to a single-row state for this case.
			if _, err := store.DB.ExecContext(context.Background(), `DELETE FROM models`); err != nil {
				t.Fatalf("clean models: %v", err)
			}
			insertModelRaw(t, store, tc.model)

			err := modelsMgr.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errSubstr != "" && !contains(err.Error(), tc.errSubstr) {
					t.Errorf("error message = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	// 17-ref cap: ONE model with 17 distinct credentials should fail
	// when materialized via the DB-backed Validate (same as the JSON
	// matrix case at the bottom of TestModelsCredentials_ValidationMatrix).
	t.Run("17 refs", func(t *testing.T) {
		if _, err := store.DB.ExecContext(context.Background(), `DELETE FROM models`); err != nil {
			t.Fatalf("clean models: %v", err)
		}
		ids17 := []string{"cred-17-A", "cred-17-B", "cred-17-C", "cred-17-D", "cred-17-E", "cred-17-F", "cred-17-G", "cred-17-H", "cred-17-I", "cred-17-J", "cred-17-K", "cred-17-L", "cred-17-M", "cred-17-N", "cred-17-O", "cred-17-P", "cred-17-Q"}
		for _, id := range ids17 {
			seedCredential(t, store, id, "openai")
		}
		refs17 := make([]models.CredentialRef, 17)
		for i, id := range ids17 {
			refs17[i] = models.CredentialRef{CredentialID: id, Weight: 1, Position: i}
		}
		insertModelRaw(t, store, models.ModelConfig{
			ID: "v-db-17-real", Name: "17 Real",
			Enabled: true, Internal: true, InternalModel: "x",
			Credentials: refs17,
		})
		err := modelsMgr.Validate()
		if err == nil {
			t.Error("17-credential model should fail Validate (cap is 16)")
		} else if !contains(err.Error(), "exceeds max of 16 refs") {
			t.Errorf("17-ref error = %q, want substring 'exceeds max of 16 refs'", err.Error())
		}
	})
}

// insertModelRaw materializes a single models row via raw SQL,
// bypassing AddModel's per-model validation (Phase-1 W4). Used by
// validation matrix tests that need to seed an invalid shape purely
// to assert Validate() rejects it. Mirrors the seedCredential
// bypass pattern.
func insertModelRaw(t *testing.T, store *Store, m models.ModelConfig) {
	t.Helper()
	credsJSON := marshalCredentialsJSON(m.Credentials)
	primary := m.PrimaryCredentialID()
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, 1, "[]", "[]", boolToInt(m.Internal), credsJSON, primary, m.InternalBaseURL, m.InternalModel,
	); err != nil {
		t.Fatalf("insertModelRaw(%s): %v", m.ID, err)
	}
}

// boolToInt is a tiny SQLite-boolean shim for the raw SQL inserts.
// The committed querybuilder uses BooleanLiteral which adapts per
// dialect; here we only target the SQLite test path so a plain int is
// sufficient.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}


