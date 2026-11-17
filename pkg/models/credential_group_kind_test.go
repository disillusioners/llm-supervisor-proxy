package models

// credential-groups commission — the R-5 kind gate.
//
// The R-5 rule ("non-chat model kinds forbid group refs") used to be
// expressed with string literals ("decision" / "imggen" / "group"). The
// "imggen" literal matches NO kind constant in this repo — the
// image-generation kind is KindImageGen == "image-gen" (config.go) — so
// the R-5 gate silently NEVER fired for image-gen models: a group ref on
// an image-gen model was ACCEPTED. The kind constants are used here so
// the test (and the validator) can never drift from them again.

import (
	"testing"
)

// TestValidateCredentialRefs_R5_NonChatKindForbidsGroupRef proves the R-5
// rule fires for EVERY non-chat kind the repo defines, and that a chat
// kind still ACCEPTS a group ref (the rule's whole point is the split).
//
// The kinds are the package's OWN constants — a literal typo in the
// validator (or in this table) is now impossible to hide.
func TestValidateCredentialRefs_R5_NonChatKindForbidsGroupRef(t *testing.T) {
	groups := map[string]CredentialGroupConfig{
		"g1": {ID: "g1", Name: "g", Provider: "openai", Members: []string{"c1"}},
	}
	creds := map[string]CredentialConfig{
		"c1": {ID: "c1", Provider: "openai"},
	}
	refs := []CredentialRef{{GroupID: "g1", Weight: 1}}

	cases := []struct {
		kind        string
		wantAllowed bool // true = a group ref is ACCEPTED for this kind
	}{
		{KindChat, true},      // chat models may reference a group
		{KindDecision, false}, // decision models may NOT
		{KindImageGen, false}, // image-generation models may NOT
		{KindGroup, false},    // virtual group models may NOT
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			err := ValidateCredentialRefs(refs, groups, creds, tc.kind)
			if tc.wantAllowed {
				if err != nil {
					t.Fatalf("kind %q: group ref rejected, want accepted: %v", tc.kind, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("kind %q: group ref ACCEPTED, want rejected (R-5 forbids group refs on non-chat kinds)", tc.kind)
			}
		})
	}
	t.Log("--- PASS")
}
