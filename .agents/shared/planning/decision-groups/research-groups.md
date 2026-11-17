# Research: existing group machinery vs. decision-kind groups

READ-ONLY research. Worktree `../llm-supervisor-proxy-wt-decision-groups`,
branch `feature/decision-groups` @ 24c51ef (== origin/latest at capture time).
All paths relative to that worktree. Every claim carries `path:line`; items I
could not verify are explicitly marked **UNVERIFIED**.

Headline finding: the group machinery is **not** kind-agnostic in three places
(write validation, request-time member predicate, FE pickers), and the
`/v1/decisions` lane **never calls the rewrite at all**. Widening kind rules
(option A) is necessary but not sufficient — the decision lane has no group
entry point, and a group row carries no field that says which lane it belongs
to.

---

## 1. Kind rules — `pkg/models/group.go`

### 1.1 Group data model

`GroupMember` is `{model_id, weight, backup_only}` — no kind field:
`pkg/models/group.go:42-46`. Cap constant `MaxGroupMembers = 16`:
`pkg/models/group.go:25`.

A group is **a normal model row with `kind="group"`**, not a separate entity:
`KindGroup = "group"` (`pkg/models/config.go:132`), predicate
`func (m *ModelConfig) IsGroup()` (`pkg/models/config.go:154-156`). There is no
group-kind column, no groups table (see §7).

**There is no "group sub-kind" / lane field.** Neither `GroupMember`
(`pkg/models/group.go:42-46`) nor the `models` table (`members_json`,
`shadow_model_id` only — `pkg/store/database/migrations/sqlite/031_add_group_fields.up.sql:39-40`)
records whether a group is a chat group or a decision group. The lane can only
be inferred from member kinds (N lookups) today.

### 1.2 Field-level rules — `validateGroupRules` (`pkg/models/group.go:60-94`)

| Rule | Line | Error string |
|---|---|---|
| G1 `Internal == true` | `:66-68` | `model %s: kind %q requires internal to be true` |
| G6 `FallbackChain` empty | `:72-74` | `... does not support fallback_chain` |
| G6 `SecondaryUpstreamModel` empty | `:75-77` | `... does not support secondary_upstream_model` |
| G6 `PeakHourEnabled == false` | `:78-80` | `... does not support peak_hour_enabled` |
| G6 `ReleaseStreamChunkDeadline == 0` | `:81-83` | `... does not support release_stream_chunk_deadline` |
| G7 | `:84-85` | retired with the ultimate subsystem — **no gate remains** |
| G8 self-part (shadow ≠ own ID) | `:90-92` | `... shadow_model_id cannot be the group's own ID` |

`validateGroupRules` is reached from `ValidateKindRules`'s `case KindGroup`
(`pkg/models/config.go:228`). It has no collection access and no provider gate
(`pkg/models/group.go:57-61`).

### 1.3 Collection-level rules — `ValidateGroupMembers` (`pkg/models/group.go:115-214`)

| Rule | Line | Error string |
|---|---|---|
| G2 ≥1 member | `:123-125` | `model %s: kind %q requires at least one member` |
| **G2 cap 16** | `:126-128` | `model %s: kind %q members list exceeds max of %d entries (got %d)` |
| D2 ≥1 non-backup member | `:139-147` | `... requires at least one non-backup (default) member (currently %d default, %d backup)` |
| empty `model_id` | `:158-160` | `... members[%d].model_id is empty` |
| weight > 0 (G3) | `:161-163` | `... weight must be > 0, got %v` |
| G4 duplicate | `:164-169` | `... duplicate member model_id %q in members list` |
| G3 existence | `:171-175` | `... member %q does not exist (members must reference existing chat-kind models)` |
| **G5 group-as-member** | `:176-179` | `model %s: kind %q member %q is itself a group (flat membership only — group-as-member is forbidden)` |
| **G3 kind = chat** | `:180-184` | `model %s: kind %q member %q must be a chat-kind model (got kind %q)` |
| G8 shadow target chat-kind | `:193-198` | `... must reference a chat-kind model (got kind %q)` |
| G8 uniqueness (other ENABLED group) | `:203-210` | `... shadow_model_id %q is already claimed by enabled group %q` |

`ValidateGroupMembers` doc block states the rule verbatim: "a member must be an
existing TEXT-kind model — kind chat (or \"\" legacy). Image-gen/decision models
are NOT valid members (they are not chat-servable)" — `pkg/models/group.go:111-114`.

**"Members must be chat kind" is enforced in four places (five edits):**

1. `pkg/models/group.go:180-184` — canonical collection validator.
2. `pkg/store/database/store_group_rewrite.go:291-301` — **request-time**
   re-check (`isServableGroupMemberBase`).
3. `pkg/ui/server.go:941-944` — POST inline mirror.
4. `pkg/ui/server.go` PUT inline mirror (kind check inside the group block that
   starts at `pkg/ui/server.go:1364`; group kind check near `:1441-1445`).
5. Shadow-target twin: `pkg/models/group.go:196-198`, `pkg/ui/server.go:962-964`,
   PUT twin near `pkg/ui/server.go:1462-1466`.

## 2. Group resolution — `pkg/store/database/store_group_rewrite.go`

### 2.1 Rewrite entry and four-phase sweep

`RewriteGroupEntries` (`:49-86`): direct-ID `GetModelNative` per entry (`:56`),
identity for non-group entries (`:57-59`), member substitution +
member's own fallback chain (`:74-75`), first group's pick reported (`:76-82`).

`pickServableGroupMember` (`:130-245`) doc block `:88-129` is the authoritative
statement of ORDERING-NOT-FILTERING:

- Phase 1 (`:97-108`, code `:147-174`) — DEFAULT pool via
  `GetOrSelectWithRepair`; each offer classified: base-stale → genuine
  exclusion (`ExcludeAndReselect`, cooldown write kept, `:164-167`); skip-marked
  or engine-cooling → **demoted into bucket, no cooldown write** (`:168-173`);
  otherwise served (`:170`).
- Phase 2 (`:109-110`, code `:176-201`) — BACKUP pool via
  `PickBackupMemberWithRepair`, identical classification; log line
  "all default members exhausted for this run" (`:195`).
- Phase 3 (`:111-116`, code `:203-227`) — completeness sweep over every ref the
  engine never offered; healthy-but-unoffered anchors at `time.Now()` (`:217-221`).
- Phase 4 (`:117-120`, code `:229-243`) — the demoted bucket **executes**:
  `sort.SliceStable` by `(expiry asc, configured position asc)` (`:233-238`),
  head returned as a full candidate (`:239-242`).

Semantics: a mark demotes, never removes. A request fails only after every
base-servable member of both pools has been ordered (`:122-124`).

### 2.2 Predicates

- `isServableGroupMemberBase` (`:291-301`): `row != nil && row.Enabled`, then
  `switch row.Kind { case "", models.KindChat: return true; default: return false }`
  (`:295-300`). Health-agnostic by design (`:289-290`).
- `isServableGroupMember` (`:318-320`): pure delegate to the base predicate; the
  part-2 cache-aware arms are RETIRED (`:303-317`).
- `memberDemotionExpiry` (`:273-282`): min of skip-cache soonest expiry
  (`memberSoonestSkipExpiry` `:340-353`) and `groupEngine.MemberCooldownUntil`
  (`:278`).

### 2.3 Two-tier pools

Pools split from the same ref list: `defaultRefs` (`:359-367`), `backupRefs`
(`:369-377`); `backup_only` round-trips as an additive JSON bool
(`GroupMember.BackupOnly`, `pkg/models/group.go:36-45`). Ordering: every default
member is offered before any backup (Phase 1 before Phase 2, `:147` vs `:176`);
engine guarantees the same inside its own two-pool layout
(`pkg/grouplb/engine.go:11-25`).

### 2.4 Weighted selection

Weights live on `grouplb.MemberRef.Weight` (`pkg/grouplb/engine.go:57-62`), built
by `GroupMemberRefs` (`pkg/store/database/store.go:1677-1686`) from
`member.Weight` with positions = slice order. The weighted picker is
`newWeightedSelector` (`pkg/grouplb/selector.go:26`) and `weightedSelector.pick`
(`pkg/grouplb/selector.go:52`), driven by the rng in `getOrSelect`
(`pkg/grouplb/engine.go:429`).

### 2.5 Kind assumption in resolution — **YES, at request time**

Resolution is **not** kind-agnostic: `isServableGroupMemberBase` re-asserts the
chat-kind rule on every pick (`pkg/store/database/store_group_rewrite.go:295-300`).
A decision-kind member would be classified base-stale in Phase 1 (`:164`),
excluded with a cooldown write (`:165`), skipped in Phase 3 (`:212-214`), and the
group would end `FailedGroupID` (`:63-65`, `:244`). The engine itself
(`pkg/grouplb`) is fully kind-blind — see §3.

### 2.6 Tried-set / within-request exclusion

- Engine-side genuine failure exclusion: `ExcludeGroupMember`
  (`pkg/store/database/store_group_rewrite.go:406-431`) → `ExcludeAndReselect`
  (`:413`), then re-runs the **same** ordering sweep for the offer (`:420`), and
  refuses when the head is the member that just failed (`:424-429`).
- Rebind exclusion sets inside the sweep: `RebindPast` (`:173`),
  `RebindBackupPast` (`:200`), `seenKeys` (`:260-266`).
- Request-scoped true exclusion (proxy side): `groupTriedMembers` on the request
  context — declared `pkg/proxy/handler_helpers.go:84-85`, populated
  `pkg/proxy/handler.go:691`, `:975-979`, walked in `tryGroupReentry`
  (`pkg/proxy/handler.go:1072-1097`, mark at `:1117`); Anthropic twin
  `pkg/proxy/handler_groups.go:457-494`, declaration
  `pkg/proxy/handler_anthropic.go:1804-1805`.

## 3. grouplb engine — `pkg/grouplb/`

### 3.1 API surface (file:line)

`NewEngine` `engine.go:146` · `HasGroup` `:276` · `HasBackupPool` `:290` ·
`DefaultPoolHealthy` `:315` · `GetOrSelect` `:394` · `GetOrSelectWithRepair`
`:408` · `OnGroupChanged` `:590` · `Stop` `:671` · `ExcludeAndReselect` `:747` ·
`PickBackupMember` `:870` · `PickBackupMemberWithRepair` `:880` ·
`ExcludeAndBackupReselect` `:1066` · `RebindPast` `:1196` ·
`RebindBackupPast` `:1234` · `ClearCooldown` `:1274` · `MemberCooldownUntil`
`:1293`.

Binding internals: `binding.expired` `binding.go:27`, `newGroupState` `:103`,
`memberCount` `:116`, `memberInRefs` `:124`, `isCoolingLocked` `:135`,
`pickHealthyLocked` `:153`, `soonestCooldownLocked` `:194`,
`pickHealthyExcludingLocked` `:217`.

Events live in `pkg/grouplb/events.go`; the constants are pinned by
`TestEventConstants` (`pkg/grouplb/events_test.go:9`). I did **not** enumerate the
individual event names (not needed for this question).

### 3.2 Kind awareness — none

`engine.go` imports only `errors`, `hash/fnv`, `log`, `sync`, `time`
(`pkg/grouplb/engine.go:3-9`). `MemberRef` carries `ModelID`, `Weight`,
`Position`, `BackupOnly` and explicitly "Zero credential awareness"
(`pkg/grouplb/engine.go:45-62`). There is an import guard test
`TestEngine_ZeroCredentialImportGuard` (`pkg/grouplb/engine_test.go:401`).
The engine is **purely id-based**.

### 3.3 What a new kind must plug in (group machinery only)

Nothing in `pkg/grouplb`. A new kind plugs in at:
1. write validation (`pkg/models/group.go:180-184` + 2 UI mirrors),
2. the request-time predicate (`store_group_rewrite.go:291-301`),
3. a **lane consumer** that calls `RewriteGroupEntries` (today only the chat
   lane: `pkg/proxy/handler.go:669`, the Anthropic lane `handler_anthropic.go:432`,
   and the two re-entry rebuilds `handler.go:1102`, `handler_groups.go:480`).

## 4. modelscache decorator — `pkg/modelscache/models.go`

`CachedModelsConfig` decorates `models.ModelsConfigInterface`
(`pkg/modelscache/models.go:93`; interface definition
`pkg/models/config.go:446-477`).

### 4.1 Methods implemented on the decorator (file:line)

Interface members: `GetModel` `:453` · `GetModelByName` `:506` · `GetModels`
`:533` · `GetEnabledModels` `:542` · `GetTruncateParams` `:549` ·
`GetFallbackChain` `:563` · `GetCredential` `:580` · `GetCredentials` `:620` ·
`ResolveInternalConfigWithAffinity` `:680` · `ResolveInternalConfig` `:750` ·
`AddModel` `:804` · `UpdateModel` `:838` · `RemoveModel` `:880` ·
`AddCredential` `:911` · `UpdateCredential` `:939` · `RemoveCredential` `:972` ·
`Save` `:983` (`return c.inner.Save()`) · `Validate` `:986`
(`return c.inner.Validate()`).

`GetModel` is the claim-first client-facing lookup; the claims redirect lives
only there, never in `modelsByID` (comment `:1105-1109`; pinned by
`TestCache_ClaimAware_GetModel_RedirectsToGroup`
`pkg/modelscache/models_group_cache_test.go:100`).

### 4.2 Optional-interface (structural) forwards — the group seams

| Method | Line | Pattern |
|---|---|---|
| `RewriteGroupEntries` | `:1043-1048` | `if gr, ok := c.inner.(models.GroupRewriter); ok { ... }`; miss ⇒ `GroupRewriteResult{ModelList: modelList}` |
| `ExcludeGroupMember` | `:1056-1061` | same assertion; miss ⇒ `"", "", false` |
| `ClearGroupMemberCooldown` | `:1079-1085` | inline anonymous interface `interface{ ClearGroupMemberCooldown(groupID, memberID string) }` |
| `GetModelNative` | `:1139` | cache-backed, claim-blind (`:1087-1109` doc) |

The canonical precedent quote (`pkg/modelscache/models.go:1063-1085`):

> "ClearGroupMemberCooldown forwards the manager-side WIN-CLEAR seam (73e3bd0)
> … The method deliberately lives OUTSIDE models.GroupRewriter (handler_helpers.go
> asserts it structurally …) so this forward mirrors that same structural
> assertion … CRITICAL-FIX (part 3 review, Critical 1): production
> conf.ModelsConfig is *CachedModelsConfig (CACHE_LAYER_ENABLED default ON —
> cmd/main.go), and without this forward the handler's dynamic assertion failed
> SILENTLY … (the modelscache-decorator-forward-trap, exact shape)."

### 4.3 Decorator-through tests that prove forwarding

- `TestWinClear_ReachesEngineThroughCachedConfig`
  (`pkg/proxy/win_clear_winner_identity_test.go:120`) — binds the CACHED config
  and asserts the engine cooldown row is gone; its failure text names the trap:
  "ClearGroupMemberCooldown is a no-op through the decorator"
  (`pkg/proxy/win_clear_winner_identity_test.go:191`).
- `TestWinClear_ClearsWinnerMemberNotFailedHead`
  (`pkg/proxy/win_clear_winner_identity_test.go:218`) — identity discipline
  through the decorator.

### 4.4 Group-related manager methods (NOT on the interface)

`RewriteGroupEntries` (`pkg/store/database/store_group_rewrite.go:49`) ·
`ExcludeGroupMember` (`:406`) · `ClearGroupMemberCooldown` (`:439`) ·
`notifyGroupEngineLocked` (`pkg/store/database/store.go:1666`) ·
`GroupMemberRefs` (`:1677`) · skip-cache readers `memberSoonestSkipExpiry`
(`pkg/store/database/store_group_rewrite.go:340`) and
`memberDemotionExpiry` (`:273`). `models.GroupRewriter` is a narrow side
interface by design (`pkg/models/group_rewrite.go:79-122`; header comment
`:14-21`).

## 5. Admin API + DTOs — `pkg/ui/server.go`

### 5.1 Routes (no dedicated group routes)

`GET/POST /fe/api/models` → `handleModels` (`server.go:162`, body `:761-1330+`) ·
`/fe/api/models/` → `handleModelDetail` (PUT/DELETE, `server.go:163`) ·
`/fe/api/models/validate` → `handleValidateModel` (`:164`) ·
`/fe/api/models/test` → `handleTestModel` (`:165`) ·
`/fe/api/decisions/test` → `handleDecisionTest` (`:171`) ·
`/fe/api/providers` → `handleProviders` (`:199`, body `:232-241`).

Group CRUD rides the generic model routes: the DTO is `Model`
(`server.go:46`) with `Kind` `:57`, `Members []models.GroupMember :76`,
`ShadowModelID :77`. There is **no** `/fe/api/groups`.

### 5.2 Validation call sites

- POST inline mirror: `pkg/ui/server.go:863-979` (G1 `:870-873`, G2 `:875-882`,
  G6 `:884-899`, G8-self `:904-907`, member loop `:912-945`, shadow `:946-978`).
- PUT inline mirror: `pkg/ui/server.go:1364` (group block) through ~`:1500`
  (target-kind `:1439`, shadow `:1460`, uniqueness `:1472`).
- Kind allow-list (POST `pkg/ui/server.go:1036-1041`, PUT `:1530-1535`).
- DB write path: `validateBeforeWrite` → group branch
  (`pkg/store/database/store.go:2089-2093`) → `validateGroupWrite`
  (`:2112-2117`) → `models.ValidateKindRules` + `models.ValidateGroupMembers`.
- JSON backend: `ModelsConfig.Validate` calls `ValidateKindRules`
  (`pkg/models/config.go:1148`); group collection branch at
  `pkg/models/config.go:694` / `:1005`; member-delete block at `:929-939`.

### 5.3 The three decision-provider gates + registry

| Gate | file:line | Validates |
|---|---|---|
| helper | `pkg/models/config.go:293-300` | `validateDecisionRules` D4: `openai` / `typesafe` / `openrouter` |
| POST mirror | `pkg/ui/server.go:1167-1178` | same set, primary credential provider |
| PUT mirror | `pkg/ui/server.go:1632-1643` | same set |
| registry init | `pkg/decision/registry.go:26`, `init()` `:49-53` | adapter availability per provider |
| FE constant | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` | `PROVIDER_OPTIONS = ['openai','typesafe','openrouter']` |

**Credential-provider vocabulary is separate**: `validProviders`
`pkg/models/credential.go:15-63` (includes `openrouter` `:54`, `typesafe` `:59`;
`lightllm`, `opencode`, `opencode-go`, …), read-only accessor
`ValidProviderNames()` `:73`. Credential validation uses that map
(`credential.go:64`), not the decision set.

Answer to "would a group member's provider hit which gate": **neither.** Group
validation never consults either vocabulary — `validateGroupRules` explicitly
skips the provider gate (`pkg/models/group.go:57-61`), and
`validateGroupWrite` passes `""` as `internalProvider`
(`pkg/store/database/store.go:2113`). A group row has no credentials of its own
(G1 rationale `pkg/models/group.go:63-68`). Providers are only checked on the
**member rows** when those members are themselves created as decision models.

### 5.4 FE-facing provider list

`GET /fe/api/providers` exists (`pkg/ui/server.go:199`) but returns
`providers.GetProviders()` (`pkg/ui/server.go:238`) — the chat-provider
registry, **not** the decision vocabulary and not `models.ValidProviderNames()`.
The decision provider list is a hard-coded FE constant
(`DecisionModelForm.tsx:29`).

Also FE-side and chat-only: group member picker filters
`!m.kind || m.kind === 'chat'`
(`pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:40`), chat model
feed for the group form `ModelsTab.tsx:363`, group form shadow mirror
`GroupModelForm.tsx:189-200`.

## 6. Proxy entry — chat lane

### 6.1 Group detection → member list (text lane)

1. `applyGroupRewrite` (`pkg/proxy/handler_groups.go:68-94`) structurally asserts
   `conf.ModelsConfig.(models.GroupRewriter)` (`:72`) and calls
   `RewriteGroupEntries` (`:76`); unrecoverable group ⇒ `failGroupID` (`:77-79`).
2. Chat lane call site: `pkg/proxy/handler.go:669` (inside the
   `rc.resolvedModel != nil` block after conversation-key derivation
   `handler.go:638-656`); failure envelope `group_unservable` at
   `pkg/proxy/handler.go:683`; success stamps `rc.modelListOriginal`,
   `rc.groupPick` (`:685-688`) and seeds `groupTriedMembers` (`:691`).
3. Anthropic lane call site: `pkg/proxy/handler_anthropic.go:432`.
4. Re-entry rebuilds: `tryGroupReentry` (`pkg/proxy/handler.go:1067-1160`,
   rebuild at `:1102`, called from `:1028`) and `tryGroupReentryAnthropic`
   (`pkg/proxy/handler_groups.go:452`, rebuild at `:480`).
5. Sticky ordering `applyStickyOrder` (`pkg/proxy/handler_groups.go:134`), applied
   at the race loop top (`pkg/proxy/handler.go:877`) and the Anthropic loop
   (`pkg/proxy/handler_anthropic.go:499`).

### 6.2 Kind dispatch — there is no central kind router

Each lane carries its own post-resolve guards:

| Lane | Guard | file:line |
|---|---|---|
| chat (`/v1/chat/completions`) | image-gen reject | `pkg/proxy/handler.go:558-564` |
| chat | decision reject (`decision_misroute`) | `pkg/proxy/handler.go:576-582` |
| Anthropic (`/v1/messages`) | decision reject only | `pkg/proxy/handler_anthropic.go:272` |
| decisions (`/v1/decisions`) | `!model.IsDecision()` → 400 `decision_misroute` | `pkg/decision/handler.go:265-277` |

Groups pass the chat guards because `IsGroup()` is neither image-gen nor decision
(`pkg/models/config.go:148-156`). `/v1/models` listing: group shadow-hide at
`pkg/proxy/handler.go:339`, kind exclusion `IsImageGen() || IsDecision()` at
`:350` (groups stay listed by design).

**The decisions lane has no group awareness at all**: it resolves one model
(`pkg/decision/handler.go:253`), rejects non-decision kinds (`:265`), resolves
one credential for that single id (`:299-310`), and makes exactly one upstream
call — "Single upstream call (no retry / fallback / race …)"
(`pkg/decision/handler.go:430-433`). Wiring: `cmd/main.go:320-328`
(`GetModel` + `ResolveInternalConfigWithAffinity(modelID, modelID)`), route
`cmd/main.go:377`.

### 6.3 Credential failure classification

- `classifyCredFailure` (`pkg/proxy/cred_failover_class.go:97-108`) —
  LIMIT-CLASS only: quota before rate-limit, else none.
- `rotationEligible` (`:162-164`) — P1: limit classes + upstream 401/403
  (`upstreamAuthRejected` `:142-154`).
- `memberAdvanceBlocked` (`:190-195`) — P2: client-attributable 400/413/415/422
  (`clientAttributableStatuses` `:174-179`) + cancelled context.
- Consumers: race coordinator / re-entry gate; skip-cache mark below.

### 6.4 Skip-cache marks and the win-clear triple surface

`Mark` call sites: chat/race lane `pkg/proxy/race_coordinator.go:2106-2107`
(gated by `classifyCredFailure != none`, `:2092-2094`) and Anthropic lane
`pkg/proxy/handler_anthropic.go:2607-2608`. `Marked`/`AnyMarked` are read only
inside the skip cache itself and in tests (`pkg/skipcache/skipcache.go:154-159`).

Win-clear triple surface: `clearWinnerUpstreamSkip`
(`pkg/proxy/handler_helpers.go:867-886`) → skip `Clear` credential (`:873`) +
provider (`:876`) → optional `ClearGroupMemberCooldown` assertion (`:880-884`) →
decorator forward (`pkg/modelscache/models.go:1079-1085`) →
`grouplb.ClearCooldown` (`pkg/grouplb/engine.go:1274`). Call sites: chat
`pkg/proxy/handler.go:920`, Anthropic `pkg/proxy/handler_anthropic.go:601`
(referenced by the helper doc `handler_helpers.go:846`); agentic hidden-member
test `pkg/proxy/group_fallback_hidden_member_test.go:315`.

### 6.5 Chat-shaped aspects relevant to a non-streaming typed response

- Group substitution feeds a **modelList** that the race coordinator consumes
  (`pkg/proxy/handler.go:882-893`), with streaming winner relay (`:927+`) and
  SSE model masking (`pkg/proxy/handler_groups.go` masker tests
  `pkg/proxy/handler_groups_test.go:633`, `:675`).
- Re-entry assumes "pre-first-byte inherent" and a race winner
  (`pkg/proxy/handler.go:846-855`).
- Metering/`reqLog.RouteModel` semantics are chat-request shaped
  (`pkg/proxy/handler.go:1121-1125`).
- The decision lane is a single `http.Client` call with a per-request timeout
  and no race/buffer machinery (`pkg/decision/handler.go:383-433`), so **none**
  of that structure transfers directly.

## 7. Migrations / schema

- `030_add_model_kind` adds `kind TEXT` — unconstrained, no default
  (`pkg/store/database/migrations/sqlite/030_add_model_kind.up.sql:29`,
  `postgres/030_add_model_kind.up.sql:14`). Adding a new kind needs **no**
  migration.
- `031_add_group_fields` adds `members_json TEXT` + `shadow_model_id TEXT` to
  **`models`** — there is no groups table
  (`sqlite/031_add_group_fields.up.sql:39-40`,
  `postgres/031_add_group_fields.up.sql:10-11`).
- Registry: `{"030"…}` `pkg/store/database/migrate.go:63`, `{"031"…}` `:69`,
  `{"032"…}` `:77`.
- Kind is stored **on the model row only**. A group's lane/kind-of-membership is
  nowhere persisted (see §1.1).

## 8. Tests inventory

### 8.1 What exists

- Group write rules: `pkg/models/group_rules_test.go` —
  `TestValidateKindRules_Group_ValidFixture` `:38`,
  `TestValidateKindRules_Group_FieldRuleViolations` `:50`,
  `TestValidateGroupMembers_CollectionRuleViolations` `:105`,
  `TestValidateGroupMembers_G8_DisabledClaimIsInert` `:227`,
  `TestValidateGroupMembers_SelfShadowLegal` `:243`,
  `TestValidateKindRules_Group_IncludedInUnknownKindMessage` `:257`,
  `TestModelsConfig_Group_SavePath` `:303`,
  `TestModelsConfig_Group_ClaimIndex` `:348`,
  `TestModelsConfig_Group_RemoveModel_MemberScan` `:393`,
  `TestValidateGroupMembers_BackupOnly_D2` `:423`.
- UI parity (helper ≡ POST ≡ PUT): `pkg/ui/kind_rules_parity_test.go` —
  `TestKindRules_Parity_*` (group table around `:520-620`, member-img case
  `:570`, positive case `pkg/ui/kind_rules_parity_test.go:671`,
  `?kind=group` wire shape `:692-731`).
- Store write + resolution: `pkg/store/database/store_group_test.go` —
  `TestGroup_AddModel_GViolations` `:146`,
  `TestGroup_UpdateModel_GViolation_And_Success` `:200`,
  `TestGroup_RewriteGroupEntries_*` `:524`/`:555`/`:592`,
  `TestGroup_ExcludeGroupMember` `:637`,
  `TestGroup_LiveEditSafety_ThreeClauses` `:674`,
  D8/C1/D2/D3 arms `:755-1150`.
- Skip-cache ordering: `pkg/store/database/store_group_skipcache_test.go`
  `:62`, `:82`, `:101`, `:127`, `:165`, `:182`.
- Proxy lane: `pkg/proxy/handler_groups_test.go` (parity `:224`, Am24 `:259`,
  Am26 `:378`/`:400`, Q15 `:480`),
  `handler_groups_equivalence_test.go:500`,
  `group_fallback_hidden_member_test.go:167`/`:232`/`:315`/`:433`/`:493`,
  `group_fallback_part2_test.go:160`/`:251`,
  `handler_groups_shadow_member_test.go:245`/`:304`/`:341`/`:413`,
  `handler_groups_metering_test.go:223`,
  `handle_models_group_test.go:24`/`:75`/`:121`.
- grouplb: `pkg/grouplb/engine_test.go` (≈40 funcs incl.
  `TestEngine_ZeroCredentialImportGuard` `:401`,
  `TestEngine_Backup_*` `:487-1500`, D8 `:909-1233`), `binding_test.go`,
  `selector_test.go`, `events_test.go`.
- Decorator forwarding: `pkg/proxy/win_clear_winner_identity_test.go:120`,
  `:218`.
- Cache claim semantics: `pkg/modelscache/models_group_cache_test.go:100-260`,
  `models_native_cache_test.go:146-360`.

### 8.2 Tests that break if chat-only membership validation is widened

Widening "member must be chat-kind" to also accept decision-kind breaks, at
minimum:

1. `pkg/models/group_rules_test.go:105` `TestValidateGroupMembers_CollectionRuleViolations`
   — its decision-kind member case (fixtures `:167-183` use `KindImageGen`; the
   decision twin is the same table).
2. `pkg/ui/kind_rules_parity_test.go:520-620` — table case
   `mutate: m.Members = []models.GroupMember{{ModelID: "member-img"…}}`
   (`:569-572`) asserts the chat-kind rejection text, and the parity test asserts
   helper ≡ POST ≡ PUT independently.
3. `pkg/store/database/store_group_test.go:146` `TestGroup_AddModel_GViolations`
   — the DB write-path kind-veto arms.
4. Any test pinning `make([]models.GroupMember, 0, models.MaxGroupMembers+1)`
   cap messaging (`pkg/ui/kind_rules_parity_test.go:529-536`) is unaffected by a
   kind widening but breaks if the cap also changes.
5. If the **request-time** predicate is widened, the ordering tests
   `pkg/store/database/store_group_skipcache_test.go:62` and
   `store_group_test.go:592` (`Am28StaleMember`) change meaning: a decision-kind
   member stops being "base-stale" and starts being servable.

### 8.3 Characterization-test style used in this repo

- **Multi-site parity tables**: one rule, N independent assertions, each site
  asserted separately (AND semantics) — `pkg/ui/kind_rules_parity_test.go`
  (helper / POST / PUT). The trap this guards is documented in repo experience
  ("parity tests must assert EACH site independently").
- **Byte-identity pins for unchanged behaviour**: `TestHandlerGroups_Am26_NonGroup_ByteIdentity`
  (`pkg/proxy/handler_groups_test.go:400`),
  `TestGroup_SkipCache_NilCacheByteIdentical`
  (`pkg/store/database/store_group_skipcache_test.go:165`).
- **Mutation-verified guards** (dropping the clause fails the test): e.g. the
  `/v1/models` KindDecision absence test
  `pkg/proxy/handle_models_imggen_test.go:129-164`.
- **Fixture-keyed sweep discipline**: gofmt-line-set diffing rather than
  hunk overlap (project convention, `.agents/tester/PACKS.md`), relevant only if
  test fixtures are edited.

---

## Kind-widening blast radius (option A)

Ranked by risk. "Blast" = what breaks or silently misroutes if skipped.

| # | Risk | Site | Why |
|---|---|---|---|
| 1 | **Fatal — option A is insufficient** | `pkg/decision/handler.go:253-310`, `:430-433`; wiring `cmd/main.go:320-328` | The decisions lane resolves ONE model and makes ONE upstream call. No `RewriteGroupEntries` call exists on this lane. Widening kind rules alone leaves a decision group resolving to a group row with no credentials → 502 "no credentials configured" (`pkg/decision/handler.go:307-308`). A rewrite call site + member-credential resolution must be added. |
| 2 | **Fatal — misroute guard** | `pkg/decision/handler.go:265-277` | `!model.IsDecision()` rejects `kind="group"` with 400 `decision_misroute`. Must become "IsDecision() OR is a decision-lane group". |
| 3 | **High — silent unservability** | `pkg/store/database/store_group_rewrite.go:291-301` (`isServableGroupMemberBase`) | Request-time kind predicate is chat-only. Unchanged ⇒ every decision member is base-stale, cooled, and the group ends `FailedGroupID` (`:63-65`). Fails closed, hard to diagnose. |
| 4 | **High — write reject** | `pkg/models/group.go:180-184` (+ doc `:111-114`) | The canonical G3 rule. Without widening, no decision member can ever be saved. |
| 5 | **High — drift lock** | `pkg/ui/server.go:941-944` (POST) and the PUT twin in the group block starting `pkg/ui/server.go:1364` (~`:1441-1445`) | Inline mirrors; parity test asserts each independently (`pkg/ui/kind_rules_parity_test.go`). |
| 6 | **High — ambiguity with no existing field** | `pkg/models/group.go:42-46`; migrations 031 (`:39-40`) | A group row has no lane field. Post-widening, "decision group" vs "chat group" is undecidable at the group row; the decisions lane must either infer from member kinds (N lookups per request) or a new column / sub-kind must be added (schema change today avoided because `kind` is unconstrained TEXT — `030…up.sql:29`). |
| 7 | Medium — shadow-target rule | `pkg/models/group.go:196-198`; `pkg/ui/server.go:962-964` (+ PUT twin) | Shadow claims must also be allowed to target decision rows if a decision group is to claim an id. |
| 8 | Medium — FE member picker | `GroupMemberEditor.tsx:40`; `ModelsTab.tsx:363`; `GroupModelsTab.tsx:303`; `GroupModelForm.tsx:189-200` | Pickers filter to chat; shadow mirror validates chat-only. Operator cannot build a decision group from the UI. |
| 9 | Medium — cap semantics | `pkg/models/group.go:126-128`; `pkg/ui/server.go:879-882`, parity `:529-536` | Unchanged, but any decision-group wanting >16 members (credential-pool-like) hits the G2 cap. |
| 10 | Medium — G6 posture | `pkg/models/group.go:72-83` | Decision models forbid `fallback_chain` (`pkg/models/config.go:302-304`); a group is the natural "fallback" surface, so the G6/fallback interaction must be decided, not inherited. |
| 11 | Low — docs/comments | `pkg/models/config.go:117-132` (KindGroup doc: "served by the normal chat pipeline"); `pkg/store/database/store_group_rewrite.go:15-21` | Stale wording after widening. |
| 12 | Low — tests to update | §8.2 list | Expected churn: 3 primary files (`group_rules_test.go:105`, `kind_rules_parity_test.go:520-620`, `store_group_test.go:146`) + any ordering test that assumed decision members are stale. |

Not affected: `pkg/grouplb` (kind-blind, `engine.go:3-9`), migrations 030/031
(no new column required unless a lane field is added), the modelscache decorator
(§4 forwards are kind-agnostic).

## Option B touch points (parallel decision-group path)

Everything a parallel path would have to duplicate, by layer:

1. **Data model / persistence** — a second member list. Cheapest realistic
   reuse: reuse `models.kind="group"` + `members_json` and add a lane field, but
   that is option A-adjacent; a truly parallel entity needs its own columns
   (`031`-style migration) because groups are columns on `models`
   (`migrations/sqlite/031_add_group_fields.up.sql:39-40`).
2. **Validation** — duplicate the G1/G2/G6/G8 field rules
   (`pkg/models/group.go:60-94`), the collection rules (`:115-214`), and the
   two UI inline mirrors (`pkg/ui/server.go:863-979`, `:1364-~1500`).
3. **Selection engine** — either reuse `pkg/grouplb` (kind-blind, safe:
   `engine.go:3-9`) or duplicate ~1300 lines of sticky binding / two-pool /
   cooldown logic (`engine.go:146-1311`).
4. **Resolution sweep** — duplicate the four-phase ordering sweep including the
   tried-set and demote-not-filter semantics
   (`pkg/store/database/store_group_rewrite.go:88-245`), plus the request-time
   predicate (`:291-301`) adapted to decision kind, plus `ExcludeGroupMember`
   (`:406-431`) and `ClearGroupMemberCooldown` (`:439`).
5. **Decorator** — every new manager method needs a `CachedModelsConfig` forward
   + a decorator-through test, per the documented trap
   (`pkg/modelscache/models.go:1063-1085`;
   `pkg/proxy/win_clear_winner_identity_test.go:120`).
6. **Lane entry** — a decision-lane rewrite call + member credential resolution +
   per-member upstream call loop inside `pkg/decision` (today one call,
   `pkg/decision/handler.go:430-433`), including failure classification
   decisions (`pkg/proxy/cred_failover_class.go:97-195`) and — if resilience is
   wanted — a skip-cache/cooldown story (marks today are chat-lane only:
   `race_coordinator.go:2106-2107`, `handler_anthropic.go:2607-2608`).
7. **Admin API + DTO** — new routes/DTOs (there is no `/fe/api/groups` today,
   `pkg/ui/server.go:159-204`) or reuse of the generic `Model` DTO
   (`pkg/ui/server.go:46-77`).
8. **FE** — new form/tab or rework of `GroupModelForm.tsx` /
   `GroupMemberEditor.tsx` pickers (`:40`) plus the ModelsTab feed (`:363`).
9. **Metering / observability** — group-vs-member row shapes and
   `model_member_selected` (`pkg/models/group_rewrite.go` result fields
   `:23-68`; proxy publish sites `pkg/proxy/handler.go:1133-1146`).
10. **Tests** — the parity/multi-site discipline (§8.3) must be rebuilt for the
    new path; nothing transfers automatically.
