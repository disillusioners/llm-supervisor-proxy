# Architecture Recommendation — Image-Generation Test Button

**Date:** 2026-10-20
**Feature:** per-row operator "Test" button for image-gen models, mirroring the shipped decision-model Test button (`fef0e76` → `latest f7faf4b` → `master 37a95a9`)
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-img-gen-test-button` @ `feature/img-gen-test-button` (base `latest` @ `a64e5262`)
**Method:** 4-way parallel analysis (data-flow / resilience / structural / system-decomposition), aggregated on the five fixed axes. All findings below carry `file:line` evidence from the four worker reports.

---

## 0. Executive summary

Build a **`pkg/imggen/test_exec.go`** seam exporting `ExecuteTest(cred models.ResolvedCredential, rawBody []byte, timeout time.Duration) *TestResult` — a package-level free function inside `pkg/imggen`, mirroring `pkg/decision/test_exec.go`. Drive it from a **new sibling route `POST /fe/api/image_generation/test`** in a **new `pkg/ui/handlers_imggen.go`**, which resolves the model and the **primary credential by exact ID** (`credentials[0]`, never the LB-engine pick), builds a canned body pinned to `response_format:"url"`, `n:1`, and returns **always-HTTP-200 JSON** carrying the classified outcome **plus the upstream image CDN URLs** — the FE renders `<img src={url}>` with **zero extra proxy round-trips**.

Three findings change the shape of the mirror relative to the decision lane — they are the core of this recommendation:

1. 🔴 **MiniMax reports business failures inside HTTP 200** (`base_resp.status_code != 0` → `RelayStatus: 502`, `pkg/imggen/minimax.go:141-143`). The decision lane's classifier branches on **upstream HTTP status first** (`pkg/decision/test_exec.go:223-235`); a verbatim mirror would classify **every MiniMax rejection as success**. The image-gen classifier must branch on the adapter `Outcome` **before** the HTTP status.
2. 🔴 **The chat Test-Connection path silently misroutes for image-gen rows.** `providers.NewProvider("minimax")` *succeeds* (`pkg/providers/factory.go:66-71`) and posts a chat body to `/chat/completions` (`pkg/providers/openai.go:126`), never touching `/image_generation` (`pkg/imggen/minimax.go:72`). Unlike the decision lane (where `"typesafe"` fails loudly), a wrong reuse **fails quietly in the wrong place** — a dedicated sibling route is mandatory.
3. ✅ **The upstream returns URLs, not bytes** (`data[].image_urls`, body ≈ 451 B, `response_format:"url"` — `pkg/imggen/handler.go:51-52`, `live_e2e_test.go:110,126-129`). The FE renders the CDN URL directly: no multi-MB base64, no second proxy hop, no blob plumbing.

---

## 1. Recommended seam location + why  (Question B)

**`pkg/imggen/test_exec.go` (NEW) — candidate (i). Rejected: (ii) `pkg/ui/handlers_imggen.go` executor, (iii) resolver-seam reuse.**

The route handler *does* live in `pkg/ui` (`handlers_imggen.go`); the *upstream-call executor* lives in `pkg/imggen`.

**Evidence for in-package placement:**

| Reused machinery | Location | Visibility |
|---|---|---|
| `lookup(providerName)` — adapter registry | `pkg/imggen/minimax.go:212-215` | **unexported** |
| `registry` map + `init()` (`minimax` = only entry) | `pkg/imggen/minimax.go:199-206` | unexported |
| `sharedHTTPClient` (`Timeout:0`, `ResponseHeaderTimeout:0` — the 17–60 s silent-generation trap, `client.go:32-37`) | `pkg/imggen/client.go:26-39` | unexported |
| `maxResponseBodyBytes` (64 MB) / `maxRequestBodyBytes` (16 MB) | `pkg/imggen/handler.go:48,56` | unexported |
| `maxResponseBodyBytesForTest` (cap-override hook for tests) | `pkg/imggen/handler.go:71` | unexported |
| `defaultImageGenTimeout` (120 s) | `pkg/imggen/handler.go:42` | unexported |
| `UpstreamProvider` / `Outcome` | `pkg/imggen/converter.go:30-33,44-65` | exported |

Everything the seam needs except the adapter interface is package-private. `pkg/ui` **cannot** reach `lookup()` or `sharedHTTPClient` — an executor there would have to re-implement the body rewrite, the `/image_generation` URL suffix, the `base_resp` classifier, the caps, the timeout, and the header discipline: the literal fork `pkg/decision/test_exec.go:26-30` exists to prevent ("Re-implementing that stack inside pkg/ui would fork the bug surface — the exact failure class the decision commission already paid for once").

**Import-cycle verdict: none.** `pkg/ui` is a leaf (only `cmd/main.go` and `test/e2e_fe_reasoning_observability` import it); `pkg/imggen` imports only `{pkg/models, pkg/auth, pkg/usage, pkg/events}` (`handler.go:32-35`). `pkg/ui → pkg/imggen` is provably acyclic — `pkg/ui` already imports `pkg/decision` the same way (`handlers_decision.go:11`).

**Rejected candidates:**
- **(ii) executor in `pkg/ui`:** cannot reach the private machinery; re-opens the duplicate-`Content-Type` / copied-`Accept-Encoding` gzip bug class (`handler.go:352-377`; the CT variant already cost a fix at `9b71e63`); and any lean on `pkg/providers` for `"minimax"` **silently misroutes to `/chat/completions`** (see §0.2).
- **(iii) resolver seams (`SetModelResolver`/`SetCredentialResolver`, `cmd/main.go:241-250`):** they answer a different question (how the production HTTP route finds its model/credential without importing the store). They resolve through `ResolveInternalConfigWithAffinity` — the **LB-engine rotating pick**, which the test-button convention explicitly forbids (`handlers_decision.go:178-187`). The decision lane deliberately ignored them for the probe; imggen should too.

**Signature — deliberately divergent from the decision lane** (the mirror is structural, not literal):

```go
// pkg/imggen/test_exec.go
func ExecuteTest(cred models.ResolvedCredential, rawBody []byte, timeout time.Duration) *TestResult
```

- **3 args, not 4** — imggen has no typed request struct; the adapter takes raw client bytes and the `model → InternalModel` rewrite happens *inside* the adapter (`minimax.go:63-65`).
- **No "inspect error" branch** — imggen's `InspectResponse(upstreamStatus int, body []byte) Outcome` returns `Outcome` with **no error return** (`converter.go:56`), unlike decision's 2-arg-with-error shape.
- **Classifier order inverted** (see §3).
- `ExecuteTest` is NOT a handler: no auth, no metering, no events, no HTTP write (`test_exec.go:32-35` contract).

**Improvement over the decision lane (recommended):** extract a **package-private** upstream-request builder (headers + CT + Authorization) inside `pkg/imggen` used by **both** `HandleImageGeneration` (`handler.go:337-377`) and `ExecuteTest`. This drives the CT/Accept-Encoding discipline from 2 machine-pinned copies (what the decision lane shipped) to **1 shared source + 2 test assertions**.

**Guard asymmetry to close while touching the package (🟢):** `pkg/imggen` has **no transitive-closure import test** and no guard vs `pkg/decision` (unlike `pkg/decision/handler_test.go:40,65,90`). Mirror `TestPackage_ImportGuard_TransitiveClosure`. And **never** add `pkg/imggen → pkg/decision` (parent→child inversion; `pkg/decision/doc.go:19-20` records imggen as the template).

---

## 2. Recommended success envelope (exact JSON)  (Question E)

**Decision: pass the upstream image CDN URLs through inside the always-200 DTO.** The FE renders `<img src={url}>` directly against MiniMax's CDN — **zero additional proxy round-trips**, tiny payload (URL-mode bodies ≈ 451 B), no base64, no blob URLs, no second endpoint.

```go
// pkg/ui/handlers_imggen.go
type ImgGenTestResponse struct {
    Success        bool     `json:"success"`
    Model          string   `json:"model,omitempty"`         // proxy row id
    Provider       string   `json:"provider,omitempty"`      // "minimax"
    CredentialID   string   `json:"credential_id,omitempty"`
    ImageURLs      []string `json:"image_urls,omitempty"`    // extracted from data — BOTH shapes tolerated (see §8 R4)
    ImageCount     int      `json:"image_count,omitempty"`   // len(ImageURLs)
    SuccessCount   int      `json:"success_count,omitempty"` // metadata.success_count
    FailedCount    int      `json:"failed_count,omitempty"`  // metadata.failed_count
    Status         int      `json:"status"`                  // classified status
    RelayStatus    int      `json:"relay_status,omitempty"`  // adapter Outcome.RelayStatus verbatim
    UpstreamStatus int      `json:"upstream_status,omitempty"` // raw upstream HTTP status
    Code           string   `json:"code,omitempty"`          // stable machine-readable slug (§3)
    Error          string   `json:"error,omitempty"`         // ≤500 chars, sanitized
    Reason         string   `json:"reason,omitempty"`        // adapter reason verbatim
    DurationMS     int64    `json:"duration_ms,omitempty"`
}
```

**Envelope split (decision-lane convention, confirmed at `handlers_decision.go:134-138`, `:388-392`):**

| Class | Wire shape | Why |
|---|---|---|
| **Every classified outcome** (upstream answered, transport, business-error-in-200, timeout, over-cap, empty-image, cooldown) | **HTTP 200 + `ImgGenTestResponse`** | `apiFetch` throws on non-2xx and would drop the structured result (`useApi.ts:19-24`) — one wire shape for everything the operator cares about |
| **Client-mistake classes** (non-POST, body > 64 KB, malformed JSON, missing `model_id`, unknown model, **wrong kind**, no credentials, credential without key) | bare `{"error":"<string>"}` non-200 via `writeFEError` | identical to `handlers_decision.go:140-228` |

**Why URL passthrough beats the alternatives** (full 5-axis comparison in `approach-comparison.md`):

| Option | Verdict |
|---|---|
| **A. CDN URLs in the DTO** (`image_urls`) | ✅ **Recommended** — tiny payload, zero extra proxy machinery, satisfies "no second round-trip" (the browser→CDN `<img>` fetch is not a proxy hop) |
| B. Proxy fetches bytes → base64 data URL | ❌ A **second upstream call** violates exactly-one-call discipline; multi-MB payloads through `/fe/api` and gzipmw; new fetch machinery = the fork we are avoiding |
| C. Blob URL / separate bytes endpoint | ❌ Two round-trips + server-side state; strictly worse than A given the upstream already returns URLs |

**Adjudicated divergence (recorded):** the resilience analysis initially argued for relaying *no* image data (count-only) on sanitization grounds. Overruled on this evidence: the upstream returns **URLs** (not bytes), so the multi-MB-base64 concern is moot; the URLs are upstream-generated CDN locators, not an echo of the request; and the commission explicitly requires the FE to render the returned image. Sanitization rule (§3.3) still forbids relaying the raw body — only the enumerated fields + URLs cross.

**Known caveat (unverified in-repo):** MiniMax image-URL **lifetime** is not recorded anywhere in the repo. The FE must render immediately and display the URL; do not promise persistence. Document this in the FE component comment.

**Cost hint tooltip:** ship it, but label the number as a hint — the `$0.0035/call` figure in the brief is **not repo-evidenced** (repo-wide grep for pricing: 0 hits; it matches a different system's pricing). Suggested copy: `"Each test generates a real image (~$0.004 — verify against provider pricing)"`.

---

## 3. Recommended error-classification scheme (exact mapping)  (Question F)

### 3.1 The structural inversion (the single most important rule)

**Branch on the adapter `Outcome` / `base_resp` BEFORE the upstream HTTP status** — the decision lane's order (`test_exec.go:223-235`: `case res.UpstreamStatus >= 400` first) is **wrong for MiniMax**, which reports failures inside HTTP 200 (`minimax.go:78-79, 141-143`).

```
 1. pre-upstream local failures (no adapter / build error / no credential / wrong kind)
      → local status (502 / 400 / 404) + code
 2. transport error            → 502  code=upstream_transport_error
 3. deadline exceeded          → 504  code=timeout
 4. body read error            → 502  code=upstream_read_failed
 5. body over 64 MB cap        → 502  code=response_too_large   (body DISCARDED)
 6. upstream HTTP >= 400       → that status  code=upstream_http_error
 7. base_resp missing/unparseable → 502  code=upstream_malformed
 8. base_resp.status_code != 0 (in a 200!) → 502  code=upstream_business_error  [+ code + status_msg]
 9. status_code == 0 but zero images → 200 success=false  code=empty_image     ← NEW class
10. otherwise                  → 200  code=ok, image_count = success_count
```

Steps 7–8 must be evaluated **even when `UpstreamStatus == 200`**. Note `RelayStatus == 0` in imggen semantics means **PassAsIs** (covers upstream ≥400 passthrough *and* success, `converter.go:11-16`) — the FE must never treat HTTP 200 alone as success.

### 3.2 The mapping table (FE labels)

**Reuse the decision lane's status→label vocabulary verbatim** — extract `DECISION_ERROR_CLASSES` (`DecisionModelsTab.tsx:56-70`) into a shared module (e.g. `pkg/ui/frontend/src/components/config/probeErrorClasses.ts`), keep `classifyDecisionTestError` as a thin re-export so its existing test stays green, and let `ImgGenModelsTab` add a small **code-keyed overlay**. Resolution order in the FE: **code label (if `code` present and known) → status label → default branch** (`status >= 500 ? 'Upstream error' : 'Unexpected error'`).

| Condition | Status | Code | FE label |
|---|---|---|---|
| FE transport failure (BE never answered) | 0 | — | `Test request failed — the proxy did not answer` |
| adapter rejected the canned body / no credential / no key | 400 | `preflight_rejected` / `no_credential` | `Request rejected by the image-gen adapter` |
| credential invalid | 401 | `upstream_http_error` | `Authentication failed — check the credential API key` |
| wallet empty | 402 | `upstream_http_error` | `Insufficient funds — top up the provider account` |
| model not permitted | 403 | `upstream_http_error` | `Access denied — the credential may not use this model` |
| wrong base URL | 404 | `upstream_http_error` | `Not found — check the upstream base URL` |
| upstream asked to retry | 408 | `upstream_http_error` | `Upstream timeout` |
| endpoint rejected | 422 | `upstream_http_error` | `Endpoint unavailable` |
| **request payload over cap** | **413** | `request_too_large` | `Request too large — the image payload exceeded the cap` |
| rate limited (upstream or probe cooldown) | 429 | `upstream_http_error` / `cooldown` | `Rate limited — retry later` |
| upstream 5xx | 500 | `upstream_http_error` | `Upstream error` |
| transport / malformed / over-cap / no adapter | 502 | `upstream_transport_error` / `upstream_malformed` / `response_too_large` / `no_adapter` | `Upstream error` |
| upstream unavailable | 503 | `upstream_http_error` | `Upstream unavailable — retry later` |
| probe deadline exceeded | 504 | `timeout` | `Upstream timed out` |
| **MiniMax business error in a 200** (`base_resp.status_code != 0`) | 502 | **`upstream_business_error`** | `Upstream rejected the request — invalid parameters` (code 2013) / default `Upstream rejected the request` |
| **200, status_code 0, zero images** | 200 (success=false) | **`empty_image`** | `No image returned — the upstream accepted the request but produced no images` |
| row is not an image-gen model | 400 (bare envelope) | `invalid_model_kind` | `Not an image-gen model — this row is served by another endpoint` |

**`2013` is the only MiniMax business code pinned in-repo** (`minimax_test.go:30`, `minimax.go:129-131`). Do NOT invent meanings for other codes — ship the generic label with the numeric code visible; pin real codes to labels only after live observation.

### 3.3 Sanitization rule (verbatim adoption from the resilience report)

The upstream body **never** crosses to the FE — not verbatim, not truncated-verbatim, not base64. The BE may extract **only**: `base_resp.status_code` (int), `base_resp.status_msg` (string, trimmed, control-stripped, **≤ 500 chars** — the `decisionTestMaxMessageLen` convention, `handlers_decision.go:61,363-369`), `metadata.success_count` / `failed_count` (int), and `data`'s `image_urls`. No upstream response header is copied onto the FE response. Secret-shaped strings (`sk-`, `Bearer `, long base64) → `[REDACTED]`. The FE caps again at 300 (`DecisionModelsTab.tsx:79-88` convention). Do **NOT** copy `decisionTestUpstreamMessage` (`handlers_decision.go:325-360`) — it probes OpenAI-envelope shapes (`error.message`/`message`/`detail`) and returns `""` for every MiniMax error; the imggen extractor probes `base_resp.status_msg`.

### 3.4 `Outcome` extension (minimal, behavior-preserving)

To carry the business code without string-parsing `Reason`, extend `pkg/imggen/converter.go:30-33` additively:

```go
type Outcome struct {
    RelayStatus int
    Reason      string
    Code    int    // upstream business code (base_resp.status_code); 0 = none
    Message string // upstream business message (base_resp.status_msg)
}
```

populated at `minimax.go:141-143`. All seven `Outcome{...}` literals in the package are **keyed** (`minimax.go:97,106,113,135,139,142,149`) so this compiles everywhere without touching other sites; the production handler ignores the new fields (byte-identical relay tests stay green). *Fallback if the team insists on zero production-file edits:* derive `code`/`message` in the UI layer from `Reason` (`"base_resp.status_code=2013"`) — works, but brittle; the additive extension is recommended.

---

## 4. Credential selection  (Question C, D, J)

**Test lane: `model.Credentials[0].CredentialID` → `s.modelsConfig.GetCredential(credID)` — exact-ID, primary credential only.** Mirrors `handlers_decision.go:178-199` (chat + decision convention) verbatim.

**Production imggen differs:** it resolves through `ResolveInternalConfigWithAffinity(modelID, modelID)` (`cmd/main.go:244-250` → `pkg/store/database/store.go:2513`) — the credential-LB engine pick (weighted, sticky key `(modelID, modelID)`, cooldown-aware; `len==1` takes the E-3 fast path on `Credentials[0]`). The test convention **deliberately bypasses** the engine so the operator validates the credential they see first. Stated caveat (same one the decision lane accepted): for a multi-credential row, `credentials[0]` may not be the credential production would serve.

**"Is a first-credential selection meaningful for imggen?" — Yes, structurally guaranteed:** image-gen rows require ≥ 1 credential and the **primary** credential's provider must be `"minimax"` at write time (`pkg/models/config.go:242-251`), and the imggen registry registers exactly one adapter (`minimax.go:199-206`). So `Credentials[0]` is always a valid, minimax-backed probe target.

**Resolved credential assembly** (mirror `handlers_decision.go:206-222`): `baseURL = model.InternalBaseURL || cred.BaseURL`; `InternalModel = model.InternalModel`; the canned body carries the **proxy row id** in `model` — the adapter rewrites it to `InternalModel` and appends `/image_generation` (`minimax.go:63-72`).

**Attribution/UA (commission item 3): NOT APPLICABLE — premise corrected.** `pkg/imggen` never imports `pkg/providers` (imports: auth/events/models/usage, `handler.go:32-35`; import guard `doc.go:11-16`), so `x-opencode-session` / attribution-UA machinery (`opencodego.go`, `openai.go:288-306 setHeaders`) is **structurally unreachable** from the image-gen path — and correctly so: attribution is an opencode-family feature, image-gen rows are gated to `minimax`. The seam must NOT import `pkg/providers` to add attribution that production itself does not send. The real-parity requirement is satisfied trivially: the probe's upstream headers (one CT + Bearer, no client headers) are exactly what production sends for an image-gen call.

**Metering (Question J) — the architect's call, made explicit:** production imggen meters **every** response (`h.meter` at `handler.go:502`; failures via `meterFailure` → `meter(...,0)` at `:401,426,440`; `IncrementTokenImages`/`IncrementModelImages`, `pkg/usage/counter.go:123,236`; documented divergence from chat's success-only convention, Amendment 14). **The test seam must NOT meter** — mirror `test_exec.go:32-35`: no auth, no meter, no events. Rationale: probe rows would pollute `model_hourly_usage.image_count` and `request_count` for a model the operator is merely probing, and `IncrementModelImages` is the *exclusive writer* for that row (`counter.go:230-235`) — a probe writing it would corrupt production accounting. No suppression flag exists (grep: 0 hits; `h.usage == nil` is the only structural off-switch and would also disable auth). Cost visibility is preserved via a dedicated log line: `[imggen-test] model=%s credential=%s provider=%s status=%d upstream_status=%d code=%s images=%d duration_ms=%d` (credential **ID only**, never the key — `handlers_decision.go:279-280` convention). **Trade-off recorded:** probe spend is invisible to the usage dashboards; the log line + FE result are the only trail.

---

## 5. FE reuse targets (exact files/components)  (Question G)

| Concern | File / component | Anchor |
|---|---|---|
| Image-gen model rows | `pkg/ui/frontend/src/components/config/ImgGenModelsTab.tsx` — `ImgGenModelsTab` (decl :34); row = inline arrow in `models.map(...)` (:190); **Test button goes in the action cluster at :257-280** (before Edit), result panel into the detail column after :254 | mount: `SettingsPage.tsx:577-591`, tab `:403`; data `useImgGenModels()` (`useApi.ts:368-455`, hits `/fe/api/models?kind=image-gen`) |
| Per-row busy isolation (template) | `DecisionModelsTab.tsx:116-117` (`testingIds: string[]` + `testResults: Record<...>`), `handleTest` :209-241 (drops prior result, per-row finally) | the `fef0e76` FIX2 — a single global slot is the exact bug to avoid |
| Test button markup (template) | `DecisionModelsTab.tsx:456-474` — `title`, `aria-label={Test ${model.name}}`, `data-testid={decision-test-button-${model.id}}`, `Test`/`Testing...`, `disabled={isTesting}` | mirror with `imggen-test-*` ids |
| Result panel chrome (template, reusable) | `DecisionModelsTab.tsx:364-373` — border/bg/`role` switch (`status` vs `alert`), `data-testid={...-result-${model.id}}` | success :374-433, error :434-451 |
| **Image rendering** | **NET-NEW — no image renderer exists anywhere in the FE.** Exhaustive grep for `<img`, `data:image`, `createObjectURL`, `base64`, `b64_json` over `src/**` (incl. tests): **0 hits**. The `<img src={url}>` element is the first in the dashboard — budget it explicitly; reuse only the panel chrome | — |
| API client | `pkg/ui/frontend/src/hooks/useApi.ts` — sibling `testImgGenModel(modelId)` next to `testDecisionModel` (:1214-1228), via `apiFetch` (never raw fetch); do NOT touch `defaultAPICache` | extend the `import type {...}` at line 2 |
| Types | `pkg/ui/frontend/src/types.ts` — `ImgGenTestResult` after the decision block (~:584-596) | `Model.kind` already unions `'image-gen'` (:123) — no change |
| FE framework | **Preact** (`jsxImportSource: "preact"`), vitest + `@testing-library/preact` + happy-dom | premise correction — not React |

---

## 6. FE build & commit contract  (Question H)

- **Build command:** `make build-frontend` (Makefile:26-28 → `cd pkg/ui/frontend && npm install && npm run build` → `vite build`, `outDir: '../static'`). Do **NOT** use `make build` / `make all` — `make build` **auto-increments `VERSION`**, an unwanted side effect. `npm ci` works (lockfile present); `node_modules` is absent in the worktree (network needed).
- **Commit list (same commit, `git add -A pkg/ui/static` so deletions stage):**
  1. `pkg/ui/static/index.html` — modified (hashed refs)
  2. `pkg/ui/static/assets/index-<NEWHASH>.js` — new (old deleted)
  3. `pkg/ui/static/assets/index-<NEWHASH>.css` — new (old deleted; Tailwind class changes usually move it)
  4. `pkg/ui/static/assets/SettingsPage-<NEWHASH>.js` — new (old deleted) — **always changes**: the tab lives in this lazy chunk
- `emptyOutDir: true` ⇒ a rebuild **deletes** the old hashed files; missing the deletions leaves dead weight.
- 🔴 **Premise correction:** `pkg/ui/embed_regression_test.go` pins **internal self-consistency only** (`TestEmbedStatic_AllAssetReferencesResolve` :67-111 asserts every `/ui/assets/...` ref in `index.html` resolves inside the embed FS). A **stale-but-self-consistent bundle passes silently** — the "same commit" rule is convention (honoured by `101bcfd` and `fef0e76`), **not a test gate**. The implementer must regenerate deliberately.
- 🔴 **tsc baseline freeze is a release gate:** `tsc-baseline.test.ts` runs `npx --no-install tsc --noEmit` and asserts **19 errors / 9 files per-file** (`tsc-baseline.txt`). `ImgGenModelsTab.tsx` is not in the error set — one unused local (`noUnusedLocals`, `strict`) breaks the gate. Write the new FE code tsc-clean; never silently bump the baseline.
- Docker does **not** consume the committed bundle (`.dockerignore:3` excludes `/pkg/ui/static/`; Dockerfile rebuilds it) — the committed bundle governs local binaries + the embed test.

---

## 7. Test strategy  (Question I)

Go: stdlib `testing` only (**no testify**), `httptest`, table-driven, white-box (`package ui` / `package imggen`). Run with `HOME=$(mktemp -d)` isolation. FE: vitest + `@testing-library/preact`, fetch-mock swap, `data-testid` queries.

**Create — BE:**

| Test | Mirrors | Key assertions |
|---|---|---|
| `pkg/imggen/test_exec_test.go` | `pkg/decision/test_exec_test.go` (245 L) | success relay; **error-in-200: `Status==502` + `Reason=="base_resp.status_code=2013"` + `UpstreamStatus==200`** (the inverted classifier); transport → 502; deadline → 504; over-cap via `maxResponseBodyBytesForTest` (`handler.go:71`); **exactly-one-upstream-call** (`fake.hitCount == 1`); **CT count == 1 + no client Accept-Encoding** (`test_exec_test.go:186-190` pattern) |
| `pkg/ui/handlers_imggen_test.go` | `pkg/ui/handlers_decision_test.go` (583 L) | non-POST 405; missing `model_id` 400; unknown model 404; **`!model.IsImageGen()` → 400** (the kind gate the production path lacks); no-credentials 400; **decoy credential: second credential with a different provider must NOT be picked** (mirror `TestHandleDecisionTest_UsesFirstCredentialByExactID` :372-420 — introduced by `101bcfd`, *not* `c63553f`); error classification table; transport-failure carries the cause (`fef0e76` FIX1); always-200-for-classified; **cross-path parity: real `/v1/image_generation` vs `/fe/api/image_generation/test` against the same fake upstream** (mirror `TestHandleTestModel_CrossPathParity_RealVsTestButton`, `handlers_models_test_connection_test.go:207-269` — note: **no such parity test exists in `pkg/decision`**; its reuse is structural. This is the one place the imggen lane should *exceed* the decision precedent) |

**Create — FE:**

| Test | Mirrors |
|---|---|
| `pkg/ui/frontend/src/components/config/__tests__/ImgGenModelsTab.imgGenTest.test.tsx` | `DecisionModelsTab.decisionTest.test.tsx` (19 tests): URL `/fe/api/image_generation/test`, payload `{model_id}`, per-row busy isolation (two rows probing concurrently), success panel + `<img>` + metadata, error classes (incl. `upstream_business_error`, `empty_image`, 413), `status:0` fallback |

**Regression (must stay green, unmodified):** `pkg/imggen/handler_test.go` (`TestHandler_Success_ByteIdenticalRelay` :352, `ErrorIn200` :369, `Upstream_SingleContentTypeNoClientAcceptEncoding` :500, `ExactlyOneUpstreamCall` :567, `ResponseOverCap_502` :750, `ImportGuard_NoProxyImports` :798), `pkg/ui/frontend/src/components/config/__tests__/ImgGenModelsTab.test.tsx` (7 tests — keep `imggen-model-row` etc. ids), `pkg/ui/embed_regression_test.go`, `pkg/ui/kind_rules_parity_test.go`, the tsc baseline (19/9).

**Recommended addition (🟢):** `TestPackage_ImportGuard_TransitiveClosure` mirror in `pkg/imggen/handler_test.go` (closes the guard asymmetry vs `pkg/decision/handler_test.go:90`).

---

## 8. Top 5 implementation risks + mitigations

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1 | **Classifier order** — mirroring the decision switch verbatim (`UpstreamStatus >= 400` first) reports every MiniMax rejection as **success** (green lie) | 🔴 | Branch on `Outcome`/`base_resp` first (§3.1). Pin with the error-in-200 test: `Status==502`, `Reason=="base_resp.status_code=2013"`, `UpstreamStatus==200`. FE never treats 200 alone as success (checks `success` + `code`) |
| 2 | **Silent misroute** — any code path touching `pkg/providers` for an image-gen row POSTs a chat body to `/chat/completions` and fails quietly | 🔴 | Dedicated sibling route only; `model.IsImageGen()` gate **before** credential resolution (production `/v1/image_generation` has no kind gate — `handler.go:247`; the test route must not inherit the hole); decoy-credential test; cross-path parity test vs the real route |
| 3 | **Stale embedded bundle ships silently** — the embed test pins self-consistency only; a stale-but-consistent bundle passes with zero failures | 🔴 | Same-commit discipline via `make build-frontend` + `git add -A pkg/ui/static`; add the bundle regeneration to the PR checklist; verify `SettingsPage-<hash>.js` changed (the tab lives in that chunk) |
| 4 | **Forked call stack** — an executor in `pkg/ui` re-opens the duplicate-CT / Accept-Encoding gzip class and duplicates the classifier/caps | 🟡 | In-package seam only; **shared package-private request-builder** used by both handler and seam; machine-pin CT count at seam **and** route level (both suites) |
| 5 | **Billable, unmetered, unauthenticated probes** — every click burns real MiniMax quota; `/fe/api/*` has no auth; a row storm = N concurrent paid generations | 🟡 | No-meter/no-events (§4) + `[imggen-test]` log line; **BE in-flight cap (1 per row, 2 global) + 10 s per-row cooldown** → 429 `code:"cooldown"` (reuses the shared 429 label — no new FE vocabulary); FE per-row busy flag; canned body pins `response_format:"url"`, `n:1`, `1:1` so the payload stays ~451 B and never approaches the 64 MB cap |

**Secondary risks (tracked):** timeout — reuse `ImageGenTimeout` (default 120 s, clamp [30 s, 4 min], `config.go:241,269-283`, getter `:645-652`) with a UI fallback constant `imgGenTestDefaultTimeout = 120s`; **never** the decision lane's 30 s (false failures). Keep `ResponseHeaderTimeout: 0` by reusing `sharedHTTPClient` — a fresh `http.Client` re-opens the 17–60 s silent-generation trap (`client.go:32-37`). `data` shape ambiguity (array-of-objects in fixtures `handler_test.go:111` vs object in live e2e `live_e2e_test.go:127-129`) — the `image_urls` extractor must tolerate **both**. tsc baseline freeze (19/9). Probe leaves no request-log row (imggen writes no `RequestStore` row — the DTO + log line are the only feedback surfaces).

---

## 9. File-by-file change list

**CREATE**

| Path | Contents |
|---|---|
| `pkg/imggen/test_exec.go` | `ExecuteTest(cred, rawBody, timeout) *TestResult` + `TestResult{Status, RelayStatus, Reason, UpstreamStatus, Body, Error, ImageURLs, ImageCount}`; reuses `lookup`/`sharedHTTPClient`/`BuildUpstream`/`InspectResponse`/`BillableImages`/caps; inverted classifier; imports only `pkg/models` |
| `pkg/imggen/test_exec_test.go` | seam unit tests (§7) |
| `pkg/ui/handlers_imggen.go` | `handleImgGenTest` + `ImgGenTestRequest`/`ImgGenTestResponse`; kind gate; credentials[0] exact-ID; canned body (`model`=row id, `prompt`, `response_format:"url"`, `n:1`, `aspect_ratio:"1:1"`); `base_resp`-shaped message extractor (≤500 chars); `[imggen-test]` log line; always-200 classified DTO |
| `pkg/ui/handlers_imggen_test.go` | route tests incl. decoy credential + cross-path parity (§7) |
| `pkg/ui/frontend/src/components/config/probeErrorClasses.ts` | shared `PROBE_ERROR_CLASSES` + `classifyProbeError` (extracted verbatim from `DecisionModelsTab.tsx:56-77`) |
| `pkg/ui/frontend/src/components/config/__tests__/ImgGenModelsTab.imgGenTest.test.tsx` | FE suite (§7) |

**MODIFY**

| Path | Change |
|---|---|
| `pkg/imggen/converter.go` | additive `Outcome.Code int` + `Outcome.Message string` (§3.4) |
| `pkg/imggen/minimax.go` | populate `Code`/`Message` at :141-143 (2 lines, behavior-preserving) |
| `pkg/imggen/doc.go` | document `ExecuteTest` + import rule |
| `pkg/ui/server.go` | register `mux.HandleFunc("/fe/api/image_generation/test", s.handleImgGenTest)` next to `:173` |
| `pkg/ui/frontend/src/types.ts` | `ImgGenTestResult` (+ image payload types) after the decision block |
| `pkg/ui/frontend/src/hooks/useApi.ts` | `testImgGenModel(modelId)`; extend type import at line 2 |
| `pkg/ui/frontend/src/components/config/ImgGenModelsTab.tsx` | per-row busy state, `handleTest`, Test button in action cluster (:257), result panel + **net-new `<img>`** + metadata (size/format from URL ext, provider, latency `duration_ms`, cost-hint tooltip), `classifyImgGenTestError` + `truncateImgGenTestMessage` (exported) |
| `pkg/ui/frontend/src/components/config/DecisionModelsTab.tsx` | re-export `classifyDecisionTestError` from `probeErrorClasses.ts` (existing test stays green) |
| `pkg/ui/static/index.html` + `assets/index-*.js` + `assets/index-*.css` + `assets/SettingsPage-*.js` | **regenerated bundle** (`make build-frontend`), old hashes deleted, same commit |

**DO NOT MODIFY**

- `pkg/imggen/handler.go` (production path byte-identical; its tests pin byte-identical relay) — except optionally extracting the shared request-builder helper, which is a refactor to propose separately, not to bundle with this feature
- `pkg/ui/frontend/tsc-baseline.txt` (must stay 19/9)
- No DB migrations, no config schema changes (reuses `ImageGenTimeout`)

**Route path note:** use `/fe/api/image_generation/test` (as commissioned) — it mirrors `/v1/image_generation` exactly as `/fe/api/decisions/test` mirrors `/v1/decisions`.

---

## 10. Corrected premises (explicit, as requested)

1. **MiniMax IS wired for image generation** — via `pkg/imggen`'s private registry (`minimax.go:199-206`), a **different path** from chat MiniMax (`pkg/providers/factory.go:66-70`). The production image-gen model **is** served by `pkg/imggen`. ✔ premise holds, with the two-paths clarification.
2. **Kind literal is `image-gen` (hyphen)**, not `image_gen` (`pkg/models/config.go:104,140-142`).
3. **Attribution/UA sharing is N/A** — `pkg/imggen` never imports `pkg/providers`; the imggen path sends no session/UA attribution, by construction (minimax-gated rows vs opencode-only attribution). Item 3 of the commission dissolves.
4. **`$0.0035/call` is not repo-evidenced** (0 grep hits; the figure matches a different system). Keep the tooltip as a labeled hint.
5. **"An embed regression test pins the same-commit rule" — false.** The test pins internal self-consistency only; a stale bundle passes. Convention, not a gate (§6).
6. **"Reuse the existing image rendering" — impossible; none exists.** Zero `<img>`/base64/blob-URL code in the FE. The image element is net-new; only the result-panel chrome is reusable.
7. **Decoy-credential test provenance:** `101bcfd` (`TestHandleDecisionTest_UsesFirstCredentialByExactID`, :372), not `c63553f` (which is the zen/typesafe arm pin, :445).
8. **No machinery-reuse proof test exists in `pkg/decision`** — its reuse is structural. The cross-path parity pattern lives in the **chat** lane (`TestHandleTestModel_CrossPathParity_RealVsTestButton`); mirror that for imggen.
9. **The FE is Preact**, not React (`jsxImportSource: "preact"`); tests are vitest + `@testing-library/preact`.
10. **The decision lane's BE emits numeric status + reason + short message; the labels live in the FE** — a verbatim classifier copy is additionally wrong because MiniMax errors ride in HTTP 200 (§3.1).

---

## 11. Decisions pending (leader)

1. **Outcome extension vs zero-production-touch** (§3.4): recommended = additive `Code`/`Message` fields (2 lines, keyed literals, behavior-preserving). Alternative = parse `Reason` in the UI layer. Pick one before implementation.
2. **Metering call confirmed?** Recommendation is no-meter + log line + cooldown (§4). If the team wants probe spend in the usage dashboards, that is a deliberate divergence requiring a synthetic-token metering design — not covered here.
3. **`/fe/api` authentication gap** (pre-existing, 🔴 from the resilience report): the probe adds a money-spending surface to an unauthenticated API family. Minimum mitigation here is the in-flight cap + cooldown. Binding the UI to loopback or gating on the proxy token is an infrastructure decision outside this feature's scope — flagged for the leader.

## 12. Open questions

- **MiniMax `base_resp.status_code` catalogue** — only `2013` is pinned in-repo. Live-probe before extending the label map; ship generic labels + numeric code meanwhile.
- **Whether MiniMax ever returns real HTTP 401/402/429** (vs always-200-with-base_resp). The classifier handles both; some status rows may be unreachable for this provider.
- **Image URL lifetime** — not recorded in-repo; FE renders immediately, does not promise persistence.
- **Live `data` shape** (object vs array-of-objects) — extractor tolerates both; confirm on first live probe.
- **Intermediate proxies** cutting a 4-minute `/fe/api` request in some deployments (server `writeTimeout` floor is 5 min — `cmd/main.go:341-347`; `apiFetch` sets no client timeout).
