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
		// remove-external-upstream P1 (task 7): G1_internal_required is
		// RETIRED from this table — the layered Internal=true write-stamp
		// makes every group write internal, so the "requires internal to
		// be true" violation is unproducible through AddModel/UpdateModel
		// (it remains a Validate()-time rule for direct callers).
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

	// remove-external-upstream P1 (task 7): the G1 (internal=false)
	// UpdateModel rejection block is RETIRED — the write-stamp forces
	// internal=true before validation, so the violation is unproducible
	// through the write path.

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

// ─── P2 carry-forward (b)3 + P2.2/2.4/2.7 manager-level tests ─────────────

// TestGroup_GetModelByName_ClaimBlind — Council note #3 [P2 if
// TRIVIAL, else P4 — do it]: the name resolver must NOT redirect
// through the claim index. Claims are ID-space only; a name lookup
// for the claimed chat row's display name still returns the REAL
// chat row, never the group. (The rewrite helper at the P2
// pipeline applies at the modelList level using the resolved
// model; a claims-aware GetModelByName would fabricate a different
// virtualization shape for clients that pass names instead of IDs —
// a silent behavior change for OpenAI/TypeSafe clients.)
func TestGroup_GetModelByName_ClaimBlind(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()

	// Add the chat row + the group claiming it.
	chat := models.ModelConfig{
		ID: "chat-x", Name: "Chat X", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "x", Credentials: models.TestRefs("cred-openai"),
	}
	if err := mgr.AddModel(chat); err != nil {
		t.Fatalf("AddModel(chat): %v", err)
	}
	group := validGroup()
	group.ShadowModelID = "chat-x"
	if err := mgr.AddModel(group); err != nil {
		t.Fatalf("AddModel(group): %v", err)
	}

	// The chat row's NAME must resolve to the REAL chat row
	// (claim-blind), not to the group.
	got := mgr.GetModelByName("Chat X")
	if got == nil {
		t.Fatal("GetModelByName returned nil")
	}
	if got.ID != "chat-x" || got.IsGroup() {
		t.Errorf("GetModelByName must stay claim-blind; got ID=%q kind=%q (want chat-x/chat)", got.ID, got.Kind)
	}
	// And the group's own name resolves to the group (claims don't
	// apply to the claiming row itself).
	if got := mgr.GetModelByName("Coding Group"); got == nil || got.ID != "coding-group" || !got.IsGroup() {
		t.Errorf("group name lookup: got %+v, want the group row", got)
	}
}

// TestGroup_EngineLifecycle — grouplb engine constructed in
// NewModelsManager + stops in Close (P2.2 lifecycle).
func TestGroup_EngineLifecycle(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	ge := mgr.GroupEngine()
	if ge == nil {
		t.Fatal("GroupEngine() returned nil on a live manager")
	}
	// No group state seeded yet — Am15 lazy posture.
	if ge.HasGroup("coding-group") {
		t.Error("fresh engine must hold no group state")
	}
	cleanup() // Close(); idempotent
	// After Close, Stop is idempotent + post-Stop reads are lazy-only.
	if _, _, err := ge.GetOrSelect("coding-group", "k"); err == nil {
		t.Error("post-Close GetOrSelect on a removed group must error")
	}
}

// TestGroup_OnGroupChanged_SurvivorFilter — group writes (Add +
// Update) drive OnGroupChanged: selector rebuild + orphan-binding
// drop. The "boundAt preserved on surviving bindings" half is
// covered in pkg/grouplb/binding_test.go (same engine, internal
// access); here the observable side effect: after a member is
// removed, a fresh key does not pick that member, and a pick on the
// surviving member's key still re-picks that member.
func TestGroup_OnGroupChanged_SurvivorFilter(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	ge := mgr.GroupEngine()

	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// First pick — locks a binding to a member.
	pre := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-stays")
	if pre.FailedGroupID != "" {
		t.Fatalf("pre: %+v", pre)
	}
	preMember := pre.MemberID

	// Drop the OTHER member.
	otherMember := "coding-alt"
	if preMember == "coding-alt" {
		otherMember = "coding"
	}
	updated := validGroup()
	updated.Members = []models.GroupMember{{ModelID: preMember, Weight: 1}}
	if err := mgr.UpdateModel("coding-group", updated); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	// Sticky on the SAME key: must re-pick the SAME surviving member
	// (the binding for the removed other-member is the one dropped;
	// the preMember binding survives).
	post := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-stays")
	if post.MemberID != preMember {
		t.Errorf("survivor: pre=%q post=%q (must stick to surviving member)", preMember, post.MemberID)
	}

	// A NEW key under the now-removed member: must NOT pick that
	// member. (The selector rebuild drops the orphan; a fresh key
	// gets a fresh healthy pick from the survivors.)
	if removedRes := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-newkey"); removedRes.MemberID == otherMember {
		t.Errorf("removed member %q must not be picked on a fresh key", otherMember)
	}

	// Remove the group: state dropped (live-edit safety).
	if err := mgr.RemoveModel("coding-group"); err != nil {
		t.Fatalf("RemoveModel(group): %v", err)
	}
	if ge.HasGroup("coding-group") {
		t.Error("RemoveModel must drop the group state wholesale")
	}
}

// TestGroup_LazySeed_FirstRewriteSeedsEngine — Am15: no boot
// RebindFromStore walk. The first rewrite after boot seeds the
// group's engine state.
func TestGroup_LazySeed_FirstRewriteSeedsEngine(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	ge := mgr.GroupEngine()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Simulate restart: no engine state for the group (Am15).
	ge.OnGroupChanged("coding-group", nil) // drop the AddModel's seed

	// First rewrite: lazy seed + sticky pick.
	res := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-lazy")
	if res.FailedGroupID != "" || !res.AnyGroup {
		t.Fatalf("rewrite: %+v", res)
	}
	if !ge.HasGroup("coding-group") {
		t.Error("first rewrite must lazily seed the engine state")
	}
	if res.MemberID != "coding" && res.MemberID != "coding-alt" {
		t.Errorf("rewrite picked %q, want one of the two members", res.MemberID)
	}
}

// TestGroup_RewriteGroupEntries_StickySecondCall — same conv key
// returns the SAME member both times (binding persists). Different
// keys distribute independently.
func TestGroup_RewriteGroupEntries_StickySecondCall(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res1 := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-sticky")
	if res1.FailedGroupID != "" {
		t.Fatalf("first rewrite: %+v", res1)
	}
	res2 := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-sticky")
	if res2.MemberID != res1.MemberID {
		t.Fatalf("sticky: res1=%q res2=%q (same key must re-pick)", res1.MemberID, res2.MemberID)
	}
	if !res1.MemberNewlyBound {
		t.Error("first pick must be newlyBound=true")
	}
	if res2.MemberNewlyBound {
		t.Error("second pick on the same key must be newlyBound=false (binding reused)")
	}

	// A DIFFERENT key may pick a different member (statistical
	// over 2+ keys, not pinned; accept either).
	for _, k := range []string{"k-x", "k-y", "k-z"} {
		_ = mgr.RewriteGroupEntries([]string{"coding-group"}, k)
	}
}

// TestGroup_RewriteGroupEntries_ChatFallbackToGroup — Q16: a chat
// row's fallback chain pointing at a group is rewritten natively.
// The execution list becomes [chat, groupMember + groupMemberChain].
func TestGroup_RewriteGroupEntries_ChatFallbackToGroup(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	// Add a chat main with a fallback pointing at the group.
	chatMain := models.ModelConfig{
		ID: "chat-main", Name: "Main", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "m",
		Credentials:   models.TestRefs("cred-openai"),
		FallbackChain: []string{"coding-group"},
	}
	if err := mgr.AddModel(chatMain); err != nil {
		t.Fatalf("AddModel(chat-main): %v", err)
	}
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("AddModel(group): %v", err)
	}
	res := mgr.RewriteGroupEntries([]string{"chat-main", "coding-group"}, "conv-mix")
	if res.FailedGroupID != "" {
		t.Fatalf("rewrite: %+v", res)
	}
	if len(res.ModelList) == 0 || res.ModelList[0] != "chat-main" {
		t.Errorf("chat main must be the first entry; got %+v", res.ModelList)
	}
	// The second slot (was the group ID) must be a REAL member ID.
	got := res.ModelList[1]
	if got != "coding" && got != "coding-alt" {
		t.Errorf("fallback-reached group entry must be rewritten to a member; got %q (list=%v)", got, res.ModelList)
	}
	// No further member-chain entries (members have no fallback chain).
	if len(res.ModelList) > 2 {
		t.Errorf("members have no chain; list should be len 2; got %v", res.ModelList)
	}
}

// TestGroup_RewriteGroupEntries_Am28StaleMember — a member
// disabled between group write and request must be skip-and-
// reselected; an all-stale group yields FailedGroupID.
func TestGroup_RewriteGroupEntries_Am28StaleMember(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Disable member "coding" between write and request.
	disabled := models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: false, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai"),
	}
	if err := mgr.UpdateModel("coding", disabled); err != nil {
		t.Fatalf("disable coding: %v", err)
	}

	// The rewrite MUST reselect away from the now-stale "coding".
	res := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-stale")
	if res.FailedGroupID != "" {
		t.Fatalf("rewrite: %+v", res)
	}
	if res.MemberID == "coding" {
		t.Errorf("Am28 violated: stale member 'coding' was picked (res=%+v)", res)
	}
	if res.MemberID != "coding-alt" {
		t.Errorf("expected coding-alt, got %q", res.MemberID)
	}

	// Disable the remaining member too — empty-at-runtime group.
	disabled2 := models.ModelConfig{
		ID: "coding-alt", Name: "Coding Alt", Enabled: false, Kind: models.KindChat,
		Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu"),
	}
	if err := mgr.UpdateModel("coding-alt", disabled2); err != nil {
		t.Fatalf("disable coding-alt: %v", err)
	}
	res2 := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-empty")
	if res2.FailedGroupID != "coding-group" {
		t.Errorf("empty group must surface FailedGroupID=coding-group; got %+v", res2)
	}
}

// TestGroup_ExcludeGroupMember — Am24 contract: terminal exclusion
// rebinds to the next healthy member; single-member group returns
// unavailable (ReselectNone ⇒ ok=false).
func TestGroup_ExcludeGroupMember(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pick, pool, ok := mgr.ExcludeGroupMember("coding-group", "conv-term", "coding")
	if !ok {
		t.Fatal("two-member group must produce a replacement")
	}
	if pick != "coding-alt" {
		t.Errorf("reselect pick = %q, want coding-alt (the only other member)", pick)
	}
	if pool != models.PoolDefault {
		t.Errorf("reselect pool = %q, want default", pool)
	}

	// Now make the group single-member: ExcludeGroupMember on a
	// single-member group returns ("" , "", false) — no alternative.
	updated := validGroup()
	updated.Members = []models.GroupMember{{ModelID: "coding", Weight: 1}}
	if err := mgr.UpdateModel("coding-group", updated); err != nil {
		t.Fatalf("update to single-member: %v", err)
	}
	_, _, ok2 := mgr.ExcludeGroupMember("coding-group", "conv-term", "coding")
	if ok2 {
		t.Error("single-member ExcludeGroupMember must return ok=false (no alternative)")
	}
}

// TestGroup_LiveEditSafety_ThreeClauses — the plan's named live-edit
// safety set (§9.4): (1) in-flight hold — an already-picked member
// stays bound (sticky hold survives membership edits); (2) new picks
// see the new membership (removed members are never picked again);
// (3) sticky-to-removed drops and re-picks (a binding pointing at a
// removed member is dropped by the survivor filter and the next
// rewrite picks from the survivors).
func TestGroup_LiveEditSafety_ThreeClauses(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// (1) In-flight hold: pick under key K, then edit weights — the
	// SAME key must keep returning the SAME member (the in-flight
	// conversation's affinity holds).
	first := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-hold")
	if first.FailedGroupID != "" {
		t.Fatalf("first: %+v", first)
	}
	weightNudged := validGroup()
	weightNudged.Members = []models.GroupMember{
		{ModelID: first.MemberID, Weight: 9},
		{ModelID: otherOf(first.MemberID), Weight: 1},
	}
	if err := mgr.UpdateModel("coding-group", weightNudged); err != nil {
		t.Fatalf("weight nudge: %v", err)
	}
	hold := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-hold")
	if hold.MemberID != first.MemberID {
		t.Errorf("(1) in-flight hold violated: first=%q hold=%q", first.MemberID, hold.MemberID)
	}

	// (3) Sticky-to-removed drops and re-picks: bind key K2 to a
	// member, then REMOVE that member — the next rewrite on K2 must
	// pick a survivor (never the removed one).
	second := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-k2")
	removed := second.MemberID
	survivor := otherOf(removed)
	slimmed := validGroup()
	slimmed.Members = []models.GroupMember{{ModelID: survivor, Weight: 1}}
	slimmed.ShadowModelID = "" // coding may be the removed one — drop the claim edge
	if err := mgr.UpdateModel("coding-group", slimmed); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	re := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-k2")
	if re.FailedGroupID != "" {
		t.Fatalf("(3) rewrite after removal failed: %+v", re)
	}
	if re.MemberID == removed {
		t.Errorf("(3) sticky-to-removed must drop and re-pick: got the removed %q", re.MemberID)
	}
	if re.MemberID != survivor {
		t.Errorf("(3) expected the survivor %q, got %q", survivor, re.MemberID)
	}

	// (2) New picks see the new membership: a fresh key can never
	// pick the removed member (selector rebuilt over survivors).
	for _, k := range []string{"n1", "n2", "n3", "n4", "n5"} {
		res := mgr.RewriteGroupEntries([]string{"coding-group"}, k)
		if res.FailedGroupID != "" || res.MemberID == removed {
			t.Errorf("(2) fresh key %s picked removed member: %+v", k, res)
		}
	}
}

// otherOf returns the other member of the canonical two-member
// fixture.
func otherOf(memberID string) string {
	if memberID == "coding" {
		return "coding-alt"
	}
	return "coding"
}

// ─── group-backup-members / D8 — manager-level stale-binding behaviour ──────
//
// The engine-level D8 tests (pkg/grouplb/engine_test.go) cover the
// picker. These cover the SEAM: the manager's two-pool picker
// (pickServableGroupMember) and the rewrite that surfaces it
// (RewriteGroupEntries) — including the empty-default defensive path
// (unreachable via the API — D2 blocks zero-default writes — but the
// picker must still serve a backup instead of failing).

// TestGroup_D8_RemovedMemberRePicks — a group's member is removed
// while a conversation is bound to it; the next rewrite for the SAME
// conversation key must serve a live member (never the removed id).
func TestGroup_D8_RemovedMemberRePicks(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const key = "conv-d8-removed"

	// First rewrite: binds the conversation to one of the two members.
	res := mgr.RewriteGroupEntries([]string{"coding-group"}, key)
	if res.FailedGroupID != "" || res.MemberID == "" {
		t.Fatalf("first rewrite failed: %+v", res)
	}
	bound := res.MemberID
	if bound != "coding" && bound != "coding-alt" {
		t.Fatalf("first rewrite member = %q, want coding|coding-alt", bound)
	}

	// Remove the bound member from the group (config change on the
	// fly) — the survivor filter (push layer) drops the binding.
	// The surviving member is the OTHER one.
	remaining := "coding"
	if bound == "coding" {
		remaining = "coding-alt"
	}
	updated := validGroup()
	updated.Members = []models.GroupMember{{ModelID: remaining, Weight: 1}}
	if err := mgr.UpdateModel("coding-group", updated); err != nil {
		t.Fatalf("update group: %v", err)
	}

	// The same conversation key now re-picks — onto the surviving
	// member, never the removed one.
	res2 := mgr.RewriteGroupEntries([]string{"coding-group"}, key)
	if res2.FailedGroupID != "" {
		t.Fatalf("post-removal rewrite failed: %+v", res2)
	}
	if res2.MemberID == bound {
		t.Fatalf("post-removal rewrite served the REMOVED member %q", res2.MemberID)
	}
	if res2.MemberID != remaining {
		t.Fatalf("post-removal rewrite member = %q, want %q (the only remaining member)", res2.MemberID, remaining)
	}
	if res2.MemberPool != models.PoolDefault {
		t.Errorf("post-removal rewrite pool = %q, want default", res2.MemberPool)
	}
}

// TestGroup_D8_EmptyDefaultPool_FallsToBackup — the DEFENSIVE
// empty-default path: a group whose members are ALL backup-flagged
// (unreachable via the write path — D2 rejects zero-default groups —
// so the group row is constructed directly and handed to the picker)
// must still SERVE a backup member (no error, correct pool) instead
// of failing the request.
func TestGroup_D8_EmptyDefaultPool_FallsToBackup(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	if err := mgr.AddModel(validGroup()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Hand-built all-backup group — bypasses the D2 write validation
	// on purpose (the state is unreachable via the API).
	allBackup := models.ModelConfig{
		ID:                           "coding-group",
		Name:                         "Coding Group",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 1, BackupOnly: true},
			{ModelID: "coding-alt", Weight: 1, BackupOnly: true},
		},
	}
	// The engine's membership for this group must reflect the
	// all-backup shape — exactly what the write path's
	// notifyGroupEngineLocked → OnGroupChanged does on a config
	// change. (The seeded group above registered the DEFAULT shape
	// under the same group id.)
	mgr.groupEngine.OnGroupChanged(allBackup.ID, GroupMemberRefs(allBackup.Members))

	memberID, newlyBound, pool, stale, ok := mgr.pickServableGroupMember(&allBackup, "conv-d8-empty")
	if !ok {
		t.Fatal("empty default pool: the picker must serve a BACKUP member, not fail")
	}
	if memberID != "coding" && memberID != "coding-alt" {
		t.Fatalf("empty default pool: member = %q, want coding|coding-alt", memberID)
	}
	if pool != models.PoolBackup {
		t.Fatalf("empty default pool: pool = %q, want backup", pool)
	}
	if stale {
		t.Error("empty default pool: staleRepaired must be false (no prior binding)")
	}
	_ = newlyBound
}
