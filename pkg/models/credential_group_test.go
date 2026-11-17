package models

import (
	"testing"
)

func TestValidateCredentialGroupGRules(t *testing.T) {
	credLookup := func(id string) *CredentialConfig {
		switch id {
		case "c1":
			return &CredentialConfig{ID: "c1", Provider: "openai"}
		case "c2":
			return &CredentialConfig{ID: "c2", Provider: "anthropic"}
		}
		return nil
	}

	// G-1 empty ID
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "", Name: "n", Provider: "openai"}, credLookup); err == nil {
		t.Fatal("G-1: expected error for empty id")
	}
	// G-2 empty name
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "", Provider: "openai"}, credLookup); err == nil {
		t.Fatal("G-2: expected error for empty name")
	}
	// G-3 invalid provider
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "nope"}, credLookup); err == nil {
		t.Fatal("G-3: expected error for invalid provider")
	}
	// G-4 >16 members
	members := make([]string, 17)
	for i := range members {
		members[i] = "c1"
	}
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: members}, credLookup); err == nil {
		t.Fatal("G-4: expected error for >16 members")
	}
	// G-5 duplicate
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: []string{"c1", "c1"}}, credLookup); err == nil {
		t.Fatal("G-5: expected error for duplicate member")
	}
	// G-6 missing member
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: []string{"nope"}}, credLookup); err == nil {
		t.Fatal("G-6: expected error for missing member")
	}
	// G-7 provider mismatch
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: []string{"c2"}}, credLookup); err == nil {
		t.Fatal("G-7: expected error for provider mismatch")
	}
	// G-8 empty members allowed
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: nil}, credLookup); err != nil {
		t.Fatalf("G-8: empty members should be allowed: %v", err)
	}
	// valid group
	if err := ValidateCredentialGroup(CredentialGroupConfig{ID: "g", Name: "n", Provider: "openai", Members: []string{"c1"}}, credLookup); err != nil {
		t.Fatalf("valid group rejected: %v", err)
	}
	t.Log("--- PASS")
}

func TestValidateCredentialRefsRRules(t *testing.T) {
	groups := map[string]CredentialGroupConfig{
		"g1":     {ID: "g1", Name: "g", Provider: "openai", Members: []string{"c1", "c3"}},
		"emptyg": {ID: "emptyg", Name: "e", Provider: "openai", Members: nil},
	}
	creds := map[string]CredentialConfig{
		"c1": {ID: "c1", Provider: "openai"},
		"c2": {ID: "c2", Provider: "openai"},
		"c3": {ID: "c3", Provider: "openai"},
	}

	// R-1 both-set
	refs := []CredentialRef{{CredentialID: "c1", GroupID: "g1", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-1 both-set: expected error")
	}
	// R-1 neither-set
	refs = []CredentialRef{{Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-1 neither-set: expected error")
	}
	// R-2 direct missing cred
	refs = []CredentialRef{{CredentialID: "nope", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-2 missing cred: expected error")
	}
	// R-2 weight <=0
	refs = []CredentialRef{{CredentialID: "c1", Weight: 0}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-2 weight: expected error")
	}
	// R-3 group missing
	refs = []CredentialRef{{GroupID: "nope", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-3 missing group: expected error")
	}
	// R-3 weight <=0
	refs = []CredentialRef{{GroupID: "g1", Weight: 0}}
	if err := ValidateCredentialRefs(refs, groups, creds); err == nil {
		t.Fatal("R-3 weight: expected error")
	}
	// R-4 expanded >16: build a group with 17 members
	bigMembers := make([]string, 17)
	for i := range bigMembers {
		bigMembers[i] = "c1"
	}
	bigGroups := map[string]CredentialGroupConfig{
		"big": {ID: "big", Name: "big", Provider: "openai", Members: bigMembers},
	}
	refs = []CredentialRef{{GroupID: "big", Weight: 1}}
	if err := ValidateCredentialRefs(refs, bigGroups, creds); err == nil {
		t.Fatal("R-4 expanded: expected error")
	}
	// R-5 non-chat kind forbids group ref
	refs = []CredentialRef{{GroupID: "g1", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds, KindDecision); err == nil {
		t.Fatal("R-5: expected error for group ref in decision kind")
	}
	// R-7 one empty group flattens to 0 refs allowed
	refs = []CredentialRef{{GroupID: "emptyg", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err != nil {
		t.Fatalf("R-7: empty group should be allowed: %v", err)
	}
	// valid mixed
	refs = []CredentialRef{{CredentialID: "c2", Weight: 1}, {GroupID: "g1", Weight: 1}}
	if err := ValidateCredentialRefs(refs, groups, creds); err != nil {
		t.Fatalf("valid mixed rejected: %v", err)
	}
	t.Log("--- PASS")
}

func TestModelsConfigValidateRejectsGroupRef(t *testing.T) {
	mc := &ModelsConfig{}
	mc.Models = []ModelConfig{
		{
			ID:            "m1",
			Name:          "m1",
			Enabled:       true,
			Internal:      true,
			InternalModel: "x",
			Credentials:   []CredentialRef{{GroupID: "g1", Weight: 1}},
		},
	}
	mc.Credentials = NewCredentialsConfig()
	if err := mc.Validate(); err == nil {
		t.Fatal("expected JSON twin to reject group ref")
	}
	t.Log("--- PASS")
}

func TestStubContract(t *testing.T) {
	mc := &ModelsConfig{}
	m := mc
	if got := m.GetCredentialGroup("x"); got != nil {
		t.Fatalf("GetCredentialGroup stub: got %v want nil", got)
	}
	if got := m.GetCredentialGroups(); got != nil {
		t.Fatalf("GetCredentialGroups stub: got %v want nil", got)
	}
	if err := m.AddCredentialGroup(CredentialGroupConfig{}); err != nil {
		t.Fatalf("AddCredentialGroup stub: %v", err)
	}
	if err := m.UpdateCredentialGroup("x", CredentialGroupConfig{}); err != nil {
		t.Fatalf("UpdateCredentialGroup stub: %v", err)
	}
	if err := m.RemoveCredentialGroup("x"); err != nil {
		t.Fatalf("RemoveCredentialGroup stub: %v", err)
	}
	if got := m.ModelsUsingCredentialGroup("x"); got != nil {
		t.Fatalf("ModelsUsingCredentialGroup stub: got %v want nil", got)
	}
	if got := m.GroupsContainingCredential("x"); got != nil {
		t.Fatalf("GroupsContainingCredential stub: got %v want nil", got)
	}
	// FlattenCredentialRefs identity fallback
	model := &ModelConfig{Credentials: []CredentialRef{{CredentialID: "c1", Weight: 1}}}
	got := m.FlattenCredentialRefs(model)
	if len(got) != 1 || got[0].CredentialID != "c1" {
		t.Fatalf("FlattenCredentialRefs stub: got %v", got)
	}
	t.Log("--- PASS")
}
