package database

// load-balance-groups §3.6.1 (Am21) — THE modelList rewrite, the
// single mechanism both protocol sites call (parity-locked by
// pkg/proxy tests). The group is ONE thin layer before request
// distribution: every group entry in the execution list is replaced
// by its sticky-picked member; the member's own pipeline (race,
// retry, credential LB, its own fallback chain) then runs UNCHANGED.
//
// Am22 hard rule: member refs resolve by DIRECT ID (GetModelNative)
// — claims apply to client-facing resolution only. A claims-first
// lookup here would infinite-loop the day-1 self-shadow group
// `coding[coding]` (a group claiming its own single member's ID).
//
// Am28 stale-member defense: every pick is re-validated (exists /
// chat-kind / not group / enabled); a stale pick is
// skip-and-reselected within the group (the stale member is cooled
// for the default 60s and the next healthy member picked); an
// empty-at-runtime group (all members stale) yields FailedGroupID —
// the caller surfaces the normal resolve-failure envelope, never a
// panic.

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// RewriteGroupEntries implements models.GroupRewriter (Am21). See
// that interface's doc comment for the contract.
//
// Lazy seeding (Am15): a group with no engine state yet (first
// request after boot) is seeded from its CURRENT row members before
// the pick; subsequent membership changes ride the manager's write
// path (notifyGroupEngineLocked), so HasGroup is the accurate
// freshness gate.
//
// Multi-group lists (a chat main with a group fallback, Q16): every
// group entry is rewritten with the SAME request conversationKey (the
// direct-named case is group-salted by construction — the key is
// computed on the group row; a fallback-reached group shares the
// main model's key space, documented). GroupID/MemberID report the
// FIRST group entry's pick (the direct/main group).
func (m *ModelsManager) RewriteGroupEntries(modelList []string, conversationKey string) models.GroupRewriteResult {
	var res models.GroupRewriteResult
	out := make([]string, 0, len(modelList)+2)
	for _, entry := range modelList {
		// Am22: direct-ID lookup, claims BYPASSED. Entry may be a
		// group ID, a chat ID, or an unknown/raw name (passthrough
		// entries — GetModelNative returns nil, entry unchanged).
		row := m.GetModelNative(entry)
		if row == nil || !row.IsGroup() {
			out = append(out, entry)
			continue
		}
		res.AnyGroup = true
		memberID, newlyBound, ok := m.pickServableGroupMember(row, conversationKey)
		if !ok {
			res.FailedGroupID = row.ID
			continue // unservable group entry is dropped; caller fails clean
		}
		memberRow := m.GetModelNative(memberID)
		if memberRow == nil { // defensive: pickServableGroupMember re-validated
			res.FailedGroupID = row.ID
			continue
		}
		// Execution list = [member.ID] + member.FallbackChain — the
		// member's own chain follows it natively.
		out = append(out, memberRow.ID)
		out = append(out, memberRow.FallbackChain...)
		if res.GroupID == "" {
			res.GroupID = row.ID
			res.MemberID = memberRow.ID
			res.MemberNewlyBound = newlyBound
		}
	}
	res.ModelList = out
	return res
}

// pickServableGroupMember picks the sticky member for the group and
// re-validates it (Am28). Stale picks (deleted / disabled / wrong
// kind between group write and request) are skip-and-reselected: the
// stale member is excluded (default cooldown) and the next healthy
// member picked. Bounded by the member count; exhaustion ⇒ ok=false
// (empty-at-runtime group).
func (m *ModelsManager) pickServableGroupMember(group *models.ModelConfig, conversationKey string) (memberID string, newlyBound bool, ok bool) {
	if m.groupEngine == nil {
		return "", false, false
	}
	refs := GroupMemberRefs(group.Members)
	if !m.groupEngine.HasGroup(group.ID) {
		// Am15 lazy seed — first rewrite after boot.
		m.groupEngine.OnGroupChanged(group.ID, refs)
	}
	// +1 defensive iteration over the member count bounds the loop
	// even when every pick comes back stale (single-member groups
	// hit the n==1 fast path, which ignores cooldowns — the loop
	// then terminates on the attempt bound).
	for attempt := 0; attempt <= len(refs); attempt++ {
		pick, newly, err := m.groupEngine.GetOrSelect(group.ID, conversationKey)
		if err != nil {
			return "", false, false
		}
		if isServableGroupMember(m.GetModelNative(pick)) {
			return pick, newly, true
		}
		// Am28: stale — skip-and-reselect (cools the stale member;
		// rebinds the conversation to the next healthy member).
		m.groupEngine.ExcludeAndReselect(group.ID, conversationKey, pick, 0)
	}
	return "", false, false
}

// isServableGroupMember is the Am28 re-validation predicate: the
// member row exists, is enabled, and is chat-kind (kind chat or the
// legacy "" normalization; not group / image-gen / decision — the
// same text-kind rule as G3/G5 at write time, re-checked at request
// time to cover member disabled/deleted/retyped between group write
// and request).
func isServableGroupMember(row *models.ModelConfig) bool {
	if row == nil || !row.Enabled {
		return false
	}
	switch row.Kind {
	case "", models.KindChat:
		return true
	default:
		return false
	}
}

// ExcludeGroupMember implements models.GroupRewriter (Am24): the
// GROUP-level exclude-and-reselect fired on terminal member-pipeline
// failure. ok=true (Healthy or SoonestExpiry mode) means a member is
// available for the single re-entry attempt; ("", false) means no
// alternative exists — the failure is real.
func (m *ModelsManager) ExcludeGroupMember(groupID, conversationKey, memberID string) (string, bool) {
	if m.groupEngine == nil {
		return "", false
	}
	pick, mode := m.groupEngine.ExcludeAndReselect(groupID, conversationKey, memberID, 0)
	if pick == "" || mode == grouplb.ReselectNone {
		return "", false
	}
	return pick, true
}
