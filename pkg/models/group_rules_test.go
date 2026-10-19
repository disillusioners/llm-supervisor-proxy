package models

// load-balance-groups commission / §9.2 + P1.2 — table-driven
// ValidateKindRules group-arm + ValidateGroupMembers tests.
//
// Covers: G1-G8 each violation, the valid fixture (including the
// span-provider member set — the deliberate divergence pin: groups
// may mix members of ANY provider because they carry no
// credentials), G8 uniqueness across two groups, and the
// SELF-SHADOW-LEGAL case (a group claiming one of its own members
// — the canonical day-1 `coding[coding]` onboarding, Am22).

import (
	"strings"
	"testing"
)

// groupBaseFixture returns a fully valid group row plus the
// collection it lives in (two chat members on different
// providers — span-provider is VALID, the divergence pin).
func groupBaseFixture() (ModelConfig, []ModelConfig) {
	memberA := ModelConfig{ID: "coding", Name: "Coding", Enabled: true, Kind: KindChat, Internal: true, InternalModel: "glm-5", Credentials: []CredentialRef{{CredentialID: "cred-openai", Weight: 1, Position: 0}}}
	memberB := ModelConfig{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: "", Internal: true, InternalModel: "kimi", Credentials: []CredentialRef{{CredentialID: "cred-zhipu", Weight: 1, Position: 0}}}
	group := ModelConfig{
		ID:                           "coding-group",
		Name:                         "Coding Group",
		Enabled:                      true,
		Kind:                         KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []GroupMember{{ModelID: "coding", Weight: 2}, {ModelID: "coding-alt", Weight: 1}},
	}
	collection := []ModelConfig{memberA, memberB, group}
	return group, collection
}

// TestValidateKindRules_Group_ValidFixture — the valid group (plus
// the span-provider member set via ValidateGroupMembers) passes.
func TestValidateKindRules_Group_ValidFixture(t *testing.T) {
	group, collection := groupBaseFixture()
	if err := ValidateKindRules(&group, ""); err != nil {
		t.Errorf("valid group should pass validateGroupRules, got: %v", err)
	}
	if err := ValidateGroupMembers(&group, collection); err != nil {
		t.Errorf("valid group with span-provider members should pass ValidateGroupMembers (divergence pin), got: %v", err)
	}
}

// TestValidateKindRules_Group_FieldRuleViolations — G1, G6, G7 and
// the G8 self-part, via the shared ValidateKindRules seam.
func TestValidateKindRules_Group_FieldRuleViolations(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(m *ModelConfig)
		wantErrSub string
	}{
		{
			name:       "G1_group_requires_internal",
			mutate:     func(m *ModelConfig) { m.Internal = false },
			wantErrSub: "requires internal to be true",
		},
		{
			name:       "G6_group_rejects_fallback_chain",
			mutate:     func(m *ModelConfig) { m.FallbackChain = []string{"other"} },
			wantErrSub: "does not support fallback_chain",
		},
		{
			name:       "G6_group_rejects_secondary_upstream_model",
			mutate:     func(m *ModelConfig) { m.SecondaryUpstreamModel = "x" },
			wantErrSub: "does not support secondary_upstream_model",
		},
		{
			name:       "G6_group_rejects_peak_hour_enabled",
			mutate:     func(m *ModelConfig) { m.PeakHourEnabled = true },
			wantErrSub: "does not support peak_hour_enabled",
		},
		{
			name:       "G6_group_rejects_release_stream_chunk_deadline",
			mutate:     func(m *ModelConfig) { m.ReleaseStreamChunkDeadline = Duration(60 * 1e9) },
			wantErrSub: "does not support release_stream_chunk_deadline",
		},
		{
			name:       "G7_group_requires_exclude_from_ultimate_switching",
			mutate:     func(m *ModelConfig) { m.ExcludeFromUltimateSwitching = false },
			wantErrSub: "requires exclude_from_ultimate_switching to be true",
		},
		{
			name:       "G8_group_cannot_shadow_own_id",
			mutate:     func(m *ModelConfig) { m.ShadowModelID = m.ID },
			wantErrSub: "cannot be the group's own ID",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group, _ := groupBaseFixture()
			tc.mutate(&group)
			err := ValidateKindRules(&group, "")
			if err == nil {
				t.Fatalf("expected group rule violation, got nil (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain expected phrase %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

// TestValidateGroupMembers_CollectionRuleViolations — G2, G3, G4,
// G5 and the collection half of G8, via the exported
// collection-aware validator.
func TestValidateGroupMembers_CollectionRuleViolations(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(m *ModelConfig, coll *[]ModelConfig)
		wantErrSub string
	}{
		{
			name:       "G2_group_requires_at_least_one_member",
			mutate:     func(m *ModelConfig, coll *[]ModelConfig) { m.Members = nil },
			wantErrSub: "requires at least one member",
		},
		{
			name: "G2_group_member_cap_16",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				members := make([]GroupMember, 0, MaxGroupMembers+1)
				for i := 0; i <= MaxGroupMembers; i++ {
					members = append(members, GroupMember{ModelID: "coding", Weight: 1})
				}
				m.Members = members
			},
			wantErrSub: "exceeds max of 16 entries",
		},
		{
			name: "G3_member_must_exist",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				m.Members = []GroupMember{{ModelID: "no-such-model", Weight: 1}}
			},
			wantErrSub: "does not exist",
		},
		{
			name: "G3_member_weight_must_be_positive",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				m.Members = []GroupMember{{ModelID: "coding", Weight: 0}}
			},
			wantErrSub: "weight must be > 0",
		},
		{
			name: "G3_member_model_id_must_be_non_empty",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				m.Members = []GroupMember{{ModelID: "", Weight: 1}}
			},
			wantErrSub: "model_id is empty",
		},
		{
			name: "G4_no_duplicate_members",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				m.Members = []GroupMember{{ModelID: "coding", Weight: 1}, {ModelID: "coding", Weight: 2}}
			},
			wantErrSub: "duplicate member",
		},
		{
			name: "G5_group_as_member_forbidden",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				other := ModelConfig{ID: "other-group", Name: "Other", Enabled: true, Kind: KindGroup, Internal: true, ExcludeFromUltimateSwitching: true, Members: []GroupMember{{ModelID: "coding", Weight: 1}}}
				*coll = append(*coll, other)
				m.Members = []GroupMember{{ModelID: "coding", Weight: 1}, {ModelID: "other-group", Weight: 1}}
			},
			wantErrSub: "group-as-member is forbidden",
		},
		{
			name: "G3_member_must_be_chat_kind",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				img := ModelConfig{ID: "img-model", Name: "Img", Enabled: true, Kind: KindImageGen, Internal: true, InternalModel: "image-01", Credentials: []CredentialRef{{CredentialID: "cred-minimax", Weight: 1, Position: 0}}, ExcludeFromUltimateSwitching: true}
				*coll = append(*coll, img)
				m.Members = []GroupMember{{ModelID: "img-model", Weight: 1}}
			},
			wantErrSub: "must be a chat-kind model",
		},
		{
			name: "G8_shadow_target_must_exist",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				m.ShadowModelID = "no-such-model"
			},
			wantErrSub: "does not exist",
		},
		{
			name: "G8_shadow_target_must_be_chat_kind",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				img := ModelConfig{ID: "img-model", Name: "Img", Enabled: true, Kind: KindImageGen, Internal: true, InternalModel: "image-01", Credentials: []CredentialRef{{CredentialID: "cred-minimax", Weight: 1, Position: 0}}, ExcludeFromUltimateSwitching: true}
				*coll = append(*coll, img)
				m.ShadowModelID = "img-model"
			},
			wantErrSub: "must reference a chat-kind model",
		},
		{
			name: "G8_shadow_target_cannot_be_a_group",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				other := ModelConfig{ID: "other-group", Name: "Other", Enabled: true, Kind: KindGroup, Internal: true, ExcludeFromUltimateSwitching: true, Members: []GroupMember{{ModelID: "coding", Weight: 1}}}
				*coll = append(*coll, other)
				m.ShadowModelID = "other-group"
			},
			wantErrSub: "must reference a chat-kind model, not a group",
		},
		{
			name: "G8_uniqueness_no_other_enabled_group_claims_same_id",
			mutate: func(m *ModelConfig, coll *[]ModelConfig) {
				claimer := ModelConfig{ID: "other-group", Name: "Other", Enabled: true, Kind: KindGroup, Internal: true, ExcludeFromUltimateSwitching: true, Members: []GroupMember{{ModelID: "coding-alt", Weight: 1}}, ShadowModelID: "coding"}
				*coll = append(*coll, claimer)
				m.ShadowModelID = "coding"
			},
			wantErrSub: "already claimed by enabled group",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group, collection := groupBaseFixture()
			tc.mutate(&group, &collection)
			err := ValidateGroupMembers(&group, collection)
			if err == nil {
				t.Fatalf("expected member-rule violation, got nil (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain expected phrase %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

// TestValidateGroupMembers_G8_DisabledClaimIsInert — a DISABLED
// other group claiming the same ID does NOT block the write
// (claims activate only on enabled groups; enable-ordering
// resolves through the same scan — §3.8 / arch R1 Am20).
func TestValidateGroupMembers_G8_DisabledClaimIsInert(t *testing.T) {
	group, collection := groupBaseFixture()
	disabledClaimer := ModelConfig{ID: "other-group", Name: "Other", Enabled: false, Kind: KindGroup, Internal: true, ExcludeFromUltimateSwitching: true, Members: []GroupMember{{ModelID: "coding-alt", Weight: 1}}, ShadowModelID: "coding"}
	collection = append(collection, disabledClaimer)
	group.ShadowModelID = "coding"
	if err := ValidateGroupMembers(&group, collection); err != nil {
		t.Errorf("disabled dual-claim should be inert, got: %v", err)
	}
}

// TestValidateGroupMembers_SelfShadowLegal — claiming one of the
// group's OWN members is LEGAL (§3.4 / §5.1 G8 note / Am22): the
// canonical day-1 onboarding where client requests for
// `coding[coding]` keep resolving while the group takes over the
// ID space. Member resolution inside the engine is by direct ID
// (GetModelNative) and never consults claims, so this cannot loop.
func TestValidateGroupMembers_SelfShadowLegal(t *testing.T) {
	group, collection := groupBaseFixture()
	group.ShadowModelID = "coding" // "coding" is also group's member [0]
	if err := ValidateKindRules(&group, ""); err != nil {
		t.Errorf("self-shadow (claiming own member) should pass field rules, got: %v", err)
	}
	if err := ValidateGroupMembers(&group, collection); err != nil {
		t.Errorf("self-shadow (claiming own member) should be LEGAL, got: %v", err)
	}
}

// TestValidateKindRules_Group_IncludedInUnknownKindMessage — the
// default-arm enumeration now names four kinds (drift lock on the
// :188 wording extension).
func TestValidateKindRules_Group_IncludedInUnknownKindMessage(t *testing.T) {
	m := &ModelConfig{ID: "x", Kind: "groupe"}
	err := ValidateKindRules(m, "")
	if err == nil {
		t.Fatal("expected unknown kind to be rejected")
	}
	for _, want := range []string{`"chat"`, `"image-gen"`, `"decision"`, `"group"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("unknown-kind error %q should enumerate %s", err.Error(), want)
		}
	}
}

// ───────────────────────────────────────────────────────────────
// JSON-backend twin (ModelsConfig): save-path + claim-index unit
// tests (§9.2: build/rebuild on add/update/delete; disabled group
// ⇒ inactive claim; RemoveModel member-scan twin / Am27).
// ───────────────────────────────────────────────────────────────

// newGroupJSONConfig builds a JSON-backed ModelsConfig with two
// chat members seeded (no file — in-memory only).
func newGroupJSONConfig() *ModelsConfig {
	mc := NewModelsConfig()
	_ = mc.AddCredential(CredentialConfig{ID: "cred-openai", Provider: "openai", APIKey: "k"})
	_ = mc.AddCredential(CredentialConfig{ID: "cred-zhipu", Provider: "zhipu", APIKey: "k"})
	_ = mc.AddModel(ModelConfig{ID: "coding", Name: "Coding", Enabled: true, Internal: true, InternalModel: "glm-5", Credentials: []CredentialRef{{CredentialID: "cred-openai", Weight: 1, Position: 0}}})
	_ = mc.AddModel(ModelConfig{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Internal: true, InternalModel: "kimi", Credentials: []CredentialRef{{CredentialID: "cred-zhipu", Weight: 1, Position: 0}}})
	return mc
}

func jsonValidGroup() ModelConfig {
	return ModelConfig{
		ID:                           "coding-group",
		Name:                         "Coding Group",
		Enabled:                      true,
		Kind:                         KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []GroupMember{{ModelID: "coding", Weight: 2}, {ModelID: "coding-alt", Weight: 1}},
		ShadowModelID:                "coding",
	}
}

// TestModelsConfig_Group_SavePath — Am23 JSON twin: a group row
// (Internal=true, Credentials=[]) saves cleanly through
// ModelsConfig.Validate's group branch — the generic internal gate
// ("credential_id is required when internal is true") is bypassed.
func TestModelsConfig_Group_SavePath(t *testing.T) {
	mc := newGroupJSONConfig()
	if err := mc.AddModel(jsonValidGroup()); err != nil {
		t.Fatalf("JSON save-path: valid group should save cleanly, got: %v", err)
	}

	// G-violations are rejected (field + collection rules). The
	// broken variants drop the shadow claim so each trips exactly
	// ONE rule (a kept claim would trip the G8 dual-claim scan on
	// the ORIGINAL row first — the scan blames whichever group the
	// loop reaches first, which is correct collection semantics).
	broken := jsonValidGroup()
	broken.ID = "coding-group-g2"
	broken.Members = nil // G2
	broken.ShadowModelID = ""
	if err := mc.AddModel(broken); err == nil {
		t.Fatal("JSON AddModel accepted G2 violation")
	} else if !strings.Contains(err.Error(), "requires at least one member") {
		t.Errorf("JSON G2 error = %q", err.Error())
	}

	brokenKind := jsonValidGroup()
	brokenKind.ID = "coding-group-2"
	brokenKind.ExcludeFromUltimateSwitching = false // G7
	brokenKind.ShadowModelID = ""
	if err := mc.AddModel(brokenKind); err == nil {
		t.Fatal("JSON AddModel accepted G7 violation")
	}

	// UpdateModel runs the same rules on the proposed state.
	upd := jsonValidGroup()
	upd.Members = []GroupMember{{ModelID: "ghost", Weight: 1}} // G3
	upd.ShadowModelID = ""
	if err := mc.UpdateModel("coding-group", upd); err == nil {
		t.Fatal("JSON UpdateModel accepted G3 violation")
	}

	// Valid update passes.
	if err := mc.UpdateModel("coding-group", jsonValidGroup()); err != nil {
		t.Fatalf("JSON UpdateModel valid: %v", err)
	}
}

// TestModelsConfig_Group_ClaimIndex — claim-first GetModel +
// GetModelNative + rebuild-on-disable + rebuild-on-delete on the
// JSON twin.
func TestModelsConfig_Group_ClaimIndex(t *testing.T) {
	mc := newGroupJSONConfig()
	if err := mc.AddModel(jsonValidGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Claim-first: claimed ID → group row.
	if m := mc.GetModel("coding"); m == nil || !m.IsGroup() {
		t.Fatalf("GetModel(coding) = %+v — claim-first should return the group", m)
	}
	// Native twin → the REAL row.
	if m := mc.GetModelNative("coding"); m == nil || m.IsGroup() {
		t.Fatalf("GetModelNative(coding) = %+v — must bypass claims", m)
	}

	// Disable ⇒ claim inactive.
	disabled := jsonValidGroup()
	disabled.Enabled = false
	if err := mc.UpdateModel("coding-group", disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if m := mc.GetModel("coding"); m == nil || m.IsGroup() {
		t.Fatalf("disabled claim must be inactive, got %+v", m)
	}

	// Re-enable ⇒ active again.
	if err := mc.UpdateModel("coding-group", jsonValidGroup()); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if m := mc.GetModel("coding"); m == nil || !m.IsGroup() {
		t.Fatalf("re-enabled claim must be active, got %+v", m)
	}

	// Delete the group ⇒ claim gone (the shadowed model
	// reappears). Note: "coding" is also a MEMBER — the Am27 scan
	// blocks deleting MEMBERS, not the group itself.
	if err := mc.RemoveModel("coding-group"); err != nil {
		t.Fatalf("RemoveModel(group): %v", err)
	}
	if m := mc.GetModel("coding"); m == nil || m.IsGroup() {
		t.Fatalf("post-delete claim must be gone, got %+v", m)
	}
}

// TestModelsConfig_Group_RemoveModel_MemberScan — Am27 JSON twin.
func TestModelsConfig_Group_RemoveModel_MemberScan(t *testing.T) {
	mc := newGroupJSONConfig()
	if err := mc.AddModel(jsonValidGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := mc.RemoveModel("coding-alt") // member
	if err == nil {
		t.Fatal("JSON RemoveModel accepted deletion of a group member")
	}
	if !strings.Contains(err.Error(), "coding-group") {
		t.Errorf("Am27 JSON error should name the group, got: %q", err.Error())
	}

	// Drop the member from the group → delete succeeds.
	upd := jsonValidGroup()
	upd.Members = []GroupMember{{ModelID: "coding", Weight: 1}}
	if err := mc.UpdateModel("coding-group", upd); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := mc.RemoveModel("coding-alt"); err != nil {
		t.Fatalf("after member removal the delete should succeed, got: %v", err)
	}
}

// TestValidateGroupMembers_BackupOnly_D2 —
// group-backup-members / D2: a group must keep ≥1 NON-BACKUP
// (default) member. All-backup is a write-time VALIDATION ERROR.
// Mixed (≥1 default + ≥1 backup) is valid. Mirror at
// pkg/ui/frontend/src/components/config/GroupModelForm.tsx.
func TestValidateGroupMembers_BackupOnly_D2(t *testing.T) {
	t.Run("D2_all_backup_rejected", func(t *testing.T) {
		group, collection := groupBaseFixture()
		// Mark every member as backup-only — D2 must reject.
		group.Members = []GroupMember{
			{ModelID: "coding", Weight: 1, BackupOnly: true},
			{ModelID: "coding-alt", Weight: 1, BackupOnly: true},
		}
		err := ValidateGroupMembers(&group, collection)
		if err == nil {
			t.Fatal("D2: all-backup group should be rejected with ≥1-default error")
		}
		if !strings.Contains(err.Error(), "non-backup (default) member") {
			t.Errorf("D2 error %q does not contain the expected message", err.Error())
		}
	})

	t.Run("D2_mixed_default_and_backup_accepted", func(t *testing.T) {
		group, collection := groupBaseFixture()
		// ≥1 default + ≥1 backup — valid.
		group.Members = []GroupMember{
			{ModelID: "coding", Weight: 1, BackupOnly: false},
			{ModelID: "coding-alt", Weight: 1, BackupOnly: true},
		}
		if err := ValidateGroupMembers(&group, collection); err != nil {
			t.Errorf("D2 mixed: should accept ≥1 default + ≥1 backup, got: %v", err)
		}
	})

	t.Run("D2_absent_field_defaults_to_default_member", func(t *testing.T) {
		group, collection := groupBaseFixture()
		// No BackupOnly field anywhere — backward compat (D7).
		group.Members = []GroupMember{
			{ModelID: "coding", Weight: 1},
			{ModelID: "coding-alt", Weight: 1},
		}
		if err := ValidateGroupMembers(&group, collection); err != nil {
			t.Errorf("D7: groups without backup_only fields should round-trip identically, got: %v", err)
		}
	})
}
