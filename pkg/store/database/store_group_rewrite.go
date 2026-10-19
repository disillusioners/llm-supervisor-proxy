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
	"log"

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
		memberID, newlyBound, pool, staleRepaired, ok := m.pickServableGroupMember(row, conversationKey)
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
			res.MemberPool = pool
			res.BindingStale = staleRepaired
		}
	}
	res.ModelList = out
	return res
}

// pickServableGroupMember picks the sticky member for the group and
// re-validates it (Am28). Stale picks (deleted / disabled / wrong
// kind between group write and request) are skip-and-reselected: the
// stale member is excluded (default cooldown) and the next healthy
// member picked. Bounded by the member count; exhaustion of the
// default pool falls through to the BACKUP pool
// (group-backup-members / D1); exhaustion of the backup pool
// returns ok=false (empty-at-runtime group).
//
// Pool selection mirrors the engine partition: the default pool is
// tried first; the backup pool is tried only when the default pool
// has no servable member left. Returns the pool the served member
// was drawn from ("default" | "backup") for D5 observability.
//
// group-backup-members / D8: the engine's selection-time repair
// (union-scoped existence check) is what detects a binding whose
// member left the group; the repair signal is carried up to the
// proxy (the `binding=stale` observability token). The default
// pool's ErrNoMembers is treated as "the default pool is exhausted"
// — NOT as a request failure — so the backup pool is consulted;
// this is the defensive empty-default path (the all-defaults-
// removed state is unreachable via the API — D2 blocks zero-default
// writes — but the picker must not fail on it).
func (m *ModelsManager) pickServableGroupMember(group *models.ModelConfig, conversationKey string) (memberID string, newlyBound bool, pool string, staleRepaired bool, ok bool) {
	if m.groupEngine == nil {
		return "", false, "", false, false
	}
	refs := GroupMemberRefs(group.Members)
	if !m.groupEngine.HasGroup(group.ID) {
		// Am15 lazy seed — first rewrite after boot.
		m.groupEngine.OnGroupChanged(group.ID, refs)
	}

	// Pool 1: defaults. +1 defensive iteration over the default
	// member count bounds the loop even when every pick comes back
	// stale. All-defaults exhausted ⇒ fall through to the backup
	// loop.
	defaults := defaultRefs(refs)
	for attempt := 0; attempt <= len(defaults); attempt++ {
		pick, newly, repaired, err := m.groupEngine.GetOrSelectWithRepair(group.ID, conversationKey)
		if err != nil {
			// D8: the default pool has no member at all (empty
			// default pool / refs invalidated under us) ⇒ the
			// default pool is EXHAUSTED. Fall through to the
			// backup pool instead of failing the request.
			break
		}
		if isServableGroupMember(m.GetModelNative(pick)) {
			return pick, newly, models.PoolDefault, repaired, true
		}
		// Am28: stale — skip-and-reselect (cools the stale member;
		// rebinds the conversation to the next healthy member).
		m.groupEngine.ExcludeAndReselect(group.ID, conversationKey, pick, 0)
	}

	// Pool 2: backups (group-backup-members / D1). Tries each
	// backup member in turn; exhausts the BACKUP pool only after
	// every backup member is stale/excluded. Returns ok=false
	// when even the backup pool is exhausted.
	backups := backupRefs(refs)
	for attempt := 0; attempt <= len(backups); attempt++ {
		pick, newly, repaired, err := m.groupEngine.PickBackupMemberWithRepair(group.ID, conversationKey)
		if err != nil {
			return "", false, "", false, false
		}
		if isServableGroupMember(m.GetModelNative(pick)) {
			if m.groupEngine.HasBackupPool(group.ID) {
				log.Printf("[grouplb] group=%s pool=backup member=%s — all default members exhausted for this run", group.ID, pick)
			}
			return pick, newly, models.PoolBackup, repaired, true
		}
		m.groupEngine.ExcludeAndBackupReselect(group.ID, conversationKey, pick, 0)
	}
	return "", false, "", false, false
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

// defaultRefs / backupRefs split engine refs into their two pools
// (group-backup-members / D1). Kept as plain helpers; the engine's
// own partition is the authoritative read source — these exist only
// so the manager can size its loop bound per pool.
func defaultRefs(refs []grouplb.MemberRef) []grouplb.MemberRef {
	out := make([]grouplb.MemberRef, 0, len(refs))
	for _, r := range refs {
		if !r.BackupOnly {
			out = append(out, r)
		}
	}
	return out
}

func backupRefs(refs []grouplb.MemberRef) []grouplb.MemberRef {
	out := make([]grouplb.MemberRef, 0, len(refs))
	for _, r := range refs {
		if r.BackupOnly {
			out = append(out, r)
		}
	}
	return out
}

// ExcludeGroupMember implements models.GroupRewriter (Am24): the
// GROUP-level exclude-and-reselect fired on terminal member-pipeline
// failure. ok=true (Healthy or SoonestExpiry mode) means a member is
// available for the single re-entry attempt; ("", "", false) means no
// alternative exists — the failure is real.
//
// group-backup-members / D5: when the default pool's
// ExcludeAndReselect returns ReselectNone (no healthy default left),
// the manager auto-transitions to the backup pool via
// PickBackupMember; the returned `pool` is "backup" so the proxy
// can surface the pool=backup observability line + event payload
// field. The fall-through is invisible to the caller unless the
// backup pool actually served a member.
func (m *ModelsManager) ExcludeGroupMember(groupID, conversationKey, memberID string) (string, string, bool) {
	if m.groupEngine == nil {
		return "", "", false
	}
	pick, mode := m.groupEngine.ExcludeAndReselect(groupID, conversationKey, memberID, 0)
	if pick != "" && mode != grouplb.ReselectNone {
		return pick, models.PoolDefault, true
	}
	// Default pool exhausted; try the backup pool (D1 fall-through).
	bPick, _, bErr := m.groupEngine.PickBackupMember(groupID, conversationKey)
	if bErr != nil || bPick == "" {
		return "", "", false
	}
	// Validate that the backup pick is servable at the SAME Am28
	// predicate; a stale backup is surfaced here only as
	// unreadable-this-run.
	if isServableGroupMember(m.GetModelNative(bPick)) {
		return bPick, models.PoolBackup, true
	}
	// Fall back to reselect within the backup pool.
	bPick2, bMode := m.groupEngine.ExcludeAndBackupReselect(groupID, conversationKey, bPick, 0)
	if bPick2 == "" || bMode == grouplb.ReselectNone {
		return "", "", false
	}
	return bPick2, models.PoolBackup, true
}
