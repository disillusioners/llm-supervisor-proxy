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
- Members are **inline pairs** `{credential_id, upstream_model, weight}` — may **span providers** (deliberate divergence from the credential provider-match invariant, `store.go:1631-1644`).
- **ID-space shadowing**: a group may *claim* one existing chat model's ID (`shadow_model_id`); requests for the claimed ID route to the group and the claimed model is hidden from `/v1/models`; group deletion (or disable) drops the claim → the model reappears.
- **Sticky routing**: conversation-hash affinity (in-memory, 24h sliding TTL — **USER-DECIDED**), reuse `ComputeConversationKey` verbatim with the group ID; member failure ⇒ exclude-and-reselect within the group (clone of `ExcludeAndReselect`); member removal ⇒ drop its bindings.
- New **`pkg/grouplb`** engine — a mechanical clone of `pkg/credentiallb` (weighted-random sticky + cooldowns free), keyed by **member key** (`credentialID|upstreamModel`), per-group state lazily instantiated.
- Pipeline integration at the **resolver seam only**: `ResolveInternalConfigWithAffinity` grows a kind-branch that fabricates the same `ResolvedCredential` — OpenAI race path, Anthropic sequential path, streaming pass-through, tool-call repair, and loop detection are **inherited untouched**.
- New FE **Group Models** tab (CRUD + cross-provider member editor), consistent with Text / ImgGen / Decision tabs; cache quadruple-delete discipline.
- Usage metered **both** under the group name (dashboard continuity) and the served member target (user's presumption), with the decision-style 5s detached meter timeout at the three existing metering sites.

### Non-goals (out)

- **Image-gen and decision paths** — untouched end-to-end (routes, guards, handlers). A group misrouted to `/v1/image_generation` or `/v1/decisions` keeps today's behavior of those guards (groups are neither `IsImageGen` nor `IsDecision`).
- **Client-facing wire changes** — no new endpoint, no new fields, no new auth surfaces. Transparency, not a new API.
- **DB persistence of sticky affinity** — in-memory only (**USER-DECIDED**); restart cold-start is documented behavior (§3.5), a log line, not engineered around.
- **Group-of-groups** — flat membership only (**USER-DECIDED**); G5 forbids nested groups.
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
- ID-keyed resolution sites (all verified): `handler_functions.go:128` (+ fallback list :149-159); `handler.go:338` (chain-internal); `handler_anthropic.go:158/:418` (+ :209-219); `race_executor.go:55/:121/:177/:275`; `race_coordinator.go:1409/:1521`; `/v1/models` `handler.go:245-272` (kind-skip :257-261); FE GET `ui/server.go:748-821` (kind switch :758-789, chat arm :781, default arm :788); `ModelsManager.GetModel store.go:854` + `GetModelByName :936` + strict twins `:1191/:1197/:1205`; `ResolveInternalConfig(WithAffinity)` `store.go:2079-2097/:2170-2267` (+ cached :2269-2293); JSON twin `config.go:1012-1064`; auth gates `handler.go:565/:647`; ultimate `ultimatemodel/handler.go:746-768` (kind-blind, dispatches on `Internal`); modelscache `models.go:400/:427/:454`.
- **Unknown model is NOT an error** on `/v1/chat/completions` — it silently external-passthroughs (`modelList=[originalModel]`, `handler_functions.go:148-165`). A group must therefore be a **real ModelConfig row**, not a resolver-side fiction.

### 2.3 Credential LB engine (`pkg/credentiallb` — the clone source)

- `Engine{mu; models map[modelID]*modelState; ttl=24h sliding idle; sweepInt=5m; defaultCooldown=60s}` (`engine.go:35-65`). Per-model state: `credentials []CredentialRef{ID,Weight,Position}` + prefix-sum weighted selector (`selector.go:16-65`, O(log k), `weight<=0 ⇒ nil`) + `bindings map[convKey]*binding` + `cooldowns map[targetID]time.Time` + per-model RNG.
- Two-level locking E-1 (outer RLock → per-model optimistic → write-upgrade with re-verify, `engine.go:248-282`, `doc.go:42-55`); 5-min janitor (:123-181); `Stop` idempotent; n==1 fast path E-3 (:241-246).
- `ExcludeAndReselect` (`engine.go:609-719`): cooldown from `Retry-After` else 60s; n==1 ⇒ `ReselectNone` no cooldown (:633-635); rebind-only-if-current-matches (:654-679); all-cooling ⇒ `ReselectSoonestExpiry` (:689-699); fall-through policy lives with the caller.
- **Known limitation**: bindings/cooldowns key on `credentialID` only — no upstream-model dimension. A group engine keyed by credential would conflate members sharing a credential (a 429 on target A cools the credential backing B). §3.5 fixes this by keying on the member identity.
- Conversation key: `ComputeConversationKey(modelID, tokenID, firstUserMessage)` = sha256 64-hex (`key.go:40-43`); multimodal via sorted-key canonical JSON (:57-157); empty key ⇒ no binding. Computed at `handler.go:534-548` (OpenAI) / `handler_anthropic.go:296` (Anthropic). Restart = cold (bindings map empty at boot; `RebindFromStore` seeds selectors, not bindings).
- Known flake: `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` flakes ~1/5 under `-race` (pre-existing, project risk note) — the clone inherits the E-1 pattern and should copy the test with the flake's lesson in mind (§9).

### 2.4 Chat pipeline (both protocols)

- **OpenAI** `/v1/chat/completions`: `HandleChatCompletions` `handler.go:393` → `initRequestContext` (`handler_functions.go:62`, 10MB cap, `reqLog.Model` = client string) → `GetModel` :128 → misroute guards (imggen :456-462, decision :474-480) → auth `IsModelAllowed` :555-604 (fail-closed :583-602) → conv key :534-548 → ultimate checks :626-953 → race handoff :1019-1025 → `executeRequest` `race_executor.go:93` (re-fetches `GetModel` per attempt) → internal path: `ResolveInternalConfigWithAffinity` `race_executor.go:278` (reselected) / :303 (normal) → `internalModel = resolved.InternalModel` :333 → `convertToProviderRequest` :381 → upstream.
- **Internal/external dispatch**: `modelConfig.Internal` gates the internal path at ~11 predicates — `race_executor.go:123/:178`, `handler.go:332/:339/:534`, `handler_anthropic.go:277/:419`, `handler_functions.go:174`, `internal_handler.go:190`, `race_coordinator.go:1410` (which additionally requires `len(Credentials) <= 1`), resolver gate `store.go:2172` (+ `:2081`, cached `:2293`). **A group row must carry `Internal=true`** or every one of these treats it as raw-passthrough (§3.6, Q13).
- **Race/fallback**: attempts = main + fallback chain (cap 1, `config.go:838-839`); four row types; credential axis ≻ model axis; rate-limit ⇒ `credFailoverPlanLocked` → `engine.ExcludeAndReselect` (`race_coordinator.go:1465`, wiring :344-351).
- **Anthropic** `/v1/messages`: separate sequential fallback loop `handler_anthropic.go:305-414` (no race coordinator); per-iteration `GetModel` :418 + `ResolveInternalConfigWithAffinity` :441; only a decision misroute guard (:195-207); **response `model` already rewritten to the client-requested name** (`translator/response.go:38` non-stream; `incremental_stream.go:458` message_start); no metering on this path (pre-existing).

### 2.5 Response identity asymmetry (load-bearing for Q5)

- OpenAI path **echoes the upstream `model` field verbatim** (stream relay `handler.go:1172-1184`; non-stream `finalBody` :1587) — a multi-target group would leak varying upstream model names. Anthropic masks to the client name.
- `RequestMetadata{ClientModel, UpstreamModel}` exists in `adapter_openai.go:53-58` but is populated **only** by `ParseRequest`, which has **zero production callers** (dead seam — `handler_functions.go:88` calls only `ToStoreMessages`; handler/race_executor never call it) — corrected per arch R1 Am7; adapter-seam masking is priced out for v1. `WriteStreamEvent` is pure passthrough (:100-106); the streamID normalization cache (`handler_helpers.go:92-95`, "cached from first chunk to maintain consistency across retries/fallbacks") is the in-`requestContext` masking template.

### 2.6 Metering

- Success-only, 3 sites in `pkg/proxy/handler.go` — **anchors corrected per arch R1 Am11** (re-verified in source): `:908-934` (**ultimate re-execution branch** inside `HandleChatCompletions` — the `model := ultimateModelID` context at :888; NOT "stream" as first drafted), `:1446-1472` (true stream, inside `streamResult` :1139), `:1656-1682` (non-stream, inside `handleNonStreamResult` :1538). Each block reads `rc.reqLog.Model` (the client-requested name), `rc.tokenID`, `rc.reqLog.Usage` — **`resolved` (`ResolvedCredential`) is NOT in scope at any of the three** (re-verified), and `winner.GetModelID()` returns the **group row ID**. Bare `context.Background()`, no timeout — the decision commission's 5s detached pattern (`decision/handler.go:89-90`) is adopted on these sites (Q9).

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

### 3.2 Group data model & membership

`ModelConfig` gains two fields (both backends share the struct):

```go
// pkg/models/config.go — added to ModelConfig
Members       []GroupMember `json:"members,omitempty"`        // kind==group only
ShadowModelID string        `json:"shadow_model_id,omitempty"` // ID-space claim
```

```go
// pkg/models/group.go (new)
type GroupMember struct {
    CredentialID  string  `json:"credential_id"`
    UpstreamModel string  `json:"upstream_model"`
    Weight        float64 `json:"weight"`
}
```

- **Member key** (engine + cooldown + binding identity): `credentialID + "|" + upstreamModel`. Unique per G4 (no duplicate members); stable across reorders; embeds the credential so cooldown precision is per-(credential, upstream-model) pair — research-codebase §9.2 item 7 folded in. **Collision gate (arch R1 Am2, 🔴):** the key is ambiguous if a credential ID contains `|` (`("foo","bar|baz")` and `("foo|bar","baz")` both yield `"foo|bar|baz"` ⇒ silent wrong-binding). `CredentialConfig.Validate` (`pkg/models/credential.go:107`, re-verified: non-empty checks only) gains a `|` ban; G3 re-checks it defense-in-depth (§5.1).
- **N-members-per-target ops pattern (arch R1 Am12):** a multi-credential target is expressed as N member rows (one per credential, same `upstream_model`) — legal and cooldown-correct (distinct member keys; weights do double duty as credential weights; prefix-sum composes arithmetically). Documented so operators don't read near-identical rows as a validation accident; per-target credential pooling remains a v2 idea (Q18). **Rotation note:** credential rotation changes the member key ⇒ `OnGroupChanged` drops the affected sticky bindings by design — a runbook sentence, not a defect (§3.5). **API-key rotation (credential ID unchanged) keeps the same member key** — bindings and cooldowns persist across key rotation (Review R1 N10; placement adjusted: member-key stability is defined here, next to the rotation note).
- **Q2 answered (recommended default) — inline membership**: members are `(credential_ref, upstream model name, weight)` pairs, NOT references to model rows. Rationale: (i) it is the user's originally suggested shape; (ii) it dodges the resolver-seam blocker — `resolveWithCredential` (`store.go:2107-2126`, :2118 `actualModel = modelConfig.InternalModel`) hard-assumes one model row = one upstream model string, and member-rows would force nested resolution or a resolver fork; (iii) it keeps ONE lb level (no group→member-model→credential hierarchy); (iv) the member key fixes the credentialID-only conflation (§2.3) at the engine, not with special cases. Members are **never** model rows, so they never appear in `/v1/models` and never need hiding — the member-visibility question dissolves.
- **Q13 — `Internal=true` on the group row (recommended default; research-codebase §9.2 item 13's `Internal=false` REJECTED)**: §2.4 lists ~11 `.Internal` dispatch predicates plus the resolver gate `store.go:2172` (`if modelConfig == nil || !modelConfig.Internal { return ..., false }`). With `Internal=false`, a group would be dispatched to the **external raw-passthrough path** (empty `UpstreamURL`) and every dispatch site would need an `|| m.Kind == KindGroup` extension — ~11 drift-prone edits. With `Internal=true`, all sites route the group into the internal path for free and the resolver kind-branch (§3.6.1) intercepts **before** any `Credentials`/`InternalModel` read, so `resolveWithCredential` never sees a group. G1 enforces it at write time.
- Group rows carry no `Credentials`, no `InternalModel` (G-rules reject them implicitly via the members-only shape; see §5).

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
- **`GetModelByName` untouched** — claims are ID-space; names stay display-only.
- **Group deletion** drops the row → the claim index entry dies with it → the shadowed model reappears in `/v1/models` and resolves normally again. No tombstone machinery.
- **Disabled group ⇒ inactive claim** (§3.8): a disabled group must not hijack traffic it isn't serving.
- **Shadow target deleted first**: tolerated, harmless — the claim keeps redirecting that (now nonexistent) ID to the group, which is a defensible takeover semantic; `/v1/models` has nothing to hide (the row is gone). No special machinery; documented behavior.
- **`/v1/models`** (`handler.go:245-272`): keep the kind-skip :257-261 as-is (groups pass); add a second pass filtering out IDs present in the (enabled-group) claim index. Fixture precedent: `TestHandleModels_FilterImageGen` (`pkg/proxy/handle_models_imggen_test.go:86-124` + :134/:170/:196) and the FE-side chat-arm-leak regression `pkg/ui/handlers_models_kind_test.go` (:283, `DefaultArm` :218). Hide mechanism: an **interface-constrained scan** — the listing pass consults the claims index to skip claimed IDs (no per-row model-kind branching) (Approver R2 P5).
- **modelscache** — package `pkg/modelscache` (path corrected per arch R1 Am4). `GetModel` caches per-requested-ID (`models.go:400/:427/:454`) **and writes negative entries** (`negByID`, :375) under `ModelsNegTTL` (60s default, :55-56); `AddModel`/`UpdateModel`/`RemoveModel` (:643/:665/:687) evict **only by the row's own ID**. So a claim write (group create/update/delete; shadow change) must run a **dedicated claim-eviction hook**: evict `modelsByID[old shadow]` + `modelsByID[new shadow]` **and** `negByID[old shadow]` + `negByID[new shadow]` — otherwise a cached chat-row or a 60s negative entry for the shadowed ID **bypasses the manager's claim logic** entirely. Claim authority vs `CachedModelsConfig.GetModel` ordering stated: the eviction hook runs synchronously in the same `OnModelChanged` extension as the claim-index rebuild (P2.2), before any subsequent resolve can read a stale entry.
- **Auth (Q1 tail)**: `IsModelAllowed` (`token.go:133-143`) evaluates the **client-requested ID string** and is untouched. A token whose `allowed_models` lists the shadowed ID reaches the group through that alias (the string matches); a token listing only the group's own ID reaches it only via the group ID; requests for the shadowed ID from a token that doesn't list it are denied fail-closed exactly as today. Auth is orthogonal to kind — no `AllowedGroups` field (research-codebase §9.2 item 2's simplest invariant adopted).
- **JSON backend** (`config.go:1012-1064`): its `ResolveInternalConfig` twin gets the same kind-branch; its GetModel equivalent gets the same claim check. Both backends, one design.

### 3.5 LB engine: `pkg/grouplb` (clone of `pkg/credentiallb`)

New package, ~80% mechanical clone. The ~7 known couplings (per §2.3) with their resolutions:

| Coupling (credentiallb) | grouplb resolution |
|---|---|
| `CredentialRef{ID,Weight,Position}` element type | `MemberRef{Key, Weight, Position}` — `Key = credentialID+"|"+upstreamModel` |
| bindings/cooldowns keyed by credentialID | keyed by **member key** (fixes the conflation; research-codebase §9.2 item 7); `|` banned in credential IDs at `CredentialConfig.Validate` + G3 (Am2 — collision gate) |
| `OnCredentialDeleted` cross-scan (:429-451) | kept as a defensive drop-bindings-and-cool-member walk (Q11 makes orphaning impossible via API; defense mirrors `store.go:2051`) |
| `OnModelChanged` filter-survivors / nil-delete (:360-389) | `OnGroupChanged(groupID, survivors)` — member removal drops that member's bindings (**Q4**: re-stick to a fresh pick next request) |
| E-3 n==1 fast path (:241-246) | cloned verbatim |
| Event constants / log prefixes | renamed (`grouplb:` prefix) |
| 24h sliding TTL + 5-min janitor + 60s default cooldown | cloned verbatim — **USER-DECIDED minimalism** (no new strategies, Q3) |

- One process-wide `grouplb.Engine` owned by `ModelsManager` alongside the `credentiallb.Engine`; **per-group state lazily instantiated** on first `GetOrSelect(groupID, convKey)` (research-codebase §9.2 item 6 folded in — per-group binding/cooldown isolation is inherent to the clone's per-model map).
- **Sticky affinity (Q4)**: conv key = `ComputeConversationKey(groupID, tokenID, firstUserMessage)` — **verbatim reuse** with the group ID (the function already takes the model ID as a parameter; `handler.go:534-548` / `handler_anthropic.go:296` pass the group row's ID because that's what `rc.resolvedModel` holds). Bindings are group-scoped by construction. Empty key ⇒ fresh pick per request (cloned semantics). In-memory only (**USER-DECIDED**); restart cold-start documented (§3.5).
- **Failure handling**: rate-limit/cred-failover on the winner member ⇒ `ExcludeAndReselect(groupID, convKey, memberKey)` — cloned semantics (`engine.go:609-719`): cooldown from `Retry-After` else 60s, n==1 ⇒ reselect-none, rebind-only-if-current-matches, all-cooling ⇒ soonest-expiry. The **new** pick becomes the sticky binding (re-stick), and the fall-through policy (serve from a cooling member rather than fail) stays with the caller exactly as in credentiallb.
- **Restart cold-start**: after restart, bindings are empty; the first request per conversation establishes a fresh binding (possibly a different member). Surfaced as a **single startup log line** documenting the cold-start (research-codebase §9.2 item 10 folded in) — no per-resolve warning, no config toggle. An **in-flight conversation** resuming after a restart re-rolls to a fresh member on its next request (its binding is gone) — a documented cold-start consequence of Q4's USER-DECIDED in-memory affinity (Review R1 N11).
- **Testhooks 1:1 (arch R1 Am14):** the clone carries `pkg/credentiallb/testhooks.go` (196L) verbatim in structure — all 7 test seams: `InjectAllCoolingForTest`, `cooldownUntil`, `bindingBoundAtForTest`, `forceCooldownExpiryForTest`, `InjectPreconditionStateForTest`, `InjectSingleCredExclusionForTest`, and the sweepHook install. The §9.1 deterministic (non-sleep) race-test promise is **undeliverable without them** — they are part of the clone contract, not optional scaffolding. New-file list: `doc.go`, `engine.go`, `selector.go`, `binding.go`, `key.go`, `events.go`, `testhooks.go`.

### 3.6 Pipeline integration

#### 3.6.1 Resolver seam (the ONE integration point)

`ResolveInternalConfigWithAffinity(modelID, convKey)` (`store.go:2170`) gains a kind-branch **immediately after the Internal gate** (`store.go:2172` passes because G1 forces `Internal=true`), before the nil-engine / fast-path / engine arms:

```
if modelConfig.IsGroup():
    members = validMembers(modelConfig)          // G-rule-clean by construction
    if len(members) == 0: return {}, false       // defense-in-depth; §3.8
    memberKey, newlyBound = groupEngine.GetOrSelect(groupID, convKey)
    cred = m.GetCredential(member.CredentialID)
    if cred == nil: return {}, false             // Q11 makes this unreachable via API
    return ResolvedCredential{
        Provider: cred.Provider, APIKey: cred.APIKey, BaseURL: cred.BaseURL,
        InternalModel: member.UpstreamModel,     // the ONLY structural difference
        CredentialID: cred.ID, NewlyBound: newlyBound,
    }, true
```

The `ResolvedCredential` contract is unchanged, so everything downstream — `internalModel = resolved.InternalModel` (`race_executor.go:333`), `convertToProviderRequest`, adapters, race, stream buffers, heartbeat, tool-call repair, loop detection — is **inherited untouched**. Streaming pass-through (v1 core) is exactly this: nothing in the streaming stack knows the group exists. **Cached variant placement (arch R1 Am10):** the same kind-branch goes into `ResolveInternalConfigWithAffinityCached` at `store.go:2292` — **before** the `Credentials==0` fall-through (~`:2333`), which would otherwise degrade a group's empty `Credentials` to `ok=false`. The cached-path RLock is already held across the branch; the branch performs the SAME operations as the uncached path — `GetOrSelect` binding writes included (Approver R2 P2: the first draft's "pure compute, no engine writes" was imprecise) — so the no-new-lock-cycle conclusion stands: identical lock-nesting exists at HEAD.

#### 3.6.2 OpenAI race path

- `GetModel` sites find the group row by ID or claim (§3.4). `modelList = [groupID]`; fallback chain is empty (G6), so no fallback rows.
- Per-attempt re-fetch (`race_executor.go:55/:121/:177/:275`) re-finds the group; `Internal=true` keeps attempts internal.
- **Rate-limit failover**: `credFailoverPlanLocked` (`race_coordinator.go:1465`) gains one branch — if the failing model row `IsGroup()`, call `groupEngine.ExcludeAndReselect` instead of the credential engine. (The guard at :1410 `len(Credentials) <= 1` would otherwise skip exclusion for groups, since groups carry no `Credentials`.) Wiring rides the existing :344-351 path.
- Streaming inherits today's winner-locked behavior (Review R1 N9): once the winning member streams its first byte, no mid-stream member change occurs — the winner is locked for the life of the response (mirrors the Anthropic `headersSent` first-byte lockout, §3.6.3).
- Ultimate checks (:626-953): groups pass through as the *request's* model (ultimate **trigger** logic is per-message-hash and kind-agnostic — milestones `[5,10,20,30,40]`, `ultimatemodel/handler.go:24/:101`); groups can never be the ultimate *target* (§3.6.4).

#### 3.6.3 Anthropic sequential path — resolver-served, PLUS a group-aware failover branch (arch R1 Am1, 🔴)

The loop's per-iteration `GetModel` (:418) + `ResolveInternalConfigWithAffinity` (:441) hit the same seam, so group **resolution** is free; the response `model` is **already rewritten to the client name** on both non-stream and stream paths (§2.4), so Anthropic-side masking is free. **But "zero code changes" (first draft) was FALSE at HEAD — re-verified verbatim:** the shared internal failover path in `pkg/proxy/internal_handler.go` guards `if len(h.config.Credentials) <= 1 { return err }` at **:240** — a group carries `Credentials == 0`, so the **first member 429 fails the whole request** despite healthy members. Even past the guard, `:244` calls the **credential** engine (`h.credEngine.ExcludeAndReselect`) which holds no group state, and the retry rebuilds from `h.config.InternalModel` (empty for groups) ⇒ a guaranteed second failure.

**Required branch (~20–30 LOC + test, new P2.7):** in the same function, add an `IsGroup()` arm before the credential fallback —
1. skip the `len(Credentials) <= 1` guard for group-kind configs (a group's member count is its capacity, not `Credentials`);
2. call `grouplb.ExcludeAndReselect(groupID, conversationKey, memberKey(resolved.CredentialID, resolved.InternalModel), providerErr.RetryAfter)` — the member-shaped analogue of the credential call;
3. rebuild the retry from the **member-shaped resolver result** (re-resolve via the kind-branch / the returned member ref), **NOT** `h.config.InternalModel`;
4. preserve the existing `ReselectNone` / `ReselectSoonestExpiry` / healthy log-and-continue semantics (mirroring :250-262).

Without this, groups on `/v1/messages` are strictly worse than the chat models they shadow. Streaming is unaffected by the branch: first-byte fallback lockout at `handler_anthropic.go:332-334` (`headersSent` break) means no mid-stream member change — the branch only fires before bytes flow. `/v1/messages` still meters nothing (§3.7, arch R1 Am13). The imggen-guard gap at `:192-194` remains documented out-of-scope as today.

#### 3.6.4 Ultimate switching (Q6)

G7 forces `ExcludeFromUltimateSwitching == true` for groups (D9 precedent `config.go:283-285`; write-time UI gates `server.go:1058-1065` POST / `:1327-1332` PUT mirrored — the payload gate is generic and needs only the G7 mirror). The runtime gate (`pkg/proxy/handler.go:696` checks only the flag — stale anchor fixed, Review R1 F2) then never selects a group as the re-execution target, and the kind-blind internal dispatch (`:746-768`, on `Internal`) never sees one. **Claimed models lose ultimate switching while claimed (arch R1 Am5):** while X is claimed by an enabled group, claims-first `GetModel(X)` returns the **group row** — which carries `ExcludeFromUltimateSwitching=true` (G7) — so X's ultimate switching silently stops for tokens pinned to X until the claim is dropped (group deleted/disabled/edited). Behaviorally defensible (the group takes over X's identity wholesale); documented semantic (Am5); proposed default **Q17 — pending user approval** (Review R1 F1: was falsely marked user-confirmed). Residual: `X-Force-Ultimate-Model` bypasses exclusion by design — an operator forcing ultimate onto a group ID gets the external-passthrough path with an empty `UpstreamURL` (fails fast); accepted, documented (§11 R9).

#### 3.6.5 Cross-kind interactions

- **Chat→group fallback entries**: a chat model's `fallback_chain` MAY reference a group ID — v1 permits it; the fallback row re-resolves per attempt through the same seam and works. (Group→group fallback is impossible — G6 gives groups no chain; research-codebase §9.2 item 14's broader question stays a v2 candidate, §14.) Note: member-level exclusion does not fire for a group reached *via fallback* (the :1410 guard skips `Credentials`-empty rows) — such attempts just fail over to the next chain row; the primary group path has full exclusion (§3.6.2). Documented posture. **Cross-protocol asymmetry (Approver R2 P9):** on `/v1/messages`, the Am1 failover branch (§3.6.3) DOES exclude-and-reselect members even for a fallback-reached group — the branch keys on `IsGroup()`, not on how the model was reached — whereas the OpenAI race path skips member exclusion for fallback-reached groups (the :1410 guard posture above). Same Q16 semantics (fallback permitted on both protocols), different member-exclusion depth.
- **Image-gen / decision paths**: untouched (§1 non-goals). Groups are neither kind, guards neither fire nor need to.

### 3.7 Metering (Q9 — **both** rows; plumbing per arch R1 Am3)

**Plumbing reality (🔴 corrected):** the first draft metered `resolved.InternalModel` — but `resolved` (`ResolvedCredential`) is **not in scope at any of the 3 metering sites** (re-verified: the blocks read only `rc.reqLog.Model` / `rc.tokenID` / `rc.reqLog.Usage`); resolution happens per-attempt in `race_executor.go:303` (lock-free write :310-311) and `winner.GetModelID()` returns the **group row ID**. Required plumbing (~15–25 LOC + test, P2.9): either a **lock-protected `GetResolved()` getter on `upstreamRequest`** (house W4 discipline) or **stamp the winner's `resolved.InternalModel` + member key into `rc` post-`WaitForWinner`**. The member row is gated on **winner-row-kind == group && resolvedOK** — which meters fallback-reached groups correctly for free (Q20).

At the three corrected sites (§2.6 anchors — `:908-934` ultimate branch, `:1446-1472` true stream, `:1656-1682` non-stream), for group-kind winners:

1. Existing row: `IncrementModelUsage(clientRequestedName)` — unchanged behavior (dashboard continuity; `rc.reqLog.Model` already records it). Written at ALL three sites, **including the ultimate re-execution branch**.
2. New row: `IncrementModelUsage(servedMember.InternalModel)` — the actually-served member target (the user's presumption). Same-named upstream models across providers blend in `model_hourly_usage`; provider-qualified usage keys are a v2 candidate (§14); collision with an operator model ID is a cosmetic cross-feature blend (§12 A7).
3. **The ultimate site (:908-934) is EXCLUDED from member-row metering** (arch R1 Am11) — the ultimate target was served, not a group member; group-name row only there, documented.
4. Both writers run under a **detached context with a 5s timeout**, adopting the decision pattern (`decision/handler.go:89-90`) on sites we already touch — chat's bare `context.Background()` (no timeout) is upgraded in the same edit (bounded behavior change, §11 R10; touches ALL chat traffic, not just groups). Token-level `Increment` unchanged. `writeMu` exclusivity holds: two `IncrementModelUsage` calls are serialized UPSERTs on distinct `model_id` rows.
5. **`/v1/messages` meters nothing** (arch R1 Am13): zero `counter.` refs in `handler_anthropic.go` — a pre-existing Anthropic-path gap, explicitly unchanged by this commission (group requests on that route write no usage rows).
6. Events: the existing `model_credential_selected` publish site (`race_executor.go`, W-1: fire only on `newlyBound`) gains the kind-split — groups emit **`model_member_selected`** with `{group_id, member_key, upstream_provider, upstream_model}` (research-codebase §9.2 item 11 folded in; non-group traffic unchanged).

### 3.8 Semantics corner (Q12)

- **Enable/disable**: `Enabled=false` group ⇒ claim inactive (shadowed model reappears and resolves normally) AND group absent from `/v1/models` (existing enabled-filter `:251`). Direct-named disabled group resolves exactly as a disabled chat model does today (no new gate — parity).
- **Empty group at runtime**: impossible via API (G2 enforces ≥1 member at every write; Q11 blocks credential deletion that would orphan a member) and via config file (`ModelsConfig.Validate` runs the same G-rules). Defense-in-depth: the resolver kind-branch returns `ok=false` on zero valid members → the request fails with the normal resolve-failure envelope, never a panic.
- **Group edits**: ID is immutable identity; member/weight/claim edits take effect immediately (claim index + `OnGroupChanged` + cache invalidation ride the write path).
- **Dual-claim edges (arch R1 Am20):** G8's uniqueness scan blocks two *enabled* groups claiming one ID at write time; two *disabled* claims are inert (no index entry — claims activate only on enabled groups); enable-ordering resolves through the same scan. The single-valued `claims` map tolerates inconsistency only via non-API writes (hand-edited config file) — assumed away, one line.
- **Claiming a DISABLED chat model's ID (Approver R2 P4):** the claim is legal (G8 checks existence + kind, not enabled state). A disabled target is already hidden from `/v1/models` (enabled-listing), so the claim is inert from the listing view while still owning the ID-space; direct requests for the disabled ID route to the group (claim-first, §3.4) — harmless, since the group itself is servable. Wording note: direct resolution of a disabled chat model is NOT refused at HEAD (`GetModel` has no enabled filter, verified) — the claim merely redirects it to a servable row, which is the defensible reading.

---

## 4. Wire Contract

**The external API is UNCHANGED.** Transparency statement, binding for implementation:

- No new routes. No new request/response fields. No new auth surfaces. No client migration of any kind.
- A client that today sends `model: "<chat-model-ID>"` (or a claimed ID) sends the same string after this feature; groups respond with normal OpenAI chat wire (streaming and non-streaming), including tool calls, reasoning fields, usage — byte-shapes identical to any internal chat model's responses.
- **Q5 answered (recommended default) — mask, don't leak**: group responses have their `model` field rewritten to the **client-requested name** (the group ID or claimed ID), restoring single-model appearance; multi-target groups otherwise leak varying upstream names (§2.5). Implementation, group-kind-only:
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
| G2 | `len(Members) >= 1`; member count cap 16 (reuse `MaxCredentialRefs`, `config.go:84`) | ≥1 keeps the group live at every write (≥2 rejected as paternalism; a one-member group is a valid staging state and the engine's n==1 fast path serves it). |
| G3 | Every member: `CredentialID` references an existing credential AND contains **no `|`** (member-key collision gate, defense-in-depth behind the `CredentialConfig.Validate` ban — arch R1 Am2); `UpstreamModel` non-empty; `Weight > 0` (mirrors `store.go:1615-1617`) | Basic integrity; no invented weight ceiling. |
| G4 | No duplicate members (same `CredentialID` + `UpstreamModel` pair) | Member-key uniqueness (§3.2); mirrors `store.go:1618-1621`. |
| G5 | Members MAY span providers — and `validateModelAgainstCredentials`' provider-match invariant (`store.go:1631-1644`) must NOT apply to groups | **Deliberate divergence — this is the feature.** Group rows skip the credential rule-set entirely; a separate `validateGroupMembers` implements G2–G4 (research-codebase §9.2 item 3's G-B shape: room for future per-member rules). |
| G6 | `FallbackChain` empty; `SecondaryUpstreamModel == ""`; `PeakHourEnabled == false`; `ReleaseStreamChunkDeadline == 0` | imggen/decision posture; makes group→group fallback impossible (research-codebase §9.2 item 14 denied for v1 → §14). Cut features are v2 candidates. |
| G7 | `ExcludeFromUltimateSwitching == true` (explicit, not defaulted) | D9 precedent (`config.go:283-285`) — the image-gen doc/code drift is NOT inherited. |
| G8 | If `ShadowModelID != ""`: target exists, is **chat-kind**, is not `KindGroup` (flat — **USER-DECIDED**), is not the group's own ID, and **no other enabled group claims the same ID** (uniqueness scan) | Claim integrity; ambiguity-proof. Claiming a member of the same group is allowed and harmless (client requests for it route to the group; member resolution inside the engine is by explicit key and never consults claims) — documented, not policed. |

Enforcement: `validateGroupRules` runs from the shared seam `models.ValidateKindRules` (JSON `config.go:971` + DB `store.go:1719`); the DB write path calls `validateGroupMembers` instead of `validateModelAgainstCredentials` for `kind==group` rows; the G8 uniqueness scan needs the model collection (available at both write paths and both UI mirrors).

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
- **`GroupMemberEditor.tsx`** — new editor on the `MultiCredentialEditor.tsx` (225L) pattern: rows of `{credential dropdown, upstream-model text input, weight number input, remove}`, row add defaults, MIN 1, MAX 16. **Cross-provider: `isOptionDisabled` lifts the same-provider constraint (:82-93) and keeps only the duplicate-pair check (:76-81).** Provider badge per row sourced from the credential list (no BE round-trip). **Duplicate-`upstream_model` hint (arch R1 Am12-FE):** a non-blocking inline hint when two rows share an `upstream_model` (~10 LOC) — that's the legal N-members-per-target pattern (§3.2, Q18), so warn, don't block. Layout: rows-in-form (decision precedent uses pills-with-edit-modal; rows are simpler here because a member has 3 fields — research-codebase §9.2 item 12's alternative noted, rows chosen). Free-text `upstream_model` is v1-acceptable (DecisionModelForm precedent).
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

- New files: `pkg/store/database/migrations/{sqlite,postgres}/031_add_group_fields.{up,down}.sql` (4 files). Up: `ALTER TABLE models ADD COLUMN members_json TEXT;` + `ADD COLUMN shadow_model_id TEXT;` (dialect-appropriate form; both nullable, no backfill — legacy rows are chat/imggen/decision, all member-free).
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

- `engine_test.go`: sticky bind→rebind on reselect; `ExcludeAndReselect` (cooldown from `Retry-After`, n==1 no-cooldown, all-cooling soonest-expiry); member-removal drops bindings (`OnGroupChanged`); janitor TTL sweep; `Stop` idempotent; concurrent `GetOrSelect` under `-race` (copy the TOCTOU/cooling-reverify scenario — the credentiallb original flakes ~1/5 under `-race`; the clone's test must pin the E-1 re-verify semantics **deterministically via the cloned `testhooks.go` seams (Am14) — injected clock + serialized upgrade, never sleeps**).
- `selector_test.go`: prefix-sum weights (O(log k)), `weight<=0 ⇒ nil`, single-member fast path.
- `binding_test.go` / `key_test.go`: member-key uniqueness; empty conv key ⇒ no binding, fresh pick.

### 9.2 `pkg/models` — rules + claims

- `group_rules_test.go`: G1–G8 each violation + the valid fixture; `validateGroupMembers` (span-provider member set VALID — the divergence pin); G8 uniqueness across two groups.
- Claim index unit tests: build/rebuild on add/update/delete; disabled group ⇒ inactive claim; shadow-target-deleted tolerated.

### 9.3 `pkg/store/database` — persistence

- Migration: up/down on both dialects; `schema_migrations` contains `"031"`; registry-append presence test.
- Column-count pins `querybuilder_test.go:239/:262/:275` bumped to 21.
- `store_group_test.go`: group CRUD round-trip (`members_json` + `shadow_model_id`); **claim-aware `GetModel`** (claimed ID → group row; strict twins still return the real row — THE admin-bypass test); dangling credential ref blocked at delete (Q11, `ErrCredentialInUse`-family error naming the group); resolver kind-branch returns member `ResolvedCredential` with `InternalModel = upstream_model`; `OnGroupChanged` binding drop via the manager.

### 9.4 `pkg/proxy` — pipeline

- `handle_models_group_test.go`: `/v1/models` lists an enabled group + **hides the claimed model** (fixture precedent `handle_models_imggen_test.go:86-124`); disabled group ⇒ claimed model reappears.
- `handler_groups_test.go`: group request end-to-end against a fake upstream — member picked by weight; sticky second request hits same member (same conv key); member-failure ⇒ exclude-and-reselect; **stream + non-stream `model` field masked to the client-requested name** (Q5, per-SSE-event buffering per Am6); misroute guards still pass groups (negative guards: imggen/decision models still rejected — no regression); auth: token listing the shadowed ID reaches the group; token not listing it is denied fail-closed; unknown-model passthrough unchanged.
- Byte-equality assertion (Review R1 N7): for a group request, output differs from the upstream bytes ONLY in the top-level `model` field (masked per Q5); for non-group requests, relayed bytes are byte-equal to upstream.
- `internal_handler_group_failover_test.go` (Am1): fake upstream returns 429 with `Retry-After` on member A — assert the group branch lifts the `:240` guard, calls `grouplb.ExcludeAndReselect` with the member key, and the retry rebuilds from the member-shaped result (NOT `h.config.InternalModel`); assert single-member group still fails after one attempt (reselect-none); assert credential-path behavior unchanged for chat models.
- Existing suite must stay green: `go test ./pkg/proxy/... ./pkg/models/... ./pkg/store/... ./pkg/grouplb/... ./pkg/credentiallb/... ./pkg/ui/...`.

### 9.5 `pkg/ui` — BE API + parity

- `kind_rules_parity_test.go` extension (§5.3) — G-case matrix across all three sites.
- `handlers_models_kind_test.go` extension: `?kind=group` arm; **chat arm excludes groups** (the chat-arm-leak regression class, :283 precedent); POST/PUT mirrors reject G-violations with 400; G7 write-gate 400s on `exclude_from_ultimate_switching=false`.

### 9.6 Frontend (vitest + happy-dom, `src/**/__tests__`)

- `hooks/__tests__/useGroupModels.test.ts`: cache key `'group-models'`; `?kind=group` URL; quadruple delete on all three mutations.
- Extend `useModels.test.ts` / `useImgGenModels.test.ts` / `useDecisionModels.test.ts`: pinned key set now **four** keys.
- `components/config/__tests__/`: `GroupModelsTab.test.tsx`, `GroupModelForm.test.tsx` (payload stamping, submit guard), `GroupMemberEditor.test.tsx` (cross-provider dropdown enabled, dup pair disabled, MIN 1 / MAX 16, weight>0); extend `ModelsTab.chatFilter.test.tsx` (group excluded from chat list) and `components/__tests__/SettingsPage.tabs.test.tsx` (11th tab id/aria); extend `components/tokens/__tests__/TokenForm.test.tsx` (both pickers exclude groups).
- tsc: `npx tsc --noEmit` ≤ 26 errors / 11 files; the baseline is FROZEN in a companion file `pkg/ui/frontend/tsc-baseline.txt` (created by the implementation — Review R1 N8) so conformance is verifiable without re-running tsc.

---

## 10. Phased Breakdown

Four phases (user directive: 3–4). Strict order P1 → P2 → P3 → P4; no phase starts before the previous exit criterion is met.

| Phase | Name | Objective | Tasks | Coupling | Status |
|---|---|---|---|---|---|
| **P1** | Kind + storage + group CRUD validation | The kind system knows `"group"`; migration 031 lands (registry + tests); G1–G8 enforced at all three sites; a group can be saved via both write paths; claim columns exist. | 8 | tight with P2 (resolver reads these rows); independent of P3 | pending |
| **P2** | `pkg/grouplb` + resolution/shadowing + pipeline | The engine exists and is wired into `ModelsManager`; claims redirect client resolution; `/v1/models` hides claimed models; OpenAI race path + Anthropic path (incl. the group-aware failover branch) + failover + metering + masking serve groups end-to-end. | 11 | tight with P1; independent of P3 | pending |
| **P3** | Frontend: Group tab + editor + cache discipline | A user can CRUD groups with cross-provider members from the dashboard; chat tab and token pickers exclude groups; quadruple cache delete everywhere. | 8 | tight with P1 (FE API surface); independent of P2 internals | pending |
| **P4** | Tests + docs | Full suite green; template/docs/blueprint updated; ready for user-authorized ship. | 5 | depends on P1+P2+P3 | pending |

### P1 — Kind + storage + group CRUD validation (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P1.1 | Add `KindGroup`, `IsGroup()`, `ModelConfig.Members` + `.ShadowModelID` fields, `GroupMember` type (`pkg/models/config.go` + new `pkg/models/group.go`); **ban `|` in `CredentialConfig.Validate` (`pkg/models/credential.go:107`) — member-key collision gate (Am2)** | none | `go build ./pkg/models/...` passes; `IsGroup()` true iff `Kind=="group"`; `Validate()` rejects credential IDs containing `\|` (unit case) |
| P1.2 | `ValidateKindRules` group arm + `validateGroupRules` (G1–G8) + `validateGroupMembers` (G2–G4); extend default-arm string `config.go:188` | P1.1 | `go test ./pkg/models/...` — new `group_rules_test.go` green (G1–G8 violations + valid cross-provider fixture) |
| P1.3 | Migration 031 (4 SQL files) + registry append + `schema_migrations`-contains-`"031"` test | P1.1 | `go test ./pkg/store/database/...` green; up/down clean on both dialects |
| P1.4 | Storage plumbing: SELECT/scan/write/`dbModelRow` (+2 cols everywhere per §3.3 table — **including the inline SELECTs at `store.go:857-873` + `:939-955`**); bump `querybuilder_test.go` pins to 21; **re-derive the exact SELECT-site enumeration from source** (arch Open Question 3 — docs disagree 8/12/16/18; the pins are the lock) | P1.3 | `go test ./pkg/store/database/...` green; group row round-trips `members_json` + `shadow_model_id`; derived SELECT-site list recorded in the execution report |
| P1.5 | DB write path: `kind==group` rows run `validateGroupMembers` instead of `validateModelAgainstCredentials`; G8 uniqueness scan at both write paths; JSON-backend twin (`config.go:971` path) same | P1.2, P1.4 | Saving a valid group succeeds via DB and JSON backends; G-violations 400/error at both |
| P1.6 | UI POST/PUT mirrors: `?kind=group` whitelist entries + inline G1–G8 400 blocks + G7 write-gate stamps (`server.go:889-895` pattern, ~:1066 / :1198-1205 pattern) | P1.5 | POST/PUT with G-violations → 400 house envelope; valid group → 200 |
| P1.7 | Parity-test extension: G-case matrix + `validateGroupRulesCaller` + `addGroupCreds` fixture (§5.3) | P1.5, P1.6 | `TestKindRules_Parity_AllThreeSitesInSync` covers 4 kinds × 3 sites and fails loudly on drift |
| P1.8 | Q11 block-at-delete: credential delete scans group members (`ErrCredentialInUse`-family naming the group), DB + JSON paths | P1.4 | Deleting a credential referenced by any member → blocked with group-naming error; after member removal → delete succeeds |

**P1 exit criterion (8 tasks)**: A `kind="group"` row with 2 cross-provider members saves via UI API and both backends; G1–G8 enforced identically at 3 sites (parity test green); migration 031 applied with registry test green; `go build ./...` passes.

### P2 — `pkg/grouplb` + resolution/shadowing + pipeline (11 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P2.1 | Create `pkg/grouplb` (`doc.go`, `engine.go`, `selector.go`, `binding.go`, `key.go`, `events.go`, **`testhooks.go` — the 7 seams, Am14**) — clone per §3.5 mapping table; member-key = `credentialID+"|"+upstreamModel`; **lazy per-group state, NO boot `RebindFromStore` walk (Am15)** | none | `go test ./pkg/grouplb/...` green (§9.1 suite incl. deterministic TOCTOU-style race test via testhooks); `go vet ./pkg/grouplb/...` clean |
| P2.2 | Wire engine into `ModelsManager` (construction, janitor lifecycle, `Stop`), `OnModelChanged` extension → claim-index rebuild + `OnGroupChanged` + **`pkg/modelscache` claim-eviction hook: evict `modelsByID[old/new shadow]` AND `negByID[old/new shadow]` (60s negative-TTL window — Am4)** | P2.1, P1.4 | Manager tests: index rebuilt on group CRUD; both positive and negative cache entries evicted for old+new shadow IDs; eviction precedes any post-write resolve |
| P2.3 | Claim-aware `GetModel` (`store.go:854`): claims-first for enabled groups; strict twins untouched; JSON twin same | P2.2 | §9.3 claim tests green — claimed ID → group; `getModelStrict` → real row |
| P2.4 | Resolver kind-branch in `ResolveInternalConfigWithAffinity` **AND the cached variant — placed at `store.go:2292` BEFORE the `Credentials==0` fall-through ~`:2333` (Am10)** | P2.2, P1.5 | `store_group_test.go`: member `ResolvedCredential{InternalModel: upstream_model}` returned from both variants; empty-members defense returns ok=false |
| P2.5 | `/v1/models` second-pass shadow-hide (`handler.go:245-272`); groups stay listed | P2.3 | `handle_models_group_test.go` green (§9.4) |
| P2.6 | Rate-limit failover branch: group rows → `groupEngine.ExcludeAndReselect` at `race_coordinator.go:1465` (+ :344-351 wiring) | P2.4 | Member failure rebinds to a fresh member; sticky binding follows the new pick |
| **P2.7** | **NEW (arch R1 Am1, 🔴) — Anthropic group-aware failover branch in `pkg/proxy/internal_handler.go` (~:239-250)**: lift the `len(Credentials) <= 1` guard for `IsGroup()` configs; call `grouplb.ExcludeAndReselect(groupID, convKey, memberKey(resolved.CredentialID, resolved.InternalModel), retryAfter)`; rebuild the retry from the member-shaped resolver result, NOT `h.config.InternalModel`; preserve ReselectNone/SoonestExpiry semantics | P2.4 | `internal_handler_group_failover_test.go` green (§9.4): member-429 fails over to a healthy member on `/v1/messages`; chat-model credential path byte-unchanged |
| P2.8 | Q5 masking (arch R1 Am6): `maskModelField` — non-stream `finalBody` patch + **per-complete-SSE-`data:`-event buffered patch** in the relay (NEVER per raw read-buffer chunk — straddling ⇒ intermittent leak), group-kind-only, fail-open-to-verbatim | P2.4 | `handler_groups_test.go`: stream + non-stream responses carry the client-requested name incl. straddled-event fixtures; non-group responses byte-unchanged (regression assert) |
| P2.9 | Metering (arch R1 Am3, 🔴): **winner-based plumbing** — lock-protected `GetResolved()` on `upstreamRequest` or `rc` stamp post-`WaitForWinner`; member row gated on winner-row-kind==group && resolvedOK; **ultimate site :908-934 group-name-row-only (Am11)**; 5s detached-context timeout at all 3 sites; `model_member_selected` event at the W-1 publish site | P2.4, P2.6 | Group request → 2 usage rows (group + member) at :1446-1472/:1656-1682, 1 row at the ultimate branch; fallback-reached group meters the member row (Q20); non-group → 1 row (unchanged); event fires only on `newlyBound` |
| P2.10 | Restart cold-start: startup log line documenting affinity loss (§3.5) | P2.2 | Log line present in boot output test/manual check |
| P2.11 | Full pipeline test pass: §9.4 suite + regression (`go test ./pkg/proxy/... ./pkg/ultimatemodel/...`) + **one-line check that `/v1/image_generation` with a group ID fails cleanly upstream (arch Open Question 1)** | P2.5–P2.10 | All green; existing imggen/decision/ultimate tests untouched and passing; imggen-group check recorded in the execution report |

**P2 exit criterion (11 tasks)**: A client request for the group name (or a claimed ID) load-balances across members with sticky affinity on BOTH protocols; member failure re-sticks (including the Anthropic failover branch); `/v1/models` hides claimed models; responses masked; metering writes both rows with corrected plumbing; `go build ./...` + `go test ./pkg/...` green.

### P3 — Frontend (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P3.1 | `types.ts`: `'group'` kind unions + `GroupMember` + group model shape | P1.6 | tsc compiles; baseline 26/11 not regressed |
| P3.2 | `useGroupModels` hook (`useApi.ts`): key `'group-models'`, `?kind=group` | P3.1 | `useGroupModels.test.ts` green |
| P3.3 | Quadruple-delete at all 9 mutation sites + docblocks (§6.3) | P3.2 | 3 mutation-test files green with four-key pin |
| P3.4 | `GroupMemberEditor.tsx` (cross-provider; dup-pair-only `isOptionDisabled`; MIN 1/MAX 16; **duplicate-`upstream_model` non-blocking hint, ~10 LOC — Am12-FE**) | P3.1 | `GroupMemberEditor.test.tsx` green incl. the duplicate-`upstream_model` hint case (warn, don't block) |
| P3.5 | `GroupModelForm.tsx` (kind+G1/G7 stamping, submit guard, aria; **clearable chat-kind `shadow_model_id` dropdown from `useModels()` — Am9**) | P3.4 | `GroupModelForm.test.tsx` green incl. shadow picker: set / clear (claim drop) / chat-kind-only options |
| P3.6 | `GroupModelsTab.tsx` + `SettingsPage.tsx` (TabType, 11th tab, content branch) + `App.tsx` threading; **verify `SettingsPage.tabs.test.tsx` assertion shape (per-tab-id vs hard count) before extending (arch Open Question 2)** | P3.2, P3.5 | `GroupModelsTab.test.tsx` + extended `SettingsPage.tabs.test.tsx` green; assertion-shape finding recorded in the execution report |
| P3.7 | Lock-step arms: `ModelsTab.tsx:162` C-16 + TokenForm picker filters (§6.4) | P3.1 | Extended `ModelsTab.chatFilter.test.tsx` + `TokenForm.test.tsx` green |
| P3.8 | `vite build` + commit regenerated `pkg/ui/static/**` in the same change | P3.6 | `go:embed static/*` resolves; `go build ./...` passes |

**P3 exit criterion (8 tasks)**: Dashboard CRUD of groups with cross-provider members works end-to-end; chat tab + token pickers exclude groups; tsc ≤ 26/11; FE tests green.

### P4 — Tests + docs (5 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P4.1 | Full suite: `go build ./...` + `go test ./pkg/...` (1.26 toolchain on PATH) | P1–P3 | Zero failures; known pre-existing flake class (credentiallb TOCTOU under `-race`) adjudicated per project note, not masked |
| P4.2 | Manual smoke (local run): create group shadowing a real model; request shadowed ID ×4 → members rotate per weights; `/v1/models` hides the target; delete group → model reappears; restart → cold-start log line + fresh binding | P2 | Smoke checklist documented in the execution report |
| P4.3 | `models-template.json` group example (docs-only) | P1 | Loads clean; grep-verifies no code reads the example |
| P4.4 | Blueprint note: `group` kind, resolver seam, claim mechanism, `pkg/grouplb` (project blueprint update) | P2 | Blueprint section present |
| P4.5 | CHANGELOG entry | All | Entry present |

**P4 exit criterion (5 tasks)**: Everything green locally; docs consistent; ready for user-authorized ship (no push without explicit instruction).

---

## 11. Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | **Single-InternalModel resolver seam** — `resolveWithCredential` reads `modelConfig.InternalModel` (`store.go:2118`); any group path that leaks past the kind-branch resolves an empty upstream model | High | Low | Kind-branch is the FIRST statement after the Internal gate (§3.6.1); G1 keeps `Internal=true` so dispatch predicates route correctly; P2.4 tests assert the member-shaped `ResolvedCredential`; defense-in-depth empty-members → ok=false |
| R2 | **credentialID-only engine keying** — cloning the engine verbatim would conflate members sharing a credential (429 on A cools B) | High | Medium (it's the default shape of the source) | Member-key `credentialID+"|"+upstreamModel` for bindings AND cooldowns (§3.5); P2.1 test pins per-member cooldown isolation |
| R3 | **ID-vs-Name shadowing misconception** — the commission says "name"; the codebase resolves IDs (`§2.2`) | High | High (if unaddressed) | §2.2 + §3.4 design the claim in ID space explicitly; Q1 records it; smoke test (P4.2) exercises the real flow; docs call it out |
| R4 | **Auth allow-list surprise** — tokens listing a shadowed ID now reach the group; tokens listing only the group ID don't reach the alias | Medium | Medium | Semantics pinned (§3.4): auth evaluates the client string, fail-closed unchanged; §9.4 auth tests (`handler_groups_test.go` — P2.8/P2.11) assert both directions |
| R5 | **FE cache fan-out miss** — adding a 4th cache key with incomplete deletes leaves stale tabs | Medium | Medium | Quadruple explicit delete (never prefix) at all 9 sites; mutation tests pin the exact key set (§6.3) |
| R6 | **Chat-arm lock-step leak** — groups leaking into the chat Models tab or token pickers | Medium | Medium | Four arms enumerated (§6.4) with tests each (P3.7, P1.6); the decision-commission's chat-arm leak is the named precedent |
| R7 | **modelscache staleness under claims** — `GetModel(shadowedID)` caches the group row; claim edits would serve stale redirects | High | Medium | Cache invalidation for group + shadowed (+ old-shadow) IDs wired into the same `OnModelChanged` extension (P2.2); §9.3 test |
| R8 | **Claim-index drift** — index and rows diverge across the two backends | Medium | Low | Single rebuild point per backend (`OnModelChanged` / JSON load); §9.2 rebuild tests; G8 uniqueness scan at every write |
| R9 | **Force-ultimate onto a group** — `X-Force-Ultimate-Model` bypasses exclusion; kind-blind dispatch then external-passthroughs an empty `UpstreamURL` | Low | Low | Fails fast with the normal error envelope; documented residual (§3.6.4); v2 could add a kind check in the force path. Related documented semantic (Am5): while X is claimed, X's ultimate switching silently stops (§3.6.4; **pending as Q17**) |
| R10 | **Metering timeout change** — adopting the 5s detached pattern at the 3 sites alters existing chat metering behavior under hangs | Low | Low | Bounded, sites are already edited for Q9; behavior change is "bounded wait" not "dropped row"; noted in the execution report |
| R11 | **Engine-clone concurrency flake** — the credentiallb TOCTOU test flakes ~1/5 under `-race` (pre-existing project note) | Low | Medium | Clone tests use injected clock + serialized upgrade assertions, not sleeps (§9.1); flake class named in P4.1 so it isn't misread as a regression |
| R12 | **Migration registry trap** — 031 SQL lands without the registry append (two prior near-misses documented in-file) | High | Low | P1.3 bundles SQL + append + contains-`"031"` test as ONE task; the in-file trap comments (:53-62) stay adjacent to the new entry |
| R13 | **Anthropic member-429 failover gap** — without the Am1 branch, `internal_handler.go:240` returns the error immediately for groups (`Credentials==0`) and the retry would rebuild from an empty `InternalModel`: the FIRST member 429 fails the whole `/v1/messages` request despite healthy members | High | High (if Am1 skipped) — certain at HEAD | P2.7 implements the group-aware branch (~20-30 LOC); `internal_handler_group_failover_test.go` pins member-429 → healthy-member failover; parity asserted chat-path-unchanged |

---

## 12. Assumptions

| # | Assumption | Plan section | Owner |
|---|---|---|---|
| A1 | `"group"` is collision-free as a kind value (verified: free-form TEXT column, `migrate` 030) | §3.1 | explorer (verified) |
| A2 | Next migration number is 031 (registry highest = 030 at `migrate.go:63`) | §7.1 | explorer (verified) |
| A3 | Both storage backends ship together (SQLite + Postgres + JSON twin) — no backend-lag profile | §3.3, §7 | planner |
| A4 | Streaming pass-through is inherited untouched by the resolver seam (no adapter/normalizer changes) | §3.6.1, §4 | planner |
| A5 | `getModelStrict` twins are admin-only fetch paths and must bypass claims | §3.4 | planner (verified usage at :1191/:1197/:1205) |
| A6 | Members never being model rows is acceptable — no per-member overrides of timeout/retry in v1 | §3.2 | planner |
| A7 | Same-named upstream models across providers may blend in `model_hourly_usage` (accepted for v1); **extended per arch R1 Am19:** a member's `upstream_model` may also collide with an operator model's ID — cross-feature cosmetic blend of one usage row, accepted | §3.7 | planner |
| A8 | Existing chat/imggen/decision/ultimate behavior is regression-frozen; their tests stay green untouched | §9.4, P2.11 | planner |
| A9 | The executing worker has the cached 1.26 toolchain available (project operating note) | §9 | project |
| A10 | PLAN-ONLY: no push/merge/commits from planning; execution honors the wt-claim (`push:false`) | §1 | commission |
| A11 | Claim-uniqueness at application level suffices (no DB unique index for v1 member/claim volumes) | §7.2 | planner |
| A12 | The 5s meter timeout adoption at the 3 sites is in-scope because those sites are edited anyway | §3.7, R10 | planner |
| A13 | `ComputeConversationKey` accepts the group ID unchanged (parameter, not model-specific logic) | §3.5 | explorer (verified `key.go:40-43`) |
| A14 | Force-ultimate residual (R9) is acceptable to ship undocumented to users (internal operator note only) | §3.6.4 | planner |

---

## 13. Open Questions for the User

Each row's **Recommended default is authoritative**; `decisions.md` mirrors this table (same numbering) with rationale per row. Rows marked **USER-DECIDED** record already-locked scope constraints — listed for traceability, not re-opening. **Architect Round 1 annotations:** Q5 (implementation detail amended — per-SSE-event buffering, Am6) and Q9 (plumbing + anchors corrected — winner-based gating, Am3/Am11); defaults themselves unchanged. Q1–Q16 numbering/titles are STABLE; Q17–Q20 appended from arch §12 (defaults = arch recommendations).

| # | Question | Recommended default |
|---|---|---|
| Q1 | **Shadowing mechanism** — the commission says "name", but resolution is ID-keyed everywhere (`§2.2`). Pick the claim design, resolution order, `/v1/models` hiding, deletion behavior, and auth-allow-list semantics. | **Single-ID claim** (`shadow_model_id` column): claims checked first in `GetModel` for enabled groups; strict admin twins bypass; `/v1/models` hides claimed IDs while claimed; deletion/disable drops the claim (model reappears); auth evaluates the client-requested ID string unchanged (fail-closed as today). Alias arrays → v2. |
| Q2 | **Membership shape**: (a) inline `(credential_ref, upstream_model, weight)` pairs vs (b) references to existing chat model rows vs hybrid. | **(a) inline pairs** (the user's originally suggested shape): dodges the one-row-one-`InternalModel` resolver blocker (`store.go:2118`), keeps one LB level, and enables member-keyed cooldowns; members never appear in `/v1/models`. (b)/hybrid → v2 if per-member overrides are ever needed. |
| Q3 | **LB strategy** | **USER-DECIDED**: clone the `pkg/credentiallb` engine (weighted-random sticky + cooldowns free); no new strategies in v1. |
| Q4 | **Sticky affinity**: what is hashed, where state lives, TTL, restart behavior, failure/removal handling. | **USER-DECIDED**: `ComputeConversationKey(groupID, tokenID, firstUserMessage)` verbatim; **in-memory** (no DB persistence); 24h sliding TTL + 5m janitor (engine defaults); restart = cold start (documented log line); sticky-target failure ⇒ exclude-and-reselect within the group and re-stick to the new pick; member removal ⇒ drop that member's bindings. |
| Q5 | **OpenAI response `model` field**: upstream leaks varying member names; Anthropic masks. Mask or accept? | **Mask to the client-requested name**, stream + non-stream, group-kind-only (bounded top-level-key patch; fail-open-to-verbatim). Restores single-model transparency; parity with Anthropic; zero cost for non-group traffic. Diagnostic verbatim toggle → v2. |
| Q6 | **Ultimate switching posture for groups** | **Force `ExcludeFromUltimateSwitching == true`** via G7 (D9 precedent `config.go:283-285`) + UI write-gate mirrors; runtime gate then never selects a group. Force-trigger residual documented (R9). |
| Q7 | **Validation rule set** — minimal G-rules; member provider-span vs the credential provider-match invariant. | **G1–G8 per §5.1**: `Internal=true`; ≥1 member (cap 16); refs exist + model non-empty + weight>0; no dup members; **members may span providers** (deliberate divergence — the invariant does not apply to groups); deny fallback/secondary/peak/deadline; G7; claim integrity + cross-group uniqueness. Flat membership (no group members) is **USER-DECIDED**. |
| Q8 | **Storage**: JSON membership column vs separate `group_members` table. | **JSON column** (recommendation shape (a) followed): migration 031 adds `members_json` + `shadow_model_id`; ~30 mechanical precedent-covered touch-sites; keeps `OnModelChanged`/modelscache untouched. Table profile rejected (house-style violation, hand-built integrity, second JSON implementation). |
| Q9 | **Metering attribution** — per-target member vs group name vs both; timeout pattern. *(R1: plumbing + anchors amended — Am3/Am11; default unchanged.)* | **Both rows**: existing group-name row (dashboard continuity) + per-target row for the **served member** (user's presumption) — delivered via **winner-based gating** (lock-protected `GetResolved()`/rc stamp; NOT the nonexistent site-scope `resolved`); ultimate-branch site group-name-row-only; same-named upstream blend accepted (v2 disambiguation). 5s detached-context timeout (decision pattern) at the 3 corrected sites. |
| Q10 | **FE specifics**: tab identity, member editor, cache discipline. | **Tab** `group_models` / label "Group Models" / 🔀; **GroupMemberEditor** cross-provider (lift same-provider check, keep dup-pair check), MIN 1 / MAX 16; cache key `'group-models'` + `?kind=group`; **quadruple** explicit delete at all 9 mutation sites; four lock-step chat-arm exclusions (§6.4). |
| Q11 | **Dangling member refs** (credential deleted underneath a member): block-at-delete vs runtime tolerance + heal. | **Block-at-delete** (one posture, v1): credential delete is refused while any group member references it (`ErrCredentialInUse`-family, naming the group); operator removes the member first. Cheap scan, zero healing machinery; tolerance+heal → v2. |
| Q12 | **Semantics corners**: enable/disable, empty-group runtime, claim-target deletion, group edits. | Enabled=false ⇒ claim inactive + absent from `/v1/models` (direct-named resolution = today's disabled-model parity); empty groups unreachable (G2 + Q11; defense-in-depth ok=false fail-closed); deleted claim-target tolerated (claim stands, nothing to hide); group ID immutable, member/claim edits live immediately. |
| Q13 | **`Internal` flag on the group row** (research-codebase §9.2 item 13 proposed `Internal=false`). | **`Internal=true` (G1)** — item 13's `false` is rejected on evidence: ~11 `.Internal` dispatch predicates (§2.4) + the resolver gate (`store.go:2172`) would all need kind extensions, and the group would otherwise fall to the external raw-passthrough path. `true` routes correctly for free; the resolver kind-branch intercepts before any Credentials/`InternalModel` read. |
| Q14 | **Member cooldown precision** when members share a credential (research-codebase §9.2 item 7). | **Member-precise**: cooldowns + bindings keyed by `credentialID+"|"+upstreamModel`; a 429 cools exactly the failed (credential, model) pair; credential-wide cooling → v2 candidate. |
| Q15 | **Member-resolution observability event** (research-codebase §9.2 item 11). | **Yes**: `model_member_selected` `{group_id, member_key, upstream_provider, upstream_model}` at the existing W-1 publish site, firing only on `newlyBound`; non-group traffic keeps `model_credential_selected` unchanged. |
| Q16 | **Chat→group fallback entries** (a chat model's `fallback_chain` referencing a group ID); group→group fallback (research-codebase §9.2 item 14). | **Group→group: impossible** (G6 — groups have no chain; item 14's broader first-class semantics → v2). **Chat→group: permitted** — fallback rows re-resolve through the same seam and work; member-level exclusion doesn't fire for fallback-reached groups (:1410 guard) — attempts fail over to the next chain row; documented posture, no extra code. |
| **Q17** | **NEW (arch R1 §12.1) — Ultimate-stops-while-shadowed**: while X is claimed by an enabled group, claims-first `GetModel(X)` returns the group row (G7-excluded), so X's ultimate switching halts until the claim drops. Accept the semantic? | **Accept + document** (Am5; §3.6.4 + §11 R9). The group takes over X's identity wholesale; un-claiming (delete/disable/edit) restores X's ultimate eligibility. No code — pure documentation. |
| **Q18** | **NEW (arch R1 §12.2) — N-members-per-target pattern**: a multi-credential target is expressed as N member rows (same `upstream_model`, one per credential) rather than per-target credential pooling. Accept for v1? | **Accept + document** (Am12; §3.2 ops notes + FE duplicate-`upstream_model` hint). Legal and cooldown-correct (distinct member keys, weights compose); rotation of a credential orphans that member's bindings by design (runbook sentence). Per-target credential pooling → v2 candidate. |
| **Q19** | **NEW (arch R1 §12.3) — Clearing `shadow_model_id` via group edit**: does an operator edit that empties the claim field drop the claim? | **Allow** (Am9; GroupModelForm clearable dropdown). Claim drops → shadowed model reappears in `/v1/models` and resolves normally; cache/index invalidation rides the existing group-write hook (P2.2). |
| **Q20** | **NEW (arch R1 §12.4) — Fallback-reached groups meter the member row too**: winner-based gating (Am3) meters chat→group fallback traffic to the served member as well as the group name — confirm intended. | **Yes — intended** (consistent with Q9's both-rows intent); falls out of the winner-row-kind==group gate for free, no extra code (P2.9 acceptance pins it). |

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
- **Per-target credential pooling** (one member spanning multiple credentials with engine-level credential LB inside the member) — the N-members-per-target pattern covers it for v1 (Q18); revisit with reference-shape membership at v2.
- **MCP-server group cross-pollination** (research-codebase §9.2 item 9) — unrelated surface; revisit only if MCP gains multi-backend servers.
- **Force-ultimate kind guard** (closing the R9 residual) — one-line check if product ever cares.

---

## Appendix A — Files Changed

### Source files (Backend — modified)

- `pkg/models/config.go` — `KindGroup`, `IsGroup()`, `ModelConfig.Members`/`.ShadowModelID`, `ValidateKindRules` arm + default-arm string (:188), JSON-backend twin updates (GetModel claim check, `ResolveInternalConfig` kind branch)
- `pkg/models/credential.go` — **`CredentialConfig.Validate` bans `|` in credential IDs (Am2, member-key collision gate; :107 re-verified)**
- `pkg/modelscache/models.go` — **claim-eviction hook: evict `modelsByID` + `negByID` for old/new shadow IDs on claim-relevant writes (Am4; neg entries :375, `ModelsNegTTL` :55-56, own-ID-only evicts :643/:665/:687)**
- `pkg/store/database/store.go` — schema scan/write plumbing (+2 cols), claim-aware `GetModel` (:854), claim index + `OnModelChanged` extension, `ResolveInternalConfigWithAffinity` kind branch (:2170) + cached variant (:2269), group write-path validation branch, Q11 delete guard
- `pkg/store/database/querybuilder.go` — SELECT/INSERT/UPDATE +2 columns ×2 dialects
- `pkg/store/database/migrate.go` — registry append `{"031", ...}` (:64, after :63)
- `pkg/proxy/handler.go` — `/v1/models` shadow-hide pass (:245-272); metering rows + 5s detached timeout (:908-934 ultimate branch / :1446-1472 stream / :1656-1682 non-stream — Am11); stream/non-stream `model` masking (:1172-1184/:1587)
- `pkg/proxy/race_coordinator.go` — group failover branch (:1465, wiring :344-351)
- `pkg/proxy/race_executor.go` — `model_member_selected` publish split (W-1 site)
- `pkg/proxy/internal_handler.go` — **group-aware failover branch (~:239-250): lift `len(Credentials) <= 1` for `IsGroup()`, `grouplb.ExcludeAndReselect` + member-shaped retry rebuild (Am1 🔴; ~20-30 LOC)**
- `pkg/ui/server.go` — `?kind=group` arm + chat-arm exclusion (:781/:788); POST/PUT group mirrors + G7 gates (:889-895/:1058-1065/:1198-1205/:1327-1332 patterns)

### New source files (Backend)

- `pkg/grouplb/doc.go` — package contract: member-key identity, in-memory affinity, restart cold-start
- `pkg/grouplb/engine.go` — clone of `credentiallb/engine.go` (member-keyed bindings/cooldowns, `OnGroupChanged`, lazy per-group state)
- `pkg/grouplb/selector.go` — prefix-sum weighted member selector
- `pkg/grouplb/binding.go` — binding record + rebind semantics
- `pkg/grouplb/key.go` — member-key + group-scoped conv-key helpers
- `pkg/grouplb/events.go` — event constants (clone)
- `pkg/grouplb/testhooks.go` — **the 7 test seams cloned 1:1 (Am14): `InjectAllCoolingForTest`, `cooldownUntil`, `bindingBoundAtForTest`, `forceCooldownExpiryForTest`, `InjectPreconditionStateForTest`, `InjectSingleCredExclusionForTest`, sweepHook install**
- `pkg/models/group.go` — `GroupMember`, `validateGroupRules` (G1–G8), `validateGroupMembers`, claim helpers
- `pkg/store/database/migrations/sqlite/031_add_group_fields.up.sql` / `.down.sql`
- `pkg/store/database/migrations/postgres/031_add_group_fields.up.sql` / `.down.sql`

### Source files (Frontend — modified)

- `pkg/ui/frontend/src/types.ts` — `'group'` kind unions + `GroupMember` + group model shape
- `pkg/ui/frontend/src/hooks/useApi.ts` — `useGroupModels` + quadruple delete ×9 + docblocks
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — `TabType` + 11th tab + content branch
- `pkg/ui/frontend/src/components/config/ModelsTab.tsx` — C-16 filter excludes groups (:162)
- `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` — both pickers exclude groups
- `pkg/ui/frontend/src/App.tsx` — tab threading

### New source files (Frontend)

- `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx`
- `pkg/ui/frontend/src/components/config/GroupModelForm.tsx` — **incl. the clearable chat-kind `shadow_model_id` dropdown (Am9; the field otherwise has no UI)**
- `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx`

### Test files

- New BE: `pkg/grouplb/{engine,selector,binding,key}_test.go`; `pkg/models/group_rules_test.go`; `pkg/store/database/store_group_test.go`; `pkg/proxy/handle_models_group_test.go`; `pkg/proxy/handler_groups_test.go`; `pkg/proxy/internal_handler_group_failover_test.go` (Am1)
- Extended BE: `pkg/ui/kind_rules_parity_test.go` (G-cases); `pkg/ui/handlers_models_kind_test.go` (group arm + chat-arm exclusion); `pkg/store/database/querybuilder_test.go` (column pins 19→21); `pkg/store/database` registry test (`"031"`)
- New FE: `src/hooks/__tests__/useGroupModels.test.ts`; `src/components/config/__tests__/{GroupModelsTab,GroupModelForm,GroupMemberEditor}.test.tsx`
- Extended FE: `src/hooks/__tests__/{useModels,useImgGenModels,useDecisionModels}.test.ts` (four-key pin); `src/components/config/__tests__/ModelsTab.chatFilter.test.tsx`; `src/components/__tests__/SettingsPage.tabs.test.tsx`; `src/components/tokens/__tests__/TokenForm.test.tsx`

### Config / build / docs

- `models-template.json` — group example (docs-only)
- `pkg/ui/frontend/tsc-baseline.txt` — NEW (Review R1 N8): frozen 26-errors/11-files baseline record for no-rerun verification
- `CHANGELOG.md` — feature entry
- Project blueprint — `group` kind note
- `.agents/shared/planning/load-balance-groups/{plan.md,decisions.md}` — this plan + decision log

### NOT changed (deliberately)

- `pkg/decision/`, `pkg/imggen/` — skipped paths (scope)
- `pkg/proxy/handler_anthropic.go` — group resolution via the resolver seam; response masking pre-existing; the Am1 failover branch lives in `internal_handler.go` (modified, above)
- `pkg/proxy/adapter_openai.go`, `adapter_anthropic.go`, normalizers, translator — passthrough stack untouched
- `pkg/ultimatemodel/` — G7 + runtime gate exclude groups; kind-blind dispatch never sees one
- `pkg/credentiallb/` — clone source, not modified
- `pkg/auth/` — ID-string semantics unchanged
- `cmd/main.go` — engine lifecycle owned by `ModelsManager`
- `pkg/toolcall/`, `pkg/toolrepair/`, `pkg/loopdetection/`, `pkg/supervisor/`, `pkg/bufferstore/` — content-actuating subsystems, model-identity-agnostic
- No pushes, no merges, no VERSION bumps (PLAN-ONLY; execution is local-only)

### Estimated diff size

- Modified BE: 11 files (~+680 / −45) — adds `internal_handler.go` (Am1), `credential.go` (Am2), `modelscache/models.go` (Am4)
- New BE: 12 files (~+1,050 incl. `pkg/grouplb` (7 files incl. `testhooks.go`) + migrations)
- Modified FE: 6 files (~+260 / −30); New FE: 3 files (~+700)
- New/extended tests: **13 new + 10 extended = 23 test files** (~+1,400 LOC) — recount per Review R1 N13 (9 new BE + 4 new FE; 4 extended BE + 6 extended FE)
- **Total ≈ 4,100 lines added / 75 removed** across ~60 manifest line-items (~37 non-test files — Approver R2 P3 count alignment) (manifest-completeness rule: **every file named in the plan body appears in this manifest**; if an execution pass touches a file not listed here, the pass report must call it out as a deviation)

---

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
| 10 | 🟡 | Cached-resolver kind-branch pinned at `store.go:2292` before the `Credentials==0` fall-through (~:2333); RLock-held, same ops as uncached path (binding writes included — P2 precision fix) | §3.6.1 · P2.4 |
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
- **P3** — Appendix A count aligned honestly: ~60 manifest line-items (~37 non-test files); ≈4,100 LOC kept (consistent).
- **P4** — §3.8 corner added: claiming a DISABLED chat model's ID is legal and inert from the listing view; wording adjusted to verified source behavior (`GetModel` has no enabled filter at HEAD — direct requests for the disabled ID route to the servable group) — micro-divergence from the approver's suggested phrasing recorded here.
- **P5** — §3.4 hide mechanism stated: interface-constrained scan consulting the claims index; no per-row model-kind branching.
- **P6, P7** — applied in `decisions.md` by the decisions worker (out of this file's scope).
- **P8, P10** — SKIPPED per approver instruction: self-describing / auto (no plan-text change required).
- **P9** — §3.6.5 cross-protocol asymmetry sentence added: Anthropic's Am1 branch excludes members even for fallback-reached groups (`IsGroup()`-keyed), OpenAI's :1410 guard skips them — same Q16 semantics, different exclusion depth.

- **P6 completion** — the literal "Q5 resolved"/"Q2 resolution" phrases lived in plan.md §4/§3.2 lead-ins (the approver's file label pointed at decisions.md); reworded to "answered (recommended default)" phrasing; decisions.md equivalents ("the plan adopts" ×2) fixed by the decisions worker.
