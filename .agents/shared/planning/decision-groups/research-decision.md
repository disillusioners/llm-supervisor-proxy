# Research — `/v1/decisions` lane end-to-end (for "exhaustive pre-commit member fallback" on a decision group)

Basis: worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`,
branch `feature/decision-groups` @ `24c51ef` (== origin/latest).
Read-only research. Nothing was built, run, or modified except this file.
All line numbers are from THIS worktree.

---

## 0. Facts that decide the design shape (summary)

1. The decision lane is **single-attempt, single-credential, non-streaming**: one upstream POST, no
   retry/fallback/race loop, no credential rotation, no group awareness.
   (`pkg/decision/handler.go:430-433` is an explicit "no retry / fallback / race" comment.)
2. The lane's **commit point is `w.WriteHeader(resp.StatusCode)` at `pkg/decision/handler.go:517`**
   (PassAsIs) / `:541`/`:543` (PromoteToError) — everything before it is pure computation, so a
   pre-commit multi-member loop is mechanically possible without touching the response writer.
3. A **group id posted to `/v1/decisions` today returns 400 `decision_misroute`**, because the
   resolver returns the group row and the handler's `IsDecision()` check rejects anything that is
   not `kind=decision` (`pkg/decision/handler.go:253-277`, gate at `:265`).
4. **Decision-kind models cannot even be group members today**: G3 requires chat-kind members
   (`pkg/models/group.go:180-184`), and the runtime re-validation predicate is chat-only
   (`pkg/store/database/store_group_rewrite.go:291-301`).
5. The decision lane touches **no** group engine, **no** credential-LB exclusion, **no** skip cache,
   **no** sticky engine — only `modelsConfig.GetModel` and `ResolveInternalConfigWithAffinity`.
6. There is **no conversation key** on this lane; the credential binding key is literally
   `(modelID, modelID)` (`cmd/main.go:324-328`), so per-request sticky affinity does not exist and
   must not be invented.
7. Both of the user's live providers (`openai`, `openrouter`) are in the decision allow-list
   (`pkg/models/config.go:295`, `pkg/ui/server.go:1171`, `pkg/ui/server.go:1636`,
   `pkg/decision/registry.go:49-53`).

---

## 1. Handler flow — `pkg/decision/*`

### 1.1 Construction and injection

- `Handler` struct fields: `configMgr, bus, tokenStore, usage, resolveModel, resolveCredential`
  (`pkg/decision/handler.go:104-111`). There is **no** group engine, credential engine, skip cache
  or sticky engine field.
- `NewHandler(configMgr, bus, tokenStore, usageCounter)` (`pkg/decision/handler.go:124-131`).
- Config seam is minimal: `configGetter` exposes only `GetDecisionTimeout()`
  (`pkg/decision/handler.go:116-118`).
- Resolver seams are function types set from `main.go`:
  `modelResolver func(modelID string) (*models.ModelConfig, bool)` (`:737`),
  `credentialResolver func(modelID string) (models.ResolvedCredential, bool)` (`:738`),
  setters `SetModelResolver` / `SetCredentialResolver` (`:740-752`), nil-safe wrappers
  `safeResolveModel` / `safeResolveCredential` (`:754-766`), adapter constructors
  `NewModelResolverFromConfig` (`:768-779`) / `NewCredentialResolverFromConfig` (`:781-788`).

### 1.2 Request path, step by step (file:line)

| # | Step | Line |
|---|------|------|
| 0 | Route registered: `mux.HandleFunc("/v1/decisions", decisionHandler.HandleDecision)` | `cmd/main.go:377` |
| 0 | Handler constructed + resolvers installed | `cmd/main.go:320-328` |
| 0 | Outer middleware chain `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))` | `cmd/main.go:461` |
| 1 | POST-only guard → 405 plain text (`http.Error`) | `pkg/decision/handler.go:157-160` |
| 2 | `MaxBytesReader` 16 MB; raw body buffered ONCE (`rawBody`) | `:163`, `:169` |
| 2a | over-cap → 413 house envelope | `:170-181` |
| 2b | read failure → 500 | `:183-191` |
| 3 | Auth: `authenticate` → `extractAPIKey` + `ValidateToken` under a 5 s ctx; `tokenStore == nil` ⇒ auth disabled | `:196-205`, `:566-581`, `:583-595`, `:567-569` |
| 3a | auth failure → 401 `authentication_error` envelope | `:716-721` |
| 4 | Typed parse into `DecisionRequest` (raw bytes preserved for byte-faithful pass-through) | `:210-220` |
| 4a | missing `model` → 400 | `:221-229` |
| 5 | `validateFirstFailed(&req)` typed gotcha validators → first failure 400 | `:236-246`; validator list `pkg/decision/validators.go:61-71`; runner `:42-52` |
| 6 | `safeResolveModel(modelID)`; nil → 404 `unknown model %q` | `:253-264` |
| 6a | `!model.IsDecision()` → 400 + code `decision_misroute` | `:265-277` |
| 7 | Token allow-list (`AllowedModels` non-empty AND model not in list) → 403 | `:281-293`; writer `:723-733` |
| 8 | `safeResolveCredential(modelID)` → 502 `no credentials configured for model %q` | `:299-310` |
| 9 | `lookup(cred.Provider)` on the local registry → 502 `no decision adapter registered for provider %q` | `:316-328`; registry `pkg/decision/registry.go:39-42` |
| 9.5 | System One pre-flight (type-asserted `*systemOneProvider`) → 400 before upstream | `:343-356`; impl `pkg/decision/typesafe.go:77-79` |
| 10 | `provider.BuildUpstream(cred, &req, rawBody)` → 502 + code `decision_upstream_build_failed` | `:361-375` |
| 11 | Timeout ctx `context.WithTimeout(context.Background(), h.decisionTimeout())` | `:383-384`; knob `:136-145` |
| 12 | `http.NewRequestWithContext`; `Content-Type: application/json`; `Authorization: Bearer <cred.APIKey>`; client auth + CT + Accept-Encoding stripped | `:388`, `:402-403`, `:419-428` |
| 13 | **single** `sharedHTTPClient.Do(upstreamReq)` | `:433` |
| 13a | transport error → 504 on `context.DeadlineExceeded`, else 502 | `:434-455` |
| 14 | response read capped 64 MB (+1 over-cap branch) | `:460-487` |
| 15 | `provider.InspectResponse(respBody, &req)` | `:492-505` |
| 16 | **Relay write / COMMIT POINT** | `:510-548` |
| 17 | Meter on success only (`RelayStatus == 0`) | `:554-556`, `:609-641` |
| 18 | Final telemetry event | `:561`, `:662-693` |

### 1.3 Call shape — strictly non-streaming

- `DecisionRequest` has only `model`, `input`, `questions`, `safety_identifier`
  (`pkg/decision/types.go:130-135`) — no `stream` field, no SSE path.
- Response envelope is a **typed answer document**: `DecisionResponse{Model, Answers []Answer, Usage}`
  (`pkg/decision/types.go:146-150`); `Answer` carries `type/name/probability/choice/probabilities/confidence/score/refusal`
  with a custom `MarshalJSON` collapsing score+choice probability arrays onto the single wire key
  `probabilities` (`pkg/decision/types.go:167-193`, marshal `:202-219`).
- Success relay: status = upstream status (`resp.StatusCode`) and body = `json.Encode(typedResp)` or
  the raw bytes when typed is nil (`pkg/decision/handler.go:510-528`).
- Error relay: adapter's `Outcome.RelayStatus` written verbatim; body preserved verbatim from the
  upstream (`:529-548`); `RelayStatus == 200` on a failure is clamped to 502 (`:534-543`).

### 1.4 The COMMIT POINT (crux for pre-commit fallback)

- First write to the response writer on the success path: `w.Header().Set(...)` at `:516`, then
  `w.WriteHeader(resp.StatusCode)` at `:517`.
- First write on the promoted-error path: `w.WriteHeader(...)` at `:541` or `:543`, body at `:545`.
- Every earlier exit already wrote a terminal error and returned (steps 1-15). So **any fallback that
  happens between step 8 and step 15 is pre-commit**; once `:517` fires the outcome is final.
- Note: the lane never sets headers before the write (no early `Content-Type`), so a pre-commit loop
  can run to completion with the writer untouched — unlike the Anthropic lane which must guard on
  `payloadCommitted` because it streams (`pkg/proxy/handler_anthropic.go:664-672`).

### 1.5 Timeout posture

- One **per-request** deadline covering the entire upstream call:
  `context.WithTimeout(context.Background(), h.decisionTimeout())` (`pkg/decision/handler.go:383`).
  It deliberately does NOT derive from `r.Context()` (comment `:377-382`).
- `h.decisionTimeout()`: nil config or `<= 0` → package default 30 s (`:136-145`, const `:74`).
- Config: field `DecisionTimeout` (`pkg/config/config.go:143`), default 30 s (`:275`), clamp
  `[10s, 60s]` in `Validate` (`:312-323`), accessor `GetDecisionTimeout` (`:720-724`),
  interface member (`:179`), DB manager impl `pkg/store/database/store.go:330-333`.
- HTTP client: `Timeout: 0`, `ResponseHeaderTimeout: 0` — intentionally context-driven
  (`pkg/decision/client.go:22-39`).
- **There are NO per-attempt deadline picks.** A multi-member loop would inherit one shared ctx unless
  the plan introduces per-attempt deadlines (nothing exists to mirror today).

### 1.6 Model resolution mechanism

- Handler calls `safeResolveModel` (`pkg/decision/handler.go:754-759`), which is the function injected
  in `cmd/main.go:321-323`: `modelsConfig.GetModel(modelID)`.
- Production `modelsConfig` is `*modelscache.CachedModelsConfig` when `CACHE_LAYER_ENABLED`
  (default ON) — assigned at `cmd/main.go:182-188` (`modelscache.NewCachedModelsConfig(modelsMgr, ...)`
  `:182`; `modelsConfig = wrappedModels` `:186`; else `modelsConfig = modelsMgr` `:188`).
- `CachedModelsConfig.GetModel` is **claims-first**: an id claimed by an enabled group resolves to the
  GROUP row before any cache hit/negative logic (`pkg/modelscache/models.go:445-473`, claim branch
  `:455-463`). The raw manager mirror is `pkg/store/database/store.go:1037-1050` (claim branch
  `:1041-1048`).
- Credential resolution likewise goes through `modelsConfig.ResolveInternalConfigWithAffinity`
  (`cmd/main.go:324-328`), whose manager implementation resolves by **row identity**
  (`GetModelNative`, claims bypassed) at `pkg/store/database/store.go:2552-2567`.

### 1.7 What happens TODAY when the requested id is a GROUP — quoted

`pkg/decision/handler.go:253-277`:

```go
model, ok := h.safeResolveModel(modelID)
if !ok { ... 404 ... }
if !model.IsDecision() {
    log.Printf("[decision] rejecting decision-route post to non-decision kind model=%s (kind=%s)", model.ID, model.Kind)
    ...
    h.openAIErrorWithCode(w, http.StatusBadRequest,
        fmt.Sprintf("decision models are served by /v1/decisions only (got kind %q)", model.Kind),
        "decision_misroute")
    return
}
```

`IsDecision()` is `m.Kind == KindDecision` (`pkg/models/config.go:144-146`); a group row is
`KindGroup` (`pkg/models/config.go:154-156`). So: **group id → 200-resolution → 400 +
`{"error":{"code"?... "decision_misroute"}}`**, no credential resolution, no upstream call, no metering.
`models.NewOpenAIError(models.ErrorTypeServerError, errorCode, message)` sets the code field
(`pkg/decision/handler.go:699-714`).

Secondary trap: if a member id is **shadow-claimed** by an enabled group, `GetModel(memberID)`
returns the GROUP row (claims-first, `pkg/modelscache/models.go:455-463`), so the decision lane would
also 400 on that member id. Today G8 forbids a non-chat shadow target
(`pkg/models/group.go:196-198`), which is why this cannot fire for decision models yet.

### 1.8 Existing retry / failover inside the decision lane

**None.** Explicit: `pkg/decision/handler.go:430-432` —

```go
// Single upstream call (no retry / fallback / race — the
// decision shape is sync + token-billed, decisions are
// cheap+fast).
resp, err := sharedHTTPClient.Do(upstreamReq)
```

There is no loop over models, credentials or providers anywhere in `pkg/decision` (only loops over
`questions[]` / validators / header copies). Credential rotation on 401/402/429 does not exist on this
lane. `pkg/models/config.go:302-304` (D5) additionally **forbids** `fallback_chain` on decision-kind
models, so even the data model has no chain mechanism.

---

## 2. Credential path

### 2.1 Does the decision lane consult the credential LB / group engine?

- **Not directly.** `pkg/decision` imports only stdlib + `pkg/auth`, `pkg/events`, `pkg/models`,
  `pkg/usage` (`pkg/decision/handler.go:48-64`). Grep for `credentiallb|grouplb|skipcache|sticky|
  ExcludeAndReselect|Cooldown` under `pkg/decision` returns only the comment mentions
  `pkg/decision/handler.go:21` and `:296`.
- Import guards pin this: `TestPackage_ImportGuard_NoProxyImports` (`pkg/decision/handler_test.go:40`),
  `..._NoImggenImports` (`:65`), `..._TransitiveClosure` (`:90`) — a *transitive* closure test, so an
  indirect import of `pkg/credentiallb` through `pkg/proxy` is also blocked.
- **Indirectly yes**: credential selection is `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)`
  (`cmd/main.go:324-328`). With 2+ credential refs the manager calls the credentiallb engine:
  `m.engine.GetOrSelect(modelID, conversationKey)` (`pkg/store/database/store.go:2614-2619`);
  single-credential models take a fast path with **no engine call** (`:2596-2612`); the cache decorator
  hot path is `pkg/modelscache/models.go:680-737`.

So the decision lane's credential "LB" is a **weighted pick + per-model stable binding**, never a
failure-driven exclusion.

### 2.2 Is `ExcludeAndReselectForClass` / `ClearGroupMemberCooldown` / cooldown machinery reachable?

- `ExcludeAndReselectForClass` (`pkg/credentiallb/engine.go:637`, legacy delegate `:615-620`) has only
  two production call sites: `pkg/proxy/internal_handler.go:263` and `pkg/proxy/race_coordinator.go:1900`.
  **Unreachable from `pkg/decision`.**
- `ClearGroupMemberCooldown` lives on `*database.ModelsManager`
  (`pkg/store/database/store_group_rewrite.go:439-444`), forwarded by the cache decorator
  (`pkg/modelscache/models.go:1079-1085`), and consumed by the proxy through an **optional interface
  assertion** inside `clearWinnerUpstreamSkip` (`pkg/proxy/handler_helpers.go:867-886`, assertion
  `:879-885`). That helper is unexported and `pkg/proxy`-only → **unreachable from the decision lane**.
- Cooldown reads: `Engine.ClearCooldown` (`pkg/grouplb/engine.go:1274-1287`),
  `Engine.MemberCooldownUntil` (`:1293-1311`) — both used only from
  `pkg/store/database/store_group_rewrite.go` (write path `:273-282`) which only the chat lanes reach.

### 2.3 Is the global upstream skip cache consulted on this lane?

**No.** `pkg/skipcache` is referenced from `pkg/proxy` (`handler.go:90`, `:166`,
`race_coordinator.go:2106-2107`, `handler_anthropic.go:2607-2608`, `handler_helpers.go:873-876`) and
`pkg/store/database` (`store.go:726` field, `SetSkipCache` `:733`, `store_group_rewrite.go:345-352`).
Zero references under `pkg/decision`. Consequence: a decision upstream failure is currently
**completely unrecorded** in the demotion layer — nothing marks a member or credential as benched.

---

## 3. Affinity / sticky — explicit answer

**NO.** The decision lane has no sticky last-success ordering, no conversation-key binding, and no
session affinity.

Evidence:
- Zero hits for `sticky`, `stickyorder`, `conversationKey`, `binding`, `affinity` under `pkg/decision`
  except two *comments* describing the credential key as `(modelID, modelID)`
  (`pkg/decision/handler.go:21`, `:296`).
- The resolver signature itself cannot carry a key: `credentialResolver func(modelID string) (...)` —
  one argument (`pkg/decision/handler.go:738`); the wiring passes the model id twice
  (`cmd/main.go:327`).
- The sticky order engine is constructed at `cmd/main.go:144` and injected **only** into
  `proxy.NewHandler` (`cmd/main.go:282`); `pkg/proxy` applies it via `applyStickyOrder`
  (call sites `pkg/proxy/handler.go:920` region, `pkg/proxy/handler_groups.go:504`).
- Request body shape gives no natural key anyway: no conversation/message list is required for
  ordering; `input` may be a bare string (`pkg/decision/types.go:130-135`).

Implication for the plan: sticky may **not** be reused or invented on this lane. Any "one honest
attempt per member" ordering must be deterministic (configured order / weight) or reuse the grouplb
weighted selector with an **empty** conversation key (which the engine documents as "fresh weighted
pick, no binding" — `pkg/models/group_rewrite.go:88-90`, `pkg/store/database/store.go:2546-2551`).

---

## 4. Provider vocabulary + registry

### 4.1 Which providers can serve decision models today

- `openai` — byte-faithful OpenAI Decision pass-through (`pkg/decision/openaidecision.go:26-34`,
  `BuildUpstream` `:56`, `InspectResponse` `:140`, `ExtractUsage` `:190`).
- `typesafe` — TypeSafe (Jev) System One transport (`pkg/decision/typesafe.go:14`,
  `NewTypesafeProvider` `:52-58`, default base `https://api.typesafe.ai`).
- `openrouter` — same System One adapter, parameterized (`pkg/decision/typesafe.go:16-22`,
  `NewOpenRouterDecisionProvider` `:63-69`, default base `https://openrouter.ai/api`; URL shape
  `<base>/v1/systemone`).
- Registry init: `pkg/decision/registry.go:49-53` (three `providerRegistry[...] = ...` lines).
- Lookup is lowercase-normalized: `lookup` `pkg/decision/registry.go:39-42`.

### 4.2 The "FE constant + 3 validation gates + registry init"

| Site | Line | What it pins |
|------|------|--------------|
| FE constant | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` | `PROVIDER_OPTIONS = ['openai','typesafe','openrouter']`; form validation message `:227`; default base map `:37` |
| FE types | `pkg/ui/frontend/src/types.ts:105` | `InternalProvider` includes `typesafe` + `openrouter` |
| Gate 1 — `models.validateDecisionRules` D4 | `pkg/models/config.go:293-300` | quoted below |
| Gate 2 — `pkg/ui/server.go` POST (create) | `pkg/ui/server.go:1165-1178` | inline mirror of D4 |
| Gate 3 — `pkg/ui/server.go` PUT (update) | `pkg/ui/server.go:1631-1643` | inline mirror of D4 |
| Registry init | `pkg/decision/registry.go:49-53` | adapters |

Gate 1 quoted (`pkg/models/config.go:293-300`):

```go
if internalProvider != "" {
    switch internalProvider {
    case "openai", "typesafe", "openrouter":
        // ok
    default:
        return fmt.Errorf("model %s: kind %q requires the primary credential's provider to be %q, %q or %q (got %q)", m.ID, KindDecision, "openai", "typesafe", "openrouter", internalProvider)
    }
}
```

Gate 2 quoted (`pkg/ui/server.go:1171`):
`if primaryProvider != "" && primaryProvider != "openai" && primaryProvider != "typesafe" && primaryProvider != "openrouter" {`
Gate 3 quoted (`pkg/ui/server.go:1636`): identical condition.

Other D-rules around it (same three write paths): D1 internal `pkg/models/config.go:278-280`,
D2 internal_model `:281-283`, D3 credentials non-empty `:284-286`, D5 no fallback_chain `:302-304`,
D6 no secondary_upstream_model `:306-308`, D7 no peak hour `:310-312`, D8 no stream deadline `:314-316`;
D9 retired `:317-318`. Parity enforcement test: `pkg/ui/kind_rules_parity_test.go:5`, `:124-135`, `:312-317`.

### 4.3 Credential vocabulary is a SEPARATE map — no cross-wiring

- Credential providers: `var validProviders = map[string]bool{...}` (`pkg/models/credential.go:15-60`),
  checker `isValidProvider` (`:63-65`), read-only accessor `ValidProviderNames` (`:73-80`).
- Decision providers: a **switch on a string literal set**, not a map
  (`pkg/models/config.go:293-300`) plus the `providerRegistry` map in `pkg/decision`
  (`pkg/decision/registry.go:26`) plus two inline UI conditionals.
- They never reference each other: `validProviders` is consulted by credential CRUD validation
  (`pkg/models/credential.go:63`) and `validateDecisionRules` never calls `isValidProvider`.
  Types differ: `map[string]bool` vs `switch` vs `map[string]UpstreamProvider`.
- **Drift note (documentation, not behavior):** `pkg/models/credential.go:55-58` still claims the
  decision gate "only accepts {openai, typesafe}" — stale after openrouter landed; the code accepts
  three (`pkg/models/config.go:295`).

### 4.4 The user's live setup (openai + openrouter)

Both are in the allow-list today: `pkg/models/config.go:295`; `pkg/ui/server.go:1171`;
`pkg/ui/server.go:1636`; `pkg/decision/registry.go:50` (openai) and `:52` (openrouter).
`openrouter` is also a valid **credential** provider (`pkg/models/credential.go:54`), so the
credential can be saved.

`pkg/decision/openrouter_decision_test.go` (214 lines) exercises, all against httptest fakes — no
network:
- `TestDecisionRegistry_OpenRouter_Registered` (`:22-62`) — `lookup("openrouter")` returns
  `*systemOneProvider` with name `openrouter`, `defaultBase == "https://openrouter.ai/api"`; openai +
  typesafe still resolve; `minimax` still 502s.
- `TestOpenRouterDecisionProvider_BuildUpstream_DefaultURL` (`:69-102`) — URL table ending in
  `/v1/systemone` for empty / trailing-slash / `.../api/v1` bases, and the body carries the
  `internal_model` rewrite (`typesafe/jev-1.13`).
- `TestHandler_OpenRouter_BearerAuth_AtSystemOneURL` (`:108-135`) — upstream `Authorization: Bearer
  sk-cred-secret`, path `/v1/systemone`, body contains the `"type":"noul"` translation, exactly one
  upstream hit.
- `TestHandler_OpenRouter_SafetyIdentifier_400` (`:140-158`) — preflight rejects `safety_identifier`
  with 400 and 0 upstream hits.
- `TestHandler_OpenRouter_UnregisteredChatProvider_502` (`:162-171`) — `minimax` → 502
  "no decision adapter registered for provider".
- `TestSystemOneProvider_BuildUpstream_V1TailStrip` (`:178-213`) — `.../api/v1` is stripped to
  `/api/v1/systemone` (no `/v1/v1/systemone`).

What it does **not** exercise: real OpenRouter credentials, real latency, real error envelopes,
multi-member fallback of any kind.

---

## 5. Route wiring — `cmd/main.go`

- Handler construction: `decisionHandler := decision.NewHandler(configMgr, bus, tokenStore, usageCounter)`
  (`cmd/main.go:320`).
- Resolver installation: model `:321-323`, credential `:324-328`.
- Route: `mux.HandleFunc("/v1/decisions", decisionHandler.HandleDecision)` (`cmd/main.go:377`).
- Outer middleware: `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))`
  (`cmd/main.go:461`); gzipmw self-scopes to `/fe/api/*` (per blueprint; not re-verified here line by
  line) so `/v1/decisions` is unwrapped by it.
- POST-only is in-handler (`pkg/decision/handler.go:150-160`), not a mux method pattern
  (house style comment `cmd/main.go:370-376`).

### 5.1 Misroute guards, both directions

| Direction | Guard | Line |
|---|---|---|
| chat route receives `kind=decision` | 400 `decision_misroute` (OpenAI envelope) | `pkg/proxy/handler.go:576-580` |
| chat route receives `kind=image_gen` | 400 `image_gen_misroute` | `pkg/proxy/handler.go:558-562` |
| `/v1/messages` receives `kind=decision` | 400 `decision_misroute` (Anthropic envelope) | `pkg/proxy/handler_anthropic.go:272-279` |
| `/v1/decisions` receives chat / image_gen / **group** | 400 `decision_misroute` | `pkg/decision/handler.go:265-277` |
| `/v1/models` listing | decision + image-gen excluded | `pkg/proxy/handler.go:349-352` |
| groups on chat routes | deliberately pass through (`IsGroup` true, neither `IsImageGen` nor `IsDecision`) | `pkg/models/config.go:148-156` |

### 5.2 Construction order vs. available dependencies

Order in `main`: DB/store/models manager (early, `cmd/main.go:94` region) → `credLB := modelsMgr.Engine()`
(`:137`) → `stickyEngine := stickyorder.NewEngine(...)` (`:144`) → `modelsConfig` wrap/assign
(`:182-188`) → token store wrap (`:261`) → `proxyHandler := proxy.NewHandler(... credLB, stickyEngine)`
(`:282`) → `imggenHandler` + resolvers (`:292-302`) → **`decisionHandler` + resolvers (`:320-328`)** →
mux + route registration (`:331`, `:359-377`).

So at the decision handler's construction point, the following locals already exist and could be handed
over with **no new plumbing**: `modelsMgr` (⇒ `Engine()`, `GroupEngine()`, `SetSkipCache` seam),
`modelsConfig` (cache decorator, implements `models.GroupRewriter` — `pkg/modelscache/models.go:1043-1063`),
`credLB`, `stickyEngine`, `tokenStore`, `bus`, `usageCounter`.

What does **not** exist at that point: any decision-specific group resolver; the decision `Handler` has
no field/setter for one (`pkg/decision/handler.go:104-111`, `:740-752`).

---

## 6. Error semantics

### 6.1 Status/body matrix for the decision lane

| Condition | Status | Body shape | Line |
|---|---|---|---|
| non-POST | 405 | plain text `Method not allowed` | `pkg/decision/handler.go:157-160` |
| body > 16 MB | 413 | OpenAI envelope (`ErrorTypeServerError`, no code) | `:179-181`, writer `:699-704` |
| body read error | 500 | OpenAI envelope | `:189-191` |
| no/invalid token | 401 | OpenAI envelope, `type=authentication_error` | `:716-721` |
| invalid JSON | 400 | OpenAI envelope | `:218` |
| missing `model` | 400 | OpenAI envelope | `:227` |
| validator failure | 400 | OpenAI envelope, message = validator text | `:244`; texts `pkg/decision/validators.go:73-277` |
| unknown model | 404 | OpenAI envelope `unknown model "x"` | `:261-262` |
| non-decision kind (**incl. group**) | 400 | OpenAI envelope + `code=decision_misroute` | `:273-275` |
| token allow-list miss | 403 | custom `{"error":{"type":"access_denied","message":...,"model":...}}` | `:723-733` |
| no credential | 502 | OpenAI envelope | `:307-308` |
| provider not registered | 502 | OpenAI envelope `no decision adapter registered for provider` | `:325-326` |
| System One pre-flight | 400 | OpenAI envelope | `:353` |
| BuildUpstream error | 502 | OpenAI envelope + `code=decision_upstream_build_failed` | `:371-373` |
| transport error | 502 | OpenAI envelope | `:438-452` |
| deadline exceeded | 504 | OpenAI envelope | `:440-443` |
| upstream body read fail | 502 | OpenAI envelope `upstream read failed` | `:470` |
| upstream body > 64 MB | 502 | OpenAI envelope | `:483-484` |
| InspectResponse error | 502 | OpenAI envelope `inspect error: ...` | `:502` |
| upstream error envelope | **502** (adapter-promoted) | **upstream body verbatim** (no proxy envelope) | `:529-548`; openai `pkg/decision/openaidecision.go:169-174`; System One `pkg/decision/typesafe.go:426-429` + `:410-425` doc |

There are **no error type structs** in `pkg/decision`. The only classifier is
`Outcome{RelayStatus int; Reason string}` (`pkg/decision/converter.go:28-31`); envelopes come from
`models.NewOpenAIError` (`pkg/decision/handler.go:699-714`).

Adapter contract detail worth quoting: "ALL error envelopes promote — regardless of upstream status
(401 auth rejections included)" (`pkg/decision/openaidecision.go:132-139`) → RelayStatus 502.

### 6.2 What "all members exhausted" would have to return

Candidates that already exist:
- **502** — the lane's universal failure status; the promoted-error relay writes status + the *last*
  upstream body verbatim (`pkg/decision/handler.go:529-547`). A synthesized exhaustion error has no
  upstream body, so it would need a new envelope (existing helper:
  `openAIErrorWithCode(w, 502, msg, "decision_all_members_failed")` pattern, `:709-714`).
- **503** — used in the codebase only for config-store unavailability on the chat lane
  (`pkg/proxy/handler.go:483-489`, `code=config_store_unavailable`), so it is available but carries a
  different meaning today.
- **Anthropic-lane precedent for exhaustion**: 502 `api_error` "All models failed after retries"
  (`pkg/proxy/handler_anthropic.go:698-714`, quoted message at `:710`).

No existing decision-lane type fits "exhausted" without a new code string; the closest frozen shape is
the `{error:{type,code,message}}` envelope produced by `openAIErrorWithCode`.

### 6.3 Metering on this lane

- `usage.Counter.Increment(ctx, tokenID, hourBucket, 1, prompt, completion, total)` and
  `IncrementModelUsage(ctx, modelID, hourBucket, ...)` — success only (`pkg/decision/handler.go:635-640`).
- Detached 5 s background ctx so client disconnect cannot cancel billing (`:624`, `meterTimeout` `:90`).
- Zero-token usage is skipped with a log (`:617-622`).
- Any failure path writes **no** usage row (comment `:606-608`).
- Model id metered is the **client-facing** `modelID` (`:638`), not the member's id — relevant if a
  group ever meters per member.

---

## 7. Tests

### 7.1 Inventory

| File | What it pins | Harness |
|---|---|---|
| `handler_test.go` (2288 lines) | import guards (`:40`, `:65`, `:90` — transitive closure walks the package dir via `helpers_test.go:19-39`); method/body-cap/auth (`:610-666`); resolver matrix (`:692` chat→400, `:716` no-cred 502, `:730` unregistered provider 502, `:681` unknown 404); validators (`:760-871`); OpenAI round-trip + error-envelope preservation + byte pass-through (`:873-1049`); single-Content-Type and Accept-Encoding discipline (`:994`, `:1049`, `:1130`); typesafe translation/synthesis (`:1170-1533`); BuildUpstream 502 (`:1535`); timeout ctx (`:1569`); meter success/failure/detached (`:1593-1688`); event on success (`:1690`); proxy auth header not forwarded (`:1737`); adapter unit tables (`:1761-2131`); validator units (`:2137-2287`) | `fakeUpstream` httptest server (`:121-196`), in-memory token store (`:270-352`), `stubConfig` (`:354-358`), `newEnvForProvider` (`:374-438`) wiring resolvers to the fake URL, real SQLite usage DB (`:457-517`), `sendRequest` (`:519-540`). No network. |
| `handler_coverage_test.go` | coverage fillers: timeout fallbacks (`:34`), key precedence (`:54`), meter edges (`:90-140`), nil resolvers (`:142-171`), header helpers (`:173`), read/parse/request-build failures (`:195-285`), adapter guards (`:292-330`), validator edges (`:332-437`) | direct unit calls + `httptest.NewRequest`; `failingBody` (`:189-191`) |
| `openrouter_decision_test.go` | registry + URL table + bearer at `/v1/systemone` + preflight + unregistered-provider 502 + `/v1` tail strip (lines in §4.4) | `newEnvForProvider` / pure provider units; no network |
| `proxy_handler_decision_models_test.go` | resolve-level misroute matrix: unknown→404 (`:137`), chat→400 (`:156`), image_gen→400 (`:188`), decision model + unregistered provider→502 (`:224`), method guard (`:280`), nil resolvers→404 (`:295`) | `makeDecisionHandler` (`:44-...`) with stub resolvers, `stubConfigMgr` (`:31-33`), `httptest.NewRecorder`; **no fake upstream** |
| `r3r4_canonical_shape_test.go` | System One request-shape lock: canonical `criteria` shapes accepted, nested `choice`/`score` objects rejected (422) — through the proxy path for typesafe + openrouter (`:174-311`), both providers (`:312`), rejection path (`:354`), unit shape validator (`:398`) | `newEnvForMockUpstream` (`:115-170`): SQLite usage DB + shape-asserting `httptest` mock |
| `live_e2e_test.go` | live OpenAI Decisions + TypeSafe round-trips | **build tag `live_decision` (`:1`)** — excluded from default build/test; skips without `OPENAI_API_KEY`/`TYPESAFE_API_KEY` (`:15-27`); ≤3 calls/provider budget ledger (`:29-32`); real network |
| `typesafe_coverage_test.go` | pure translation helpers (`:26-263`), `InspectResponse` error branches (`:270`), answer synthesis fixtures (`:297-672`) | pure unit, no I/O |
| `types_coverage_test.go` | `Answer` marshal/unmarshal round-trips + input accessors (`:20-147`) | pure unit |
| `test_exec.go` + `test_exec_test.go` | `ExecuteTest` probe contracts (preflight 400, upstream status classified, typed success, unsupported provider, transport error) | fake `httptest` upstream; no auth/meter/events |
| `helpers_test.go` | thin wrappers for the transitive-import test (`:14-40`) | n/a |

### 7.2 `test_exec.go` — the execution harness

`ExecuteTest(cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte, timeout time.Duration)
*TestResult` (`pkg/decision/test_exec.go:118`) is **not** a handler: it does not authenticate, meter,
publish events, or write HTTP (`:32-35`). It runs exactly steps 9→15 of the handler: adapter lookup
(`:124-129`), System One pre-flight (`:131-...`), `BuildUpstream`, one upstream call under the
caller-supplied timeout, `InspectResponse` (`:97-106` doc). It returns a classified
`TestResult{Status, RelayStatus, Reason, UpstreamStatus, Response, Body, Error}` (`:70-92`).

This is the natural seam for group-failover tests: it takes the credential **per call** (so a loop can
feed different member credentials), has no commit point, and already preserves the adapter's
classification verbatim (`:62-66`). Its caller today is the FE test button
(`pkg/ui/handlers_decision.go:233`), which resolves the model by exact row id and rejects non-decision
kinds before calling (`:161-176`).

### 7.3 Tests that would break / need updating if resolution started accepting group ids

Static reasoning (nothing executed):

1. `pkg/decision/handler_test.go:692` `TestHandler_ResolveModel_NotDecisionKind_400` — pins
   chat-kind → 400 `decision_misroute`. Survives only if the group branch is inserted **before** the
   `IsDecision()` check; a naive "accept anything not chat/image-gen" change would break the sibling
   `proxy_handler_decision_models_test.go:188` (image_gen → 400) semantics.
2. `pkg/decision/proxy_handler_decision_models_test.go:156` and `:188` — chat and image-gen → 400.
   If group handling is added by widening that check rather than adding a branch, these go red.
3. `pkg/decision/proxy_handler_decision_models_test.go:224` — expects 502 from provider lookup for a
   decision model whose credential is `anthropic`. Unaffected by group acceptance, but any change to
   step 9's error path will touch it.
4. Group membership rules: `pkg/models/group.go:180-184` (G3 chat-only) is pinned by
   `pkg/ui/kind_rules_parity_test.go` and the store group tests; relaxing G3 to allow decision members
   will red-test the parity matrix and `pkg/store/database/models_manager_kind_test.go:535`-region
   tests.
5. Runtime predicate: `isServableGroupMemberBase` (`pkg/store/database/store_group_rewrite.go:291-301`)
   is chat-only and is asserted across `store_group_test.go` (`:591-630`, `:926`, `:944-950`) and
   `group_fallback_*` suites — a kind-aware relaxation changes those expectations.
6. `/v1/models` listing: `pkg/proxy/handle_models_imggen_test.go:129-164` pins decision models absent;
   a decision group would need an explicit exclusion and that test's sibling group-listing assertions
   would need a new case.
7. Chat-lane route tests that assume groups are chat-servable
   (`pkg/proxy/handle_chat_misroute_test.go`, `pkg/proxy/handler_anthropic_misroute_test.go`,
   `pkg/proxy/handler_groups_equivalence_test.go`) are only affected if a group gains a lane/kind
   discriminator; today a group is lane-agnostic (`pkg/models/config.go:148-156`).

### 7.4 Network-dependent tests (gate accordingly)

- `pkg/decision/live_e2e_test.go` — build tag `live_decision` (`:1`), skips without keys (`:15-27`),
  real upstreams, budget ledger (`:29-32`). Keep it tag-gated; it must not enter the failover pack.
- Everything else in `pkg/decision` is `httptest`-based (fake upstream / mock recorder) — safe for
  default `go test`.

---

## 8. Adaptation cost for exhaustive pre-commit fallback

Four capabilities, each mapped to the text-lane implementation it would mirror. All four land inside
`pkg/decision` + a small amount of `pkg/store/database`/`pkg/models` surface; none of them exist today.

### (a) Pre-commit multi-member attempts

What must be added:

1. **A group branch in the handler, placed between step 6 (model resolution) and step 8 (credential
   resolution)** — because the `IsDecision()` gate at `pkg/decision/handler.go:265` currently rejects
   the group row. Output of the branch must be an ordered member list, not a single id.
2. **A group-resolution seam on the decision package.** Mirror: `models.GroupRewriter`
   (`pkg/models/group_rewrite.go:82-121`), implemented by `*database.ModelsManager`
   (`pkg/store/database/store_group_rewrite.go:49-86` for `RewriteGroupEntries`, `:406-431` for
   `ExcludeGroupMember`) and forwarded by the cache decorator
   (`pkg/modelscache/models.go:1043-1061`). The decision `Handler` needs a new optional field +
   setter next to the existing resolver seam (`pkg/decision/handler.go:104-111`, `:737-752`), wired in
   `cmd/main.go:320-328` (where `modelsConfig` is already in scope, `:186-188`).
3. **A member attempt loop wrapping steps 8-15** with the write deferred to the end. The text-lane
   equivalent is the Anthropic sequential loop's pre-commit re-entry: gate on
   `!arc.payloadCommitted && arc.groupAdvances < models.MaxGroupMembers`
   (`pkg/proxy/handler_anthropic.go:664-672`), rebuild via `applyGroupRewrite`
   (`pkg/proxy/handler_groups.go:68`), and only write once a winner exists. In the decision lane the
   equivalent commit flag is simply "has `w.WriteHeader` fired" — i.e. the loop must own
   `pkg/decision/handler.go:507-548` and only execute it for the winning attempt.
4. **Member row → credential resolution**: the loop must resolve each member's credential by **row
   identity**, not claims-first, or a self-shadow group loops. Mirror:
   `pkg/store/database/store.go:2552-2567` (`ResolveInternalConfigWithAffinity` deliberately uses
   `GetModelNative`, claims bypassed) and `pkg/proxy/handler_groups.go:544-550` (`directModelGetter`
   seam).
5. **Data-model unlock**: G3 must stop requiring chat-kind members
   (`pkg/models/group.go:180-184`), and the runtime predicate must stop hard-rejecting non-chat
   (`pkg/store/database/store_group_rewrite.go:291-301`). Note `RewriteGroupEntries` also appends the
   member's own `FallbackChain` (`store_group_rewrite.go:72-75`) — but D5 forbids a fallback chain on
   decision models (`pkg/models/config.go:302-304`), so that part is inert for decision members.
6. **A lane discriminator question**: a group row carries no kind beyond `KindGroup`
   (`pkg/models/config.go:154-156`) and G6 forbids a fallback chain and shadow self-claim
   (`pkg/models/group.go:60-93`). Accepting groups on `/v1/decisions` without a discriminator means a
   chat group could be posted there and vice-versa (chat routes accept groups by design,
   `pkg/models/config.go:148-156`). Cheap options visible in the code: validate member kinds at
   resolution time (all members must be `kind=decision`), which the sweep can do with
   `GetModelNative` (`store_group_rewrite.go:163`, `:187`, `:211`).

### (b) Demote-not-filter ordering

What must be added:

1. **Skip-cache access on the decision lane.** Mirror: `*skipcache.Cache` attached to the proxy config
   (`pkg/proxy/handler.go:90`, `:166`) and to the manager (`pkg/store/database/store.go:726`,
   `SetSkipCache` `:733`). The decision handler currently has neither; its config seam exposes only
   `GetDecisionTimeout` (`pkg/decision/handler.go:116-118`).
2. **Failure classification to justify a mark.** A decision failure must be classified limit-class vs
   terminal before it can demote. Mirror: `pkg/providers/interface.go` `IsRateLimitError`/`IsQuotaError`
   and `pkg/proxy/cred_failover_class.go:93` `classifyCredFailure`. Reality check: the decision adapters
   **collapse every upstream error envelope to `RelayStatus 502`**
   (`pkg/decision/openaidecision.go:169-179`, `pkg/decision/typesafe.go:426-429`), so the classification
   input (`resp.StatusCode` + body) is available at `pkg/decision/handler.go:492` but is discarded by
   the adapter contract. A plan that wants quota-vs-rate-limit demotion must extend `Outcome`
   (`pkg/decision/converter.go:28-31`), not just call skipcache.
3. **Demoted-does-not-mean-excluded ordering.** Mirror: `memberDemotionExpiry`
   (`pkg/store/database/store_group_rewrite.go:273-282`), the four-phase sweep
   (`:130-245`), and the soonest-expiring-first sort (`:229-242`); skip-cache primitives
   `Mark`/`Marked`/`SoonestExpiry` (`pkg/skipcache/skipcache.go:87`, `:102`, `:170`), TTL knob
   `UpstreamSkipCacheTTL` (`pkg/config/config.go:120`, default `:255`, clamp `:357-362`).
4. **The "one honest attempt when everything is benched" rule** already exists in the sweep
   (`store_group_rewrite.go:117-124`, `:229-243`) — reusing the sweep gets it for free; re-implementing
   ordering inside `pkg/decision` would have to re-derive it.

### (c) Win-clear on success

What must be added:

1. **Clear the winner's own marks + member cooldown after a successful relay.** Mirror:
   `clearWinnerUpstreamSkip` (`pkg/proxy/handler_helpers.go:867-886`) — skipcache `Clear` for
   `(credential, wireModel)` and `(provider, wireModel)` (`:871-878`), then the **optional interface
   assertion** for `ClearGroupMemberCooldown(groupID, winnerModelID)` (`:879-885`).
2. **The decorator forward** is mandatory in production because `conf.ModelsConfig` is the cache
   decorator: `pkg/modelscache/models.go:1063-1085` (the comment there documents the exact
   silent-failure trap if the forward is missing).
3. **Manager + engine seam**: `pkg/store/database/store_group_rewrite.go:439-444` →
   `pkg/grouplb/engine.go:1274-1287`.
4. **Identity discipline**: clear on the **winner's** identity (member id / credential / wire model),
   never on the originally-picked head (`pkg/proxy/handler.go:905-920`, comment `:906-915`). The
   decision lane's winner identity is `member.ID` + `cred.APIKey`'s `CredentialID` +
   `cred.InternalModel` — all three are already in scope at `pkg/decision/handler.go:299` and `:361`.
5. **Placement**: after the relay write and before/with metering, i.e. between
   `pkg/decision/handler.go:548` and `:554`.

### (d) Request-scoped tried-set

What must be added:

1. **A per-request `tried` set + advance cap** in the decision handler. Mirror:
   `arc.groupTriedMembers map[string]bool` (`pkg/proxy/handler_anthropic.go:1804`), the offer walk that
   refuses an already-tried replacement (`pkg/proxy/handler_groups.go:457-479`), the post-rebuild guard
   that lands the tried-mark on the member actually served (`:485-493`), and the
   `arc.groupAdvances++` cap against `models.MaxGroupMembers` (`:494`; constant
   `pkg/models/group.go:25`; caller gate `pkg/proxy/handler_anthropic.go:669-670`).
2. **The exclusion vehicle**: `ExcludeGroupMember(groupID, conversationKey, memberID, cooldown)`
   (`pkg/models/group_rewrite.go:121`, impl `pkg/store/database/store_group_rewrite.go:406-431`,
   forward `pkg/modelscache/models.go:1056-1061`). Cooldown is caller-owned — the text lane passes the
   config-owned skip TTL (`pkg/proxy/handler_groups.go:466`,
   `pkg/proxy/handler.go:1084`) from `UpstreamSkipCacheTTL` (`pkg/config/config.go:120`).
3. **The credential-layer twin** (if the plan wants per-credential exhaustion within a member, not just
   per-member): `SeedRaceState`/`HarvestRaceState` (`pkg/proxy/race_coordinator.go:2140`, `:2158`) and
   `rc.credTriedAcrossRace` seeding (`pkg/proxy/handler.go:887-892`, harvest `:951-960`). On the
   decision lane there is currently one credential per attempt, so a member-level tried set is the
   minimal form; a credential-level set is only needed if a single decision model has 2+ credential
   refs (possible: `pkg/store/database/store.go:2614-2619`).
4. **Empty conversation key is the correct input**: the lane has no key (§3). `ExcludeGroupMember` and
   the sweep accept `conversationKey == ""` (fresh weighted pick, no binding —
   `pkg/models/group_rewrite.go:88-90`), so no affinity has to be invented.

### (e) Explicit non-goals this research rules out

- Reusing `pkg/proxy`'s handlers or its `ConfigSnapshot` — the import guards forbid it
  (`pkg/decision/handler_test.go:40-119`).
- Reusing sticky last-success ordering — no key exists (§3).
- Reusing `Outcome.RelayStatus` as a failure classifier — it is 502 for every error envelope
  (`pkg/decision/openaidecision.go:169-179`), so quota vs rate-limit demotion needs a new field.

---

## 9. What I could not verify in this pass

- **Nothing was executed** (read-only commission): no build, no `go test`, no smoke. Test-breakage in
  §7.3 is static reasoning from the assertions I read, not observed failures.
- `handler_test.go` is 2288 lines; I inventoried it by function signatures + the harness section plus
  the specific tests quoted. Individual assertion bodies beyond those points were not read.
- `pkg/proxy/handler.go` lines 960-1130 (re-entry loop body) were not read in full; the claims about
  `ExcludeGroupMember` usage there rest on `:1026-1118` grep hits plus `handler_groups.go:457-493`.
- gzipmw self-scoping to `/fe/api/*` is asserted per the project blueprint, not re-verified line by
  line in this worktree.
- Live provider behavior (OpenAI Decisions / OpenRouter System One error envelopes) is documented by
  the adapter comments and the `live_decision`-tagged tests; I did not run them.
- The user's actual 2 decision models (ids, internal_model values) live in the running DB — not read.
