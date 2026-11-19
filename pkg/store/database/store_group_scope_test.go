package database

// The AUTH COMMISSION's scope-enforcement pins at the GROUP-PICK
// authority (pkg/store/database/store_group_rewrite.go).
//
// THE SEAM UNDER TEST: the four-phase ordering sweep
// (pickServableGroupMember) and its scoped variant. The sweep is the
// ONE place a group's member is chosen, so it is the ONE place where
// an out-of-reach member must never be picked — and where a scope
// exclusion must never leave cross-request state behind.

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// testReach is a models.ReachFilter over a fixed id set — the token's
// reach set, handed to the scoped rewrite.
type testReach map[string]bool

func (r testReach) Allows(id string) bool { return r[id] }

// scopeTestManager seeds a manager with two chat members and a group
// that contains both.
func scopeTestManager(t *testing.T) (*ModelsManager, func()) {
	t.Helper()
	mgr, cleanup := newGroupManager(t)
	group := models.ModelConfig{
		ID:       "scope-group",
		Name:     "Scope Group",
		Enabled:  true,
		Kind:     models.KindGroup,
		Internal: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 1},
			{ModelID: "coding-alt", Weight: 1},
		},
	}
	if err := mgr.AddModel(group); err != nil {
		cleanup()
		t.Fatalf("seed group: %v", err)
	}
	return mgr, cleanup
}

// TestScopeExclusion_NeverWritesCooldown_MultiToken is the NAMED
// cooldown-skip pin (approver blocker 2).
//
// (a) A scope-refused offer writes a BINDING (the sweep advances to the
//
//	next candidate) but NO cooldown row, NO skip-cache mark and NO
//	engine cooldown entry. A permission denial is not a health fact:
//	it must never create cross-request state (ADR D6 / P6).
//
// (b) CROSS-TOKEN: after Token A's scope exclusion of member M1, a
//
//	WILDCARD token still selects M1 with ZERO cooldown interference —
//	Token A's denial cannot degrade Token B's routing.
//
// The refusal path under test is the REAL one: the ordering sweep
// (RewriteGroupEntriesScoped → pickServableGroupMember), NOT the
// ExcludeGroupMemberScoped seam (that one is only called for a member
// that genuinely FAILED upstream).
func TestScopeExclusion_NeverWritesCooldown_MultiToken(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	const groupID = "scope-group"
	const memberM1 = "coding"
	const memberM2 = "coding-alt"

	// Seed the engine's binding for the conversation to M1, so the
	// sweep's FIRST offer is M1 — the refusal is deterministic.
	mgr.groupEngine.RebindPast(groupID, "conv-a", []string{memberM2})

	// TOKEN A: reaches M2 only. M1 is out of reach.
	reachA := testReach{memberM2: true}
	res := mgr.RewriteGroupEntriesScoped([]string{groupID}, "conv-a", reachA)
	if res.GroupID != groupID {
		t.Fatalf("Token A rewrite: GroupID = %q, want %q", res.GroupID, groupID)
	}
	if res.MemberID != memberM2 {
		t.Fatalf("Token A rewrite: MemberID = %q, want %q (the out-of-reach member %q must never be picked)", res.MemberID, memberM2, memberM1)
	}
	if res.ScopeExcluded {
		t.Error("Token A rewrite: ScopeExcluded = true, want false (M2 WAS reachable — the group served)")
	}

	// (a) NO COOLDOWN for the refused member.
	if until, cooling := mgr.groupEngine.MemberCooldownUntil(groupID, memberM1); cooling {
		t.Errorf("Token A scope exclusion wrote a COOLDOWN on %q (until %s) — a permission denial must never create cross-request state", memberM1, until)
	}
	if until, cooling := mgr.groupEngine.MemberCooldownUntil(groupID, memberM2); cooling {
		t.Errorf("the picked member %q is cooling (until %s) — the sweep must not cool what it serves", memberM2, until)
	}

	// (a) THE BINDING ADVANCED: the refused member is skipped, so the
	// conversation's binding must now point at the next candidate — the
	// engine's getOrSelect returns the LIVE binding on every call, so
	// without the advance the sweep would re-offer M1 forever.
	bound, _, err := mgr.groupEngine.GetOrSelect(groupID, "conv-a")
	if err != nil {
		t.Fatalf("GetOrSelect after the scope exclusion: %v", err)
	}
	if bound != memberM2 {
		t.Errorf("conversation binding = %q, want %q — the scope-refused offer must ADVANCE the binding (RebindPast), not re-offer the refused member", bound, memberM2)
	}

	// (b) CROSS-TOKEN: a WILDCARD token (no reach filter) still selects
	// M1 — Token A's denial left NO cooldown behind.
	mgr.groupEngine.RebindPast(groupID, "conv-b", []string{memberM2})
	resB := mgr.RewriteGroupEntriesScoped([]string{groupID}, "conv-b", nil)
	if resB.MemberID != memberM1 {
		t.Fatalf("wildcard Token B rewrite: MemberID = %q, want %q — Token A's scope exclusion must not interfere with another token's routing", resB.MemberID, memberM1)
	}
	if until, cooling := mgr.groupEngine.MemberCooldownUntil(groupID, memberM1); cooling {
		t.Errorf("member %q is cooling (until %s) after Token A's scope exclusion — a wildcard token must be unaffected", memberM1, until)
	}
}

// TestGroupScopeExclusion_AllMembersOutOfScope_ScopeExcluded pins the
// "empty by scope" signal: when EVERY base-servable member is out of
// reach, the rewrite reports ScopeExcluded (the caller answers 403
// access_denied — NEVER the 502 group_unservable envelope).
func TestGroupScopeExclusion_AllMembersOutOfScope_ScopeExcluded(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	const groupID = "scope-group"

	// Token reaches NOTHING of this group.
	res := mgr.RewriteGroupEntriesScoped([]string{groupID}, "conv-none", testReach{})
	if res.FailedGroupID != groupID {
		t.Fatalf("FailedGroupID = %q, want %q", res.FailedGroupID, groupID)
	}
	if !res.ScopeExcluded {
		t.Error("ScopeExcluded = false, want true — every base-servable member was out of reach (this is a PERMISSION failure, not an availability failure)")
	}
	if res.MemberID != "" {
		t.Errorf("MemberID = %q, want \"\" — no member may be picked when all of them are out of reach", res.MemberID)
	}

	// No cooldown may be written for either member.
	for _, id := range []string{"coding", "coding-alt"} {
		if until, cooling := mgr.groupEngine.MemberCooldownUntil(groupID, id); cooling {
			t.Errorf("member %q is cooling (until %s) after an all-out-of-scope sweep — no cooldown may be written", id, until)
		}
	}
}

// TestGroupScopeExclusion_AllMembersDisabled_NotScopeExcluded pins the
// DISCRIMINATOR: an all-DISABLED group is an AVAILABILITY failure
// (ScopeExcluded stays false — the caller answers 502), never a
// permission failure. The two arms must stay distinguishable.
func TestGroupScopeExclusion_AllMembersDisabled_NotScopeExcluded(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	const groupID = "scope-group"

	// Disable both members.
	for _, id := range []string{"coding", "coding-alt"} {
		row := mgr.GetModelNative(id)
		if row == nil {
			t.Fatalf("member %q not found", id)
		}
		cp := *row
		cp.Enabled = false
		if err := mgr.UpdateModel(id, cp); err != nil {
			t.Fatalf("disable %q: %v", id, err)
		}
	}

	res := mgr.RewriteGroupEntriesScoped([]string{groupID}, "conv-disabled", testReach{})
	if res.FailedGroupID != groupID {
		t.Fatalf("FailedGroupID = %q, want %q", res.FailedGroupID, groupID)
	}
	if res.ScopeExcluded {
		t.Error("ScopeExcluded = true, want false — a group whose members are ALL DISABLED is an AVAILABILITY failure (502), not a permission failure")
	}
}

// TestGroupExcludeScoped_ScopeOnlySignal pins the PD-11 outcome struct
// at the seam: the offer walk must say WHY it produced nothing.
//
//   - a member out of reach ⇒ OK == false AND ScopeOnly == true (the
//     scope exhaustion);
//   - an availability-shaped group (no servable candidate at all) ⇒
//     OK == false AND ScopeOnly == false.
//
// The two facts must be distinguishable AT THE SEAM, never inferred by
// the callers.
func TestGroupExcludeScoped_ScopeOnlySignal(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	const groupID = "scope-group"

	// (1) The scope arm: M1 fails, M2 is out of reach ⇒ the walk
	// produces nothing BECAUSE OF SCOPE.
	out := mgr.ExcludeGroupMemberScoped(groupID, "conv-scope", "coding", 30_000_000_000, testReach{})
	if out.OK {
		t.Fatalf("OK = true, want false — every alternative is out of reach (Replacement=%q)", out.Replacement)
	}
	if !out.ScopeOnly {
		t.Error("ScopeOnly = false, want true — the walk found a base-servable candidate and it was out of reach")
	}

	// (2) The availability arm: a group with no servable candidate at
	// all (its only member is disabled) ⇒ not a scope exhaustion.
	for _, id := range []string{"coding", "coding-alt"} {
		row := mgr.GetModelNative(id)
		cp := *row
		cp.Enabled = false
		if err := mgr.UpdateModel(id, cp); err != nil {
			t.Fatalf("disable %q: %v", id, err)
		}
	}
	out = mgr.ExcludeGroupMemberScoped(groupID, "conv-avail", "coding", 30_000_000_000, testReach{})
	if out.OK {
		t.Fatalf("OK = true, want false — no servable candidate exists")
	}
	if out.ScopeOnly {
		t.Error("ScopeOnly = true, want false — an availability-shaped group is NOT a scope exhaustion")
	}
}

// TestGroupScopeExclusion_NeverPicked_AnyPhase pins the reach
// invariant across the sweep: no phase of the ordering sweep may
// return an out-of-reach member — not the engine offer (Phase 1/2),
// not the completeness sweep (Phase 3) and not the demoted head
// (Phase 4).
func TestGroupScopeExclusion_NeverPicked_AnyPhase(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	const groupID = "scope-group"

	// Reach: nothing of this group.
	res := mgr.RewriteGroupEntriesScoped([]string{groupID}, "conv-any", testReach{})
	if res.MemberID != "" {
		t.Fatalf("MemberID = %q, want \"\" — no phase of the sweep may return an out-of-reach member", res.MemberID)
	}
	for _, id := range res.ModelList {
		t.Errorf("the rewritten list carries %q — every entry must be inside the reach set", id)
	}
}

// TestFallbackPositionGroup_Unreachable_Dropped pins the T2.6
// index-0-vs-fallback rule: a group that appears at a FALLBACK position
// and cannot serve this token is DROPPED from the execution list — it
// must never fail the request. A fallback the token cannot use is not
// the request's failure; failing it would let a permission setting turn
// a healthy primary into a 502.
func TestFallbackPositionGroup_Unreachable_Dropped(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	// A standalone primary (NOT one of the group's members) so the
	// group's members are genuinely out of reach.
	if err := mgr.AddModel(models.ModelConfig{
		ID: "primary", Name: "Primary", Enabled: true, Kind: models.KindChat, Internal: true,
		InternalModel: "stub-model", Credentials: models.TestRefs("cred-openai"),
	}); err != nil {
		t.Fatalf("seed primary: %v", err)
	}

	// The list is [primary chat model, group] and the token reaches only
	// the primary.
	res := mgr.RewriteGroupEntriesScoped([]string{"primary", "scope-group"}, "conv-fb", testReach{"primary": true})
	if res.FailedGroupID != "" {
		t.Errorf("FailedGroupID = %q, want empty — a fallback-position group with no reachable member must be DROPPED, not fail the request", res.FailedGroupID)
	}
	if res.ScopeExcluded {
		t.Error("ScopeExcluded = true, want false — the requested (index-0) primary was servable")
	}
	if len(res.ModelList) == 0 || res.ModelList[0] != "primary" {
		t.Fatalf("ModelList = %v, want the primary %q at index 0", res.ModelList, "primary")
	}
	for _, id := range res.ModelList {
		if id == "scope-group" {
			t.Errorf("ModelList = %v carries the unreachable fallback group %q — it must be dropped", res.ModelList, "scope-group")
		}
	}
	if res.GroupID != "" {
		t.Errorf("GroupID = %q, want empty — a dropped fallback group must not mint a group pick", res.GroupID)
	}
}

// TestIndexZeroGroup_Unreachable_FailsWithScopeExcluded is the T2.6
// counterpart: the REQUESTED group (index 0) with no reachable member
// fails the request, and the failure is a SCOPE exclusion (403), never
// an availability failure (502).
func TestIndexZeroGroup_Unreachable_FailsWithScopeExcluded(t *testing.T) {
	mgr, cleanup := scopeTestManager(t)
	defer cleanup()

	res := mgr.RewriteGroupEntriesScoped([]string{"scope-group"}, "conv-index0", testReach{})
	if res.FailedGroupID != "scope-group" {
		t.Fatalf("FailedGroupID = %q, want the requested group", res.FailedGroupID)
	}
	if !res.ScopeExcluded {
		t.Error("ScopeExcluded = false, want true — the requested group had base-servable members and every one was out of reach")
	}
}
