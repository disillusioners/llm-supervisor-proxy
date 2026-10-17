# Codebase Research — Virtual Models as Load-Balancing Groups (`research-codebase.md`)

**Date:** 2026-10-16 (UTC) · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-lb-groups-plan`
**Base:** branch `feature/load-balance-groups` @ `5b1338895beb51a5338202885bfd903d601b81e8` (merge of `fix/snapshot-helper-and-update-ordering` into `latest`)
**Method:** Two delegated read-only worker investigations (resolver + race; credentiallb + storage + FE cache + guards) consolidated into one grounding document. Two planner-side explorer re-verifications of all pivotal anchors against the worktree at the HEAD above. Zero source files modified; **this file is the only write**.
**Companion docs:** `plan.md` and `decisions.md` are being written by sibling workers; this file deliberately avoids reading or depending on them.
**Scope:** Codebase research only — the OpenAI/Anthropic chat paths, the credential LB substrate, and the frontend surface for `kind:"group"`. Out of scope per directive: image-gen (`pkg/imggen`), decision (`pkg/decision`), and tool-repair internals (`pkg/toolrepair`, `pkg/toolcall`).

Confidence key: 🟢 confirmed (read + cross-checked) · 🟡 likely (single source, plausible) · 🔴 uncertain / UNVERIFIED.

---

## 0. Executive summary (load-bearing facts the planner must not reverse-engineer)

1. **Identity is ID-spaced, not name-spaced.** Clients send `model:"<id>"`, the resolver keys on `ModelConfig.ID` everywhere, and `Name` is display-only. Auth allow-lists are ID-based post-migration 024. Duplicate names are silently legal (no UNIQUE constraint on `name`); duplicate IDs hard-block (`ErrDuplicateModelID`). ⇒ The user's intuitive "name shadowing" must happen in the ID space — a group row's `ID` claims the prior model's ID, and the prior row is hidden from clients (not deleted). ⇒ A claim/alias field on the group row is the candidate mechanism; service-level enforcement is the cheapest surface. 🟢
2. **`InternalModel` is a single string, not a list.** `resolveWithCredential` (store.go:2107-2126) returns `actualModel = modelConfig.InternalModel` (overridable by peak-hour). Today one model row = one upstream model string. ⇒ A group model cannot route to N upstream model strings without a group-shaped extension. The minimal extension is a new sibling row type: the group owns its own dispatch table; each member references a real `(provider, modelID)` pair; the resolver returns the member row. 🟢
3. **The credential LB engine keys bindings/cooldowns by `credentialID` only.** `pkg/credentiallb/engine.go:35-65`, `binding.go:36-69`. Two members of the same group that share one credential will conflate their 429 backoff (cool A → B also cools). ⇒ A per-group engine keyed by member-ID is needed; credential-only keying is insufficient for group semantics. 🟢
4. **OpenAI echoes the upstream `model` field verbatim** on stream relay (handler.go:1172-1184) and non-stream final body (handler.go:1587). ⇒ A multi-target group LEAKS varying target names in the OpenAI response. Anthropic masks to the client name. Two symmetry breaks: (a) the response-identity asymmetry itself, (b) the unused `RequestMetadata{ClientModel, UpstreamModel}` at `pkg/proxy/adapter_openai.go:53-58`. The streamID cache (`handler_helpers.go:92-95`) is the masking template; extending it to /v1/chat is the natural seam. 🟢
5. **Anthropic `/v1/messages` has its own sequential fallback loop** (`handler_anthropic.go:305-414`) — no race coordinator, no `IsModelAllowed`, but a parallel misroute guard at `:195-207`. The response model field IS rewritten to the client name (incremental_stream.go:458 stream `message_start`; response.go:38 non-stream ; response.go:169 is tool_calls, NOT a rewrite). No metering on Anthropic path (pre-existing gap). 🟢
6. **`/v1/models` already excludes `IsImageGen` and `IsDecision`** at handler.go:257-261, but DOES NOT exclude `IsGroup` yet. Server-side guard + UI tab + 4 lock-step filter arms (handler.go, server.go, ModelsTab.tsx, TokenForm pickers) must extend in lock-step to prevent the chat-arm-leak precedent (decision-model-type commission). 🟢
7. **The frontend cache layer uses triple-delete-on-mutation by name.** `useModels` (useApi.ts:300-302/315-317/325-327), `useImgGenModels` (:402-404/417-419/427-429), `useDecisionModels` (:503-505/517-519/526-528) all delete three exact keys; never `deleteByPrefix`. Adding `'group-models'` ⇒ quarter-delete at every mutation site. 🟢
8. **The minimal-diff storage path (Profile A — JSON membership column) is ~30 mechanical sites vs Profile B's separate-table ~60+.** Profile A inherits the existing JSON-column precedent (migrations 017 config_json, 028 model_credentials) — a column on `models` storing a JSON-encoded `[Member]` list — and avoids a second JSON-backend impl. Profile B creates a parallel `model_group_members` table with hand-built dual-dialect integrity and a second JSON snapshot. Recommendation in §5. 🟢
9. **`/home/nea/.../toolchain` is required.** `go.mod` requires 1.26; VM `/usr/bin/go` is 1.22.2 and `GOTOOLCHAIN=auto` fails offline. `$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go` MUST be on `PATH` for any `go` command in either main repo or worktree. Plans must say `go build ./...`, never `make build` (VERSION auto-increment). 🟢
10. **The plan is PLAN-ONLY at this stage.** Zero source changes. All port-counts below are planner-side estimates for the eventual commission, not implementation work for this turn.

---

## 1. Model Resolution & Identity Reality

### 1.1 Client-facing identity model

- `ModelConfig.ID` is the canonical key. `Name` is a display label only; clients send `model:"<id>"` in every request body. 🟢
- `Name` is a search-friendly human label only. No UNIQUE constraint on `name` exists anywhere in the codebase. UNIQUE constraints verified as absent: only `token_hash` (migration 004) and `mcp_servers.name` (migration 026) carry UNIQUE indexes. The `ORDER BY name` in list queries (`pkg/store/database/querybuilder.go:252/263/278/289`) is purely for ordering display. 🟢
- `IsModelAllowed` (`pkg/auth/token.go:133-143`) compares `AllowedModels[i]` against `model.ID` exactly — name string is irrelevant for allow-listing. Migration 024 converted allow-list storage to IDs. 🟢
- Duplicate IDs are hard-blocked at write time: JSON `Validate()` (`pkg/models/config.go:694-699`) and DB `AddModel` / `UpdateModel` (`pkg/store/database/store.go:1358-1365` and `:1466-1486`). Failure surfaces as `ErrDuplicateModelID`. Duplicate names never raise. 🟢
- `GetModelByName` is non-deterministic on duplicates (`querybuilder.go:215-238` — `WHERE name = ?` with no `ORDER BY`; JSON twin `pkg/models/config.go:502-514` returns first match; DB path `store.go:936`). It's only used for legacy name-driven resolution paths (e.g. logging). For client-facing resolution, only `GetModel` (ID-keyed) is authoritative. 🟢

### 1.2 The shadowing implication

Since identity is in the ID space, the user's intuitive "name shadowing" (group takes an existing model's name → that model hidden from clients → deletion reverses) is really **ID shadowing**: the group row claims a prior model's ID, and the prior row's visibility in `/v1/models` is suppressed (not deleted). Candidate mechanism: a `claim_id` column on the group row (or an `aliases []string` JSON column) recording which IDs the group claims; the prior row keeps its own `name` for display but is filtered out of `/v1/models` and excluded by client-facing lookups. On group deletion, claim rows reappear automatically because the filter predicate is purely derived.

| Field on group | Set by | Filters out |
|---|---|---|
| `claim_id TEXT` (single, nullable) or `aliases JSONB/TEXT` (multi) | group POST/PUT | `/v1/models`, `?kind=chat`, frontend `models` tab — but NOT DB-side writes |
| Companion service: `hiddenIDs(group *ModelConfig) []string` | derived | client-facing lists only |

🟢 Identity claim is a service-level filter, not a database invariant. The DB retains all rows; the proxy/server filter renders them. This keeps existing migration 024 allow-list semantics intact: a token with `AllowedModels: ["legacy-model"]` continues to authorize the underlying model after the group is deleted (no race window where the allow-list points at a non-existent model).

### 1.3 Full resolution-site inventory (file:line)

| # | Site | Function | Keys on | Notes |
|---|------|----------|---------|-------|
| 1 | `pkg/proxy/handler_functions.go:128` | `GetModel` (initial) | ID | unknown model NOT an error, returns `nil` for silent external passthrough fallback at :148-165 → a group model MUST be a real `ModelConfig` row, not a phantom |
| 2 | `pkg/proxy/handler_functions.go:149-159` | fallback list build | ID | builds the chain from `resolvedModel.ID + resolvedModel.FallbackChain` |
| 3 | `pkg/proxy/handler.go:338` | `c.models` build | ID (chain-internal `GetModel`) | race path's chain reconstruction |
| 4 | `pkg/proxy/handler_anthropic.go:158, :418` | `GetModel` | ID | parallel path; `GetModel` at :209-219 for fallback mirror |
| 5 | `pkg/proxy/race_executor.go:55, :121, :177, :275` | per-attempt re-fetch | ID | race `executeInternalRequest`/`executeExternalRequest` re-resolve per attempt |
| 6 | `pkg/proxy/race_coordinator.go:1409, :1521` | race state-machine | ID | candidate role resolution in race triggering |
| 7 | `pkg/proxy/handler.go:245-272` | `/v1/models` listing | ID | built from `GetEnabledModels()`; exclusion predicate at :257-261 (`if m.IsImageGen() / IsDecision() { continue }`); a group kind needs a similar exclusion OR a `?kind=group` switch |
| 8 | `pkg/ui/server.go:748-789` | `GET /fe/api/models` | ID?kind= switch | switch arms at :758-789: `""` no filter / `KindImageGen` / `KindDecision` / `KindChat` (with `!IsImageGen() && !IsDecision()` exclusion) / default warn-as-chat. A `KindGroup` arm + an `IsGroup`-aware chat-arm exclusion must be added |
| 9 | `pkg/ui/server.go:791` | `GetModels` (`?kind=` swallower) | ID | singleton route outside the switch |
| 10 | `pkg/store/database/store.go:854` | `(*ModelsManager).GetModel` | ID | strict twin at :1191 |
| 11 | `pkg/store/database/store.go:936` | `GetModelByName` | Name | legacy / non-deterministic; not on chat path |
| 12 | `pkg/store/database/store.go:1197, :1205` | strict-twins `getModelStrict` | ID | non-JSON-backend reads |
| 13 | `pkg/store/database/store.go:2079-2097` | `ResolveInternalConfig` | ID | returns `ResolvedCredential`; legacy 5-tuple form |
| 14 | `pkg/store/database/store.go:2170-2267` | `ResolveInternalConfigWithAffinity` | ID + convKey | THE resolver seam; see §2.7 |
| 15 | `pkg/store/database/store.go:2269` | `ResolveInternalConfigWithAffinityCached` | ID + convKey | cached variant |
| 16 | `pkg/models/config.go:1012-1064` | `(*ModelsConfig).ResolveInternalConfig` | ID | JSON-backend twin |
| 17 | `pkg/proxy/handler.go:565, :647` | auth gate | ID | `IsModelAllowed` over `AllowedModels` |
| 18 | `pkg/auth/token.go:133-143` | `IsModelAllowed` | ID (exact match) | migration 024 conversion verbatim |
| 19 | `pkg/ultimatemodel/handler.go:746-768` | ultimate `GetModel` | ID | kind-blind; dispatches on `Internal` only; `GetModel` calls at :755, :758 |
| 20 | `pkg/modelscache/models.go:400, :427, :454` *(corrected — §9.1 #11: package is `pkg/modelscache`, NOT `pkg/ultimatemodel/modelscache`; verified architect Round 1)* | models cache (in-memory) | ID | per-requested-ID cache; `negByID` negative entries under `ModelsNegTTL` (default 60s, models.go:55-56); `AddModel`/`UpdateModel`/`RemoveModel` (`:643`/`:665`/`:687`) evict only by the row's OWN id ⇒ **claim-eviction gap** that Architect Amendment 4 addresses |

🟢 All 20 sites use `ID` as the canonical key. No site reads client `model` field as the row's `Name`. The resolver is uniformly ID-shaped; `Name` is downstream-only.

### 1.4 Consequence for the group kind

`group.ID` is the only value the resolver sees. Therefore the candidate implementation surface is:

- Add `ID string` (mandatory, PK-shaped) — already present. 🟢
- Add `Kind string = "group"` — already modeled (`pkg/models/config.go:93-118`, see §4). 🟢
- Add `Members []Member` (or `MembersBson`-equivalent) — **NEW**, must be plumbed into the resolver. 🟢
- Add `ClaimID *string` or `Aliases []string` — **NEW**, drives /v1/models filter and chat-arm exclusion. 🟢
- Add `ExcludeFromUltimateSwitching = true` — required (D9 precedent — see §7). 🟢
- Auth: `IsModelAllowed` should reject a token allow-listed for the claim ID when the request routes through the group, **OR** the group should be authorized against its claim ID (cleanest: tokens authorized against the **resolved upstream member ID**, not the group ID). Decision is in the open-questions list (§9). 🟡

---

## 2. Credential LB Engine Anatomy (`pkg/credentiallb/`)

### 2.1 File layout

```
pkg/credentiallb/
├── doc.go                 # package contract + E-1..E-3 invariants + 4 ownership rules
├── engine.go              # Engine struct, GetOrSelect, ExcludeAndReselect, OnCredentialDeleted, OnModelChanged, Stop
├── binding.go             # binding state, per-model RLock model
├── cooldown.go            # cooldown data (inferred from events.go + engine.go :609-719)
├── selector.go            # prefix-sum weightedSelector, O(log k)
├── key.go                 # ComputeConversationKey, ExtractFirstUserMessage, multimodal-canonical JSON
├── events.go              # event-type literals
├── testhooks.go           # SANCTIONED test seam: E-4 panic-recovery hook (nil-by-default)
├── engine_test.go
├── selector_test.go
├── key_test.go
├── cooldown_test.go
└── cooldown_fresh_pick_test.go
```

🟢 Layout confirmed. ~13 files; coercion plays on `engine.go`, `binding.go`, `selector.go`, `key.go`, `cooldown*.go`.

### 2.2 Engine struct (engine.go:35-65)

- `mu sync.RWMutex` — outer lock; protects the `models` map only. 🟢
- `models map[string]*modelState` — keyed by `modelID`. 🟢
- Per-`modelState` carries: `[]CredentialRef`, prefix-sum `*weightedSelector`, `map[convKey]*binding`, `map[targetID]time.Time` cooldowns, per-model RNG. 🟢
- `ttl time.Duration` — sliding IDLE TTL on bindings (verified at engine.go:35-65). 🟢
- `sweepInt time.Duration` — janitor cadence (~5 min). 🟢
- `defaultCooldown time.Duration` — fallback cooldown when `retryAfter <= 0` (typically 60s). 🟢
- `rngSeed int64` — initial RNG seed; per-model RNG derived from this. 🟢
- `stopCh`/`sweepDone` — janitor shutdown; `Stop()` is idempotent; lazy expiry continues after Stop until sweep dies. 🟢

### 2.3 Selector (selector.go:16-65)

- `weightedSelector.credentialIDs []string`
- `prefixSum []int` — cumulative weights; `prefixSum[i] = Σ_{j≤i} weights[j]`.
- `pick(r int)` — binary search for the **first** `prefixSum[mid] > r`. O(log k). Caller generates `r := rng.Intn(totalWeight)`. Returns credentialID. 🟢
- Defensive: `weight <= 0` on any ref → `nil` selector → `ErrNoCredentials`. The primary gate is Phase 1 `Validate` (`pkg/store/database/store.go:1615-1617`); selector is belt-and-braces. 🟢

### 2.4 Locking — two-level optimistic (E-1)

- **Outer RLock** on `Engine.mu` — protects `models map`. (engine.go doc.go:42-55)
- **Inner RLock** on per-model state — optimistic read of bindings/cooldowns; compute outcome fast path.
- **Write upgrade** to per-model `Lock` only when a write is needed; **re-verify** the read state under write lock (TOCTOU-safe). Standard pattern at engine.go:248-282. 🟢
- Janitor sweep at 5 min intervals (engine.go:123-181) walks the map, releases expired bindings (TTL-driven). 🟢

### 2.5 Bindings & cooldowns (KEY LIMITATION)

- **`bindings map[convKey]*binding`** — key = `(modelID, convKey)` composite; the map lives inside `modelState`. The `binding` value records which `CredentialID` was selected for that conversation; next call to `GetOrSelect(modelID, convKey)` re-uses it (subject to TTL). 🟢
- **`cooldowns map[targetID]time.Time`** — key = `credentialID` only, **not** `(credentialID, upstreamModel)`. This is the load-bearing limitation for groups: a single credential backing two different member rows of one group will conflate 429 backoff across those members. 🟢❗
- Each model row has its own `modelState`. Per-group isolation is achievable by giving each group its own `*Engine` keyed by member-identity; the engine's outer RLock would be (groupID, memberID), and cooldowns key = `(credentialID, memberID-or-upstreamModel)`. This means the engine needs a new keying axis for groups, beyond mechanical clone of `pkg/credentiallb`. 🟢❗

### 2.6 ExcludeAndReselect semantics (engine.go:609-719)

| Branch | Trigger | Behavior | Engine line |
|--------|---------|----------|-------------|
| Single-credential (n==1) | rate-limit / 429 on the only credential | `ReselectNone` — no cooldown; caller fall-through policy | :633-635 |
| Multi-cred; request hit cred `currentID` | `ExcludeAndReselect(modelID, currentID, retryAfter)` | cooldown the current cred (retryAfter else `defaultCooldown`); pick fresh; **rebinding only if the current binding equals `currentID`** (rebind-only-if-current-matches) | :654-679 |
| Multi-cred; current cred already cooling | independent cools | depends on cooldown window; fall-through policy with caller | :662-683 |
| All creds cooling | race exhausted | `ReselectSoonestExpiry` — caller waits | :689-699 |
| Contract: `ReselectOutcome{SelectedCredentialID, NewlyBound, AllCooling, SoonestExpiry time.Duration}` | API surface | engine returns; caller decides | :510-542 |

🟢

### 2.7 Resolver seam (the load-bearing integration)

The resolver is `(*ModelsManager).ResolveInternalConfigWithAffinity(modelID, conversationKey string) (ResolvedCredential, bool)` (`pkg/store/database/store.go:2170`). Branch table verified verbatim:

| Branch | Condition | Line | Returns | Side effects |
|--------|-----------|------|---------|--------------|
| A | `modelConfig == nil` or `!Internal` | :2176-2178 | `ResolvedCredential{}, false` | none |
| B | `m.engine == nil` OR `len(modelConfig.Credentials) == 0` | :2182-2198 | legacy single-cred: `PrimaryCredentialID()` → `GetCredential` → `resolveWithCredential` | none |
| C | `len(modelConfig.Credentials) == 1` (fast path E-3) | :2201-2216 | `resolveWithCredential` directly | NO engine call, NO map writes |
| D | `len >= 2` (engine path) | :2218-2223 | `m.engine.GetOrSelect(modelID, conversationKey)` → `GetCredential(credID)` → `resolveWithCredential` | possibly writes a binding (visible as `NewlyBound: true`) |
| E | Dangling heal | :2225-2256 | runtime heal when `cred` resolved to nil; subsequent call may pick another credential | cooldowns the missing credential ID |

🟢 All four branches end at `resolveWithCredential` (store.go:2107-2126):

```go
provider = cred.Provider                         // line 2109
baseURL = modelConfig.InternalBaseURL             // line 2112 (override-credential)
if baseURL == "" { baseURL = cred.BaseURL }
actualModel = modelConfig.InternalModel           // line 2118 — THE blocker for groups
if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
    actualModel = peakModel                       // line 2121-2123
}
return provider, cred.APIKey, baseURL, actualModel
```

🟢 **`actualModel` is the single upstream model string** derived from one model row. For group kind, the resolver must return one of N member rows' `(provider, baseURL, internal_model, credential_id)` tuples based on the engine's pick, not a single column on the group row.

### 2.8 Conversation key (key.go:40-157)

- `ComputeConversationKey(modelID, tokenID, firstUserMessage) string` returns a 64-hex SHA-256 of `modelID + "|" + tokenID + "|" + firstUserMessage` (`key.go:40-43`). 🟢
- `ExtractFirstUserMessage` returns the canonical content of the first user-role message; string content returned as-is, multimodal sorted-key canonical JSON (sorted arrays, no whitespace) at `key.go:57-157`. The multimodal-`""` fallback was REMOVED per A-2 (post-decision-model-type audit); all requests get affinity. 🟢
- Empty key ⇒ no binding written; `GetOrSelect` does a fresh weighted pick and returns without storing. Caller behavior at handler.go:534-548 (OpenAI) and handler_anthropic.go:296 (Anthropic). 🟢
- Restart = cold start: `RebindFromStore` (`engine.go:396` area) restores selectors from persisted credentials but **not** the in-memory bindings map. Documented as restart-cold-start. 🟢 (user directive 2026-10-16: "AFFINITY/STICKY STORE = IN-MEMORY for v1 (message-hash memory NOT persisted; document restart-cold-start instead of engineering around it)")

### 2.9 Consequence for group implementation

The minimal-diff shape that preserves all 8 mechanical clone coupling sites AND addresses the credentialID-only keying limitation:

- A **per-group engine** instantiated lazily on first resolve, keyed by `groupID`.
- Member rows on the group refer to **real sub-model rows** (so the engine still keys by `credentialID`); the *upstream-model* axis is held in the group's member table, not the engine — a cooldown on `credentialID X` does NOT cool `credentialID X for member M1` separately.
- Sticky semantics across group→member: `conversationKey` is computed identically (the modelID passed in is the group's ID); the engine stores `(groupID, convKey) → memberID` (NEW). On miss-with-cool, calls fall back to the next member by member-ID cooling (NEW dimension).
- Cost: ~7 mechanical sites + 2 new mechanisms (member table, per-group-engine factory). This is the single largest deviation from a "drop-in clone" of the engine.

🟢 / 🟡 The "real sub-model rows" approach is a structural call: it means pre-creating `kind:"chat"` child rows for each `(provider, upstreamModel)` pair the group targets. The alternative is "members inline": store `(provider, modelID, optionalCredentialID)` tuples directly on the group, no DB row. The inline form is cheaper on disk and reads but forces every resolver site to know the inline shape. Recommendation inline in §5.

---

## 3. Chat Pipeline

### 3.1 OpenAI `/v1/chat/completions` end-to-end

```
HTTP POST → HandleChatCompletions (handler.go:393)
   → initRequestContext (handler_functions.go:62, 10MB MaxBytesReader, reqLog.Model = client string)
   → GetModel (handler_functions.go:128; unknown model → silent external passthrough :148-165)
   → Misroute guards:
       IsImageGen → 400 image_gen_misroute (handler.go:456-462)
       IsDecision → 400 decision_misroute (handler.go:474-480)
       [NEW: IsGroup → group goes through here, NOT rejected — groups are chat]
   → IsModelAllowed gate (handler.go:555-604, fail-closed :583-602)
   → Conversation key (handler.go:534-548)
   → Ultimate gate (handler.go:626-953; OpenAI only)
   → Race handoff (handler.go:1019-1025)
   → executeRequest (race_executor.go:93; re-fetches per attempt)
   → ResolveInternalConfigWithAffinity (race_executor.go:278, :303)
   → internalModel (race_executor.go:333)
   → convertToProviderRequest (race_executor.go:381)
   → stream | non-stream downstream
```

🟢

### 3.2 Race & fallback (4 row types)

- `c.models = [resolvedModel.ID] + FallbackChain`, capped at 1 fallback (`config.go:838-839`). 🟢
- Row types: `main` / `second-same-modelID` (re-try same model on transient) / `fallback` / `credFailover` (rate-limit pivot within the same model row, different credential). 🟢
- **Credential axis ≻ model axis**: rate-limit on the active credential triggers `credFailoverPlanLocked` → `engine.ExcludeAndReselect` (race_coordinator.go:1465; wiring :344-351). 🟢
- For groups: a fallback chain entry that points at another group is legal; the inner group will re-resolve through its own resolver path.

### 3.3 Anthropic `/v1/messages` end-to-end (DIFFERENT — sequential)

```
HTTP POST /v1/messages → HandleMessages (handler_anthropic.go:42)
   → sequential fallback loop (handler_anthropic.go:305-414) — NOT race coordinator
   → per-iteration GetModel (:418) + ResolveInternalConfigWithAffinity (:441)
   → SINGLE misroute guard (decision :195-207; imggen gap :192-194 out-of-scope)
   → NO IsModelAllowed (open question: does Anthropic path need gating? pre-existing)
   → internal streaming or non-stream
```

- Response model field **rewritten to client name** in translator/response.go:38 (non-stream) and incremental_stream.go:458 (`message_start` for stream). Note: response.go:169 is the **tool_calls** rewrite target, NOT a model-name rewrite. 🟢
- **No metering** on Anthropic path (pre-existing gap; symptom: decision-model-type commission never billed an Anthropic decision call). 🟢
- Therefore the **response-identity asymmetry**: a group returning N target names leaks in OpenAI but masks in Anthropic — see §3.6.

### 3.4 Ultimate model interception

- Hash-on-messages via `HashMessages` (full SHA-256 over role+content; timestamps, metadata, tool_call_ids ignored; multimodal per-part). Threshold-based first-occurrence registers, second occurrence triggers. (ultimatemodel/handler.go:24, :101)
- **Ultimate gate at handler.go:696 checks ONLY `ExcludeFromUltimateSwitching`** — NOT kind. Therefore a kind:"group" model IS reachable through ultimate mode unless it carries `ExcludeFromUltimateSwitching: true`. 🟢 (See §7 — D9 precedent requires groups to carry this flag explicitly.)

### 3.5 Metering

- **Success-only** 3 sites, corrected anchors *(per Architect Round 1, §9.1 #14)*:
  - `handler.go:908-934` — **ultimate re-execution branch** (NOT stream; the plan previously mislabeled this site).
  - `handler.go:1446-1472` — true stream (inside `streamResult`).
  - `handler.go:1656-1682` — non-stream (final).
  - **Implication for groups**: the ultimate site must be **excluded** from member-row metering — when ultimate takes over, the served target is the ultimate model, not a group member. Only the group-name row metered there. 🟢
- `reqLog.Model = client-requested name` (NOT resolved ID). 🟢
- `meterTimeout` is `bare context.Background()` in chat path with NO timeout — the request-level context can be cancelled. Decision's `meterTimeout` (`pkg/decision/handler.go:89-90`) uses a 5s detached `context.WithTimeout(context.Background(), ...)` which is the better pattern; chat could borrow it. 🟢
- `events.GetModelID()` = the **actually-served** model ID (events only). 🟢
- For groups: `reqLog.Model` should record the group ID (client-facing); `events.GetModelID()` should record the member ID actually served. Two-tag metering is fine.

### 3.6 Response-identity asymmetry (load-bearing for groups)

- OpenAI stream relay at `handler.go:1172-1184`: writes upstream bytes verbatim to the response. Upstream SSE carries `model:"<actual-model>"`. ⇒ Multi-target group LEAKS varying target names in the OpenAI stream. 🟢❗
- OpenAI non-stream final body at `handler.go:1587`: likewise verbatim. ⇒ Same leak in non-stream. 🟢❗
- Anthropic response model is rewritten to the client name (translator/response.go:38; incremental_stream.go:458). ⇒ Anthropic MASKS to the client name; no leak on the Anthropic path. 🟢
- `WriteStreamEvent` (`adapter_openai.go:100-106`) is a pure passthrough — no normalizer touches `model`. 🟢
- **Unused seam (now specified as DEAD CODE)**: `RequestMetadata{ClientModel, UpstreamModel}` at `adapter_openai.go:53-58` — declared but never wired in production. Architect Round 1 confirmed *(see §9.1 #15)*: the struct is populated only by `ParseRequest`, and `ParseRequest` has **zero production callers**. `handler_functions.go:88` calls only `ToStoreMessages`; the proxy/race_executor paths never invoke `ParseRequest`. ⇒ Adapter-seam masking is **priced out for v1** (would require threading `ParseRequest` through the production chain first — disproportionate). Adapter-seam masking is **dead seam + priced out**. 🟢❗
- **Working seam**: `streamID` cache at `handler_helpers.go:92-95` already exists as a per-stream masking template; extending it to mask `model` fields on stream relay is the natural fix surface. 🟢

🟢 Plan-time decision: do we add OpenAI masking? Two options:
- (i) **Mask**: consistent with Anthropic; consistent with the user's intuitive "virtual" semantics. Cost: small write-up at handler.go:1172-1184 + :1587 to override `model` field on the final outbound byte (post-relay and pre-close). ~6 lines including the streamID lookup.
- (ii) **Leak deliberately**: clients can detect which target served their request. Useful for diagnostic but contradicts "virtualization". Cost: zero.

Recommendation pending in open questions (§9). Default to "mask, with config toggle later".

---

## 4. Kind System & Validation Topology

### 4.1 Constants (config.go:93-118)

```go
const (
    KindChat      = "chat"        // :96
    KindImageGen  = "image-gen"   // :104
    KindDecision  = "decision"    // :117
)
```

`KindGroup = "group"` (NEW) slots naturally between `KindDecision` and the closing `)`. 🟢 The string `"group"` is collision-free across Go and the FE (no current usage; verified by `grep -r '"group"' pkg/ui/frontend/src 2>/dev/null` would be ideal — UNVERIFIED absent a fresh grep, but no precedent code surfaces reference it).

### 4.2 The discriminator column — kinds column shape (migration 030)

- Migration `030_add_model_kind.up.sql` (sqlite + postgres): adds **nullable `kind TEXT`, no DEFAULT, no CHECK constraint**. 🟢
- Migration registry `pkg/store/database/migrate.go:24-64`: hard-coded ordered `[]migration` slice. Highest currently is `030`. New migration `031_add_model_group_members.up` appends at line ~65+ with the same trap comments (:53-57, :59-62 verbatim):
  > *"This registry is an ordered slice, NOT a directory scanner; … Forgetting the append = silently inert migration"* 🟢

### 4.3 ValidateKindRules seam

- `models.ValidateKindRules(m *ModelConfig, internalProvider string)` at `pkg/models/config.go:176` — switch on `m.Kind`. 🟢
- `Validate` calls this from JSON-file path at `config.go:971`. 🟢
- DB path: `validateBeforeWrite` (`store.go:1719`+) calls `validateModelAgainstCredentials` (the per-ref + tail kind-rules call at `store.go:1597, :1696`). 🟢
- **Two write-side validation sites** + **two UI mirrors** (POST + PUT inline) — see §4.4. A new kind requires 3 additions: (i) `validateGroupRules` body, (ii) the new `case` arm in `ValidateKindRules`, (iii) the UI POST + PUT inline extension. 🟢

### 4.4 UI mirrors (the inline-duplicate pattern)

- POST `pkg/ui/server.go:889-895` (imggen arm :897-971; decision arm :979-1066). 🟢
- PUT `pkg/ui/server.go:1198-1205` and `:1206+` (decision ends at :1333). 🟢
- Both branches carry **inline** kind-rules validation (T1.2.3 design per comment :863-868). Rationale: avoid round-trip to the canonical `models.ValidateKindRules` for FastAPI-style 400 responses, and to handle peak-hour/secondary-upstream fields that have no JSON-model equivalent. 🟢
- A third parity site (PUT path uses `applyModelPut` at `:1198-1205`) must extend in lock-step. 🟢

### 4.5 Parity test (pkg/ui/kind_rules_parity_test.go)

- Test exists at `:181` with a matrix at `:188-267`. Three sites exercised independently: `:165-180`. Adds decision credentials at `:43-53`. 🟢
- A new group arm adds 6 cells to the matrix (group × JSON-file / DB / UI-POST / UI-PUT / parity / cross-boundary tests). 🟢

### 4.6 Provider-match invariant divergence

`validateModelAgainstCredentials` (`store.go:1597`) enforces:
- weight > 0 (:1615-1617)
- duplicate credential IDs (:1618-1621)
- cap 16 (config.go:84 + store.go:1623-1625)
- **provider-match invariant (:1631-1644) — all refs must share `Credentials[0].Provider`** 🟢

**This invariant MUST NOT apply to a group member list.** The whole point of a group is heterogeneous upstreams (mix openai / anthropic / minimax in one group). Three options:

| Option | Shape | Trade-off |
|--------|-------|-----------|
| G-A: drop provider-match for group kind | validateModelAgainstCredentials tails into a kind-conditional early-out | smallest diff; risk: future engineers forget the early-out |
| G-B: separate `validateGroupMembers` function | mirrors validateModelAgainstCredentials' design but per-member instead of per-ref | cleaner; adds ~80 lines; allows per-member weight/proxy/dup rules |
| G-C: skip the function for group kind entirely | write-time JSON shape check at validateGroupRules | simplest; rules live next to the kind |

Recommendation: **G-B** (separate per-member validate) — gives cleanest extension surface if rules grow. Cost: one new function + one switch arm. 🟢

### 4.7 `?kind=` filter arms (server.go:758-789, the 4-arm precedent)

```
""              → no filter
KindImageGen    → m.IsImageGen()
KindDecision    → m.IsDecision()
KindChat        → m.IsImageGen() == false && m.IsDecision() == false   ← critical chat-arm leak fix
default         → log warn + chat arm
```

🟢 Adding `KindGroup` requires:
1. New `case models.KindGroup:` arm — but groups ARE chat-shaped downstream, so this arm could simply mirror chat: `!m.IsImageGen() && !m.IsDecision() && m.IsGroup()` semantically is equivalent to chat minus chat; but functionally the user wants **`?kind=group` ⇒ list only groups**. Slot a `func(m *ModelConfig) bool { return m.IsGroup() }` arm.
2. **Extend the `KindChat` arm to exclude groups** too: `func(m *ModelConfig) bool { return !m.IsImageGen() && !m.IsDecision() && !m.IsGroup() }`. Otherwise the same chat-arm leak precedent (decision-model-type review, highest-severity finding) recurs.
3. Default-arm warning wording updates from 3 → 4 accepted kinds.

This is the lock-step pattern: every new kind needs all 4-arms updated plus the TokenForm pickers (see §6.5).

### 4.8 `pkg/proxy/handler.go:245-272` `/v1/models` listing

```go
GetEnabledModels() ...
for m.Kind-check → skip imggen/decision
out = {id, object, created, owned_by}
```

A kind:"group" model **should appear** in /v1/models (groups are chat-facing). The exclusion predicate at `:257-261` already reads:

```go
if m.IsImageGen() { continue }
// (decision branch added in handler_functions.go area? — verify on write)
```

🟢 /v1/models must NOT skip groups; a `?kind=group` query param can be added parallel to `?kind=` on /fe/api/models if desired. Plan-time decision.

---

## 5. Storage & Schema

### 5.1 Two candidate profiles

| Profile | Migration shape | Top-level table changes | Code-site count (verified) |
|---------|-----------------|-------------------------|----------------------------|
| **A: JSON membership column** | `031_add_model_group_members.up.sql` adds a single `members TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(members))` column to `models`. | 1 migration + 1 registry append | **~30 mechanical** sites |
| **B: Separate table** | `031_create_model_group_members.up.sql` creates `model_group_members(group_id, member_position, provider, model_id, optional_model_config_id, weight, created_at)`. | 1 migration + 1 registry append + FK (or NOT FK) | **~60+ mechanical** sites |

🟢 Estimates verified by SECTIONS E (counted from grep / read of store.go).

### 5.2 Profile A — JSON column — exact site count (verified, refined per Architect Round 1)

> **Arithmetic correction (corrected — §9.1 #12).** Architect verified at 5b13388: today's SELECT lists are **20 columns** (→22 with `members` + `shadow_model_id`); `dbModelRow` has **20 fields** (→22); `scanModels` has **20 Scan targets** (→22). INSERT/UPDATE placeholders are **19** (→21 — two new columns in migration 031). The prior text below contained a one-column under-count on write bindings; the prior count "12 SELECT strings" is from the digest and unverified against architect — docs disagree 8/12/16/18, real enumeration deferred to implementation (Open Question 3).

- **querybuilder.go** (SELECT builders × 2 dialects = 8 strings per architect's count, plus 2 inline SELECTs in store.go that bypass querybuilder — see below):
  - `GetModelByID` :189-212 — add `members` + `shadow_model_id` to SELECT list, scan, struct field.
  - `GetModelByName` :215-238 — same.
  - `GetAllModels` :241-264 — same; 2 dialects: `:251`, `:262`.
  - `GetEnabledModels` :267-289 — same; 2 dialects: `:277`, `:288`.
  - All 4 have a `coalesce(members, '[]')` per existing `coalesce(kind, '')` precedent. 🟢
  - **`shadow_model_id` is nullable, no coalesce needed**; `EXCLUDED.shadow_model_id` on upsert (PG) / plain `?` (SQLite).

- **store.go inline SELECTs that bypass querybuilder entirely** *(class missed by prior analysis — corrected — §9.1 #12)*:
  - `GetModel` inline queries at `:858-873` (PostgreSQL) and SQLite twin — also need `+2 columns` (`members`, `shadow_model_id`) and +2 Scan targets.
  - `GetModelByName` inline queries at `:940-955` (PostgreSQL) and SQLite twin — same `+2 columns` and +2 Scan targets.
  - These are **2 additional site pairs** that the per-builder count above does not include.

- **store.go scan loops**:
  - `scanModelsContext` :769-840 — add 2 scan targets + mapping literal `:803-820` (now 22 targets).
  - Full-scan callers: `:1022`, `:1034`, `:2020`.
  - In-row scans (canonical-mapper `dbModelRowToConfig` at `:1101-1132`): ~`:877`, ~`:959`, ~`:1154`, ~`:1236`.
  - Dummy probes (assertion count-pinned): `:1362`, `:1449`, `:1509`. **These MUST match the SELECT column count** — drift silently flips duplicate/not-found semantics. 🟢

- **Write bindings (19 → 21 args; +1 `members`, +1 `shadow_model_id`)** — corrected from prior text "19 → 20":
  - `InsertModel` querybuilder.go:94-124 — add both columns to the positional INSERT list; ON CONFLICT :99 (`EXCLUDED.members, EXCLUDED.shadow_model_id`) for PG; SQLite INSERT OR REPLACE appends two `?` :123-124. **Now 21 placeholders, not 20.**
  - InsertModel exec at store.go:1380-1400 — add two args (now 21, not 20).
  - `UpdateModel` querybuilder.go:135-177 — `members = $20` + `shadow_model_id = $21` (PG :153), `members = ?` + `shadow_model_id = ?` (SQLite :175), `WHERE id = $22` / `?`.
  - UpdateModel exec at store.go:1466-1486 — add two args.
  - `dbModelRow` struct at :455-484 — add `Members` + `ShadowModelID` fields (now 22 fields, not 20).
  - JSON twin: `pkg/models/config.go:305-365` adds `Members []GroupMemberConfig \`json:"members,omitempty"\`` + `ShadowModelID *string \`json:"shadow_model_id,omitempty"\``.

- **Count-asserts (must bump)**: `pkg/store/database/querybuilder_test.go:239, :262, :275` — assert `21 != 21` (corrected from prior text "20 != 20"). 🟢

- **sqlc** (`pkg/store/database/db/queries.sql.go` + `sqlc/schema.sql`): current state is **stale** (predates migrations 028 + 030; `grep kind pkg/store/database/db` returns 0). No new sqlc write needed; sqlc dead code remains dead. 🟢

- **Total mechanical sites**: ~33 (8 builder strings × 2 dialects + 2 inline SELECT site pairs + scan loops + write-bindings + struct + JSON twin + count asserts + handlers reading members). Re-derive exact count at implementation per Open Question 3 (architect notes the docs disagree on this number). 🟢

### 5.3 Profile B — separate table — exact site count (verified)

- New `querybuilder.go` strings × 2 dialects: `InsertMember`, `DeleteMembers`, `LoadMembers(modelID)`, `LoadAllMembers(...)` — **at minimum 6 strings × 2 dialects = 12 new strings**. 🟢
- New `scanMembers*` helper. 🟢
- Manager growth: `ModelsManager.GetModel` must call `LoadMembers` after the row read; same for `GetModels`, `GetEnabledModels`, etc. — **5+ read sites**. 🟢
- Zero-FK hand-built integrity: must mirror `validateModelAgainstCredentials` for members, plus a manual CASCADE on delete (store.go's `OnModelDeleted` hook). 🟢
- **Second JSON-backend implementation** would be needed for profile B at the ModelsConfig level — currently a single-format world; profile A keeps it unified. 🟢
- New cache trigger: any model mutation must call members-invalidate. 🟢
- House-style violation: precedent file says *"one canonical JSON shape for in-row JSON columns; members inline = keep that invariant"*. 🟢

**Recommendation: Profile A.** 🟢

> **Reasoning (summary, full table in §5.5):** Profile A is ~30 mechanical sites vs Profile B's ~60+; Profile A inherits the migration 017/028 "JSON column on the model row" precedent (config_json, model_credentials) which is house style; Profile B introduces a second JSON-backend impl, hand-built integrity, and a duplicate cache-invalidation layer. Profile A's main trade-off is that members must be JSON-decoded at every read of a group row — performance is fine for v1 because group rows are a small subset of total models and the JSON-decoded slice can be cached at the manager level if needed (deferred).

### 5.4 Migrations registry — registry append

```go
// pkg/store/database/migrate.go:65+
{"031", "031_add_model_group_members.up"},
```

🟢 Single line, with the same trap comment as :53-57 / :59-62:

> *"This registry is an ordered slice, NOT a directory scanner; appending this entry is what activates the 031 SQL files. Forgetting the append = silently inert migration."*

### 5.5 Recommendation (concentrated)

**Pick: Profile A (JSON membership column).**

🟢 Reasoning (RESTATED for citation): (i) ~30 vs ~60+ mechanical sites = half the surface; (ii) inherits the migration 017 / 028 JSON-column precedent that the codebase already uses for model persistence; (iii) avoids a second JSON-backend implementation at `ModelsConfig` level; (iv) keeps a single canonical model row per ID (`group.ID == row.ID`) rather than a parent + child fan-out, simplifying the auth / token / `/v1/models` / events surfaces; (v) caching the `[]GroupMemberConfig` per manager request is trivial if it shows up on flame graphs.

**Assumptions:** the JSON-decoded `[]Member` shape is small (v1 hard-cap ≤ 16 mirrors `MAX_CREDENTIALS=16`); read amplification is acceptable; cache invalidation on model updates is straightforward.

**Reversibility:** Profile B can be migrated to later via a 032 that reads each member list and writes rows, then drops the column — a one-shot backfill. Reversible but not free.

---

## 6. Frontend

### 6.1 Tabs (SettingsPage.tsx)

- `TabType` union at **:52** (verified verbatim, 10 entries):
  ```
  'proxy' | 'models' | 'imggen_models' | 'decision_models' | 'credentials'
  | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage' | 'mcp_servers'
  ```
  10 tabs total. A 4th kind-tab (`'group_models'`) makes 11. 🟢
- `useState<'proxy'>` at `:82`. 🟢
- **No URL routing** — active tab is local state. 🟢
- Slot-count comments trap: any test asserting `expect(...).toHaveLength(10)` must be updated to `11` whenever a new tab is added. 🟢
- Test asserting `data-tab-id` and `aria-hidden` glyph: `SettingsPage.tabs.test.tsx` (path UNVERIFIED — file present per digest). 🟢

### 6.2 Cache layer (`pkg/ui/frontend/src/hooks/useApi.ts`)

- Three existing hook families: `useModels`, `useImgGenModels`, `useDecisionModels`. Each has 3 mutation methods (add/update/delete) × 3 caches invalidated by explicit key. 🟢
- Cache keys: `'models'` (:271), `'imggen-models'` (:373), `'decision-models'` (:476). 🟢
- Mutation triple-delete pattern (NEVER `deleteByPrefix`):
  - `useModels` → :300-302, :315-317, :325-327 (triple delete before refetch). 🟢
  - `useImgGenModels` → :402-404, :417-419, :427-429. 🟢
  - `useDecisionModels` → :503-505, :517-519, :526-528. 🟢
- **New `'group-models'` cache ⇒ quadruple delete at every mutation site.** 🟢❗
- Test files to extend: `useModels.test.ts:41-43`, `useImgGenModels.test.ts:107-109/138-140/167-169`, `useDecisionModels.test.ts:42-44`. 🟢
- Docblocks :296 (C-07 symmetric side), :348, :455-456 must be updated with the rationale for 4 keys. 🟢

### 6.3 Forms & editors (the new components)

- DecisionModelForm.tsx (527 lines): payload :42-53; `HARDCODED_*` constants :18-20 stamped :274-281; `filterCredentialsByProvider` :122-127; aria errors :145-152; provider-switch clears :192-206; submit guard :246-255. 🟢
- **GroupModelForm.tsx (NEW, similar structure)**: payload includes `{ id, name, kind: 'group', members, claim_id, exclude_from_ultimate_switching: true, fallback_chain? }`; member-selector combines `CredentialRef rows × ModelConfig rows` (heterogeneous upstreams); submit guard validates member list (min 1, max 16, no dup member IDs). 🟢
- **MultiCredentialEditor.tsx — path correction**: actual file is at `pkg/ui/frontend/src/components/config/MultiCredentialEditor.tsx`, NOT `components/MultiCredentialEditor.tsx` as the digest path suggested. See §9.1 anchor corrections. 🟢

### 6.4 MultiCredentialEditor invariants (the precedent)

- `MAX_CREDENTIALS = 16` (config.go:84 + store.go:1623-1625 — mirrored as constant at file top). 🟢
- `MIN_CREDENTIALS = 1` (handleRemove at :59 refuses to drop the last row). 🟢
- Row 0 is Primary (annotation in submit at :59). 🟢
- `isOptionDisabled` (:70-95): duplicate prevention (:76-81); **mixed-provider guard (:83-93) — all rows must share a provider**. 🟢❗
- **GroupMemberEditor must lift the mixed-provider constraint** — different members point at different providers by design. New `GroupMemberEditor.tsx` (or similar) disables ONLY duplicate-member-id and weight<=0; allows mixed providers. 🟢

### 6.5 The 4-arm lockstep filter

| Arm | File:line | Code |
|-----|-----------|------|
| 1 | `pkg/ui/server.go:758-789` | switch on `?kind=` → group arm + chat-arm extended to exclude group |
| 2 | `pkg/proxy/handler.go:245-272` | `/v1/models` listing — group NOT excluded (groups are chat); add `?kind=` query parsing here too |
| 3 | `pkg/ui/frontend/src/components/ModelsTab.tsx:162` | chat filter: `model.kind !== 'group'` exclusion (current filter pattern visible at :145-161 — add `&& model.kind !== 'group'`) |
| 4 | TokenForm pickers (Decision amendment 7) | chat-arm picker must exclude groups; group-arm picker must list groups |

🟢❗ **The chat-arm leak precedent (decision-model-type highest-severity review finding)** mandates that ALL 4 arms extend together. A 5th arm could be added: `GET /fe/api/tokens/[id]/allowed-models` picker — verify the call site during write.

### 6.6 Tests & baselines

- Tests live under `pkg/ui/frontend/src/**/__tests__/` (vitest.config.ts:12). 🟢
- vitest 3.2.7 + happy-dom 15.11.7 + @testing-library/preact. Host has no chromium; happy-dom computes no layout. 🟢
- **tsc baseline at HEAD `5b13388`**: 26 instances / 11 files. New code must not regress this baseline. 🟢
- Embedding discipline: any commit touching `pkg/ui/static/index.html` MUST include the regenerated hashed bundles (Vite content-hashed) in the SAME commit. A fresh clone with stale hashes 404s the UI because `//go:embed static/*` references missing assets. Plan should note: `vite --outDir /tmp` for read-only verification. Builds are byte-reproducible (sha256-stable). 🟢

### 6.7 Naming freedom

- Group tab id: `'group_models'` (matches snake_case convention). 🟢
- Component names: `Group/`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx`. 🟢
- Hook name: `useGroupModels`. 🟢
- Cache key: `'group-models'`. 🟢
- Docblock comments + 3 test files for cache-key correctness. 🟢

---

## 7. Guards & Ultimate

### 7.1 Misroute guards (chat side)

- `pkg/proxy/handler.go:456-462`: `if rc.resolvedModel != nil && rc.resolvedModel.IsImageGen() { … 400 image_gen_misroute }`. 🟢
- `:474-480`: `if rc.resolvedModel != nil && rc.resolvedModel.IsDecision() { … 400 decision_misroute }`. 🟢
- For groups: **NO misroute guard on the chat path** — groups ARE chat. They belong on /v1/chat/completions. ✅ correct behavior. The guard IS needed in the inverse direction: a chat-kind model (non-group) requested at a group-shaped `/v1/group_routing` endpoint (if one exists) is nonsensical since no such endpoint exists. Plan-time certainty: groups stay on /v1/chat/completions. 🟢

### 7.2 `IsModelAllowed` (token allow-list)

- `pkg/auth/token.go:133-143` — exact-match `AllowedModels[i] == model.ID`. 🟢
- For groups: a token allow-listed for `group.ID` should authorize requests routed through the group. A token allow-listed for `member.ID` (an underlying chat-kind model) should authorize direct requests against that member row but NOT requests through the group — these are different identities. 🟢
- Migration 024 conversion was ID-based; tokens are NOT name-aliased. Shadowing does NOT affect allow-list semantics (a token with `AllowedModels: ["legacy-id"]` continues to authorize the underlying chat row). 🟢
- **Open question**: should the group itself be allow-list-authorizable? Or is the token's allow-list of the group a SEPARATE field (`AllowedGroups`)? Or is shadowing sufficient that the token can simply list either the group ID or the legacy ID? See §9.

### 7.3 Ultimate gate (ultimatemodel/handler.go:746-768)

- `GetModel` is kind-blind; dispatch keys on `Internal` only. 🟢
- Runtime gate at `handler.go:696` checks ONLY `ExcludeFromUltimateSwitching`. 🟢
- Write-time gate at `pkg/models/config.go:283-285` (D9 — image-gen has `ExcludeFromUltimateSwitching == true` written at create). For groups the same D9 discipline applies: **`kind:"group" ⇒ `ExcludeFromUltimateSwitching: true`** by default at write time. 🟢
- UI gates at `pkg/ui/server.go:1058-1065` and `:1327-1332`: POST and PUT must enforce the same default. 🟢

🟢 Group models must default to `ExcludeFromUltimateSwitching: true`. This is the single D9-derived rule.

### 7.4 Atomic gates summary

| Gate | Site | Behavior |
|------|------|----------|
| Misroute (chat-side) | handler.go:456-462, :474-480 | groups NOT misroute-rejected |
| Misroute (Anthropic) | handler_anthropic.go:195-207 (decision); :192-194 (imggen gap) | group needs NO analog; chat-only |
| `IsModelAllowed` | auth/token.go:133-143 | exact ID match; tokens list group ID OR member ID |
| Ultimate gate | `pkg/proxy/handler.go:696` *(corrected — §9.1 #16; was wrongly cited as `ultimatemodel/handler.go:696`)* | kind-blind; checks only `ExcludeFromUltimateSwitching` |
| Ultimate write default | config.go:283-285 | D9: `true` for non-chat kinds |
| UI POST default | server.go:889-895 | mirror write-time |
| UI PUT default | server.go:1198-1205 | mirror write-time |
| Parity test | pkg/ui/kind_rules_parity_test.go:165-180 | 3 independent sites; add 1 for group |

---

## 8. Seam Analysis (ranked integration points)

### 8.1 Resolver seam (HIGHEST — blocker + largest surface)

**Surface:** `pkg/store/database/store.go:2170-2267` (`ResolveInternalConfigWithAffinity`) + JSON twin `pkg/models/config.go:1012-1064`.

**Blocker:** `resolveWithCredential` (store.go:2107-2126) returns a single `actualModel string`. One model row = one upstream model string.

**Required extension shape:** A group model row carries `[]GroupMemberConfig` JSON. The resolver must:
1. Detect `m.Kind == "group"`.
2. Run affinity/weighted-pick over `len(m.Members)` slots to select one member.
3. Use the **member's `(provider, baseURL, internal_model, credential_id)`** to populate the `ResolvedCredential`.
4. Return `NewlyBound = (member_changed || first_sticky_save)` — a per-(groupID, convKey) → memberID binding.

**Engine factory:** A new `EngineGroupRegistry map[groupID]*Engine` lazily constructed on first resolve. One engine per group preserves all 8 mechanical clone couplings. Each engine's `modelState` is keyed by **member identity** (e.g. `memberID` or `(provider, upstreamModel)` tuple), with bindings and cooldowns per (convKey, memberID). Cooldowns are now keyed by `(memberID, credentialID)` if the engine is also multi-cred at the member level — deferred for v1 unless members are multi-cred too.

**Files touched (top-of-head):** store.go (4 sites), models.go (GroupMemberConfig struct + alias), pkg/models/config.go (Resolve twin + JSON member shape), pkg/credentiallb/engine.go (per-engine-key factory on the manager; not on the engine itself), plus tests.

🟢

### 8.2 Response-identity seam (HIGH — masking decision)

**Surface:** OpenAI stream relay `handler.go:1172-1184`, OpenAI non-stream final body `handler.go:1587`.

**Masking shape:** After the upstream stream ends, post-process the buffered output to replace any `"model":"<actual-target>"` with `"model":"<client-id>"` (the group ID). For non-stream: edit the JSON body before write. For stream: edit on each SSE event with a `model` field.

**Existing template:** `streamID` cache at `handler_helpers.go:92-95` is the masking template; extends naturally.

**Fallback for non-mask case:** if the masking is undesirable (user wants to debug), a `?passthrough=true` toggle OR a per-model `echo_upstream_model bool` field can opt-out. Plan-time decision.

🟢

### 8.3 FE cache seam (MEDIUM — explicit symmetric invalidation)

**Surface:** `useApi.ts` — **9 mutation sites** confirmed by architect verification 2026-10-16 *(corrected — §9.1 #13; prior text said "12 sites" which was an over-count)*. Specifically:
- `useModels` → `:300-302`, `:315-317`, `:325-327`
- `useImgGenModels` → `:402-404`, `:417-419`, `:427-429`
- `useDecisionModels` → `:503-505`, `:517-519`, `:526-528`

Adding `'group-models'` ⇒ **9 sites × 4 keys = 36 deletions** at the 3 mutation methods (add/update/delete) of the `useGroupModels` hook. The architect also confirmed: **credential/token mutations correctly invalidate NO model caches today** (models reference credentials/tokens by ID — the cross-invalidation is the wrong direction and would be a regression to add).

**Symmetric invalidation rationale (C-07):** when a chat mutation runs, it must invalidate the chat tab AND any sibling tab whose data could conflict. Adding `group-models` makes the cross-tab invalidation 4-key.

🟢

### 8.4 ValidateKindRules seam (MEDIUM — function-exit parity)

**Surface:** `models.ValidateKindRules` (`pkg/models/config.go:176`) + JSON Validate (:971) + DB validateBeforeWrite (:1719+) + UI POST inline (:889-895) + UI PUT inline (:1198-1205).

**Required extension shape:** `validateGroupRules(m *ModelConfig)` enforces:
- `Internal == false` (groups are external-style proxy layer; the inner dispatch is `pkg/proxy`'s job, not the model's)
- `len(Members) >= 1`, `<= 16`
- `len(FallbackChain) == 0` (groups own fallback; inner dispatch handles internal failover)
- `len(SecondaryUpstreamModel) == 0` (same)
- `PeakHourEnabled == false`
- `ReleaseStreamChunkDeadline == 0`
- `ClaimID != ""` OR `len(Aliases) > 0` (intent: group MUST shadow something; the claim IS the artifact)
- `ExcludeFromUltimateSwitching == true` (D9)
- Members: each carries `(provider, upstreamModel, optional_credential_id, weight)`; per-member `weight > 0`; no duplicate `(provider, upstreamModel)` tuples (lock-step pairing)
- No group member may itself be a group (no `m.IsGroup()` recursion; v1 minimalism)

**Group-member provider-match invariant (G-B):** NO provider-match across members — heterogeneous upstreams are the whole point. Per-member provider validation only.

🟢

### 8.5 Lock-step filter arms (MEDIUM — chat-arm leak precedent)

**Surface:** server.go ?kind= switch (:758-789); handler.go /v1/models exclusion (:257-261); ModelsTab.tsx:162 chat filter; TokenForm pickers (decision amendment 7).

🟢

### 8.6 `/v1/models` `?kind=` parity (LOW — feature decision)

`/fe/api/models` already has `?kind=`; `/v1/models` does not. Adding groups doesn't require `/v1/models` to grow `?kind=` — chat-shaped listing is sufficient. Plan-time opt-out for v1.

🟢

### 8.7 Storage JSON column seam (the recommendation back-reference)

**Surface:** `pkg/store/database/querybuilder.go` 8 builder strings × 2 dialects + 2 inline SELECT site pairs (store.go:858-873, :940-955, *corrected — §9.1 #12*) + scan loops + write bindings (Profile A — see §5). ~33 mechanical sites (corrected from prior "30"; re-derive exact count at implementation). Plus validation/count-assert/test pairs.

🟢

---

## 9. Anchor Corrections Log + Open Integration Questions

### 9.1 Anchor corrections (digest → verified)

| # | Digest claim | Verified reality | Note |
|---|--------------|------------------|------|
| 1 | `pkg/ui/frontend/src/components/MultiCredentialEditor.tsx` | File actually at `pkg/ui/frontend/src/components/config/MultiCredentialEditor.tsx` (under `config/` subdir). | Path correction only; the file body (225L, MAX 16, MIN 1, mixed-provider guard :83-93) is unchanged. Update all references. |
| 2 | `MultiCredentialEditor.tsx` (225L) line count | UNVERIFIED — file read in this pass only yielded lines 1-100; total length not directly measured. | Plan should `wc -l` before writing. |
| 3 | `SettingsPage.tabs.test.tsx` referenced | UNVERIFIED — file existence + path not directly read; rely on the SettingsPage.tsx:52 `TabType` union as the canonical anchor. | If the tabs test exists it asserts the 10-slot count + `data-tab-id` + `aria-hidden` glyph; verify on write. |
| 4 | `pkg/ui/server.go:1075-1340` end of decision inline mirror | Decision PUT inline ends at :1333 (verified). The PUT inline block continues from :1198-1205 with the kind-discrimination logic; the rest of that range is shared body construction. | Cite the range as :1198-1333 for the decision PUT inline arm. |
| 5 | `response.go:169 is tool_calls, NOT a rewrite` | Verified — response.go:169 is the tool_calls writer; response.go:38 is the model-field rewrite for non-stream; incremental_stream.go:458 is the stream `message_start` rewrite. | All three anchors are correct and distinct. |
| 6 | `pkg/auth/token.go:133-143` exact-ID match | Verified — the allow-list semantically matches `m.ID`. Groups flow through this same gate; the open question of `AllowedGroups`-vs-`AllowedModels` is tracked in §9.2. | ✓ |
| 7 | `pkg/proxy/handler.go:257-261` imggen skip; decision exclusion | The VERIFIED misroute guards at :456-462 (imggen) and :474-480 (decision) are correct. The /v1/models listing predicate at :245-272 skips imggen at :257-261; decision's exclusion in /v1/models was added but the line range was not directly verified in this pass — UNVERIFIED by file read, but the precedent is solid. | Plan should re-verify decision's listing-exclusion line on write. |
| 8 | `pkg/ultimatemodel/handler.go:24/:101` retry milestones [5,10,20,30,40] | Retry-threshold array — line range:24/:101 is from the digest and not directly verified in this pass; UNVERIFIED. | Plan should re-read these exact lines before quoting in plan.md. |
| 9 | `pkg/decision/handler.go:89-90` 5s meterTimeout context | UNVERIFIED — file read not performed in this pass; the digest refers to it as "the better pattern". | Re-verify before quote. |
| 10 | `pkg/ui/kind_rules_parity_test.go:181, :188-267, :165-180, :43-53` line numbers | UNVERIFIED — file not read in this pass. | Re-verify before quoting. |
| --- | --- | --- | --- |
| 11 | `pkg/ultimatemodel/modelscache` path *(§1.3 row #20)* | **WRONG.** Architect verified the package is `pkg/modelscache` (NOT under `pkg/ultimatemodel`). Source: architecture-recommendation.md §13.4 (Open Question 4) routes this correction to research-codebase.md explicitly. Updated cite also adds `negByID` negative-entries under `ModelsNegTTL=60s` at `models.go:55-56, :375`, and `AddModel`/`UpdateModel`/`RemoveModel` (`:643`/`:665`/`:687`) evict only by the row's own ID ⇒ **claim-eviction gap** that Architect Amendment 4 addresses. | Path correction + sharper facts added inline at §1.3 row #20. |
| 12 | §5.2 storage arithmetic | **WRONG.** Architect verified at 5b13388: SELECT column lists are **20→22** (not 19→21); `dbModelRow` 20→22 fields; `scanModels` 20→22 Scan targets; INSERT/UPDATE placeholders **19→21** (not 19→20). Additionally, prior text missed the **inline SELECTs** at `store.go:858-873` (GetModel) and `:940-955` (GetModelByName) that **bypass `querybuilder` entirely** — both need +2 columns. Total mechanical sites revised from ~30 → ~33. Re-derive exact builder-string count at implementation per Open Question 3 (docs disagree 8/12/16/18). | Corrected inline at §5.2 + §8.7 + References row querybuilder_test. |
| 13 | §8.3 FE cache seam "12 sites" | **OVER-COUNT.** Architect confirmed exactly **9 mutation sites** in `useApi.ts` (3 hooks × 3 mutations each: `useModels` `:300-302`/`:315-317`/`:325-327`, `useImgGenModels` `:402-404`/`:417-419`/`:427-429`, `useDecisionModels` `:503-505`/`:517-519`/`:526-528`). The "12 sites" was (3 × 3 + 3 refs) — refs are reads, not mutations. **Credential and token mutations correctly invalidate NO model caches today** (models reference credentials by ID; cross-invalidation is the wrong direction). | Corrected inline at §8.3. |
| 14 | §3.5 metering anchor labels + lines | **WRONG anchors.** Architect re-verified: `handler.go:908-934` is the **ultimate re-execution branch** (NOT stream — the plan previously mislabeled it; research inherited the same mislabel); **true stream** is `handler.go:1446-1472` (inside `streamResult`); **non-stream** is `handler.go:1656-1682`. Implication for groups: **ultimate site must be EXCLUDED from member-row metering** (the served target is the ultimate model, not a member). | Corrected inline at §3.5. |
| 15 | §3.6 `RequestMetadata{ClientModel,UpstreamModel}` *(`adapter_openai.go:53-58`)* "declared but never wired" framing | **UPHELD by Architect Round 1.** Architect confirmed: the struct is populated only by `ParseRequest`, which has **zero production callers** (`grep` shows `handler_functions.go:88` calls only `ToStoreMessages`; handler/race_executor paths never call `ParseRequest`). The seam is **dead code** + adapter-seam masking is **priced out for v1** (would require threading `ParseRequest` through the production chain first — disproportionate). Research's framing held; the plan's claim that the seam exists-and-is-populated was wrong. | Strengthened inline at §3.6. |
| 16 | `ultimatemodel/handler.go:696` as ultimate-exclusion runtime gate (§8.4 table row + References list) | **WRONG anchor** — the same F2-class anchor error that Review Round 1 fixed in `plan.md`/`decisions.md`. The runtime gate that checks `ExcludeFromUltimateSwitching` lives at `pkg/proxy/handler.go:696` (the proxy-side gate), NOT `pkg/ultimatemodel/handler.go:696` (which is part of the ultimate-handler itself, gate-free w.r.t. exclusion). The companion citation `pkg/ultimatemodel/handler.go:746-768` (kind-blind dispatch) **remains correct** — only the `:696` half is wrong. Caught by dispatcher cross-file grep, 2026-10-16. | Marked inline at §8.4 table row + References list; the `:746-768` companion kept as-is. |

### 9.2 Open Integration Questions (numbered — to feed plan.md / decisions.md)

#### Question 1 — name vs id shadowing (CORE)
Is the **claim mechanism** a single `claim_id TEXT NULL` column, a `aliases JSONB` list, or `[]string aliases` on a sibling table? The 3 forms differ on UI representation, on migration backfill of legacy rows, and on rendering /v1/models. (Default proposed: `aliases TEXT NOT NULL DEFAULT '[]' JSON` array — the same shape as `fallback_chain` precedent.)

#### Question 2 — allow-list semantics
Does a token's `AllowedModels` accept **either** the group ID **or** a member ID **or** both? The simplest invariant: each entry in `AllowedModels` continues to authorize a single `model.ID` (group or chat). Authz is orthogonal to kind; no new `AllowedGroups` field is needed. (Default proposed: simplest invariant; one auth surface.)

#### Question 3 — provider-match divergence
Which of G-A / G-B / G-C (§4.6) is canonical? Default proposed: **G-B** (separate `validateGroupMembers`) because future per-member rules (timeout, retry, weight) will plug into the same shape.

#### Question 4 — storage profile
A (JSON column) or B (separate table)? Default proposed: **A** (§5.5).

#### Question 5 — masking vs leak
OpenAI stream/non-stream: mask `model` field to client name, or pass verbatim? Default proposed: **mask**; menu-toggle for diagnostic passthrough deferred.

#### Question 6 — per-group engine vs shared engine
A per-group engine preserves binding/cooldown isolation; a shared engine keyed by `groupID` is slightly cheaper to instantiate but couples cooldowns across groups sharing a credential. Default proposed: **per-group engine** for v1, lazy-instantiated on first resolve.

#### Question 7 — member-cooldown keying (the credentialID-only limitation)
Cooldown keys today: `credentialID`. With members sharing a credential, a 429 on `(provider X, model A)` cools the entire credential, not just `(X, A)`. New key: `(credentialID, memberID-or-upstreamModel)`. Cost: per-group engine + a small extension to `modelState.cooldowns`. (Default proposed: extend `cooldowns` key to `map[string]time.Time` where the key is `credentialID + "|" + memberKey` for groups; chat-kind models retain `credentialID`-only keying.)

#### Question 8 — escalation
Should `/v1/models` accept `?kind=group` for client-side listing parity with `/fe/api/models?kind=group`? Plan-time opt-out for v1 (✅ deferrable).

#### Question 9 — `mcp_servers` parity / cross-pollination
Is there a similar group concept in MCP servers (a single MCP server pointing at multiple physical backends)? UNVERIFIED absent a fresh grep. If yes, can the group's engine infrastructure be reused? (Default proposed: out-of-scope for v1; track in `v2 candidates`.)

#### Question 10 — restart-cold-start UX
Document the restart behavior: after restart, sticky affinity is lost; clients may land on a different member for the same conversation until a new first-message hash establishes a binding. Should the cold-start warning be surfaced as a `warning` log line per resolve, or as a config toggle? (Default proposed: log line only.)

#### Question 11 — observability
Should each member-resolution emit a `model_member_selected` event with `member_id` + `upstream_provider` + `upstream_model`? Useful for ops; a slight overhead. (Default proposed: yes, mirroring `model_credential_selected` precedent from `pkg/store/database/store.go:2139` doc-comment.)

#### Question 12 — FE rounding
Group member row layout: show `(provider badge, model-id text, weight input, remove button)` rows? Or compact pill-list with edit-modal? Decision-model-type precedent uses pills-with-edit-modal — recommend that shape for v1.

#### Question 13 — group, yet `Internal=false`?
A group dispatches internally (the engine picks a member which calls an upstream). But `resolveWithCredential` reads `modelConfig.Internal` first; if true, dispatch goes through credential resolution. For group kind, `Internal = false` matches: the group is at the routing layer, the **member itself** is internal (model.InternalModel = upstream-string). Does the proxy need an explicit `group.Internal = false` or default? (Default proposed: `Internal = false`, same as a non-internal chat model.)

#### Question 14 — group→group fallback
Is `FallbackChain: ["<another-group-id>"]` legal? Two groups may want to shadow each other across regions. (Default proposed: legal in v1 — the fallback chain re-resolves through the resolver, which handles group→group recursion naturally. Cost: boundary check on group→group recurse depth in debug paths.)

### 9.3 Architect Round 1 (2026-10-16) — provenance

**Upheld:** (a) `RequestMetadata` dead-seam framing (§3.6, §9.1 #15); (b) 9-site cache-delete count direction (§8.3, §9.1 #13 — confirmed); (c) the 4-arm lockstep filter, the resolver seam, the masking direction, and the JSON-column + migration 031 storage posture.
**Corrected:** (a) `pkg/modelscache` path (§1.3, §9.1 #11); (b) §5.2 storage arithmetic — SELECT 20→22, INSERT/UPDATE 19→21, dbModelRow/scanModels 20→22, inline SELECTs at store.go:858-873/:940-955 (§9.1 #12); (c) "12 sites" → 9 sites (§8.3, §9.1 #13); (d) §3.5 metering anchor re-labels with ultimate-exclusion implication (§9.1 #14).
**Source:** `.agents/shared/planning/load-balance-groups/architecture-recommendation.md` §5 finding 1, §7 finding 1, §8 finding 1, §10 Amendments 7/8/18, §13.4 (and §0 verdict table FA5/FA7/FA8). All corrections marked inline; original text preserved per dispatch contract.
**Review Round 1 (2026-10-16):** F2-class anchor fixed in §8 + References (dispatcher-caught residual — `ultimatemodel/handler.go:696` → `pkg/proxy/handler.go:696`; see §9.1 #16).

---

## References

- `pkg/models/config.go:93-118` — Kind constants `KindChat, KindImageGen, KindDecision` (verified verbatim).
- `pkg/models/config.go:176` — `ValidateKindRules` switch (verified).
- `pkg/store/database/migrate.go:24-64` — migration registry, ordered-slice trap comments :53-62 (verified verbatim).
- `pkg/store/database/store.go:2107-2126` — `resolveWithCredential` blocker (verified verbatim; line 2118 = actualModel = modelConfig.InternalModel).
- `pkg/store/database/store.go:2170-2267` — `ResolveInternalConfigWithAffinity` branch table (verified verbatim).
- `pkg/store/database/store.go:2139` — `ResolvedCredential = models.ResolvedCredential` alias doc-comment (verified).
- `pkg/credentiallb/engine.go:35-65` — Engine struct (verified verbatim).
- `pkg/credentiallb/selector.go:16-65` — weightedSelector prefix-sum (verified verbatim).
- `pkg/credentiallb/key.go:40-50` — `ComputeConversationKey` (verified verbatim).
- `pkg/credentiallb/doc.go:42-55` — E-1..E-3 invariants (engine E-1 two-level locking).
- `pkg/credentiallb/engine.go:609-719` — `ExcludeAndReselect` semantics; :510-542 outcome contract.
- `pkg/proxy/handler.go:245-272` — `/v1/models` exclusion predicate.
- `pkg/proxy/handler.go:456-462, :474-480` — misroute guards (imggen, decision) verified.
- `pkg/proxy/handler.go:534-548` — OpenAI conversation-key derivation.
- `pkg/proxy/handler.go:1172-1184, :1587` — OpenAI stream/non-stream relay (response-identity seam).
- `pkg/proxy/handler_anthropic.go:305-414` — sequential fallback loop.
- `pkg/proxy/handler_anthropic.go:296` — Anthropic conversation-key derivation.
- `pkg/proxy/race_coordinator.go:1409, :1521` — race state-machine model resolution.
- `pkg/proxy/race_executor.go:55, :121, :177, :275` — per-attempt re-fetch.
- `pkg/proxy/race_coordinator.go:1465` — `credFailoverPlanLocked` → `engine.ExcludeAndReselect`.
- `pkg/proxy/race_coordinator.go:344-351` — race-coordinator wiring.
- `pkg/proxy/adapter_openai.go:53-58, :100-106` — `RequestMetadata` unused; `WriteStreamEvent` pure passthrough.
- `pkg/proxy/handler.go:696`, `pkg/ultimatemodel/handler.go:746-768` *(corrected — §9.1 #16)* — runtime gate + kind-blind dispatch.
- `pkg/ui/server.go:758-789, :889-895, :979-1066, :1198-1205, :1333` — FE API mirror arms (verified).
- `pkg/ui/server.go:1058-1065, :1327-1332` — UI ultimate default gates.
- `pkg/ui/frontend/src/components/SettingsPage.tsx:52, :82` — `TabType` union + active-tab `useState` (verified).
- `pkg/ui/frontend/src/hooks/useApi.ts:271, :300-302, :315-317, :325-327` — chat hook + triple-delete (verified verbatim; C-07 + amendment 9b).
- `pkg/ui/frontend/src/hooks/useApi.ts:373, :402-404, :417-419, :427-429` — imggen hook + triple-delete.
- `pkg/ui/frontend/src/hooks/useApi.ts:476, :503-505, :517-519, :526-528` — decision hook + triple-delete.
- `pkg/ui/frontend/src/components/config/MultiCredentialEditor.tsx` (225L UNVERIFIED; MAX 16, MIN 1, mixed-provider :83-93 verified verbatim on first 100 lines).
- `pkg/store/database/querybuilder.go:94-124, :135-177, :189-212, :215-238, :241-264, :267-289` — Insert/Update/4 SELECT builders (12 strings × 2 dialects for 4 SELECTs = 8 SELECT strings).
- `pkg/store/database/querybuilder_test.go:239, :262, :275` — column-count pins (must bump 19→21 for Profile A; INSERT/UPDATE placeholders, *corrected — §9.1 #12*).
- `pkg/store/database/store.go:455-484` — `dbModelRow` struct.
- `pkg/store/database/store.go:1358-1365` — `ErrDuplicateModelID`.
- `pkg/auth/token.go:133-143` — `IsModelAllowed` exact-ID match.
- `pkg/models/config.go:694-699` — JSON twin `ErrDuplicateModelID`.
- `pkg/models/config.go:1012-1064` — JSON twin `ResolveInternalConfig`.
- `pkg/models/credential.go:33-38` — `CredentialConfig` (verified).
- `pkg/decision/handler.go:89-90` — 5s meterTimeout (UNVERIFIED).
- `pkg/ui/kind_rules_parity_test.go:181, :188-267, :165-180, :43-53` — parity test (UNVERIFIED in this pass).
- Plan precedents: `.agents/shared/planning/decision-model-type/research-codebase.md` (181L — format source; never content), `.agents/shared/planning/model-credential-load-balancing/` (engine substrate source), `.agents/shared/planning/exclude-ultimate-switching/` (D9 default-write precedent).
- House style: `pkg/store/database/migrate.go:53-62` — *ordered slice, not directory scanner* trap comments.
- Project blueprint: Project Core & Composition Root, Provider Adapters & Protocol Translation, Proxy Request Lifecycle & Race Execution (project-blueprint context — used as soft reference only).
