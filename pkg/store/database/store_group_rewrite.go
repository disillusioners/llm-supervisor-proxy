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
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
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

	// C1: when a backup pool exists AND every default is cooling, the
	// default pool is exhausted for this run — the backup pool takes
	// over. Without this the rebuild would re-serve a cooling
	// default (the v1 all-cooling availability ruling) and undo the
	// re-entry cascade in ExcludeGroupMember. Backup-less groups
	// keep the v1 ruling byte-identically (D7).
	defaultPoolExhausted := m.groupEngine.HasBackupPool(group.ID) && !m.groupEngine.DefaultPoolHealthy(group.ID)

	// Pool 1: defaults. +1 defensive iteration over the default
	// member count bounds the loop even when every pick comes back
	// stale. All-defaults exhausted ⇒ fall through to the backup
	// loop. wi-7769a812 part 2: the re-validation predicate is
	// CACHE-AWARE — a member whose upstreams are all skip-marked
	// (limit-class failures, D3) is skipped exactly like a stale one,
	// so selection becomes health-aware while unmarked alternatives
	// exist (the A2 fix).
	defaults := defaultRefs(refs)
	for attempt := 0; !defaultPoolExhausted && attempt <= len(defaults); attempt++ {
		pick, newly, repaired, err := m.groupEngine.GetOrSelectWithRepair(group.ID, conversationKey)
		if err != nil {
			// D8: the default pool has no member at all (empty
			// default pool / refs invalidated under us) ⇒ the
			// default pool is EXHAUSTED. Fall through to the
			// backup pool instead of failing the request.
			break
		}
		if m.isServableGroupMember(m.GetModelNative(pick)) {
			return pick, newly, models.PoolDefault, repaired, true
		}
		// Am28: stale — skip-and-reselect (cools the stale member;
		// rebinds the conversation to the next healthy member).
		// wi-7769a812 part 2: a cache-marked (unhealthy) member lands
		// here too; the exclusion window stays the engine default —
		// the SKIP CACHE is the health authority, this write is
		// exclusion-set bookkeeping only.
		m.groupEngine.ExcludeAndReselect(group.ID, conversationKey, pick, 0)
	}

	// Pool 2: backups (group-backup-members / D1). Tries each
	// backup member in turn; exhausts the BACKUP pool only after
	// every backup member is stale/excluded/skip-marked. Returns
	// ok=false when even the backup pool is exhausted — OR falls
	// through to the D3 degradation below.
	backups := backupRefs(refs)
	for attempt := 0; attempt <= len(backups); attempt++ {
		pick, newly, repaired, err := m.groupEngine.PickBackupMemberWithRepair(group.ID, conversationKey)
		if err != nil {
			return m.degradeToSoonestExpiry(group, refs)
		}
		if m.isServableGroupMember(m.GetModelNative(pick)) {
			if m.groupEngine.HasBackupPool(group.ID) {
				log.Printf("[grouplb] group=%s pool=backup member=%s — all default members exhausted for this run", group.ID, pick)
			}
			return pick, newly, models.PoolBackup, repaired, true
		}
		m.groupEngine.ExcludeAndBackupReselect(group.ID, conversationKey, pick, 0)
	}
	return m.degradeToSoonestExpiry(group, refs)
}

// degradeToSoonestExpiry (wi-7769a812 part 2 / USER RULING D3 — the
// anti-availability-gate guard). When EVERY member's upstreams are
// skip-marked (a provider-wide wall / all-credentials-walled group),
// the cache-aware servability above would exhaust both pools and —
// pre-part-2 — surface an empty-at-runtime group (Am28 FailedGroupID)
// for a group whose members are merely HEALTH-MARKED, not gone. The
// cache is an OPTIMIZER, NEVER an availability gate: this degradation
// picks the member whose soonest skip mark expires FIRST (the most
// likely-recovered upstream) and serves ONE honest attempt against
// it. Members that fail the BASE Am28 checks (deleted / disabled /
// wrong kind) are never degradation candidates — only health marks
// are bypassed here.
func (m *ModelsManager) degradeToSoonestExpiry(group *models.ModelConfig, refs []grouplb.MemberRef) (string, bool, string, bool, bool) {
	var bestID string
	var bestExpiry time.Time
	var bestBackup bool
	found := false
	for _, r := range refs {
		row := m.GetModelNative(r.ModelID)
		if !isServableGroupMemberBase(row) {
			continue
		}
		expiry, ok := m.memberSoonestSkipExpiry(row)
		if !ok {
			// An unmarked member reaching this point means the engine
			// refused it for a non-health reason (excluded this run);
			// still a valid one-honest-attempt candidate — treat its
			// expiry as "now" so a genuinely-marked member wins the
			// anchor only when nothing unmarked remains.
			expiry = time.Now()
			ok = true
		}
		if !found || expiry.Before(bestExpiry) {
			bestID, bestExpiry, bestBackup, found = r.ModelID, expiry, r.BackupOnly, true
		}
	}
	if !found {
		return "", false, "", false, false
	}
	if bestBackup {
		log.Printf("[grouplb] group=%s pool=backup member=%s — all upstreams skip-marked (D3 degradation: ONE honest attempt against the soonest-expiring mark)", group.ID, bestID)
		return bestID, false, models.PoolBackup, false, true
	}
	log.Printf("[grouplb] group=%s member=%s — all upstreams skip-marked (D3 degradation: ONE honest attempt against the soonest-expiring mark)", group.ID, bestID)
	return bestID, false, models.PoolDefault, false, true
}

// isServableGroupMemberBase is the Am28 re-validation predicate: the
// member row exists, is enabled, and is chat-kind (kind chat or the
// legacy "" normalization; not group / image-gen / decision — the
// same text-kind rule as G3/G5 at write time, re-checked at request
// time to cover member disabled/deleted/retyped between group write
// and request). HEALTH-AGNOSTIC by design: this is the shape check
// the D3 degradation also honors (health marks never gate it).
func isServableGroupMemberBase(row *models.ModelConfig) bool {
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

// isServableGroupMember (wi-7769a812 part 2 / USER RULING D3) is the
// CACHE-AWARE re-validation predicate: the base Am28 checks AND at
// least one UNMARKED upstream. The member's upstream identities are
// its credential refs — a member is skippable when EVERY credential
// is marked for the member's wire model, either directly
// (credential, model) or provider-wide ((provider, model) — every
// cred of the provider marked skips every member on that provider).
// Credential-less rows (legacy) have no markable upstream identity
// and stay base-servable. A nil cache (feature unwired) degrades to
// the base checks — byte-identical pre-part-2 behavior.
func (m *ModelsManager) isServableGroupMember(row *models.ModelConfig) bool {
	if !isServableGroupMemberBase(row) {
		return false
	}
	if m.skipCache == nil || len(row.Credentials) == 0 {
		return true
	}
	model := memberWireModel(row)
	for _, ref := range row.Credentials {
		if m.skipCache.Marked(skipcache.ScopeCredential, ref.CredentialID, model) {
			continue
		}
		cred := m.GetCredential(ref.CredentialID)
		if cred == nil || cred.Provider == "" {
			return true // no provider identity to wall — servable
		}
		if !m.skipCache.Marked(skipcache.ScopeProvider, strings.ToLower(cred.Provider), model) {
			return true // one healthy upstream — member servable
		}
	}
	return false // every upstream walled
}

// memberWireModel is the skip-cache model key for a member row: the
// upstream model name the executor would send (InternalModel), or
// the row ID when no explicit upstream name is configured.
func memberWireModel(row *models.ModelConfig) string {
	if row.InternalModel != "" {
		return row.InternalModel
	}
	return row.ID
}

// memberSoonestSkipExpiry returns the earliest live skip-cache expiry
// across the member's upstream identities (the D3 degradation anchor:
// ONE honest attempt against the soonest-expiring mark). ok=false
// when the member has no live marks (or the cache is unwired).
func (m *ModelsManager) memberSoonestSkipExpiry(row *models.ModelConfig) (time.Time, bool) {
	if m.skipCache == nil || row == nil || len(row.Credentials) == 0 {
		return time.Time{}, false
	}
	model := memberWireModel(row)
	pairs := make([]skipcache.Pair, 0, len(row.Credentials)*2)
	for _, ref := range row.Credentials {
		pairs = append(pairs, skipcache.Pair{Scope: skipcache.ScopeCredential, ID: ref.CredentialID})
		if cred := m.GetCredential(ref.CredentialID); cred != nil && cred.Provider != "" {
			pairs = append(pairs, skipcache.Pair{Scope: skipcache.ScopeProvider, ID: strings.ToLower(cred.Provider)})
		}
	}
	return m.skipCache.SoonestExpiry(pairs, model)
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
// failure. ok=true means a member is available for the next
// re-entry attempt; ("", "", false) means no alternative exists —
// the failure is real. (wi-7769a812 part 2: the Am24 max-ONE
// re-entry latch is retired — the PROXY bounds the member iteration
// to N attempts for an N-member group; the engine's contract here is
// unchanged: one exclude + one reselect per call.)
//
// group-backup-members / C1 + D5 — the cascade decision is
// mode-aware:
//
//   - ReselectHealthy: a live default is available → serve it
//     (PoolDefault).
//   - ReselectSoonestExpiry (EVERY default is now cooling) WITH a
//     backup pool: cascade to the backup pool (PoolBackup). Serving
//     the soonest-expiring cooling default here would re-run the
//     member that just failed and keep the backups dead on the
//     terminal-failure seam — the C1 defect. Backup-less groups keep
//     the v1 soonest-default re-serve byte-identically (D7).
//   - ReselectSoonestExpiry WITHOUT a backup pool: serve the
//     soonest-expiring default (v1 behavior, D7).
//   - ReselectNone (default pool genuinely exhausted, or a
//     single-member group): cascade to the backup pool; a
//     single-member backup-less group therefore fails fast exactly
//     as v1 did.
//
// The returned `pool` is "backup" only when the backup pool actually
// served a member, so the proxy's D5 `pool=backup` annotation + the
// D4 metering predicate flow unchanged.
//
// wi-7769a812 part 2 / USER RULING D3: the cooldown parameter is
// CALLER-OWNED — the proxy passes the config-owned skip-cache TTL so
// the member exclusion window derives from the same config key as
// the upstream skip marks (one source of truth). <=0 keeps the
// engine default (the Am28/stale and legacy callers).
func (m *ModelsManager) ExcludeGroupMember(groupID, conversationKey, memberID string, cooldown time.Duration) (string, string, bool) {
	if m.groupEngine == nil {
		return "", "", false
	}
	pick, mode := m.groupEngine.ExcludeAndReselect(groupID, conversationKey, memberID, cooldown)

	switch {
	case mode == grouplb.ReselectHealthy && pick != "":
		// A live default is available.
		return pick, models.PoolDefault, true

	case mode == grouplb.ReselectSoonestExpiry && pick != "" && !m.groupEngine.HasBackupPool(groupID):
		// v1 all-cooling availability ruling — backup-less groups
		// only (D7 byte-compat).
		return pick, models.PoolDefault, true

	default:
		// C1: every default is cooling (SoonestExpiry) with a backup
		// pool, OR the default pool is genuinely exhausted
		// (ReselectNone). The backup pool takes over; if it cannot
		// serve, the failure is real.
		if bPick, ok := m.pickServableBackup(groupID, conversationKey); ok {
			return bPick, models.PoolBackup, true
		}
		return "", "", false
	}
}

// pickServableBackup resolves a servable BACKUP member for a re-entry
// attempt (group-backup-members / C1): pick, validate at the Am28
// predicate, and on a stale pick reselect once within the backup pool
// (mirrors the rewrite loop's skip-and-reselect). Returns ("", false)
// when no servable backup exists.
func (m *ModelsManager) pickServableBackup(groupID, conversationKey string) (string, bool) {
	bPick, _, err := m.groupEngine.PickBackupMember(groupID, conversationKey)
	if err != nil || bPick == "" {
		return "", false
	}
	if m.isServableGroupMember(m.GetModelNative(bPick)) {
		return bPick, true
	}
	// Stale backup — reselect once within the backup pool.
	replacement, mode := m.groupEngine.ExcludeAndBackupReselect(groupID, conversationKey, bPick, 0)
	if replacement == "" || mode == grouplb.ReselectNone {
		return "", false
	}
	return replacement, true
}
