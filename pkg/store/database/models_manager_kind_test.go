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
