# Runtime candidate-list plumbing — research (AUTH COMMISSION `token-authz`)

Basis: worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`,
branch base `a3f0335` (feature/token-authz-allowed-groups). All `file:line` below are
verified against the worktree at that SHA (read-only; no source file was modified).

---

## 0. DRIFT REPORT vs the commission's cited line numbers

| Commission/report citation | Status at a3f0335 | Correct location |
|---|---|---|
| `pkg/proxy/handler_functions.go:200-202` — entry + fallback-chain assembly | **CORRECT** | `handler_functions.go:196-206` (modelList build at `:200-202`) |
| `pkg/proxy/handler_anthropic.go:330-332` | **CORRECT** | `handler_anthropic.go:326-336` |
| `pkg/proxy/handler.go:996` — race-coordinator candidate-list construction | **CORRECT** | `handler.go:996` (`newRaceCoordinatorWithEvents(... rc.modelList ...)`) |
| `pkg/proxy/race_coordinator.go:858` (`c.models[i+1]`) — chain advance | **PARTLY DRIFTED** | `:858` IS the advance CALL SITE but it reads `c.fallbackAdvanceTargetLocked(latestReq.modelID)`. The literal `c.models[i+1]` is at **`race_coordinator.go:1906`** inside `fallbackAdvanceTargetLocked` (`:1899-1912`) |
| `pkg/proxy/race_coordinator.go:884` — spawn | **DRIFTED** | `:884` is `modelID: advanceTarget,` inside the `spawnTriggerInfo` literal. The SPAWN HELPER is `race_coordinator.go:406` `func (c *raceCoordinator) spawn(mType upstreamModelType, triggerInfo spawnTriggerInfo)`; spawn CALL SITES: `:809` (credFailover), `:947` (second/hedge), `:949` + `:954` (fallback) |
| `pkg/proxy/handler_groups.go:68-94` — `applyGroupRewrite` | **CORRECT** | `handler_groups.go:68-94` |
| `pkg/proxy/handler_groups.go:799-815` — chat group member stamping | **DOES NOT EXIST** (file is 589 lines) | The chat-lane rewrite + stamping live in **`pkg/proxy/handler.go:782-816`**: `applyGroupRewrite` call `:783`, `modelListOriginal` `:800`, list swap `:801`, `groupPick` `:802`, tried-set seed `:805`, `RouteModel` stamp `:810-812`, log `:813-814`. The Anthropic mirror is `handler_anthropic.go:472-504` |
| `pkg/proxy/handler.go:1196-1290` — re-entry, pick at `:1231` | **CORRECT** | `tryGroupReentry` spans `handler.go:1196-1287` (`:1292` = `handleRaceFailure`); `:1231` = the `applyGroupRewrite` rebuild |
| `pkg/proxy/handler_groups.go:452`, `:480` — Anthropic re-entry | **CORRECT** | `tryGroupReentryAnthropic` `:452-542`; `:466` = `ExcludeGroupMember` offer; `:480` = `applyGroupRewrite` rebuild |
| `pkg/decision/policy.go` — decision-group candidate list | **CORRECT but incomplete** | `policy.go:58` `buildGroupPolicy`, `policy.go:110` `groupPolicy.order()`; the LIST is CONSUMED at **`pkg/decision/group.go:118-119`** |
| `pkg/store/database/store_group_rewrite.go:105-117` — four-phase sweep | **DRIFTED** | Four-phase sweep lives in `pickServableGroupMember` (**`:160-286`**): Phase 1 `:188-215`, Phase 2 `:217-242`, Phase 3 `:244-268`, Phase 4 `:270-284`. `:105-117` is inside `RewriteGroupEntries`' body / the doc comment |
| `store_group_rewrite.go:291` — `isServableGroupMemberBase` | **DRIFTED** | **`:348`** |
| `store_group_rewrite.go:318` — `isServableGroupMember` | **DRIFTED** | **`:434`** (method on `*ModelsManager`) |
| "race_coordinator.go + race_executor.go contain ZERO token references" | **VERIFIED CORRECT** | Every `token`/`Token` hit in those two files is LLM-token-usage / tokenizer / "Token Plan" vocabulary — never the client auth token |

---

## 1. EVERY candidate-list CONSTRUCTION site

### 1.1 Model-list assembly (chain entries)

| Lane | Site | Lines |
|---|---|---|
| OpenAI chat | `(*Handler).initRequestContext` — `modelList = [resolvedModel.ID] + resolvedModel.FallbackChain` | `pkg/proxy/handler_functions.go:196-206` (build `:200-202`) |
| Anthropic | `HandleAnthropicMessages` — same shape | `pkg/proxy/handler_anthropic.go:326-336` (build `:330-332`) |

Both lists are then handed to the race/sequential walkers. The Anthropic lane has **no race
coordinator** — it walks `arc.modelList` sequentially (`handler_anthropic.go:548-767`).

### 1.2 Group member selection (the group → member substitution)

Single shared seam, called by BOTH lanes:

- `pkg/proxy/handler_groups.go:68` — `func (h *Handler) applyGroupRewrite(conf *ConfigSnapshot, modelList []string, conversationKey string) (newList []string, pick *groupPickInfo, failGroupID string)`
  - `:72-75` structural assert `conf.ModelsConfig.(models.GroupRewriter)`; no backend ⇒ identity passthrough.
  - `:76` `gr.RewriteGroupEntries(modelList, conversationKey)` — **THE candidate-list construction for a group**.
  - `:77-79` `res.FailedGroupID != ""` ⇒ caller must surface the resolve-failure envelope.
  - `:87-93` returns the rewritten list + `groupPickInfo{GroupID, MemberID, NewlyBound, Pool, BindingStale}`.
- Call sites: `pkg/proxy/handler.go:783` (chat) and `pkg/proxy/handler_anthropic.go:472` (anthropic) — plus the two re-entry rebuilds `handler.go:1231` and `handler_groups.go:480`.
- Backend: `pkg/store/database/store_group_rewrite.go:55` — `func (m *ModelsManager) RewriteGroupEntries(modelList []string, conversationKey string) models.GroupRewriteResult`
  - `:62` per-entry `m.GetModelNative(entry)` (claim-blind, row identity).
  - `:82-88` decision-lane refusal at the rewrite boundary.
  - `:89` `m.pickServableGroupMember(row, conversationKey)` — **the actual member pick**.
  - `:101-102` execution list = `[member.ID] + member.FallbackChain`.
- Decorator forward (production always runs through it): `pkg/modelscache/models.go:1301-1306` `(*CachedModelsConfig).RewriteGroupEntries` — **adds NO caching** (comment `:1296-1300`); the rewrite is per-request and engine-side.

### 1.3 Race-coordinator candidate-list construction

- `pkg/proxy/handler.go:996` — `coordinator := newRaceCoordinatorWithEvents(rc.baseCtx, &rc.conf, r, rc.rawBody, rc.modelList, h.bus, rc.reqID, rc.interleavedThinking, h.credEngine, rc.conversationKey, liveFirstByteGate, h.stickyEngine)`
  - The candidate list IS `rc.modelList` (post-group-rewrite, post-sticky-reorder `:991`).
- Constructor: `pkg/proxy/race_coordinator.go:324-369` `newRaceCoordinatorWithEvents(...)`; `:338-340` empty list ⇒ `{cfg.ModelID}`; `:350` `models: models`; `:351` `requests: make(..., len(models))`.
- The list is re-aliased on EVERY iteration of the race loop (`handler.go:971` loop, `:991` sticky reorder) — the group re-entry rebuild mutates `rc.modelList` (`:1248`) and re-enters the same loop (`:1155` `continue`).

### 1.4 Decision-group candidate list

- `pkg/decision/group.go:118` `policy := buildGroupPolicy(group, h.currentModelRows(), lane)`; `:119` `order := policy.order()`; `:120-123` empty ⇒ exhaustion.
- `pkg/decision/policy.go:58-96` `buildGroupPolicy` — candidacy = row exists AND enabled AND member lane == group lane (`:72-84`); weights `:85-88`; pools `:89-92`.
- `pkg/decision/policy.go:110-136` `groupPolicy.order()` — weighted head from the default pool, then remaining defaults, then backups.

### 1.5 Credential-layer candidate list (the deeper tier)

- `pkg/store/database/store.go:2810` and `:2950` — `m.engine.GetOrSelect(modelID, conversationKey)` (the `credentiallb` engine pick), reached from `models.ModelsConfigInterface.ResolveInternalConfigWithAffinity` (`pkg/models/config.go:509` / `:1289`), whose call sites are `pkg/proxy/race_executor.go:543` and `:568`, `pkg/proxy/handler_anthropic.go:822`, `pkg/proxy/internal_handler.go:224`.
- Credential GROUPS (two-tier pools) flatten at `pkg/modelscache/models.go:1473-1516` (`flattenCredentialRefsCacheSide`) and `pkg/store/database/credential_group.go` (`members_json`, migration 033).

---

## 2. The chain ADVANCE call site

- **Advance call site:** `pkg/proxy/race_coordinator.go:858`
  `advanceTarget, hasAdvance := c.fallbackAdvanceTargetLocked(latestReq.modelID)`
  - enclosing function: `(*raceCoordinator).manage()` (the manage loop).
  - gate: `:867` `if hasAdvance && !memberAdvanceBlocked(failReason)`.
  - the advance target rides `spawnTriggerInfo{trigger: triggerMainError, failedRequest: latestReq.id, reason: failReason, modelID: advanceTarget}` (`:880-885`).
- **Advance target computation:** `pkg/proxy/race_coordinator.go:1899-1912`
  `func (c *raceCoordinator) fallbackAdvanceTargetLocked(failedModelID string) (target string, hasAdvance bool)`
  - `:1900-1902` `< 2 models ⇒ no advance`; `:1903-1909` index-of-failed + 1 ⇒ `c.models[i+1]` (**:1906**); `:1911` failed model not in chain ⇒ no advance.
- **Spawn helper:** `pkg/proxy/race_coordinator.go:406` `func (c *raceCoordinator) spawn(mType upstreamModelType, triggerInfo spawnTriggerInfo)`
  - `:415-420` `modelTypeMain` ⇒ `c.models[0]`;
  - `:421-435` `modelTypeSecond` (the credential-level HEDGE) ⇒ `c.models[0]` + `SetUseSecondaryUpstream(true)` (`:491-493`);
  - `:436-458` `modelTypeFallback` ⇒ `triggerInfo.modelID` else `c.models[1]` (`:454-457`);
  - `:459-485` `modelTypeCredFailover` ⇒ `triggerInfo.modelID` else `c.models[0]`.
  - `:488` `newUpstreamRequest(idx, mType, modelID, ...)`; `:513-514` appends to `c.requests` / `c.spawnTriggers`; `:561` `go c.execute(req)`.
- **Spawn call sites:** `:809` credFailover, `:947` second (hedge), `:949` + `:954` fallback. Terminal "all failed" resolution: `:987-996` (and the chain-end latch `c.chainEndPublished` `:895`, gate `:722-723`).

---

## 3. The HEDGE surface (found by reading; there is NO function named "hedge")

There is **no single `hedge` call site**. The mechanism is the race coordinator's PARALLEL
SECOND UPSTREAM ROW, materialised as the `modelTypeSecond` request type:

- Type constant: `modelTypeSecond` (switch cases at `race_coordinator.go:421`; the type
  `upstreamModelType` is referenced at `:406`).
- **Where it is spawned:** `pkg/proxy/race_coordinator.go:947`
  `c.spawn(modelTypeSecond, triggerInfo)` — inside the manage loop's "Case 2: Main request is
  idle (Parallel race retry)" arm (`:900-935`), guarded by `:911`
  `if !shouldSpawn && !spawnedCredFailover && c.cfg.RaceParallelOnIdle && len(c.requests) == 1`.
- **How it picks candidates:** it does NOT pick a new candidate — it re-drives `c.models[0]`
  (`:435`) on the model's SECONDARY upstream/credential (`req.SetUseSecondaryUpstream(true)`
  `:491-493`). Comment at `:421-430`: the secondary upstream "does not widen model coverage".
  So the hedge is a **credential-level** hedge on the SAME model, bounded to exactly one row
  (Case 2 only fires with exactly one running request).
- The second hedge flavour is the **credential-failover spawn** (`modelTypeCredFailover`,
  `race_coordinator.go:809`), which re-drives the SAME model on the NEXT credential
  (`:481-485`, resolved by `credFailoverPlanLocked` `:1929` → `credentiallb.Engine.ExcludeAndReselectForClass`).
- Tests naming it: `pkg/proxy/cache_layer_separation_test.go:593-622` ("a single-model list must
  still get its credential-level hedge"; "the hedge is BOUNDED — exactly one second row").

⇒ For the commission: a hedge/parallel row is ALWAYS on the head candidate (`c.models[0]`) or
on the failed model's own chain — so if the head is scope-legal, the hedge is legal by
construction; a scope filter applied at candidate-list construction covers the hedge for free.

---

## 4. The RE-ENTRY surface

"Re-entry" = returning into the group AFTER a member's pipeline failed terminally, to try
another member of the same group. It is a **handler-level** loop, not a coordinator feature.

### 4.1 OpenAI / chat lane

- Gate: `pkg/proxy/handler.go:1138-1159`
  `if rc.groupPick != nil && !rc.streamingNonRetryable.Load() && rc.groupAdvances < models.MaxGroupMembers {`
  → `:1150` `fr := coordinator.FinalFailureReason()` → `:1151`
  `if !memberAdvanceBlocked(fr) && h.tryGroupReentry(rc) { ... :1155 continue }` (re-enters the
  race loop at `:971`) → else `:1157-1158` `group_member_failed` + `group_fallback_exhausted`
  → `:1161` `h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "all_models_failed")`.
- Body: `pkg/proxy/handler.go:1196-1287` `func (h *Handler) tryGroupReentry(rc *requestContext) bool`
  - `:1213` offer walk: `gr.ExcludeGroupMember(rc.groupPick.GroupID, rc.conversationKey, excludeTarget, rc.conf.UpstreamSkipCacheTTL)`
    (bounded by `models.MaxGroupMembers`, `:1212`; refuses already-tried members `:1217-1221`).
  - `:1223-1226` exhausted ⇒ `false` ("offer walk exhausted without a fresh member — group fallback exhausted").
  - `:1231` REBUILD: `newList, pick, failGroup := h.applyGroupRewrite(&rc.conf, rc.modelListOriginal, rc.conversationKey)` — the SAME ordering authority as the offer (comment `:1228-1230`).
  - `:1242-1245` post-rebuild guard (refuses an already-tried member) → `:1246-1249` tried-mark, `groupAdvances++`, list swap, `groupPick` swap → `:1262-1285` `grouplb.EventMemberFailover` publish → `:1286` `return true`.
- The picked member's own chain then runs; the race coordinator is rebuilt fresh each iteration
  (`handler.go:996`) with the credential tried-set carried across (`handler.go:1006`
  `coordinator.SeedRaceState(rc.credTriedAcrossRace, rc.soonestExpiryAcrossRace)`).

### 4.2 Anthropic lane

- Gate: `pkg/proxy/handler_anthropic.go:728-765` (`arc.groupPick != nil && !arc.payloadCommitted && arc.groupAdvances < models.MaxGroupMembers`) → `:754` `if h.tryGroupReentryAnthropic(arc) { ... :760 continue loop }`.
- Body: `pkg/proxy/handler_groups.go:452-542` `func (h *Handler) tryGroupReentryAnthropic(arc *anthropicRequestContext) bool`
  - `:466` offer walk (`gr.ExcludeGroupMember`), `:476-479` exhaustion ⇒ `false`,
  - `:480` rebuild via `h.applyGroupRewrite(&arc.conf, arc.modelListOriginal, arc.conversationKey)`,
  - `:489-494` post-rebuild guard + tried-mark + `groupAdvances++`, `:504` sticky reorder, `:539` `EventMemberFailover`.

Mechanically: "re-entry" = **the group member pipeline is re-entered** — the failed member is
excluded from the engine's pool (a genuine exclusion with a cooldown write), the engine's
ordering sweep picks the NEXT head, the execution list is rebuilt from the ORIGINAL list, and
the request resumes on the new member. It is bounded to N attempts for an N-member group
(`groupAdvances < models.MaxGroupMembers`).

---

## 5. TOKEN AVAILABILITY in the race coordinator (VERIFIED: absent)

**Verified:** `pkg/proxy/race_coordinator.go` and `pkg/proxy/race_executor.go` contain ZERO
references to the client auth token. Every `token`/`Token`/`auth` hit is LLM-token-usage
(`TokenUsage`, `PromptTokens`), the tokenizer package (`pkg/proxy/token`), upstream
"Token Plan" quota vocabulary, or comments about upstream 401/403. The client token is
**never threaded into the coordinator or the executor**.

### 5.1 Where the token DOES live today

| Holder | Type | Site | Lifetime |
|---|---|---|---|
| `authToken` | `*auth.AuthToken` | chat: local var `pkg/proxy/handler.go:704` (assigned `:706`); anthropic: `handler_anthropic.go:402` (assigned `:404`); decision: `pkg/decision/handler.go` → passed into `serveDecisionGroup` `pkg/decision/group.go:96` | handler-local |
| `rc.tokenID` / `rc.tokenName` | `string` | fields `pkg/proxy/handler_helpers.go:193-194`; set `handler.go:727-728` | request-context |
| `arcTokenID` | `string` | local `handler_anthropic.go:401`, set `:421` | handler-local only — **the anthropic context struct has NO token field** (`handler_anthropic.go:1959-2031`) |
| conversation key salt | `sha256(modelID\|tokenID\|leadingUserRun)` | `pkg/credentiallb/key.go:48` `ComputeConversationKey(modelID, tokenID, leadingUserRun string) string`; chat call `handler.go:758-762`, anthropic call `handler_anthropic.go:449` | request-context |
| `*http.Request` | — | the coordinator HOLDS the raw request: `race_coordinator.go:173` `req *http.Request` (ctor arg `:327`, call site `handler.go:996`) ⇒ the `Authorization` header is physically reachable inside the coordinator | request |

### 5.2 Where it STOPS

- **Chat lane:** after `handler.go:832-878` (the allow-list gate: `authToken.IsModelAllowed(modelID)`
  at `:839`, unknown-model fail-closed arm at `:857`) the `*auth.AuthToken` is dropped; only
  `rc.tokenID` (a string) survives into the race path. The race coordinator gets
  `rc.modelList`, `rc.conf`, `rc.conversationKey` — **no scope**.
- **Anthropic lane:** after `handler_anthropic.go:514` (`authToken.IsModelAllowed(reqLog.Model)`)
  the token is dropped; only `arcTokenID` (string) survives, and it is NOT stored on `arc`.
- **Decision lane:** the only lane where the token STRUCT reaches the group loop
  (`group.go:96` `authToken *auth.AuthToken`) — and it is used ONLY for metering
  (`group.go:341` `h.meterOnSuccess(authToken, winner.memberID, ...)`), never for candidacy.
  **This is the live defect**: the group branch returns at `pkg/decision/handler.go:323-326`
  (`h.serveDecisionGroup(...)`) BEFORE the allow-list gate at `:342-354`, so a decision group
  bypasses `IsModelAllowed` entirely (scope ESCAPE).
- **`context.Context`:** ZERO `context.WithValue` / `ctx.Value` in `pkg/proxy` — the token is
  NOT carried in the context. `rc.baseCtx` (`handler_helpers.go:47`) / `c.baseCtx`
  (`race_coordinator.go:171`) carry only cancellation.

### 5.3 Concrete plumbing options (with real signatures + call sites)

**Option A — explicit scope struct, injected at the coordinator constructor (house precedent).**
Add e.g. `type tokenScope struct { AllowedGroups map[string]bool; AllowedModels map[string]bool }`
to `pkg/proxy` and:
- store it on `requestContext` (struct at `pkg/proxy/handler_helpers.go:25`) and on
  `anthropicRequestContext` (`pkg/proxy/handler_anthropic.go:1959`);
- pass it as a 13th positional arg to `newRaceCoordinatorWithEvents`
  (`pkg/proxy/race_coordinator.go:324-337`; call site `pkg/proxy/handler.go:996`). The file's own
  precedent is the 12th arg `sticky *stickyorder.Engine` (`:319-323`, `:336`, `:349`).
- The coordinator then filters `c.models` at construction (`:350`) — but note the list is
  already built upstream, so filtering inside the coordinator is "skip-at-construction, in the
  coordinator", still pre-attempt.

**Option B — filter at the rewrite seam (recommended: ONE site covers both lanes).**
`applyGroupRewrite` (`pkg/proxy/handler_groups.go:68`) is the shared choke point for the chat
lane (`handler.go:783`), the anthropic lane (`handler_anthropic.go:472`) and BOTH re-entry
rebuilds (`handler.go:1231`, `handler_groups.go:480`). Filtering there excludes unauthorized
members from the candidate list at construction — never at attempt time.

**Option C — filter at the model-list assembly sites** (`handler_functions.go:200-202`,
`handler_anthropic.go:330-332`): covers chain entries (fallback chain) but NOT group members
(the group rewrite happens later and re-introduces members).

**Option D — re-derive from `c.req` inside the coordinator** (`race_coordinator.go:173`):
technically possible (the `Authorization` header is present) but re-validates against the
token store on a hot path and duplicates auth — against house style.

**Option E — `context.Context` value:** no precedent (zero `context.WithValue` in `pkg/proxy`);
would be a new pattern. Not recommended.

---

## 6. Exported signatures that would need an exclusion predicate / scope filter

### 6.1 `pkg/grouplb` (group-member layer — engine, hot)

| Exported symbol | Signature | File:line | Call sites |
|---|---|---|---|
| `Engine.GetOrSelect` | `(groupID, conversationKey string) (memberID string, newlyBound bool, err error)` | `pkg/grouplb/engine.go:410` | (wrapper) |
| `Engine.GetOrSelectWithRepair` | `(groupID, conversationKey string) (memberID string, newlyBound bool, staleRepaired bool, err error)` | `pkg/grouplb/engine.go:424` | **`pkg/store/database/store_group_rewrite.go:191`** (Phase 1 offer) |
| `Engine.PickBackupMemberWithRepair` | `(groupID, conversationKey string) (memberID string, newlyBound bool, staleRepaired bool, err error)` | `pkg/grouplb/engine.go:894` | **`store_group_rewrite.go:220`** (Phase 2 offer) |
| `Engine.ExcludeAndReselect` | `(groupID, conversationKey, excludedMemberID string, retryAfter time.Duration) (memberID string, mode ReselectMode)` | `pkg/grouplb/engine.go:761` | `store_group_rewrite.go:206` |
| `Engine.ExcludeAndBackupReselect` | (same shape, backup pool) | `pkg/grouplb/engine.go:1078` | `store_group_rewrite.go:230` |
| `Engine.RebindPast` | `(groupID, conversationKey string, excludeIDs []string)` | `pkg/grouplb/engine.go:1208` | `store_group_rewrite.go:214` |
| `Engine.RebindBackupPast` | `(groupID, conversationKey string, excludeIDs []string)` | `pkg/grouplb/engine.go:1246` | `store_group_rewrite.go:241` |
| `Engine.OnGroupChanged` | `(groupID string, refs []MemberRef)` | `pkg/grouplb/engine.go:604` | `store_group_rewrite.go:178` (lazy seed), `pkg/store/database/store.go:1706` (write path), `store.go:1897` (delete) |
| `Engine.MemberCooldownUntil` | `(groupID, memberID string) (time.Time, bool)` | `pkg/grouplb/engine.go:1305` | `store_group_rewrite.go:328` |
| `Engine.ClearCooldown` | `(groupID, memberID string)` | `pkg/grouplb/engine.go:1286` | `pkg/modelscache/models.go:1337-1343` (decorator forward) |

The engine's own pick is a weighted selector over `st.refs` — it does NOT know about
"membership" or "scope". The manager-level predicate (`isServableGroupMemberBase`) is where a
scope filter fits without an engine API change (engine offers → manager validates → manager
excludes+reselects; that loop is `:190-215` / `:219-242`).

### 6.2 `pkg/store/database`

| Exported symbol | Signature | File:line | Call sites |
|---|---|---|---|
| `(*ModelsManager).RewriteGroupEntries` | `(modelList []string, conversationKey string) models.GroupRewriteResult` | `pkg/store/database/store_group_rewrite.go:55` | via `models.GroupRewriter` at `pkg/proxy/handler_groups.go:76`; forwarded by `pkg/modelscache/models.go:1301-1306` |
| `(*ModelsManager).ExcludeGroupMember` | `(groupID, conversationKey, memberID string, cooldown time.Duration) (string, string, bool)` | `store_group_rewrite.go:489` | `pkg/proxy/handler.go:1213`, `pkg/proxy/handler_groups.go:466`; forwarded `modelscache/models.go:1314-1319` |
| `(*ModelsManager).GetModelNative` | `(modelID string) *models.ModelConfig` | `pkg/store/database/store.go:1095` (RLock `:1096`) | HOT — `store_group_rewrite.go:62, :94, :204, :228, :252`; `pkg/proxy/handler_groups.go:581-589` (`directGetModel`); `pkg/proxy/race_executor.go:48,:114,:208,:353`; `pkg/proxy/race_coordinator.go:1547,:1668`; `pkg/proxy/handler_anthropic.go:521`. Cache-backed in production: `pkg/modelscache/models.go:1431` |
| `(*ModelsManager).GetModel` | `(modelID string) *models.ModelConfig` | `store.go:1073` (claims-first, client-facing) | `pkg/proxy/handler_functions.go:147` region (boundary resolve) |
| `GroupMemberRefs` | `(members []models.GroupMember) []grouplb.MemberRef` | `store.go:1713` | `store_group_rewrite.go:175`, `store.go:1706` |

### 6.3 `pkg/models` (the seam contract)

| Symbol | Signature | File:line |
|---|---|---|
| `models.GroupRewriter.RewriteGroupEntries` | `(modelList []string, conversationKey string) GroupRewriteResult` | `pkg/models/group_rewrite.go:91` |
| `models.GroupRewriter.ExcludeGroupMember` | `(groupID, conversationKey, memberID string, cooldown time.Duration) (replacement string, pool string, ok bool)` | `pkg/models/group_rewrite.go:121` |
| `models.ModelsConfigInterface.ResolveInternalConfigWithAffinity` | `(modelID, conversationKey string) (ResolvedCredential, bool)` | `pkg/models/config.go:509` (impl `:1289`) |

The house pattern for extending the seam WITHOUT touching the ~11 structural test fakes is the
**optional-interface forward** (see `ClearGroupMemberCooldown` — `modelscache/models.go:1337`,
`GroupLane` — `modelscache/models.go:1370`). The scope filter should follow that shape.

### 6.4 `pkg/credentiallb` (credential layer — only if the scope must bite per-credential)

| Symbol | Signature | File:line | Call sites |
|---|---|---|---|
| `Engine.GetOrSelect` | `(modelID, conversationKey string) (credentialID string, newlyBound bool, err error)` | `pkg/credentiallb/engine.go:226` | `pkg/store/database/store.go:2810`, `store.go:2950` |
| `Engine.ExcludeAndReselect` | `(modelID, conversationKey, excludedCredID string, retryAfter time.Duration) (credentialID string, mode ReselectMode)` | `pkg/credentiallb/engine.go:634` | `pkg/proxy/race_coordinator.go` (`credFailoverPlanLocked` `:1929`+), `pkg/decision/group.go:505` (class form) |
| `Engine.ExcludeAndReselectForClass` | `(modelID, conversationKey, excludedCredID string, class FailureClass, retryAfter time.Duration) (credentialID string, mode ReselectMode)` | `pkg/credentiallb/engine.go:656` | `pkg/proxy/race_coordinator.go:~1950+`, `pkg/decision/group.go:505` |
| `Engine.CooldownUntil` | `(modelID, credID string) (time.Time, bool)` | `pkg/credentiallb/engine.go:467` | `pkg/decision/group.go:484` |
| `Engine.OnModelChanged` | `(modelID string, refs []models.CredentialRef)` | `pkg/credentiallb/engine.go:365` | model write path |

### 6.5 Hot-path flag (request latency)

HOT (per request, per attempt): `GetModelNative` (cache-backed), `RewriteGroupEntries`,
`ExcludeGroupMember`, `grouplb.GetOrSelectWithRepair` / `PickBackupMemberWithRepair`,
`credentiallb.GetOrSelect`, `ResolveInternalConfigWithAffinity`.
A scope filter added to the manager-level predicate is O(members) per group entry — cheap.
An engine-level filter would touch the weighted selector (hot, per request).

---

## 7. GROUP MEMBERSHIP RESOLUTION AT REQUEST TIME (commission Q6)

### 7.1 Storage → row → engine

1. **Storage:** `models.members_json` (migration 031) — column on the `models` row; the row
   struct field `dbModelRow.MembersJSON` (`pkg/store/database/store.go:543`);
   parse `parseGroupMembersJSON` (`store.go:637`); marshal `marshalGroupMembersJSON`
   (`store.go:653`); scanned into `models.ModelConfig.Members` at `store.go:1039, :1166, :1250, :1379`.
   `models.shadow_model_id` = the claim (client-facing virtualisation).
2. **Request-time resolution (the authoritative chain):**
   `RewriteGroupEntries` (`store_group_rewrite.go:55`) →
   `m.GetModelNative(entry)` (`:62`, claim-blind group row) →
   `pickServableGroupMember(row, conversationKey)` (`:89`, impl `:160`) →
   `refs := GroupMemberRefs(group.Members)` (`:175`) →
   engine offers (`m.groupEngine.GetOrSelectWithRepair` `:191`, `PickBackupMemberWithRepair` `:220`)
   → per-member row re-validation `m.GetModelNative(pick)` (`:204`, `:228`, `:252`).
3. **Cache layers:**
   - `pkg/modelscache.CachedModelsConfig` — `CACHE_LAYER_ENABLED` default ON (cmd/main.go).
     `GetModelNative` is CACHE-BACKED (`modelscache/models.go:1431`, fast path `:1437-1443`,
     claim-blind fill `:1459`); the claim-blind `modelsByID` map holds the group row WITH its
     `Members` list.
   - `RewriteGroupEntries` / `ExcludeGroupMember` are **NOT cached** by the decorator
     (`modelscache/models.go:1296-1306`, `:1308-1319`) — the rewrite is per-request and
     engine-side; sticky state lives in the grouplb engine.
   - `pkg/grouplb.Engine` holds the per-group selector state (`st.refs`) — rebuilt by
     `OnGroupChanged` (`engine.go:604`).

### 7.2 INVALIDATION POINTS (where a scope-derived membership set must be invalidated)

| # | Point | Site | Trigger |
|---|---|---|---|
| 1 | `notifyGroupEngineLocked` → `groupEngine.OnGroupChanged(groupID, GroupMemberRefs(members))` | `pkg/store/database/store.go:1702-1707`; called from the Add path `:1689` and the Update path `:1831` | group row written (membership changed) → engine selector rebuilt, orphan bindings dropped |
| 2 | group row deleted → `groupEngine.OnGroupChanged(modelID, nil)` | `pkg/store/database/store.go:1897` | group removed |
| 3 | Am15 lazy seed: `m.groupEngine.OnGroupChanged(group.ID, refs)` | `pkg/store/database/store_group_rewrite.go:176-179` | first rewrite after boot when `HasGroup` is false |
| 4 | modelscache write-through: `AddModel` (`:1062`, claim eviction `:1078`), `UpdateModel` (`:1096`, entry swap `:1117-1128`), `RemoveModel` (`:1138`) | `pkg/modelscache/models.go:1062 / 1096 / 1138` | ANY model write (incl. member enable/disable) → the cached row (and its `Members`) is replaced; negatives cleared |
| 5 | reconciler snapshot swap (`applySnapshot` at `:449`, strict scan `:407`) — the periodic mirror refresh | `pkg/modelscache/models.go:~380-450` | direct-SQL / restore / repair writes become visible (bounded by the reconcile interval) |
| 6 | negative tier (`negByID`/`negByName`, `ModelsNegTTL` 60s) — cleared by 4 and 5 | `pkg/modelscache/models.go:1411-1426` | missing-row verdicts |

⇒ **Answer to commission Q6:** a token-scope-derived membership set MUST be derived at REQUEST
TIME from the CURRENT group row (`group.Members` via `GetModelNative`, i.e. the cached row
refreshed by points 4/5) and must NOT be cached in a new long-lived map. If a scope-derived set
IS cached, its invalidation points are exactly (4) + (5) + (1)/(2)/(3) for the engine's own
refs — i.e. the same set that already keeps membership fresh.

### 7.3 Trap to note

`isServableGroupMemberBase` (`store_group_rewrite.go:348`) validates the member ROW (exists,
enabled, lane) — it reads the CACHED row. A member that was DISABLED may therefore still look
enabled for up to the cache's refresh window (point 5); this is the pre-existing behaviour and
the scope filter inherits it. Any scope predicate that needs fresh membership facts should
read through `GetModelNative` (cache-backed) exactly like the existing shape gate does, rather
than issuing its own DB read (hot path).

---

## 8. Where a scope-exclusion predicate plugs in, and the ALL-EXCLUDED failure semantics

### 8.1 The one predicate that gates every group candidate

`isServableGroupMemberBase(row *models.ModelConfig, lane models.GroupLane) bool`
— `pkg/store/database/store_group_rewrite.go:348-360`:
`:349-351` row exists + enabled; `:352-359` kind ∈ lane.
Called at exactly three places — the three phases of the sweep:
- Phase 1 (default pool): `store_group_rewrite.go:205`
- Phase 2 (backup pool): `store_group_rewrite.go:229`
- Phase 3 (completeness sweep): `store_group_rewrite.go:253`

⇒ A scope predicate plugs in HERE (it is already the "is this member a candidate" gate, and it
runs BEFORE the member becomes the head). Two shapes are possible:
(a) **manager-level (no engine API change):** widen the predicate to
`(row, lane, scope)` and let the existing "engine offers → manager validates → manager
`ExcludeAndReselect` → next offer" loop (`:190-215`, `:219-242`) walk past out-of-scope
members. Cost: up to N engine round-trips per request (N = member count) — bounded by
`len(defaults)`/`len(backups)` loop bounds, and the Phase-3 completeness sweep already
enumerates every ref directly (`:248-268`), so the scope filter MUST also be applied there or
Phase 4 could still serve an out-of-scope member (`:273-283`).
(b) **engine-level:** `grouplb` would need a filter/predicate parameter on
`GetOrSelectWithRepair` / `PickBackupMemberWithRepair` (hot path; the engine's weighted
selector picks over `st.refs`).

Both chat and anthropic lanes go through the SAME manager code ⇒ parity by construction
(`handler_groups.go:68` is the single shared helper; the parity test pins both call sites).

### 8.2 Chat lane plug points (construction + advance + re-entry)

- Construction: `handler.go:783` → `handler_groups.go:76` → `store_group_rewrite.go:55` → `:89`
  `pickServableGroupMember` → the four-phase sweep → predicate at `:205/:229/:253`.
- Advance (chain, inside a member): `race_coordinator.go:858` → `fallbackAdvanceTargetLocked`
  `:1899` walks `c.models`. If the candidate list was filtered at construction, the advance is
  automatically in-scope; otherwise the advance must skip out-of-scope entries (the chain
  entries come from `memberRow.FallbackChain` — `store_group_rewrite.go:102`).
- Hedge: `race_coordinator.go:947` → `spawn(modelTypeSecond)` → always `c.models[0]` (`:435`) ⇒
  in-scope by construction if the head is legal.
- Re-entry: `handler.go:1213` (offer walk) and `handler.go:1231` (rebuild) — the offer walk must
  not accept an out-of-scope member, and the rebuild is the same authority ⇒ one predicate
  change covers both. Anthropic mirror: `handler_groups.go:466` and `:480`.

### 8.3 Anthropic lane plug points

- Construction: `handler_anthropic.go:472` (same helper ⇒ same predicate).
- Sequential walk: `handler_anthropic.go:548-767` (`for modelIndex, currentModel := range arc.modelList`).
- Re-entry: `handler_groups.go:466` / `:480`.

### 8.4 Failure semantics when ALL members are excluded

| Lane | Trigger | Status + body | Site |
|---|---|---|---|
| Chat | rewrite returns `FailedGroupID` (no candidate passed the predicate) | **HTTP 502**, code `group_unservable`, message `group '<id>' has no servable members` | decision at `store_group_rewrite.go:91`; envelope at `pkg/proxy/handler.go:783-798` (sendError `:797`); message builder `handler_groups.go:430-432` |
| Anthropic | same | **HTTP 502**, Anthropic envelope `{"type":"api_error","code":"group_unservable"}` | `pkg/proxy/handler_anthropic.go:472-489` (`:486-488`) |
| Chat re-entry exhaustion | offer walk finds no fresh member | returns `false` ⇒ caller falls through to `group_member_failed` + `group_fallback_exhausted` events (`handler.go:1157-1158`) then `handleRaceFailure(..., "all_models_failed")` (`:1161`) → the upstream/terminal error envelope | `handler.go:1223-1226` |
| Anthropic re-entry exhaustion | same | returns `false` ⇒ `break` (`handler_anthropic.go:766`) ⇒ "All models failed" (`:770`) + error envelope (`:773+`) | `handler_groups.go:476-479` |
| Decision group | `buildGroupPolicy` yields zero members (or all members out of scope) | **HTTP 502**, code `decision_all_members_failed`, message `all decision group members failed (N attempted)` | `pkg/decision/group.go:120-123` → `exhaustDecisionGroup` `:347-358` (`:355-357`) |

### 8.5 Traps the plan must handle explicitly

1. **`FailedGroupID` fires for FALLBACK-position groups too.** `store_group_rewrite.go:89-92`
   sets `res.FailedGroupID` for ANY entry index, so a group sitting in the fallback chain whose
   members are all out-of-scope currently 502s the WHOLE request. A scope filter must decide:
   drop the fallback entry (like the decision-lane refusal at `:82-88` does) vs fail the request.
2. **Order of gates.** The chat lane's group rewrite (`:783`) runs BEFORE the allow-list gate
   (`:832`), and the anthropic lane's rewrite (`:472`) runs BEFORE its allow-list gate (`:514`).
   A scope filter inside the rewrite therefore runs before the token allow-list is consulted —
   the two must be sequenced deliberately (the allow-list decides the CLIENT-REQUESTED string;
   the scope decides the MEMBER set).
3. **`allowed_groups` must resolve to CURRENT members at REQUEST time.** The group row's
   `Members` list is the storage truth (migration 031) but the member ROWS (enabled/kind) are
   read through the cache-backed `GetModelNative` — so "current" means "as of the last
   > **⚠️ SUPERSEDED IN PLACE (fix pass A, leader Ruling 5 / I14 — research record RETAINED as dated evidence):** the reach-membership mechanism above is superseded — reach membership reads the **in-memory models snapshot the cache layer maintains** (`GetModels()` / `currentModelRows()`), invalidated on group writes, and **`GetModelNative` is FORBIDDEN as a per-request reach source** (it is a live SELECT under `m.mu.RLock`). See `plan.md` §4.4 and §11.
   write-through / reconciler refresh" (points 4/5 above), bounded by the cache window. If the
   commission demands strict read-after-write membership, the scope resolution must bypass the
   cache (a direct `GetModelNative` on the manager) — a latency cost on a hot path.
4. **Group ids can never be claim targets** (house rule): `allowed_groups` entries must be
   matched against `group.ID`, never against a claimed/shadowed alias, and the member's own ID
   is what the scope must allow.
5. **Decision lane is a separate code path** (`pkg/decision`) with its own group loop
   (`group.go:96-343`) — it does NOT use `pkg/proxy`'s rewrite seam at all, so the scope
   predicate must be implemented there too (candidate list = `policy.order()`,
   `policy.go:110`, consumed at `group.go:118-119`).

---

## 9. Open questions for the approver

1. Should the scope predicate live in `pkg/store/database` (manager-level, next to
   `isServableGroupMemberBase`) or in `pkg/grouplb` (engine-level)? Manager-level keeps the
   engine API stable and preserves the "ordering-not-filtering" doctrine shape, but pays up to
   N offer round-trips per request.
2. Does `allowed_groups` gate the GROUP (all members) or the MEMBER set (a subset of members)?
   The commission says "allowed_groups must resolve to CURRENT group members" — i.e. a token
   naming a group may still be restricted per member by `allowed_models`.
3. For the decision lane: does the fix extend the token allow-list to the group branch
   (`pkg/decision/handler.go:323-326` returning before `:342`), or does the group branch get its
   own group-scoped gate?
4. What is the failure envelope when ALL members of a requested group are out of scope:
   403 `model_not_allowed` (auth-style, token scope) or 502 `group_unservable`
   (availability-style)? The two are semantically different and the FE/operator dashboards
   will branch on the code.
