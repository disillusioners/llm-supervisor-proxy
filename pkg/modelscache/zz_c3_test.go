package modelscache

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
)

func TestC3_GroupCachePrimed(t *testing.T) {
	inner := &fakeStrictSource{
		models: fixtureModels(),
		creds:  fixtureCreds(),
		groups: []models.CredentialGroupConfig{
			{ID: "g1", Name: "g1", Provider: "openai", Members: []string{"cred-1"}},
		},
	}
	c := mustWrap(t, inner, Options{})
	gs := c.GetCredentialGroups()
	if len(gs) != 1 || gs[0].ID != "g1" {
		t.Fatalf("expected 1 group after boot, got %d", len(gs))
	}
	if g := c.GetCredentialGroup("g1"); g == nil || g.Provider != "openai" {
		t.Fatalf("GetCredentialGroup miss: %+v", g)
	}
}

func TestC3_ResolveInternalConfig_GroupPrimary(t *testing.T) {
	ms := []models.ModelConfig{
		{ID: "m-group", Name: "group", Enabled: true, Internal: true, InternalModel: "group-x",
			Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1, Position: 0}}},
	}
	inner := &fakeStrictSource{
		models: ms,
		creds:  fixtureCreds(),
		groups: []models.CredentialGroupConfig{
			{ID: "g1", Name: "g1", Provider: "openai", Members: []string{"cred-1"}},
		},
	}
	c := mustWrap(t, inner, Options{})
	provider, apiKey, _, model, ok := c.ResolveInternalConfig("m-group")
	if !ok || provider != "openai" || apiKey != "sk-plain-1" || model != "group-x" {
		t.Fatalf("resolve failed: (%q,%q,%q,%v)", provider, apiKey, model, ok)
	}
	if got := normalizers.DetectProvider(c, "m-group"); got != "openai" {
		t.Fatalf("DetectProvider = %q, want openai", got)
	}
}

func TestC3_ReconcileHealsDrift(t *testing.T) {
	inner := &fakeStrictSource{
		models: fixtureModels(),
		creds:  fixtureCreds(),
		groups: []models.CredentialGroupConfig{},
	}
	c := mustWrap(t, inner, Options{})
	if len(c.GetCredentialGroups()) != 0 {
		t.Fatal("expected empty initially")
	}
	// simulate a direct DB write by mutating inner groups
	inner.mu.Lock()
	inner.groups = []models.CredentialGroupConfig{
		{ID: "g2", Name: "g2", Provider: "openai", Members: []string{"cred-1"}},
	}
	inner.mu.Unlock()
	c.reconcileOnce()
	if len(c.GetCredentialGroups()) != 1 || c.GetCredentialGroups()[0].ID != "g2" {
		t.Fatalf("reconcile did not heal drift: %+v", c.GetCredentialGroups())
	}
}

func TestC3_RebuildBeforeSnapshot(t *testing.T) {
	inner := &fakeStrictSource{
		models: fixtureModels(),
		creds:  fixtureCreds(),
		groups: []models.CredentialGroupConfig{},
	}
	called := false
	inner.rebuildGroups = func() error {
		called = true
		inner.mu.Lock()
		inner.groups = []models.CredentialGroupConfig{
			{ID: "g3", Name: "g3", Provider: "openai", Members: []string{"cred-1"}},
		}
		inner.mu.Unlock()
		return nil
	}
	c := mustWrap(t, inner, Options{})
	_ = c
	if !called {
		t.Fatal("rebuild not called at boot")
	}
	if len(c.GetCredentialGroups()) != 1 || c.GetCredentialGroups()[0].ID != "g3" {
		t.Fatalf("group not primed after rebuild: %+v", c.GetCredentialGroups())
	}
}
