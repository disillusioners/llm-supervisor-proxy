# Codebase Research — `decision` Model Kind (research-codebase.md)

**Date:** 2026-10-15 (UTC) · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-model-type`
**Base:** branch `feature/decision-model-type` @ `b2660a6cfb8c916a03ec49def89c8053e697788c` (merge of feature/imggen-models into latest)
**Method:** 5 delegated read-only worker investigations (kind system; routing+providers; imggen+usage; frontend; config+testing) + wanderer spot-checks of every pivotal anchor against source. Zero source files modified; this file is the only write.
**Companion doc:** web research on the OpenAI Decision API + Jev was done separately (sibling wanderer instance `wanderer-web-decision`, completed) — this file covers ONLY the local machinery.

Confidence key: 🟢 confirmed (read + cross-checked) · 🟡 likely (single source, plausible) · 🔴 uncertain.

---

## 1. Model kind system

### Kind values & validators — `pkg/models/config.go`
- Constants `pkg/models/config.go:90-101`: `KindChat = "chat"` (:93), `KindImageGen = "image-gen"` (:100). Helper `IsImageGen()` at :105-107 (`return m != nil && m.Kind == KindImageGen`). 🟢
- `ModelConfig.Kind string` field with `json:"kind,omitempty"` at :264 (comment block :249-263). Chat models serialize kind-free — the discriminator only materializes for non-chat kinds. 🟢
- `ValidateKindRules(m *ModelConfig, internalProvider string)` at **:141-152**. Switch: `case "", KindChat → nil`; `case KindImageGen → validateImageGenRules(...)`; **default rejects unknown kinds** (:151) — so `kind:"decision"` is rejected today until a case is added. 🟢
- `validateImageGenRules` at **:157-185** enforces, in order: `Internal==true` (:159), `InternalModel != ""` (:162), `len(Credentials) > 0` (:165), primary provider `== "minimax"` when `internalProvider != ""` (:172 — JSON-file path passes ""), `FallbackChain` empty (:175), `SecondaryUpstreamModel == ""` (:178), `PeakHourEnabled == false` (:181), `ReleaseStreamChunkDeadline == 0` (:184). 🟢 (verified verbatim)
- **Three enforcement sites** for kind rules: (1) `ModelsConfig.Validate` JSON-file path — call at `pkg/models/config.go:829`; (2) DB write path — `validateModelAgainstCredentials` tail at `pkg/store/database/store.go:1696-1708` calling `models.ValidateKindRules`, reached via `validateBeforeWrite` (:1725-1732) from `AddModel` (:1340) / `UpdateModel` (:1427); (3) an **intentionally duplicated inline mirror** in the FE API — POST `pkg/ui/server.go:869-947` and PUT `pkg/ui/server.go:1080-1143` (T1.2.3 design per comment :863-868; kept inline to match the peak-hour/secondary-upstream 400 patterns). A new kind must be added at **all three** (POST **and** PUT inline) or the surfaces drift. 🟢

### Migration 030 & the persistence layer
- `migrations/{sqlite,postgres}/030_add_model_kind.up.sql`: adds **nullable `kind TEXT`, no DEFAULT, no CHECK constraint** (sqlite up, col def ~line 29). Down = plain `DROP COLUMN` (SQLite has no `DROP COLUMN IF EXISTS`). 🟢
- **Migration registry** `pkg/store/database/migrate.go:24-64`: hard-coded ordered `[]migration` slice, last entry `{"030", "030_add_model_kind.up"}` (:63). The Amendment-1 warning comments sit directly above the 029 (:53-57) and 030 (:59-62) entries: *"This registry is an ordered slice, NOT a directory scanner; … Forgetting the append = silently inert migration"* (trap hit twice historically). 🟢 (verified verbatim)
- **`querybuilder.go` model SQL sites** (all dialect-paired PG/SQLite):
  - `InsertModel` :92-125 — 19 columns incl. `kind`; PG `$1..$19` + `EXCLUDED.kind` on ON CONFLICT upsert (:98, :117); SQLite `INSERT OR REPLACE` with 19 `?` (:123-124).
  - `UpdateModel` :133-178 — `kind = $18` (PG :153) / `kind = ?` (SQLite :175); `WHERE id = $19` / `?` (:155/:177).
  - 4 SELECT builders × 2 dialects = **8 SELECT strings**, each with `coalesce(kind, '')`: `GetModelByID` :189-212 (coalesce at :199/:210), `GetModelByName` :215-238 (:225/:236), `GetAllModels` :241-264 (:251/:262), `GetEnabledModels` :267-289 (:277/:288). 🟢
- **`store.go` model persistence sites**:
  - `dbModelRow` struct :455-484 (`Kind` field ~:480-483, migration-030 comment).
  - Column-list consts `modelSelectColumnsPG` :1074-1079 and `modelSelectColumnsSQLite` :1081-1086 (`coalesce(kind, '')` at :1079/:1086) — used by strict reads (:1094/:1096).
  - **6 scan sites**, each scanning 20 positional columns: `scanModelsContext` :767-840 (scan :777-798, mapping literal :803-820, `Kind: dbModel.Kind` at :819); `GetModel` :843-922 (inline queries :847-862, scan :867-886, `Kind:` :908); `GetModelByName` :924-1004 (queries :929-944, scan :948-968, `Kind:` :990); `getModelStrict` :1138-1172 (scan :1144-1163); `GetTruncateParams` :1216-1263 (`Kind` at :1245); `GetFallbackChain` :1265-1309 (`Kind` at :1294). Plus shared mapper `dbModelRowToConfig` :1101-1132 (`Kind: dbModel.Kind` at :1118). → the "14 model SELECT lists" = 8 (querybuilder) + 6 (2 consts + 4 inline dialect query pairs in GetModel/GetModelByName).
  - **3 existence checks** with 20-`&dummy` scans whose count MUST match the SELECT column count: AddModel :1348-1354 (:1351), UpdateModel :1435-1441 (:1438), RemoveModel :1495-1501 (:1498). Drift silently flips duplicate/not-found semantics. 🟢
  - **Write bindings**: AddModel :1368-1389 (`model.Kind` at :1388, position 19 = last); UpdateModel :1454-1475 (`model.Kind` at :1473 position 18, `modelID` :1474 position 19).
- **Count-pinning tests** `pkg/store/database/querybuilder_test.go`: `TestQueryBuilder_InsertModel` :207-243 (PG `$ != 19` pin at :239); `TestQueryBuilder_UpdateModel` :245-281 (`? != 19` at :262, `$ != 19` at :275). Migration convention: `TestMigrationNNN_ForwardFreshDB` in `migrations_test.go` (029 :622-659, 030 :667-709) asserting `schema_migrations` contains NNN + `columnExists`.
- **Kind-aware filter site (server-side)**: `GET /fe/api/models` `?kind=` switch at `pkg/ui/server.go:758-770` — cases `""` (no filter), `models.KindImageGen`, `models.KindChat`, default (log warning + treat as chat). Verified verbatim. 🟢
- **sqlc output is STALE dead code — do NOT mirror**: `pkg/store/database/db/queries.sql.go:45,114,157,184,270` and `pkg/store/database/sqlc/schema.sql:24-40` predate migrations 028 (credentials_json) and 030 (kind); `grep kind pkg/store/database/db pkg/store/database/sqlc` → 0 hits. Not on any call path. 🟢

### ⭐ Key planning conclusion (Q1)
Adding the **value** `"decision"` to the existing `models.kind` column needs **NO migration, NO registry append, NO querybuilder/store/querybuilder_test changes** — 030's column is unconstrained TEXT and `ValidateKindRules` is the gatekeeper. The minimal-diff path is ~8 code sites (constants, switch arm, `validateDecisionRules`, `IsDecision`, the `?kind=` case, POST+PUT inline blocks, misroute guard, `/v1/models` exclusion) + tests. The maximal-diff path (separate column, ~25 sites) is available but unnecessary. Two prior-BE workers + KB checklist agree; anchors verified. 🟢

---

## 2. Routing, middleware, auth

- **Composition root** `cmd/main.go`: `imggenHandler := imggen.NewHandler(configMgr, bus, tokenStore, usageCounter)` at **:241** (signature `pkg/imggen/handler.go:97`); resolver wiring :242-251 — `SetModelResolver(NewModelResolverFromConfig(func(modelID) → modelsConfig.GetModel(modelID)))` and `SetCredentialResolver(NewCredentialResolverFromConfig(func(modelID) → modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)))` (stable affinity key `(modelID, modelID)` per BE-A3/Amendment 18). 🟢
- Route registrations :282-291: `/v1/chat/completions` :282, `/v1/messages` :283, `/v1/models` :284, `/v1/image_generation` :291. POST-only is enforced **in-handler** (house style — no Go 1.22 method-pattern mux anywhere; comment at :286-290). A `/v1/decision` registration slots at ~:285 with a sibling handler block after :251. 🟢
- Middleware chain :377: `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))`. **gzipmw self-scopes to `/fe/api/*`** (`pkg/middleware/gzipmw/response.go:307` prefix check; SSE exclusion `/fe/api/events` :58-60; `MinResponseSize = 1024` :54) — it is a **no-op for all `/v1/*`**; nothing to change for `/v1/decision`. 🟢
- **Auth is in-handler, not middleware**: chat `pkg/proxy/handler.go:465-485` (`authenticate` + `requiresInternalAuth` :324-345; header extraction Bearer/X-API-Key/x-api-key :268-285; allow-list :546 via `authToken.IsModelAllowed`, empty `AllowedModels` = all allowed per `pkg/auth/token.go:129-144`). ImgGen has its **own** in-handler auth: `pkg/imggen/handler.go:203-211` → `authenticate` :523-540 / `extractAPIKey` :544-555; allow-list :267-275. A decision handler mirrors this exactly. 🟢
- **Chat-side misroute guard** `pkg/proxy/handler.go:455-462` (verified verbatim): `if rc.resolvedModel != nil && rc.resolvedModel.IsImageGen() { … 400 OpenAI envelope, code "image_gen_misroute" }`. Per comment :441-454 this guard was *intentionally the only pkg/proxy change* of the ImgGen commission. Decision needs the sibling `IsDecision()` branch here (new error code e.g. `decision_misroute`). 🟢
- **Reverse-direction gap (verified)**: `NewModelResolverFromConfig` (`pkg/imggen/handler.go:715-725`) only checks `m == nil` — its doc comment claims a "GetModel + IsImageGen-allowed guard" but **no kind check exists in code**. A chat-kind model POSTed to `/v1/image_generation` resolves fine and is only stopped later IF its primary credential's provider is not in the imggen registry (`lookup(cred.Provider)` at :302 → 502; registry holds only `"minimax"`, `minimax.go:21-22, 199-215`). A chat model with a minimax primary credential would be forwarded to MiniMax `/image_generation`. **The decision handler should add an explicit `IsDecision()` resolve check** (and the plan may want to note/fix the imggen gap). 🟢
- **`/v1/models` listing** `pkg/proxy/handler.go:244-274`: built from `GetEnabledModels()`, excludes `IsImageGen()` at **:258** (`if m.IsImageGen() { continue }`, verified) so image-gen models never appear in chat pickers. No `?kind=` on `/v1/models` (only `/fe/api/models` has one). Decision models should be excluded the same way (`|| m.IsDecision()`), unless product wants them listed. 🟢
- Env-flag gating: only MCP is gated (`MCP_ENABLED`, `cmd/main.go:263-276`); imggen is **unconditionally wired** — mirroring that (always-on `/v1/decision`) needs no env flag. 🟢

---

## 3. Provider & credential config

- **`CredentialConfig`** `pkg/models/credential.go:33-38`: `{ID, Provider, APIKey, BaseURL omitempty}`. Persisted in the `credentials` table (migration `005_add_credentials.up.sql:3-11`: `id PK, provider, api_key, base_url, created_at, updated_at`). **`api_key` encrypted at rest** AES-256-GCM: encrypt-on-write `store.go:1933-1937` (AddCredential) / `:1964` (UpdateCredential); decrypt-on-read `:1766-1771` (GetCredential) / `:1805-1809` (GetCredentials) — WARN-and-serve-ciphertext on failure (strict twin `:1822-1827` returns `ErrDecryptionFailed`). JSON-file twin `credential.go:147-216`. `APIKey` supports `${ENV_VAR}` / `${ENV_VAR:-default}` expansion (`expandEnvVars` :50-94, `ResolveAPIKey` :97-99, validator :101-120). 🟢
- **Provider factory** `pkg/providers/factory.go`: `ProviderType` consts :10-20 (openai, anthropic, gemini, zhipu, azure, zai, minimax, grok, deepseek); `NewProvider` switch :23-70 injects default baseURLs per brand (zhipu `https://open.bigmodel.cn/api/paas/v4` :36; zai `https://api.z.ai/api/coding/paas/v4` :42; minimax `https://api.minimax.io/v1` :48; grok `https://api.x.ai/v1` :58; deepseek `https://api.deepseek.com` :64; azure = no default; anthropic/gemini = "not implemented" :27-32). Supporting surfaces: `IsProviderSupported` :72-80, `providerMetadataMap` :99-154, `GetProviders` :156-171, `GetProviderTypes` :173-186 (feeds FE dropdown via `GET /fe/api/providers`, `pkg/ui/server.go:183` → :216-225). 🟢
- **The provider list is duplicated in 3 more places**: (1) `pkg/models/credential.go:14-24` `validProviders` map — gates `CredentialConfig.Validate` (:116-118) and therefore `AddCredential`/`UpdateCredential` (`store.go:1929/:1959`); (2) FE `pkg/ui/frontend/src/types.ts:109` `InternalProvider` union (7 entries — drops anthropic/gemini); (3) provider-specific hardcodes like `ImgGenModelForm.tsx:10-14` (`HARDCODED_INTERNAL_PROVIDER = 'minimax'`). Adding `openai-decision` + `jev` touches **factory ×5 sites + validProviders + FE union** (see consolidated table). 🟢
- **How imggen binds to credentials** (the pattern for decision): `handler.go:285-296` `resolveCredential(modelID)` → `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)` (`store.go:2159-2257`): `GetModel` → `Internal` check → `PrimaryCredentialID()` (credentials[0] tie-break) → `GetCredential` (**decrypts transparently** `:1752-1771`) → `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}`. Handler uses `cred.Provider` (dispatch :302), `cred.APIKey` (Bearer :358), `cred.BaseURL` (`minimax.go:72` = TrimSuffix + `"/image_generation"`), `cred.InternalModel` (body `model` rewrite `minimax.go:58`). Provider dispatch = package-local registry `registry` + `init()` (`minimax.go:199-215`), key `"minimax"`. 🟢
- **`validateModelAgainstCredentials`** `store.go:1586-1713`: per-credential-ref rules (weight>0, dup IDs, 16-credential cap, **provider-match invariant** :1620-1632 — all refs must share `Credentials[0].Provider`), then tail :1696-1708 resolves `primaryProvider = lower(GetCredential(Credentials[0]).Provider)` and calls `models.ValidateKindRules(&model, primaryProvider)`. 🟢
- Adding providers `openai-decision` + `jev`: two new `ProviderType` consts + `NewProvider` case arms (openai-decision can wrap `NewOpenAIProvider`; jev needs a decision on whether its wire is OpenAI-compatible — the *proxy-facing* endpoint speaks OpenAI Decision format regardless, per mission), `IsProviderSupported`, `providerMetadataMap`, `GetProviderTypes`, `credential.go:14-24` map, FE `types.ts:109` union, and the `validateDecisionRules` provider gate (`internalProvider ∈ {"openai-decision","jev"}`). Note: `pkg/providers` is the CHAT provider layer — whether a `pkg/decision` sibling even needs `pkg/providers` entries (vs only credential-validation entries) is a plan decision; the credential path requires the `validProviders` entries either way. 🟢/🟡 (the last nuance is judgment, flagged as such)

---

## 4. pkg/imggen anatomy (template for pkg/decision)

- **Files** (~1182 lines total): `doc.go` :1-29 (4 architectural rules: import set `{pkg/models, pkg/auth, pkg/usage, pkg/events}` only; exactly one upstream attempt; single-classifier seam; bounded RAM); `handler.go` (entry `HandleImageGeneration` :155; helpers: authenticate :526, extractAPIKey :545, meter :575, meterFailure :606, publishEvent :615, openAIError :630, resolvers :715/:735, count extraction :786-833); `client.go` :26-39 (`sharedHTTPClient`: `Timeout: 0`, `ResponseHeaderTimeout: 0` — survives 17-60s silent generations, `MaxIdleConns: 100`); `converter.go` :30-65 (`Outcome{RelayStatus, Reason}` + `UpstreamProvider` interface: `BuildUpstream`, `InspectResponse`, `BillableImages`); `minimax.go` (provider name :21-22, `BuildUpstream` :46, `InspectResponse` :93, `BillableImages` :160, registry :199-215). 🟢
- **Lifecycle** (POST /v1/image_generation): entry :155 (POST-only guard; 16MB `MaxBytesReader` :169; 413 via `*http.MaxBytesError` :177-188) → auth :203 → resolveModel :247 (404 unknown) → allow-list :267-275 (403) → resolveCredential :286 (502 unresolved) → provider lookup :302 (502 unregistered) → `BuildUpstream` :316 (URL + model rewrite) → timeout ctx :334 (`defaultImageGenTimeout = 120s` :42; clamp `[30s, 4min]` at `pkg/config/config.go:271-277`) → `sharedHTTPClient.Do` :371 → response cap `io.LimitReader(cap+1)` :404 (64MB `maxResponseBodyBytes` :56; over-cap → 502 :418-431) → `InspectResponse` :437 → relay write :444-475 (`RelayStatus==0` pass-as-is; else promote to that status, **body preserved verbatim**) → meter :486-491 → event publish :512. 🟢
- **Detached-context metering** :581 (quoted): `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)` — deliberate detachment so client disconnect cannot cancel the billing write; chat precedent `pkg/proxy/handler.go:898-902` uses `context.Background()` inside `go func()` (async) while imggen meters synchronously. 🟢
- **Error mapping is structural, not a code table**: `base_resp.status_code == 0` ⇒ success (any HTTP status incl. ≥400); `!= 0` ⇒ **502** with upstream body verbatim (`minimax.go:141-143`). The `status_msg` strings (1002/1004/1008/1026/2013/2049) are observed live values in test fixtures (`minimax_test.go:30,126`) but the proxy **never branches on them**. Number-or-string coercion of `status_code` at :121-140. 🟢 (corrects the blueprint's "error mapping table" phrasing)
- **Import-purity guard**: `Makefile:54-58` grep rule (`grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/imggen/` → FAIL) + `TestPackage_ImportGuard_NoProxyImports` `handler_test.go:689-713` (same regex over package files). **No automated transitive-closure test exists** — closure was a manual `go list -deps` audit (doc.go:25-28); a future transitive leak via pkg/models would not be caught. 🟢
- **Reusability verdict for pkg/decision** (~80% copy-and-rename): doc.go model, handler skeleton (auth/resolve/call/classify/relay/meter/event), client.go, Outcome+UpstreamProvider seam, size caps, detached metering, import guard, test scaffolding — all reusable. Provider-specific: the `minimax.go` equivalent (`BuildUpstream` URL, `InspectResponse` wire-shape — OpenAI-style error envelope instead of `base_resp`, `BillableImages`→usage coercion), event-type literal, timeout knob name. 🟢

---

## 5. Frontend

- **Tabs**: `SettingsPage.tsx` owns them. `TabType` union at :40 (verified): `'proxy' | 'models' | 'imggen_models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage' | 'mcp_servers'`. Chat tab button :327-336; ImgGen button :340-351 (`data-tab-id="imggen_models"`, 🖼️ emoji span, `aria-hidden`); content ternaries :465-475 (chat) / :484-496 (imggen); toggle handlers :207-214 / :219-226; component imports :6-7; props surface :17-38. `ConfigModal.tsx:29` is a separate LEGACY tab host without imggen — skipped by the ImgGen commission; decision should skip it too. 🟢
- **Components**: `src/components/config/` — `ModelsTab.tsx` (chat, 323 lines), `ImgGenModelsTab.tsx` (341), `ModelForm.tsx` (815, chat form, no kind field — kind implicit), `ImgGenModelForm.tsx` (439, hardcodes at :10-14: `HARDCODED_KIND='image-gen'`, `HARDCODED_INTERNAL=true`, `HARDCODED_INTERNAL_PROVIDER='minimax'`, `HARDCODED_EXCLUDE_FROM_ULTIMATE=true`, `DEFAULT_BASE_URL='https://api.minimax.io/v1'`; stamped on submit :206-217), `MultiCredentialEditor.tsx` (shared). **No kind selector UI exists** — kinds are hardcoded per-form. 🟢
- **Hooks** (`src/hooks/useApi.ts`): `useModels` :264-328 (fetch `GET ${API_BASE}/models` :272 — no kind filter; cache key `'models'` :271; TTL 15s); `useImgGenModels` :347-422 (fetch `?kind=image-gen` :364, verified; key `'imggen-models'` :363, verified; extra `error` return). **Symmetric dual-invalidation** (C-07): every mutation deletes BOTH keys — useModels :295-296/:308-309/:316-317; useImgGenModels :389-390/:402-403/:410-411. A `useDecisionModels` needs key `'decision-models'` + `?kind=decision`, and **all six existing delete-pairs become triple deletes** (+ new hook's own). 🟢
- **apiCache** (`src/utils/apiCache.ts`): `cache`/`promises` maps :33-34, `DEFAULT_TTL=30_000` :6, `getOrFetch` :79 (in-flight dedupe via `promises`), `delete(key)` :131 (drops cache + in-flight promise), `deleteByPrefix` :147, singleton `defaultAPICache` :238. 🟢
- **C-16 cross-tab filter** — exactly ONE site: `ModelsTab.tsx:157` `const chatModels = models.filter((model) => model.kind !== 'image-gen');` (verified verbatim; comment :145-156 explains hoisting above the empty-state ternary; degrades to no-op when kind absent). Must become `&& model.kind !== 'decision'`. FE-wide sweep found **no other two-kind assumptions** except `types.ts:121` union (`kind?: 'chat' | 'image-gen'`) and :153 (`ImgGenModel` alias) — add `'decision'` + `DecisionModel` alias. 🟢
- **Provider list is BE-served**: `useProviders()` :709-740 → `GET /fe/api/providers` (key `'providers'` :719) → `factory.go:173-186`. FE consumes automatically; no FE list edit needed for openai-decision/jev except the `types.ts:109` union. 🟢
- **FE API surface**: `GET /fe/api/models` :749-867 (`?kind=` switch :758-770); POST branch (kind whitelist :869-877); `PUT /fe/api/models/{id}` `handleModelDetail` :1006+ (mirror :1080-1088); `POST /fe/api/models/validate` :1212; `POST /fe/api/models/test` :1295; `GET /fe/api/providers` :183→:216-225. 🟢
- **Build/embed discipline**: `//go:embed static/*` at `pkg/ui/server.go:84-85`; `vite.config.ts:7-9` `outDir: '../static'`, `emptyOutDir: true`; `base: '/ui/'` :5. **Same-commit rule**: any commit touching `pkg/ui/static/index.html` must include regenerated hashed bundles in the SAME commit (rule recorded in blueprint + `docs/2026-09-22-fe-api-payload-ram-incident.md:241`; no dedicated doc). **tsc baseline 26 errors / 11 files is documented ONLY in the project blueprint** — not in-repo; do not regress upward. 🟢
- **FE tests**: vitest include `src/**/__tests__/**/*.test.{ts,tsx}` (`vitest.config.ts:8-14`, happy-dom). Mirror targets: `SettingsPage.tabs.test.tsx` (data-tab-id pattern), `useImgGenModels.test.ts` (URL/key/invalidations), `ImgGenModelsTab.test.tsx`, `ImgGenModelForm.test.tsx`, `ModelsTab.chatFilter.test.tsx` (C-16 — must gain a decision-row case). No `useModels.test.ts` exists. 🟢

---

## 6. Usage metering

- **`pkg/usage/counter.go`**: `Counter{db, dialect, qb, writeMu sync.Mutex}` (`writeMu` :52 serializes the 4 writers; reads unguarded). Hour buckets = `time.Now().UTC().Format("2006-01-02T15")`. Tables: `token_hourly_usage` (migration 019) and `model_hourly_usage` (migration 025, both dialects; registry :49), keyed `(token_id|model_id, hour_bucket)`. Writers (all dual-dialect UPSERT, one `ExecContext` round-trip each): `Increment` :68 (tokens), `IncrementModelUsage` :194, `IncrementTokenImages` :123 (verified — bumps `request_count` + `image_count`, tokens=0), `IncrementModelImages` :236. **Exclusivity rule**: never co-call `Increment` with `IncrementTokenImages` for one request — double-counts `request_count` (:105-114). 🟢
- Reads `GetTokenUsage`/`GetModelUsage` (:152/:265) have **zero production callers** (comment :118-122) — FE usage views run raw SQL in `pkg/ui/handlers_usage.go:197-258+`. 🟢
- **Migration 029** (image_count): `ALTER TABLE token_hourly_usage ADD COLUMN image_count INTEGER NOT NULL DEFAULT 0;` + same for `model_hourly_usage`, both dialects (sqlite :35-36 / postgres :13-14; registry :58) — NOT NULL DEFAULT 0 backfills. 🟢
- **Call sites**: chat — `pkg/proxy/handler.go:899` (token) / `:915` (model), post-failure `:1437`/`:1453`, ultimate-model `:1647`/`:1663`, all async `go func()` on `context.Background()`; imggen — `handler.go:591` (`IncrementTokenImages`) / `:594` (`IncrementModelImages`) inside synchronous `meter` :575 with the detached 5s ctx :581; `BillableImages` called only on success (:486-489), failures meter with imageCount=0 (`meterFailure` :606). 🟢
- **Decision metering options**:
  - **(a) Decision API returns token usage** → reuse `Increment` + `IncrementModelUsage` as-is; zero new counters, zero migration. Caveat: usage tables have **no kind column** — decision rows are indistinguishable from chat rows in FE usage views. Optional `kind TEXT NOT NULL DEFAULT 'chat'` column path ≈ 10 coupled sites (migration ×2 dialects + registry + 4 writer signatures + 2 UPSERT shapes + FE usage SQL).
  - **(b) No usage returned** → mirror image_count: migration 031 `decision_count` (both dialects + **registry append** — the Amendment-1 trap), `IncrementTokenDecisions`/`IncrementModelDecisions` (copy :123-149/:236-262), `HourlyUsageRow.DecisionCount` fields, test DDLs (`pkg/usage/counter_test.go:175-194`, `counter_stress_test.go`, imggen-style `testhelpers_test.go:18-46`), FE usage SQL columns. ~10-12 coupled sites, every miss is a silent leak. 🟢

---

## 7. Config seed

- `config/` contains only `models-template.json` (24 lines, 2 entries: `glm-5` :3-12 and `MiniMax-M2.5` :13-22; both `internal: true` chat models; reciprocal fallback chains). **No `kind` field in seeds** — consistent with `omitempty` (chat serializes kind-free). 🟢
- **The template is NOT loaded by any code** — `grep -rn "models-template" --include="*.go"` → 0 hits; it is an operator-reference artifact. Actual bootstrap: `cmd/main.go:85` → `database.InitializeAll` (`pkg/store/database/init.go:47-60`) → `RunMigrations` + managers; `ConfigManager.Load` starts from `config.Defaults` (`store.go:50-63`). Nothing kind-specific at seed/load time. 🟢
- **pkg/config is kind-agnostic**. The one sibling knob is `ImageGenTimeout` (`pkg/config/config.go:95-103`, global not per-model; default 120s :229-233; clamp `[30s, 4m]` :271-277; accessor on `ManagerInterface` :137, impl :636-643). A `DecisionTimeout` follows the same quartet: field + Defaults + Validate clamp + interface accessor implemented by BOTH the file Manager and DB `ConfigManager` (`store.go:23-80`). No `IMGGEN_*` env vars exist; the only env-flag pattern is `MCP_ENABLED` (`cmd/main.go:263`). Provider whitelists do NOT live in pkg/config — they live in `validateKindRules` (`models/config.go:172`) + `credential.go:14-24`. 🟢
- `models-template.json` may optionally gain a decision example entry — documentation-only, no behavioral effect. 🟢

---

## 8. Testing conventions

- **imggen harness** (`pkg/imggen/handler_test.go`, 884 lines): scriptable upstream `fakeMiniMax` :26-79 (`httptest.NewServer` + `fakeScript{status, body, contentType, delay}`, records hitCount/lastBody/lastAuthH, `t.Cleanup`); body builders `successBody`/`errorIn200Body` :81-97; env struct `handlerTestEnv` :102-112 + `newEnv` :208-292 (wires resolver closures, builds `Kind: models.KindImageGen` model, registers `sk-test-token`); `sendRequest` :302; `memTokenStore` :115-200 (in-memory `auth.TokenStoreInterface`, hash-matching); `stubConfig` :202-206 (**1-method `configGetter` seam** — handlers depend on narrow interfaces so tests stub one method); `openSQLiteForUsage` `testhelpers_test.go:14-46` (`:memory:` SQLite with hand-written usage DDL incl. image_count). 🟢
- **Style**: flat `TestHandler_<Behavior>_<Expected>` naming (not table-driven) — e.g. `TestHandler_Success_ByteIdenticalRelay` :323, `TestHandler_ErrorIn200_502WithBodyPreserved` :340, `TestHandler_MethodGuard_405` :413, caps :641/:668; `TestPackage_ImportGuard_*` :689. Same-package white-box (`package imggen`), stdlib-only (no testify in go.mod requires), `t.Helper`/`t.Cleanup`/`t.Run`. 🟢
- **Live e2e**: `live_e2e_test.go` behind `//go:build live_imggen` (:1); silently skips without `MINIMAX_API_KEY`; hard budget ≤3 upstream calls; key from out-of-tree `.env-minimax`; run via `go test -tags live_imggen -run TestLiveE2E ./pkg/imggen/...`. A `live_decision` twin fits this pattern. 🟢
- **Scripts**: `test-api-key.sh` (78 lines; 4 curl cases vs `POST /v1/chat/completions` on :4321 — Bearer/X-API-Key/missing/wrong; token from `LLM_PROXY_TEST_TOKEN` :5-9, positional `$1` overrides). `verify.sh` 🟡 likely stale (targets :8080, half-finished comments :27-36). `test_proxy_requests.py` (retry-loop diagnostics), `test_anthropic_endpoint.py` (SDK e2e). `scripts/smoke.go` (`go run scripts/smoke.go -model X|-models a,b|@file`), `scripts/test-mcp.sh` 🟡 contains a hardcoded `sk-` token at :7 (hygiene flag). `test/` holds the mock family (`mock_llm*.go` incl. minimax/race/openai/lb/loop/peak_hour variants), `e2e_*` dirs, shell e2e, Go integration tests (`access_control_test.go`, `integration_allowed_models_test.go`). 🟢/🟡 as marked
- **Makefile**: `test` :52-59 = import-guard grep :54-58 + `go test ./...` :59 — new package tests run automatically; a `pkg/decision` isolation rule adds its own grep clause mirroring :54-58. `build` :30-35 (⚠ auto-increments `VERSION` on every build), `build-frontend` :26-28. **No vet/lint/staticcheck targets exist.** 🟢
- **CI**: single workflow `.github/workflows/docker-ci.yml` — Node 22 FE build :55-59, Go 1.24 :61-68, compile :73-78, `go test ./... -v` :80-86 — **but line :83 pipes through `|| echo "No tests found or tests completed"`, which SWALLOWS test failures: CI is NOT test-gating**. `make test` locally is the real gate. 🟢

---

## Sites requiring change for `kind=decision` (consolidated)

**Path A — minimal (recommended): reuse the `kind` column; NO migration, NO registry, NO querybuilder/store changes.**

| # | File:line | Change |
|---|---|---|
| 1 | `pkg/models/config.go:90-101` | add `KindDecision = "decision"` const |
| 2 | `pkg/models/config.go:~105-107` | add `IsDecision()` helper (mirror `IsImageGen`) |
| 3 | `pkg/models/config.go:141-152` | `case KindDecision:` arm + 3-kind error message in default |
| 4 | `pkg/models/config.go:~157-185` (new sibling) | `validateDecisionRules` — internal/internal_model/credentials/provider-gate ∈ {openai-decision, jev}/feature rejections (design choices: fallback_chain? peak_hour? streaming?) |
| 5 | `pkg/proxy/handler.go:455-462` | chat-misroute guard: add `IsDecision()` branch (new code `decision_misroute`) |
| 6 | `pkg/proxy/handler.go:258` | `/v1/models`: exclude `IsDecision()` |
| 7 | `pkg/ui/server.go:758-770` | `?kind=` switch: add `case models.KindDecision` (+ default-branch log wording) |
| 8 | `pkg/ui/server.go:869-947` (POST) | kind whitelist + inline decision-rule 400 block |
| 9 | `pkg/ui/server.go:1080-1143` (PUT) | same, mirrored — both or drift |
| 10 | `cmd/main.go:~241-251 + ~285` | `decisionHandler := decision.NewHandler(configMgr, bus, tokenStore, usageCounter)` + resolver wiring + `mux.HandleFunc("/v1/decision", ...)`; optional startup log |
| 11 | NEW `pkg/decision/` | `handler.go`, `client.go`, `converter.go` (Outcome + UpstreamProvider), provider files (`openaidecision.go`, `jev.go`), `doc.go`, registry+init — mirror pkg/imggen (~80% reusable); **add explicit `IsDecision()` resolve check** (imggen lacks the reverse guard — see §2) |
| 12 | `pkg/providers/factory.go:10-20, 23-70, 72-80, 99-154, 173-186` | `ProviderOpenAIDecision` + `ProviderJev` across 5 surfaces (only if the chat provider layer must know them — credential validation needs #13 regardless) |
| 13 | `pkg/models/credential.go:14-24` | `validProviders` += `"openai-decision"`, `"jev"` |
| 14 | `pkg/ui/frontend/src/types.ts:109` | `InternalProvider` union += 2 |
| 15 | FE: `types.ts:121` + `:153` | kind union += `'decision'`; add `DecisionModel` alias |
| 16 | FE: `useApi.ts:347-422` (clone) + `hooks/index.ts:1` | `useDecisionModels` (key `'decision-models'`, `?kind=decision`); **extend ALL model mutation hooks to triple-key invalidation** (:295-296, :308-309, :316-317, :389-390, :402-403, :410-411 + new) |
| 17 | FE: `ModelsTab.tsx:157` (+comment :145-156) | C-16: `kind !== 'image-gen' && kind !== 'decision'` |
| 18 | FE: `SettingsPage.tsx` :40, ~:351, :17-38, :45-63, ~:226, ~:497 | TabType + tab button + props + destructure + toggle handler + content branch |
| 19 | FE: `App.tsx` :6/:30/:182-241 | `useDecisionModels` + prop threading |
| 20 | FE: NEW `DecisionModelsTab.tsx`, `DecisionModelForm.tsx` (hardcoded kind/provider constants pattern) | mirror ImgGen pair |
| 21 | Tests BE | `models_manager_kind_test.go` decision variants + restart round-trip; `handlers_models_kind_test.go` ?kind=decision filter; chat-misroute `IsDecision` test; new pkg/decision suite (fake upstream, memTokenStore, 1-method config stub, `:memory:` usage DDL, `TestPackage_ImportGuard_NoProxyImports`) |
| 22 | Tests FE | clone useImgGenModels/Tab/Form tests; extend `SettingsPage.tabs.test.tsx` + `ModelsTab.chatFilter.test.tsx` (decision-row fixture) |
| 23 | `Makefile:52-59` | import-guard grep clause for `pkg/decision` (convention) |
| 24 | `pkg/ui/static/*` | regenerate FE bundles in the SAME commit as any index.html/FE change |
| 25 | conditional metering | (a) tokens returned → reuse `Increment`/`IncrementModelUsage`, no changes (note: usage rows blend with chat — optional kind column ≈10 sites); (b) no usage → migration 031 `decision_count` ×2 dialects + registry append + `IncrementTokenDecisions`/`IncrementModelDecisions` + row fields + test DDLs + FE usage SQL (~10-12 sites) |
| 26 | optional config | `DecisionTimeout` quartet: `pkg/config/config.go` field/Defaults/clamp/accessor (interface :137 + both impls) |
| 27 | optional docs | `config/models-template.json` decision example (not code-loaded); CHANGELOG |

**Path B (NOT recommended) — separate `kind_decision`-style column**: adds migrations ×4 files + registry append + `migrations_test.go` fresh-DB test + querybuilder 12 SQL strings + 2 count-pin bumps + dbModelRow field + 6 scans + 3 dummy renumbers + 2 bindings + mapper — ~17 extra sites over Path A, for no functional gain.

---

## Surprises & conventions not asked about but relevant

1. **CI does not gate on tests** — `docker-ci.yml:83` `|| echo` swallows failures; `make test` is the only real gate. 🟢
2. **Reverse misroute gap (imggen)** — no `IsImageGen()` check in the image-gen handler; the resolver comment (handler.go:715-725) *claims* a guard that does not exist in code. Real gate is the provider-registry lookup. Decision should implement the explicit check. 🟢
3. **Kind rules enforced at 3 sites** — shared helper (×2 call paths) + intentionally-duplicated inline POST/PUT mirrors in `ui/server.go` (T1.2.3 design). New kind rules must be written in 3 places. 🟢
4. **Doc/code drift**: `config.go:258-259` claims image-gen forces `ExcludeFromUltimateSwitching=true` on write — no code enforces it (grep 0 hits). Don't inherit the claim for decision; decide explicitly. 🟢
5. **Import guard is 2-layer, not 3** — no automated transitive-closure test (manual `go list -deps` audit only). 🟢
6. **Usage tables have no kind column** — decision-with-tokens rows are indistinguishable from chat in FE usage views (raw SQL at `handlers_usage.go:197-258+`). 🟢
7. **Metering async vs sync**: chat meters in `go func()`; imggen synchronously; both on detached contexts. Either fits decision. 🟢
8. **`make build` auto-increments `VERSION`** (Makefile:30-35) — beware when building for verification. 🟢
9. **sqlc output + `sqlc/schema.sql` are stale dead code** — never mirror schema there. 🟢
10. **`config/models-template.json` is not code-loaded** — documentation artifact only. 🟢
11. **Hygiene flags**: `scripts/test-mcp.sh:7` hardcoded `sk-` token; `verify.sh` likely stale (port 8080). 🟡
12. **tsc baseline (26/11) lives only in the project blueprint**, not the repo — cite the blueprint in FE review. 🟢
13. **Provider name casing**: registry lookup lowercases (`minimax.go:210`); `validateModelAgainstCredentials` lowercases the primary provider (`store.go:1699`) — keep new provider ids lowercase-stable.

## Gaps / open design questions (not research failures)
- Decision wire specifics (endpoint path, token-usage presence, streaming) come from the sibling web-research report, not this file.
- Whether decision supports `fallback_chain`/`peak_hour_enabled`/streaming is a product decision for `validateDecisionRules`.
- Whether `pkg/providers` needs full `Provider` impls for openai-decision/jev (chat layer) vs only credential-validation entries — plan decision; credential `validProviders` entries are required either way.
