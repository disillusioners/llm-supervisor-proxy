package database

// ImgGen Models commission / T1.1.2 — ModelsManager.AddModel kind
// enforcement test (Architect Amendment 2 split-brain fix).
// Proves the production /fe/api/models POST/PUT write path
// enforces the same kind rule set as the JSON-file bulk path
// (ModelsConfig.Validate).

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// setupImgGenManager creates a ModelsManager with a minimax
// credential + an openai credential so we can test the
// provider-match invariant on image-gen.
func setupImgGenManager(t *testing.T) (*ModelsManager, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("create SQLite: %v", err)
	}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		store.Close()
		t.Fatalf("NewModelsManager: %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "minimax-cred", Provider: "minimax", APIKey: "k",
	}); err != nil {
		store.Close()
		t.Fatalf("add minimax cred: %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "openai-cred", Provider: "openai", APIKey: "k",
	}); err != nil {
		store.Close()
		t.Fatalf("add openai cred: %v", err)
	}
	return mgr, func() { store.Close() }
}

func TestModelsManager_AddModel_KindImageGen_Valid(t *testing.T) {
	mgr, cleanup := setupImgGenManager(t)
	defer cleanup()

	err := mgr.AddModel(models.ModelConfig{
		ID:            "img-valid",
		Name:          "Valid Image Gen",
		Enabled:       true,
		Kind:          models.KindImageGen,
		Internal:      true,
		InternalModel: "image-01",
		Credentials:   models.TestRefs("minimax-cred"),
	})
	if err != nil {
		t.Fatalf("valid image-gen model should be accepted, got: %v", err)
	}
}

func TestModelsManager_AddModel_KindImageGen_Violations(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*models.ModelConfig)
		wantErrSub string
	}{
		{
			name: "image-gen without internal",
			mutate: func(m *models.ModelConfig) {
				m.Internal = false
			},
			wantErrSub: "requires internal",
		},
		{
			name: "image-gen without internal_model",
			mutate: func(m *models.ModelConfig) {
				m.InternalModel = ""
			},
			wantErrSub: "internal_model",
		},
		{
			name: "image-gen with empty credentials",
			mutate: func(m *models.ModelConfig) {
				m.Credentials = nil
			},
			// The existing chat validation rule (Internal ==
			// true ⇒ credentials non-empty) catches this BEFORE
			// the kind-rule validator; either reject is correct.
			wantErrSub: "credential",
		},
		{
			name: "image-gen with non-minimax provider",
			mutate: func(m *models.ModelConfig) {
				m.Credentials = models.TestRefs("openai-cred")
			},
			wantErrSub: "minimax",
		},
		{
			name: "image-gen with fallback_chain",
			mutate: func(m *models.ModelConfig) {
				m.FallbackChain = []string{"other"}
			},
			wantErrSub: "fallback_chain",
		},
		{
			name: "image-gen with secondary_upstream_model",
			mutate: func(m *models.ModelConfig) {
				m.SecondaryUpstreamModel = "x"
			},
			wantErrSub: "secondary_upstream_model",
		},
		{
			name: "image-gen with peak_hour_enabled",
			mutate: func(m *models.ModelConfig) {
				m.PeakHourEnabled = true
			},
			wantErrSub: "peak_hour_enabled",
		},
		{
			name: "image-gen with release_stream_chunk_deadline",
			mutate: func(m *models.ModelConfig) {
				m.ReleaseStreamChunkDeadline = models.Duration(60 * 1e9)
			},
			wantErrSub: "release_stream_chunk_deadline",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr, cleanup := setupImgGenManager(t)
			defer cleanup()
			m := models.ModelConfig{
				ID:            "img-bad",
				Name:          "Bad Image Gen",
				Enabled:       true,
				Kind:          models.KindImageGen,
				Internal:      true,
				InternalModel: "image-01",
				Credentials:   models.TestRefs("minimax-cred"),
			}
			tc.mutate(&m)
			err := mgr.AddModel(m)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestModelsManager_AddModel_UnknownKind_Rejected(t *testing.T) {
	mgr, cleanup := setupImgGenManager(t)
	defer cleanup()

	err := mgr.AddModel(models.ModelConfig{
		ID:            "img-typo",
		Name:          "Typo",
		Enabled:       true,
		Kind:          "image_gen", // typo
		Internal:      true,
		InternalModel: "image-01",
		Credentials:   models.TestRefs("minimax-cred"),
	})
	if err == nil {
		t.Fatal("expected unknown kind to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("error %q does not mention 'unknown kind'", err.Error())
	}
}

func TestModelsManager_AddModel_Chat_NoKindRules(t *testing.T) {
	mgr, cleanup := setupImgGenManager(t)
	defer cleanup()

	// A plain chat model (no kind) should pass — kind rules
	// don't fire on empty / chat.
	err := mgr.AddModel(models.ModelConfig{
		ID:            "chat-plain",
		Name:          "Plain Chat",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-x",
		Credentials:   models.TestRefs("openai-cred"),
	})
	if err != nil {
		t.Errorf("plain chat model should pass, got: %v", err)
	}
}

// --- Kind persistence round-trips (ship-blocker fix, 2026-10-06) ---
//
// The tester's final verification found the exact gap these tests
// close: the store kind tests validated kind RULES but never across
// a reload — ModelConfig.Kind was memory-only (no kind column, no
// write binding, no load mapping), so post-restart every image-gen
// model loaded as chat (leaking into chat surfaces, vanishing from
// the ImgGen tab). Migration 030 + InsertModel/UpdateModel bindings
// + the four row→ModelConfig load mappings fix that; these tests
// prove it via save → NEW store instance from the same DB file →
// load (the restart simulation the API-level repro used).

// setupKindManagerAt is setupImgGenManager with a caller-chosen
// dbPath, so a test can RE-OPEN the same DB file after closing the
// first manager (the restart simulation). Seeds the same minimax +
// openai credentials the kind-rule tests rely on.
func setupKindManagerAt(t *testing.T, dbPath string) (*ModelsManager, func()) {
	t.Helper()

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("create SQLite: %v", err)
	}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		store.Close()
		t.Fatalf("NewModelsManager: %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "minimax-cred", Provider: "minimax", APIKey: "k",
	}); err != nil {
		store.Close()
		t.Fatalf("add minimax cred: %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "openai-cred", Provider: "openai", APIKey: "k",
	}); err != nil {
		store.Close()
		t.Fatalf("add openai cred: %v", err)
	}
	return mgr, func() { store.Close() }
}

// reopenKindManagerAt opens a NEW store instance + ModelsManager
// over an existing DB file — the post-restart half. No credentials
// are seeded (reads don't need them; the restart must not depend on
// re-writing config).
func reopenKindManagerAt(t *testing.T, dbPath string) (*ModelsManager, func()) {
	t.Helper()

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrations on reopen: %v", err)
	}
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		store.Close()
		t.Fatalf("NewModelsManager on reopen: %v", err)
	}
	return mgr, func() { store.Close() }
}

// TestModelsManager_KindPersistsAcrossRestart_ImageGen is test (a):
// save a kind:"image-gen" model via AddModel (Insert path) → close →
// open a NEW store instance from the same DB file → the loaded model
// has Kind == "image-gen" on ALL read paths (GetModel, GetModelStrict,
// GetModels list, GetEnabledModels list).
func TestModelsManager_KindPersistsAcrossRestart_ImageGen(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Phase 1 — write, verify pre-restart, close (the restart).
	mgr, cleanup := setupKindManagerAt(t, dbPath)
	img := models.ModelConfig{
		ID:            "img-persist",
		Name:          "Persisting Image Gen",
		Enabled:       true,
		Kind:          models.KindImageGen,
		Internal:      true,
		InternalModel: "image-01",
		Credentials:   models.TestRefs("minimax-cred"),
	}
	if err := mgr.AddModel(img); err != nil {
		cleanup()
		t.Fatalf("AddModel image-gen: %v", err)
	}
	if got := mgr.GetModel("img-persist"); got == nil || got.Kind != models.KindImageGen {
		cleanup()
		t.Fatalf("pre-restart Kind = %v, want %q (write path broke in-memory state?)", got, models.KindImageGen)
	}
	cleanup()

	// Phase 2 — NEW store instance from the same DB file.
	mgr2, cleanup2 := reopenKindManagerAt(t, dbPath)
	defer cleanup2()

	loaded := mgr2.GetModel("img-persist")
	if loaded == nil {
		t.Fatal("model lost across restart")
	}
	if loaded.Kind != models.KindImageGen {
		t.Fatalf("POST-RESTART Kind = %q, want %q — kind is memory-only again (ship-blocker regression)", loaded.Kind, models.KindImageGen)
	}
	if !loaded.IsImageGen() {
		t.Error("IsImageGen() false post-restart — the /fe/api/models kind filter would misroute this model to chat")
	}

	strict, err := mgr2.GetModelStrict(context.Background(), "img-persist")
	if err != nil {
		t.Fatalf("GetModelStrict post-restart: %v", err)
	}
	if strict.Kind != models.KindImageGen {
		t.Errorf("strict read post-restart Kind = %q, want %q", strict.Kind, models.KindImageGen)
	}

	var listKind, enabledKind string
	for _, m := range mgr2.GetModels() {
		if m.ID == "img-persist" {
			listKind = m.Kind
		}
	}
	for _, m := range mgr2.GetEnabledModels() {
		if m.ID == "img-persist" {
			enabledKind = m.Kind
		}
	}
	if listKind != models.KindImageGen {
		t.Errorf("GetModels list post-restart Kind = %q, want %q", listKind, models.KindImageGen)
	}
	if enabledKind != models.KindImageGen {
		t.Errorf("GetEnabledModels list post-restart Kind = %q, want %q", enabledKind, models.KindImageGen)
	}
}

// TestModelsManager_KindPersistsAcrossRestart_UpdatePath covers the
// second write path: a chat model flipped to image-gen via
// UpdateModel must keep Kind == "image-gen" across a restart.
func TestModelsManager_KindPersistsAcrossRestart_UpdatePath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	mgr, cleanup := setupKindManagerAt(t, dbPath)
	chat := models.ModelConfig{
		ID:      "flip-model",
		Name:    "Flip Model",
		Enabled: true,
	}
	if err := mgr.AddModel(chat); err != nil {
		cleanup()
		t.Fatalf("AddModel chat: %v", err)
	}

	flipped := models.ModelConfig{
		ID:            "flip-model",
		Name:          "Flip Model",
		Enabled:       true,
		Kind:          models.KindImageGen,
		Internal:      true,
		InternalModel: "image-01",
		Credentials:   models.TestRefs("minimax-cred"),
	}
	if err := mgr.UpdateModel("flip-model", flipped); err != nil {
		cleanup()
		t.Fatalf("UpdateModel → image-gen: %v", err)
	}
	if got := mgr.GetModel("flip-model"); got == nil || got.Kind != models.KindImageGen {
		cleanup()
		t.Fatalf("pre-restart post-Update Kind = %v, want %q (UPDATE path lost kind in memory)", got, models.KindImageGen)
	}
	cleanup()

	mgr2, cleanup2 := reopenKindManagerAt(t, dbPath)
	defer cleanup2()

	loaded := mgr2.GetModel("flip-model")
	if loaded == nil {
		t.Fatal("model lost across restart")
	}
	if loaded.Kind != models.KindImageGen {
		t.Fatalf("POST-RESTART Kind after UpdateModel = %q, want %q — UPDATE write path does not persist kind", loaded.Kind, models.KindImageGen)
	}
	if strict, err := mgr2.GetModelStrict(context.Background(), "flip-model"); err != nil || strict.Kind != models.KindImageGen {
		t.Errorf("strict read after UpdateModel round-trip: err=%v Kind=%q, want %q", err, strict.Kind, models.KindImageGen)
	}
}

// TestModelsManager_KindPersistsAcrossRestart_ChatUnchanged is the
// chat-model round-trip guard: a Kind=="" (chat default) model must
// STILL load as "" post-restart — never mutated to "chat" — so the
// omitempty wire shape stays byte-stable for every pre-ImgGen
// client.
func TestModelsManager_KindPersistsAcrossRestart_ChatUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	mgr, cleanup := setupKindManagerAt(t, dbPath)
	if err := mgr.AddModel(models.ModelConfig{
		ID:      "chat-stable",
		Name:    "Chat Stable",
		Enabled: true,
	}); err != nil {
		cleanup()
		t.Fatalf("AddModel chat: %v", err)
	}
	cleanup()

	mgr2, cleanup2 := reopenKindManagerAt(t, dbPath)
	defer cleanup2()

	loaded := mgr2.GetModel("chat-stable")
	if loaded == nil {
		t.Fatal("model lost across restart")
	}
	if loaded.Kind != "" {
		t.Errorf("chat model Kind round-tripped to %q, want \"\" (empty = chat; must stay empty for omitempty wire stability)", loaded.Kind)
	}
	if loaded.IsImageGen() {
		t.Error("chat model IsImageGen() true post-restart — inverted discriminator")
	}
}

// TestModelsManager_LegacyNullKindRow_LoadsAsChat is test (b): a
// pre-ImgGen row whose kind column is NULL (the exact shape every
// row has between migration 030 and the next kind-bearing write)
// must load as chat (Kind == "") on every read path — back-compat
// without any backfill statement.
func TestModelsManager_LegacyNullKindRow_LoadsAsChat(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	mgr, cleanup := setupKindManagerAt(t, dbPath)
	// Raw INSERT in the legacy column shape — the kind column is
	// simply absent, so it stores NULL (proves the migration needs
	// no DEFAULT/backfill).
	_, err := mgr.store.DB.ExecContext(context.Background(),
		`INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json, internal, credentials_json, credential_id, internal_base_url, internal_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-row", "Legacy Row", 1, "[]", "[]", 0, "[]", "", "", "",
	)
	if err != nil {
		cleanup()
		t.Fatalf("legacy-shape insert: %v", err)
	}
	var rawKind interface{}
	if err := mgr.store.DB.QueryRow(`SELECT kind FROM models WHERE id = ?`, "legacy-row").Scan(&rawKind); err != nil {
		cleanup()
		t.Fatalf("select raw kind: %v", err)
	}
	if rawKind != nil {
		cleanup()
		t.Fatalf("legacy row kind = %v, want NULL (test premise broken)", rawKind)
	}
	cleanup()

	mgr2, cleanup2 := reopenKindManagerAt(t, dbPath)
	defer cleanup2()

	loaded := mgr2.GetModel("legacy-row")
	if loaded == nil {
		t.Fatal("legacy row lost")
	}
	if loaded.Kind != "" {
		t.Errorf("NULL-kind legacy row loaded Kind = %q, want \"\" (chat)", loaded.Kind)
	}
	if loaded.IsImageGen() {
		t.Error("NULL-kind legacy row IsImageGen() true — legacy rows would leak into the ImgGen tab")
	}

	strict, err := mgr2.GetModelStrict(context.Background(), "legacy-row")
	if err != nil {
		t.Fatalf("GetModelStrict legacy row: %v", err)
	}
	if strict.Kind != "" {
		t.Errorf("strict read of NULL-kind row Kind = %q, want \"\"", strict.Kind)
	}

	for _, m := range mgr2.GetModels() {
		if m.ID == "legacy-row" && m.Kind != "" {
			t.Errorf("list read of NULL-kind row Kind = %q, want \"\"", m.Kind)
		}
	}
}
