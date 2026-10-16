package database

// load-balance-groups / P1.5 + P1.8 — DB-backend group persistence
// tests (§9.3):
//
//   - Am23 SAVE-PATH: a group row saves cleanly through
//     validateBeforeWrite (the validateGroupMembers routing
//     bypasses the validateModelAgainstCredentials Internal gate
//     at store.go:1597-1604, which rejects Internal=true +
//     Credentials=[] — exactly the group-row shape; a misplaced
//     bypass means groups are unsavable).
//   - members_json / shadow_model_id round-trip.
//   - G-violations rejected at AddModel + UpdateModel.
//   - Claim-aware GetModel (claimed ID → group row) + GetModelNative
//     + strict twins (→ real row).
//   - Disabled group ⇒ inactive claim.
//   - Am27 DeleteModel member-scan (blocked, error names the group;
//     succeeds after member removal).
//   - Resolver-untouched regression pin (a group ID resolves
//     ok=false — no kind-branch exists in the resolver under the
//     Round-2 pivot).

import (
	"context"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// newGroupManager creates a migrated SQLite store + ModelsManager
// with two chat member models seeded (span-provider: openai +
// zhipu — the divergence pin).
func newGroupManager(t *testing.T) (*ModelsManager, func()) {
	t.Helper()
	store, cleanup := newStoreWithMigrations(t)
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		cleanup()
		t.Fatalf("NewModelsManager: %v", err)
	}
	for _, c := range []struct{ id, provider string }{
		{"cred-openai", "openai"},
		{"cred-zhipu", "zhipu"},
	} {
		if err := mgr.AddCredential(models.CredentialConfig{ID: c.id, Provider: c.provider, APIKey: "k"}); err != nil {
			cleanup()
			t.Fatalf("AddCredential %s: %v", c.id, err)
		}
	}
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu")},
	} {
		if err := mgr.AddModel(m); err != nil {
			cleanup()
			t.Fatalf("seed member %s: %v", m.ID, err)
		}
	}
	return mgr, cleanup
}

// validGroup returns a fully valid group row (2 model-ID members,
// span-provider, self-shadow claim on its first member — the
// canonical day-1 shape).
func validGroup() models.ModelConfig {
	return models.ModelConfig{
		ID:                           "coding-group",
		Name:                         "Coding Group",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 2},
			{ModelID: "coding-alt", Weight: 1},
		},
		ShadowModelID: "coding",
	}
}

// TestGroup_SavePath_And_RoundTrip — Am23 pin + column round-trip.
func TestGroup_SavePath_And_RoundTrip(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	group := validGroup()
	// Am23: the save must clear validateBeforeWrite — whose
	// Internal gate (validateModelAgainstCredentials) rejects
	// Internal=true + Credentials=[] — exactly this row's shape.
	if err := mgr.AddModel(group); err != nil {
		t.Fatalf("Am23 save-path: valid group should save cleanly through validateBeforeWrite, got: %v", err)
	}

	got := mgr.GetModelNative("coding-group")
	if got == nil {
		t.Fatal("group row not found after AddModel")
	}
	if got.Kind != models.KindGroup || !got.IsGroup() {
		t.Errorf("round-trip: Kind = %q, want %q (IsGroup=%v)", got.Kind, models.KindGroup, got.IsGroup())
	}
	if !got.Internal || !got.ExcludeFromUltimateSwitching {
		t.Errorf("round-trip: Internal=%v ExcludeFromUltimateSwitching=%v, want true/true", got.Internal, got.ExcludeFromUltimateSwitching)
	}
	if len(got.Members) != 2 {
		t.Fatalf("round-trip: Members len = %d, want 2 (got %+v)", len(got.Members), got.Members)
	}
	if got.Members[0].ModelID != "coding" || got.Members[0].Weight != 2 {
		t.Errorf("round-trip: Members[0] = %+v, want {coding 2}", got.Members[0])
	}
	if got.Members[1].ModelID != "coding-alt" || got.Members[1].Weight != 1 {
		t.Errorf("round-trip: Members[1] = %+v, want {coding-alt 1}", got.Members[1])
	}
	if got.ShadowModelID != "coding" {
		t.Errorf("round-trip: ShadowModelID = %q, want %q", got.ShadowModelID, "coding")
	}

	// Non-group rows must keep Members nil / ShadowModelID ""
	// (columns normalize to the empty shape).
	chat := mgr.GetModelNative("coding")
	if chat == nil {
		t.Fatal("chat member row lost")
	}
	if chat.Members != nil || chat.ShadowModelID != "" {
		t.Errorf("chat row should round-trip member-free, got Members=%v ShadowModelID=%q", chat.Members, chat.ShadowModelID)
	}

	// UpdateModel rewrites both columns (members_json + claim
	// change).
	group.Members = []models.GroupMember{{ModelID: "coding-alt", Weight: 3}}
	group.ShadowModelID = "coding-alt"
	if err := mgr.UpdateModel("coding-group", group); err != nil {
		t.Fatalf("UpdateModel group: %v", err)
	}
	got = mgr.GetModelNative("coding-group")
	if len(got.Members) != 1 || got.Members[0].ModelID != "coding-alt" || got.Members[0].Weight != 3 {
		t.Errorf("post-update Members = %+v, want single {coding-alt 3}", got.Members)
	}
	if got.ShadowModelID != "coding-alt" {
		t.Errorf("post-update ShadowModelID = %q, want coding-alt", got.ShadowModelID)
	}
}

// TestGroup_AddModel_GViolations — every G-violation is rejected at
// the DB write path (both field-level and collection rules).
func TestGroup_AddModel_GViolations(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(m *models.ModelConfig)
		wantErrSub string
	}{
		{"G1_internal_required", func(m *models.ModelConfig) { m.Internal = false }, "requires internal to be true"},
		{"G2_member_required", func(m *models.ModelConfig) { m.Members = nil }, "requires at least one member"},
		{"G2_cap_16", func(m *models.ModelConfig) {
			ms := make([]models.GroupMember, 0, models.MaxGroupMembers+1)
			for i := 0; i <= models.MaxGroupMembers; i++ {
				ms = append(ms, models.GroupMember{ModelID: "coding", Weight: 1})
			}
			m.Members = ms
		}, "exceeds max of 16"},
		{"G3_member_must_exist", func(m *models.ModelConfig) {
			m.Members = []models.GroupMember{{ModelID: "ghost", Weight: 1}}
		}, "does not exist"},
		{"G3_weight_positive", func(m *models.ModelConfig) {
			m.Members = []models.GroupMember{{ModelID: "coding", Weight: 0}}
		}, "weight must be > 0"},
		{"G4_no_dup_members", func(m *models.ModelConfig) {
			m.Members = []models.GroupMember{{ModelID: "coding", Weight: 1}, {ModelID: "coding", Weight: 1}}
		}, "duplicate member"},
		{"G6_no_fallback_chain", func(m *models.ModelConfig) { m.FallbackChain = []string{"coding-alt"} }, "does not support fallback_chain"},
		{"G6_no_secondary", func(m *models.ModelConfig) { m.SecondaryUpstreamModel = "x" }, "does not support secondary_upstream_model"},
		{"G6_no_peak_hour", func(m *models.ModelConfig) { m.PeakHourEnabled = true }, "does not support peak_hour_enabled"},
		{"G6_no_chunk_deadline", func(m *models.ModelConfig) { m.ReleaseStreamChunkDeadline = models.Duration(60 * 1e9) }, "does not support release_stream_chunk_deadline"},
		{"G7_ultimate_exclusion_required", func(m *models.ModelConfig) { m.ExcludeFromUltimateSwitching = false }, "requires exclude_from_ultimate_switching"},
		{"G8_no_own_id_claim", func(m *models.ModelConfig) { m.ShadowModelID = "coding-group" }, "cannot be the group's own ID"},
		{"G8_shadow_target_must_exist", func(m *models.ModelConfig) { m.ShadowModelID = "ghost" }, "does not exist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr, cleanup := newGroupManager(t)
			defer cleanup()
			group := validGroup()
			tc.mutate(&group)
			err := mgr.AddModel(group)
			if err == nil {
				t.Fatalf("AddModel accepted a G-violation (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

// TestGroup_UpdateModel_GViolation_And_Success — UpdateModel funnels
// through the same validateBeforeWrite chokepoint.
func TestGroup_UpdateModel_GViolation_And_Success(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	broken := validGroup()
	broken.Internal = false // G1
	if err := mgr.UpdateModel("coding-group", broken); err == nil {
		t.Fatal("UpdateModel accepted G1 violation (internal=false)")
	} else if !strings.Contains(err.Error(), "requires internal to be true") {
		t.Errorf("UpdateModel G1 error = %q", err.Error())
	}

	// Valid update with a G8 self-shadow (member claim) passes.
	ok := validGroup()
	if err := mgr.UpdateModel("coding-group", ok); err != nil {
		t.Fatalf("valid UpdateModel: %v", err)
	}
}

// TestGroup_ClaimAware_GetModel — §3.4 resolution order:
// claims hit + owning group enabled → the GROUP row; strict twins
// and GetModelNative → the REAL row.
func TestGroup_ClaimAware_GetModel(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	// Claimed ID ("coding", self-shadow) → the group row via
	// claims-first GetModel.
	got := mgr.GetModel("coding")
	if got == nil {
		t.Fatal("GetModel(coding) = nil")
	}
	if got.ID != "coding-group" || !got.IsGroup() {
		t.Fatalf("GetModel(coding) returned %q (kind %q) — claim-first should return the group row", got.ID, got.Kind)
	}

	// GetModelNative → the REAL chat row (Am22 — member refs
	// resolve by row identity; claims are client-facing only).
	native := mgr.GetModelNative("coding")
	if native == nil || native.ID != "coding" || native.IsGroup() {
		t.Fatalf("GetModelNative(coding) = %+v — must bypass claims and return the real row", native)
	}

	// Strict twins → the REAL row (admin edit/delete paths).
	strict, err := mgr.GetModelStrict(context.Background(), "coding")
	if err != nil || strict == nil {
		t.Fatalf("GetModelStrict(coding): %v", err)
	}
	if strict.ID != "coding" || strict.IsGroup() {
		t.Fatalf("GetModelStrict(coding) returned %q — strict twins must bypass claims", strict.ID)
	}

	// Unclaimed IDs resolve normally.
	if m := mgr.GetModel("coding-alt"); m == nil || m.ID != "coding-alt" {
		t.Fatalf("GetModel(coding-alt) = %+v — unclaimed ID should resolve normally", m)
	}
	// The group's own ID resolves to itself.
	if m := mgr.GetModel("coding-group"); m == nil || !m.IsGroup() {
		t.Fatalf("GetModel(coding-group) = %+v — group ID should resolve to the group row", m)
	}
}

// TestGroup_DisabledClaim_Inactive — disabling the group drops the
// claim: the shadowed model resolves normally again (§3.8).
func TestGroup_DisabledClaim_Inactive(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	disabled := validGroup()
	disabled.Enabled = false
	if err := mgr.UpdateModel("coding-group", disabled); err != nil {
		t.Fatalf("disable group: %v", err)
	}

	if m := mgr.GetModel("coding"); m == nil || m.IsGroup() {
		t.Fatalf("disabled group's claim must be inactive — GetModel(coding) = %+v", m)
	}

	// Re-enable → claim active again.
	enabled := validGroup()
	if err := mgr.UpdateModel("coding-group", enabled); err != nil {
		t.Fatalf("re-enable group: %v", err)
	}
	if m := mgr.GetModel("coding"); m == nil || !m.IsGroup() {
		t.Fatalf("re-enabled group's claim must be active — GetModel(coding) = %+v", m)
	}
}

// TestGroup_ClaimDroppedOnDelete — deleting the group drops the
// claim (the shadowed model reappears).
func TestGroup_ClaimDroppedOnDelete(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := mgr.RemoveModel("coding-group"); err != nil {
		t.Fatalf("RemoveModel(group): %v", err)
	}
	if m := mgr.GetModel("coding"); m == nil || m.IsGroup() {
		t.Fatalf("post-delete claim must be gone — GetModel(coding) = %+v", m)
	}
}

// TestGroup_DeleteModel_MemberScan — Am27: deleting a model that is
// any group's member is BLOCKED with an error naming the group;
// after removing the member from the group the delete succeeds.
func TestGroup_DeleteModel_MemberScan(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// "coding-alt" is a member → blocked, error names the group.
	err := mgr.RemoveModel("coding-alt")
	if err == nil {
		t.Fatal("RemoveModel accepted deletion of a group member (Am27 scan missing)")
	}
	if !strings.Contains(err.Error(), "coding-group") {
		t.Errorf("Am27 error should name the group, got: %q", err.Error())
	}

	// "coding" is both member AND shadow target → also blocked.
	if err := mgr.RemoveModel("coding"); err == nil {
		t.Fatal("RemoveModel accepted deletion of a member/shadow-target")
	}

	// Remove the member from the group (single-member group is a
	// valid staging state) → delete now succeeds.
	updated := validGroup()
	updated.Members = []models.GroupMember{{ModelID: "coding", Weight: 1}}
	updated.ShadowModelID = "coding"
	if err := mgr.UpdateModel("coding-group", updated); err != nil {
		t.Fatalf("update group to drop member: %v", err)
	}
	if err := mgr.RemoveModel("coding-alt"); err != nil {
		t.Fatalf("after member removal the delete should succeed, got: %v", err)
	}
}

// TestGroup_ResolverUntouched — regression pin (Round-2 Am21): no
// kind-branch exists in the resolver; a group ID resolves ok=false
// (Internal=true but zero credentials ⇒ the credentials gate fails
// exactly as for any credentials-less internal row). Group rows
// never reach ResolveInternalConfig(WithAffinity).
func TestGroup_ResolverUntouched(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, _, _, _, ok := mgr.ResolveInternalConfig("coding-group"); ok {
		t.Error("ResolveInternalConfig(group) should be ok=false (resolver untouched by kind)")
	}
	if _, ok := mgr.ResolveInternalConfigWithAffinity("coding-group", "conv-key"); ok {
		t.Error("ResolveInternalConfigWithAffinity(group) should be ok=false (resolver untouched by kind)")
	}
}
