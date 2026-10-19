package database

// P0 shadowfix — "group member id shadowed by its own group" (the
// credential-LB engine refresh path).
//
// The engine invalidation drain (drainCredentialsChanged) refreshes
// the CREDENTIAL-LB state of the model named by a
// model.credentials.changed event. The event is published by the
// model write path (AddModel/UpdateModel) for the model that was
// written — and the drain re-reads that model to pick up its
// credential list.
//
// The re-read was CLAIMS-FIRST. For a member ID that its own group
// claims (the day-1 self-shadow shape coding-group[coding]) the
// claims-first read returned the GROUP row — whose credential list is
// EMPTY — so the refresh called OnModelChanged(id, nil), which
// DELETES the member's engine state (credentiallb/engine.go:366).
// The member's next multi-credential resolution then hit
// ErrNoCredentials and failed with "failed to resolve internal config
// for model coding" (502) — and since the drain always fed it the
// group row, the state was never restored: a PERMANENT failure for
// that member (until a reboot re-seeds the engine).
//
// The drain must re-read by ROW IDENTITY (GetModelNative).

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestShadowFix_CredentialsChangedDrain_KeepsSelfShadowMemberEngineState
// — after a member write, the member's credential-LB engine state
// must survive the bus-driven refresh.
func TestShadowFix_CredentialsChangedDrain_KeepsSelfShadowMemberEngineState(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()
	// A REAL bus — the drain only runs when the manager has one.
	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	defer mgr.Close()

	for _, c := range []struct{ id, provider string }{
		{"cred-openai", "openai"},
		{"cred-openai-2", "openai"},
	} {
		if err := mgr.AddCredential(models.CredentialConfig{ID: c.id, Provider: c.provider, APIKey: "k"}); err != nil {
			t.Fatalf("AddCredential %s: %v", c.id, err)
		}
	}
	// The member carries TWO credentials — the engine path (GetOrSelect)
	// is what a wiped engine state breaks (the single-credential E-3
	// fast path never consults the engine).
	member := models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-openai", Weight: 1, Position: 0},
			{CredentialID: "cred-openai-2", Weight: 1, Position: 1},
		},
	}
	if err := mgr.AddModel(member); err != nil {
		t.Fatalf("AddModel(coding): %v", err)
	}
	// The group claims "coding" through its own shadow.
	if err := mgr.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
		ShadowModelID:                "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}

	// A member write — this publishes model.credentials.changed for
	// "coding" and the drain re-reads it.
	member.InternalModel = "glm-5b"
	if err := mgr.UpdateModel("coding", member); err != nil {
		t.Fatalf("UpdateModel(coding): %v", err)
	}

	// Let the asynchronous drain process the event. The bus delivery is
	// sub-millisecond; the settle window is generous so the assertion
	// is about the settled state, not about a race.
	time.Sleep(500 * time.Millisecond)

	res, ok := mgr.ResolveInternalConfigWithAffinity("coding", "conv-1")
	if !ok {
		t.Fatalf("the self-shadow member did not resolve after its own write — the credentials-changed drain wiped its credential-LB engine state (the claims-first re-read returned the credential-less group row)")
	}
	if res.CredentialID == "" {
		t.Fatalf("resolved credential is empty: %+v", res)
	}
}
