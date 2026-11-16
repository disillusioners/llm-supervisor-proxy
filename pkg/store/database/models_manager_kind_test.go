package database

// decision-groups commission — the decision VOCABULARY split pin.
//
// The CREDENTIAL vocabulary and the DECISION-MODEL vocabulary are two
// SEPARATE maps, nested but never crossed:
//
//   - a CREDENTIAL may carry any provider of the credential map
//     (including "anthropic");
//   - a decision MODEL may only reference a credential whose provider
//     is in the decision map {openai, typesafe, openrouter}.
//
// The positive case pins the split: an anthropic credential SAVES,
// while a decision model referencing it is REJECTED.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected
// — the credential/decision vocabulary split, positive case: an
// "anthropic" credential SAVES (the credential vocabulary includes
// it) while a decision MODEL referencing it is REJECTED (the decision
// model's vocabulary does not).
func TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vocab-split.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	// The anthropic CREDENTIAL saves (the credential vocabulary is a
	// separate map).
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "anthropic-cred", Provider: "anthropic", APIKey: "k",
	}); err != nil {
		t.Fatalf("an anthropic credential must save: %v", err)
	}

	// A CHAT model referencing it saves too (the credential map is
	// not the decision map).
	if err := mgr.AddModel(models.ModelConfig{
		ID: "chat-1", Name: "Chat 1", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "claude-3",
		Credentials: []models.CredentialRef{{CredentialID: "anthropic-cred", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("a chat model with an anthropic credential must save: %v", err)
	}

	// The DECISION model referencing it is REJECTED (the decision
	// vocabulary does not include anthropic).
	err = mgr.AddModel(models.ModelConfig{
		ID: "dec-1", Name: "Decision 1", Enabled: true, Kind: models.KindDecision,
		Internal: true, InternalModel: "gpt-6-luna",
		Credentials: []models.CredentialRef{{CredentialID: "anthropic-cred", Weight: 1, Position: 0}},
	})
	if err == nil {
		t.Fatal("a decision model referencing an anthropic credential must be rejected (the two vocabularies must never be crossed)")
	}
}
