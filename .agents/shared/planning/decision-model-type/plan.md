# Plan: `decision` Model Kind

> **Companion decision log:** [`decisions.md`](./decisions.md) — recommended defaults for every open question in §13; user can approve or override item-by-item.
>
> **Inputs consumed:** [`research-codebase.md`](./research-codebase.md) (181 lines, code anchors), [`research-web.md`](./research-web.md) (185 lines, vendor APIs). No source files written. No commits. No builds.

---

## 1. Executive Summary

### Goal

Add a third model **kind** `"decision"` to `llm-supervisor-proxy`, served at `POST /v1/decisions` and managed from a new **Decision Models** tab in the dashboard. The proxy speaks the **OpenAI Decisions wire** to clients (mirroring the way it speaks OpenAI chat wire today) and translates to **TypeSafe (Jev)**'s "System One" wire for TypeSafe-backed decisions. Both upstream providers are synchronous, input-token-billed, with no streaming. Multi-provider from the start: **OpenAI Decision API** (gpt-6-luna) and **TypeSafe (Jev)** (`/v1/systemone`).

### Goals (in)

- `kind: "decision"` models are first-class (UI CRUD + `/v1/decisions` route + metering + auth + misroute guards).
- Provider abstraction ships with **OpenAI Decision API** and **TypeSafe** adapters; credential validation accepts both provider IDs.
- Two-way translation: client ↔ OpenAI Decision wire ↔ per-provider upstream wire.
- Decision rows are excluded from `/v1/models` (chat picker) and from `/v1/chat/completions` (with an OpenAI-envelope 400 on misroute).
- Token usage is metered via existing `Increment` / `IncrementModelUsage` writers — **no schema migration**.
- Same import-purity discipline as `pkg/imggen` (`Makefile` grep + `TestPackage_ImportGuard_NoProxyImports`), plus a new **transitive-closure test** `pkg/imggen` lacks.
- Same FE build discipline as `pkg/ui/frontend` (any `index.html` change ships regenerated hashed bundles in the same commit; tsc baseline 26 errors / 11 files does not regress).

### Non-goals (out)

- **Streaming** (both vendors lack it today; no SSE infrastructure needed).
- **Idempotency / `Idempotency-Key`** support (neither vendor documents it).
- **Per-decision-kind usage column** in the hourly usage tables — decision input tokens blend into the existing `token_hourly_usage` / `model_hourly_usage` totals. The discriminator for "this hour's decision consumption" is at the model-id level (kind-tagged model IDs), not in the usage schema.
- **`/v1/systemone` native pass-through** — clients speak OpenAI Decision wire to the proxy. A future native pass-through could be added without breaking this surface.
- **`fallback_chain` / `peak_hour_enabled` / `secondary_upstream_model` / `release_stream_chunk_deadline` features for decision models** — the kind rules reject them, mirroring the imggen posture.
- **Push, merge, build.** Per project convention and the active wt-claim: this plan is **PLAN-ONLY**; the worker who executes it must respect the `LOCAL ONLY — never push without explicit user instruction; no merge; no commits until instructed` policy in shared meta-KV.

---

## 2. Grounding — How Existing Machinery Works

Citations are file:line anchors from `research-codebase.md` (🟢 = read+verified in this worktree). Vendor facts are from `research-web.md`.

### 2.1 Model-kind system (existing)

- **`pkg/models/config.go:90-101`** declares `KindChat = "chat"` and `KindImageGen = "image-gen"`. The discriminator is **nullable**, an `omitempty` JSON field; chat models serialize kind-free (research-codebase §1).
- **`ValidateKindRules(m, internalProvider)`** at `pkg/models/config.go:141-152` is the single source of truth — a switch over `Kind`. Unknown values are **rejected** by the `default` arm (`:151`) with a message naming the two accepted values.
- **`validateImageGenRules`** at `pkg/models/config.go:157-185` enforces the image-gen rule set: `Internal==true`, `InternalModel != ""`, `len(Credentials) > 0`, primary provider `== "minimax"` when `internalProvider != ""`, `FallbackChain` empty, `SecondaryUpstreamModel == ""`, `PeakHourEnabled == false`, `ReleaseStreamChunkDeadline == 0`.
- **Three enforcement sites** must add the new kind in parallel: (a) `ModelsConfig.Validate` JSON-file path (`pkg/models/config.go:829`); (b) DB write path via `validateModelAgainstCredentials` (`pkg/store/database/store.go:1696-1708`, called from `AddModel` `:1340` / `UpdateModel` `:1427`); (c) an **intentionally duplicated inline mirror** in the FE API — POST `pkg/ui/server.go:869-947` and PUT `pkg/ui/server.go:1080-1143`. The inline mirror is documented as T1.2.3 design — kept inline to match the peak-hour/secondary-upstream 400 patterns. A new kind must be added at **all three** or surfaces drift.

### 2.2 Persistence — **NO migration for `kind="decision"`** (Path A)

- Migration `030_add_model_kind.up.sql` added a **nullable `kind TEXT`, no DEFAULT, no CHECK constraint** (research-codebase §1, surprise-free 🟢). The text column accepts any value today.
- `models.kind` has only three validation gates: the `ValidateKindRules` switch, the 2 inline mirrors in `pkg/ui/server.go` (POST `:869-947` + PUT `:1080-1143`), and the `?kind=` switch in `GET /fe/api/models` (`pkg/ui/server.go:758-770`).
- The `kind` column is read via `coalesce(kind, '')` in 8 SELECT builders (`pkg/store/database/querybuilder.go:189-289`) and scanned at 6 store sites (`:767-840`, `:843-922`, `:924-1004`, `:1138-1172`, `:1216-1263`, `:1265-1309`); all are 20-column scans and **count-pinned by tests** (`querybuilder_test.go:207-281`). **Adding the value `"decision"` requires zero changes to the migration registry, querybuilder, or store scan sites** — only the three enforcement-site gates plus the discriminator surfaces.
- `pkg/store/database/sqlc/schema.sql` and `db/queries.sql.go` are stale dead code (research-codebase §1) and must NOT be mirrored.

### 2.3 Routing, middleware, auth

- **Composition root** `cmd/main.go`: `imggenHandler := imggen.NewHandler(configMgr, bus, tokenStore, usageCounter)` at **`:241`**; resolver wiring `:242-251` (`SetModelResolver(NewModelResolverFromConfig(...))` + `SetCredentialResolver(NewCredentialResolverFromConfig(...))` with stable affinity `(modelID, modelID)` per BE-A3/Amendment 18). Route registration `:282-291` lists `/v1/chat/completions` `:282`, `/v1/messages` `:283`, `/v1/models` `:284`, `/v1/image_generation` `:291`. **POST-only is enforced in-handler** — house style, no Go 1.22 mux method patterns anywhere (comment `:286-290`).
- **Middleware** `cmd/main.go:377`: `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))`. **`gzipmw` self-scopes to `/fe/api/*`** (`pkg/middleware/gzipmw/response.go:307` prefix check; SSE exclusion for `/fe/api/events` `:58-60`; `MinResponseSize = 1024` `:54`) — a **no-op for all `/v1/*`**. Nothing to change for `/v1/decisions`.
- **Auth is in-handler**, not middleware: chat `pkg/proxy/handler.go:465-485` (`authenticate` + `requiresInternalAuth` `:324-345`; header extraction Bearer/X-API-Key/x-api-key `:268-285`; allow-list `:546` via `authToken.IsModelAllowed`, empty `AllowedModels` = all allowed per `pkg/auth/token.go:129-144`). ImgGen has its own in-handler auth at `pkg/imggen/handler.go:203-211` → `authenticate` `:523-540` / `extractAPIKey` `:544-555`; allow-list `:267-275`. A decision handler mirrors this exactly.
- **Misroute guards**:
  - Chat-side: `pkg/proxy/handler.go:455-462` (verified verbatim in this worktree) — `if rc.resolvedModel != nil && rc.resolvedModel.IsImageGen() { … 400 OpenAI envelope, code "image_gen_misroute" }`. The guard was *intentionally the only `pkg/proxy` change* of the ImgGen commission (comment `:441-454`).
  - Reverse-direction gap (verified, surprise #2): `NewModelResolverFromConfig` (`pkg/imggen/handler.go:715-725`) only checks `m == nil` — its doc comment claims a kind guard that **does not exist in code**. The real gate is the provider-registry lookup (`minimax.go:21-22, 199-215`). **Decision must add an explicit `IsDecision()` resolve check** (closing the reverse-direction gap as a bonus).
- **`/v1/models` listing** `pkg/proxy/handler.go:244-274` excludes `IsImageGen()` at **`:258`** (verified). No `?kind=` on `/v1/models` — only `/fe/api/models` has one. Decision models should be excluded the same way (`|| m.IsDecision()`).
- **Env-flag gating**: only MCP is gated (`MCP_ENABLED`, `cmd/main.go:263-276`); imggen is **unconditionally wired**. Mirroring that posture (always-on `/v1/decisions`) needs no env flag.

### 2.4 Provider & credential config (existing)

- **`CredentialConfig`** `pkg/models/credential.go:33-38`: `{ID, Provider, APIKey, BaseURL omitempty}`. Persisted in `credentials` (migration `005_add_credentials.up.sql:3-11`); **API keys encrypted at rest** with AES-256-GCM (encrypt-on-write `store.go:1933-1937`/`:1964`, decrypt-on-read `:1766-1771`/`:1805-1809`); JSON-file twin `credential.go:147-216`; `${ENV_VAR}` / `${ENV_VAR:-default}` expansion (`expandEnvVars` `:50-94`).
- **Provider factory** `pkg/providers/factory.go`: `ProviderType` consts `:10-20` (openai, anthropic, gemini, zhipu, azure, zai, minimax, grok, deepseek); `NewProvider` switch `:23-70` injects default base URLs; `IsProviderSupported` `:72-80`; `providerMetadataMap` `:99-154`; `GetProviderTypes` `:173-186` (feeds FE dropdown via `GET /fe/api/providers` at `pkg/ui/server.go:183`→`:216-225`).
- **The provider list is duplicated in 3+ places**: `pkg/models/credential.go:14-24` `validProviders` map (gates `CredentialConfig.Validate` `:116-118`, hence `AddCredential` / `UpdateCredential`); FE `pkg/ui/frontend/src/types.ts:109` `InternalProvider` union; hardcoded per-form constants like `ImgGenModelForm.tsx:10-14`. Adding two new provider IDs touches **factory ×5 sites + validProviders + FE union** (consolidated table below).
- **How imggen binds credentials** (the template for decision): `pkg/imggen/handler.go:285-296` `resolveCredential(modelID)` → `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)` (`store.go:2159-2257`): `GetModel` → `Internal` check → `PrimaryCredentialID()` (credentials[0] tie-break) → `GetCredential` (**decrypts transparently** `:1752-1771`) → `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}`. Handler uses `cred.Provider` for dispatch, `cred.APIKey` for Bearer, `cred.BaseURL` for upstream URL, `cred.InternalModel` for body rewrite.
- **`validateModelAgainstCredentials`** `store.go:1586-1713` enforces per-credential rules (weight>0, dup IDs, 16-credential cap, **provider-match invariant** `:1620-1632`); tail `:1696-1708` resolves `primaryProvider = lower(GetCredential(Credentials[0]).Provider)` and calls `models.ValidateKindRules(&model, primaryProvider)`.
- **Open question for this plan** (resolved per amendment 2): whether `pkg/providers` needs full `Provider` impls for the new decision adapter or only credential-validation entries — the credential path requires the `validProviders` entries either way. **Recommended default**: only credential-validation entries (one new entry: `typesafe`; `openai` is reused). Decision handler builds its own upstream HTTP client, mirroring `pkg/imggen` (decision-local registry keyed on `cred.Provider`).

### 2.5 `pkg/imggen` anatomy — the copy-and-rename template (research-codebase §4)

`pkg/imggen` is **~1182 lines** across `doc.go` (29), `handler.go` (~880), `client.go` (~40), `converter.go` (~65), `minimax.go` (~220). The architectural rules in `doc.go:1-29` (import set `{pkg/models, pkg/auth, pkg/usage, pkg/events}` only; exactly one upstream attempt; single-classifier seam; bounded RAM) port verbatim to `pkg/decision`. Reusable skeleton:

- `handler.go` entry `HandleImageGeneration` `:155` → POST-only guard; 16MB `MaxBytesReader` `:169` (413 via `*http.MaxBytesError` `:177-188`); auth `:203`; `resolveModel` `:247` (404 unknown); allow-list `:267-275` (403); `resolveCredential` `:286` (502); provider lookup `:302` (502 unregistered); `BuildUpstream` `:316` (URL + model rewrite); timeout ctx `:334` (`defaultImageGenTimeout = 120s` `:42`, clamped `[30s, 4min]` at `pkg/config/config.go:271-277`); `sharedHTTPClient.Do` `:371`; response cap `io.LimitReader(cap+1)` `:404` (64MB `maxResponseBodyBytes` `:56`); `InspectResponse` `:437`; relay write `:444-475`; meter `:486-491`; event publish `:512`.
- `client.go` `:26-39` (`sharedHTTPClient`: `Timeout: 0`, `ResponseHeaderTimeout: 0`, `MaxIdleConns: 100`) — survives long silent generations.
- `converter.go` `:30-65`: `Outcome{RelayStatus, Reason}` + `UpstreamProvider` interface (`BuildUpstream`, `InspectResponse`, `BillableImages`).
- **Detached-context metering** at `:581`: `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)` — deliberate detachment so client disconnect cannot cancel billing writes.
- **Error mapping is structural, not a code table**: `base_resp.status_code == 0` ⇒ success; `!= 0` ⇒ 502 with upstream body verbatim. For OpenAI Decision wire, the equivalent is the standard OpenAI error envelope `{"error":{"message","type","code"}}` — the proxy should detect this body and **propagate the upstream status code** with the body verbatim (research-codebase §4: "body preserved verbatim").
- **Import-purity guard**: `Makefile:54-58` grep rule (`grep -rnE 'github.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/imggen/` → FAIL) + `TestPackage_ImportGuard_NoProxyImports` `handler_test.go:689-713`. **No automated transitive-closure test exists** — closure was a manual `go list -deps` audit. Decision package adds the closure test (improvement).
- **Provider-specific surface**: the `minimax.go` equivalent is `BuildUpstream` URL, `InspectResponse` wire-shape (OpenAI-style error envelope), `BillableImages`→`BillableUsage` coercion. **Reusability verdict**: ~80% copy-and-rename.

### 2.6 Frontend (existing)

- **Tabs**: `SettingsPage.tsx` owns them. `TabType` union at `:40`: `'proxy' | 'models' | 'imggen_models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage' | 'mcp_servers'`. ImgGen button `:340-351` (`data-tab-id="imggen_models"`, 🖼️ span, `aria-hidden`); content ternaries `:465-475` (chat) / `:484-496` (imggen); toggle handlers `:207-214` / `:219-226`; component imports `:6-7`; props surface `:17-38`. `ConfigModal.tsx:29` is a separate LEGACY tab host without imggen — **decision should skip it** (mirrors ImgGen commission).
- **Components** (`src/components/config/`): `ModelsTab.tsx` (chat, 323 lines), `ImgGenModelsTab.tsx` (341), `ModelForm.tsx` (815, chat form, kind implicit), `ImgGenModelForm.tsx` (439, **hardcodes at `:10-14`**: `HARDCODED_KIND='image-gen'`, `HARDCODED_INTERNAL=true`, `HARDCODED_INTERNAL_PROVIDER='minimax'`, `HARDCODED_EXCLUDE_FROM_ULTIMATE=true`, `DEFAULT_BASE_URL='https://api.minimax.io/v1'`; stamped on submit `:206-217`). **No kind selector UI exists** — kinds are hardcoded per-form.
- **Hooks** (`src/hooks/useApi.ts`): `useModels` `:264-328` (no kind filter, cache key `'models'`, TTL 15s); `useImgGenModels` `:347-422` (`?kind=image-gen`, key `'imggen-models'`). **Symmetric dual-invalidation** (C-07): every mutation deletes BOTH keys — `useModels` `:295-296`/`:308-309`/`:316-317`; `useImgGenModels` `:389-390`/`:402-403`/`:410-411`. Adding a third kind requires **triple invalidation** at all six sites plus the new hook's own site.
- **apiCache** (`src/utils/apiCache.ts`): `cache`/`promises` maps `:33-34`, `DEFAULT_TTL=30_000` `:6`, `getOrFetch` `:79`, `delete(key)` `:131`, `deleteByPrefix` `:147`, singleton `defaultAPICache` `:238`.
- **C-16 cross-tab filter — exactly ONE site**: `ModelsTab.tsx:157` `const chatModels = models.filter((model) => model.kind !== 'image-gen');` (verified). Must become `&& model.kind !== 'decision'`. FE-wide sweep found no other two-kind assumptions except `types.ts:121` union (`kind?: 'chat' | 'image-gen'`) and `:153` (`ImgGenModel` alias) — add `'decision'` + `DecisionModel` alias.
- **Provider list is BE-served**: `useProviders()` `:709-740` → `GET /fe/api/providers` → `factory.go:173-186`. FE consumes automatically; no FE list edit needed except the `types.ts:109` union.
- **FE API surface**: `GET /fe/api/models` `:749-867` (`?kind=` switch `:758-770`); POST branch kind whitelist `:869-877`; `PUT /fe/api/models/{id}` `handleModelDetail` `:1006+` (mirror `:1080-1088`); `POST /fe/api/models/validate` `:1212`; `POST /fe/api/models/test` `:1295`; `GET /fe/api/providers` `:183`→`:216-225`.
- **Build/embed discipline**: `//go:embed static/*` at `pkg/ui/server.go:84-85`; `vite.config.ts:7-9` `outDir: '../static'`, `emptyOutDir: true`; `base: '/ui/'` `:5`. **Same-commit rule**: any commit touching `pkg/ui/static/index.html` must include regenerated hashed bundles in the SAME commit (rule recorded in project blueprint + `docs/2026-09-22-fe-api-payload-ram-incident.md:241`; no dedicated doc). **tsc baseline 26 errors / 11 files is documented ONLY in the project blueprint** — not in-repo; do not regress upward.
- **FE tests**: vitest include `src/**/__tests__/**/*.test.{ts,tsx}` (`vitest.config.ts:8-14`, happy-dom). Mirror targets: `SettingsPage.tabs.test.tsx`, `useImgGenModels.test.ts`, `ImgGenModelsTab.test.tsx`, `ImgGenModelForm.test.tsx`, `ModelsTab.chatFilter.test.tsx` (C-16 — must gain a decision-row case).

### 2.7 Usage metering (existing)

- **`pkg/usage/counter.go`** `Counter{db, dialect, qb, writeMu sync.Mutex}` (`writeMu` `:52` serializes the 4 writers; reads unguarded). Hour buckets = `time.Now().UTC().Format("2006-01-02T15")`. Tables `token_hourly_usage` (migration 019) and `model_hourly_usage` (migration 025); writers `Increment` `:68`, `IncrementModelUsage` `:194`, `IncrementTokenImages` `:123`, `IncrementModelImages` `:236`. **Exclusivity rule**: never co-call `Increment` with `IncrementTokenImages` — double-counts `request_count` (`:105-114`).
- **Call sites**: chat `pkg/proxy/handler.go:899`/`:915` async on `context.Background()`; imggen `handler.go:591`/`:594` inside synchronous `meter` `:575` with the detached 5s ctx `:581`. Both providers DO return token usage (research-web Q1/Q2: usage = `{input_tokens, output_tokens}` minimum). **No schema change needed for token metering**.
- **Caveat (surprise #6)**: usage tables have **no kind column** — decision rows are indistinguishable from chat rows in FE usage views (FE usage SQL in `pkg/ui/handlers_usage.go:197-258+`). The alternative is migration 031 `decision_count` (≈10-12 coupled sites); **recommended path is Path A (no migration) with the caveat documented**, not migration 031.

### 2.8 Config seed (existing)

- `config/models-template.json` (24 lines, 2 chat entries) is **not code-loaded** — `grep -rn "models-template" --include="*.go"` returns 0 hits (research-code §8). Documentation artifact only.
- **`pkg/config` is kind-agnostic**. The one sibling knob is `ImageGenTimeout` (`pkg/config/config.go:95-103`, default 120s `:229-233`, clamp `[30s, 4m]` `:271-277`, accessor on `ManagerInterface` `:137`, impl `:636-643`). A `DecisionTimeout` follows the same quartet. No `IMGGEN_*` env vars; the only env-flag pattern is `MCP_ENABLED`.

### 2.9 Vendor APIs (research-web.md)

- **OpenAI Decisions**: `POST https://api.openai.com/v1/decisions` (auth `Authorization: Bearer $OPENAI_API_KEY`). Public beta since 2026-10-06. `model` = `gpt-6-luna` only today. Request schema verified end-to-end (table in research-web §Q1): `model`, `input` (string | DecisionInputMessage[]; per-part `input_text` / `input_image`), `questions[1..128]` (Predicate | Choice | Score), `safety_identifier` (optional). Response: `{model, answers[], usage}` — **no `id`, `object`, `created` fields**. Pricing $0.10/1M input tokens; output/cache-free. **No streaming**; **no decision-specific billing units** — plain token metering. SDK: `github.com/openai/openai-go/v3` ≥3.73.0 — reference types only (proxy speaks raw HTTP).
- **Jev / TypeSafe**: `POST https://api.typesafe.ai/v1/systemone` (auth `Authorization: Bearer <key>` — Bearer format inferred). Models: `jev-latest`, `jev-1.13.0`, `jev-preview`. **NOT wire-compatible** with OpenAI Decisions — uses "System One" schema (`state`, `questions` map, `noul` predicate, `criteria` map/array, answers map keyed by id, `legend` on score). LiteLLM v1.104.2+ ships exactly this bidirectional translation — existence proof and reference. Pricing $0.042/1M input tokens; output tokens free. 64k combined context budget. **No streaming**. Constraints: text-only, no images/audio/video; English best / CJK weaker; no fine-tuning.

---

## 3. Backend Design

### 3.1 Kind system changes — 7 sites, NO migration

| Site | File:line | Change |
|---|---|---|
| 1 | `pkg/models/config.go:90-101` | add `KindDecision = "decision"` const, after `KindImageGen` |
| 2 | `pkg/models/config.go:~105-107` | add `IsDecision()` helper mirroring `IsImageGen` |
| 3 | `pkg/models/config.go:141-152` | add `case KindDecision:` arm; update default-arm message to mention three accepted values |
| 4 | `pkg/models/config.go:~157-185` (new sibling) | `validateDecisionRules(m, internalProvider)` — see rule set below |
| 5 | `pkg/proxy/handler.go:455-462` | **TWO parallel guard branches** (not a boolean disjunction) so error codes stay distinct: `if rc.resolvedModel.IsImageGen() { … 400 OpenAI envelope, code "image_gen_misroute" }` **plus** `if rc.resolvedModel.IsDecision() { … 400 OpenAI envelope, code "decision_misroute" }` — same site, two separate `if` branches. **Reverse-direction error code (decision resolver, §3.2 step 6)**: same `decision_misroute` code string (cross-route canonical; the code string identifies the misroute class, not the route). |
| 6 | `pkg/proxy/handler.go:258` | `IsDecision()` exclusion from `/v1/models` listing |
| 7 | `pkg/ui/server.go:758-770` | **🔴 (finding 1) extend the chat arm** so `?kind=chat` excludes decision models too: `case models.KindChat → func(m) bool { return !m.IsImageGen() && !m.IsDecision() }`. The current chat arm is `!m.IsImageGen()` only — adding KindDecision without this extension leaks decision models into `GET /fe/api/models?kind=chat` for any non-UI direct API consumer (FE C-16 covers the tab UI only, contradicting §2.3/§2.5/§3.4). Default arm log wording updated for 3 kinds. |
| **8** | **`pkg/proxy/handler_anthropic.go:~155-183`** **(NEW — amendment 1)** | **Post-resolve `IsDecision()` reject on the `/v1/messages` (Anthropic) route. The route has ZERO misroute checks today** (verified: `grep -nE 'IsImageGen\|IsDecision' pkg/proxy/handler_anthropic.go` → 0 hits). A decision model POSTed here would execute a real chat-wire upstream call (the money-burning scenario). Emit **Anthropic error envelope** via `models.NewAnthropicError` (the route's envelope family). **Error code string: `decision_misroute`** — same code string as the chat-side guard at `pkg/proxy/handler.go:455-462`, but routed through the Anthropic envelope (route-local; per finding 23). **Non-blocking note**: a pre-existing image-gen gap on the same route is a candidate for backport in a separate commission — out of v1 scope per Appendix "NO pkg/imggen changes". |

#### `validateDecisionRules` rule set

Justified analog of `validateImageGenRules`. Same posture: internal-only, deny aux features that don't apply, provider-gated.

| # | Rule | Reason |
|---|---|---|
| D1 | `m.Internal == true` | Decision calls are pure upstream passes, same as imggen |
| D2 | `m.InternalModel != ""` | Body-rewrite alias needs a target (`gpt-6-luna`, `jev-1.13.0`, etc.) |
| D3 | `len(m.Credentials) > 0` | Need a credential to authenticate upstream |
| D4 | `internalProvider ∈ {"openai","typesafe"}` (lowercase; recommended identity B). With override A: `{"openai-decision","typesafe"}`. | Provider allowlist; non-match ⇒ 400 from inline mirror AND DB write path |
| D5 | `m.FallbackChain` empty | Decisions are cheap+fast; racing is out of scope (mirrors imggen) |
| D6 | `m.SecondaryUpstreamModel == ""` | Same reasoning as D5 |
| D7 | `m.PeakHourEnabled == false` | No peak-hour logic for decisions |
| D8 | `m.ReleaseStreamChunkDeadline == 0` | No streaming; field is for stream-deadline knob |
| D9 | `m.ExcludeFromUltimateSwitching == true` | **Decision**: TRUE explicitly. The doc/code drift at `config.go:258-259` (claims image-gen forces this, no code does) is **not** inherited. Decision gets explicit write-time enforcement via the inline mirror + DB write path to prevent ultimate-model shadowing of decision models. |
| D10 | `m.UPSTREAM_MODEL` field not used (no analog for D-decision) | N/A |

**Provider id note (resolved per amendments 2-3)**: the original rationale that `openai-decision` was needed to avoid collision with `openai` chat was **false** (verified per architect amendment 2: chat dispatch is unreachable from decision routes; imggen/`minimax` proves the reuse pattern at scale). The plan's recommended identity is **B** — `openai` (reused) + `typesafe` (new). Override A keeps `openai-decision` + `typesafe` for operator identity clarity (mandatory FE display label "OpenAI Decisions"). Both port equally; override is one-line per site.

### 3.2 NEW `pkg/decision` — modeled on `pkg/imggen` (~80% copy-and-rename of the *handler shell*; **typed seam replaces body-map seam per amendment 4**)

Files to create (mirroring pkg/imggen structure):

| File | Lines (~) | Purpose |
|---|---|---|
| `pkg/decision/doc.go` | ~30 | Architectural comment block (port `pkg/imggen/doc.go:1-29` verbatim, swap "image generation" → "decision") |
| `pkg/decision/types.go` | ~120 | **NEW (typed seam).** Typed structs: `DecisionRequest`, `Question` (sealed by `Type QuestionType` + `Instructions string` + per-type fields), `DecisionInputMessage`, `InputPart` (text/image), `DecisionResponse`, `Answer` (per-type), `Usage{InputTokens, OutputTokens int64, CachedTokens, CacheWriteTokens, ReasoningTokens int64}` (typed struct, not a `BillableUsage` rename — different semantic from `BillableImages int`). |
| `pkg/decision/handler.go` | ~350 | Entry `HandleDecision`; auth (`authenticate`, `extractAPIKey`); **parse client wire into `DecisionRequest` ONCE**; run shared gotcha validators as typed predicates (400 + house envelope); resolveModel (404 unknown) with **explicit `IsDecision()` check** (closes imggen reverse-direction gap); allow-list (403); resolveCredential (502); provider lookup (502); `BuildUpstream` (per-provider, typed); timeout ctx; `sharedHTTPClient.Do`; response cap; `InspectResponse` (typed); relay write; **success-only** meter; event publish |
| `pkg/decision/validators.go` | ~80 | **NEW (typed gotcha predicates).** Pure functions on typed `*DecisionRequest`: `validateQuestionsCount`, `validateChoicesPerQuestion`, `validateScoresPerQuestion`, `validateBooleanStringCollision`. Each returns `error`. The handler runs these once; `typesafe.go` re-asserts a subset before emitting System One (defense-in-depth per amendment 4). |
| `pkg/decision/client.go` | ~40 | `sharedHTTPClient` (port `pkg/imggen/client.go:26-39`) |
| `pkg/decision/converter.go` | ~70 | `Outcome{RelayStatus, Reason}` + `UpstreamProvider` interface (`BuildUpstream`, `InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error)` — **unified 2-arg signature per finding 5**, both adapters satisfy it; `ExtractUsage` returns the typed `Usage` struct, replacing the original `BillableUsage` rename) |
| `pkg/decision/openaidecision.go` | ~80 | OpenAI Decision API adapter (`Provider` const `"openai"`, keyed locally). **`BuildUpstream` is a near-pure pass-through: re-emit the client wire bytes with only `model` rewritten** (`model` from the typed request → `cred.InternalModel`). No structural translation. **`InspectResponse` returns the typed `DecisionResponse` (or `Outcome{RelayStatus: …, Reason: …}` on error).** `ExtractUsage` reads `usage.input_tokens` + `usage.output_tokens` from the parsed typed response. |
| `pkg/decision/typesafe.go` | ~250 | TypeSafe (Jev) adapter (`Provider` const `"typesafe"`, keyed locally). **`BuildUpstream` is a typed struct → System One JSON translation** (pure function over the typed `DecisionRequest`); also re-asserts the typed invariants before emitting System One (defense-in-depth against future OpenAI Decision schema additions the common validator hasn't learned). **`InspectResponse` parses System One response and synthesizes OpenAI-shape typed `DecisionResponse` with ordered `answers[]`** (using the original `questions[]` order via the per-question key map). `ExtractUsage` reads `usage.input_tokens` + `usage.output_tokens` from the parsed response. |
| `pkg/decision/registry.go` | ~30 | `providerRegistry` map + `init()` (port `pkg/imggen/minimax.go:199-215`); keyed on `cred.Provider`; **the handler does not call `providers.NewProvider`** — the local registry returns per-provider HTTP handlers built on `sharedHTTPClient`. |
| `pkg/decision/handler_test.go` | ~400 | Mirror of `pkg/imggen/handler_test.go` — see §9 |
| `pkg/decision/live_e2e_test.go` | ~150 | Mirror of `pkg/imggen/live_e2e_test.go` behind `//go:build live_decision` tag (see §9) |

**Lifecycle (POST /v1/decisions)**:

1. Method guard: 405 if not POST (mirror `pkg/imggen/handler.go:158-168`).
2. Body cap: `MaxBytesReader` at 16MB; 413 on `*http.MaxBytesError` (mirror `pkg/imggen/handler.go:169-188`). **Decision requests can include base64 images (max 128 per request per research-web §Q1) — 16MB is sufficient only at ~128KB avg base64 image (128 × 128KB ≈ 16MB); at 1MB avg the cap fails at ~16 images. 413-on-large-batches is designed behavior** (finding 7: corrected arithmetic; the cap is not a guaranteed headroom, it's a body-shape limit).
3. Auth: sk- Bearer / X-API-Key extraction (mirror `pkg/imggen/handler.go:203-211` + `:523-555`).
4. `parseBody`: read body into the typed `DecisionRequest` (the wire parser). One parse; the same typed value flows through validation, build-upstream, and downstream.
5. Run gotcha validators (typed predicates in `validators.go`): 400 + house OpenAI envelope on any failure (Q4). **Typed-validation errors return 400 at this step; BuildUpstream errors (step 10) return 502 `decision_upstream_build_failed`** (finding 8 — see Q15 for the mapping contract).
6. `resolveModel`: look up model by client-supplied `model` field; **explicit `IsDecision()` check** — reject chat/image-gen models with 400 + house OpenAI envelope (closes the imggen reverse-direction gap, surprise #2). Mirror `pkg/imggen/handler.go:247-265` but with the kind guard.
7. Allow-list (403): `token.IsModelAllowed(model.ID)`; empty `AllowedModels` = all allowed.
8. `resolveCredential`: `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)` (stable affinity per BE-A3/Amendment 18).
9. Provider lookup: `providerRegistry[cred.Provider]` (502 if unregistered).
10. `BuildUpstream`: per-provider, returns `(url, bodyBytes, err)` — on `err` return HTTP 502 `decision_upstream_build_failed` (mirror `pkg/imggen/handler.go:316`); typed-validation errors map to 400 at step 5:
    - `openaidecision.BuildUpstream` → **byte-faithful pass-through (finding 3/Q18 — preferred)**: capture raw request bytes, rewrite only the `model` field; **never re-marshal through typed structs** (silent field-drop on future OpenAI Decision schema additions). Translation only on the typesafe path.
    - `typesafe.BuildUpstream` → translate typed `DecisionRequest` → System One JSON bytes; re-validate typed invariants before emitting (defense-in-depth).
11. Timeout ctx: `DecisionTimeout` (see §8.1).
12. `sharedHTTPClient.Do`: timeout-aware request.
13. Response cap: `io.LimitReader(cap+1)` at 64MB (mirror `pkg/imggen/handler.go:404`).
14. `InspectResponse(respBytes, originalReq)` — **unified 2-arg signature** (finding 5 — openai captures `originalReq` at `BuildUpstream` time so both adapters satisfy the unified `UpstreamProvider` interface): classify success/error per provider wire shape; on success, return typed `DecisionResponse` for relay.
15. Relay write: `Outcome.RelayStatus == 0` ⇒ body verbatim; `!= 0` ⇒ set that status, **body preserved verbatim**.
16. Meter: detached 5s ctx; `Increment` + `IncrementModelUsage` on **success only** (per amendment 5); on failure, **NO writer is called** (failure telemetry via `DecisionEvent` at step 17).
17. Event publish: emit a `DecisionEvent` — string-typed event (sibling of `eventTypeImageGeneration = "image_generation"` at `pkg/imggen/handler.go:62`, a string const), with `map[string]interface{}` payload (sibling of `pkg/imggen/handler.go:612-622`). **Not** a phantom type `ImageGenEvent` (finding 21 — the plan's earlier text invented a type that doesn't exist; the actual constant is `eventTypeImageGeneration` and the payload is a `map[string]interface{}`).

### 3.2.a File-creation → phase mapping (finding 19)

| File | Phase | Task | Notes |
|---|---|---|---|
| `pkg/decision/doc.go` | P1 | P1.6 | Handler shell scaffold |
| `pkg/decision/client.go` | P1 | P1.6 | `sharedHTTPClient` port |
| `pkg/decision/converter.go` | P1 | P1.6 | `Outcome` + `UpstreamProvider` interface (interface signature must be the unified 2-arg `InspectResponse(respBytes, originalReq)` per finding 5) |
| `pkg/decision/registry.go` | P1 | P1.6 | Local registry keyed on `cred.Provider` |
| `pkg/decision/handler.go` | P1 | P1.6 | Skeleton (no adapters); P2 fills in steps 4 + 16 |
| `pkg/decision/types.go` | P2 | P2.2 | Typed structs (per amendment 4) |
| `pkg/decision/validators.go` | P2 | P2.3 + P2.6 | Typed gotcha predicates + new validators per findings 9 (duplicate name) + 24 (safety_identifier ≤128) |
| `pkg/decision/openaidecision.go` | P2 | P2.2 | `openai` adapter — byte-faithful pass-through per finding 3/Q18 |
| `pkg/decision/typesafe.go` | P2 | P2.3 | `typesafe` adapter — typed → System One + re-validates |
| `pkg/decision/handler_test.go` | P2 | P2.7 | Full suite incl. new tests per findings 1/2/9/12/14/24/30 |
| `pkg/decision/live_e2e_test.go` | P4 | P4.2 | Behind `//go:build live_decision` tag |

### 3.3 New route registration

`cmd/main.go`:

- After `cmd/main.go:241` (after the imggen handler block): `decisionHandler := decision.NewHandler(configMgr, bus, tokenStore, usageCounter)`.
- After `cmd/main.go:251` (after the imggen resolver wiring): mirror resolver wiring for decision.
- After `cmd/main.go:291` (after `/v1/image_generation`): `mux.HandleFunc("/v1/decisions", decisionHandler.HandleDecision)`.
- **POST-only is enforced in-handler** (house style — comment at `:286-290`); no `mux.HandleFunc("POST /v1/decisions", ...)` method pattern.
- Optional startup log line (mirror the imggen startup log if one exists; if not, omit — `cmd/main.go` is sparse).

### 3.4 Misroute guards — all 4 entry points

Verified guard matrix (every entry point × guard × status, anchors verified in-source per architect amendment 1):

| Entry point | Guard | Error envelope | Status |
|---|---|---|---|
| `/v1/chat/completions` | `IsDecision()` reject at `pkg/proxy/handler.go:455` (post-resolve, pre-auth `:465`) | `models.NewOpenAIError` code `decision_misroute` | ✅ planned (site 5) |
| `/v1/messages` (Anthropic) | **NEW — `IsDecision()` reject at `pkg/proxy/handler_anthropic.go:~155-183`** (post-resolve, pre-auth) | `models.NewAnthropicError` code `decision_misroute` (route-local — same code string as the chat-side guard but routed through the Anthropic envelope; per finding 23) | 🔴 **REQUIRED** (site 8) — closes a real gap (the route has zero `IsImageGen`/`IsDecision` checks today) |
| `/v1/decisions` | explicit `!model.IsDecision()` reject in resolver (§3.2 step 6) | `models.NewOpenAIError` code `decision_misroute` | ✅ planned — closes imggen's documented reverse gap for decision |
| `/v1/image_generation` | resolver `:715-725` checks `m == nil` only (doc-comment drift `:711-713`); real barrier = minimax-only registry `:302-315` → 502 pre-flight | n/a (502 = provider-registry miss) | 🟢 benign — **no upstream call, no metering** (meter `:486` is post-upstream); documented per amendment 9a |
| `/v1/models` | `IsImageGen()` exclusion `:258` → add `‖ IsDecision()` | n/a | ✅ planned (site 6) |
| Ultimate switching | `ShouldTrigger`/`ForceTrigger` called **only** from chat flow (`pkg/proxy/handler.go:667-672`; 0 hits in anthropic/imggen/main) — `/v1/decisions` can never be ultimate-shadowed even without D9; the site-5 + site-8 guards also preempt `X-Force-Ultimate` shadowing | n/a | ✅ D9 = belt-and-suspenders |
| `events.Bus` → FE | list patcher is type-dispatched (`useEvents.ts:259` PATCH_TYPES, `:266` REFRESH_TYPES, others dropped); imggen events carry no `id` ⇒ dropped by the per-request accumulator (`:107` reqId gate) | n/a | ✅ `DecisionEvent` is FE-inert; no chat-list corruption |
| gzip / body caps | `CompressResponse` scoped `/fe/api/*` (`gzipmw/response.go:307-311`) — no-op for `/v1/*`; caps are per-package constants | n/a | ✅ `pkg/decision` self-contained (own 16MB/64MB constants) |

**Non-blocking note (amendment 1 carry-over)**: a pre-existing image-gen gap on the same `/v1/messages` route (zero `IsImageGen` checks in `handler_anthropic.go`) is a candidate for backport — out of v1 scope per Appendix "NO pkg/imggen changes". Record as a follow-up commission; do not scope-creep this one.

### 3.5 Import-purity guard — 2-layer + new transitive-closure test

- **Layer 1 (Makefile grep)**: `Makefile:52-59` test target; add a clause `! grep -rnE 'github.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/decision/` → FAIL (mirror the imggen rule).
- **Layer 2 (in-package test)**: `pkg/decision/handler_test.go` `TestPackage_ImportGuard_NoProxyImports` (mirror `pkg/imggen/handler_test.go:689-713`).
- **Layer 3 (NEW transitive-closure test)** — improvement over pkg/imggen: `pkg/decision/handler_test.go` `TestPackage_ImportGuard_TransitiveClosure` runs `go list -deps ./pkg/decision/...` and asserts none of the deps are under `github.com/disillusioners/llm-supervisor-proxy/pkg/proxy` or `pkg/imggen` (the chat-imggen interaction surface). Mirror the manual audit at `pkg/imggen/doc.go:25-28` as a CI-runnable test.

### 3.6 Misroute guards — `/v1/models` exclusion

`pkg/proxy/handler.go:258`: change `if m.IsImageGen() { continue }` to `if m.IsImageGen() || m.IsDecision() { continue }`.

### 3.7 Timeout — **`DecisionTimeout` config quartet (Option A — committed)**

**Committed default (per finding 13)**: Option A — `DecisionTimeout` config quartet (mirroring `ImageGenTimeout`). Default 30s, clamp `[10s, 60s]` (per architect amendment 8). The package-constant alternative (Option B) is recorded in §13 Q14 with pricing; the plan does not develop Option B.

- Field: `pkg/config/config.go` (sibling to `ImageGenTimeout` at `:95-103`).
- **Default: 30s** (tightened per architect amendment 8; a 60s default would mask slow upstreams).
- **Clamp: `[10s, 60s]`** (tightened per architect amendment 8; the vendor "10x faster than Responses API" claim is **unverified marketing** — tune post-deploy on live data).
- Accessor: `ManagerInterface` `:137` + both impls (file `Manager` + DB `ConfigManager` `store.go:23-80`).

---

## 4. Wire Contract

### 4.1 Client-facing wire = OpenAI Decisions schema

**Client → proxy request** (verified shape from research-web §Q1):

```json
{
  "model": "decision-router",
  "input": "Customer wrote: I was charged twice for my order #4411 and want a refund today.",
  "questions": [
    {"type": "predicate", "name": "is_refund_request", "instructions": "Is the customer asking for a refund?"},
    {"type": "choice", "name": "department", "instructions": "Which department should handle this?",
     "choices": [
       {"value": "billing", "description": "Payments, invoices, refunds"},
       {"value": "technical", "description": "Bugs and outages"},
       {"value": "other"}]},
    {"type": "score", "name": "frustration", "instructions": "How frustrated is the customer?",
     "levels": [{"label": "calm"}, {"label": "mildly annoyed"}, {"label": "angry"}]}
  ],
  "safety_identifier": "user-1234"
}
```

- `input` accepts string OR `DecisionInputMessage[]` with parts `input_text` (maxLength 10,485,760) and `input_image` (≤128/request).
- `questions` is an array (order is preserved into `answers[]`).
- `safety_identifier` is optional (≤128 chars). **Proxy enforces ≤128 chars at the edge** (`validateSafetyIdentifier` predicate in `pkg/decision/validators.go`, returns 400 + house OpenAI envelope; per finding 24).
- Auth: proxy's own sk- Bearer token (house style); upstream Bearer per credential.

**Proxy → client response** (verified shape):

```json
{
  "model": "gpt-6-luna",
  "answers": [
    {"type": "predicate", "name": "is_refund_request", "probability": 0.97},
    {"type": "choice", "name": "department", "choice": "billing",
     "probabilities": [{"value": "billing", "probability": 0.93}, {"value": "technical", "probability": 0.04}, {"value": "other", "probability": 0.03}],
     "confidence": 0.91},
    {"type": "score", "name": "frustration", "score": 1.2,
     "probabilities": [{"value": 0, "label": "calm", "probability": 0.1}, {"value": 1, "label": "mildly annoyed", "probability": 0.7}, {"value": 2, "label": "angry", "probability": 0.2}],
     "confidence": 0.61}
  ],
  "usage": {"input_tokens": 401, "input_tokens_details": {"cached_tokens": 0, "cache_write_tokens": 0}, "output_tokens": 0, "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": 401}
}
```

- `answers[]` may include `{"type":"refusal","name"|null}` for declined questions (other questions in the same request still answered).
- `model` echoes versioned upstream ID (e.g. `gpt-6-luna` or `jev-1.13.0`); proxy does NOT synthesize `id`/`object`/`created` (neither provider returns them — verified).
- Error envelope on proxy surface = **house OpenAI envelope via `models.NewOpenAIError`** (per project blueprint).

### 4.2 Provider translation (hybrid typed seam per amendment 4)

The shared client wire (`DecisionRequest`, `Question`, etc. — defined in `pkg/decision/types.go`) is **parsed once** by the handler. Each adapter operates over the typed value; no `map[string]interface{}` key-walking in `typesafe.go`.

#### `openai` adapter (registered in decision-local registry as `"openai"`) — **byte-faithful pass-through (committed per finding 3 / Q18)**

- `BuildUpstream(req *DecisionRequest, cred *ResolvedCredential, rawBody []byte) (url string, body []byte, err error)`:
  - **URL**: `https://api.openai.com/v1/decisions` (default; respects `cred.BaseURL` if overridden per Q17 — honor shared credential's base URL as-is).
  - **Body (byte-faithful, NOT typed re-marshal)**: copy `rawBody` (the original request bytes captured at lifecycle step 4 BEFORE typed parse consumes them; or buffered in a `bytes.Buffer` that the typed parser doesn't drain) and overwrite the `model` field with `cred.InternalModel` via `bytes.Replace(rawBody, []byte(`"decision-router"`), []byte(`"gpt-6-luna"`), 1)` (or equivalent — exact model-name tokens from the typed request). **Unknown future OpenAI Decision fields flow through verbatim** — neither dropped nor renamed. The typed `DecisionRequest` is only used for routing/lookup (provider resolution + BuildUpstream body construction), NEVER for re-marshalling the openai path. See A26 in §12.3.
- `InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error)` — **unified 2-arg signature per finding 5**:
  - Parse JSON; if `error` key present at root → return `Outcome{RelayStatus: <upstream status>, Reason: <error code>}`; else return typed `*DecisionResponse`.
- `ExtractUsage(resp *DecisionResponse) Usage`:
  - Read `usage.input_tokens` + `usage.output_tokens` (typed `Usage` struct).

#### `typesafe` adapter (registered as `"typesafe"`) — **typed struct → System One translation**

The translation is a pure function over the typed `DecisionRequest`. **Defense-in-depth**: `BuildUpstream` re-asserts the typed invariants (`validateQuestionsCount`, `validateChoicesPerQuestion`, `validateScoresPerQuestion`, `validateBooleanStringCollision`) before emitting System One — protecting against future OpenAI Decision schema additions the common handler-level validator hasn't learned.

- `BuildUpstream(req *DecisionRequest, cred *ResolvedCredential) (url string, body []byte, err error)`:
  - **URL**: `https://api.typesafe.ai/v1/systemone` (default; respects `cred.BaseURL` if overridden).
  - **`input` → `state`**: string `input` → `state` string. Array `input` with `input_text` parts → join text strings into `state`. Array `input` with `input_image` parts → **return typed validation error → 400** (Jev is text-only per research-web §Q2).
  - **`questions[]` → `questions{}`**: build a map keyed by `name` (or generated id when `name` is absent — System One needs a key per question). **REJECT 400 if any two `Question.Name` equal** (System One map-key collision; per finding 9). Also reject 400 on `len(questions) > 128` or any question lacks `name`.
  - **Type map**:
    - `predicate` → `noul`: `instructions` → `instructions` (string); OpenAI `predicate` has no `criteria`, so no-op.
    - `choice` → `choice`: `choices[].value` (string or boolean) → map entry `{label: value-string}` with `description` retained; **REJECT 400** if `len(choices) < 2` or `len(choices) > 255`. Boolean-vs-string collision (`true` vs `"true"`) rejected at typed predicate layer.
    - `score` → `score`: `levels[]` → `criteria` array of `{label, description?}`-shaped objects. **REJECT 400** if `len(levels) < 2`.
  - **Drop `safety_identifier`** with a 400 from proxy if present (research-web §Q2: "safety_identifier → 400 on TypeSafe").
  - **Body**: marshal the translated struct to bytes.
- `InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error)`:
  - Parse upstream `{model, answers{}, usage}` into a typed System-One response struct. Build ordered `answers[]` from the original `req.Questions[]` order using the `name → key` map. Per-question type map (typed struct ↔ typed struct):
    - `noul` → `predicate`: `{type:"predicate", name, probability: answer.noul}`.
    - `choice` → `choice`: `{type:"choice", name, choice: answer.choice, probabilities:[{value, probability}], confidence}`. Probabilities are a map `{"low":0.0,"high":1.0}` → array `[{value:"low",probability:0.0},{value:"high",probability:1.0}]`.
    - `score` → `score`: `{type:"score", name, score: answer.score, probabilities:[{value: int(key), label: legend[key], probability: probabilities[key]}], confidence: answer.confidence}`. System One's `score` is 0..levels-1; OpenAI's `score` is a probability-weighted average that can fall between levels. **Adopt the liteLLM translation**: emit the int level index as `score` and synthesize `probabilities` from the `legend` keys (liteLLM does the same — see **A24 in §12.2**).
  - Synthesize `Usage` typed struct: `InputTokens=upstream.input_tokens`, `OutputTokens=upstream.output_tokens`, `CachedTokens=0`, `CacheWriteTokens=0`, `ReasoningTokens=0` (System One only returns `{input_tokens, output_tokens}`; the detail fields are zero-filled — see **A25 in §12.2**).
  - Set response `Model` to the upstream `model` (e.g. `jev-1.13.0`).
- `ExtractUsage(resp *DecisionResponse) Usage`:
  - Read `resp.InputTokens` + `resp.OutputTokens`. Note Jev output tokens are free; we still meter them for analytics.

#### LiteLLM gotcha list (from research-web §Q2) — adopted as typed predicates in `pkg/decision/validators.go`

- OpenAI requires `instructions` for `predicate` — synthesize "Is the following statement true?" generic fallback if absent (System One allows `criteria` instead).
- System One `choice` with 1 label has no OpenAI equivalent → 400 at proxy edge.
- System One `score` with 1 level has no OpenAI equivalent → 400 at proxy edge.
- Boolean-vs-string value collision (`true` vs `"true"`) is illegal across the bridge → 400 at proxy edge.
- Text parts of OpenAI `input` get joined into `state`; per-message structure is lost (documented limitation).
- Score semantics differ subtly (OpenAI score = weighted average, can be fractional; System One score = 0..levels-1, between-levels allowed) — emit int level index in OpenAI score field (liteLLM's choice; ASSUMPTION flagged).

### 4.3 Error envelope = `models.NewOpenAIError` (house style)

All 4xx/5xx responses from the decision handler use the standard OpenAI envelope:
```json
{"error": {"message": "...", "type": "...", "param": null, "code": "..."}}
```
This is the same envelope chat and imggen use (`pkg/imggen/handler.go:630` `openAIError` helper, called via `models.NewOpenAIError` from `pkg/models/errors.go`).

### 4.4 NO streaming — both vendors (verified)

Both vendors confirmed no streaming in their schema (research-web §Q1 "No `stream` parameter"; §Q2 "No streaming [verified-from-docs: liteLLM feature table]"). **Future SSE extension point**: if either vendor adds streaming later, the proxy can adopt the same pattern as chat's `pkg/proxy/handler.go` streaming path. For now, the decision handler is synchronous, single-attempt, no SSE relay, no heartbeat — same posture as `pkg/imggen`.

### 4.5 Body caps and response cap

- **Request body**: 16MB `MaxBytesReader` (mirror `pkg/imggen/handler.go:169`); 413 via `*http.MaxBytesError` (`:177-188`). Decision requests can include base64 images (≤128/request per research-web §Q1) — 16MB is sufficient at ≤1MB avg base64 image. If a real-world test pushes this cap, the cap raises to 32MB without other touch-points.
- **Response body**: 64MB `io.LimitReader(cap+1)` (mirror `pkg/imggen/handler.go:404`); over-cap → 502 `:418-431`. Decision responses are tiny (kilobytes); 64MB is paranoid insurance against buggy upstreams.

---

## 5. Provider / Credential Config

### 5.1 Provider IDs — recommended default: **reuse `openai` + add `typesafe`**

The original draft of this plan asserted that reusing bare `"openai"` for decision models would collide with chat dispatch. **That rationale was wrong** (corrected per architect amendment 2, verified in source):

- Chat dispatch calls `providers.NewProvider(cred.Provider, …)` from `pkg/proxy/internal_handler.go:335` and `pkg/proxy/race_executor.go:28` (variable `newProviderClient`). Decision models never reach those paths — `/v1/decisions` is a separate route, and the chat-side `IsDecision()` guard at `pkg/proxy/handler.go:455-462` (per amendment 1, also `/v1/messages` post-resolve) gates them out.
- **`pkg/imggen` proves the reuse pattern at scale**: `pkg/imggen/minimax.go:22` keys the imggen-local registry on `"minimax"` — identical to chat `ProviderMiniMax = "minimax"` (`pkg/providers/factory.go:17`). The imggen-local registry (`minimax.go:199-215`, lookup at `:212-214`) never calls the chat factory. **One year of operation, zero collision.**
- The only thing that would reject a decision model with `internal_provider = "openai"` is our *own* `validateDecisionRules` allowlist — a design choice, not a structural constraint.

Two provider-id approaches are defensible. The plan recommends approach B; A is the documented override.

| Approach | Provider ids | Trade-off |
|---|---|---|
| **A (override)** | `openai-decision` + `typesafe` | **Operator identity clarity** — distinct ids per kind. Cost: operator enters the same OpenAI API key twice (chat `openai` + decision `openai-decision`) and rotates it in two places. **Mandatory FE display label**: "OpenAI Decisions" (never bare "OpenAI") to avoid visual collision with chat `openai` in the credentials tab. |
| **B (recommended)** | `openai` + `typesafe` | **Credential singularity + minimal registry surface** — one OpenAI credential row serves chat and decisions (matches how OpenAI itself works: one key, all endpoints). Mirrors imggen/`minimax` exactly. Cost: operator sees `openai` in both chat and decision forms (mitigated by FE hint: "this OpenAI credential works for chat and decisions"). |

**Recommended default is B**: `openai` (reused; nothing added) + `typesafe` (new). See `decisions.md` Q11 for the override path.

### 5.1.a Reuse-pattern precedent (verified)

- `pkg/imggen/minimax.go:22` registers `Provider = "minimax"` (the same id as chat `ProviderMiniMax = "minimax"` at `pkg/providers/factory.go:17`).
- `pkg/imggen/minimax.go:199-215` keeps the imggen-local registry keyed on `"minimax"`; the imggen handler never calls `providers.NewProvider` — its HTTP client is built in `client.go:26-39`.
- Same shape applies verbatim to the recommended approach: `pkg/decision/typesafe.go` and `openaidecision.go` keep their own local registry keyed on `cred.Provider`; they do not call `providers.NewProvider`. The decision-local registry follows `pkg/imggen/minimax.go:199-215`.

### 5.2 Surfaces to touch for the new provider id (recommended default: `typesafe` only)

Under the recommended identity (B), **`openai` already exists** at `pkg/providers/factory.go:10-20`. The only new provider id is `typesafe`. The factory.go changes shrink to one entry.

| Surface | File:line | Change |
|---|---|---|
| Provider const | `pkg/providers/factory.go:10-20` | Add **`ProviderTypesafe = "typesafe"`** (one entry; `openai` already exists) |
| `NewProvider` switch | `pkg/providers/factory.go:23-70` | One case arm. **`typesafe` returns `fmt.Errorf("… not yet implemented")`** (house style per `pkg/providers/factory.go:27-32` — finding 22 corrected the plan's invented `BUNDLE_NOT_IMPLEMENTED` sentinel). The chat provider layer doesn't speak System One. **The decision handler builds its own upstream HTTP client** (mirroring `pkg/imggen`). |
| `IsProviderSupported` | `pkg/providers/factory.go:72-80` | Add `typesafe` |
| `providerMetadataMap` | `pkg/providers/factory.go:99-154` | Add `typesafe` entry with display name "TypeSafe (Jev)" + default base URL `https://api.typesafe.ai` |
| `GetProviderTypes` | `pkg/providers/factory.go:173-186` | Auto-includes via metadata map (no code change) |
| `validProviders` map | `pkg/models/credential.go:14-24` | Add `typesafe` |
| FE `InternalProvider` union | `pkg/ui/frontend/src/types.ts:109` | Add `typesafe` (FE type-only, no behavioral change) |
| **`validateDecisionRules` provider gate** | `pkg/models/config.go:~157-185` (new) | `internalProvider ∈ {"openai","typesafe"}` (lowercase). With approach A (override), the gate is `{"openai-decision","typesafe"}`. |
| `GET /fe/api/providers` | `pkg/ui/server.go:183`→`:216-225` | Auto-served via `GetProviderTypes` (no code change) |
| **FE DecisionModelForm provider filter** | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx` | Filter BE-served provider list to `["openai","typesafe"]` (mirrors `ImgGenModelForm.tsx:10-14` hardcoded `minimax`). With approach A: `["openai-decision","typesafe"]`. |
| **FE shared-credential hint** | `pkg/ui/frontend/src/components/config/CredentialsTab` (or equivalent) | One-line hint near the OpenAI credential entry: "this OpenAI credential works for chat and decisions". |
| **Pkg/decision local registry** | `pkg/decision/registry.go` | Keyed on `cred.Provider` per `pkg/imggen/minimax.go:199-215`; entries for `typesafe` and a passthrough entry that maps `openai` → `openaidecision.go` (which is a near-pure pass-through). The handler does **not** call `providers.NewProvider`. |

### 5.3 Credential binding

Mirror `pkg/imggen/handler.go:285-296` exactly: `resolveCredential(modelID)` → `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)` (`store.go:2159-2257`) with stable affinity `(modelID, modelID)`. Handler uses `cred.Provider` for dispatch (via the decision-handler-local registry), `cred.APIKey` for upstream Bearer, `cred.BaseURL` for upstream URL (overriding `defaultBaseURL`), `cred.InternalModel` for body rewrite.

### 5.4 Default base URLs

| Provider | Default base URL | Source |
|---|---|---|
| `openai` (reused; recommendation B) | `https://api.openai.com` | research-web §Q1 (full URL `https://api.openai.com/v1/decisions`) |
| `openai-decision` (override A) | `https://api.openai.com` | research-web §Q1 — same URL; only the provider id differs |
| `typesafe` | `https://api.typesafe.ai` | research-web §Q2 |

Operators may override via credential `BaseURL` (e.g. for an Azure-fronted OpenAI proxy or a private TypeSafe deployment). Mirrors the multi-brand baseURL pattern at `pkg/providers/factory.go:36,42,48,58,64`.

### 5.5 `pkg/providers` integration — **recommended: minimal (one new entry for `typesafe` only)**

The `pkg/providers` package is the **chat** provider layer (the `Provider` interface exposes `ChatCompletion` / `StreamChatCompletion`). Decision models are not interchangeable with chat — they have a different wire entirely. **Recommended (identity B)**: add **one** entry in `NewProvider`'s switch for `typesafe` (returns `fmt.Errorf("… not yet implemented")`; chat layer doesn't speak System One). **`openai` is reused as-is** — it already exists and the decision-local registry keys on it without going through `providers.NewProvider`. This mirrors how imggen handles `minimax`: `ProviderType` const exists, but the imggen package ships its own minimax-specific adapter (`pkg/imggen/minimax.go:22`) rather than reusing the chat `OpenAIProvider`.

**Override (identity A — `openai-decision` + `typesafe`)**: add two entries in `NewProvider`'s switch — `openai-decision` returning `NewOpenAIProvider` (chat layer's HTTP client is fine for raw HTTP; only the wire differs), and `typesafe` returning `fmt.Errorf("… not yet implemented")`.

**Override (full Provider impls)**: user may decide to build full `Provider` interface impls for both decision adapters (model-name remap on the chat body for `openai`/`openai-decision`; translation of System One into OpenAI chat format for `typesafe`). This is a much larger surface; **not recommended** for v1.

### 5.6 `validateDecisionRules` provider allowlist

```go
// pkg/models/config.go (sketch — actual code follows the validateImageGenRules pattern)
func validateDecisionRules(m *ModelConfig, internalProvider string) error {
    if !m.Internal {
        return fmt.Errorf("model %s: kind %q requires internal to be true", m.ID, KindDecision)
    }
    if m.InternalModel == "" {
        return fmt.Errorf("model %s: kind %q requires internal_model to be set", m.ID, KindDecision)
    }
    if len(m.Credentials) == 0 {
        return fmt.Errorf("model %s: kind %q requires at least one credential", m.ID, KindDecision)
    }
    if internalProvider != "" {
        switch internalProvider {
        case "openai", "typesafe":
            // ok
        default:
            return fmt.Errorf("model %s: kind %q requires provider openai or typesafe, got %q", m.ID, KindDecision, internalProvider)
        }
    }
    if len(m.FallbackChain) > 0 {
        return fmt.Errorf("model %s: kind %q does not support fallback_chain", m.ID, KindDecision)
    }
    if m.SecondaryUpstreamModel != "" {
        return fmt.Errorf("model %s: kind %q does not support secondary_upstream_model", m.ID, KindDecision)
    }
    if m.PeakHourEnabled {
        return fmt.Errorf("model %s: kind %q does not support peak_hour_enabled", m.ID, KindDecision)
    }
    if m.ReleaseStreamChunkDeadline != 0 {
        return fmt.Errorf("model %s: kind %q does not support release_stream_chunk_deadline", m.ID, KindDecision)
    }
    if !m.ExcludeFromUltimateSwitching {
        return fmt.Errorf("model %s: kind %q requires exclude_from_ultimate_switching=true", m.ID, KindDecision)
    }
    return nil
}
```

The inline mirrors in `pkg/ui/server.go:869-947` (POST) and `:1080-1143` (PUT) MUST mirror these rules (research-codebase §1: T1.2.3 design). They produce `{"error":"..."}` house format, not the OpenAI envelope.

---

## 6. Frontend Design

### 6.1 Decision Models tab — mirror ImgGen tab

Mirror exactly the ImgGen tab's structure (research-codebase §5). Concretely:

- **SettingsPage.tsx** (`pkg/ui/frontend/src/components/SettingsPage.tsx`):
  - `:40` `TabType` union: append `'decision_models'`.
  - `~:351` (after the ImgGen button `:340-351`): new tab button with `data-tab-id="decision_models"`, `aria-hidden`, an emoji span (`🎯` per Q19 default; see decisions.md Q19 for the override option).
  - `:17-38` props surface: add `decisionModels`, `decisionModelActions` (or whatever naming the codebase uses — mirror ImgGen).
  - `~:45-63` destructuring.
  - `~:226` toggle handler: add a case for `'decision_models'`.
  - `~:497` (after imggen content ternary `:484-496`): new content ternary rendering `<DecisionModelsTab ... />`.
  - `:6-7` component imports.
- **App.tsx** (`pkg/ui/frontend/src/App.tsx`):
  - `:6` import `useDecisionModels`.
  - `:30` hook call.
  - `:182-241` prop threading to `SettingsPage`.

### 6.2 NEW components

| File | Lines (~) | Purpose |
|---|---|---|
| `src/components/config/DecisionModelsTab.tsx` | ~341 | Mirror of `ImgGenModelsTab.tsx`. Renders the list of decision models, calls the new hook, handles CRUD. |
| `src/components/config/DecisionModelForm.tsx` | ~439 | Mirror of `ImgGenModelForm.tsx`. **Hardcodes at `:10-14`**: `HARDCODED_KIND='decision'`, `HARDCODED_INTERNAL=true`, `HARDCODED_INTERNAL_PROVIDER` — but here it must be a **dropdown** between `openai` and `typesafe` (recommended identity B — two providers), with `DEFAULT_BASE_URL` switching between `https://api.openai.com` and `https://api.typesafe.ai`. Stamped on submit `:206-217`. (Under override A: `openai-decision` + `typesafe`.) |

The form's provider field is the **only** divergence from the ImgGen form (ImgGen has one hardcoded provider — minimax — Decision has two). The form's submit handler must look up the selected provider's `DEFAULT_BASE_URL` and stamp both `HARDCODED_INTERNAL_PROVIDER` + `DEFAULT_BASE_URL` per the chosen provider.

### 6.3 NEW hook — `useDecisionModels`

Mirror `useImgGenModels` (`src/hooks/useApi.ts:347-422`):

- Cache key: `'decision-models'` (sibling of `'imggen-models'`).
- Fetch URL: `${API_BASE}/models?kind=decision`.
- Return shape: same as `useImgGenModels` (data, loading, error, refresh, mutation handlers).
- **Barrel export** (finding 6): `src/hooks/index.ts:1` re-exports `useDecisionModels` from `./useApi` — `App.tsx:6` imports from `'./hooks'`, so without the barrel export the new hook is unreachable from `App.tsx`. Verified: `src/hooks/index.ts` exists and currently re-exports `useRequests, useRequestDetail, useConfig, useModels, useImgGenModels, formatDuration, useTokens, useAppTags`. Add `useDecisionModels` to that list.

### 6.4 Triple-invalidation everywhere

**Symmetric dual-invalidation becomes triple-invalidation** (C-07, currently 6 sites). **6 existing mutation sites + 3 in the new hook = 9 sites** (finding 17 corrected the prior "9+1=9" garbled arithmetic); **each mutation deletes all 3 keys**. Sites:

| Hook | Site | Change |
|---|---|---|
| `useModels` | `:295-296` | + `delete('decision-models')` |
| `useModels` | `:308-309` | + `delete('decision-models')` |
| `useModels` | `:316-317` | + `delete('decision-models')` |
| `useImgGenModels` | `:389-390` | + `delete('decision-models')` |
| `useImgGenModels` | `:402-403` | + `delete('decision-models')` |
| `useImgGenModels` | `:410-411` | + `delete('decision-models')` |
| `useDecisionModels` (NEW) | (3 sites mirroring imggen) | delete all three keys |

This is correct-and-deliberate but the **strict invariant** is that **any new model mutation hook** must also delete all three keys. **Hard constraint (amendment 9b)**: the follow-up helper MUST use an **explicit key list** `['models','imggen-models','decision-models']` — NEVER `deleteByPrefix('models')`. The prefix matches none of the actual keys (`imggen-models` and `decision-models` do NOT start with `models`), and prefix deletion misses in-flight-only promise entries (`apiCache.delete` is a single key drop). The plan leaves this as 9 explicit deletes (no helper extraction) to match the existing style; **a follow-up extracts a `invalidateAllModelLists` helper with the explicit list hard-coded**.

### 6.4.a FE mutation tests MUST assert triple-key deletion (amendment 9d)

The parity guarantee on the BE side (§9.2) has a FE analog: **every model mutation hook test must assert all three keys are deleted on every mutation**. The new `useDecisionModels.test.ts` covers the new hook's own triple-delete behavior; the **existing `useModels` and `useImgGenModels` mutation tests must be extended** so their deletion-assertions also include `'decision-models'`. Without this, the FE drift class is real: a mutation could delete two of the three keys and silently leave the third stale until the 15s TTL expires.

### 6.5 C-16 client filter — add decision exclusion

**Single site**: `ModelsTab.tsx:157` — change:
```ts
const chatModels = models.filter((model) => model.kind !== 'image-gen');
```
to:
```ts
const chatModels = models.filter((model) => model.kind !== 'image-gen' && model.kind !== 'decision');
```
Update comment `:145-156` to mention both filters. FE-wide sweep (research-codebase §5) found no other two-kind assumptions; **only `types.ts:121` and `:153`** need the kind union extended.

### 6.6 Provider dropdown is BE-served

The new form's provider field reads from `useProviders()` (already BE-served via `GET /fe/api/providers`). The form's `HARDCODED_INTERNAL_PROVIDER` field becomes a `<select>` over the BE-served provider list filtered to `["openai", "typesafe"]` (recommended identity B; the form restricts to those two; the BE serves all known providers). Under override A: `["openai-decision", "typesafe"]`.

### 6.7 FE API surface mirrors

Mirror ImgGen through `pkg/ui/server.go`:

- `?kind=` switch `pkg/ui/server.go:758-770`: add `case models.KindDecision` returning `func(m) bool { return m.IsDecision() }`; default-branch log wording updated for 3 kinds.
- POST kind whitelist `pkg/ui/server.go:869`: extend to include `models.KindDecision`; add inline decision-rule 400 block mirroring the imggen block.
- PUT mirror `pkg/ui/server.go:1080`: same.
- `models.KindChat` / `models.KindImageGen` / `models.KindDecision` are the three accepted values.

### 6.7.a TokenForm picker filters + ultimate-target note (amendment 7)

Two picker sites in `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` currently list **all models unfiltered** — a pre-existing quirk that today leaks image-gen models into token scoping UI. Decision models would inherit the same quirk, so the commission fixes both at once:

| Site | Current | After this plan |
|---|---|---|
| `TokenForm.tsx:101` (allowed-models picker, passed to `<ModelMultiSelect models={models} … />`) | Unfiltered over `[...models]` — today lists image-gen models (pre-existing quirk) | **Filter shape (finding 10)**: compute `const chatModels = models.filter((m) => !m.kind \|\| m.kind === 'chat');` above the `<ModelMultiSelect>` invocation; pass `models={chatModels} …` instead of `models={models} …`. One-line `kind` exclusion (`kind !== 'image-gen' && kind !== 'decision'`; `!m.kind` matches undefined kind for chat-kind compatibility). |
| `TokenForm.tsx:157` (per-token ultimate-model `<select>`) | `[...models].sort(...).map(...)` — today lists image-gen models | **Filter shape (finding 10)**: insert the same `chatModels` filter (or an inline `[...models].filter(m => !m.kind \|\| m.kind === 'chat').sort(...).map(...)`) before mapping to `<option>`. Both pickers receive a pre-filtered `models.filter(...)` array. |

Both changes mirror C-16's `ModelsTab.tsx:157` filter pattern.

**Ultimate-target note (promoted to R21 risk per finding 11)**: the **global** ultimate `model_id` (operator-configured in `SettingsPage:104`) is **free-text**, not a picker. D9 gates *switching for the requested model* (`pkg/proxy/handler.go:677`) — it does **not** prevent configuring a decision model as the ultimate *target*. This is a UX-level concern; the commission does **not** add a kind filter to the free-text field for v1. **Accepted for v1; record as a follow-up commission trigger (see Q16)**. The D9 enforcement at the model-write site (P1.5) prevents the failure case: a decision model will be rejected at write time if `ExcludeFromUltimateSwitching=false`, so an operator who configures a decision model as the global ultimate target has explicitly bypassed the gate. Document in the blueprint; revisit if the bypass causes confusion.

**Follow-up Commission Trigger (item 7b, approver polish round 3)**: **trigger condition** = product asks "why is this decision model being targeted for ultimate switching?" (i.e. an operator workflow friction or an audit incident). When fired: open a small commission to make `SettingsPage:104`'s global ultimate `model_id` field a `<select>` of chat-kind models only, or to add a kind-gate validator at write-time. Priced ~30-150 LOC depending on UX choice (free-text kind-gate validator is cheaper; picker replacement is more invasive).

### 6.8 Embed discipline + tsc baseline

- **Same-commit rule**: any commit touching `pkg/ui/static/index.html` must include regenerated hashed bundles. **Action**: when implementing phase P3 (FE), the implementer regenerates `vite build` and commits `pkg/ui/static/**` + source changes in the SAME commit.
- **tsc baseline 26 errors / 11 files**: documented ONLY in project blueprint. The implementer must not regress this upward (no new `tsc` errors). Adding `DecisionModel` and the new types may push this higher; mitigated by type-only changes that don't introduce new runtime code paths. **Override**: if the implementer cannot keep the baseline flat, flag for review.

### 6.9 FE tests to add

| Test | Path | Purpose |
|---|---|---|
| `useDecisionModels.test.ts` | `src/hooks/__tests__/useDecisionModels.test.ts` | URL hits `${API_BASE}/models?kind=decision`; cache key `'decision-models'`; invalidation on every mutation deletes `'models'` + `'imggen-models'` + `'decision-models'` (all three keys asserted) |
| **`useModels.test.ts` extension** | `src/hooks/__tests__/useModels.test.ts` (no file exists today — create) | **NEW (amendment 9d)**: every `useModels` mutation (create/update/delete) asserts all three keys `'models'` + `'imggen-models'` + `'decision-models'` are deleted from the cache. Locks the 6 existing invalidation sites against silent drift. |
| **`useImgGenModels.test.ts` extension** | `src/hooks/__tests__/useImgGenModels.test.ts` (exists) | **NEW (amendment 9d)**: every `useImgGenModels` mutation asserts the same triple-key deletion. The existing test covers its own key; extend to cover the other two. |
| `DecisionModelsTab.test.tsx` | `src/components/config/__tests__/DecisionModelsTab.test.tsx` | Renders the list, opens form on add, calls API on submit |
| `DecisionModelForm.test.tsx` | `src/components/config/__tests__/DecisionModelForm.test.tsx` | Hardcoded kind/provider on submit; provider dropdown filters to `['openai','typesafe']` (recommended identity; with override A: `['openai-decision','typesafe']`) |
| `SettingsPage.tabs.test.tsx` extension | `src/components/__tests__/SettingsPage.tabs.test.tsx` | New tab renders; data-tab-id matches; click toggles to the decision tab content |
| `ModelsTab.chatFilter.test.tsx` extension | `src/components/config/__tests__/ModelsTab.chatFilter.test.tsx` | New fixture: model of `kind: 'decision'` is filtered out of `chatModels` |
| **`TokenForm.test.tsx` extension** | `src/components/tokens/__tests__/TokenForm.test.tsx` (create if not present) | **NEW (amendment 7)**: allowed-models `ModelMultiSelect` excludes `kind: 'image-gen'` and `kind: 'decision'`; per-token ultimate-model `<select>` excludes both non-chat kinds. |

---

## 7. DB / Schema

### 7.1 NO migration (Path A — recommended)

Justification (research-codebase §1): migration `030_add_model_kind.up.sql` added a **nullable `kind TEXT`, no DEFAULT, no CHECK constraint**. The text column accepts any value today. `ValidateKindRules` is the gatekeeper. The minimal-diff path requires zero changes to the migration registry, querybuilder, store scan sites, or any count-pinned test.

### 7.2 Token metering — reuse existing writers, **success-only** failure policy (no schema change)

- **Both providers DO return token usage** (research-web §Q1, §Q2: `usage.input_tokens` + `usage.output_tokens` minimum).
- **Reuse** `pkg/usage/counter.go` `Increment` (`:68`) and `IncrementModelUsage` (`:194`) — same writers chat uses.
- **No `IncrementTokenDecisions` / `IncrementModelImages` analog needed** (the imggen pattern is image-specific; decision is token-based, which `Increment` already covers).
- **Detached 5s ctx** (mirror `pkg/imggen/handler.go:581`) so client disconnect cannot cancel the billing write.
- **Failure metering policy — `success-only` (chat semantics, NOT imggen's `meterFailure`)**: chat (`pkg/proxy/handler.go:899`/`:915`) writes `Increment` only on success. Imggen (`pkg/imggen/handler.go:601-607`) writes `IncrementTokenImages` on **both** success and failure (with `imageCount=0` on failure — `request_count=1` is still billed). Mirroring `meterFailure` would make decision the only token-billed route that counts failures, mixing a third semantic into the FE totals. **The plan adopts chat's success-only semantics**: `Increment` is called only when `pkg/decision/handler.go` `meter` runs after a successful upstream response. Failure telemetry flows via the `DecisionEvent` published to `events.Bus` (per §3.2 step 17). If failure-counting is desired, it must be documented as a deliberate semantic divergence (see `decisions.md` Q5 override path).
- **Decisions go in `go func()` or sync** (surprise #7; both patterns are house style — chat uses `go func()`, imggen uses sync; either fits decision). **Recommended**: sync inside `meter()`, with the detached ctx, mirroring imggen.

#### 7.2.a Attribution after the fact (escape hatch, verified)

- **Model-level: recoverable.** `model_hourly_usage` PK is `(model_id, hour_bucket)` (migration 025) → join to `models.kind='decision'` (migration 030) works. The kind is recoverable for model-level reporting.
- **Token-level: never recoverable, by schema.** `token_hourly_usage` carries `token_id` only (migration 019 — no `model_id`). No later migration fixes historical token-level attribution. **Accepted small loss**; this is also true under migration 031 (`decision_count`) for any pre-031 rows.
- **Documented in `decisions.md` Q5.**

### 7.3 Caveat: usage rows blend with chat (documented)

- `token_hourly_usage` and `model_hourly_usage` have **no kind column** (surprise #6, verified). Decision input tokens are billed into the same totals; FE usage views (`pkg/ui/handlers_usage.go:197-258+`) cannot distinguish decision consumption from chat.
- **The kind is recoverable at the model level**: a decision model's `kind` is in the `models.kind` column; a usage row for a decision model is identifiable by joining `token_hourly_usage.token_id` → `tokens.id` → `tokens` (not directly — usage rows store `token_id` not `model_id`). **The cleanest signal is `model_hourly_usage.model_id` joined against `models.kind='decision'`** — but the FE usage SQL doesn't currently do that join. This is a FE-only follow-up, not a schema change.
- **Token-level kind attribution — v1 limitation (finding 26)**: the cheapest token-level kind attribution is a **FE-only join** — `token_hourly_usage.token_id` → `tokens.id` → `tokens.allowed_models` → for each allowed model, `models.kind='decision'`. This is approximate (a token with both chat + decision models in its allowed_models cannot be uniquely attributed) and adds zero coupling cost. The **per-token kind preference** path (a per-token `kind` column on `tokens` itself; only `JwtTokensKind` per token would-be attribute) is a true schema change and out of v1 scope. Documented as v1 limitation.

**Follow-up Commission Trigger (item 7a, approver polish round 3)**: **trigger condition** = product asks "how much decision vs chat traffic did token X consume?" (i.e. per-token kind attribution becomes product-visible). When fired: open a small commission to add `JwtTokensKind` (`per-token kind preference`) and backfill `tokens.kind` from `tokens.allowed_models` UDF at write-time. Priced ~150 LOC + migration 033 + 3 coupled sites; v1 ships without.
- **Decision-specific counters (`decision_count`)**: deliberately out of scope (research-codebase §6 option b). The cost of ~10-12 coupled sites (migration ×2 dialects + registry append + 4 writer signatures + 2 UPSERT shapes + FE usage SQL) outweighs the value for v1. **Open question §13**: if product wants decision-specific counters, migration 031 `decision_count` is the path.

### 7.4 Migration 031 trigger condition

Documented trigger (in case the open question is approved): migration `031_add_decision_count.up.sql` ×2 dialects + registry append + `IncrementTokenDecisions`/`IncrementModelDecisions` (copy `:123-149`/`:236-262`) + `HourlyUsageRow.DecisionCount` fields + test DDLs (`pkg/usage/counter_test.go:175-194`, `counter_stress_test.go`, imggen-style `testhelpers_test.go:18-46`) + FE usage SQL columns. **~10-12 coupled sites; every miss is a silent leak** (research-codebase §6). Plan-level note: do NOT do this in the initial ship; gate behind user opt-in.

---

## 8. Config / Settings

### 8.1 `DecisionTimeout` config quartet (committed per finding 13 — Option A)

Mirror `ImageGenTimeout` at `pkg/config/config.go:95-103`:

| Surface | File:line | Change |
|---|---|---|
| Field | `pkg/config/config.go` (sibling to `ImageGenTimeout`) | `DecisionTimeout time.Duration` |
| Default | `pkg/config/config.go:~229-233` (sibling to `ImageGenTimeout: 120s`) | `DecisionTimeout: 30 * time.Second` (tightened per architect amendment 8) |
| Clamp | `pkg/config/config.go:~271-277` (sibling to `ImageGenTimeout: [30s, 4min]`) | `DecisionTimeout: [10s, 60s]` (tightened per architect amendment 8) |
| Accessor | `pkg/config/config.go:137` (`ManagerInterface`) + both impls (`:636-643` file Manager; `store.go:23-80` DB `ConfigManager`) | `GetDecisionTimeout() time.Duration` |

**Q14 alternative (package constant, NOT adopted)**: `defaultDecisionTimeout = 30 * time.Second` in `pkg/decision/handler.go` (sibling of `pkg/imggen/handler.go:42`). Simpler, no config surface; ~5 lines, no config surface, no tests. Trade-off: less operator flexibility. Priced in §13 Q14.

### 8.2 `models-template.json` decision example (documentation-only)

Append a third entry to `config/models-template.json` (recommended identity B — `openai` reused; under override A rename to `openai-decision-default`):

```json
{
  "id": "decision-router-openai",
  "kind": "decision",
  "internal": true,
  "internal_model": "gpt-6-luna",
  "credentials": [{"id": "openai-default", "weight": 100}],
  "exclude_from_ultimate_switching": true
}
```

Plus a second entry for `typesafe`. **Documentation-only** — `grep -rn "models-template" --include="*.go"` returns 0 hits (research-codebase §8). Not loaded by any code.

### 8.3 No env flag (mirroring imggen posture)

No `DECISION_ENABLED` / `IMGGEN_ENABLED`-style env vars. Mirrors imggen: always-on. Only MCP is gated (`MCP_ENABLED`).

### 8.4 `pkg/ui/frontend/src/types.ts:109` provider union

Add `"typesafe"` to the `InternalProvider` union (recommended identity B; `openai` already exists). Under override A, also add `"openai-decision"`. Pure type — no behavioral change. Used by the provider dropdown filter in `DecisionModelForm.tsx`.

### 8.5 `pkg/ui/frontend/src/types.ts:121` + `:153` model type union

- `:121`: change `kind?: 'chat' | 'image-gen'` to `kind?: 'chat' | 'image-gen' | 'decision'`.
- `:153`: add `DecisionModel` type alias (mirror `ImgGenModel`).

---

## 9. Testing Strategy

### 9.1 BE unit tests — `pkg/decision/handler_test.go`

Mirror `pkg/imggen/handler_test.go` (884 lines):

| Test | Purpose |
|---|---|
| `TestHandler_Success_ByteIdenticalRelay` | OpenAI decision returns 200 with body verbatim; relay status 0; usage metered |
| `TestHandler_ErrorIn200_502WithBodyPreserved` | Provider returns 200 with `error` envelope → 502 with body verbatim |
| `TestHandler_MethodGuard_405` | Non-POST returns 405 |
| `TestHandler_BodyCap_413` | Body > 16MB returns 413 via `*http.MaxBytesError` |
| `TestHandler_ResponseCap_502` | Response > 64MB returns 502 |
| `TestHandler_Auth_NoToken_401` | Missing sk- token returns 401 |
| `TestHandler_Auth_WrongToken_401` | Wrong sk- token returns 401 |
| `TestHandler_Auth_TokenNotInAllowList_403` | sk- token with non-matching `AllowedModels` returns 403 |
| `TestHandler_ResolveModel_Unknown_404` | Unknown `model` field returns 404 |
| `TestHandler_ResolveModel_NotDecisionKind_400` | Chat/image-gen model posted to `/v1/decisions` returns 400 (closes imggen reverse-direction gap) |
| `TestHandler_ResolveCredential_502` | Unresolved credential returns 502 |
| `TestHandler_ProviderLookup_502` | Unregistered provider returns 502 |
| `TestHandler_OpenAI_Decision_WireTranslation` | Client sends OpenAI wire → upstream receives model-name remap → response relayed |
| `TestHandler_TypeSafe_InputString_Translation` | Client sends `input: "string"` → upstream receives `state: "string"` |
| `TestHandler_TypeSafe_InputArray_Translation` | Client sends `input: [{type:"message", content:"..."}]` → upstream receives `state: "..."` |
| `TestHandler_TypeSafe_InputImage_400` | Client sends `input: [{type:"message", content:[{type:"input_image"}]}]` → 400 (Jev is text-only) |
| `TestHandler_TypeSafe_QuestionsArrayToMap` | Client sends `questions: [{name:"x",...}]` → upstream receives `questions: {"x": {...}}` |
| `TestHandler_TypeSafe_PredicateTranslation` | `predicate` ↔ `noul` round-trip |
| `TestHandler_TypeSafe_ChoiceTranslation` | `choice` ↔ System One `choice` round-trip; `value` (string/boolean) preserved |
| `TestHandler_TypeSafe_ChoiceOneOption_400` | `choices` with 1 option → 400 (System One requires ≥2) |
| `TestHandler_TypeSafe_BooleanStringCollision_400` | `choices` containing both `true` and `"true"` → 400 |
| `TestHandler_TypeSafe_ScoreTranslation` | `score` ↔ System One `score` round-trip; `legend` synthesized |
| `TestHandler_TypeSafe_ScoreOneLevel_400` | `levels` with 1 entry → 400 |
| `TestHandler_TypeSafe_UsageDetailSynthesis` | System One usage `{input_tokens, output_tokens}` → OpenAI usage with zero-filled `input_tokens_details`/`output_tokens_details` |
| `TestHandler_TypeSafe_AnswerOrderPreserved` | System One answer map → OpenAI ordered array (using original `questions[]` order) |
| `TestHandler_TypeSafe_SafetyIdentifier_400` | Client sends `safety_identifier` for typesafe → 400 (TypeSafe rejects) |
| **`TestHandler_SafetyIdentifier_Over128Chars_400`** (NEW per finding 24) | Client sends `safety_identifier` longer than 128 chars → 400 (proxy edge enforces ≤128 chars via `validateSafetyIdentifier` predicate; per research-web §Q1) |
| **`TestHandler_TypeSafe_DuplicateQuestionName_400`** (NEW per finding 9) | Client sends `questions[]` with two entries sharing the same `name` → 400 (proxy edge detects the System One map-key collision; only boolean/string collision was previously covered) |
| `TestHandler_Meter_DetachedContext_DoesNotCancelOnClientCancel` | Client cancels mid-request; metering still writes |
| **`TestHandler_Meter_NoUsageRow` (renamed from `…_ZeroTokens` per finding 2)** | Failed upstream → `Increment`/`IncrementModelUsage` are **NOT called** (no usage row written); failure surfaces via `DecisionEvent` only. **Locks in success-only metering** per amendment 5; the previous test spec contradicted the success-only decision by implying `Increment` was called with 0 tokens (imggen's `meterFailure` semantics, explicitly rejected). |
| **`TestHandler_BuildUpstream_Error_502_DecisionUpstreamBuildFailed`** (NEW per finding 8/Q15) | `BuildUpstream` returns an error → handler returns 502 + house OpenAI envelope code `decision_upstream_build_failed`. Mirror `pkg/imggen/handler.go:316` mapping. |
| **`TestPackage_ImportGuard_NoProxyImports`** | Mirror `pkg/imggen/handler_test.go:689-713` |
| **`TestPackage_ImportGuard_TransitiveClosure` (NEW)** | Run `go list -deps ./pkg/decision/...`; assert no dep is under `pkg/proxy` or `pkg/imggen` |
| **`TestHandler_PerProviderDefaultBaseURL_Stamping`** (NEW per finding 14) | Form submit with `internal_provider='openai'` → stamped `internal_base_url='https://api.openai.com'`; with `internal_provider='typesafe'` → `internal_base_url='https://api.typesafe.ai'`. (Locks the per-provider DEFAULT_BASE_URL stamping.) |
| **`TestHandler_OpenAI_PathFidelity_BytePassThrough`** (NEW per finding 3/Q18) | Client sends OpenAI wire with a future field (e.g. `experimental_feature: 'test'`); the openai-decision adapter's request bytes must contain that field verbatim (no silent field-drop via typed re-marshal). Per finding 3 + Q18. |
| **`TestHandler_DecisionKind_PersistenceRestart_RoundTrip`** (NEW per finding 30) | Save a `kind: 'decision'` model; restart the in-memory store; reload → `kind` survives. Locks migration-030 kind persistence for the new kind value. |

**Note (per approver polish round 3 item 4)**: The four `/fe/api/models` tests (`TestHandler_ChatArmExcludesDecision`, `…_ImageGenArmExcludesDecision`, `…_ChatArmExcludesImageGen`, `…_DefaultArm_UnknownKind_LogsWarning_ThreeKinds`) **belong canonically in `pkg/ui/__tests__/handlers_models_kind_test.go` (§9.2)** — listed here for visibility, but the canonical home is §9.2.

Same-package white-box (`package decision`), stdlib-only (no testify in go.mod's required module paths), flat `TestHandler_<Behavior>_<Expected>` naming (not table-driven), `t.Helper`/`t.Cleanup`/`t.Run`. `memTokenStore`, `stubConfig` (1-method `configGetter` seam), `openSQLiteForUsage` for usage DDL — all mirror imggen. Use `httptest.NewServer` + `fakeScript{status, body, contentType, delay}` for upstream stubs.

### 9.2 BE kind-system tests

Mirror existing kind-system tests (research-codebase §1) + the new parity test (amendment 6) + the `/v1/messages` guard test (amendment 1):

| Test | Path | Purpose |
|---|---|---|
| `models_manager_kind_test.go` extension | `pkg/models/__tests__/models_manager_kind_test.go` | Decision variant: internal/internal_model/credentials/provider-gate ∈ {`openai`,`typesafe`} passes; provider-gate = other (`minimax`) fails; fallback_chain non-empty fails; peak_hour true fails; secondary_upstream non-empty fails; exclude_from_ultimate_switching=false fails |
| `handlers_models_kind_test.go` extension | `pkg/ui/__tests__/handlers_models_kind_test.go` **(§9.2 canonical home for `?kind=` switch tests)** | **`?kind=chat` excludes image-gen AND decision** (finding 1 — locks the fix for the chat-arm leak; current chat arm `!m.IsImageGen()` only would pass decision models through); `?kind=image-gen` returns only image-gen; `?kind=decision` returns only decision; unknown `kind=` falls back to chat with server log warning (default-branch log wording updated for 3 kinds: "treats as chat" still applies; warning text mentions the unknown kind). **Owns the four `/fe/api/models` tests** (`TestHandler_ChatArmExcludesDecision`, `…_ImageGenArmExcludesDecision`, `…_ChatArmExcludesImageGen`, `…_DefaultArm_UnknownKind_LogsWarning_ThreeKinds`) — per approver polish round 3 item 4, these move from §9.1 to here. |
| `proxy_handler_chat_misroute_test.go` extension | `pkg/proxy/__tests__/proxy_handler_chat_misroute_test.go` | Decision model posted to `/v1/chat/completions` returns 400 + OpenAI envelope `code: "decision_misroute"` |
| `proxy_handler_anthropic_misroute_test.go` (NEW) | `pkg/proxy/__tests__/proxy_handler_anthropic_misroute_test.go` | **Post-resolve** `IsDecision()` reject (asserts the reject happens AFTER model resolution at `pkg/proxy/handler_anthropic.go:~155-183` and BEFORE auth/upstream call at `:439`): returns 400 + **Anthropic envelope** (NOT the OpenAI envelope — route's envelope family) with code `decision_misroute` (route-local; per finding 23). **Negatives**: unknown-model → 404 (not 400); chat model posted to `/v1/messages` → succeeds (chat models are the legitimate target of `/v1/messages`); image-gen model posted → 200 + the pre-existing benign path (no upstream call until cred resolve; out of v1 scope per finding 1 non-blocking). Per finding 23. |
| **`proxy_handler_decision_models_test.go` (NEW)** | `pkg/proxy/__tests__/proxy_handler_decision_models_test.go` | **Post-resolve** `IsDecision()` reject on `/v1/decisions` (asserts the reject happens AFTER model resolution and BEFORE provider lookup): returns 400 + OpenAI envelope code `decision_misroute`. **Negatives**: unknown-model → 404 (not 400); chat model posted → succeeds; image-gen model posted → succeeds (the imggen route, not decisions). Per finding 12 symmetric coverage. |
| **`TestKindRules_Parity_AllThreeSitesInSync` (NEW)** | `pkg/ui/__tests__/kind_rules_parity_test.go` (or `pkg/models/__tests__/`) | Asserts `pkg/ui/server.go:869-947` (POST inline) ≡ `pkg/ui/server.go:1080-1143` (PUT inline) ≡ `models.validateDecisionRules` for the D1–D9 rule set (~80–100 LOC). Runs under `make test` (the real gate). **Drift detector**: if any of the three sites drifts out of sync, the test fails loudly. The FE analog is in §6.4.a (triple-key invalidation on every model mutation); this test covers the **kind-rules** parity (different drift class — model-row schema rules vs cache-key invalidation). |

### 9.3 FE tests — see §6.9

### 9.4 E2E — `pkg/decision/live_e2e_test.go` behind build tag

Mirror `pkg/imggen/live_e2e_test.go`:

- Build tag: `//go:build live_decision` (sibling of `live_imggen`).
- Silently skips without `OPENAI_API_KEY` / `TYPESAFE_API_KEY` env vars.
- Hard budget: ≤3 upstream calls per test (≤6 total across both providers).
- Key from out-of-tree `.env-decision` (mirror `.env-minimax`).
- Run: `go test -tags live_decision -run TestLiveE2E ./pkg/decision/...`.
- Tests:
  - `TestLiveE2E_OpenAI_Decisions_BasicRoundTrip` — POST → relay; verify response shape; verify usage metered.
  - `TestLiveE2E_TypeSafe_SystemOne_RoundTrip` — POST with `input: string` + 3 questions → verify answer shape + ordering.

### 9.5 `make test` as the real gate

Per research-codebase §8 surprise #1: **CI is not test-gating** (`.github/workflows/docker-ci.yml:83` `|| echo` swallows test failures). **`make test` locally is the real gate**. Each phase exit criterion includes "all `make test` passes locally".

### 9.6 Test coverage targets

- `pkg/decision` line coverage ≥85% (mirror imggen's target).
- FE: 100% of the new hook + tab + form + C-16 extension.
- All kind-system paths covered (decision + chat + image-gen).

---

## 10. Phased Breakdown

Four phases. Each phase is a logical work unit; the dependency order is strict (P1 → P2 → P3 → P4). Phases cannot start until the previous phase's exit criterion is met.

| Phase | Name | Objective | Tasks | Coupling | Status |
|---|---|---|---|---|---|
| **P1** | BE core: kind system + handler skeleton + route + guards + parity test | The kind system knows "decision"; the route is wired; misroute guards hold on all 4 entry points (chat, anthropic, decision, image-gen benign); pkg/decision skeleton compiles; in-handler auth works; the 3 enforcement sites are locked in sync by the parity test. | 9 | tight with P2 (handler skeleton shape), independent of P3/P4 | pending |
| **P2** | Providers + typed seam + success-only metering | OpenAI Decision + TypeSafe adapters via the hybrid typed seam (typed structs in `pkg/decision/types.go`; validators as typed predicates; openai re-emits bytes; typesafe typed→System-One + re-validates); usage metered on success only into existing writers. | 8 | tight with P1 (skeleton shape), independent of P3/P4 | pending |
| **P3** | Frontend: settings + tab + form + hook + C-16 + TokenForm filters | A user can CRUD decision models from the dashboard; chat picker excludes them; tab toggles type correctly; token pickers (per-token ultimate + allowed-models) exclude non-chat kinds. | 8 | tight with P1 (FE API surface), independent of P2/P4 | pending |
| **P4** | Tests + e2e + bundle regen + docs | All tests pass locally; live e2e tagged; FE bundle regenerated in same commit; documentation updated. | 5 | depends on P1+P2+P3 | pending |

### P1 — BE core (9 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P1.1 | Add `KindDecision` const + `IsDecision()` helper to `pkg/models/config.go` | none | `go build ./pkg/models/...` passes; `IsDecision()` returns true iff `Kind == "decision"` |
| P1.2 | Add `case KindDecision:` arm + `validateDecisionRules` to `pkg/models/config.go` | P1.1 | Unit tests for D1-D9 (rule set) pass; existing image-gen + chat tests still pass |
| P1.3 | Add chat-side misroute guard at `pkg/proxy/handler.go:455-462` + `/v1/models` exclusion at `:258` **+ `/v1/messages` post-resolve `IsDecision()` reject at `pkg/proxy/handler_anthropic.go:~155-183`** (NEW per amendment 1; Anthropic envelope via `models.NewAnthropicError`) | P1.1 | Decision model POSTed to `/v1/chat/completions` returns 400 + `decision_misroute` (OpenAI envelope); decision model POSTed to `/v1/messages` returns 400 + Anthropic envelope; `/v1/models` excludes decision models |
| P1.4 | Add `case models.KindDecision` to `pkg/ui/server.go:758-770` `?kind=` switch **AND extend the chat arm to `!m.IsImageGen() && !m.IsDecision()` (finding 1 — current chat arm is `!m.IsImageGen()` only; without this fix, decision models leak into `?kind=chat`)** | P1.1 | `GET /fe/api/models?kind=chat` returns only chat models (no image-gen, no decision); `?kind=image-gen` returns only image-gen models; `?kind=decision` returns only decision models; unknown `kind=` falls back with log warning (default-branch log wording updated for 3 kinds) |
| P1.5 | Add inline decision-rule 400 blocks to `pkg/ui/server.go:869-947` (POST) + `:1080-1143` (PUT) | P1.2 | POST/PUT with non-conforming decision model returns 400 with house `{"error":"..."}`; mirrors `models.ValidateKindRules` |
| P1.6 | Create `pkg/decision/{doc.go,client.go,converter.go,registry.go,handler.go}` skeleton — no provider adapters yet | none | `pkg/decision/handler.go` has the entry function; routes POST `/v1/decisions`; in-handler auth (no providers); resolveModel + IsDecision() check; resolveCredential; 502 for unregistered provider |
| P1.7 | Register route at `cmd/main.go:~285` + handler wiring at `~:241-251` | P1.6 | `go build ./...` succeeds; **do NOT run `make build`** (auto-increments VERSION — R12). `cmd/main.go` references `pkg/decision` and the mux registers `/v1/decisions` |
| P1.8 | Add import-guard clauses: `Makefile:52-59` grep + `pkg/decision/handler_test.go` `TestPackage_ImportGuard_NoProxyImports` + NEW `TestPackage_ImportGuard_TransitiveClosure` | P1.6 | All three guards pass on the empty-skeleton state |
| **P1.9** | **NEW (amendment 6) — decision-rule parity test**: assert `pkg/ui/server.go:869-947` (POST inline) ≡ `pkg/ui/server.go:1080-1143` (PUT inline) ≡ `models.validateDecisionRules` for the D1–D9 rule set (~80–100 LOC). Runs under `make test` (the real gate; CI is non-gating). Locks the 3 enforcement sites in sync. | P1.5 | The parity test fails loudly if any of the three sites drifts out of sync with the other two. **Documented binding trigger (4th-kind trigger)**: if a 4th model kind is commissioned, extract a single `ValidateAndMapToHouseError` entrypoint and rewrite the mirrors in that commission. Full extraction now is the priced alternative (Q13; ~400–500 LOC; retroactively fixes image-gen's `ExcludeFromUltimateSwitching` doc/code drift). |

**P1 exit criterion (9 tasks)**: A decision model (with `kind="decision"`, internal=true, internal_model="gpt-6-luna", provider=`openai` only at P1 — the `typesafe` validProviders gate lands in P2.1) can be saved via the FE API; `POST /v1/decisions` returns 502 (no providers registered yet) but the route is live; chat-misroute guard returns 400 + `decision_misroute` (TWO parallel branches per §3.1 site 5); **Anthropic-route misroute guard returns 400 + Anthropic envelope** (NEW); `/v1/models` excludes decision models; the 3-site parity test (P1.9) passes. `make test` passes.

### P2 — Providers + typed seam + success-only metering (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P2.1 | Add provider id entries: `pkg/providers/factory.go:10-20, 23-70, 72-80, 99-154, 173-186` + `pkg/models/credential.go:14-24` + `pkg/ui/frontend/src/types.ts:109`. **Recommended identity (B): only `typesafe` is new**; `openai` already exists. (Override A adds `openai-decision` instead.) | P1 | `GET /fe/api/providers` returns `typesafe` (and `openai-decision` under override A); FE `DecisionModelForm` provider dropdown filtered to the recommended pair |
| P2.2 | Implement `pkg/decision/types.go` (typed structs: `DecisionRequest`, `Question`, `DecisionResponse`, `Usage`) + `pkg/decision/openaidecision.go` adapter: emits the captured raw request bytes with `model` rewritten only (byte-faithful per Q18); `InspectResponse` returns typed `*DecisionResponse`; `ExtractUsage` reads typed `Usage` | P1 | Typed wire round-trip succeeds for `openai` provider; relay + typed usage extraction |
| P2.3 | Implement `pkg/decision/validators.go` (typed gotcha predicates) + `pkg/decision/typesafe.go` adapter: `BuildUpstream` is a pure typed-struct → System One JSON translation; re-asserts typed invariants before emitting (defense-in-depth); `InspectResponse` synthesizes typed ordered `DecisionResponse` from System One's keyed `answers{}` map | P1, P2.2 | Translation edge cases for `input_image` rejection, choice/score/boolean edge cases all return 400 via typed predicates |
| P2.4 | Wire detached-context metering in `pkg/decision/handler.go` (mirror `pkg/imggen/handler.go:581`) — **success-only** semantics (chat, not imggen's `meterFailure`). Failure telemetry via `DecisionEvent` (step 17) | P2.2, P2.3 | Successful decisions write `Increment` + `IncrementModelUsage` with input + output tokens; failures do NOT write a token row; client cancel does not cancel metering (detached ctx) |
| P2.5 | Emit `DecisionEvent` via `events.Bus` from `pkg/decision/handler.go` | P2.2, P2.3 | Event fires on every completed decision; payload includes model ID, provider, usage |
| P2.6 | Typed gotcha predicates in `pkg/decision/validators.go` (typed functions on `*DecisionRequest`): choice-with-1-option, score-with-1-level, boolean/string collision, >255 choices, >10 levels, >128 questions, **duplicate `Question.Name` (finding 9), `safety_identifier` ≤128 chars (finding 24)**. The handler runs these once; `typesafe.BuildUpstream` re-asserts a subset before emitting (defense-in-depth). | P2.3 | All 8 validators return 400 with house OpenAI envelope |
| P2.7 | `pkg/decision/handler_test.go` full suite (29+ tests from §9.1) | P2.2, P2.3, P2.4, P2.5, P2.6 | All tests pass; coverage ≥85% |
| P2.8 | End-to-end smoke (manual, not CI): save a decision model, POST `/v1/decisions`, verify relay, verify usage in `model_hourly_usage` table | P2.1, P2.2, P2.3, P2.4 | Smoke script succeeds for both providers (using test creds) |

**P2 exit criterion (8 tasks)**: A decision model with provider `openai` (reused) **or** `typesafe` (new — validProviders entry is added in P2.1) can be saved; `POST /v1/decisions` returns 200 with the typed OpenAI Decision wire shape (translated from System One for typesafe); usage is metered **on success only** (chat semantics); misroute guards hold on all 4 entry points; all BE tests pass.

### P3 — Frontend (8 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P3.1 | Add `kind: 'decision'` to FE type unions: `types.ts:109, :121, :153` (+ `DecisionModel` alias) | P1 | `tsc` baseline (26/11) does not regress |
| P3.2 | Extend `ModelsTab.tsx:157` C-16 filter to exclude decision models from `chatModels` | P3.1 | New test fixture: `ModelsTab.chatFilter.test.tsx` decision-row case passes |
| P3.3 | Extend all six existing dual-invalidation sites in `useApi.ts` to triple-key invalidation (`:295-296`, `:308-309`, `:316-317`, `:389-390`, `:402-403`, `:410-411`) | P3.1 | All existing hook tests pass; new mutations also delete `'decision-models'` |
| P3.4 | Add `useDecisionModels` hook (mirror `useImgGenModels`) in `useApi.ts:347-422` template | P3.1 | New `useDecisionModels.test.ts` passes; URL hits `${API_BASE}/models?kind=decision`; cache key is `'decision-models'`; mutations delete all three keys |
| P3.5 | Add `DecisionModelsTab.tsx` + `DecisionModelForm.tsx` components; wire to `SettingsPage.tsx` (TabType, tab button, content branch, toggle handler, prop surface, App.tsx threading) | P3.1, P3.4 | New `DecisionModelsTab.test.tsx` + `DecisionModelForm.test.tsx` pass; `SettingsPage.tabs.test.tsx` extended for the new tab passes |
| P3.6 | Rebuild frontend bundle (`vite build`) and commit `pkg/ui/static/**` in the SAME commit | P3.5 | `pkg/ui/static/index.html` + regenerated hashed bundles present in the same diff; `go:embed static/*` references resolve |
| P3.7 | `tsc` baseline check: ensure no regression above 26 errors / 11 files | P3.5 | `tsc --noEmit` count ≤26/11; if higher, flag for review before merge |
| **P3.8** | **NEW (amendment 7)** — Add TokenForm picker kind filters: `TokenForm.tsx:101` (allowed-models `ModelMultiSelect` prop) and `TokenForm.tsx:157` (per-token ultimate-model `<select>`) exclude non-chat kinds (one-line each, mirrors C-16). Tests: extend `TokenForm.test.tsx` with fixtures covering image-gen + decision exclusion. **Global ultimate `model_id` (free-text, `SettingsPage:104`) NOT kind-gated — accepted for v1** (document in blueprint). | P3.1 | Both pickers list chat-only models; tests pass |

**P3 exit criterion (8 tasks)**: A user can open the dashboard, navigate to the new "Decision Models" tab, create a model of `kind=decision` with provider `openai` or `typesafe`, see it in the list, edit it, delete it. The chat picker excludes it. The chat-models tab shows only chat models. The TokenForm pickers (allowed-models + per-token ultimate) exclude image-gen and decision models (one-line kind filters each). All FE tests pass — including mutation tests that assert all three cache keys are deleted on every model mutation.

### P4 — Tests + e2e + bundle regen + docs (5 tasks)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| P4.1 | Full `make test` runs and passes | P1, P2, P3 | Zero test failures; coverage ≥85% for `pkg/decision`; 100% for new FE tests |
| P4.2 | Live e2e tests: `pkg/decision/live_e2e_test.go` behind `//go:build live_decision` tag | P2 | Tests skip silently without env vars; manual run with `OPENAI_API_KEY` + `TYPESAFE_API_KEY` succeeds for both providers; ≤3 calls per provider (hard budget) |
| P4.3 | Update `models-template.json` with decision example entries (documentation-only) | P1 | Both providers represented; no code-loading effect (verified via grep) |
| P4.4 | Update project blueprint: add a "Decision Models" blueprint section citing this plan; document the kind value, route, providers, FE tab, import-guard test | P1, P2, P3 | Blueprint mentions all 7 decision sites + the new test types |
| P4.5 | CHANGELOG entry: "Decision model kind added (kind=decision); OpenAI Decision API + TypeSafe (Jev) providers; new /v1/decisions route; new Decision Models dashboard tab" | All | CHANGELOG.md entry present |

**P4 exit criterion (5 tasks)**: `make test` passes; live e2e is runnable (but not required to pass for ship — out of CI budget); documentation is consistent; blueprint reflects the new surface. Ready for user-authorized ship.

---

## 11. Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | **OpenAI Decisions API is in beta** — schema may change before GA. | High | High | Pinned to verified-from-docs schema (research-web §Q1). Adapter has a single wire-translation surface (`openaidecision.go`); future revisions are local. LiteLLM v1.104.2+ already speaks this wire (reference impl). |
| R2 | **base64-vs-hosted-URL doc discrepancy** for OpenAI Decisions image input (research-web §Q1 unverified #3). | Medium | High | Plan for base64-only at v1; the API reference says hosted URLs are allowed but the guide says they're not. Re-test on live; if hosted URLs work, no change; if rejected, current plan already correct. |
| R3 | **Jev auth-header format is inferred**, not verbatim (research-web §Q2). | High | Low | Bearer format is corroborated by liteLLM + Kong docs; if live test rejects, swap to `X-API-Key` or whatever the actual format is. One-line fix in `typesafe.go`. |
| R4 | **Usage rows blend with chat** (surprise #6). | Low | High | Documented in §7.3. The kind is recoverable via `model_hourly_usage.model_id` → `models.kind='decision'` (FE-only follow-up if needed). No schema change in v1. |
| R5 | **jev-latest model drift** — `model` field reports the versioned ID that actually answered (research-web §Q2). | Medium | Medium | The proxy echoes the upstream model in the response; clients are expected to handle model versioning. If thresholds are tuned, pin to `jev-1.13.0` etc. via the model's `internal_model`. |
| R6 | **Unknown rate limits** for both providers (research-web §Q3 #1 + #5). | Medium | High | **CORRECTED per finding 15**: imggen's `sharedHTTPClient` (port `pkg/imggen/client.go:26-39`) is **single-attempt** with no retry / 429 handling — the previous plan text was wrong. Mitigation: decision uses the same single-attempt posture; upstream status + body surface to the client verbatim (imggen-style: `Outcome{RelayStatus, Reason}` per step 15 of §3.2). 429 + `retry-after` are forwarded as upstream status with body verbatim; the client decides retry policy. **Retry / rate-limiting is a follow-up commission** that would touch all `sharedHTTPClient` consumers — out of v1 scope. |
| R7 | **Embed discipline regression** — FE bundle staleness if same-commit rule not followed. | Low | Medium | Plan §6.8 explicitly mandates same-commit regeneration; P3.6 enforces it; FE blueprint records the rule; P4 verification confirms `pkg/ui/static/index.html` + hashed bundles in the same diff. |
| R8 | **tsc baseline regression** — adding new types may push the 26/11 baseline higher. | Low | Medium | Plan §6.8 mandates ≤26/11; P3.7 enforces it; if violated, flag for review before merge. |
| R9 | **Importer cycle** via `pkg/decision → pkg/models → pkg/usage` or similar. | Medium | Low | Makefile grep + in-package test + transitive-closure test (P1.8). All three layers guard against `pkg/proxy` and `pkg/imggen` (which would cause cycles). |
| R10 | **`IsDecision` resolve check breaks the imggen pattern** if the new handler accidentally uses imggen's resolver. | Low | Low | `pkg/decision` ships its own resolver functions (not reused from `pkg/imggen`); import-guard transitive test (P3) blocks imggen imports. |
| R11 | **`ExcludeFromUltimateSwitching=true` enforcement** — image-gen's doc/code drift (surprise #4) means the codebase never enforced it; decision adds explicit enforcement (D9). | Low | Low | D9 in `validateDecisionRules` + inline mirror + DB write path. If a test tries to create a decision model with `ExcludeFromUltimateSwitching=false`, it fails. |
| R12 | **`make build` auto-increments VERSION** (surprise #8) — implementer must not run it. | Low | Medium | Plan §1 (non-goals) explicitly states "no build". Implementer uses `go build ./...` directly for verification. P4.1 uses `make test` (not `make build`). |
| R13 | **`/v1/systemone` pass-through is out of scope** — clients wanting native Jev wire have no path. | Low | Medium | Open question §13; documented in research-web §Q1.7. Future native pass-through can be added without breaking this surface. |
| R14 | **Migration 031 trigger** — if the open question on metering is approved mid-flight, ~10-12 coupled sites change in one PR. | Medium | Low | Plan §7.4 documents the migration path; the implementer must NOT do it speculatively. The open question gates the trigger. |
| R15 | **Provider id naming** — `openai-decision` vs `openai` (collision), `typesafe` vs `jev` (brand alignment). | Low | Low | **CORRECTED**: the collision claim was false (amendment 2). §5.1 recommends **`openai` (reused) + `typesafe`**; override is `openai-decision` + `typesafe` (operator identity clarity; mandatory FE display label "OpenAI Decisions"). |
| **R16** | **NEW — `/v1/messages` pre-existing gap** (amendment 1). Today the Anthropic route has zero `IsImageGen`/`IsDecision` checks (`pkg/proxy/handler_anthropic.go` verified). A decision model posted there executes a real chat upstream call (money-burning). | High | High | Add the `IsDecision()` reject at `pkg/proxy/handler_anthropic.go:~155-183` post-resolve, Anthropic envelope via `models.NewAnthropicError`. Pre-existing image-gen gap on the same route is a backport follow-up (out of v1 scope per Appendix "NO pkg/imggen changes"). |
| **R17** | **NEW — Typed seam schema churn** (amendment 4). Future OpenAI Decision schema additions may add new fields to `DecisionRequest` that the typed `pkg/decision/types.go` doesn't model. | Medium | Medium | `typesafe.BuildUpstream` re-validates typed invariants (defense-in-depth, P2.3). For unknown new fields, `json.Marshal` ignores them and System One translation emits whatever it can; the 400 message names the missing field. Adding new fields is a local extension to `types.go` + the relevant translation step. |
| **R18** | **NEW — Parity-test false sense of safety** (amendment 6). The parity test locks the 3 sites in sync but does NOT cover the inline mirrors' `models.KindChat` whitelist in `pkg/ui/server.go:869` — drift in the whitelist is not parity-tested. | Low | Low | Extend the parity test to also cover the kind-whitelist set (POST `:869-877` and PUT `:1080-1088` mirror). Add a sibling assertion that `models.KindChat` / `models.KindImageGen` / `models.KindDecision` are the only accepted values in the inline mirrors. |
| **R19** | **NEW — Success-only metering surprise** (amendment 5). Decision failures do not write a token row; operators comparing decision vs chat usage rows may be confused by the absence of failure rows. | Low | Medium | Failure telemetry flows through `DecisionEvent` (P2.5). Document in blueprint: "decision metering is success-only (chat semantics); failures emit an event but no usage row." |
| **R20** | **NEW — DecisionTimeout default 30s vs vendor claim** (amendment 8). The vendor "10x faster than Responses API" claim is unverified marketing; live latency may exceed 30s under load. | Medium | Medium | 30s default with `[10s, 60s]` clamp surfaces the issue (operator will see timeouts and tune). Live e2e (P4.2) measures latency; tune on real data. Configurable via `DecisionTimeout` quartet (operator can set higher temporarily). |
| **R21** | **NEW — Global ultimate `model_id` not kind-gated** (finding 11). The operator-configured **global** ultimate `model_id` (`SettingsPage:104`, free-text input) accepts any string; an operator can name a decision model as the ultimate target. D9 gates switching *for the requested model* (`pkg/proxy/handler.go:677`) but does NOT gate the ultimate target's kind. **Scope-deferral**: accepted for v1 (per-kind filter on free-text input adds friction without clear win; D9 at write-site catches the parallel mistake). Trigger: **follow-up commission** — when product asks "why is this decision model targeted for ultimate switching?", open a kind-filter follow-up. Recorded as Q16. | Low | Low | Documented; recorded as follow-up commission trigger; no code change for v1. |

### 11.1 Zero-decision-deployment + rollback (finding 29)

**Zero-decision deployments** (no decision models saved + no decision traffic) are **safe**:

- `POST /v1/decisions` is always registered (imggen model — always-on, no env flag). If no decision model is saved, `resolveModel` at §3.2 step 6 returns 404 (`unknown model`) — no upstream call, no metering, no event. The route is reachable but inert.
- The new Decision Models tab in the FE renders an empty list (`useDecisionModels` returns `data: []`). Tab toggle works; form submit fails with 400 (no providers — pre-§3.2 step 9 returns 502 if a decision model exists; with no models, the list is just empty).
- The chat picker filter (C-16 + finding 1 fix) is a passive kind exclusion — adding `decision` to the exclusion list when no decision models exist is a no-op.
- **No schema-level change** (Path A — migration 030 already in place; the value `"decision"` is a NEW string constant in the `kind` column, not a schema change). Rolling back does NOT require a migration.
- **No env flag**. Imggen is always-on (mirrors this posture); MCP is the only env-gated route.

**Rollback path**: discard the `feature/decision-model-type` branch (LOCAL ONLY per active wt-claim). On the next deploy (or revert), `pkg/decision/` is no longer compiled, `/v1/decisions` is no longer registered, `models.KindDecision` and `IsDecision()` are no longer defined. The `kind` column retains any `"decision"` rows that were saved (no DDL change), but `ValidateKindRules` no longer has the `case KindDecision` arm, so any existing decision rows would be rejected by the default arm ("unknown kind"). **Mitigation for an active deployment**: before rolling back the branch, an admin should DELETE all `kind='decision'` rows (a small one-time cost). This is the **stated rollback mechanism** — consistent with §8.3's no-env-flag posture (always-on; no production-rollback feature flag is added or proposed; the feature-flagged-state suggestion is removed as contradictory to the always-on posture, per approver polish round 3 item 5).

**This is a Path A selling point** (finding 29): zero-decision deployments are silent + rollback is a branch discard.

---

## 12. Assumptions

Every non-verified schema/behavior fact is flagged here. Each entry: what is assumed + source + when to verify.

### 12.1 OpenAI Decisions API assumptions

| # | Assumption | Source | When to verify |
|---|---|---|---|
| A1 | Request schema: `model` string, `input` (string \| DecisionInputMessage[]), `questions[1..128]` (Predicate \| Choice \| Score), `safety_identifier` optional ≤128 chars. | research-web §Q1 [verified-from-docs] | Live e2e (P4.2) — first call confirms schema acceptance. |
| A2 | Response schema: `{model, answers[], usage}` — no `id`, `object`, `created` fields. | research-web §Q1 [verified-from-docs] | Live e2e (P4.2). |
| A3 | Image input: `input_image` part with `image_url` (base64 data URL or hosted HTTP(S) URL). The reference schema accepts both; the guide says base64-only. | research-web §Q1 [verified-from-docs, conflict noted] | Live test with hosted URL (R2); if rejected, current plan is correct. |
| A4 | Streaming: NONE. | research-web §Q1 [verified-from-docs; liteLLM feature table "Streaming: No"] | N/A — not implemented. |
| A5 | Idempotency: NONE — no `Idempotency-Key` header support. | research-web §Q1 [verified-from-docs, negative finding] | N/A — out of scope. |
| A6 | Rate limits: standard usage-tier, no endpoint-specific schedule. | research-web §Q1 [inferred from platform conventions] | Live observation; R6. |
| A7 | Error envelope: standard OpenAI `{error: {message, type, param, code}}`. | research-web §Q1 [inferred from platform conventions — no decisions-specific error doc] | Live test failure path; if shape differs, propagate verbatim. |
| A8 | Pricing: $0.10/1M input tokens; output/cache-free. | research-web §Q1 [verified-from-docs] | Provider invoice. |

### 12.2 TypeSafe/Jev assumptions

| # | Assumption | Source | When to verify |
|---|---|---|---|
| A9 | Auth header: `Authorization: Bearer <key>`. | research-web §Q2 [inferred — TypeSafe reference lists Authorization header (value stripped by reader); Bearer format corroborated by liteLLM + Kong docs. High confidence, not verbatim-verified] | First live call (R3); if rejected, swap to whatever format works. |
| A10 | Base URL: `https://api.typesafe.ai`. Endpoint: `POST /v1/systemone`. | research-web §Q2 [verified-from-docs] | Live e2e (P4.2). |
| A11 | System One schema: `state` (string\|object\|array), `questions{}` map (NOT array), `noul` predicate, `choice` with `criteria` map, `score` with `criteria` array. | research-web §Q2 [verified-from-docs: docs.typesafe.ai/api + liteLLM verbatim example] | Live e2e. |
| A12 | Jev is text-only — image/audio/video inputs are pre-processed. Decision models with `input_image` parts posted to a typesafe-backed model return 400 from the proxy. | research-web §Q2 [verified-from-docs: /models — text-only] | Live test. |
| A13 | Response: `{model, answers{}, usage: {input_tokens, output_tokens}}`. | research-web §Q2 [verified-from-docs: liteLLM verbatim TypeSafe response] | Live e2e. |
| A14 | Pricing: $0.042/1M input tokens; output tokens free. | research-web §Q2 [inferred: marktechpost + cellcog citing TypeSafe pricing page] | Provider invoice. |
| A15 | Rate limits: 429 + 529 Overloaded documented; exact quotas not published. | research-web §Q2 [verified status codes / gap on numbers] | Live observation; R6. |
| A16 | Model catalog: `jev-latest` (moving alias), `jev-1.13.0` (versioned ID), `jev-preview`. Response `model` reports the versioned ID that actually answered. | research-web §Q2 [verified-from-docs: /models] | Live test. |
| A24 (NEW per finding 20) | **OpenAI Decision `score` semantics translation**: emit the System One **integer level index** (0..levels-1) as the OpenAI `score` field, and synthesize `probabilities[]` from the System One `legend` keys. liteLLM reference bridge does the same. System One may return scores between levels (the `score` field is 0..levels-1 and not necessarily an integer); the integer-only OpenAI representation is an approximation. | research-web §Q2 [liteLLM reference; liteLLM feature table] | Live e2e (P4.2); verify with at least one typesafe call that returns a non-integer score. |
| A25 (NEW per finding 20) | **OpenAI usage zero-fill**: System One returns only `{input_tokens, output_tokens}`. The OpenAI-shape usage detail sub-objects `input_tokens_details.cached_tokens`, `input_tokens_details.cache_write_tokens`, `output_tokens_details.reasoning_tokens` are zero-filled in the proxy's synthesized response. liteLLM reference bridge does the same. | research-web §Q2 [liteLLM reference; inferred from OpenAI decisions shape] | Live e2e (P4.2). |
| **A26 (NEW per finding 3)** | **OpenAI path fidelity (byte-faithful pass-through)**: under finding 3's committed default, the openai-decision adapter does **not** re-marshal through typed `DecisionRequest`. Unknown future OpenAI Decision fields flow through verbatim — neither dropped nor renamed. Trade-off accepted: unknown fields bypass the typed validator (no defense-in-depth for the openai path). The `DisallowUnknownFields` alternative would **reject** unknown fields upstream and is explicitly NOT adopted (it would break forward-compat). Documented as accepted posture per finding 3/Q18. | plan §3.2 step 10 + §4.2/Q18 | Live e2e (P4.2) with a known unknown field; verify byte-faithful transport. |

### 12.3 Codebase-derived assumptions

| # | Assumption | Source | When to verify |
|---|---|---|---|
| A17 | `pkg/decision` will be a **sibling** package of `pkg/imggen`, with its own resolver functions (not reused from `pkg/imggen`). | research-codebase §4 + §2 (imggen reverse-direction gap) | P1.6 implementation. |
| A18 | `DecisionTimeout` config quartet is acceptable (operator-tunable). If user prefers package constant, §3.7 Option B replaces §8.1. | §3.7 + §8.1 of this plan | User decision. |
| A19 | Migration 031 `decision_count` is out of scope for v1. | §7.4 of this plan | User decision. |
| A20 | `ExcludeFromUltimateSwitching=true` is **explicitly enforced** for decision models (D9), unlike image-gen's doc/code drift. | §5.6 + §3.1 of this plan; surprise #4 | User decision. |
| A21 | Provider ids are `openai` (reused) + `typesafe` (recommended identity B per amendments 2-3). | §5.1 of this plan | User decision (override: `openai-decision` + `typesafe` for operator identity clarity, mandatory FE display label "OpenAI Decisions"). |
| A22 | Client wire = OpenAI Decision wire. No native `/v1/systemone` pass-through at v1. | §4.1 of this plan | User decision. |
| A23 | No new migration; reuse `models.kind` column. | §7.1 of this plan; research-codebase §1 | Already verified at migration 030. |

---

## 13. Open Questions for the User

Each open question has a recommended default. See [`decisions.md`](./decisions.md) for the rationale + consequence of each default and what changes if overridden.

| # | Question | Recommended default |
|---|---|---|
| Q1 | **Endpoint path**: `POST /v1/decisions` (OpenAI-compatible, aggregator-normalized) — confirm. Also expose `/v1/systemone` native pass-through for native Jev clients? | **Yes; no native pass-through at v1**. |
| Q2 | **Model kind value**: `models.kind` new value `"decision"` — confirm discriminator string (NO migration needed). | **`"decision"`** (lowercase). |
| Q3 | **Image policy**: base64-only from day 1 (guide-compliant), or allow hosted URLs with proxy-side fetch-and-encode? Jev path is text-only regardless — 400 or drop? | **Posture (NOT validator check) per finding 31** — `pkg/decision/openaidecision.go` forwards data URLs as-is (no proxy-side hosted-URL fetch); `pkg/decision/typesafe.go` `BuildUpstream` rejects any `input_image` part with 400 + house OpenAI envelope (Jev is text-only). **Documented as design posture, not enforcement**: no edge validator checks the `image_url` shape; the upstream may accept base64 + reject hosted URLs (the documented guide behavior; the reference schema differs — R2). Operators are expected to follow the OpenAI guide. |
| Q4 | **Cross-format validation**: reject at proxy edge what cannot translate (choice with 1 option, score with 1 level, true+"true" collision, >255 choices, >10 levels, >128 questions) — adopt liteLLM's 400-before-upstream pattern? | **Yes, adopt liteLLM's gotcha list at proxy edge as typed predicates** (P2.6 / amendment 4). |
| Q5 | **Metering**: both providers bill input-only — map input_tokens into existing hourly usage counters; decide treatment of Jev's free output_tokens (log-only?); decision-specific counters probably unnecessary. | **Map `input_tokens` + `output_tokens` into existing writers on **success only** (chat semantics, NOT imggen's `meterFailure`); failure telemetry via `DecisionEvent`** (amendment 5). No decision-specific counters at v1. |
| Q6 | **Racing/fallback**: decisions are cheap+fast — include decision models in race/fallback chains, or carve out like imggen (no race, sync + long deadline)? | **Carve out like imggen** — no race, no fallback chain, sync + `DecisionTimeout` (default 30s, clamp `[10s, 60s]` per amendment 8). |
| Q7 | **Misroute guard**: add `IsDecision()`-style guard rejecting decision-kind models on `/v1/chat/completions` (imggen precedent at `pkg/proxy/handler.go:455`)? | **Yes — and add a sibling guard at `/v1/messages`** (Anthropic path, amendment 1, error code `decision_misroute` route-local; emitted via Anthropic envelope `models.NewAnthropicError` per finding 23), plus the reverse-direction `IsDecision()` check on the decision resolver (closes the imggen reverse-direction gap; surprise #2). |
| Q8 | **Upstream registry**: support `openai` + `typesafe` provider adapters now; keep OpenRouter/liteLLM as fallback transports? | **Add `openai` (reused; nothing added) + `typesafe` (new)** (recommended identity B per amendments 2-3); OpenRouter/liteLLM as future transports (out of v1 scope). |
| Q9 | **`DecisionTimeout` config surface** vs **package constant** (§3.7 + §8.1). **SUPERSEDED by Q14** (per approver polish round 3, item 1) — Q9 kept for stable numbering; defaults agree. | **`DecisionTimeout` config quartet** (mirrors `ImageGenTimeout`; operator-tunable). **Default 30s, clamp `[10s, 60s]`** (amendment 8; vendor "10x faster" claim is unverified marketing — tune post-deploy). |
| Q10 | **`ExcludeFromUltimateSwitching=true` for decision models** — adopt explicit enforcement (D9) given the doc/code drift in image-gen (surprise #4)? | **Yes, explicit enforcement** via D9 in `validateDecisionRules` + inline mirror. |
| Q11 | **Provider id naming**: now `openai` + `typesafe` (corrected recommended identity per amendments 2-3) vs `openai-decision` + `typesafe` (operator-identity-clarity override). | **`openai` + `typesafe`** (credential singularity + minimal registry surface; mirrors imggen/`minimax` exactly). Override keeps `openai-decision` + `typesafe` (operator identity clarity; mandatory FE display label "OpenAI Decisions"). |
| Q12 | **`pkg/providers` integration** — full `Provider` interface impls for both, or credential-validation entries only? | **Credential-validation entries only**; decision handler builds its own upstream HTTP client (mirrors imggen). |
| **Q13** | **NEW (amendment 6) — FA5 depth — keep 3 sites + parity test (recommended default) vs full single-entrypoint extraction now (~400-500 LOC; retroactively fixes image-gen's `ExcludeFromUltimateSwitching` drift as a bonus; touches stable mirrors in chat+imggen). | **Keep 3 sites + parity test** for v1; record the binding 4th-kind trigger (extract + BOTH FA1 passthrough-kernel extraction AND FA5 validation-entrypoint extraction fire together; a differently-shaped 4th kind — embeddings/audio — re-opens the analysis). |
| **Q14** | **NEW (finding 13) — `DecisionTimeout` config surface** — Option A (config quartet, committed default) vs Option B (package constant `defaultDecisionTimeout = 30 * time.Second` in `pkg/decision/handler.go`). | **Option A — config quartet** (committed per finding 13). Default 30s, clamp `[10s, 60s]`. Option B pricing: ~5 lines, no config surface, no tests; less operator flexibility per deployment. |
| **Q15** | **NEW (finding 8) — BuildUpstream error mapping** — `BuildUpstream` returns `err` → HTTP status mapping. | **502 `decision_upstream_build_failed`** on BuildUpstream errors (mirror `pkg/imggen/handler.go:316`); **400 at lifecycle step 5** for typed-validation errors (gotcha predicates). Alternatives: drop the mapping (BuildUpstream errors propagate as Go panics — bad); remap to 500 (loses upstream-credibility signal). |
| **Q16** | **NEW (finding 11) — Global ultimate `model_id` kind-gate** — should the operator-configured global ultimate `model_id` (free-text, `SettingsPage:104`) reject decision models? | **No — scope-defer (recommended)**. D9 at the model-write site catches the parallel mistake (a decision model is rejected at write if `ExcludeFromUltimateSwitching=false`). R21 records the risk; a follow-up commission trigger is set for the case where product asks. Kind-gating the free-text input now: adds friction without clear win (operator who picks a decision model has explicitly bypassed the gate). |
| **Q17** | **NEW — Credential `BaseURL` steering under identity B** — since identity B reuses the chat `openai` credential, the credential's `BaseURL` (operator-overridable; e.g. Azure-fronted OpenAI proxy or private TypeSafe deployment) would also steer decision traffic. Decide whether decision traffic honors the shared credential's `BaseURL` as-is (recommended default) or per-kind BaseURL resolution. | **Honor as-is** (recommended default): one credential = one endpoint identity. Decision adapter's `BuildUpstream` constructs upstream URL from `cred.BaseURL` (overriding the per-provider default). This is consistent with how imggen handles minimax (`pkg/imggen/handler.go:286`). Per-kind-base-URL override pricing: a future post-v1 follow-up; out of scope now. |
| **Q18** | **NEW (finding 3) — OpenAI path fidelity** — byte-faithful pass-through (capture raw request bytes, rewrite only `model`) vs typed re-marshal (parse to `DecisionRequest`, modify `Model`, re-emit bytes via `json.Marshal`). | **Byte-faithful pass-through (committed per finding 3)**. The openai-decision adapter captures the raw request bytes after parsing the `model` field via typed `DecisionRequest` (for routing/lookup), but **re-emits the original bytes** with only the `model` field rewritten. **No typed re-marshal** — unknown future OpenAI Decision fields flow through without modification. `DisallowUnknownFields` alternative explicitly NOT adopted (breaks forward-compat). Typed-re-marshal alternative pricing: `json.Marshal` of typed struct silently DROPS unknown future fields (the bug finding 3 catches); with `DisallowUnknownFields`, unknown fields REJECT — both fail forward-compat. The byte-faithful path is the only forward-compatible option. Recorded as A26 in §12.3. |
| **Q19** | **NEW (approver polish round 3, item 2) — Decision Models tab icon/label**. §6.1 (`SettingsPage.tsx` tab button construction `~:351`) notes `data-tab-id="decision_models"`, `aria-hidden`, and an emoji span `🎯 or ⚖️ — TBD by user` — this is an orphan user decision not covered by "approve all 18". | **🎯 (target/bullseye)** — recommended default. Conveys "decision" (aimed choice); mirrors the ImgGen 🖼️ (image) pattern in `SettingsPage.tsx`. **Label text** `Decision Models` (PascalCase, mirroring `ImgGen Models`). **Override** (per-house emoji set): ⚖️ (scale/balance) — also decision-appropriate but reads as "tradeoff" rather than "aimed choice"; ship as-is if operator prefers. ~5 lines (emoji + label string), `aria-label`/`aria-hidden` set per imggen pattern. |

---

## Appendix A — Files Changed (consolidated Path A)

### Source files (Backend)
- `pkg/models/config.go` — KindDecision const, IsDecision(), ValidateKindRules case + validateDecisionRules
- `pkg/proxy/handler.go` — chat-misroute guard + /v1/models exclusion
- **`pkg/proxy/handler_anthropic.go` — NEW (amendment 1): post-resolve `IsDecision()` reject at the `/v1/messages` (Anthropic) route; Anthropic error envelope via `models.NewAnthropicError`**
- `pkg/ui/server.go` — ?kind= switch + POST + PUT inline mirrors
- `cmd/main.go` — handler wiring + route registration

### New source files (Backend — `pkg/decision/`)
- `pkg/decision/doc.go`
- **`pkg/decision/types.go` — NEW (amendment 4): typed structs `DecisionRequest`, `Question`, `DecisionResponse`, `Usage{InputTokens, OutputTokens, CachedTokens, CacheWriteTokens, ReasoningTokens int64}`**
- `pkg/decision/handler.go` — entry, auth, parse-once, resolveModel w/ IsDecision(), providers, relay, success-only meter, event
- **`pkg/decision/validators.go` — NEW (amendment 4): typed gotcha predicates (pure functions on `*DecisionRequest`)**
- `pkg/decision/client.go` — sharedHTTPClient
- `pkg/decision/converter.go` — `Outcome{RelayStatus, Reason}` + `UpstreamProvider` interface (BuildUpstream, InspectResponse, ExtractUsage)
- `pkg/decision/openaidecision.go` — `openai` adapter (re-emits bytes with model rewrite)
- `pkg/decision/typesafe.go` — `typesafe` adapter (typed → System One, re-validates)
- `pkg/decision/registry.go` — local registry keyed on cred.Provider
- `pkg/decision/handler_test.go` (29+ tests + parity test stubs)
- `pkg/decision/live_e2e_test.go` (behind `//go:build live_decision`)

### Source files (Frontend)
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — TabType, button, content, toggle, destructure, imports
- `pkg/ui/frontend/src/components/config/ModelsTab.tsx` — C-16 extension
- `pkg/ui/frontend/src/components/config/DecisionModelsTab.tsx` — NEW
- `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx` — NEW (provider filter `['openai','typesafe']` per recommended identity)
- **`pkg/ui/frontend/src/components/tokens/TokenForm.tsx` — NEW (amendment 7): one-line `kind` exclusion on `:101` (allowed-models) and `:157` (per-token ultimate-model)**
- `pkg/ui/frontend/src/types.ts` — provider union + kind union + DecisionModel alias
- `pkg/ui/frontend/src/hooks/useApi.ts` — useDecisionModels + 6 triple-invalidations
- `pkg/ui/frontend/src/App.tsx` — useDecisionModels + prop threading
- `pkg/ui/frontend/src/hooks/index.ts` — barrel export: add `useDecisionModels` to re-export list

### New test files (Frontend)
- `src/hooks/__tests__/useDecisionModels.test.ts`
- **`src/hooks/__tests__/useModels.test.ts` — NEW (amendment 9d): triple-key delete assertion on every useModels mutation**
- **`src/hooks/__tests__/useImgGenModels.test.ts` extension — NEW (amendment 9d): triple-key delete assertion on every useImgGenModels mutation**
- `src/components/config/__tests__/DecisionModelsTab.test.tsx`
- `src/components/config/__tests__/DecisionModelForm.test.tsx`
- `src/components/config/__tests__/ModelsTab.chatFilter.test.tsx` (extension)
- `src/components/__tests__/SettingsPage.tabs.test.tsx` (extension)
- **`src/components/tokens/__tests__/TokenForm.test.tsx` — NEW (amendment 7): pickers exclude non-chat kinds**

### Modified test files (Backend)
- `pkg/models/__tests__/models_manager_kind_test.go` (decision variants; provider-gate `{"openai","typesafe"}`)
- `pkg/ui/__tests__/handlers_models_kind_test.go` (?kind=decision)
- `pkg/proxy/__tests__/proxy_handler_chat_misroute_test.go` (IsDecision test)
- **`pkg/proxy/__tests__/proxy_handler_anthropic_misroute_test.go` — NEW (amendment 1): decision model → 400 + Anthropic envelope**
- **`pkg/proxy/__tests__/proxy_handler_decision_models_test.go` — NEW (finding 12, symmetric coverage for `/v1/decisions`): decision model → 400 + OpenAI envelope `decision_misroute`; negatives: unknown-model → 404, chat model → 200, image-gen model → 200**
- **`pkg/ui/__tests__/kind_rules_parity_test.go` (or `pkg/models/__tests__/`) — NEW (amendment 6): POST inline ≡ PUT inline ≡ validateDecisionRules**

### Config / build / docs
- `pkg/providers/factory.go` — **1 new const** (`ProviderTypesafe = "typesafe"`); `openai` already exists (recommended identity B). Switch arm + IsProviderSupported + providerMetadataMap entries
- `pkg/models/credential.go` — `validProviders += "typesafe"`
- `pkg/config/config.go` — `DecisionTimeout` quartet (default 30s, clamp `[10s, 60s]` per amendment 8)
- `Makefile` — import-guard grep clause
- `config/models-template.json` — 2 decision example entries (documentation-only)
- `pkg/ui/static/**` — regenerated FE bundles (SAME commit as any index.html change)

### NOT changed (per Path A)
- NO new migration files
- NO migration registry append
- NO querybuilder changes
- NO store scan / binding / count changes
- NO sqlc output (stale dead code)
- NO pkg/imggen changes (closes its reverse-direction gap as a future follow-up; not required for v1)
- NO pkg/ultimatemodel changes
- NO pkg/proxy/translator changes

### Estimated diff size
- ~25 source-file sites (~500 lines added) + **~21 new files** (~1900 lines added incl. tests) = ~2400 lines added
- ~0 lines deleted
- ~7 new test files (~700 lines)
- ~3 documentation lines (CHANGELOG + blueprint + models-template)

**Manifest completeness rule (review round 2, recommendation b)**: "every file named in the plan body appears in the manifest." Adopted uniformly. **Manifest file count (post-round-2): 21 NEW + 12 MODIFIED = 33 entries in § A's change manifest** (verified in this pass — `pkg/ui/frontend/src/hooks/index.ts` and `pkg/proxy/__tests__/proxy_handler_decision_models_test.go` were missing per blockers 4 + 6; now present).

---

## Appendix B — Amendment Log (Architect Round 1)

Per `architecture-recommendation.md` §9, 9 amendments were applied in place. The plan survives competitive challenge on its core choices (topology, zero-migration, metering reuse, guard design, FE posture); these amendments are scoped corrections + structural upgrades.

| # | Severity | What changed | Why |
|---|---|---|---|
| 1 | 🔴 | Added `/v1/messages` (Anthropic) post-resolve `IsDecision()` reject at `pkg/proxy/handler_anthropic.go:~155-183` (Anthropic envelope via `models.NewAnthropicError`). Touches: §3.1 site list (new site 8), §3.4 (expanded to 4-entry-point matrix), P1.3 acceptance, §9.2 test table (new `proxy_handler_anthropic_misroute_test.go`), Appendix A (new file + new source site), R16. | The Anthropic route had **zero** misroute checks today (verified: `grep -nE 'IsImageGen\|IsDecision' pkg/proxy/handler_anthropic.go` → 0 hits). A decision model POSTed there would execute a real chat upstream call. Pre-existing image-gen gap on the same route is an optional backport (out of v1 scope). |
| 2 | 🔴 | Corrected the false provider-collision rationale in §5.1 + decisions.md Q11. Touches: §2.4 (open-question note), §3.1 (D4 + provider id note), §5.1 (rewrote rationale + added §5.1.a reuse-pattern precedent), §5.2 (rewrote table), §5.5 (rewrote paragraph), §5.6 (allowlist + sketch), §8.4 (types.ts union), §12.3 A21, §13 Q11, R15. | Verified in source: chat dispatch is unreachable from decision routes; imggen/`minimax` proves the reuse pattern at scale (one year, zero collision). The only thing that would reject a decision model with `internal_provider="openai"` is our own `validateDecisionRules` allowlist — a design choice. |
| 3 | 🟡 | Changed Q11 recommended default to **reuse `openai` + add `typesafe` only**. Touches: §5.1 (rationale rewrite + A vs B table), §5.2 (rewrote table; one new entry vs two), §5.6 (allowlist `{"openai","typesafe"}`), §6.2/§6.6 (form filter `["openai","typesafe"]`), §8.2 (models-template), §13 Q5/Q7/Q8/Q11 (text), Appendix A (factory.go says 1 new const). | Credential singularity — one OpenAI credential row serves chat and decisions (matches OpenAI's own model: one key, all endpoints). Mirrors imggen/`minimax` exactly. Override A keeps `openai-decision` + `typesafe` (mandatory FE display label "OpenAI Decisions"). |
| 4 | 🟡 | Replaced body-map seam with hybrid typed seam. Touches: §3.2 (file table now lists `types.go` + `validators.go`; lifecycle adds `parseBody` step 4 + gotcha-validators step 5 + re-validated `BuildUpstream` step 10), §4.2 (rewrite of both adapters as typed struct ↔ struct), P2.2/P2.3/P2.6 (rewording), Appendix A (new `types.go` + `validators.go`). Removed all `map[string]interface{}` key-walking prose. | The edge must parse the client wire anyway; liteLLM's reference bridge is struct-based. Typed predicates make validators and translations testable; `typesafe.BuildUpstream` re-validates (defense-in-depth). Typed `Usage` struct replaces `BillableUsage` rename (different semantic). |
| 5 | 🟡 | Changed failure metering to success-only. Touches: §7.2 (rewrote "Failure metering policy" + added §7.2.a "Attribution after the fact"), P2.4 acceptance (rewording), §13 Q5, R19. | Chat (the other `Increment` user) is success-only; imggen's `meterFailure` bills `request_count=1` on upstream errors. Mirroring it would make decision the only token-billed route that counts failures, mixing a third semantic into FE totals. Failure telemetry flows via `DecisionEvent`. |
| 6 | 🟡 | Added decision-rule parity test (POST inline ≡ PUT inline ≡ `validateDecisionRules`). Touches: §9.2 (new test row), P1.9 (new task), §13 (new Q13), R18, Appendix A (new test file). | The 3 enforcement sites are empirically in sync today (verified for image-gen); a parity test locks them mechanically rather than by hope. Records the **binding 4th-kind trigger**: if a 4th model kind is commissioned, extract a single `ValidateAndMapToHouseError` entrypoint and rewrite the mirrors in that commission (BOTH the FA1 passthrough-kernel extraction AND the FA5 validation-entrypoint extraction fire together; a differently-shaped 4th kind — embeddings/audio — re-opens the analysis). Full extraction now is the priced alternative (~400-500 LOC; retroactively fixes image-gen's `ExcludeFromUltimateSwitching` drift). |
| 7 | 🟡 | Added TokenForm picker filters + ultimate-target note. Touches: §6 (new §6.7.a sub-section), P3.8 (new task), §6.9 (new test row), Appendix A (new TokenForm source + test sites), R19. | `TokenForm.tsx:101` (allowed-models picker) and `:157` (per-token ultimate-model `<select>`) are unfiltered today (pre-existing image-gen quirk). One-line `kind` exclusion each, mirroring C-16. Global ultimate `model_id` (free-text, `SettingsPage:104`) is NOT kind-gated — accepted for v1 (document in blueprint; revisit if the bypass causes confusion). |
| 8 | 🟢 | Tightened `DecisionTimeout` to default 30s, clamp `[10s, 60s]`. Touches: §3.7, §8.1, §13 Q9, R20. | The vendor "10x faster than Responses API" claim is unverified marketing. 60s default would mask slow upstreams; live e2e measures latency; tune on real data. |
| 9 | 🟢 | Plan notes incorporated. Touches: §3.4 (imggen benign pre-flight 502 row), §6.4 (FE invalidation helper MUST use explicit key list `['models','imggen-models','decision-models']` — NEVER `deleteByPrefix`), §6.4.a (FE mutation tests assert triple-key deletion on all hooks — NEW sub-section), §6.9 (new test rows for `useModels.test.ts` + `useImgGenModels.test.ts` extension), §13 Q13 (binding 4th-kind trigger covers BOTH FA1 and FA5 extractions). | (9a) decision-model-on-`/v1/image_generation` is benign: minimax-only registry → pre-flight 502, no upstream call, no metering. (9b) `deleteByPrefix` matches none of the actual keys and misses in-flight-only promise entries. (9c) 4th-kind trigger covers both extractions together (with the caveat that a differently-shaped 4th kind re-opens the analysis). (9d) FE analog of the parity test: extend existing hook tests to assert all three keys deleted on every mutation. |

**Topology + zero-migration + metering reuse + guard design + FE posture** all survive unchanged per architect §10 (No-Change Endorsements). Phasing P1→P4 stays valid; amendments 1/6 slot into P1 (now 9 tasks), amendments 3/4/5 into P2, amendment 7 into P3 (now 8 tasks), amendment 8 into §3.7/§8.1.

---

## Appendix B.2 — Review Round 1 (Approved-With-Notes)

33 findings applied in ONE consolidated pass (per reviewer report). The plan's reviewer-survived overall was APPROVED-WITH-NOTES — no core rejection, only scoped corrections. Findings are grouped by material change; the "nits" class (typos, formatting, doc-quality) is summarized in a single row.

| # | Sev | What changed | Why |
|---|---|---|---|
| **R1.1** | 🔴 | **Critical — chat-arm leak fixed.** `pkg/ui/server.go:758-770` chat arm extended from `!m.IsImageGen()` to `!m.IsImageGen() && !m.IsDecision()`; P1.4 acceptance widened to "both ?kind=chat and ?kind=decision return only the requested kind"; §9.2 test rows added (chat-excludes-decision, image-gen-excludes-decision, chat-excludes-image-gen regression lock, default-arm warning wording for 3 kinds). | Verified `pkg/ui/server.go:758-770` in source. The current chat arm was `!m.IsImageGen()` only — adding `KindDecision` without this extension would leak decision models into `GET /fe/api/models?kind=chat` for any non-UI direct API consumer. FE C-16 covers the tab UI only, not direct API consumers. |
| **R1.2** | 🟡 | Test re-spec: `TestHandler_Meter_Failure_ZeroTokens` → `TestHandler_Meter_NoUsageRow` (renamed; spec changed from "Increment called with 0 tokens" → "Increment/IncrementModelUsage NOT called on failure; failure surfaces via DecisionEvent only"). | The previous test spec locked in rejected imggen `meterFailure` semantics (failure-billed `request_count=1`), which amendment 5 explicitly rejected in favor of chat-style success-only. The new spec aligns with the success-only decision. |
| **R1.3** | 🟡 | **OpenAI path fidelity committed to byte-faithful pass-through** (recorded as Q18 + A26). `openaidecision.go` captures raw request bytes; rewrites only the `model` field via string substitution; never re-marshals through typed structs. §3.2 step 10 + §4.2 rewritten. Test `TestHandler_OpenAI_PathFidelity_BytePassThrough` added (sends a future field; verifies verbatim transport). | §3.2 said "re-emit client wire bytes" but §4.2 used typed `json.Marshal` — unknown future OpenAI fields would be silently dropped where fidelity matters most. Byte-faithful is the only forward-compatible option (A26). |
| **R1.4** | 🟢 | P1.7 acceptance corrected: "**`go build ./...` succeeds; do NOT run `make build`** (auto-increments VERSION — R12)" — was "make build succeeds (DO NOT RUN IT)". | "make build succeeds" is un-executable when the implementer must not run `make build`. Corrected to the executable verification command. |
| **R1.5** | 🟡 | **Unified 2-arg `InspectResponse(respBytes, originalReq)` signature** for the `UpstreamProvider` interface. `Openai-decision` captures the original request at `BuildUpstream` time so both adapters satisfy the unified interface. §3.2 step 14 + §4.2 + `converter.go` row in §3.2 file table all updated. | The 1-arg vs 2-arg signatures were inconsistent. A unified 2-arg signature lets `openai-decision` and `typesafe` satisfy the same interface (typesafe needs `originalReq` for answer-order preservation; openai doesn't, but capturing it once is cheap). |
| **R1.6** | 🟢 | `useDecisionModels` added to barrel export `src/hooks/index.ts:1`. Verified: `index.ts` currently re-exports `useRequests, useRequestDetail, useConfig, useModels, useImgGenModels, formatDuration, useTokens, useAppTags`; the new hook must be added. §6.3 + Appendix A updated. | `App.tsx:6` imports from `'./hooks'`; without the barrel export the new hook is unreachable from `App.tsx`. |
| **R1.7** | 🟢 | §3.2 step 2 body-cap arithmetic corrected: "16MB sufficient only at ~128KB avg base64 image (128 × 128KB ≈ 16MB); at 1MB avg the cap fails at ~16 images. **413-on-large-batches is designed behavior**." | The previous "16MB sufficient at ≤1MB avg image" was arithmetically unsound (128×1MB=128MB ≫ 16MB). |
| **R1.8** | 🟡 | **BuildUpstream error → 502 `decision_upstream_build_failed`** (mirror `pkg/imggen/handler.go:316`); typed-validation errors → 400 at step 5. Recorded as Q15. Test `TestHandler_BuildUpstream_Error_502_DecisionUpstreamBuildFailed` added. | BuildUpstream `err` was previously unmapped to any HTTP status — a code path that could fail in production with no clear status code. |
| **R1.9** | 🟡 | Duplicate `Question.Name` (System One map-key) added to typed validators. Test `TestHandler_TypeSafe_DuplicateQuestionName_400` added. | Boolean collision was covered but duplicate names were not — System One maps `name → key`, so duplicates collide silently. |
| **R1.10** | 🟢 | §6.7.a clarified with explicit filter shape: "compute `const chatModels = models.filter((m) => !m.kind \|\| m.kind === 'chat');` above the `<ModelMultiSelect>` invocation; pass `models={chatModels}` instead of `models={models}`". | The previous "filter `models`" was ambiguous about what the filter produces. |
| **R1.11** | 🟡 | **R21 + Q16 added**: global ultimate `model_id` free-text (`SettingsPage:104`) is NOT kind-gated; promoted to explicit risk row R21 tagged scope-deferral with follow-up commission trigger; recorded as Q16. | The free-text global ultimate `model_id` is a UX-level concern; an operator can name a decision model as the ultimate target. D9 at write-site catches the parallel mistake but does NOT gate the ultimate target's kind. |
| **R1.12** | 🟢 | `/v1/messages` guard test rewritten: "**Post-resolve** `IsDecision()` reject (asserts the reject happens AFTER model resolution at `:155-183` and BEFORE auth/upstream call at `:439`)" + **negatives** added (unknown-model → 404; chat model posted → succeeds; image-gen model → benign pre-flight 502). Symmetric coverage added for `/v1/decisions` (`proxy_handler_decision_models_test.go`). | The test previously had no post-resolve wording (the actual rejection point matters) and no negative cases. |
| **R1.13** | 🟢 | §3.7 + §8.1 + Q9 **committed to Option A (config quartet)** consistently; Option B recorded as Q14 with pricing. | Un-committed Option A/B across sections — was inconsistent. |
| **R1.14** | 🟢 | Test `TestHandler_PerProviderDefaultBaseURL_Stamping` added: `internal_provider='openai'` → `internal_base_url='https://api.openai.com'`; `internal_provider='typesafe'` → `internal_base_url='https://api.typesafe.ai'`. | Per-provider DEFAULT_BASE_URL stamping was untested. |
| **R1.15** | 🟢 | R6 reworded: "**CORRECTED**: imggen's `sharedHTTPClient` is single-attempt with no retry / 429 handling — the previous plan text was wrong. Mitigation: decision uses the same single-attempt posture; upstream status + body surface to the client verbatim; **Retry / rate-limiting is a follow-up commission**." | The previous R6 claimed "429 + retry-after handling in `sharedHTTPClient.Do`" — verified false via `pkg/imggen/client.go:26-39`. |
| **R1.16** | 🟢 | P3 task count "(7 tasks)" → "(8 tasks)"; P2 exit criterion now also carries "(8 tasks)" suffix; P4 exit criterion gets "(5 tasks)". All cross-references P1=9/P2=8/P3=8/P4=5 consistent. | Task count sweep across per-phase headings, summary table, exit-criteria lines. |
| **R1.17** | 🟢 | §6.4 arithmetic corrected: "**6 existing mutation sites + 3 in the new hook = 9 sites; each mutation deletes all 3 keys**" (was garbled "9+1=9"). | Finding 17 — the previous arithmetic was wrong. |
| **R1.18** | 🟢 | §7.2 stale step refs corrected: "(step 15)" → step 17 (event publish); "(step 5)" → step 17. Verified against current §3.2 lifecycle numbering (after the §3.2 renumber). | After the lifecycle renumber (steps 4 parseBody + 5 gotcha validators inserted), downstream sections still cited old ones. |
| **R1.19** | 🟡 | **§3.2.a added**: file-creation → phase mapping table (`types.go`→P2.2, `validators.go`→P2.3/P2.6, `openaidecision.go`→P2.2, `typesafe.go`→P2.3, `handler_test.go`→P2.7, `live_e2e_test.go`→P4.2). | No file-creation→phase mapping existed before; readers had to cross-reference P-tasks and Appendix A. |
| **R1.20** | 🟢 | §12.2 assumption rows **A24 + A25** added (score-as-int mapping; usage zero-fill); §12.3 **A26** added (openai byte-faithful posture per finding 3). | §4.2 dangling "flagged in §12" pointers — explicit assumption rows with verification triggers. |
| **R1.21** | 🟢 | §3.2 step 17 corrected: "emit a `DecisionEvent` — **string-typed event (sibling of `eventTypeImageGeneration = "image_generation"` at `pkg/imggen/handler.go:62`)**, with `map[string]interface{}` payload (sibling of `pkg/imggen/handler.go:612-622`)" — was "emit `DecisionEvent` (new event type, sibling to `ImageGenEvent`)". | The phantom `ImageGenEvent` type was invented; the actual constant is `eventTypeImageGeneration` and the payload is `map[string]interface{}`. |
| **R1.22** | 🟢 | `BUNDLE_NOT_IMPLEMENTED` (invented sentinel) → `fmt.Errorf("… not yet implemented")` per `pkg/providers/factory.go:27-32` house style. Touches §5.2, §5.5, decisions.md Q12. | House style for unimplemented providers. |
| **R1.23** | 🟢 | Anthropic-route misroute error code string named: **`decision_misroute`** (route-local; same code string as the chat-side guard but routed through the Anthropic envelope). Touches §3.1 site 8 + §3.4 + §13 Q7 row. | The Anthropic-envelope misroute row in §3.4 didn't name the code string. |
| **R1.24** | 🟢 | `safety_identifier` ≤128 chars enforcement added: `validateSafetyIdentifier` predicate in `validators.go` returns 400 + house OpenAI envelope. Test `TestHandler_SafetyIdentifier_Over128Chars_400` added. | §4.1 said "≤128 chars" but didn't enforce it. |
| **R1.25** | 🟢 | Naming consistency: "Jev/TypeSafe" → "TypeSafe (Jev)"; "TypeSafe/Jev" → "TypeSafe (Jev)". Narrative standardized across §1, §3.2 file table, §10 P4.5, §12.2, decisions.md Q3. | Mixed narrative across the plan; "TypeSafe (Jev)" is the standard parenthetical form. |
| **R1.26** | 🟢 | §7.3 added token-level kind attribution paragraph: cheapest is a **FE-only join** (`token_hourly_usage.token_id` → `tokens.id` → `tokens.allowed_models` → for each, `models.kind='decision'`); **per-token `kind` column** is a true schema change, out of v1 scope. | Token-level usage kind-blindness lacked an escape hatch escape path; reviewers couldn't tell whether the design had one. |
| **R1.27** | 🟢 | §2.1: "the 3 inline mirrors" → "the 2 inline mirrors (POST `:869-947` + PUT `:1080-1143`)". | The plan claimed "3 inline mirrors" but there are 2 (POST + PUT). |
| **R1.28** | 🟢 | Appendix A diff size: "~11 new files" → "~16 new files" (recounted: 11 BE + 2 FE components + 3 FE tests = 16). | The manifest's own count was 16; the prose summary said 11. |
| **R1.29** | 🟡 | **§11.1 added**: zero-decision-deployment + rollback paragraph (route always-on but inert when no decision models saved; rollback = branch discard; DDL-free rollback except for `kind='decision'` rows in the `kind` column). | The plan had no rollback note. |
| **R1.30** | 🟢 | §9.2 test rows added: `/v1/models` decision-exclusion (`TestHandler_ChatArmExcludesDecision` + `TestHandler_ImageGenArmExcludesDecision`) + decision-kind persistence restart (`TestHandler_DecisionKind_PersistenceRestart_RoundTrip`). | Migration-030 kind persistence needs a restart round-trip test. |
| **R1.31** | 🟢 | Q3 + decisions.md Q3 clarified: **"Posture (NOT validator check)"** — no edge validator checks the `image_url` shape; the upstream is the authoritative source. `pkg/decision/openaidecision.go` forwards data URLs as-is; `pkg/decision/typesafe.go` rejects `input_image` parts. | Q3 wording read as proxy-enforced base64 check; the design is forward-as-is, not enforcement. |
| **R1.32** | 🟢 | Doc-quality sweep (the "nits" class): (a) §5.6:417 "not nor" typo → "not interchangeable"; (b) §9.2:713 "the FE analog" wording → reference §6.4.a by name; (c) §3.4 cross-refs verified; (d) task counts/step numbers swept under R1.16 + R1.18. | The pre-logged nits from the reviewer summary. |
| **R1.33** | 🟢 | **Scope-expansion acknowledgment** (per finding 33): TokenForm filters (§6.7.a / P3.8) also fix a **pre-existing image-gen picker leak** (image-gen models were leaking into `TokenForm.tsx:101` allowed-models picker and `:157` per-token ultimate-model `<select>` today). This is a deliberate, priced, tested scope expansion — no other changes needed. | The reviewer flagged this as an intentional scope expansion; one-line acknowledgment is sufficient. |
| **R1.34** | 🟢 | **§13 + decisions.md extended with Q14–Q18** (per the review-round brief): Q14 (timeout Option A committed; Option B pricing), Q15 (BuildUpstream error mapping → 502 / 400), Q16 (global ultimate kind-gate → scope-defer R21), Q17 (credential BaseURL steering → honor as-is), Q18 (openai path fidelity → byte-faithful pass-through). Q1-Q13 unchanged (no renumbering). decisions.md header + footer updated to reference Q1-Q18 + Review Round 1 provenance. | The brief required adding Q14-Q18 (product-call decisions from the round-1 review); Q1-Q13 kept stable. |

**Topology + zero-migration + metering reuse + guard design + FE posture** all survive unchanged. Phasing P1→P4 stays valid (now P1=9, P2=8, P3=8, P4=5).

---

## Appendix B.3 — Review Round 2 (mechanical re-fixes)

Mechanical fix round (per reviewer report). Scope: **6 blockers + 2 sanctioned process recommendations + provenance honesty**. No rewording, no new content, no design changes. **decisions.md received zero changes this pass.**

| # | Sev | What changed (post-fix line numbers) | Why |
|---|---|---|---|
| B1 | 🔴 | **P2.2 acceptance (line 801)** rewritten: "`BuildUpstream` re-emits typed `DecisionRequest` as bytes with `model` rewritten only" → "**emits the captured raw request bytes with `model` rewritten only (byte-faithful per Q18)**; `InspectResponse` returns typed `*DecisionResponse`". | The previous wording contradicted the committed Q18 default (§4.2:313, §3.2 step 10, Q18:952, A26:913). An implementer executing the phase acceptance would build the rejected typed re-marshal. |
| B2 | 🟢 | **§7.2:609** — "(per §3.2 step 15)" → "(per §3.2 step 17)" | §3.2 step 15 is "Relay write", not "DecisionEvent published to events.Bus". The event publish is step 17 (verified against §3.2 lifecycle table). R1.18's prior sweep missed this survivor. |
| B3 | 🟢 | **P2.4:803** — "Failure telemetry via `DecisionEvent` (step 5)" → "(step 17)" | Step 5 is "gotcha validators → 400", NOT event publish. Wrong step number AND wrong semantics (step 5 is for typed-validation; event publish is step 17). R1.18's prior sweep missed this survivor. |
| B4 | 🟢 | **Appendix A "Source files (Frontend)" (line 987)** — added row: `- pkg/ui/frontend/src/hooks/index.ts — barrel export: add useDecisionModels to re-export list`. | The R1.6 audit row claimed this landed; it did not. Without the barrel export, `App.tsx:6`'s `import from './hooks'` cannot reach `useDecisionModels`. Verified `src/hooks/index.ts` exists and currently re-exports 8 hooks — `useDecisionModels` must be added. |
| B5 | 🟢 | **Appendix A "Estimated diff size" (line 1024-1027)** — "~16 new files" → "~21 new files". Plus manifest completeness rule adopted: "every file named in the plan body appears in the manifest". | R1.28's recount at "~16" predated amendments 1/6/7/9d + finding 12, which added ~5 more NEW files. Recount: 9 BE production + 2 BE tests (from §3.2.a) + 3 BE additional tests (anthropic_misroute, decision_models, kind_rules_parity) + 2 FE components + 5 FE tests = 21. |
| B6 | 🟢 | **Appendix A "Modified test files (Backend)" (line 1003)** — added row: `- pkg/proxy/__tests__/proxy_handler_decision_models_test.go — NEW (finding 12, symmetric coverage for /v1/decisions): decision model → 400 + OpenAI envelope decision_misroute; negatives: unknown-model → 404, chat model → 200, image-gen model → 200`. | Cited at §9.2:739 + R1.12:1068 but absent from the manifest. The manifest completeness rule (B5) catches the omission. |
| **B7** | 🟢 | **Process (a) — Step-N cross-reference re-sweep.** Verified all 13 step references in plan.md against the current §3.2 lifecycle table (1=Method guard, 2=Body cap, 3=Auth, 4=parseBody, 5=Gotcha validators, 6=resolveModel, 7=Allow-list, 8=resolveCredential, 9=Provider lookup, 10=BuildUpstream, 11=Timeout ctx, 12=sharedHTTPClient.Do, 13=Response cap, 14=InspectResponse, 15=Relay write, 16=Meter, 17=Event publish). **13 refs checked; 2 wrong (B2 + B3) — both already corrected above**; 11 correct (lines 174 step 10 ✓, 179 step 5 ✓, 187 step 17 ✓, 224 step 6 ✓, 313 lifecycle step 4 ✓, 870 step 6 ✓, 871 step 9 ✓, 913 step 10 ✓, 949 step 5 ✓, 1059 step 10 ✓, 1061 step 14 ✓, 1063 step 2 ✓, 1064 step 5 ✓, 1077 step 17 ✓). R1.18's prior sweep missed the 2 survivors — this redo is mechanical and complete. | R1.18's audit row proved unreliable (B2 + B3 survivors). Per recommendation (a): do not trust the prior sweep; redo mechanically. |
| **B8** | 🟢 | **Process (b) — Manifest completeness rule adopted:** "every file named in the plan body appears in the manifest." Applied here; with B4 + B6 added, the manifest now satisfies the rule. Two new files found missing: `pkg/ui/frontend/src/hooks/index.ts` (B4) and `pkg/proxy/__tests__/proxy_handler_decision_models_test.go` (B6). No other gaps. | The manifest is the source of truth for the implementer; a missing row = a missed file = a silent miss. |

### Provenance honesty — superseded rows

The following **B.2 (Review Round 1) rows overstated what landed** and are superseded by this B.3 entry. Per the brief: append-only audit; the B.2 rows are NOT edited in place.

- **B.2 R1.6** (audit row claiming `useDecisionModels` was added to barrel export in `src/hooks/index.ts:1`) — **did not land in the manifest**. Superseded by B4.
- **B.2 R1.18** (audit row claiming "(step 15) → step 17" + "(step 5) → step 17" sweep) — **incomplete**: line 609 (B2) + line 803 (B3) survivors not caught. Superseded by B7.
- **B.2 R1.28** (audit row claiming "~16 new files" is the final count) — **stale**: amendments 1/6/7/9d + finding 12 added ~5 more NEW files after R1.28 was written. Superseded by B5.

### Items NOT touched this round (noted in report only)

The following items were visible during the sweep but explicitly out of scope per the brief ("no rewording, no new content, no design changes, no extra cleanup"):

- `useImgGenModels.test.ts extension` row in the New test files (Frontend) manifest (line 992) is labeled "NEW (amendment 9d)" but is actually a **MODIFIED** file (pre-existing per FE tests research). Wording is mildly inconsistent; **not fixed** (would be a rewording).
- `SettingsPage.tabs.test.tsx` (line 121 of the plan body) is referenced in §6.9 but the manifest's "Modified test files (Frontend)" section is not separately labelled — the existing row mentions it inline. **Not restructured** (would be a manifest re-organization).
- §3.2 step 1 reference to `pkg/imggen/handler.go:158-168` (Method guard) is a 3-line reference; lifecycle step numbering vs the actual source comments in imggen handler.go was not cross-checked. **Not cross-checked** (would be a new check).

---

## Appendix B.4 — Approver polish (round 3)

Final pre-delivery pass. Scope: 7 approver polish items, EXACTLY these, nothing beyond. All gates cleared. decisions.md header + footer + Q9 + Q19 updated; no other decisions.md changes.

| # | Item | Post-fix line numbers | Why |
|---|---|---|---|
| **B.4.1** | **Q9 ↔ Q14 supersede** — decisions.md: Q9 header now reads "Superseded by Q14 (per approver polish round 3, item 1). Q14 is the canonical home for this question; defaults agree (Option A config quartet / 30s / `[10s, 60s]`). Q9 retained here for stable numbering." Historical content preserved; plan §13 Q9 row: "**SUPERSEDED by Q14** (per approver polish round 3, item 1)". **Resolution chosen**: supersede-with-note (NOT merge) — preserves stable numbering across all earlier round-1/round-2/round-3 references to Q9/Q14. | plan §13 line 943 (Q9 row); decisions.md line 145 (Q9 section). | Q9 and Q14 both address `DecisionTimeout` config surface with identical defaults. Supersede-with-note is the lower-friction option: no historical cross-references break (R1.7, R1.13, R1.34 all reference Q9 by name; those references remain valid). Merge would have required rewriting 5+ historical cross-references to Q14. |
| **B.4.2** | **Q19 added** — Decision Models tab icon/label (was `TBD by user` at §6.1 line 490). §6.1 emoji note now: "an emoji span (`🎯` per Q19 default; see decisions.md Q19 for the override option)". plan §13 Q19 row added (line 955); decisions.md Q19 section added (line 309). **Recommended default**: 🎯 (target/bullseye); **Override**: ⚖️ (scale/balance). | plan §6.1 line 490; plan §13 line 955 (Q19 row); decisions.md line 309 (Q19 section); decisions.md header + footer (lines ~7, 313-315). | §6.1 had an orphan user decision `🎯 or ⚖️ — TBD by user` not covered by "approve all 18". Adding Q19 makes it actionable. Mirrors the ImgGen tab emoji pattern (🖼️). |
| **B.4.3** | **P1 exit criterion: `typesafe` clause removed** (line 794). P1 now says "provider=`openai` only at P1 — the `typesafe` validProviders gate lands in P2.1". P2 exit criterion owns the `typesafe` clause: line 809 now reads "provider `openai` (reused) **or** `typesafe` (new — validProviders entry is added in P2.1)". | plan §10 line 794 (P1 exit criterion); plan §10 line 809 (P2 exit criterion). | The original P1 exit criterion over-promised: the `typesafe` validProviders gate lands in P2.1 (factory.go + validProviders), not P1. Moving the clause to the phase that actually delivers it makes the exit gates honest. |
| **B.4.4** | **§9.1 /fe/api/models tests → §9.2 canonical home**. The 4 tests (`TestHandler_ChatArmExcludesDecision`, `…_ImageGenArmExcludesDecision`, `…_ChatArmExcludesImageGen`, `…_DefaultArm_UnknownKind_LogsWarning_ThreeKinds`) are removed from the §9.1 row list and replaced by a note ("listed here for visibility, canonical home is §9.2"). The §9.2 `handlers_models_kind_test.go` row gains the ownership note: "**(§9.2 canonical home for `?kind=` switch tests)**" + "**Owns the four `/fe/api/models` tests**". | plan §9.1 line ~722 (note added); plan §9.2 line ~735 (canonical-home note added). | The four tests are about `/fe/api/models` HTTP routing + switch logic, which lives in `pkg/ui/server.go:758-770` — not in `pkg/decision/handler.go`. Canonical test file = `handlers_models_kind_test.go` (§9.2). §9.1 list now lists only `pkg/decision/handler_test.go`-canonical tests. |
| **B.4.5** | **§11.1 rollback aside: feature-flagged suggestion removed** (line ~876). Now reads: "**Mitigation for an active deployment**: before rolling back the branch, an admin should DELETE all `kind='decision'` rows (a small one-time cost). This is the **stated rollback mechanism** — consistent with §8.3's no-env-flag posture (always-on; no production-rollback feature flag is added or proposed; the feature-flagged-state suggestion is removed as contradictory to the always-on posture, per approver polish round 3 item 5)." | plan §11.1 line ~876 (rollback path paragraph). | The previous wording ended with "Alternatively, treat the branch as additive-only and ship the FE tab in a feature-flagged state for any production rollback concern" — this contradicted §8.3's "no env flag (mirroring imggen posture)". Per the always-on posture, the DELETE-rows mechanism is the only rollback path; feature flags are not added. |
| **B.4.6** | **§3.1 site 5: TWO parallel guard branches** (line 126), not a boolean disjunction. Rewritten: "`TWO parallel guard branches (not a boolean disjunction) so error codes stay distinct: `if rc.resolvedModel.IsImageGen() { … 400 OpenAI envelope, code "image_gen_misroute" }` **plus** `if rc.resolvedModel.IsDecision() { … 400 OpenAI envelope, code "decision_misroute" }` — same site, two separate `if` branches`. Reverse-direction error code (decision resolver, §3.2 step 6): same `decision_misroute` code string (cross-route canonical; the code string identifies the misroute class, not the route)." | plan §3.1 line 126 (site 5 row). | The previous `add || rc.resolvedModel.IsDecision()` (boolean disjunction) risked the `image_gen_misroute` and `decision_misroute` codes collapsing if both checks shared a branch. Two parallel `if` blocks keep the codes independent AND name the reverse-direction code explicitly. |
| **B.4.7** | **Follow-up commission triggers (a + b) made explicit**. (a) §7.3 added: "**Follow-up Commission Trigger (item 7a, approver polish round 3)**: trigger condition = product asks 'how much decision vs chat traffic did token X consume?'. When fired: open a small commission to add `JwtTokensKind` (`per-token kind preference`) and backfill `tokens.kind` from `tokens.allowed_models` UDF at write-time. Priced ~150 LOC + migration 033 + 3 coupled sites; v1 ships without." (b) §6.7.a added: "**Follow-up Commission Trigger (item 7b, approver polish round 3)**: trigger condition = product asks 'why is this decision model being targeted for ultimate switching?'. When fired: open a small commission to make `SettingsPage:104`'s global ultimate `model_id` field a `<select>` of chat-kind models only, or to add a kind-gate validator at write-time. Priced ~30-150 LOC depending on UX choice." | plan §7.3 (token-level attribution); plan §6.7.a (global-ultimate kind-gate). | The plan tracked these follow-ups informally (R21 + Q16 + §7.3 sentence) but did not state **explicit trigger conditions + priced commissions**. This row makes both trigger conditions + priced commissions actionable: a future maintainer can read either section and know exactly when to fire the commission + what the budget is. |

**Topology + zero-migration + metering reuse + guard design + FE posture** all survive unchanged. **Phasing P1→P4** stays valid (now P1=9, P2=8, P3=8, P4=5). **decisions.md** received: Q9 supersede header (line 145); Q19 NEW section (line 309); header amendment history (line ~7); footer counts (lines 313-315). Q1-Q19 numbering: Q1-Q19 stable (Q9 preserved as superseded-with-note rather than deleted; Q19 added; no other renumbering).