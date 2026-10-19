# Backup-Only Members in Load-Balance Groups — design note

Commission: add a per-member `backup_only` flag; backup members are
served ONLY when every default member in the run's exclusion set has
failed. Two sub-pools per group: `default` and `backup`.

Base: `9972212cf5217a5d25191d8e2f040d028a3c353d` (latest).

## Decisions (D1–D7) — implementation choices + seam reality

### D1 fallback / stickiness

Per-request execution: default pool is evaluated first with the
existing weighted-random + conversation-key sticky logic; backup pool
is consulted only when the default pool is exhausted for that run
(all default members in the run's exclusion set). Backup pool gets
its OWN sticky bindings, so recurring fallbacks from one
conversation bind to one backup member — but a conversation that
fell back does NOT stay on backup once a default can serve again
(no cross-request backup preference). The engine simply never
auto-binds a conversation to a backup member on a fresh request.

Seam reality (file:line): the exclusion-and-reselect surfaces
the run's exclusion set to the picker at exactly one place —
`pkg/store/database/store_group_rewrite.go:99-111`
(`pickServableGroupMember`'s bounded skip-and-reselect loop) AND
`pkg/proxy/handler.go:1217` (handler tryGroupReentry). The
handler-side call is the only ONE re-entry per request; the
manager-side loop is the initial-pick loop. Both call sites are
extended to fall to the backup pool when defaults are exhausted.

### D2 ≥1 default member = write-time VALIDATION ERROR

Cheap fence. The `pkg/models.ValidateGroupMembers` rule set is
extended with: at least one member must NOT have `BackupOnly=true`.
Added at the top of the validator (after the existing G2 ≥1 + cap
checks and before the per-member G3/G4/G5 walk so a zero-default
submission is rejected with a clear, dedicated message rather than
a downstream "all backup" cascade).

Client-side mirror at
`pkg/ui/frontend/src/components/config/GroupModelForm.tsx`'s
`handleSubmit` (added to the existing error-collection block,
mirroring the existing G2/G3/G8 mirror idiom). Server-side validation
remains authoritative.

### D3 masking / shadow / claims: UNCHANGED

The group id remains the masked/model identity; pool choice is
internal to the picker. Claims (`pkg/store/store.go:988`) and
shadow-model semantics are untouched. `self-shadow` day-1 onboarding
shape (`coding[coding]`) preserves the same behavior: members
without flags pick defaults; adding a backup member adds an
inert second-tier member that doesn't participate until defaults
fail.

### D4 metering: UNCHANGED

The proxy-side metering predicate
`pkg/proxy/handler.go:1707` + `pkg/proxy/handler.go:1950`
(`winnerModel != reqLog.Model && rc.groupPick != nil`) covers
both pools identically — the dual-row `IncrementModelUsage`
emission depends only on `groupPick != nil` and the winner's
model differing from the client-requested string. A backup-served
request still carries `groupPick.MemberID` of the chosen backup
member, so the dual-row emission fires the same way. Day-1
self-shadow (`coding[coding]`) and non-group fallback ACs stay
green: `rc.groupPick != nil` is set whenever the rewrite selected
ANY group member, default or backup.

### D5 observability

`MemberPool` field added on
`pkg/models/group_rewrite.go:GroupRewriteResult` and propagated
to `pkg/proxy/handler_groups.go:groupPickInfo.Pool`. The
`ModelsManager.pickServableGroupMember` rewrites its return value
to include the pool string (`"default" | "backup"`). The
proxy-tier surfaces it in TWO observation channels:

1. A bounded, redacted log line at
   `pkg/store/database/store_group_rewrite.go`:
   `[grouplb] request %s: group=%s pool=backup member=%s`
   (visible ONLY on actual backup fallback — default picks emit
   the existing log line untouched).

2. The `model_member_selected` event payload at
   `pkg/proxy/race_coordinator.go:994-1007` carries an ADDITIVE
   `pool` field with the same `"default"|"backup"` value, so the
   FE EventLog / audit row reflects the fallback (default = the
   FE's existing default branch — absent when not needed).

Idiom: the race-reason class (`pkg/proxy/race_reason.go`) is
deliberately NOT extended with a `pool` class; instead the
`pool` field rides on `model_member_selected` exactly like the
existing additive `reason` / `reason_class` keys do, keeping the
reason taxonomy unchanged.

### D6 schema: NO migration

`backup_only` is an additive bool field on each
`pkg/models/group.go:GroupMember`, with `json:"backup_only,omitempty"`
so absent = false (D7 backward compat). NO SQL migration:
`models.members_json` is already a JSON column; absent field
round-trips as default false. Verified round-trip sites:

- `pkg/store/database/store.go:1623` `GroupMemberRefs` —
  translate `MemberBackupOnly` onto `MemberRef.BackupOnly`.
- `pkg/store/database/querybuilder.go` — JSON column
  round-trips through `models.GroupMember` unmarshal, so the
  new `BackupOnly` field is just decoded into the new field
  (zero-value defaults to false).
- `pkg/store/database/store.go` model scan sites
  (`GetModelNative`, `GetModelByName`, `GetTruncateParams`,
  `GetFallbackChain`) — all route through `models.GroupMember`
  so the new field round-trips identically.
- `pkg/store/database/store_group_rewrite.go` rewrite path —
  consumes `models.GroupMember` directly, passes through.
- `pkg/modelscache` decorator — passes through; no
  `MemberRef`-specific caching.
- `pkg/ui/frontend/src/types.ts:177-180` `GroupMember` gains
  `backup_only?: boolean` (optional + defaults to false in JS).

### D7 backward compat

Member without `backup_only` field = default; all existing
behavior identical. The day-1 `coding[coding]` no-op guarantee
holds: a one-member self-shadow group still produces exactly
one usage row; an existing two-member group with no
`backup_only` flags picks among the two with the same
weighted-random + sticky semantics as before. The new logic only
activates when at least one member carries `backup_only: true`.

## Seams touched

| Seam | File:line | What changes |
|------|-----------|--------------|
| Engine `MemberRef` | `pkg/grouplb/engine.go:33-37` | Add `BackupOnly bool` field |
| Engine `OnGroupChanged` | `pkg/grouplb/engine.go:399-428` | Split refs into default + backup selectors + binding maps; survivor filter operates per-pool |
| Engine new method | `pkg/grouplb/engine.go` (new) | `PickBackupMember(groupID, convKey)` — sticky weighted pick in backup pool only |
| Engine new method | `pkg/grouplb/engine.go` (new) | `ExcludeAndBackupReselect(groupID, convKey, excludedMemberID, retryAfter)` — exclusion + sticky rebind in backup pool |
| Engine preload | `pkg/grouplb/engine.go:399` | Adjust to receive the new flag |
| Rewrite helper | `pkg/store/database/store_group_rewrite.go:80-112` | Two-pool loop: default loop first, then backup loop on exhaustion; pool returned alongside the pick |
| Rewrite helper | `pkg/store/database/store_group_rewrite.go:43-77` | `RewriteGroupEntries` populates `res.MemberPool = pool` on the chosen group row |
| `GroupRewriter` interface | `pkg/models/group_rewrite.go:54-73` | Extend `ExcludeGroupMember` return with the pool (so the proxy can observe) |
| Validator | `pkg/models/group.go:113-193` | `ValidateGroupMembers` adds D2 (≥1 default) |
| ModelsManager fan-in | `pkg/store/database/store.go:1623-1631` | `GroupMemberRefs` carries `BackupOnly` |
| Proxy `applyGroupRewrite` | `pkg/proxy/handler_groups.go:24-69` | `groupPickInfo.Pool` populates from `res.MemberPool`; log line on `pool=backup` |
| Proxy telemetry | `pkg/proxy/race_coordinator.go:994-1007` | `model_member_selected` event payload gains additive `"pool"` field |
| FE type | `pkg/ui/frontend/src/types.ts:177-180` | `GroupMember.backup_only?: boolean` |
| FE editor | `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:35-154` | "Backup only" tick per row; setMemberBackupOnly handler |
| FE form | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:151-228` | Client-side mirror of D2 (≥1 default); payload carries `backup_only` round-trip |
| Anthropic mirror | `pkg/proxy/handler_anthropic.go:340-378` + `pkg/proxy/handler_groups.go:355-380` | Same `groupPickInfo.Pool` propagation; `tryGroupReentryAnthropic` mirror carries the same pool observation |

## Mechanism: binding choice

Two separate binding maps inside `groupState` (`pkg/grouplb/binding.go`):

```go
type groupState struct {
    // ... existing fields ...
    members        []MemberRef         // ALL members, sorted
    defaultSel     *weightedSelector   // only default members
    backupSel      *weightedSelector   // only backup members
    bindings       map[string]*binding // existing default pool bindings
    backupBindings map[string]*binding // NEW backup pool bindings
    cooldowns      map[string]time.Time
    // ...
}
```

`ExcludeAndReselect` (unchanged signature) operates on the
default pool (default-selector + `bindings`). `PickBackupMember`
operates on the backup pool (`backupSel` + `backupBindings`).
Sticky-affinity is per-pool — re-using a default binding on a
backup-only request is impossible by construction.

Cooldowns are SHARED across both pools (a member that failed in
the default pool stays cooling for the backup pool too — a
"this member is bad right now" gate). This is the
cleanest interpretation of "exclude-and-reselect terminal-failure
semantics" from the task brief.

## Migration

**None.** `models.members_json` is a JSON column (migration 031);
adding a field is purely additive. `BackupOnly` on
`models.GroupMember` defaults to false via `json:"backup_only,omitempty"`
and zero-value handling, so existing rows decode identically.

## Test plan

Behavioural tests (this exact list, mapped one-to-one):

1. `(a) backup never picked while a default can serve` →
   `pkg/grouplb/engine_test.go` new test — group with 2 default +
   1 backup; many requests; backup member never appears in
   `GetOrSelect` results; no binding for it in `bindings` map
   (only `backupBindings` if called).

2. `(b) backup picked only when defaults exhausted` →
   same file — after forceCooldownExpiryForTest on all defaults
   OR ExcludeAndReselect until ReselectNone, `PickBackupMember`
   returns the backup member with sticky binding.

3. `(c) backup pool weights + sticky` →
   weight + sticky coverage: same logic applied within backup
   pool via `backupSel`.

4. `(d) multi-backup (≥2 backups; exclusion chain across backups)` →
   0 default + 3 backup; after each backup fails, the next
   backup is picked; all-backups exhausted ⇒ no candidate.

5. `(e) existing groups (no flags) unaffected` →
   existing `pkg/grouplb/engine_test.go` + `pkg/proxy/handler_groups_*.go`
   test suite (handler_groups_test.go, handler_groups_metering_test.go,
   handler_groups_equivalence_test.go) all pass unchanged;
   `pkg/store/database/store_group_test.go` likewise.

6. `(f) metering dual-row + masking unchanged` →
   `pkg/proxy/handler_groups_metering_test.go` cases (a)/(b)/(c)/(d)
   pass identically — the predicate `rc.groupPick != nil &&
   winnerModel != reqLog.Model` still satisfies on a backup-served
   request; the group id (not the backup member id) appears
   unmasked at the model-list level (Am26 masking identity
   untouched).

7. `(g) FE tick round-trips` →
   `pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.test.tsx`
   new tests — tick toggles `backup_only` via onChange, round-trips
   through the `FormFields → payload` mapping in
   `GroupModelForm.tsx:210-221` (add a small
   `__tests__/GroupModelForm.test.tsx` if not present), and the
   loader's `initialData.members` mapping preserves an existing
   `backup_only: true` through `makeInitialState` (so edit mode
   doesn't drop the flag).

plus the D2 write-validation test in
`pkg/store/database/store_group_test.go` and a parity pin in
`pkg/ui/kind_rules_parity_test.go` for the FE mirror.

## Branching / commit plan

Conventional commits in this order:

1. `docs(planning): add group-backup-members design note (D1–D7)`
   (this file + commit — precedes code per the brief)

2. `feat(grouplb): add backup_only member flag + two-pool picker`
   (`MemberRef.BackupOnly`, `PickBackupMember`,
   `ExcludeAndBackupReselect`, partition into default + backup
   selectors / bindings; behavioural tests (a)/(b)/(c)/(d);
   existing suite (e) passes unchanged)

3. `feat(group-write): validate ≥1 default member (D2)`
   (`ValidateGroupMembers` rule + corresponding store
   write-path test; kind_rules_parity pin)

4. `feat(proxy): surface pool=backup in rewrite + telemetry (D5)`
   (`GroupRewriteResult.MemberPool`, `groupPickInfo.Pool`,
   race_coordinator `model_member_selected` payload, log line;
   metering test (f) adapts/retains its predicates unchanged)

5. `feat(ui): per-member Backup only tick + client-side D2 guard (g)`
   (`GroupMemberEditor.tsx`, `GroupModelForm.tsx`,
   `types.ts:GroupMember.backup_only?`, vitest round-trip tests)

6. `chore(ui): regenerate FE bundles for the same sources (D7 embed)`
   (vite build within pkg/ui/frontend, hashes committed in the
   same commit as the FE source change — pinned by
   `pkg/ui/embed_regression_test.go:TestEmbedStatic_AllAssetReferencesResolve`)

5/6 will likely be combined (any FE source change carries its
regenerated assets in ONE commit per embed discipline — first
seen in commit history: `ecbcf97` "FE static artifacts rebuilt
from merged sources" — the FE source + assets ride together).

## Risks / known unknowns

- The existing `OnGroupChanged` survivor filter keeps default
  pool survivors even if the backup pool changes; verified under
  `pkg/grouplb/engine_test.go:TestEngine_OnGroupChanged_SurvivorFilter`
  — extension preserves the survivor semantics per-pool.

- The Anthropic mirror (`pkg/proxy/handler_anthropic.go`) MUST
  apply the same group pick rewrite — `applyGroupRewrite` is
  the shared helper, so `MemberPool` propagation rides for free,
  but the `tryGroupReentryAnthropic` mirror in
  `pkg/proxy/handler_groups.go` will need the same
  fallback-to-backup cascade.

- The defaults caching layer in `pkg/store/database/store.go`
  sometimes returns an OLD member snapshot on a hot write path;
  members_json decoding at scan sites is the round-trip point
  and tested through `pkg/store/database/store_group_test.go`.

- The existing `pickServableGroupMember` bounded loop (`attempt <= len(refs)`)
  uses `len(refs)` (defaults + backups). Post D1, the loop becomes
  "iterate defaults, then iterate backups" with each bounded
  separately by the per-pool member count — empirically still
  O(default count + backup count), not worse than today.

## Divergences from D1–D7 (recorded here if any)

At submission time: NONE. The locked decisions all
materialize as designed. The only choice UNLOCKED in D1 was
"parallel binding map or equivalent — your call on mechanism";
I chose TWO binding maps inside `groupState` (one for default,
one for backup) over a single map with a `pool` discriminator
tag, because the per-pool GetOrSelect/ExcludeAndReselect hot
paths stay key-on-memberID-only and the cooldown map (shared)
is the only structure that crosses pools.

---

## D8 — selection-time stale-binding resolution (two-layer model)

Added after the D1–D7 implementation, in response to the user's
requirement: *"Group membership can change ON THE FLY; sticky
bindings must survive/repair correctly."*

### The problem D8 closes

The D1 implementation binds a conversation to a member and relies on
`OnGroupChanged` (the push-based survivor filter) to drop bindings
whose member left the group. That leaves two gaps:

1. **The config-change → selection race.** A request already in
   flight can write its binding AFTER the `OnGroupChanged` call that
   dropped the old row — the binding then points at a member the
   membership no longer contains, and it survives until the NEXT
   config change. The push layer cannot see it (its delta already
   passed).
2. **The reclassification nuance.** A member whose `backup_only`
   flag flipped still EXISTS (it moved pools, it did not leave).
   Membership-scoped drop semantics cannot express "keep, but the
   pool rules changed" — a pool-scoped existence test would kill
   the binding, and the D8 ruling is that the binding is
   **grandfathered** (continuity for in-flight conversations).

### The two layers

| Layer | Trigger | Mechanism | Scope |
|-------|---------|-----------|-------|
| **PUSH (pre-existing)** | group config write → `Engine.OnGroupChanged` (pkg/grouplb/engine.go) | rebuild membership + drop orphan bindings whose member is absent from the NEW membership (both binding maps) | eager GC of bindings for conversations that never come back; keeps `bindingsCount` accurate |
| **PULL (new, D8)** | every selection (`GetOrSelect` / `PickBackupMember`) | resolve the bound member id against the CURRENT membership — the **UNION of both pools** (default ∪ backup). Absent ⇒ discard the binding, re-pick per pool rules, emit the `binding=stale` token | race window (1) + reclassification (2) |

The PULL layer is a strict superset of the PUSH layer's *removal*
semantics: both end at "no binding may point at a member that is
not in the group". The PUSH layer is kept because it is the only
layer that reclaims bindings for conversations that never return.

**Composition rules (no double-drop, no stale repopulation):**

- The PUSH layer drops by *membership delta* (the removed id is not
  in the new refs).
- The PULL layer drops by *existence check at selection time* (the
  bound id is not in the current union).
- Every PULL drop is guarded by `if b, ok := st.bindings[key]; ok`
  — the `bindingsCount` decrement happens at most once per row, so
  the two layers cannot double-drop a row or underflow the counter.
- After a drop, the re-pick is made against the CURRENT membership
  (the selector was rebuilt by the PUSH layer) — a stale member is
  never re-bound.
- The PULL layer never re-populates a row the PUSH layer dropped:
  the re-pick writes a binding for a member that IS in the union.

### Where the selection-time resolution lives

- `pkg/grouplb/engine.go` — `getOrSelect` (the internal
  implementation behind `GetOrSelect` / `GetOrSelectWithRepair`):
  the binding-validity check splits the "member left the group"
  case out of the "TTL-expired" / "cooling" case, logs the
  `binding=stale` token, discards the row, and re-picks. The
  existence test is **union-scoped** (`memberInRefs(st.members, …)`
  where `st.members` holds default ∪ backup) — the D1 code already
  used the union here; D8 keeps it and makes it load-bearing.
- `pkg/grouplb/engine.go` — `pickBackupMember` (internal impl
  behind `PickBackupMember` / `PickBackupMemberWithRepair`): same
  stale branch; the existence test CHANGES from backup-pool-scoped
  (`st.backupMembers`) to **union-scoped** (`st.members`) so a
  backup→default reclassification is grandfathered rather than
  killed.
- `pkg/grouplb/engine.go` — `ExcludeAndReselect` /
  `ExcludeAndBackupReselect`: the B2 precondition now ALSO
  requires the currently-bound member to still exist in the union —
  a removed member can never be returned as the reselect
  replacement.
- `pkg/store/database/store_group_rewrite.go` —
  `pickServableGroupMember`: uses the repair-aware engine entry
  points, carries the repair signal up to the proxy, and treats
  `ErrNoMembers` from the default pool as "default pool exhausted"
  (fall through to the backup pool) instead of failing the request.

### Grandfathering asymmetry (explicit)

| Case | Existing binding | New picks |
|------|------------------|-----------|
| member **removed** (either pool) | DISCARDED, re-pick per pool rules (default first; backup only when the default pool is exhausted) | n/a |
| **default → backup** flag flip (member still exists) | **CONTINUES on that member** (grandfathered — continuity) | never land on it while backup-flagged (the default selector no longer contains it) |
| **backup → default** flag flip (member still exists) | **CONTINUES on that member** (fully consistent with the row above) | never land on it while default-flagged (the backup selector no longer contains it) |

The asymmetry is DELIBERATE and is the reason the existence test is
union-scoped rather than pool-scoped: a reclassification is a
*configuration* change to the member, not a removal of the member,
so the conversation's continuity is preserved. Grandfathering ends
naturally on any of: actual removal, idle-TTL eviction of the
binding, or the run excluding that member for failure (the
exclude-and-reselect path rebinds away from it).

### Empty-default defensive path

The "all defaults removed" state is **unreachable via the API** — D2
rejects a zero-default group at the write path
(`pkg/models/group.go:ValidateGroupMembers`). The picker must
still handle it defensively:

- `GetOrSelect` on a group whose default pool is empty returns
  `ErrNoMembers` (the default selector is nil).
- `pickServableGroupMember` treats that as **"default pool
  exhausted"** and falls through to the backup pool — the request
  is served by a backup member instead of failing.
- The engine-level test constructs the state DIRECTLY (bypassing
  the write-path validation) to assert "no error + correct pool".

### Stale token surface

- **Log** (engine, same idiom as `pool=backup`):
  `[grouplb] group=%s conv=%s binding=stale member=%s (member no longer configured) — re-picking`
  and the backup-pool variant carrying `pool=backup` before `conv`.
  Bounded + redacted by construction: it carries only the group id,
  the conversation key and the member id — the same three internal
  identifiers the surrounding `[grouplb]` log family already prints.
- **Event**: the additive `"binding": "stale"` field on the
  `model_member_selected` payload — the SAME carrier the D5
  `"pool"` field uses (`pkg/proxy/race_coordinator.go`). Absent
  unless the condition occurs. The signal travels
  engine → `pickServableGroupMember` → `GroupRewriteResult.BindingStale`
  → `groupPickInfo.BindingStale` → event payload.

### D8 implementation findings (appended after the implementation)

Three things the code reality forced, recorded here so the note stays
accurate:

1. **The default pool's E-3 single-member fast path does NOT
   inspect bindings** (pre-existing load-balance-groups contract:
   "the fast path ignores cooldowns … NO map writes"). It returns the
   group's CURRENT single member, so a removed member can never be
   served from it — but it also cannot REPORT a stale repair, because
   it never looks at the binding map. Consequence: the
   `binding=stale` signal is not emitted for a single-member default
   group. The stale row is inert there (the fast path ignores it) and
   is reclaimed by the janitor / the next push-layer drop.

2. **The backup pool's single-member fast path DOES write a binding**
   (it stores `backupBindings[convKey]`), so it had to be made
   binding-aware for D8: an existing VALID binding for the
   conversation now wins (grandfathering — the member may have been
   reclassified into the default pool but still EXISTS), and a
   binding whose member left the group is discarded with the
   `binding=stale` token before the pool's only member is bound.
   Without this, the fast path would silently rebind the
   conversation to the pool's only member and break the
   grandfathering ruling.

3. **The E-3 fast path is DEFAULT-POOL ONLY.** A group whose single
   member is backup-flagged has an EMPTY default pool; the fast path
   must not serve that backup through the default entry point. It now
   falls through to the full path, which reports the default pool as
   exhausted (`ErrNoMembers`) so the manager falls to the backup
   pool. (The state is unreachable via the API — D2 — but the engine
   handles it defensively per the empty-default rule above.)

### D8 known limitation (stated, not worked around)

The `binding=stale` signal is emitted by the DEFAULT pool's full path
and by the BACKUP pool's fast + full paths. It is NOT emitted by the
default pool's E-3 single-member fast path (see finding 1) — the
member served is still always correct there, only the observability
signal is absent.
