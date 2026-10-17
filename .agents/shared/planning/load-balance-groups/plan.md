# Plan: Virtual Models as Load-Balancing Groups (`group` Model Kind)

> **Companion decision log:** `decisions.md` (written separately; mirrors §13 exactly — this plan's Recommended-default column is authoritative).
>
> **Inputs consumed:** verified explorer digest (two passes at `5b13388`) + `research-codebase.md` (cited as "research-codebase §N" for its own items; its §9.2 question numbers are NEVER conflated with this plan's Q-numbers). No source files written. No commits. No builds. **PLAN-ONLY commission** — zero source changes by this task.

---

## 1. Executive Summary

### Goal

Add a fourth model **kind** `"group"`: a **virtual model** that load-balances client chat traffic across multiple **(provider, model) targets** ("members") behind one stable model identity. Today the proxy load-balances only across *credentials of one model* (`pkg/credentiallb`); groups extend that to *multiple providers/models*. The client names a model (the group's ID, or a **claimed** existing model's ID) and receives normal OpenAI-format chat responses — transparent virtualization, zero client-facing surface change.

### Goals (in)

- `kind: "group"` rows are first-class: CRUD via the existing model surfaces, validated by `validateGroupRules` (G1–G8) + `validateGroupMembers`, stored via **one JSON membership column + one shadow-claim column** (migration 031).
- **Members are PROXY MODEL IDs (+weight)** — the group is ONE THIN LAYER before request distribution (user-decided Round 2 pivot, verbatim: *"quicker, more robust, less change"*): a pure-function rewrite of the modelList to the sticky-picked member, after which **the member's existing pipeline runs unchanged** (race, retry, credential LB, its own fallback chain). Multi-credential fan-out is the member's own credential LB, inherited natively. Two-level hashing cost accepted (group-salted + member-salted spaces).
- **ID-space shadowing**: a group may *claim* one existing chat model's ID (`shadow_model_id`); requests for the claimed ID route to the group and the claimed model is hidden from `/v1/models`; group deletion (or disable) drops the claim → the model reappears.
- **Sticky routing**: conversation-hash affinity over **member model IDs** (in-memory, 24h sliding TTL — **USER-DECIDED**), `ComputeConversationKey(groupID, …)` verbatim ⇒ group-salted keys; member failure at the GROUP layer ⇒ exclude-and-reselect on **terminal** member-pipeline failure only (max ONE re-entry, pre-first-byte); member removal ⇒ drop its bindings.
- New **`pkg/grouplb`** engine — a mechanical clone of `pkg/credentiallb` reduced to a **sticky weighted picker over model IDs** with zero credential awareness; `key.go` imported (not forked); per-group state lazy.
- Pipeline integration as a **pure-function member rewrite at modelList build** (OpenAI `handler_functions.go:149-159`; Anthropic mirror `:210-219`) — ONE shared helper + parity test; `rc.resolvedModel` stays the group row through guards/auth/group-salted conv-key/ultimate; per-attempt re-fetches run member-native; streaming pass-through inherited untouched.
- New FE **Group Models** tab (CRUD + cross-provider member editor), consistent with Text / ImgGen / Decision tabs; cache quadruple-delete discipline.
- Usage metered **both** under the group name and the served member — via a ~5-LOC **dedup gate** (member row only when `group-pick active && winner.GetModelID() != reqLog.Model`; day-1 = exactly one row), with the decision-style 5s detached meter timeout at the three existing metering sites.

### Non-goals (out)

- **Image-gen and decision paths** — untouched end-to-end (routes, guards, handlers). A group misrouted to `/v1/image_generation` or `/v1/decisions` keeps today's behavior of those guards (groups are neither `IsImageGen` nor `IsDecision`).
- **Client-facing wire changes** — no new endpoint, no new fields, no new auth surfaces. Transparency, not a new API.
- **DB persistence of sticky affinity** — in-memory only (**USER-DECIDED**); restart cold-start is documented behavior (§3.5), a log line, not engineered around.
- **Group-of-groups** — flat membership only (**USER-DECIDED**); G5 forbids nested groups.
- **No auto-creation, no bootstrap migration, no seed groups** — groups are purely manual, FE-created (user supplement; Am29). The FE one-click "wrap model X" flow is form pre-fill convenience only.
- **Health tracking, weighted strategies beyond what the engine clone gives free, exotic validation** — cut (**USER-DECIDED minimalism**); see §14.
- **`fallback_chain` / `secondary_upstream_model` / `peak_hour_enabled` / `release_stream_chunk_deadline` for groups** — rejected by G6 (v2 candidates).
- **Push, merge, build.** PLAN-ONLY; the executing worker respects `LOCAL ONLY — never push without explicit user instruction` (shared meta-KV; repo convention).

---

## 2. Grounding — How Existing Machinery Works

Anchors verified in this worktree (`feature/load-balance-groups` @ `5b13388`). Full detail lives in `research-codebase.md`; this section is the load-bearing subset the plan reasons over.

### 2.1 Model-kind system

- `pkg/models/config.go:93-118` — kind constants: `KindChat = "chat"` (:96), `KindImageGen = "image-gen"` (:104), `KindDecision = "decision"` (:117). The kind discriminator is nullable (`json:"kind,omitempty"` :364); `""` normalizes to chat.
- `ValidateKindRules(m, internalProvider)` gate at `config.go:176-191`; the **default arm** (:188) enumerates accepted kinds verbatim: `fmt.Errorf("model %s: unknown kind %q (expected %q, %q, or %q)", ...)` — must gain the literal. Per-kind rules: `validateImageGenRules` :194-224, `validateDecisionRules` D1–D9 :239-287.
- The `kind` DB column is free-form TEXT (migration 030) — **`"group"` is collision-CLEAN**; no kind migration needed.
- **Three enforcement sites** in lock-step: (a) shared seam `models.ValidateKindRules` (JSON path `config.go:971` + DB path `store.go:1719`), (b) inline POST mirror `pkg/ui/server.go:889-895` + kind arms, (c) inline PUT mirror `:1198-1205` + kind arms. `pkg/ui/kind_rules_parity_test.go` (`TestKindRules_Parity_AllThreeSitesInSync` :181, ruleCase matrix :188-267, 3 asserted sites :165-180) locks them.

### 2.2 Resolution & the ID-vs-Name reality (load-bearing)

- **Clients send `model: "<ID>"`, not names.** Resolution keys on `ModelConfig.ID` everywhere; `Name` is display-only. Auth allow-lists are ID-based (migration `024_convert_allowed_models_to_ids`; `IsModelAllowed` `pkg/auth/token.go:133-143`, exact-ID match).
- Duplicate **names** are legal (no `UNIQUE(name)`; `GetModelByName` nondeterministic — `querybuilder.go:215-238` has `WHERE name=?` no ORDER BY). Duplicate **IDs** are hard-blocked (PK + `ErrDuplicateModelID` `config.go:694-699` / `store.go:1358-1365`).
- ⇒ The commission's "name shadowing" is **ID-space shadowing**: a group claims another model's *ID*, not its display name (§3.4, Q1).
- ID-keyed resolution sites (all verified): `handler_functions.go:128` (+ fallback list :149-159); `handler.go:338` (chain-internal); `handler_anthropic.go:158/:418` (+ :210-219); `race_executor.go:55/:121/:177/:275`; `race_coordinator.go:1409/:1521`; `/v1/models` `handler.go:245-272` (kind-skip :257-261); FE GET `ui/server.go:748-821` (kind switch :758-789, chat arm :781, default arm :788); `ModelsManager.GetModel store.go:854` + `GetModelByName :936` + strict twins `:1191/:1197/:1205`; `ResolveInternalConfig(WithAffinity)` `store.go:2079-2097/:2170-2267` (+ cached :2269-2293); JSON twin `config.go:1012-1064`; auth gates `handler.go:565/:647`; ultimate `ultimatemodel/handler.go:746-768` (kind-blind, dispatches on `Internal`); modelscache `models.go:400/:427/:454`.
- **Unknown model is NOT an error** on `/v1/chat/completions` — it silently external-passthroughs (`modelList=[originalModel]`, `handler_functions.go:148-165`). A group must therefore be a **real ModelConfig row**, not a resolver-side fiction.

### 2.3 Credential LB engine (`pkg/credentiallb` — the clone source)

- `Engine{mu; models map[modelID]*modelState; ttl=24h sliding idle; sweepInt=5m; defaultCooldown=60s}` (`engine.go:35-65`). Per-model state: `credentials []CredentialRef{ID,Weight,Position}` + prefix-sum weighted selector (`selector.go:16-65`, O(log k), `weight<=0 ⇒ nil`) + `bindings map[convKey]*binding` + `cooldowns map[targetID]time.Time` + per-model RNG.
- Two-level locking E-1 (outer RLock → per-model optimistic → write-upgrade with re-verify, `engine.go:248-282`, `doc.go:42-55`); 5-min janitor (:123-181); `Stop` idempotent; n==1 fast path E-3 (:241-246).
- `ExcludeAndReselect` (`engine.go:609-719`): cooldown from `Retry-After` else 60s; n==1 ⇒ `ReselectNone` no cooldown (:633-635); rebind-only-if-current-matches (:654-679); all-cooling ⇒ `ReselectSoonestExpiry` (:689-699); fall-through policy lives with the caller.
- **Known limitation**: bindings/cooldowns key on `credentialID` only — no upstream-model dimension. A group engine keyed by credential would conflate members sharing a credential (a 429 on target A cools the credential backing B). §3.5 fixes this by keying on the member identity.
- Conversation key: `ComputeConversationKey(modelID, tokenID, firstUserMessage)` = sha256 64-hex (`key.go:40-43`); multimodal via sorted-key canonical JSON (:57-157); empty key ⇒ no binding. Computed at `handler.go:534-548` (OpenAI) / `handler_anthropic.go:296` (Anthropic). Restart = cold (bindings map empty at boot; `RebindFromStore` seeds selectors, not bindings).
- Known flake: `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` flakes ~1/5 under `-race` (pre-existing, project risk note) — the clone inherits the E-1 pattern and should copy the test with the flake's lesson in mind (§9).

**P4 standing-flake list (P2 deferred #4 addition — added 2026-10-17):**

The following tests are known-standing-flake or network-dependent and
are NOT counted as P4 regressions when they fail in the gate suite:

| Test | Reason | Scope |
|------|--------|-------|
| `pkg/credentiallb TestEngine_GetOrSelect_TOCTOU_CoolingReverify` | race-condition flake, ~1/5 under `-race` | existing credentiallb |
| `pkg/store TestStoreEngine_CloseLifecycle` | TempDir cleanup race (macOS, pre-existing) | existing store |
| `pkg/mcp handlers_api_test.go httpbin tests` | network-dependent on httpbin.org | existing mcp |
| `pkg/store TestStoreEngine_PeakHourParity` | **newly added by P4.5 P2 deferred #4** — pre-existing flake of similar shape; race in temp-table substitution; harmless when it fires | existing store |

The release process for P4 surface treats a green `go test ./... -count=1 x2` run (with the above table named) as the exit gate. Any failure outside this table is a regression.

### 2.4 Chat pipeline (both protocols)

- **OpenAI** `/v1/chat/completions`: `HandleChatCompletions` `handler.go:393` → `initRequestContext` (`handler_functions.go:62`, 10MB cap, `reqLog.Model` = client string) → `GetModel` :128 → misroute guards (imggen :456-462, decision :474-480) → auth `IsModelAllowed` :555-604 (fail-closed :583-602) → conv key :534-548 → ultimate checks :626-953 → race handoff :1019-1025 → `executeRequest` `race_executor.go:93` (re-fetches `GetModel` per attempt) → internal path: `ResolveInternalConfigWithAffinity` `race_executor.go:278` (reselected) / :303 (normal) → `internalModel = resolved.InternalModel` :333 → `convertToProviderRequest` :381 → upstream.
- **Internal/external dispatch**: `modelConfig.Internal` gates the internal path at ~11 predicates — `race_executor.go:123/:178`, `handler.go:332/:339/:534`, `handler_anthropic.go:277/:419`, `handler_functions.go:174`, `internal_handler.go:190`, `race_coordinator.go:1410` (which additionally requires `len(Credentials) <= 1`), resolver gate `store.go:2172` (+ `:2081`, cached `:2293`). **A group row must carry `Internal=true`** or every one of these treats it as raw-passthrough (§3.6, Q13).
- **Race/fallback**: attempts = main + fallback chain (cap 1, `config.go:838-839`); four row types; credential axis ≻ model axis; rate-limit ⇒ `credFailoverPlanLocked` → `engine.ExcludeAndReselect` (`race_coordinator.go:1465`, wiring :344-351).
- **Anthropic** `/v1/messages`: separate sequential fallback loop `handler_anthropic.go:305-414` (no race coordinator); per-iteration `GetModel` :418 + `ResolveInternalConfigWithAffinity` :441; only a decision misroute guard (:195-207); **response `model` already rewritten to the client-requested name** (`translator/response.go:38` non-stream; `incremental_stream.go:458` message_start); no metering on this path (pre-existing).

### 2.5 Response identity asymmetry (load-bearing for Q5)

- OpenAI path **echoes the upstream `model` field verbatim** (stream relay `handler.go:1172-1184`; non-stream `finalBody` :1587) — a multi-target group would leak varying upstream model names. Anthropic masks to the client name.
- `RequestMetadata{ClientModel, UpstreamModel}` exists in `adapter_openai.go:53-58` but is populated **only** by `ParseRequest`, which has **zero production callers** (dead seam — `handler_functions.go:88` calls only `ToStoreMessages`; handler/race_executor never call it) — corrected per arch R1 Am7; adapter-seam masking is priced out for v1. `WriteStreamEvent` is pure passthrough (:100-106); the streamID normalization cache (`handler_helpers.go:92-95`, "cached from first chunk to maintain consistency across retries/fallbacks") is the in-`requestContext` masking template.

### 2.6 Metering

- Success-only, 3 sites in `pkg/proxy/handler.go` — **anchors corrected per arch R1 Am11** (re-verified in source): `:908-934` (**ultimate re-execution branch** inside `HandleChatCompletions` — the `model := ultimateModelID` context at :888; NOT "stream" as first drafted), `:1446-1472` (true stream, inside `streamResult` :1139), `:1656-1682` (non-stream, inside `handleNonStreamResult` :1538). Each block reads `rc.reqLog.Model` (the client-requested name), `rc.tokenID`, `rc.reqLog.Usage` — **`resolved` (`ResolvedCredential`) is NOT in scope at any of the three** (re-verified), **the served member is stamped at rewrite (Am25): `rc.resolvedModel` (the GROUP row) and `winner.GetModelID()` (the MEMBER row, via the post-rewrite per-attempt re-fetch) are DIFFERENT objects at these sites** -- the Round-1 phrasing was pre-pivot. Bare `context.Background()`, no timeout — the decision commission's 5s detached pattern (`decision/handler.go:89-90`) is adopted on these sites (Q9).

### 2.7 Frontend

- 10 fixed tabs, `TabType` union `SettingsPage.tsx:52`, pure `useState` :82, no URL routing; BE `?kind=` query param (`server.go:758-789`; `useApi.ts:374/:477`).
- Cache keys `'models'` / `'imggen-models'` / `'decision-models'` (`useApi.ts:271/:373/:476`) with **explicit key deletes** on all 9 mutation sites — NEVER `deleteByPrefix` (misses in-flight promises). Mutation tests pin the exact key set (`hooks/__tests__/useModels.test.ts:41-43`, `useImgGenModels.test.ts:107-109/138-140/167-169`, `useDecisionModels.test.ts:42-44`).
- Editor precedent `components/config/MultiCredentialEditor.tsx` (225L): `CredentialRef` rows (credential_id + weight, row 0 Primary), MIN 1, MAX 16 (`isOptionDisabled` :70-95 enforces dup + **same-provider** — groups must lift the same-provider half). Form precedent `components/config/DecisionModelForm.tsx` (527L): payload stamping, provider-switch clears credentials, submit race guard, aria errors.
- Chat-tab filter `components/config/ModelsTab.tsx:162` verbatim: `models.filter((m) => m.kind !== 'image-gen' && m.kind !== 'decision')` — must gain `&& m.kind !== 'group'`.
- Component + hook + token test locations verified: `src/components/config/__tests__/`, `src/hooks/__tests__/`, `src/components/tokens/__tests__/TokenForm.test.tsx`, `src/components/__tests__/SettingsPage.tabs.test.tsx`. vitest + happy-dom (no chromium); tsc baseline **26 errors / 11 files** canonical; embedding discipline (§6.6).

---

## 3. Backend Design

### 3.1 Kind-system extension (7 mechanical sites)

1. `pkg/models/config.go:93-118` — add `KindGroup = "group"` to the constants block (with a doc comment: flat member list, no fallback/secondary/peak/deadline, shadow claim optional, `ExcludeFromUltimateSwitching` forced true).
2. Add `IsGroup() bool { return m.Kind == KindGroup }` helper next to `IsImageGen()`/`IsDecision()`.
3. `ValidateKindRules` switch (:176-191): add `case KindGroup: return validateGroupRules(m, internalProvider)`; extend the default-arm enumeration string :188 to four expected values (updated wording: `expected %q, %q, %q, or %q`).
4. `validateGroupRules` + `validateGroupMembers` — §5 (new file `pkg/models/group.go`).
5. FE-API kind arm: `pkg/ui/server.go` GET `?kind=group` (:758-789) + **chat arm now excludes group** (:781 + default arm :788) + POST/PUT inline mirrors + write-time ultimate gate parity (:1058-1065 / :1327-1332).
6. Parity test extension — §5.3.
7. `/v1/models` (`pkg/proxy/handler.go:245-272`): groups are **NOT excluded** (they are chat-servable — a client must be able to discover the group name); the second-pass shadow-hide is §3.4.

**Misroute guards: untouched.** A group is neither `IsImageGen` nor `IsDecision`, so `handler.go:456-462/:474-480` pass it through — correct, since groups are served by the normal chat paths. No new guard, no new route.

### 3.2 Group data model & membership *(Round 2 pivot — Am21)*

*(Round 1 §3.2 — inline `(credential_ref, upstream_model, weight)` triples, the composite member key, the `|` credential-ID ban (Am2), the N-members-per-target ops pattern and rotation/API-key notes (Q18, Am12) — **SUPERSEDED by the user-decided pivot**: members are PROXY MODEL IDs. Round-1 text retained in git history @ `c6e0fd5`.)*

`ModelConfig` gains two fields (both backends share the struct):

```go
Members       []GroupMember `json:"members,omitempty"`        // kind==group only
ShadowModelID string        `json:"shadow_model_id,omitempty"` // ID-space claim
```

```go
// pkg/models/group.go (new)
type GroupMember struct {
    ModelID string  `json:"model_id"`  // an EXISTING text-kind model's ID
    Weight  float64 `json:"weight"`
}
```

- **Member identity = the member model's own ID.** Bindings, cooldowns, dedup gates, and events key on it — no composite keys, no `|` gate (Round-1 Am2 evaporates).
- **Thin layer (user's words: "quicker, more robust, less change"):** the group does ONE thing — rewrite the modelList to the sticky-picked member; everything downstream is the member's existing pipeline (race, retry, credential LB, its own fallback chain). Multi-credential fan-out for a member is that member's own credential LB — **inherited natively** (Round-1 Q18 dissolves; see §13).
- **Two-level hashing (accepted cost, user-approved):** group-salted conv key (`ComputeConversationKey(groupID, …)`) picks the member; the member's cred-LB then binds `(memberID, group-salted-key)`. Same conversation ⇒ same key ⇒ same cred binding; a member shared by two groups gets distinct group-salted spaces; direct member requests use the member-salted space (Am31 documents all three).
- **Q13 — `Internal=true` on the group row (STAYS, re-verified by both Round-2 workers):** the ~11 `.Internal` dispatch predicates (§2.4) + the resolver gate fire on the group row **before** the rewrite; `Internal=false` would external-passthrough it (`handler.go:332/:339`). G1 enforces at write time.

### 3.3 Storage — JSON columns (Q8, recommendation shape (a) followed)

One migration, **031**, adding two nullable columns to `models` in both dialects:

- `members_json TEXT NULL` — the `[]GroupMember` array (house style: `credentials_json`, migration 028).
- `shadow_model_id TEXT NULL` — the claim. (Go-computed-column precedent: `store.go:1370-1374` / `:1457-1460`.)

Mechanical touch list (corrected per arch R1 Am8; re-verified counts):

| Area | Sites | Change |
|---|---|---|
| SELECT column lists | querybuilder SELECT builders ×2 dialects **+ two inline SELECTs that bypass querybuilder entirely: `store.go:857-873` (`GetModel`) and `:939-955` (`GetModelByName`)** | columns **20→22** (after `coalesce(kind,'')`) |
| Scan sites | `scanModels` def :769; full-scan callers :1022/:1034/:2020; in-row scans `GetModel` ~:877, `GetModelByName` ~:959, `getModelStrict` ~:1154, `GetModelsStrict` ~:1236; dummy probes :1362/:1449/:1509 | +2 scan targets each (**22-col rows** — `scanModels`/`dbModelRow` go 20→22) |
| Write bindings | INSERT `querybuilder.go:94-124` + exec `store.go:1380-1400`; UPDATE `querybuilder.go:135-177` + exec `store.go:1466-1486` | **19→21 args** (placeholders only — this was the plan's correct count) |
| Struct | `dbModelRow` `store.go:467` (definition line — approver R2) | +2 fields (20→22) |
| Column-count pins | `querybuilder_test.go:239/:262/:275` (assert 19) | bump to 21 |

**Exact builder-string count is re-derived at P1.4** — secondary docs disagree (8/12/16/18); the source enumeration is authoritative and the arity/placeholder test pins are the real lock (arch Open Question 3). **No M-1 shadow-write dance (arch R1 Am16):** both 031 columns are purely additive — `json.Marshal` on INSERT/UPDATE suffices; the `credentials_json` dual-write pattern does **NOT** apply (the store.go:1370-1374/:1457-1460 citation stays as style precedent only).

Prefer option (a) over a separate `group_members` table: the table profile needs ~6+ new querybuilder strings ×2 dialects, a new scan loop, manager CRUD + interface growth, hand-built integrity in a zero-FK schema, a SECOND implementation for the JSON backend, a new cache-invalidation trigger, and violates house style. Option (a) keeps `OnModelChanged` and modelscache invalidation untouched (single-row storage) and is precedent-covered at ~30 mechanical sites.

### 3.4 Resolution & shadowing mechanics (Q1 — THE design center)

**Mechanism: single-ID claim (`shadow_model_id`), not an alias array.** Minimalism: one column, one G-rule set, zero array scanning; multiple aliases per group → v2 candidate (§14).

**Claim index.** `ModelsManager` gains `claims map[shadowedID]groupID`, rebuilt wherever the in-memory model state is rebuilt (the same `OnModelChanged` hook that feeds `credentiallb`), plus on every group Add/Update/Delete. JSON-backend twin (`config.go` `ModelsConfig`) builds the equivalent index from the loaded slice.

**Resolution order** — implemented ONCE, in `ModelsManager.GetModel` (`store.go:854`), because §2.2's inventory shows every client-path site funnels through it:

1. `claims[id]` exists **and** the owning group exists **and** is enabled → return the **group row**.
2. Else → normal ID lookup (which also finds group rows by their own IDs).

Consequences, all deliberate:

- **Admin twins bypass claims**: `getModelStrict` (`store.go:1191/:1197/:1205`) is the Update/Delete fetch path — it must keep hitting the REAL row so operators can still edit/delete a shadowed model by ID. No change needed there; the rule is: never touch the strict twins.
- **`GetModelNative(modelID)` (Am22, 🔴 L1 hard rule):** a new manager-level direct-ID lookup with strict-twin claim-BYPASS semantics on a non-admin surface. The Round-2 rewrite MUST use it for member refs — **never claims-first `GetModel`** (a self-shadow group like day-1 `coding[coding]` would otherwise infinite-loop). Identity rule: *"member refs resolve by direct ID (row identity); claims apply to client-facing resolution only."* Under it: **self-shadow is LEGAL** (canonical onboarding); a member claimed by another enabled group is mechanically safe — **allow + document**; group-as-member is forbidden (flat, G5).
- **`GetModelByName` untouched** — claims are ID-space; names stay display-only.
- **Q11 block-at-delete REWORKED (Am27):** the delete-time guard moves to **`DeleteModel`** — refuse while the model ID is a member of any group (error naming the group); the Round-1 credential-side scan disappears (groups carry no credentials). ~20 LOC + test.
- **Group deletion** drops the row → the claim index entry dies with it → the shadowed model reappears in `/v1/models` and resolves normally again. No tombstone machinery.
- **Disabled group ⇒ inactive claim** (§3.8): a disabled group must not hijack traffic it isn't serving.
- **Shadow target deleted first**: tolerated, harmless — the claim keeps redirecting that (now nonexistent) ID to the group, which is a defensible takeover semantic; `/v1/models` has nothing to hide (the row is gone). No special machinery; documented behavior.
- **`/v1/models`** (`handler.go:245-272`): keep the kind-skip :257-261 as-is (groups pass); add a second pass filtering out IDs present in the (enabled-group) claim index. Fixture precedent: `TestHandleModels_FilterImageGen` (`pkg/proxy/handle_models_imggen_test.go:86-124` + :134/:170/:196) and the FE-side chat-arm-leak regression `pkg/ui/handlers_models_kind_test.go` (:283, `DefaultArm` :218). Hide mechanism: an **interface-constrained scan** — the listing pass consults the claims index to skip claimed IDs (no per-row model-kind branching) (Approver R2 P5).
- **modelscache** — package `pkg/modelscache` (path corrected per arch R1 Am4). `GetModel` caches per-requested-ID (`models.go:400/:427/:454`) **and writes negative entries** (`negByID`, :375) under `ModelsNegTTL` (60s default, :55-56); `AddModel`/`UpdateModel`/`RemoveModel` (:643/:665/:687) evict **only by the row's own ID**. So a claim write (group create/update/delete; shadow change) must run a **dedicated claim-eviction hook**: evict `modelsByID[old shadow]` + `modelsByID[new shadow]` **and** `negByID[old shadow]` + `negByID[new shadow]` — otherwise a cached chat-row or a 60s negative entry for the shadowed ID **bypasses the manager's claim logic** entirely. Claim authority vs `CachedModelsConfig.GetModel` ordering stated: the eviction hook runs synchronously in the same `OnModelChanged` extension as the claim-index rebuild (P2.2), before any subsequent resolve can read a stale entry.
- **Auth (Q1 tail)**: `IsModelAllowed` (`token.go:133-143`) evaluates the **client-requested ID string** and is untouched. A token whose `allowed_models` lists the shadowed ID reaches the group through that alias (the string matches); a token listing only the group's own ID reaches it only via the group ID; requests for the shadowed ID from a token that doesn't list it are denied fail-closed exactly as today. Auth is orthogonal to kind — no `AllowedGroups` field (research-codebase §9.2 item 2's simplest invariant adopted).
- **JSON backend** (`config.go:1012-1064`): its `ResolveInternalConfig` twin is **UNTOUCHED under the pivot** (group rows never reach the resolver - Am21); its GetModel equivalent gains the claim-first check + the `GetModelNative` companion (Am22). Both backends, one design.

### 3.5 LB engine: `pkg/grouplb` — sticky weighted picker over MODEL IDs *(Round 2 pivot)*

*(Round 1 §3.5 — member-key `credentialID+"|"+upstreamModel`, the `|` credential-ID ban (Am2), and credential-aware cooldowns — **EVAPORATED by Am21**: keys are plain member model IDs; Round-1 text retained in git history @ `c6e0fd5`.)*

`pkg/grouplb` is a mechanical clone of `pkg/credentiallb` reduced to a **sticky weighted picker over model IDs** with **ZERO credential awareness**:

- `MemberRef{ModelID, Weight, Position}`; bindings/cooldowns keyed by the **member model ID** (a real `models` row ID — no composite keys, no escaping concerns).
- What carries verbatim from the clone: prefix-sum weighted selector (O(log k), `weight<=0 ⇒ nil`); E-1 two-level locking with re-verify; E-3 n==1 fast path (day-1 latency); 24h sliding TTL + 5-min janitor + 60s default cooldown (**USER-DECIDED**, Q3); `ExcludeAndReselect(groupID, convKey, memberID)` semantics (rebind-only-if-bound-to-failed, soonest-expiry fall-through); `OnGroupChanged(groupID, survivors)` survivor-filter dropping removed-member bindings (mirrors `credentiallb` `engine.go:382-388`/`:423-451` patterns); per-group state **lazily instantiated** on first `GetOrSelect`, **NO boot `RebindFromStore` walk** (Am15); **`testhooks.go` cloned 1:1 — all 7 seams** (Am14).
- **`key.go` is IMPORTED from `pkg/credentiallb`, not forked** (`ComputeConversationKey` — planner call, ~−150 LOC; Am31). `ComputeConversationKey(groupID, tokenID, firstUserMessage)` — verbatim reuse with the group ID ⇒ **group-salted** keys; empty key ⇒ fresh pick.
- **Anti-pattern guard (Am31):** `pkg/grouplb` must keep ZERO `pkg/models`-credential / provider imports — a grep guard in `grouplb_test.go` pins it.
- Restart cold-start: bindings empty at boot; **single startup log line** documents the loss; an in-flight conversation re-rolls to a fresh member on its next request (Q4 USER-DECIDED; Review R1 N11).
- New-file list: `doc.go`, `engine.go`, `selector.go`, `binding.go`, `events.go`, `testhooks.go` (**no `key.go`** — imported).

### 3.6 Pipeline integration — the modelList REWRITE *(Round 2 pivot — Am21)*

*(Round 1 §3.6.1 resolver kind-branch + cached-variant placement (Am10), §3.6.2's `race_coordinator.go:1465` group branch (P2.6), and §3.6.3's `internal_handler.go` group-aware failover branch (Round-1 🔴 Am1) — **ALL SUPERSEDED/MOOT by Am21**: group rows never reach the resolver or the internal failover path; the InternalHandler sees the member row with its own credentials. Round-1 text retained in git history @ `c6e0fd5`.)*

#### 3.6.1 THE rewrite — pure function at modelList build (Am21, 🔴 core mechanism)

The group is **one thin layer before request distribution** (user's architecture, verbatim: *"client → proxy → GROUP (members: models …) → hash-bound weighted pick of ONE member MODEL → that model's EXISTING pipeline runs UNCHANGED (race, retry, credential LB, fallback) → upstream"*). Implementation: a **pure-function member rewrite applied to the modelList at build time** —

- **Siting (verified):** OpenAI `handler_functions.go:149-159` (inside `initRequestContext`, called at `handler.go:399`) and the Anthropic mirror `handler_anthropic.go:210-219` — both build `modelList = [resolvedModel.ID] + FallbackChain` with identical code. For every entry `e` whose **direct-ID** row `IsGroup()`, replace `e` with the sticky-picked member's ID; **the member's own FallbackChain follows it** (execution list = `[member.ID] + member.FallbackChain`).
- **`rc.resolvedModel` STAYS the group row** through misroute guards (`:456/:474`), conversation-key derivation (`:534-548` ⇒ **group-salted** key), auth (`:555-604`, client string — group-scoped), ultimate checks (`:626-953`, G7 evaluated on the group row), and race handoff (`:1025` passes the rewritten list). Per-attempt re-fetches (`race_executor.go:121/:178`; Anthropic `:418`) hit the **member row natively**.
- **ONE shared manager-level helper** (on `ModelsManager`) called by both protocol sites + a **parity test** locking them in sync (Worker E's shape, Worker D's siting; Worker E's resolver-site alternative REJECTED — it would member-salt the conv key and mis-scope ultimate).
- **What evaporates** (verified, R2.1): resolver kind-branch + cached variant (Am10 dead); `internal_handler.go:240/:244` group branch (Am1 moot); `race_coordinator.go:1465` branch (P2.6 moot); member-key `|` construction + credential-ID ban (Am2 evaporates); metering resolved-identity plumbing (Am3 → Am25); G5 provider-match divergence (moot — groups carry no credentials); Q18 N-members-per-target (dissolves — multi-credential fan-out is the member's own credential LB, inherited natively).

#### 3.6.2 OpenAI race path

- The rewritten list feeds the race handoff (:1019-1025) unchanged; attempts re-fetch the **member** row per attempt (`race_executor.go:55/:121/:177/:275`) and run the member's full existing pipeline — its own credential LB, its own fallback chain, its own race budget. Member-internal 429s are absorbed by the member's cred-LB and **never bubble** to the group layer.
- **Group-level failure semantics (Am24):** group-level `ExcludeAndReselect` fires **ONLY on terminal member-pipeline failure** — surfacing as `handleRaceFailure` (definition `handler.go:1086`, call site `handler.go:1078`; winner==nil). On terminal failure: `grouplb.ExcludeAndReselect(groupID, convKey, memberID)` + **max ONE re-entry per request** (rebuild the modelList via the rewrite, resume execution); re-entry does not chain — a second terminal failure is a real failure. **Pre-first-byte only.**
- Streaming inherits today's winner-locked behavior (Review R1 N9): once the winning member streams its first byte, no mid-stream member change — the winner is locked for the life of the response.

#### 3.6.3 Anthropic sequential path

*(Round-1 Am1 — the `internal_handler.go:240` guard lift + grouplb failover + member-shaped retry — **MOOT by Am21**: the InternalHandler executes the member row natively with its own credentials; the Round-1 🔴 defect class cannot occur.)*

- The mirror rewrite at `handler_anthropic.go:210-219` serves groups identically; the sequential loop's per-iteration `GetModel` (:418) + resolution (:441) run **member-native**.
- **Am24 Anthropic side:** group-level reselect fires only at the "all models failed" exit (`:395-412`), same max-ONE/pre-first-byte/no-chaining rule (the `headersSent` lockout at `:332-334` commits streaming to the picked member).
- Response `model` already rewritten to the client name (`translator/response.go:38`; `incremental_stream.go:458`) — masking parity free. `/v1/messages` still meters nothing (§3.7, Am13). imggen-guard gap at `:192-194` remains out-of-scope as today.

#### 3.6.4 Ultimate switching (Q6 — verified on the group row PRE-rewrite)

G7 forces `ExcludeFromUltimateSwitching == true` for groups (D9 precedent `config.go:283-285`; UI write-gates mirrored). The runtime gate (`pkg/proxy/handler.go:696`) and trigger milestones evaluate on `rc.resolvedModel` = the **group row** ⇒ G7 fully shields group traffic; a member's own ultimate eligibility applies **only on direct member requests**. `X-Force-Ultimate-Model` residual unchanged (§11 R9). **Round-1 Am5 (ultimate-stops-while-shadowed) still applies** — while X is claimed, claims-first `GetModel(X)` returns the group row (G7-excluded), so X's ultimate switching halts until the claim drops (**Q17 — pending user approval**).

#### 3.6.5 Cross-kind interactions

- **Chat→group fallback entries: NATIVE under the pivot (Q16 posture preserved with zero resolver contact)** — the rewrite applies uniformly to every list entry **including fallback entries**, so a chat model falling back to a group gets the group's member-pick + the member's own chain, for free. Group→group fallback remains impossible (G6 — groups have no chain; §14).
- *(Round-1 §3.6.5 cross-protocol failover asymmetry sentence — **SUPERSEDED**: under Am24 both protocols share the same terminal-only/max-ONE group-level rule; the Round-1 `:1410` divergence described a resolver-seam mechanism that no longer exists.)*
- **Image-gen / decision paths:** untouched (§1 non-goals). Groups are neither kind; guards neither fire nor need to.

### 3.7 Metering (Q9 — both rows via the **L2 dedup gate**, Am25)

*(Round-1 Am3 — winner-based `GetResolved()`/rc-stamp plumbing — **REPLACED by Am25**: the winner row IS the member row natively (`winner.GetModelID()` = member ID), so the plumbing problem dissolves into a ~5-LOC dedup gate. Round-1 Am11 anchor corrections and ultimate-site exclusion CARRY.)*

- **Stamp at rewrite:** when the picked `memberID != reqLog.Model` (i.e. the group rewrote the request), stamp the served member into the request log (`FallbackUsed[0]` or a new `RouteModel` field) — Am11's three metering sites stay where they are (`:908-934` ultimate branch / `:1446-1472` stream / `:1656-1682` non-stream). **`group-pick active`** = the rewrite fired for this request (the served-member stamp exists / `memberID != reqLog.Model`) — the gate NEVER counts a member row for non-group traffic (Execution-time Am1: an implementer following the bare predicate reintroduced non-group fallback double-metering).
- **Dedup gate:** emit the member usage row **ONLY when `group-pick active && winner.GetModelID() != reqLog.Model`** — i.e. only when a group actually rewrote the request. **Day-1 (`coding[coding]`, single member with the same ID) = exactly ONE usage row, unchanged** (L2).
- Existing group-name row: `IncrementModelUsage(clientRequestedName)` — unchanged at all three sites, **including the ultimate re-execution branch** (excluded from member-row metering per Am11 — the ultimate target was served, not a member).
- 5s detached-context timeout (decision pattern, `decision/handler.go:89-90`) at the three sites — carried from Round 1 (§12 A12). Token-level `Increment` unchanged. `writeMu` serialization unaffected.
- **`/v1/messages` meters nothing** (Am13, pre-existing gap — unchanged).
- Events: `model_member_selected` with payload **`{group_id, member model_id, upstream_provider, upstream_model}`** (Q15 payload adjusted for the pivot), fired at the existing W-1 publish site, `newlyBound`-only; non-group traffic keeps `model_credential_selected` unchanged.

### 3.8 Semantics corner (Q12)

- **Enable/disable**: `Enabled=false` group ⇒ claim inactive (shadowed model reappears and resolves normally) AND group absent from `/v1/models` (existing enabled-filter `:251`). Direct-named disabled group resolves exactly as a disabled chat model does today (no new gate — parity).
- **Empty group at runtime**: impossible via API (G2 enforces >=1 member at every write; Am27 blocks model deletion that would orphan a member) and via config file (`ModelsConfig.Validate` runs the same G-rules). Defense-in-depth: **at the rewrite site (Am28)** an empty-at-runtime group yields the normal resolve-failure envelope - never a panic (no resolver kind-branch exists post-pivot).
- **Runtime stale-member defense (Am28):** at rewrite, re-validate the picked member (exists / chat-kind / not group / enabled); on stale: **skip-and-reselect** within the group; an empty-at-runtime group → resolve-failure envelope (defense-in-depth posture above). Covers a member disabled/deleted between group-write and request.
- **Group edits**: ID is immutable identity; member/weight/claim edits take effect immediately (claim index + `OnGroupChanged` + cache invalidation ride the write path).
- **Dual-claim edges (arch R1 Am20):** G8's uniqueness scan blocks two *enabled* groups claiming one ID at write time; two *disabled* claims are inert (no index entry — claims activate only on enabled groups); enable-ordering resolves through the same scan. The single-valued `claims` map tolerates inconsistency only via non-API writes (hand-edited config file) — assumed away, one line.
- **Claiming a DISABLED chat model's ID (Approver R2 P4):** the claim is legal (G8 checks existence + kind, not enabled state). A disabled target is already hidden from `/v1/models` (enabled-listing), so the claim is inert from the listing view while still owning the ID-space; direct requests for the disabled ID route to the group (claim-first, §3.4) — harmless, since the group itself is servable. Wording note: direct resolution of a disabled chat model is NOT refused at HEAD (`GetModel` has no enabled filter, verified) — the claim merely redirects it to a servable row, which is the defensible reading.

---

## 4. Wire Contract

**The external API is UNCHANGED.** Transparency statement, binding for implementation:

- No new routes. No new request/response fields. No new auth surfaces. No client migration of any kind.
- A client that today sends `model: "<chat-model-ID>"` (or a claimed ID) sends the same string after this feature; groups respond with normal OpenAI chat wire (streaming and non-streaming), including tool calls, reasoning fields, usage — byte-shapes identical to any internal chat model's responses.
- **Q5 answered (recommended default) — mask, don't leak — L3 verdict (Am26): mask to the CLIENT-REQUESTED STRING uniformly**: group responses have their `model` field rewritten to the **client-requested name** (the group ID or claimed ID), restoring single-model appearance; multi-target groups otherwise leak varying upstream names (§2.5). Implementation, group-kind-only: **Day-1 byte-identity condition (precise):** the manual `coding[coding]` rollout is fully byte-identical **iff `internal_model == id`** (alias-style models); where they differ, exactly the `model` field differs on day 1 — by design. Round-1 Am6 per-SSE-event buffering carries unchanged.
  - Non-stream: patch the top-level `model` key of `finalBody` before write (`handler.go:1587` — the body is already parsed on this path for content/usage extraction). Error envelopes carry no top-level `model` — no patch needed on errors.
  - Stream (**buffered per complete SSE event — arch R1 Am6**): the relay writes **raw read-buffer chunks** (`buffer.GetChunksFrom`, `handler.go:1172-1184`); a JSON object can straddle chunk boundaries, so a per-chunk patch fails open on straddled events ⇒ an **intermittent leak**. Instead, buffer/patch **per complete `data:` event** (reusing the existing per-event parsing machinery adjacent at :1182), then write. Group-kind-only (`if rc.resolvedModel.IsGroup()` gate at the call site), so zero cost and zero risk for existing traffic. A patch failure falls back to verbatim write (log + continue), never drops the stream.
  - Anthropic: already masks (§2.4) — parity achieved with zero code.
  - Alternative (accept the leak) rejected: breaks the transparency goal for any client that pins the model field. A diagnostic toggle for verbatim passthrough is a v2 candidate (§14).
- Error envelopes unchanged: public `/v1/*` failures use the OpenAI envelope family (`models.NewOpenAIError`); the Anthropic path the Anthropic family — exactly as the chat paths behave today.
- `/v1/models`: groups listed (clients must discover them); claimed models hidden while the claiming group is enabled (§3.4).

---

## 5. Group Config & Validation

### 5.1 `validateGroupRules` (G1–G8) — `pkg/models/group.go` (new)

| # | Rule | Rationale / precedent |
|---|---|---|
| G1 | `Internal == true` | Routes through the internal-dispatch predicates (§3.2, Q13). **Diverges from research-codebase §9.2 item 13's `Internal=false` default — rejected on the ~11-predicate evidence.** |
| G2 | `len(Members) >= 1`; member count cap 16 (reusing the same ceiling) | ≥1 keeps the group live at every write (≥2 rejected as paternalism; a one-member group is a valid staging state and the engine's n==1 fast path serves it). |
| G3 | Every member: `ModelID` references an **existing TEXT-kind model** (direct-ID existence check); `Weight > 0` (mirrors `store.go:1615-1617`) — **no credential refs** (Round-1 Am2 `\|` gate evaporates with the composite key) | Basic integrity; no invented weight ceiling. |
| G4 | No duplicate members (same `ModelID`) | Member-key uniqueness; mirrors `store.go:1618-1621`. |
| G5 | Members may reference models of ANY provider mix — and **group-as-member is FORBIDDEN** (flat, **USER-DECIDED**). The Round-1 provider-match divergence is **MOOT**: groups carry no credentials, and `validateModelAgainstCredentials` is bypassed for group rows entirely | The feature is now "any chat models"; flat rule enforced at the member-kind check. |
| G6 | `FallbackChain` empty; `SecondaryUpstreamModel == ""`; `PeakHourEnabled == false`; `ReleaseStreamChunkDeadline == 0` | imggen/decision posture; makes group→group fallback impossible (research-codebase §9.2 item 14 denied for v1 → §14). Cut features are v2 candidates. |
| G7 | `ExcludeFromUltimateSwitching == true` (explicit, not defaulted) | D9 precedent (`config.go:283-285`) — the image-gen doc/code drift is NOT inherited. |
| G8 | If `ShadowModelID != ""`: target exists, is **chat-kind**, is not `KindGroup` (flat — **USER-DECIDED**), is not the group's own ID, and **no other enabled group claims the same ID** (uniqueness scan) | Claim integrity; ambiguity-proof. **Self-shadow (claiming the group's own single member) is LEGAL and canonical day-1 onboarding** (`coding[coding])`); claiming a member of the same or another group is allowed and harmless (client requests for it route to the group; member resolution inside the engine is by explicit key and never consults claims) — documented, not policed. |

Enforcement: `validateGroupRules` runs from the shared seam `models.ValidateKindRules` (JSON `config.go:971` + DB `store.go:1719`); the DB write path calls `validateGroupMembers` instead of `validateModelAgainstCredentials` for `kind==group` rows; the G8 uniqueness scan needs the model collection (available at both write paths and both UI mirrors). **Am23 ship-gate pin (🔴):** the kind-routing — `kind==group` rows run `validateGroupMembers` INSTEAD of `validateModelAgainstCredentials` — is load-bearing and must branch **BEFORE the Internal gate**: `validateModelAgainstCredentials` (`store.go:1597-1604`, verified verbatim) rejects `Internal=true, Credentials=[]` — exactly the group-row shape. **Am23 branch placement (recommended): inside `validateBeforeWrite` (`store.go:1736`) - TWO production call sites (AddModel :1351 + UpdateModel :1438), one shared chokepoint covering both writes**; pinned by a save-path test (a group row must save cleanly through it). Latent gap: bulk `ModelsManager.Validate()` (:1561, zero non-test callers today) must route through the same branch if ever wired.

### 5.2 UI mirrors (inline-duplicate pattern)

POST `pkg/ui/server.go:889-895` whitelist + a group kind arm (~:1066, after decision's :979-1066); PUT `:1198-1205` whitelist + group arm (after decision's, ending ~:1333); each mirror enforces G1–G8 inline (400 + house `{"error":"..."}`), stamps the G7 write-gate, and runs the G8 uniqueness scan. Mirrors are intentionally duplicated per T1.2.3 design — the parity test (§5.3) is the drift lock.

### 5.3 Parity-test extension

`pkg/ui/kind_rules_parity_test.go`: extend the `ruleCase` matrix (:188-267) with G1–G8 cases (violations + a valid group fixture); add `validateGroupRulesCaller` to the three asserted sites (:165-180); add `addGroupCreds` fixture analog to `addDecisionCreds` (:43-53) for member credential refs. `TestKindRules_Parity_AllThreeSitesInSync` (:181) then locks all four kind rule-sets across all three sites.

---

## 6. Frontend Design

### 6.1 Group Models tab

`SettingsPage.tsx`: extend `TabType` (:52) with `'group_models'`; new tab button (11th, after Decision Models) — `data-tab-id="group_models"`, label **"Group Models"**, glyph **🔀** (shuffle/branching = load balancing; mirrors 🖼️/🎯), `aria-label` per house pattern; content branch + toggle handler + `App.tsx` threading (pure `useState` :82 — no routing work). `SettingsPage.tabs.test.tsx` extended (tab id + aria discipline).

### 6.2 New components (`src/components/config/`)

- **`GroupModelsTab.tsx`** — list/CRUD shell, mirror of `DecisionModelsTab.tsx` (same table/edit/delete affordances; slot-count comments are a known trap — do not copy them).
- **`GroupModelForm.tsx`** — mirror of `DecisionModelForm.tsx` (527L) discipline: payload stamps `kind: 'group'` + `exclude_from_ultimate_switching: true` + `internal: true` (G1/G7 surfacing, read-only), hardcoded constants stamped into the payload, submit race guard, aria error wiring. **Shadow-target picker (arch R1 Am9 — real gap, the field otherwise has NO UI):** a **clearable dropdown for `shadow_model_id`** populated from `useModels()` **filtered to chat kind** (placeholder "None — no shadowing"); clearing it via group edit drops the claim so the shadowed model reappears (Q19).
- **One-click "wrap model X as group [X]" affordance (Am30):** from a chat-model row, pre-fills a new group form (single member = that model, shadow claim = that ID) — **form pre-fill convenience ONLY, not system auto-creation** (~40 LOC; §1 non-goal: no bootstrap/seed/auto-create machinery).
  - **P4 amend (D1 deviation — rationale in-line, locked at 0c607b5):** the literal "ID = member ID" pre-fill is **unsavable** — using `coding` as the new group's ID would collide with the existing chat model's PRIMARY KEY and trip G8 (an enabled group may not claim its own ID; G8 self-ID scan rejects groups whose `id == shadow_model_id`). The canonical encoding is `${id}[${id}]` (e.g. `coding[coding]`), the day-1 self-shadow shape the rest of the suite assumes. The pre-fill stamps `id = "${memberID}[${memberID}]"`, `shadow_model_id = memberID`, `members = [{model_id: memberID, weight: 1}]`, `internal = true`, `exclude_from_ultimate_switching = true`. Verified by the P3 form tests and `TestAm29_Equivalence_AllFiveCases/CodingCoding_Routing` — the encoding is the contract; do not regress it to the literal "ID = member ID" reading.
- **`GroupMemberEditor.tsx`** *(Round 2 Am30 — simplified)*: rows of `{chat-kind MODEL picker, weight number input, remove}` — a model dropdown populated from `useModels()` filtered to chat kind (members are model IDs now), MIN 1, MAX 16, dup-model check only. **−80–100 LOC vs the Round-1 credential+free-text editor.** Duplicate-member hint now = duplicate model ID (warn, don't block).
- **`useGroupModels`** hook (`useApi.ts`): cache key `'group-models'`, URL `${API_BASE}/models?kind=group`, add/update/delete mirroring the sibling hooks' explicit-delete discipline.

### 6.3 Cache quadruple-delete (FE-critical)

All **9** mutation sites in `useApi.ts` (3 per sibling hook: add/update/delete at :300-302/:315-317/:325-327, :402-404/:417-419/:427-429, :503-505/:517-519/:526-528) extend from triple to **quadruple** explicit key deletion: `'models'`, `'imggen-models'`, `'decision-models'`, `'group-models'` — quadruple ⇒ 9 × 4 = 36 deletions. **Count confirmed at 9 (arch R1 Am18):** credential/token mutations correctly invalidate NO model caches today (models reference credentials by ID) — research §8.3's "12 sites" was an over-count; **do not implement phantom sites**. Never `deleteByPrefix` (misses in-flight promises; sibling keys share no prefix). Hook docblocks (:296/:348/:455-456) updated; the three mutation-test files' pinned key sets extended to four.

### 6.4 The four lock-step "exclude group from chat-models" arms

| Arm | Site | Action |
|---|---|---|
| BE `?kind=chat` | `server.go:781` + default arm :788 | exclude `kind=="group"` (groups get their own tab; chat tab stays pure) |
| FE C-16 filter | `components/config/ModelsTab.tsx:162` | add `&& model.kind !== 'group'` (hoist above the empty-state ternary per the C-16 comment block :145-161) |
| `/v1/models` | `handler.go:245-272` | groups **stay listed**; claimed models hidden (§3.4) — the odd arm out, by design |
| TokenForm pickers | `components/tokens/TokenForm.tsx` (~:101 allowed-models, ~:157 ultimate-target) | exclude `kind=="group"` from both pickers (Decision amendment 7 precedent — a chat-arm leak was that commission's highest-severity finding; groups are ultimate-excluded anyway, but the picker should say so) |

### 6.5 FE types

`src/types.ts`: `'group'` added to the kind unions — **citations corrected per arch R1 Am17: kind union at `:123`, `ImgGenModel` at `:155`, `DecisionModel` at `:161`** (first draft's :109/:121/:153 were stale); `GroupMember` + group model shape (members, shadow_model_id). tsc baseline **26 errors / 11 files** must not regress (§9.6).

### 6.6 Embed discipline

Any commit touching `pkg/ui/static/index.html` ships regenerated hashed bundles in the SAME commit; `vite build` (default outDir `pkg/ui/static`); `--outDir /tmp` only for read-only verification; builds byte-reproducible.

---

## 7. DB / Schema

### 7.1 Migration 031 — `031_add_group_fields` (single migration, both columns, both dialects)

- New files: `pkg/store/database/migrations/{sqlite,postgres}/031_add_group_fields.{up,down}.sql` (4 files). Up: `ALTER TABLE models ADD COLUMN members_json TEXT;` + `ADD COLUMN shadow_model_id TEXT;` (dialect-appropriate form; both nullable, no backfill — legacy rows are chat/imggen/decision, all member-free). **Round 2 (R2.1 #8): ZERO schema delta vs Round 1** — same two columns; `members_json` content becomes `[{model_id, weight}]`; same ~30 mechanical sites, same registry append.
- **Registry append (CRITICAL — unskippable):** `pkg/store/database/migrate.go` ordered-slice registry (:24-64, highest entry `{"030", ...}` :63) gains `{"031", "031_add_group_fields.up"}`. The registry is **NOT a directory scanner** — the in-file trap comments (:53-62) document two prior near-misses (029, 030). Forgetting the append = silently inert migration.
- **Registry test:** the existing `schema_migrations`-contains-N test pattern extended to assert `"031"` present after migrate-up (the 030 test is the template).

### 7.2 No other schema changes

Kind column already free-form TEXT (030); usage tables untouched (Q9 reuses existing writers); no `UNIQUE` constraints added (claim uniqueness is a G8 application-level scan — SQLite/Postgres partial-unique-index alternatives rejected as schema divergence for no gain at v1 member volumes).

---
## 8. Config / Settings

- **No new config keys, no env flags** (minimalism; mirrors the imggen posture). All group constants are package-level in `pkg/grouplb`, cloned from credentiallb: affinity TTL 24h sliding, janitor 5m, default cooldown 60s, member cap 16 (reused `MaxCredentialRefs`).
- `models-template.json`: add a documented group example (2 cross-provider members + one `shadow_model_id` example), documentation-only.
- The engine lifecycle (janitor start/`Stop`) is owned by `ModelsManager` beside the credential engine — `cmd/main.go` unchanged.

---

## 9. Testing Strategy

Per-package `go test`; the executing worker uses the cached 1.26 toolchain on PATH (`$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`; VM `/usr/bin/go` is 1.22, `GOTOOLCHAIN=auto` fails offline). Acceptance criteria say `go build ./...` — **never `make build`** (auto-increments VERSION).

### 9.1 `pkg/grouplb` — engine tests cloned from credentiallb patterns

- `engine_test.go`: sticky bind→rebind on reselect over **member model IDs**; `ExcludeAndReselect` (Retry-After cooldown, n==1 no-cooldown, all-cooling soonest-expiry); member-removal drops bindings (`OnGroupChanged` survivor-filter); janitor TTL sweep; `Stop` idempotent; concurrent `GetOrSelect` under `-race` — deterministic via the cloned `testhooks.go` seams (Am14), never sleeps; **zero-credential-import guard** (Am31).
- `selector_test.go`: prefix-sum weights (O(log k)), `weight<=0 ⇒ nil`, single-member fast path.
- `binding_test.go` / `key_test.go`: member-key uniqueness; empty conv key ⇒ no binding, fresh pick.

### 9.2 `pkg/models` — rules + claims

- `group_rules_test.go`: G1–G8 each violation + the valid fixture; `validateGroupMembers` (span-provider member set VALID — the divergence pin); G8 uniqueness across two groups.
- Claim index unit tests: build/rebuild on add/update/delete; disabled group ⇒ inactive claim; shadow-target-deleted tolerated; **self-shadow legality** (G8 + Am22 identity rule).

### 9.3 `pkg/store/database` — persistence

- Migration: up/down on both dialects; `schema_migrations` contains `"031"`; registry-append presence test.
- Column-count pins `querybuilder_test.go:239/:262/:275` bumped to 21.
- `store_group_test.go`: group CRUD round-trip (`members_json` = `[{model_id, weight}]` + `shadow_model_id`); **Am23 save-path test** — a group row saves cleanly through `validateBeforeWrite` (the `validateGroupMembers` routing bypasses the `store.go:1597-1604` Internal gate); **Am27 DeleteModel scan** — deleting a model that is any group's member → blocked, error names the group; **claim-aware `GetModel`** (claimed ID → group; strict twins + `GetModelNative` → real row); resolver UNTOUCHED assertion (no group row ever reaches `ResolveInternalConfigWithAffinity` — a Round-2 regression pin); `OnGroupChanged` binding drop.

### 9.4 `pkg/proxy` — pipeline

- `handle_models_group_test.go`: `/v1/models` lists an enabled group + **hides the claimed model** (fixture precedent `handle_models_imggen_test.go:86-124`); disabled group ⇒ claimed model reappears.
- `handler_groups_test.go`: **Am22 self-shadow regression FIRST (gated before all other group tests)** — single-member `coding[coding]` resolves its member to the REAL row in ONE pass, no recursion; rewrite helper parity test (OpenAI :149-159 ≡ Anthropic :210-219 — Am21); member-pick by weight + sticky second request; **Am24** terminal-failure ⇒ ONE group-level reselect + max-ONE re-entry + member-internal 429s never bubble; **Am28** stale-member skip-and-reselect; **Am25** metering dedup (rewritten request ⇒ 2 rows, direct ⇒ 1, day-1 ⇒ 1); **Am26** stream + non-stream `model` masked to the client-requested string incl. straddled-event fixtures; non-group byte-unchanged regression; auth: token listing the shadowed ID reaches the group, fail-closed otherwise; unknown-model passthrough unchanged.
- Byte-equality assertion (Review R1 N7): for a group request, output differs from the upstream bytes ONLY in the top-level `model` field (masked per Q5); for non-group requests, relayed bytes are byte-equal to upstream.
- **Am29 named equivalence tests - 5 named cases (1 zero-groups + 4 coding[coding] sub-dimensions: routing / response-per-Am26 / metering-per-Am25 / latency-n==1)**: (A) zero-groups ≡ today — byte-identical across routing/response/metering/latency; (B) manual `coding[coding]` ≡ today — routing (winner = the real coding row), response (per Am26: byte-identical iff `internal_model == id`), metering (exactly ONE row per Am25), latency (n==1 E-3 fast path).
- *(Round-1 `internal_handler_group_failover_test.go` — **DROPPED**: Am1 is moot under the pivot; the InternalHandler sees member rows natively.)*
- Existing suite must stay green: `go test ./pkg/proxy/... ./pkg/models/... ./pkg/store/... ./pkg/grouplb/... ./pkg/credentiallb/... ./pkg/ui/...`.

### 9.5 `pkg/ui` — BE API + parity

- `kind_rules_parity_test.go` extension (§5.3) — G-case matrix across all three sites.
- `handlers_models_kind_test.go` extension: `?kind=group` arm; **chat arm excludes groups** (the chat-arm-leak regression class, :283 precedent); POST/PUT mirrors reject G-violations with 400; G7 write-gate 400s on `exclude_from_ultimate_switching=false`.

### 9.6 Frontend (vitest + happy-dom, `src/**/__tests__`)

- `hooks/__tests__/useGroupModels.test.ts`: cache key `'group-models'`; `?kind=group` URL; quadruple delete on all three mutations.
- Extend `useModels.test.ts` / `useImgGenModels.test.ts` / `useDecisionModels.test.ts`: pinned key set now **four** keys.
- `components/config/__tests__/`: `GroupModelsTab.test.tsx`, `GroupModelForm.test.tsx` (payload stamping, submit guard, **one-click wrap pre-fill — Am30**), `GroupMemberEditor.test.tsx` (**chat-kind model picker**, weight>0, dup-model warn, MIN 1 / MAX 16); extend `ModelsTab.chatFilter.test.tsx` and `components/__tests__/SettingsPage.tabs.test.tsx`; extend `components/tokens/__tests__/TokenForm.test.tsx`.
- tsc: `npx tsc --noEmit` ≤ 26 errors / 11 files; the baseline is FROZEN in a companion file `pkg/ui/frontend/tsc-baseline.txt` (created by the implementation — Review R1 N8) so conformance is verifiable without re-running tsc.

---

## 10. Phased Breakdown

*(Restructured for the Round-2 pivot — the resolver-surgery tasks of Round 1 are gone; task numbering below is the Round-2 canonical set.)*

Four phases. Strict order P1 → P2 → P3 → P4; no phase starts before the previous exit criterion is met.

| Phase | Name | Objective | Tasks | Coupling | Status |
|---|---|---|---|---|---|
| **P1** | Kind + storage + group CRUD validation | The kind system knows `"group"`; migration 031 lands; G-rules enforced at all three sites **with the Am23 ship-gate bypass pinned**; a group with model-ID members saves via both write paths; DeleteModel member-scan (Am27). | 8 | tight with P2; independent of P3 | pending |
| **P2** | `pkg/grouplb` + rewrite + failure/metering/masking | The model-ID picker engine; claims-aware GetModel + **`GetModelNative`** (Am22); the **modelList rewrite** wired at both protocol sites (Am21) with parity test; Am24 failure semantics; Am28 stale-member defense; Am26 masking; Am25 metering dedup; `/v1/models` hide + cache eviction. | 11 | tight with P1; independent of P3 | pending |
| **P3** | Frontend: Group tab + model-picker editor + cache discipline | A user can CRUD groups (chat-kind model picker + weight; one-click wrap pre-fill) from the dashboard; chat tab and token pickers exclude groups; quadruple cache delete everywhere. | 8 | tight with P1 (FE API surface); independent of P2 internals | pending |
| **P4** | Tests + docs | Full suite green incl. **Am29 equivalence tests**; template/docs/blueprint updated; ready for user-authorized ship. | 5 | depends on P1+P2+P3 | pending |

### P1 — Kind + storage + group CRUD validation (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P1.1 | Add `KindGroup`, `IsGroup()`, `ModelConfig.Members` (`.[]GroupMember{ModelID,Weight}`) + `.ShadowModelID` fields, `GroupMember` type (`pkg/models/config.go` + new `pkg/models/group.go`) | none | `go build ./pkg/models/...` passes; `IsGroup()` true iff `Kind=="group"` |
| P1.2 | `ValidateKindRules` group arm + `validateGroupRules` (G1–G8, pivot form: member = existing text-kind model, no credentials) + `validateGroupMembers`; extend default-arm string `config.go:188` | P1.1 | `go test ./pkg/models/...` — `group_rules_test.go` green (violations + valid fixture + **self-shadow legal** case) |
| P1.3 | Migration 031 (4 SQL files, **zero schema delta** — `members_json` = `[{model_id, weight}]`) + registry append + `schema_migrations`-contains-`"031"` test | P1.1 | `go test ./pkg/store/database/...` green; up/down clean on both dialects |
| P1.4 | Storage plumbing: SELECT/scan/write/`dbModelRow` (+2 cols everywhere per §3.3 — **incl. inline SELECTs `store.go:857-873` + `:939-955`**); bump `querybuilder_test.go` pins to 21; re-derive the exact SELECT-site enumeration from source | P1.3 | `go test ./pkg/store/database/...` green; group row round-trips both columns; derived SELECT-site list in the execution report |
| P1.5 | Write-path routing: `kind==group` rows run `validateGroupMembers` INSTEAD of `validateModelAgainstCredentials`, branching **BEFORE the Internal gate** (`store.go:1597-1604` — **Am23 pin**); branch placement RECOMMENDED: inside `validateBeforeWrite` (`store.go:1736`; TWO production call sites: AddModel `store.go:1351` + UpdateModel `store.go:1438`) so every write is covered — branching only inside `validateModelAgainstCredentials` (:1597) would leave the Internal gate reachable from other callers; G8 uniqueness scan; JSON twin same | P1.2, P1.4 | **Save-path test**: group row saves cleanly via DB and JSON backends; G-violations 400/error at both |
| P1.6 | UI POST/PUT mirrors: `?kind=group` whitelist + inline G-rule 400 blocks + G7 write-gate stamps | P1.5 | POST/PUT G-violations → 400 house envelope; valid group → 200 |
| P1.7 | Parity-test extension: G-case matrix + `validateGroupRulesCaller` + fixtures | P1.5, P1.6 | `TestKindRules_Parity_AllThreeSitesInSync` covers 4 kinds × 3 sites |
| P1.8 | **Am27 DeleteModel member-scan**: refuse model delete while the ID is any group's member (error names the group); credential-side scan NOT added (groups carry no credentials) | P1.4 | Delete-blocked test green; after member removal → delete succeeds |

**P1 exit criterion (8 tasks)**: A `kind="group"` row with 2 model-ID members saves via UI API and both backends (Am23 pin proven); G-rules identical at 3 sites; migration 031 + registry test green; `go build ./...` passes.

### P2 — `pkg/grouplb` + rewrite + failure/metering/masking (11 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P2.1 | Create `pkg/grouplb` (`doc.go`, `engine.go`, `selector.go`, `binding.go`, `events.go`, `testhooks.go` — 7 seams; **`key.go` IMPORTED from credentiallb**; zero credential/provider imports; lazy per-group state, no boot rebind) | none | `go test ./pkg/grouplb/...` green (deterministic race tests via testhooks; import-guard test); `go vet` clean |
| P2.2 | Manager wiring: engine lifecycle + `OnModelChanged` extension → claim-index rebuild + `OnGroupChanged` survivor-filter + **`pkg/modelscache` claim-eviction hook** (`modelsByID`+`negByID`, old/new shadow) | P2.1, P1.4 | Manager tests: index rebuilt on group CRUD; positive+negative cache entries evicted |
| P2.3 | Claims-aware `GetModel` (claims-first, enabled groups) + **`GetModelNative`** (direct-ID, claim-bypass — **Am22 hard rule**); strict twins untouched; JSON twin same | P2.2 | §9.3 tests green — claimed ID → group; native + strict → real row |
| P2.4 | **Am21 rewrite**: ONE shared manager-level helper (member pick via grouplb + `GetModelNative` + Am28 re-validation) wired at `handler_functions.go:149-159` AND `handler_anthropic.go:210-219`; `rc.resolvedModel` stays the group row; **parity test** locks both sites | P2.3 | Parity test green; **Am22 self-shadow regression FIRST** — one-pass resolution, no recursion; rewritten list = `[member.ID] + member.FallbackChain` |
| P2.5 | `/v1/models` second-pass shadow-hide (claims-index scan); groups stay listed | P2.3 | `handle_models_group_test.go` green |
| P2.6 | **Am24 failure semantics**: group-level `ExcludeAndReselect` + max-ONE re-entry on terminal member-pipeline failure (`handler.go:1086` / Anthropic `:395-412`), pre-first-byte only, no chaining | P2.4 | Member-internal 429 never re-enters the group layer; terminal failure ⇒ exactly ONE reselect + one re-entry; second terminal failure propagates |
| P2.7 | **Am28 stale-member defense** at rewrite (exists/chat-kind/not-group/enabled; skip-and-reselect; empty → resolve-failure envelope) | P2.4 | Member disabled/deleted between write and request ⇒ fresh pick; empty ⇒ clean failure envelope |
| P2.8 | **Am26 masking**: `maskModelField` — non-stream `finalBody` + per-complete-SSE-event buffered patch (Am6), group-kind-only, fail-open-to-verbatim | P2.4 | Group responses carry the client-requested string incl. straddled fixtures; non-group byte-unchanged |
| P2.9 | **Am25 metering dedup**: stamp served member at rewrite (`FallbackUsed[0]`/`RouteModel`) when `memberID != reqLog.Model`; member row gated on `group-pick active && winner.GetModelID() != reqLog.Model`; ultimate site group-name-row-only (Am11); 5s detached timeout; `model_member_selected` event (Q15 payload) | P2.4 | Rewritten request ⇒ 2 rows; direct ⇒ 1; **day-1 ⇒ exactly 1**; event `newlyBound`-only |
| P2.10 | Restart cold-start startup log line | P2.2 | Log line present |
| P2.11 | Full pipeline test pass + regression (`go test ./pkg/proxy/... ./pkg/ultimatemodel/...`) + one-line check: `/v1/image_generation` with a group ID fails cleanly upstream | P2.5–P2.10 | All green; imggen/decision/ultimate tests untouched and passing |

**P2 exit criterion (11 tasks)**: A client request for the group name (or claimed ID) sticky-picks a member whose FULL existing pipeline executes natively on BOTH protocols; terminal failures re-enter once; metering dedups; responses masked; `go build ./...` + `go test ./pkg/...` green.

### P3 — Frontend (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P3.1 | `types.ts`: `'group'` kind unions (`:123`/`:155`/`:161`) + `GroupMember{model_id, weight}` + group shape | P1.6 | tsc ≤ 26/11 |
| P3.2 | `useGroupModels` hook (`'group-models'`, `?kind=group`) | P3.1 | hook test green |
| P3.3 | Quadruple-delete at all 9 mutation sites + docblocks (§6.3 — unchanged by the pivot) | P3.2 | 3 mutation-test files green with four-key pin |
| P3.4 | `GroupMemberEditor.tsx` — **chat-kind model picker + weight** (Am30; −80–100 LOC vs Round 1) | P3.1 | editor tests green (picker filtered to chat kind, weight>0, dup-model warn, MIN 1/MAX 16) |
| P3.5 | `GroupModelForm.tsx` (kind+G1/G7 stamping, submit guard, aria; Am9 clearable chat-kind shadow dropdown) + **one-click wrap pre-fill (Am30, ~40 LOC)** | P3.4 | form tests green incl. pre-fill + shadow set/clear |
| P3.6 | `GroupModelsTab.tsx` + `SettingsPage.tsx` 11th tab + `App.tsx` threading; verify tabs-test assertion shape (arch OQ 2) | P3.2, P3.5 | tab tests green |
| P3.7 | Lock-step arms: `ModelsTab.tsx:162` C-16 + TokenForm picker filters (§6.4) | P3.1 | extended filter tests green |
| P3.8 | `vite build` + regenerated `pkg/ui/static/**` in the same change | P3.6 | `go:embed` resolves; `go build ./...` passes |

**P3 exit criterion (8 tasks)**: Dashboard CRUD of model-ID groups works end-to-end incl. one-click wrap; arms hold; tsc ≤ 26/11; FE tests green.

### P4 — Tests + docs (5 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P4.1 | Full suite: `go build ./...` + `go test ./pkg/...` (1.26 toolchain) | P1–P3 | Zero failures; known credentiallb TOCTOU `-race` flake adjudicated per project note |
| P4.2 | **Am29 equivalence tests**: (A) zero-groups identical to today (byte-identical); (B) manual coding[coding] identical to today - routing / response (per Am26) / metering (per Am25) / latency (n==1) | P1-P3 | All Am29 equivalence tests green in `pkg/proxy/handler_groups_test.go` (5 named cases: zero-groups x1 + coding[coding] x4 sub-dimensions); smoke checklist supplements the release note; restart -> cold-start log + fresh binding |
| P4.3 | `models-template.json` group example (docs-only) | P1 | Loads clean |
| P4.4 | Blueprint note: `group` kind, modelList rewrite, `pkg/grouplb` picker, claim mechanism | P2 | Blueprint section present |
| P4.5 | CHANGELOG entry | All | Entry present |

**P4 exit criterion (5 tasks)**: Everything green locally; both Am29 equivalences demonstrated; docs consistent; ready for user-authorized ship (no push without explicit instruction).

---

## 11. Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | **Single-InternalModel resolver seam** — `resolveWithCredential` reads `modelConfig.InternalModel` (`store.go:2118`); any group path that leaks past the kind-branch resolves an empty upstream model | ~~High~~ | ~~Low~~ | **ROUND 2: MOOT** — group rows never reach the resolver (Am21); retained for audit | Kind-branch is the FIRST statement after the Internal gate (§3.6.1); G1 keeps `Internal=true` so dispatch predicates route correctly; P2.4 tests assert the member-shaped `ResolvedCredential`; defense-in-depth empty-members → ok=false |
| R2 | **credentialID-only engine keying** - Round-1 concern: an engine keyed by credential conflates members sharing a credential (429 on A cools B) | struck: High | struck: Medium | **ROUND 2: EVAPORATED** - member key = `models.ID` (a plain model ID); grouplb binding/cooldown maps are keyed by the member model ID; no composite construction exists. Retained for audit | P2.1 pins per-member binding/cooldown isolation on model-ID keys |
| R3 | **ID-vs-Name shadowing misconception** — the commission says "name"; the codebase resolves IDs (`§2.2`) | High | High (if unaddressed) | §2.2 + §3.4 design the claim in ID space explicitly; Q1 records it; Am29 equivalence tests (P4.2) exercises the real flow; docs call it out |
| R4 | **Auth allow-list surprise** — tokens listing a shadowed ID now reach the group; tokens listing only the group ID don't reach the alias | Medium | Medium | Semantics pinned (§3.4): auth evaluates the client string, fail-closed unchanged; §9.4 auth tests (`handler_groups_test.go` — P2.4; full suite P2.11) assert both directions |
| R5 | **FE cache fan-out miss** — adding a 4th cache key with incomplete deletes leaves stale tabs | Medium | Medium | Quadruple explicit delete (never prefix) at all 9 sites; mutation tests pin the exact key set (§6.3) |
| R6 | **Chat-arm lock-step leak** — groups leaking into the chat Models tab or token pickers | Medium | Medium | Four arms enumerated (§6.4) with tests each (P3.7, P1.6); the decision-commission's chat-arm leak is the named precedent |
| R7 | **modelscache staleness under claims** — `GetModel(shadowedID)` caches the group row; claim edits would serve stale redirects | High | Medium | Cache invalidation for group + shadowed (+ old-shadow) IDs wired into the same `OnModelChanged` extension (P2.2); §9.3 test |
| R8 | **Claim-index drift** — index and rows diverge across the two backends | Medium | Low | Single rebuild point per backend (`OnModelChanged` / JSON load); §9.2 rebuild tests; G8 uniqueness scan at every write |
| R9 | **Force-ultimate onto a group** — `X-Force-Ultimate-Model` bypasses exclusion; kind-blind dispatch then external-passthroughs an empty `UpstreamURL` | Low | Low | Fails fast with the normal error envelope; documented residual (§3.6.4); v2 could add a kind check in the force path. Related documented semantic (Am5): while X is claimed, X's ultimate switching silently stops (§3.6.4; **pending as Q17**) |
| R10 | **Metering timeout change** — adopting the 5s detached pattern at the 3 sites alters existing chat metering behavior under hangs | Low | Low | Bounded, sites are already edited for Q9; behavior change is "bounded wait" not "dropped row"; noted in the execution report |
| R11 | **Engine-clone concurrency flake** — the credentiallb TOCTOU test flakes ~1/5 under `-race` (pre-existing project note) | Low | Medium | Clone tests use injected clock + serialized upgrade assertions, not sleeps (§9.1); flake class named in P4.1 so it isn't misread as a regression |
| R12 | **Migration registry trap** — 031 SQL lands without the registry append (two prior near-misses documented in-file) | High | Low | P1.3 bundles SQL + append + contains-`"031"` test as ONE task; the in-file trap comments (:53-62) stay adjacent to the new entry |
| R13 | **Anthropic member-429 failover gap** — without the Am1 branch, `internal_handler.go:240` returns the error immediately for groups (`Credentials==0`) and the retry would rebuild from an empty `InternalModel`: the FIRST member 429 fails the whole `/v1/messages` request despite healthy members | ~~High~~ | ~~High~~ | **ROUND 2: MOOT** — Am1 superseded by Am21 (the InternalHandler sees member rows natively). Retained for audit | P2.4 rewrite + Am21: the InternalHandler sees member rows natively; the dropped internal_handler_group_failover_test.go is superseded by the Am22/Am24 tests in handler_groups_test.go |
| R14 | **L1 claims-loop** — any claims-aware lookup at the rewrite site infinite-loops the day-1 self-shadow group `coding[coding]` | High | High (if Am22 skipped) | `GetModelNative` hard rule (direct-ID, claim-bypass); self-shadow regression test **gated FIRST** in §9.4 (P2.4) |
| R15 | **Ship-gate** — `validateModelAgainstCredentials` (`store.go:1597-1604`) rejects `Internal=true, Credentials=[]`, exactly the group-row shape; a misplaced bypass = groups unsavable | High | Medium | Am23: the kind-routing branch precedes the Internal gate; save-path test in P1.5 |
| R16 | **Rewrite-siting drift** across the two protocol build sites | Medium | Medium | ONE shared manager-level helper + parity test (Am21/P2.4) |
| R17 | **Group-level re-entry** is a new responsibility boundary (could mask real failures or loop) | Medium | Medium | Am24: terminal-only trigger, max-ONE re-entry, pre-first-byte, no chaining; P2.6 tests |
| R18 | **Member-side knobs silently active inside groups** (member's own fallback_chain / peak hours / secondary execute within group traffic) | Low | High (by design) | Documented, not gated (Am31/Q21) — surfaced for user awareness |

---

## 12. Assumptions

| # | Assumption | Plan section | Owner |
|---|---|---|---|
| A1 | `"group"` is collision-free as a kind value (verified: free-form TEXT column, `migrate` 030) | §3.1 | explorer (verified) |
| A2 | Next migration number is 031 (registry highest = 030 at `migrate.go:63`) | §7.1 | explorer (verified) |
| A3 | Both storage backends ship together (SQLite + Postgres + JSON twin) — no backend-lag profile | §3.3, §7 | planner |
| A4 | Streaming pass-through is inherited untouched by the resolver seam (no adapter/normalizer changes) | §3.6.1, §4 | planner |
| A5 | `getModelStrict` twins are admin-only fetch paths and must bypass claims | §3.4 | planner (verified usage at :1191/:1197/:1205) |
| A6 | **ROUND 2 SUPERSEDED:** members ARE model rows now (pivot); per-member overrides unnecessary — the member's own model config governs | §3.2 | planner |
| A7 | Same-named upstream models across providers may blend in `model_hourly_usage` (accepted for v1); **extended per arch R1 Am19:** **ROUND 2 REFINED:** members are real model rows — per-member usage attribution is the member's own model ID (attribution natural) | §3.7 | planner |
| A8 | Existing chat/imggen/decision/ultimate behavior is regression-frozen; their tests stay green untouched | §9.4, P2.11 | planner |
| A9 | The executing worker has the cached 1.26 toolchain available (project operating note) | §9 | project |
| A10 | PLAN-ONLY: no push/merge/commits from planning; execution honors the wt-claim (`push:false`) | §1 | commission |
| A11 | Claim-uniqueness at application level suffices (no DB unique index for v1 member/claim volumes) | §7.2 | planner |
| A12 | The 5s meter timeout adoption at the 3 sites is in-scope because those sites are edited anyway | §3.7, R10 | planner |
| A13 | `ComputeConversationKey` accepts the group ID unchanged (parameter, not model-specific logic) | §3.5 | explorer (verified `key.go:40-43`) | **Round 2: group-salted keys verified coherent** (member cred-LB binds `(memberID, group-salted-key)`; distinct spaces per group/direct) |
| A14 | Force-ultimate residual (R9) is acceptable to ship undocumented to users (internal operator note only) | §3.6.4 | planner |

---

## 13. Open Questions for the User

Each row's **Recommended default is authoritative**; `decisions.md` mirrors this table (same numbering) with rationale per row. Rows marked **USER-DECIDED** record already-locked scope constraints — listed for traceability, not re-opening. **Architect Round 1 annotations:** Q5 (implementation detail amended — per-SSE-event buffering, Am6) and Q9 (plumbing + anchors corrected — winner-based gating, Am3/Am11); defaults themselves unchanged. Q1–Q16 numbering/titles are STABLE; Q17–Q20 appended from arch §12 (defaults = arch recommendations).

| # | Question | Recommended default |
|---|---|---|
| Q1 | **Shadowing mechanism** — the commission says "name", but resolution is ID-keyed everywhere (`§2.2`). Pick the claim design, resolution order, `/v1/models` hiding, deletion behavior, and auth-allow-list semantics. | **Single-ID claim** (`shadow_model_id` column): claims checked first in `GetModel` for enabled groups; strict admin twins bypass; `/v1/models` hides claimed IDs while claimed; deletion/disable drops the claim (model reappears); auth evaluates the client-requested ID string unchanged (fail-closed as today). Alias arrays → v2. |
| Q2 | **Membership shape** — **USER-DECIDED (Round 2 pivot)**: members = **proxy model IDs (+weight)**; the group is one thin layer before distribution; the hash binds to the member PROXY model; *"quicker, more robust, less change"*; two-level hashing accepted. | **Adopted as decided** — inline `(credential, upstream_model)` triples and all Round-1 alternatives are OFF the table; see §3.2/§3.6.1. |
| Q3 | **LB strategy** | **USER-DECIDED**: clone the `pkg/credentiallb` engine (weighted-random sticky + cooldowns free); no new strategies in v1. |
| Q4 | **Sticky affinity**: what is hashed, where state lives, TTL, restart behavior, failure/removal handling. | **USER-DECIDED**: `ComputeConversationKey(groupID, tokenID, firstUserMessage)` verbatim; **in-memory** (no DB persistence); 24h sliding TTL + 5m janitor (engine defaults); restart = cold start (documented log line); sticky-target failure ⇒ exclude-and-reselect within the group and re-stick to the new pick; member removal ⇒ drop that member's bindings. |
| Q5 | **OpenAI response `model` field**: upstream leaks varying member names; Anthropic masks. Mask or accept? | **Mask to the client-requested name**, stream + non-stream, group-kind-only (bounded top-level-key patch; fail-open-to-verbatim). Restores single-model transparency; parity with Anthropic; zero cost for non-group traffic. Diagnostic verbatim toggle → v2. |
| Q6 | **Ultimate switching posture for groups** | **Force `ExcludeFromUltimateSwitching == true`** via G7 (D9 precedent `config.go:283-285`) + UI write-gate mirrors; runtime gate then never selects a group. Force-trigger residual documented (R9). |
| Q7 | **Validation rule set** — minimal G-rules; member provider-span vs the credential provider-match invariant. | **G1–G8 per §5.1**: `Internal=true`; ≥1 member (cap 16); refs exist + model non-empty + weight>0; no dup members; **members may span providers** (deliberate divergence — the invariant does not apply to groups); deny fallback/secondary/peak/deadline; G7; claim integrity + cross-group uniqueness. Flat membership (no group members) is **USER-DECIDED**. |
| Q8 | **Storage**: JSON membership column vs separate `group_members` table. | **JSON column** (recommendation shape (a) followed): migration 031 adds `members_json` + `shadow_model_id`; ~30 mechanical precedent-covered touch-sites; keeps `OnModelChanged`/modelscache untouched. Table profile rejected (house-style violation, hand-built integrity, second JSON implementation). |
| Q9 | **Metering attribution** — per-target member vs group name vs both; timeout pattern. *(R1: anchors amended — Am11; R2: the Am25 dedup gate replaces the Am3 plumbing; default unchanged.)* | **Both rows**: existing group-name row (dashboard continuity) + per-target row for the **served member** (user's presumption) — **Round 2 / Am25: delivered via the dedup gate** — member row emitted ONLY when `group-pick active && winner.GetModelID() != reqLog.Model` (the winner row IS the member row; no resolver plumbing exists or is needed); ultimate-branch site group-name-row-only; same-named upstream blend accepted (v2 disambiguation). 5s detached-context timeout (decision pattern) at the 3 corrected sites. |
| Q10 | **FE specifics**: tab identity, member editor, cache discipline. | **Tab** `group_models` / label "Group Models" / 🔀; **GroupMemberEditor** = chat-kind MODEL picker + weight (Round-2 Am30; the Round-1 cross-provider credential editor is superseded), MIN 1 / MAX 16; cache key `'group-models'` + `?kind=group`; **quadruple** explicit delete at all 9 mutation sites; four lock-step chat-arm exclusions (§6.4). |
| Q11 | **Dangling member refs** (a member model deleted underneath a group): block-at-delete vs runtime tolerance + heal. | **Block-at-delete** (one posture, v1 - Am27): **DeleteModel refuses while the model ID is a member of any group** (error naming the group); the operator removes the member first. The Round-1 credential-side scan DISAPPEARS (groups carry no credentials - nothing to scan). Cheap scan, zero healing machinery; tolerance+heal -> v2. |
| Q12 | **Semantics corners**: enable/disable, empty-group runtime, claim-target deletion, group edits. | Enabled=false ⇒ claim inactive + absent from `/v1/models` (direct-named resolution = today's disabled-model parity); empty groups unreachable (G2 + Q11; defense-in-depth ok=false fail-closed); deleted claim-target tolerated (claim stands, nothing to hide); group ID immutable, member/claim edits live immediately. |
| Q13 | **`Internal` flag on the group row** (research-codebase §9.2 item 13 proposed `Internal=false`). | **`Internal=true` (G1)** — item 13's `false` is rejected on evidence: ~11 `.Internal` dispatch predicates (§2.4) + the resolver gate (`store.go:2172`) would all need kind extensions, and the group would otherwise fall to the external raw-passthrough path. `true` routes correctly for free via the `.Internal` predicates; the **Am21 rewrite substitutes the member row before the resolver/coordinator see anything**. | **Round 2: STAYS** — re-verified by both workers (the ~11 `.Internal` predicates fire on the group row pre-rewrite). |
| Q14 | **Member cooldown precision** — Round-1 composite key (research-codebase §9.2 item 7) | **SUPERSEDED by the Round-2 pivot (R2.1 evaporation #4):** cooldown/binding keys are **plain member model IDs** — no composite key, no separator gate, no shared-credential conflation question (each member is a full model with its own credential LB). |
| Q15 | **Member-resolution observability event** (research-codebase §9.2 item 11). | **Yes**: `model_member_selected` `{group_id, member model_id, upstream_provider, upstream_model}` (payload adjusted — Round 2) at the existing W-1 publish site, firing only on `newlyBound`; non-group traffic keeps `model_credential_selected` unchanged. **Round 2: CARRIES** with the adjusted payload. |
| Q16 | **Chat→group fallback entries** (a chat model's `fallback_chain` referencing a group ID); group→group fallback (research-codebase §9.2 item 14). | **Group→group: impossible** (G6 — groups have no chain; item 14's broader first-class semantics → v2). **Chat→group: NATIVE under the Round-2 pivot** — the Am21 rewrite applies uniformly to every list entry incl. fallback entries (zero resolver contact); group-level re-entry follows Am24 uniformly on both protocols (the Round-1 `:1410` asymmetry sentence is superseded). |
| **Q17** | **NEW (arch §12 item 1) — Ultimate-stops-while-shadowed**: while X is claimed by an enabled group, claims-first `GetModel(X)` returns the group row (G7-excluded), so X's ultimate switching halts until the claim drops. Accept the semantic? | **Accept + document** (Am5; §3.6.4 + §11 R9). The group takes over X's identity wholesale; un-claiming (delete/disable/edit) restores X's ultimate eligibility. No code — pure documentation. | **Round 2 re-verified: still applies** (ultimate evaluated on the group row pre-rewrite; Am5 semantics unchanged). |
| **Q18** | **N-members-per-target pattern** — Round 1: accept + document | **SUPERSEDED by the Q2 pivot:** multi-credential fan-out for a member is that member's OWN credential LB, inherited natively — no N-rows pattern, no pooling question. Pointer: see Q2. |
| **Q19** | **NEW (arch §12 item 3) — Clearing `shadow_model_id` via group edit**: does an operator edit that empties the claim field drop the claim? | **Allow** (Am9; GroupModelForm clearable dropdown). Claim drops → shadowed model reappears in `/v1/models` and resolves normally; cache/index invalidation rides the existing group-write hook (P2.2). |
| **Q20** | **NEW (arch §12 item 4) — Fallback-reached groups meter the member row too**: the Am25 dedup gate meters chat→group fallback traffic to the served member as well as the group name — confirm intended. | **Yes — intended** (consistent with Q9's both-rows intent); falls out of the Am25 dedup gate (`group-pick active && winner.GetModelID() != reqLog.Model`) for free, no extra code (P2.9 acceptance pins it). |
| **Q21** | **NEW (Round 2, Am31) — Member-side knobs inside groups**: a member's own `fallback_chain` / `peak_hours` / `secondary_upstream_model` now execute within group traffic (the member pipeline is unchanged by design). Gate them for group traffic, or document? | **Document, don't gate** — knob inheritance is a feature (per-member resilience composes into the group); documenting the surprise is the v1 posture; per-context knob suppression → v2 candidate. |

---

## 14. v2 Candidates (Deliberately Cut)

Every cut below is a direct consequence of the USER-DECIDED minimalism; each is recorded so v1 reviews don't re-litigate and v2 can pick them up deliberately.

- **Affinity persistence (DB-backed sticky memory, warm restart)** — in-memory is USER-DECIDED for v1; restart cold-start documented instead of engineered around (Q4).
- **Group-of-groups (nested groups)** — flat membership only, USER-DECIDED (G5).
- **Health-tracking sophistication** (active probing, success-rate scoring, auto-ejection) — cooldowns + exclude-and-reselect cover v1.
- **Weighted/round-robin/least-latency strategies beyond the engine clone's free weighted-random** — USER-DECIDED.
- **Exotic validation rules** (per-member timeouts/retries, weight ceilings beyond >0, claim-pattern validation) — minimal G-set only (Q7).
- **`fallback_chain` / `secondary_upstream_model` / `peak_hour_enabled` / `release_stream_chunk_deadline` for groups** — G6 rejects; first-class group fallback semantics + group→group fallback (research-codebase §9.2 item 14) revisited together.
- **Multiple shadow claims per group (alias arrays)** — single `shadow_model_id` for v1 (Q1).
- **Credential-wide member cooling** (a 429 cools all members on that credential) — member-precise chosen (Q14); the coarse posture is the alternative, not a bug.
- **Provider-qualified usage keys** (disambiguating same-named upstream models in `model_hourly_usage`) — schema change; accepted blend for v1 (Q9).
- **Diagnostic verbatim `model`-field passthrough toggle** — masking is unconditional in v1 (Q5).
- **Runtime dangling-member tolerance + heal** — block-at-delete chosen (Q11); posture B is the v2 alternative.
- **`/v1/models?kind=group` client-side filter parity** (research-codebase §9.2 item 8) — deferred; groups are already listed.
- **Per-target credential pooling / N-members-per-target** — **SUPERSEDED by the Round-2 pivot**: multi-credential fan-out is the member model's own credential LB, inherited natively (Q18 → Q2).
- **MCP-server group cross-pollination** (research-codebase §9.2 item 9) — unrelated surface; revisit only if MCP gains multi-backend servers.
- **Force-ultimate kind guard** (closing the R9 residual) — one-line check if product ever cares.

---

## Appendix A — Files Changed

### Source files (Backend — modified) *(Round-2 manifest; Round-1 entries superseded where noted)*

- `pkg/models/config.go` — `KindGroup`, `IsGroup()`, `ModelConfig.Members`/`.ShadowModelID`, `ValidateKindRules` arm + default-arm string (:188), JSON-backend twin updates (GetModel claim check)
- `pkg/store/database/store.go` — schema scan/write plumbing (+2 cols), claim-aware `GetModel` (:854), **`GetModelNative` (Am22)**, claim index + `OnModelChanged` extension, **DeleteModel member-scan (Am27)**, **Am23 write-path routing** (group rows bypass `validateModelAgainstCredentials` before the Internal gate :1597-1604). **Round-1 resolver-region edits DROPPED** (no kind-branch in `ResolveInternalConfigWithAffinity` — Am21)
- `pkg/store/database/querybuilder.go` — SELECT/INSERT/UPDATE +2 columns ×2 dialects
- `pkg/store/database/migrate.go` — registry append `{"031", ...}`
- ~~`pkg/proxy/handler_functions.go`~~ — **AS-BUILT CORRECTION: NOT modified** — the rewrite landed via the shared `GroupRewriter` seam called at both protocol sites (P2 drift note)
- `pkg/proxy/handler_anthropic.go` — **Am21 rewrite site (mirror, :210-219)** *(Round-1 "NOT changed" status superseded — it now hosts the rewrite call, not resolver surgery)*
- `pkg/proxy/handler.go` — `/v1/models` shadow-hide pass (:245-272); Am25 metering dedup + 5s detached timeout (:908-934 ultimate / :1446-1472 stream / :1656-1682 non-stream); Am24 group-level re-entry hook at `handleRaceFailure` (:1086); stream/non-stream `model` masking (:1172-1184/:1587)
- `pkg/proxy/race_executor.go` — `model_member_selected` publish split (W-1 site; Q15 payload)
- `pkg/ui/server.go` — `?kind=group` arm + chat-arm exclusion (:781/:788); POST/PUT group mirrors + G7 gates

### New source files (Backend)

- `pkg/grouplb/doc.go` — package contract: model-ID membership, in-memory affinity, restart cold-start
- `pkg/grouplb/engine.go` — sticky weighted picker over model IDs (zero credential awareness; `ExcludeAndReselect`; `OnGroupChanged`)
- `pkg/grouplb/selector.go` — prefix-sum weighted selector
- `pkg/grouplb/binding.go` — binding record + rebind semantics
- `pkg/grouplb/events.go` — event constants
- `pkg/grouplb/testhooks.go` — the 7 test seams (Am14) *(no `key.go` — imported from `pkg/credentiallb`, Am31)*
- `pkg/models/group.go` — `GroupMember{ModelID,Weight}`, `validateGroupRules` (G1–G8 pivot form), `validateGroupMembers`
- `pkg/store/database/migrations/{sqlite,postgres}/031_add_group_fields.{up,down}.sql` (4 files)

### Source files (Frontend — modified)

- `pkg/ui/frontend/src/types.ts` — `'group'` kind unions + `GroupMember{model_id, weight}` + group shape
- `pkg/ui/frontend/src/hooks/useApi.ts` — `useGroupModels` + quadruple delete ×9 + docblocks
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — `TabType` + 11th tab + content branch
- `pkg/ui/frontend/src/components/config/ModelsTab.tsx` — C-16 filter excludes groups (:162)
- `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` — both pickers exclude groups
- `pkg/ui/frontend/src/App.tsx` — tab threading

### New source files (Frontend)

- `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx`
- `pkg/ui/frontend/src/components/config/GroupModelForm.tsx` — incl. Am9 shadow dropdown + Am30 one-click wrap pre-fill
- `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx` — chat-kind model picker + weight (Am30)

### Test files

- New BE: `pkg/grouplb/{engine,selector,binding,events}_test.go` (incl. zero-credential-import guard); `pkg/models/group_rules_test.go`; `pkg/store/database/store_group_test.go` (incl. Am23 save-path + Am27 delete-scan); `pkg/proxy/handle_models_group_test.go`; `pkg/proxy/handler_groups_test.go` (**Am22 self-shadow regression gated FIRST**, Am21 parity, Am24, Am25, Am26, Am28, Am29 equivalence — 5 named cases (1 zero-groups + 4 coding[coding] sub-dimensions))
- Extended BE: `pkg/ui/kind_rules_parity_test.go`; `pkg/ui/handlers_models_kind_test.go`; `pkg/store/database/querybuilder_test.go` (pins 19→21); registry test (`"031"`)
- New FE: `src/hooks/__tests__/useGroupModels.test.ts`; `src/components/config/__tests__/{GroupModelsTab,GroupModelForm,GroupMemberEditor}.test.tsx`
- Extended FE: `src/hooks/__tests__/{useModels,useImgGenModels,useDecisionModels}.test.ts` (four-key pin); `src/components/config/__tests__/ModelsTab.chatFilter.test.tsx`; `src/components/__tests__/SettingsPage.tabs.test.tsx`; `src/components/tokens/__tests__/TokenForm.test.tsx`

### Config / build / docs

- `models-template.json` — group example (docs-only)
- `pkg/ui/frontend/tsc-baseline.txt` — NEW (Review R1 N8): frozen 26-errors/11-files baseline
- `CHANGELOG.md` — feature entry
- Project blueprint — `group` kind note
- `.agents/shared/planning/load-balance-groups/{plan.md,decisions.md}` — this plan + decision log

### NOT changed (deliberately)

- `pkg/decision/`, `pkg/imggen/` — skipped paths (scope)
- `pkg/ultimatemodel/` — G7 shields group traffic (evaluated on the group row pre-rewrite); a member's own ultimate eligibility applies only on direct member requests (Review R2 N20)
- **`pkg/store/database/store.go` resolver region** — `ResolveInternalConfigWithAffinity`(+cached) UNTOUCHED under the pivot (Am21); regression-pinned by a test
- `pkg/proxy/internal_handler.go` — Round-1 Am1 target; **MOOT under the pivot** (member rows execute natively)
- ~~`pkg/proxy/race_coordinator.go`~~ — **AS-BUILT CORRECTION: modified in P2** (manifest declared it unchanged; drift note did not record the one-phrase reason — see the P2 execution report)
- `pkg/credentiallb/` — clone source (`key.go` imported), not modified
- `pkg/auth/` — ID-string semantics unchanged
- `cmd/main.go` — engine lifecycle owned by `ModelsManager`
- `pkg/toolcall/`, `pkg/toolrepair/`, `pkg/loopdetection/`, `pkg/supervisor/`, `pkg/bufferstore/` — content-keyed, model-identity-agnostic
- No pushes, no merges, no VERSION bumps (PLAN-ONLY; execution is local-only)

### Estimated diff size

- Modified BE: 9 files (~+600 / −40)
- New BE: 12 files (~+900 incl. `pkg/grouplb` (6 files) + migrations)
- Modified FE: 6 files (~+280 / −30); New FE: 3 files (~+550)
- New/extended tests: **13 new + 10 extended = 23 test files** (~+1,200 LOC)
- **Total ≈ 3,500 lines added / 60 removed across ~33 files** (−10% LOC, −20% files vs Round 1's ≈3,900/41 — R2.7; manifest-completeness rule: **every file named in the plan body appears in this manifest**; deviations must be called out in the execution report)

---

## Appendix B — Amendment Log (Architect Round 1)

Source: `architecture-recommendation.md` (278L, endorse-with-amendments; all §10 amendments folded in place — none rejected). Severity as graded by the architect.

| Am | Sev | Summary | Applied at |
|---|---|---|---|
| 1 | 🔴 | Anthropic group-aware failover branch in `internal_handler.go` (:240 guard lift + `grouplb.ExcludeAndReselect` + member-shaped retry) — replaces the false "zero code changes" claim | §3.6.3 (rewrite) · P2.7 (new task; P2.8–P2.11 renumbered, refs swept) · §9.4 test · §11 R13 · Appendix A |
| 2 | 🔴 | Member-key `|` collision gate: ban `|` in credential IDs at `CredentialConfig.Validate` + G3 defense-in-depth | §3.2 · §3.5 table · §5.1 G3 · P1.1 · Appendix A (`credential.go`) |
| 3 | 🔴 | Metering plumbing spec: winner-based (`GetResolved()`/rc-stamp), gate on winner-row-kind==group && resolvedOK — the as-written `resolved` does not exist at the sites | §3.7 (rewrite) · P2.9 (rewrite) · Q20 · §13 Q9 annotation |
| 4 | 🟡 | `pkg/modelscache` claim-eviction hook (`modelsByID` + `negByID`, old/new shadow; 60s neg-TTL window); package path corrected; claim authority stated | §3.4 (rewrite) · P2.2 · Appendix A (`modelscache/models.go`) |
| 5 | 🟡 | Ultimate-stops-while-shadowed documented | §3.6.4 · §11 R9 · Q17 |
| 6 | 🟡 | Masking buffered per complete SSE `data:` event, never per raw chunk (straddling ⇒ intermittent leak); fail-open retained | §4 · P2.8 · §9.4 straddled fixtures |
| 7 | 🟡 | §2.5 factual fix: `RequestMetadata` populated only by `ParseRequest` (zero production callers — dead seam); adapter-seam masking priced out | §2.5 (rewrite) |
| 8 | 🟡 | Storage arithmetic: SELECT 20→22; `dbModelRow`/`scanModels` 20→22; INSERT/UPDATE 19→21 (was correct); inline SELECTs `store.go:857-873`/`:939-955` added; builder-string count re-derived at P1.4 | §3.3 (rewrite) · P1.4 |
| 9 | 🟡 | Clearable chat-kind `shadow_model_id` dropdown in `GroupModelForm` (field had no UI) | §6.2 · P3.5 · Appendix A · Q19 |
| 10 | 🟡 | Cached-resolver kind-branch pinned at `store.go:2292` before the `Credentials==0` fall-through (~:2333); RLock-held, same ops as uncached path (binding writes included — P2 precision fix) | §3.6.1 · P2.4— **cite obsolete post-Am21** (Round 2) |
| 11 | 🟡 | Metering anchors corrected (:908-934 ultimate branch / :1446-1472 stream / :1656-1682 non-stream); ultimate site excluded from member-row metering | §2.6 (rewrite) · §3.7 · P2.9 · §13 Q9 annotation |
| 12 | 🟡 | N-members-per-target ops pattern + rotation-orphans-bindings runbook + FE duplicate-`upstream_model` hint (~10 LOC) | §3.2 · §6.2 · P3.4 · Q10 annotation via Q18 |
| 13 | 🟡 | Explicit: `/v1/messages` meters nothing (pre-existing gap, unchanged) | §3.7 · §3.6.3 |
| 14 | 🟢 | `testhooks.go` cloned 1:1 — all 7 named seams (deterministic race-test promise depends on them) | §3.5 · §9.1 · P2.1 · Appendix A (`testhooks.go`) |
| 15 | 🟢 | Init posture: lazy per-group state on first `GetOrSelect`; NO boot `RebindFromStore` walk; cold-start log line covers it | §3.5 · P2.1 |
| 16 | 🟢 | No M-1 shadow-write dance: 031 columns purely additive; credentials_json dual-write does NOT apply | §3.3 |
| 17 | 🟢 | types.ts citations corrected: kind union `:123`, `ImgGenModel` `:155`, `DecisionModel` `:161` | §6.5 |
| 18 | 🟢 | 9 mutation sites CONFIRMED (research's "12" over-count); credential/token mutations correctly invalidate no model caches — no phantom sites | §6.3 |
| 19 | 🟢 | A7 extended: member `upstream_model` may collide with an operator model ID in `model_hourly_usage` (cosmetic blend) | §12 A7 |
| 20 | 🟢 | Dual-claim edge doc: both-disabled inert; enable-order via G8 scan; single-valued `claims` map assumes API-only writes | §3.8 (recorded placement adjustment: semantics-corner section; §3.4 already covers the G8 scan) |

**Arch §13 open questions 1–3** folded as verification steps: item 1 → P2.11 (imggen-group clean-failure check); item 2 → P3.6 (tabs-test assertion shape); item 3 → P1.4 (SELECT-site enumeration). Item 4 (research path fix) belongs to the research worker — out of scope here.

### Review Round 1 (REJECTED-narrow → fixes applied; no re-review → approver-next routing)

All findings applied in place; **F1 was the approval blocker (critical)**. Numbering stable throughout (no section/task renumbering in this round).

- **F1 (critical)** — Q17 was falsely marked "user-confirmed" at 2 sites; reworded to "proposed default **Q17 — pending user approval**" (§3.6.4) and "**pending as Q17**" (§11 R9); defensive grep confirmed no other confirmed-style wording on Q17–Q20.
- **F2** — stale anchor `ultimatemodel/handler.go:696` → `pkg/proxy/handler.go:696` (grep found exactly 1 occurrence, at §3.6.4; the reviewer's other two cited lines carried F1 wording, not this anchor).
- **F3** — stale section ref §6.7 → §6.6 (§2.7).
- **F4** — R4 mitigation's stale "P2.10 auth tests" (Am1-renumber residue) → "§9.4 auth tests (`handler_groups_test.go` — P2.8/P2.11)"; full P-ref sweep clean (see below).
- **F5** — Appendix A metering anchors corrected to :908-934 / :1446-1472 / :1656-1682 + "(Am11)" tag.
- **N6** — `:243` → `:244` (`h.credEngine.ExcludeAndReselect` anchor, §3.6.3).
- **N7** — byte-equality assertion added to §9.4 (group output differs from upstream only in top-level `model`; non-group byte-equal).
- **N8** — tsc baseline frozen in companion `pkg/ui/frontend/tsc-baseline.txt` (implementation-created; plan text §9.6 + Appendix A entry only — file NOT created at plan time).
- **N9** — §3.6.2: OpenAI streaming inherits winner-locked behavior (no mid-stream member change).
- **N10** — API-key rotation keeps the member key; bindings/cooldowns persist (§3.2; placement adjusted next to the rotation note — recorded).
- **N11** — §3.5: in-flight conversation after restart re-rolls to a fresh member (Q4 cold-start consequence).
- **N12** — stray corrupted fragment ("teness rule: …") deleted (dangling duplicate of the manifest-completeness rule line).
- **N13** — test-file count recounted honestly: 13 new + 10 extended = 23 files (~+1,400 LOC).
- **N14** — this Review Round 1 sub-section appended to Appendix B.
- **N15** — numbering stability confirmed: Q1–Q20, G1–G8, R1–R13, A1–A14, Am1–20, P1.1–P4.5 all unchanged by this round (only in-place wording edits + the Appendix B sub-section).
### Approver Round — polish P1–P7 (APPROVED verdict; light touch only, no structural edits / renumbering / new sections)

- **P1** — anchor drift ×3 fixed (approver-verified): `dbModelRow` → `store.go:467`; ultimate-branch context `:884` → `:888`; inline SELECT leading bounds → `store.go:857-873`/`:939-955` (3 occurrences swept: §3.3, P1.4, Appendix B Am8 log).
- **P2** — §3.6.1 Am10 wording made precise: the cached-path branch performs the SAME operations as the uncached path (`GetOrSelect` binding writes included) under the already-held RLock; no-new-lock-cycle conclusion stands (identical lock-nesting at HEAD). Appendix B Am10 log line aligned.
- **P3** — Appendix A count aligned honestly: ~60 manifest line-items (~37 non-test files); ≈4,100 LOC kept (consistent). **[superseded by Round 2: ≈3,500 LOC / ~33 files]** |
- **P4** — §3.8 corner added: claiming a DISABLED chat model's ID is legal and inert from the listing view; wording adjusted to verified source behavior (`GetModel` has no enabled filter at HEAD — direct requests for the disabled ID route to the servable group) — micro-divergence from the approver's suggested phrasing recorded here.
- **P5** — §3.4 hide mechanism stated: interface-constrained scan consulting the claims index; no per-row model-kind branching.
- **P6, P7** — applied in `decisions.md` by the decisions worker (out of this file's scope).
- **P8, P10** — SKIPPED per approver instruction: self-describing / auto (no plan-text change required).
- **P9** — §3.6.5 cross-protocol asymmetry sentence added: Anthropic's Am1 branch excludes members even for fallback-reached groups (`IsGroup()`-keyed), OpenAI's :1410 guard skips them — same Q16 semantics, different exclusion depth.

- **P6 completion** — the literal "Q5 resolved"/"Q2 resolution" phrases lived in plan.md §4/§3.2 lead-ins (the approver's file label pointed at decisions.md); reworded to "answered (recommended default)" phrasing; decisions.md equivalents ("the plan adopts" ×2) fixed by the decisions worker.

## Appendix C — Amendment Log (Round 2 — Membership Pivot)

Source: `architecture-recommendation.md` "# Round 2 — Membership Pivot" (§R2.0–R2.8; verdict SOUND — adopt). All 11 amendments folded; anchors re-verified in source (`handler_functions.go:149-159`, `handler_anthropic.go:210-219`, `store.go:1597-1604`, `handler.go:1086`, `:395-412`).

- **Am21** 🔴 — applied: §3.6 rewritten (pure-function modelList rewrite; shared helper + parity test; §3.6.1–§3.6.3 superseded banners); §10 P2.4.
- **Am22** 🔴 — applied: §3.4 `GetModelNative` + identity rule; self-shadow legal; §9.4 regression gated FIRST; §3.8 Am28 complements.
- **Am23** 🔴 — applied: §5.1 enforcement pin (bypass BEFORE the `store.go:1597-1604` Internal gate — verified verbatim); P1.5 save-path test.
- **Am24** 🟡 — applied: §3.6.2/§3.6.3 terminal-only, max-ONE re-entry, pre-first-byte, no chaining; P2.6.
- **Am25** 🟡 — applied: §3.7 rewritten (dedup gate; stamp at rewrite; day-1 one row); P2.9; Am3 superseded.
- **Am26** 🟡 — applied: §4 L3 verdict (mask to client-requested string uniformly; day-1 byte-identical iff `internal_model == id`); Am6 carries.
- **Am27** 🟡 — applied: §3.4 + P1.8 (DeleteModel member-scan; credential-side scan disappears).
- **Am28** 🟡 — applied: §3.8 stale-member defense (skip-and-reselect; empty → resolve-failure envelope); P2.7.
- **Am29** 🟡 — applied: §9.4 four equivalence tests; §1 non-goal (no auto-create/bootstrap/seed); P4.2 smoke.
- **Am30** 🟡 — applied: §6.2 chat-kind model picker + weight (−80–100 LOC); one-click wrap pre-fill (~40 LOC); Am9/§6.3/§6.4 unchanged.
- **Am31** 🟢 — applied: §3.5 (zero-credential import guard; `key.go` imported; knob inheritance documented → Q21; two-level hashing spaces); §3.2.

**Supersession map (old → new):** §3.6.1 resolver kind-branch + cached variant → **Am21** · Am1 (Anthropic internal_handler branch) → **moot-by-Am21** · Am2 (`\|` gate + composite key) → **evaporated-by-Am21** · Am3 (winner plumbing) → **replaced-by-Am25** · Am10 (cached-variant placement) → **superseded-by-Am21** · P2.6-R1 (race_coordinator group branch) → **moot** · G5 provider-match divergence (Round-1 form) → **moot** (groups carry no credentials) · Q18 → **superseded-by-Q2-pivot** · Q14 → **pivot-native** (plain model-ID keys) · §3.6.5 R1 asymmetry sentence → **superseded** (Am24 uniform) · chat→group fallback (Q16) → **native** (uniform entry-wise rewrite).

### Review Round 2 (delta — Round-1 residue in live normative positions; wording/replacement only, ZERO design change)

- **C1 (critical)** — §2.6 restated: served member stamped at rewrite; `rc.resolvedModel` (group) and `winner.GetModelID()` (member) are DIFFERENT objects at the metering sites (pre-pivot "group row ID" phrasing removed).
- **C3** — P4.2 retitled "Am29 equivalence tests" (no "smoke"); acceptance = all equivalence tests green in `pkg/proxy/handler_groups_test.go` (zero-groups; `coding[coding]` ×4).
- **C4** — §3.4 JSON backend: `ResolveInternalConfig` twin UNTOUCHED; GetModel equivalent gains claim-first + `GetModelNative` companion.
- **C5** — §3.8 empty-group: resolve-failure envelope AT THE REWRITE SITE (Am28); Q11 clause reworded to Am27 model-deletion scan.
- **C6** — Q13: resolver-intercept narration dropped → `.Internal` predicates + Am21 member-row substitution.
- **C8** — Q20: "(Am3)" → Am25 dedup gate phrasing.
- **I9-I11** — R2/R13/R1 rows rebuilt (malformed trailing cells from the Round-2 splice removed); plain-model-ID keying, P2.4/Am21, modelList-build ordering stated.
- **I12** — Q7: provider-match "deliberate divergence" → MOOT (no credentials; Am23 bypass).
- **I13** — §9.4: self-shadow gating = test-order convention (first declared test in `handler_groups_test.go`).
- **I14** — §9.4: live-edit safety named clauses (i) in-flight-holds-member, (ii) new-picks-see-new-membership, (iii) sticky→removed-drops-and-re-picks.
- **I15** — Q17/Q19/Q20: "arch R1 §12.x" → "arch §12 item 1/3/4".
- **I16** — `store.go:1501-1533` / `config.go:777-815`: ZERO occurrences in plan.md (grep-verified) — likely decisions.md residue; noted for the decisions worker.
- **I17** — off-by-1 rewrite anchors: ALL occurrences → `:149-159` / `:210-219` (8 sites incl. bare forms at §1/§2.2/§3.6/§9.4/Appendix A/C).
- **I18** — Am24: call-site anchor `handler.go:1078` added alongside the :1086 definition cite.
- **I19** — P1.5: Am23 branch placement pinned — RECOMMENDED `validateBeforeWrite` (`store.go:1736`, single production caller). *(rationale corrected by Approver R2 N1 — TWO call sites)* |
- **N20** — `pkg/ultimatemodel/` added to Appendix A NOT-changed.
- **N21** — Am29 count: "5 named cases (1 zero-groups + 4 coding[coding] sub-dimensions)".
- **N22** — Appendix B Am10 line tagged "cite obsolete post-Am21".
- **N23** — P2.8: Am26 streaming buffer placement clarified (at the relay, adjacent to existing per-event parsing).
- *(C2, C6-decisions, C7, I15-decisions — applied by the decisions worker.)*
- **N-extra (Round-2 delta audit trail)** — two additional same-class residue fixes applied in that pass, recorded here: §1 Goals "CRUD + cross-provider member editor" → "CRUD + model-picker member editor"; §8 `models-template.json` example "2 cross-provider members" → "2 model-ID members".
### Approver R2 — fast pass (one blocker + three factual corrections)

- **B1 (blocking)** — §13 Q11 row still carried the Round-1 credential-delete mechanism (removed by Am27); BOTH cells rewritten to the post-pivot design: guard on MODEL deletion (DeleteModel refuses while the ID is any group's member, error naming the group); credential-side scan gone. **Disclosure:** the Review-R2 C5 note claimed this rewording happened — it did not fully land (only the resolver-branch clause of §3.8 was fixed; the Q11 row survived); caught by the approver gate.
- **N1** — "single production caller" rationale INVERTED and corrected: `validateBeforeWrite` has TWO production call sites (AddModel `store.go:1351` + UpdateModel `store.go:1438`) — placement still right (ONE shared chokepoint covering both writes); latent bulk `ModelsManager.Validate()` (`store.go:1561`, zero non-test callers) gap noted. Applied at P1.5 + §5.1.
- **N2** — duplicate `pkg/ultimatemodel/` NOT-changed bullet removed (em-dash/hyphen twin from the N20 insert).
- **N3** — equivalence-test counts reconciled to "5 named cases (1 zero-groups + 4 coding[coding] sub-dimensions)" at Appendix A + P4.2 (§9.4 already correct).
### Execution-time amendments (P2 code-review council)

- **Predicate gap (root-cause mirror of the shipped defect)** — Review of implementation surfaced §3.7 predicate gap — C1 fixed the objects narration, not the predicate: the bare `winner.GetModelID() != reqLog.Model` gate, followed literally, reintroduces non-group fallback double-metering. EVERY live statement (§1, §3.7, P2.9, §13 Q9/Q20) now carries **`group-pick active &&`** (group-pick active = the rewrite fired / served-member stamp present).
- **Scan-site enumeration credit** — the plan's §3.3 touch list omitted `GetFallbackChain` + `GetTruncateParams` SELECT sites and 3 dummy probes; the dev's source-authoritative enumeration found them before they shipped as Scan-arity failures. Dev's call credit: the P1 council vindicated the plan's own "re-derive the exact SELECT-site enumeration from source" instruction (P1.4) — the plan text alone was insufficient.
- **Manifest drift (as-built)** — `race_coordinator.go` WAS modified (manifest declared unchanged); `handler_functions.go` was NOT modified (manifest named it the rewrite site; the rewrite landed via the shared `GroupRewriter` seam at both protocol sites). Appendix A rows corrected above.
