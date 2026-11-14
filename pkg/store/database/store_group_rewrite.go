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
	"sort"
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
// re-validates it (Am28 shape gates) — now under ORDERING-NOT-
// FILTERING (wi-7769a812 part 3 — P0 fix, USER-AUTHORITATIVE):
//
//	The cache is an optimization, not the truth: it may reorder
//	candidates, never exclude them.
//
// Four phases, one request:
//
//	Phase 1 — DEFAULT pool via engine offers (sticky binding /
//	          weighted pick / all-cooling honest-attempt ruling, all
//	          UNCHANGED engine semantics). Each offer is classified:
//	          • base-stale (deleted / disabled / wrong kind) → genuine
//	            Am28 exclusion — engine skip-and-reselect KEPT
//	            (cooldown write is honest bookkeeping for a row that
//	            cannot serve at all);
//	          • skip-marked or engine-cooling → DEMOTED into the
//	            bucket — NO cooldown write (a demotion must not
//	            poison the next re-entry's engine view — that write
//	            fed the incident's marked-member bounce);
//	          • otherwise → SERVED (head of the order).
//	Phase 2 — BACKUP pool: identical classification via the backup
//	          picker.
//	Phase 3 — COMPLETENESS sweep: every ref not yet classified is
//	          enumerated directly and demoted with its earliest live
//	          expiry (skip mark ∨ engine cooldown; a healthy-but-
//	          unoffered member anchors at "now"). This is what makes
//	          the demoted list END-TO-END: nothing servable can be
//	          left undiscovered behind the engine's offer loop.
//	Phase 4 — the demoted sweep EXECUTES: sorted by (expiry asc, ref
//	          order asc) the head is SERVED — flows into the race
//	          list like any other pick (the superseded D3 "degrade"
//	          picked-then-dropped; this head is a full candidate).
//
// A request therefore fails ONLY after every base-servable member
// across BOTH pools has had its honest attempt ordered (the actual
// attempts happen in the race/re-entry cascade the rewrite feeds).
// supersede note: the C1 default-skip pre-check and the D3
// degradeToSoonestExpiry special case are GONE — both were filters;
// the ordering above preserves their intent (healthy backups are
// served before benched defaults because benched defaults sit in the
// bucket behind every unbenched candidate).
func (m *ModelsManager) pickServableGroupMember(group *models.ModelConfig, conversationKey string) (memberID string, newlyBound bool, pool string, staleRepaired bool, ok bool) {
	if m.groupEngine == nil {
		return "", false, "", false, false
	}
	refs := GroupMemberRefs(group.Members)
	if !m.groupEngine.HasGroup(group.ID) {
		// Am15 lazy seed — first rewrite after boot.
		m.groupEngine.OnGroupChanged(group.ID, refs)
	}

	idxOf := make(map[string]int, len(refs))
	for i, r := range refs {
		idxOf[r.ModelID] = i
	}
	var bucket []demotedMember
	seen := make(map[string]bool, len(refs))

	// Phase 1: default pool via engine offers.
	defaults := defaultRefs(refs)
	for attempt := 0; attempt <= len(defaults); attempt++ {
		pick, newly, repaired, err := m.groupEngine.GetOrSelectWithRepair(group.ID, conversationKey)
		if err != nil {
			// D8: empty/invalid default pool — fall through to the
			// backup sweep (NOT a request failure).
			break
		}
		if seen[pick] {
			// Engine re-offered a classified member (its distinct-offer
			// space is spent — the all-cooling ruling is deterministic).
			// Stop asking rather than spinning to the loop bound.
			break
		}
		seen[pick] = true
		row := m.GetModelNative(pick)
		if !isServableGroupMemberBase(row) {
			m.groupEngine.ExcludeAndReselect(group.ID, conversationKey, pick, 0)
			continue
		}
		expiry, benched := m.memberDemotionExpiry(group.ID, row)
		if !benched {
			return pick, newly, models.PoolDefault, repaired, true
		}
		bucket = append(bucket, demotedMember{memberID: pick, expiry: expiry, idx: idxOf[pick], pool: models.PoolDefault})
		m.groupEngine.RebindPast(group.ID, conversationKey, seenKeys(seen))
	}

	// Phase 2: backup pool — same shape.
	backups := backupRefs(refs)
	for attempt := 0; attempt <= len(backups); attempt++ {
		pick, newly, repaired, err := m.groupEngine.PickBackupMemberWithRepair(group.ID, conversationKey)
		if err != nil {
			break // no backups / invalid refs → the bucket decides
		}
		if seen[pick] {
			break
		}
		seen[pick] = true
		row := m.GetModelNative(pick)
		if !isServableGroupMemberBase(row) {
			m.groupEngine.ExcludeAndBackupReselect(group.ID, conversationKey, pick, 0)
			continue
		}
		expiry, benched := m.memberDemotionExpiry(group.ID, row)
		if !benched {
			if m.groupEngine.HasBackupPool(group.ID) {
				log.Printf("[grouplb] group=%s pool=backup member=%s — all default members exhausted for this run", group.ID, pick)
			}
			return pick, newly, models.PoolBackup, repaired, true
		}
		bucket = append(bucket, demotedMember{memberID: pick, expiry: expiry, idx: idxOf[pick], pool: models.PoolBackup})
		m.groupEngine.RebindBackupPast(group.ID, conversationKey, seenKeys(seen))
	}

	// Phase 3: completeness sweep — classify every ref the engine's
	// offer loop never surfaced (the all-cooling ruling re-offers only
	// its soonest member, so untouched members MUST be enumerated
	// directly or they would stay invisible — the incident's trap).
	for _, r := range refs {
		if seen[r.ModelID] {
			continue
		}
		row := m.GetModelNative(r.ModelID)
		if !isServableGroupMemberBase(row) {
			continue
		}
		expiry, benched := m.memberDemotionExpiry(group.ID, row)
		if !benched {
			// Healthy but never offered (defensive — a fresh binding
			// the rebind kept pointing away from). Most-recovered
			// anchor: "now" sorts it to the bucket head.
			expiry = time.Now()
		}
		poolTag := models.PoolDefault
		if r.BackupOnly {
			poolTag = models.PoolBackup
		}
		bucket = append(bucket, demotedMember{memberID: r.ModelID, expiry: expiry, idx: r.Position, pool: poolTag})
	}

	// Phase 4: the demoted sweep EXECUTES — head = soonest-expiring
	// mark (ties: configured member order). This pick is a FULL
	// candidate: it flows into the race list end-to-end.
	if len(bucket) > 0 {
		sort.SliceStable(bucket, func(i, j int) bool {
			if !bucket[i].expiry.Equal(bucket[j].expiry) {
				return bucket[i].expiry.Before(bucket[j].expiry)
			}
			return bucket[i].idx < bucket[j].idx
		})
		head := bucket[0]
		log.Printf("[grouplb] group=%s pool=%s member=%s — no unbenched candidate; serving soonest-expiring demoted head (ordering-not-filtering; expiry in %s)",
			group.ID, head.pool, head.memberID, time.Until(head.expiry).Round(time.Second))
		return head.memberID, false, head.pool, false, true
	}
	return "", false, "", false, false
}

// demotedMember is one base-servable member the ordering sweep
// benched (skip-marked and/or engine-cooling): a real candidate,
// demoted — never excluded (ordering-not-filtering doctrine). idx is
// the member's configured position — the deterministic tie-break.
type demotedMember struct {
	memberID string
	expiry   time.Time
	idx      int
	pool     string
}

// seenKeys extracts the classified-member ids for the RebindPast
// exclusion sets.
func seenKeys(seen map[string]bool) []string {
	keys := make([]string, 0, len(seen))
	for id := range seen {
		keys = append(keys, id)
	}
	return keys
}

// memberDemotionExpiry returns the member's earliest live benching
// expiry — the soonest skip mark across its upstream identities and
// its grouplb engine cooldown, whichever recovers first. ok=false
// when the member is neither skip-marked nor cooling (an unbenched
// candidate).
func (m *ModelsManager) memberDemotionExpiry(groupID string, row *models.ModelConfig) (time.Time, bool) {
	if row == nil {
		return time.Time{}, false
	}
	best, ok := m.memberSoonestSkipExpiry(row)
	if cdUntil, cooling := m.groupEngine.MemberCooldownUntil(groupID, row.ID); cooling && (!ok || cdUntil.Before(best)) {
		best, ok = cdUntil, true
	}
	return best, ok
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

// isServableGroupMember is the CANDIDATE predicate (wi-7769a812
// part 3 — P0 fix): the row passes the BASE Am28 shape gates (exists,
// enabled, chat-kind) — full stop.
//
//	ORDERING-NOT-FILTERING DOCTRINE: the cache is an optimization,
//	not the truth: it may reorder candidates, never exclude them.
//
// The part-2 cache-aware arms (every-credential-marked ⇒ member
// invisible) are RETIRED — that predicate converted a health MARK
// into member NON-EXISTENCE and is the root cause of the incident
// (two servable members hidden behind credential-scoped marks).
// Health state now reaches selection only through
// memberDemotionExpiry (the ordering key): a marked member is DEMOTED
// to the tail of the candidate order, never removed from it. Nil row:
// false (nothing to serve).
func (m *ModelsManager) isServableGroupMember(row *models.ModelConfig) bool {
	return isServableGroupMemberBase(row)
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
// across the member's upstream identities — one input to
// memberDemotionExpiry's ordering key: the member's soonest live mark
// sorts it to the demoted bucket's tail head (ORDERING-NOT-FILTERING;
// the part-2 "D3 degradation anchor: ONE honest attempt" framing is
// retired — there is no special degrade mode, only the ordering
// sweep). ok=false when the member has no live marks (or the cache is
// unwired).
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
// failure. ok=true means a member is available for the next re-entry
// attempt; ("", "", false) means no alternative exists — the failure
// is real. (wi-7769a812 part 2: the Am24 max-ONE re-entry latch is
// retired — the PROXY bounds the member iteration to N attempts for
// an N-member group.)
//
// wi-7769a812 part 3 — ORDERING-NOT-FILTERING rework (P0 fix). The
// old mode-switch cascade (ReselectHealthy / SoonestExpiry+no-backup
// D7 re-serve / C1 backup-cascade) offered candidates from a DIFFERENT
// authority than the rebuild's rewrite: its backup cascade starved the
// default pool (the C1 "all default members exhausted" seam — a
// benched default could NEVER be offered back while any backup could
// be picked), and its SoonestExpiry offers stored no binding, so the
// rebuild re-served the OLD bound member — the incident's race-#2
// mechanics. Now the offer IS the rewrite: exclude the failed member
// (engine cooldown write — genuine-failure bookkeeping, cooldown
// CALLER-OWNED per part 2 D3: the proxy passes the config-owned
// skip-cache TTL), then let pickServableGroupMember — the SAME
// ordering authority the rebuild uses — pick the head. Offer and
// rebuild can no longer diverge: both are the four-phase ordering
// sweep (unbenched members first, demoted soonest-expiring first,
// every base-servable member of BOTH pools a real candidate). The
// proxy's tried-set (F3) refuses re-offers of members that already
// failed THIS request by excluding them through this same call, which
// cools them and strictly advances the ordering head.
func (m *ModelsManager) ExcludeGroupMember(groupID, conversationKey, memberID string, cooldown time.Duration) (string, string, bool) {
	if m.groupEngine == nil {
		return "", "", false
	}
	// (1) Honest exclusion of the failed member (genuine-failure
	// bookkeeping — this write is DESIRED; the write the doctrine bans
	// is cooling merely-MARKED picks inside the ordering sweep).
	m.groupEngine.ExcludeAndReselect(groupID, conversationKey, memberID, cooldown)
	// (2) The offer is the ordering sweep's head — same authority as
	// the handler's rebuild.
	group := m.GetModelNative(groupID)
	if group == nil || !group.IsGroup() {
		return "", "", false
	}
	pick, _, pool, _, ok := m.pickServableGroupMember(group, conversationKey)
	if !ok || pick == "" {
		return "", "", false
	}
	if pick == memberID {
		// The ordering head IS the member that just failed terminally
		// (single-servable-member groups, or every alternative base-
		// stale) — no alternative exists; the failure is real (Am24).
		return "", "", false
	}
	return pick, pool, true
}

// ClearGroupMemberCooldown is the manager-side WIN-CLEAR seam: a
// member that just WON a race is by definition servable, so its
// grouplb exclusion row must not outlive the win. Consumed by the
// proxy via an OPTIONAL interface assertion (the GroupRewriter
// interface itself is untouched — the ~11 structural fakes stay
// valid; backends without the capability simply never clear).
func (m *ModelsManager) ClearGroupMemberCooldown(groupID, memberID string) {
	if m == nil || m.groupEngine == nil {
		return
	}
	m.groupEngine.ClearCooldown(groupID, memberID)
}
