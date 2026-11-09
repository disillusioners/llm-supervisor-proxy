# Independent Verification Gate — 9router image-gen provider

- **Date:** 2026-11-09 (UTC)
- **Commission:** TEST & VERIFY — 9router image-gen provider (`pkg/imggen/ninerouter.go` + six drift-locked sites + rebuilt FE bundle)
- **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-9router-imggen`
- **Branch:** `feature/9router-imggen-provider`
- **HEAD verified:** `7a6a45b4af87a65a86b63fff4f6a270de9bfc49b` (`7a6a45b`), clean (`git status --porcelain` empty at start AND after every worker)
- **Base:** `432362f`; 5 commits (`cc26b69 → 307d9ef → d115f89 → eb0ba85 → 7a6a45b`); 29 files, +2098/−141
- **Verifier:** tester (dispatch-only); 11 worker instances, every run dual-layer-timeout wrapped
- **Boundaries honored:** no deploy, no cluster mutation (one `port-forward` tunnel, torn down), no code fixes, no merge, no push, API key never echoed/logged

## Verdict

**PASS-WITH-DISCLOSURES**

All three gates pass. Zero new regressions, zero vacuous tests, both mutation controls VALID, HTTP 200 end-to-end against the real upstream, and the embedded bundle carries the drift-locked vocabulary. Disclosures D1–D4 below must be carried into the commission report.

- Gate 1 (regression): **PASS** — 42/42 packages green except 2 deterministic quarantined `pkg/mcp` httpbin.org failures (pre-existing, row 11).
- Gate 2 (specific verification): **PASS** — parity/rejection receipts, both mutation controls RED→GREEN, bundle literal served.
- Gate 3 (local-boot e2e): **PASS** — HTTP 200, JPEG `data:image/jpeg;base64,`, 541,925 bytes, 30.2 s.

---

## Scope Decision

Full regression was explicitly ordered and is **warranted**: the change set is cross-module (pkg/imggen + pkg/models + pkg/providers + pkg/store/database + pkg/ui + pkg/ui/frontend + embedded bundle, 29 files) and touches shared provider-kind validation gates used by every model kind. The full suite was executed, split into the project's documented RELEASE-GATE slices A/B to stay inside the 5-minute per-pack cap (equivalent coverage, no package omitted):

| Slice | Packages | Result |
|---|---|---|
| A | `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (9 pkgs) | **PASS** — exit 0, 117 s wall (`pkg/proxy` 109.334 s), real `~/.config` hash unchanged |
| B | complement of A (33 pkgs, module-prefix-anchored `go list`) | **exit 1 — 31/33 ok; 2 failures both PRE-EXISTING-QUARANTINED** (`pkg/mcp` httpbin.org redirect pair, QUARANTINE row 11); 0 NEW |

`pkg/proxy` 109.3 s and `pkg/credentiallb`/`pkg/usage` green ⇒ no cap pressure observed.

### Known-failure reconciliation (no base re-run needed)
- `TestStoreEngine_CloseLifecycle` (QUARANTINE row 7) — **fired once**, in the Gate 2.2/2.3 verbose run on `pkg/store/database` (`testing.go:1464` `TempDir RemoveAll cleanup: unlinkat … directory not empty`; test assertions never failed, body 0.23 s), and **passed** in this gate's slice-A run (`pkg/store/database` ok 30.349 s) ⇒ flake confirmed on Linux, **not** macOS-only. QUARANTINE row 7 wording updated to record the observation.
- `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (row 10) — **did not fire**; `pkg/credentiallb` ok 3.8 s.
- `pkg/usage` stress pair (row 9) — **passed**, no SQLITE_BUSY.
- `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` (row 11) — **fired deterministically** in slice B with the exact recorded signature (`handlers_api_test.go:1865/:1905`, httpbin.org `403` before the classifier). Byte-identical to the 2026-11-06 base proof already in QUARANTINE.md ⇒ classified pre-existing, not re-proven at base (already proven there).
- `TestValidateUpstreamURL` SSRF octal/decimal/hex subtests — **passed** (consistent with the 2026-11-06 observation; formal un-quarantine still pending protocol 3× isolated).

---

## Gate 1 — Full regression

### Go

| Run | Command | Result |
|---|---|---|
| A | `timeout 300 go test ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/... -count=1 -timeout=240s` | **PASS**, exit 0, 117 s |
| B | `timeout 300 go test $(go list ./... \| grep -vE '^github.com/disillusioners/llm-supervisor-proxy/(cmd\|pkg/(proxy\|store\|decision\|ui))(/$\|$)') -count=1 -timeout=240s` | **exit 1**, 51 s, 31 ok / 2 quarantined |

Per-package (A): `pkg/proxy` 109.334 s, `pkg/proxy/normalizers` 0.023 s, `pkg/proxy/token` 0.885 s, `pkg/proxy/translator` 0.061 s, `pkg/store` 0.014 s, `pkg/store/database` 30.349 s, `pkg/decision` 0.923 s, `pkg/ui` 2.211 s, `cmd` 0.014 s — all `ok`.
Per-package (B) notable: `pkg/imggen` ok, `pkg/models` ok, `pkg/providers` ok, `pkg/credentiallb` ok 3.8 s, `pkg/usage` ok 45.9 s, `pkg/modelscache` ok 7.9 s; `pkg/mcp` **FAIL** 19.6 s (2 quarantined only).

Both runs used `HOME=$(mktemp -d)` + `XDG_CONFIG_HOME=$HOME/.config`; slice A additionally verified the real `~/.config/llm-supervisor-proxy/config.json` sha256 unchanged (`68a06693…3080c8`).

### Frontend

| Run | Result |
|---|---|
| `cd pkg/ui/frontend && timeout 300 npx --no-install vitest run` | **PASS** — 24 files / 252 tests, 0 fail, 0 skip, 13 s (imggen area 55 tests, SettingsPage tabs 15, windowing 36, RequestDetail 16, tsc-baseline comparator 5) |
| `cd pkg/ui/frontend && timeout 240 npx --no-install tsc --noEmit` | **PASS by policy** — 19 errors / 9 files, byte-identical per-file counts to `tsc-baseline.txt` (19/9) ⇒ **zero NEW error locations** (see D3) |

### Build / vet / fmt

| Check | Result |
|---|---|
| `test/packs/build_gate_test.sh` | **PASS** (go build + go vet), 4.8 s |
| `go build -o /tmp/lsp-9r ./cmd` | **PASS** — 26,345,740 B ELF x86-64 (real `./cmd`, not the 3.7 MB repo-root stub) |
| `gofmt -l` on the 18 lane-changed `.go` files | **empty — ZERO new drift** (repo-wide count 52, pre-existing baseline ≈60) |
| `go vet ./pkg/imggen/ ./pkg/models/ ./pkg/ui/ ./cmd/` | **clean**, no diagnostics |
| build-tag scan of lane files | `NO_BUILD_TAGS` — nothing skipped by `./...` |

### Anti-vacuous (37 added Go tests)

Enumerated independently from `432362f..HEAD` (9 changed test files, 4 added):

| File | Added | All ran? | All `--- PASS`? |
|---|---|---|---|
| `pkg/imggen/ninerouter_test.go` | 25 | ✅ | ✅ |
| `pkg/imggen/handler_9router_test.go` | 6 | ✅ | ✅ |
| `pkg/imggen/test_exec_b64_test.go` | 2 | ✅ | ✅ |
| `pkg/ui/handlers_models_kind_test.go` | 2 | ✅ | ✅ |
| `pkg/models/config_imggen_test.go` | 1 | ✅ | ✅ |
| `pkg/store/database/models_manager_kind_test.go` | 1 | ✅ | ✅ |
| **TOTAL** | **37** | **37/37** | **37/37** |

Single `-count=1 -v` run over `pkg/imggen ./pkg/ui ./pkg/models ./pkg/store/database`: 537 top-level RUN = 533 PASS + 4 SKIP (`TestPostgreSQL*`, gated on unset `TEST_DATABASE_URL`, pre-existing) + 452 subtest PASS, **0 FAIL**. Exact-boundary matching used (no prefix masking — `TestNineRouter_NormalizeErrorBody` vs `_VerbatimOnNoMessage` both distinct). **Not-run list: EMPTY.**

---

## Gate 2 — Specific verification ordered by the reviewer

### 2.1 Mutation controls (independent, scratch copy via `git archive`)

Baseline in scratch: GREEN (`pkg/imggen` ok 0.805 s, 100 tests).

| Control | Mutation (exact seam) | Result | Verdict |
|---|---|---|---|
| MUT1 — conversion disabled | `pkg/imggen/handler.go:457` `if rw, ok := provider.(ResponseRewriter); ok && false` | **RED — 3 tests**: `TestHandler_9Router_ConversionRelay` (:103 "body should have been converted…"), `TestHandler_9Router_URLShapedUpstreamResponse` (:134 status 200 want 502), `TestHandler_9Router_PartialConversion_CountsAndBilling` (:228 len 2 want 1). 97 others PASS. Restore → GREEN | **VALID** |
| MUT2 — conversion forced onto minimax | new `func (p *minimaxProvider) RewriteResponse(...)` delegating to `nineRouterProvider.RewriteResponse` | **RED — 5 tests**: `TestHandler_Minimax_ByteIdenticalStill` (handler_test.go:366), `TestHandler_Success_ByteIdenticalRelay` (:366), `TestMinimax_HasNoResponseRewriter`, plus collateral `TestHandler_Upstream_GzipNegotiationRoundTrip` (:566) and `TestHandler_Metering_PartialSuccess_ImageCount2` (:973 image_count 1 want 2). Restore → GREEN | **VALID** |

Restores were byte-identical (`diff -r` empty) and the worktree was never touched (`git status` empty after).

### 2.2 / 2.3 Parity and rejection gates

**Run:** `timeout 300 go test ./pkg/imggen/ ./pkg/models/ ./pkg/ui/ ./pkg/store/database/ -v -count=1 -timeout=240s` → **exit 1**, 34.9 s, 537 top-level (532 PASS / 4 SKIP / 1 FAIL) + 452 subtest PASS. The single FAIL is `TestStoreEngine_CloseLifecycle` — QUARANTINE row 7 flake (see reconciliation above); it passed in slice A the same gate.

| Item | Receipt | Verdict |
|---|---|---|
| **a. Parity — both providers valid for image-gen** | `TestValidateKindRules_ImageGen_AllRules/valid_image-gen` **and** `/valid_image-gen_9router` PASS (`provider: "minimax"` / `"9router"`, `wantErrSub: ""`); `TestModelsManager_AddModel_KindImageGen_Valid` + `..._Valid9Router` PASS; `TestHandleModels_POST_AcceptsValidImageGen` + `..._AcceptsValidImageGen9Router` PASS (201). FE locks the dropdown: `the provider options list contains exactly minimax and 9router` (`ImgGenModelForm.test.tsx:430`) | **PASS** |
| **b. `openai` rejected for image-gen at every backend gate** | (1) model `pkg/models/config.go:252` → subtest `image-gen_requires_minimax_or_9router_provider`, expects `requires the primary credential's provider to be "minimax" or "9router" (got "openai")`; (2) store `pkg/store/database/store.go:2088` → `..._Violations/image-gen_with_non-minimax_provider`; (3) HTTP POST `pkg/ui/server.go:1122` → `TestHandleModels_POST_RejectsImageGen_NonMiniMaxProvider` asserts 400; (4) HTTP PUT `pkg/ui/server.go:1581` → gate present, logic byte-identical to POST — **no dedicated standalone PUT+openai+400 test** (see D5) | **PASS (one test-coverage gap disclosed)** |
| **c. 9router rejected for chat / decision kinds** | model layer: `TestValidateKindRules_Chat_RejectsNineRouter` PASS — asserts the literal `cannot use "9router" provider (image-generation only)` for both `KindChat` and empty kind (defaults to chat); decision allowlist `openai/typesafe/openrouter` (`config.go:300-305`) pinned by `TestKindRules_Parity_AllThreeSitesInSync/D4_decision_provider_gate_rejects_minimax`; decision+9router proven at HTTP level in (d) | **PASS** |
| **d. HTTP-level decision POST/PUT + 9router → 400** | `TestHandleModels_POST_and_PUT_ChatAndDecisionRejectNineRouter` PASS (7 sub-assertions): POST decision → `400` + `kind "decision" requires the primary credential's provider to be …`; PUT decision → `400`; `PUT /fe/api/models/g9r-img-model` kind-flip also 400 in Gate 3 with the row verified unmutated | **PASS** |
| **e. chat + openai acceptance** | HTTP level **exists**: `TestHandleModels_POST_CreatesModelWithSecondaryUpstreamModel`, `..._AcceptsEmptySecondaryWithInternal`, `..._IgnoresLegacyCredentialIDWhenCredentialsPresent`, `..._AcceptsMultiCredentialAndStampsPositions` all POST `provider: "openai"` and assert **201**. Model-layer pin `TestValidateKindRules_Chat_AlwaysValid` PASS. No coverage gap | **PASS** |
| **f. GET-after-rejection persisted state** | Same test re-GETs `/fe/api/models` and asserts: `chat-9r`, `chat-9r-empty`, `dec-9r` absent; `chat-existing` / `dec-existing` not overwritten with `ninerouter-cred`; `img-9r` **was** persisted (`handlers_models_kind_test.go:562-607`) | **PASS** |
| **g. 9router accepted for image-gen (model + HTTP)** | `TestModelsManager_AddModel_KindImageGen_Valid9Router` PASS (0.25 s, accepts `ag/gemini-3.8-flash`); `TestHandleModels_POST_AcceptsValidImageGen9Router` PASS (201) | **PASS** |

**Drift-locked sites — all present in `432362f..HEAD` (9 hunks / 6 sites):**
1. `pkg/models/credential.go:23` — `"9router": true` in `validProviders`.
2. `pkg/models/config.go:221-224` — chat (and empty-kind) rejects 9router; `:252-254` — image-gen allows `minimax` **or** `9router`.
3. `pkg/ui/server.go:1074-1087` (POST chat reject), `:1122-1127` (POST image-gen allow), `:1544-1557` (PUT chat reject), `:1581-1586` (PUT image-gen allow).
4. `pkg/ui/frontend/src/types.ts:108` — `InternalProvider` union widened with `'9router'`.
5. `pkg/ui/frontend/src/components/config/ImgGenModelForm.tsx:6-30` — `PROVIDER_OPTIONS = ['minimax','9router']` + `DEFAULT_BASE_URLS` incl. `http://ninerouter.9router.svc.cluster.local:20128/v1`.
6. Embedded bundle regenerated in `7a6a45b` (SettingsPage chunk + index renames, no stale hashed leftovers).

`pkg/providers/factory_test.go` (modified, 0 added tests) ran green in slice B (`pkg/providers ok`).

### 2.4 Embedded bundle

| Check | Receipt |
|---|---|
| Shipped literal | `pkg/ui/static/assets/SettingsPage-BPmx97kG.js:1` — `kind "chat" cannot use "9router" provider (image-generation only)` (byte-identical to `ModelForm.tsx:334`). 188,241 B, sha256 `0ae3e1e0e281e957b0ce31b12ca59625906bbc8047aae73fe264ab8b191fde9a` |
| `9router` in static assets | only `SettingsPage-BPmx97kG.js` (2×): the guard above + `availableCredentials:w.filter(f=>f.provider!=="9router")` (9router hidden from chat credential pickers) |
| `index.html` refs | 2/2 EXIST — `assets/index-RYocBZYl.js` (310,829 B), `assets/index-B9lW4EHC.css` (52,568 B) |
| Lazy-chunk reachability | entry chunk contains `import("./SettingsPage-BPmx97kG.js")` and that file exists |
| Embed coverage | single directive `//go:embed static/*` (server.go:90) — Go embed matches the `assets` **directory** and embeds the subtree recursively; no second directive needed, none exists. No `.`/`_` entries that the exclusion rule could drop |
| Drift-locked vocabulary in bundle | `const ke=["minimax","9router"]`, `Gt="image-gen"`, default base URL map incl. `"9router":"http://ninerouter.9router.svc.cluster.local:20128/v1"` |

---

## Gate 3 — Local-boot end-to-end smoke (real upstream, local instance only)

Fenced boot: throwaway `HOME=/tmp/g9r-home-AoCs`, `DATABASE_URL=` (→ SQLite `…/config.db`), `APPLY_ENV_OVERRIDES=1 PORT=4321`, `ENSEMBLE_SELF_ENV=dev`; binary rebuilt from `7a6a45b` (26,345,740 B). `/healthz` 200 in 2 s.

| Step | Result |
|---|---|
| Credential create (`POST /fe/api/credentials`, provider `9router`, api_key from `/tmp/9router.key` via shell var) | **201**, id `g9r-smoke-cred`, key masked in response, never echoed |
| Model create (`POST /fe/api/models`, kind `image-gen`, `internal_model=ag/gemini-3.8-flash`) | **201**, row `g9r-img-model`, retrievable via `GET /fe/api/models?kind=image-gen` |
| Tunnel `kubectl -n 9router port-forward svc/ninerouter 20128:20128` | `Forwarding from 127.0.0.1:20128` in 2 s; upstream `GET /v1/models` → **200** with real JSON (`ag/gemini-4-argon-high`, …) — API not dashboard |
| **`POST /v1/image_generation`** `{"model":"g9r-img-model","prompt":"a red cube on a white table","n":1}` | **HTTP 200**, **30,191 ms**, `data[0].image_urls[0]` prefix **`data:image/jpeg;base64,`**, decoded **541,925 B**, magic **`FF D8 FF E0`** (JPEG/JFIF), envelope `{base_resp, created, data, metadata}` |
| 429 path | **not taken** — account quota had reset (12:59 Z); nothing to assert |
| Usage metering | `GET /fe/api/usage/summary` token `gate3-smoke`: `total_requests:2` (the 404 diagnostic + the 200); `GET /fe/api/usage/models` `g9r-img-model request_count:2`; prompt/completion tokens 0 (image bytes are counted via `IncrementModelImages`, not token-metered); image-gen calls do not appear in the chat `/fe/api/requests` ring |
| UI (a) served assets | `/ui/` 200 → `index-RYocBZYl.js`; lazy chunk fetched over HTTP: 200, 188,241 B, `grep -c 9router` = **2**; served `const ke=["minimax","9router"]`, the chat-kind guard literal, and "Provider must be minimax or 9router" |
| UI (b) dropdown shows 9router | served array + imggen row retrievable — **confirmed** |
| UI (c) 9router not selectable for chat/decision | `POST /fe/api/models` chat+9router → **400** `kind "chat" cannot use "9router" provider (image-generation only)`; decision+9router → **400** `kind "decision" requires the primary credential's provider to be "openai", "typesafe" or "openrouter" (got "9router")`; `PUT` kind-flip image-gen→decision on an existing row → **400**, row verified **not mutated** (still `image-gen`) |
| UI (d) probe on b64_json | real upstream: `POST /fe/api/image_generation/test {"model_id":"g9r-img-model"}` → **200**, 13,371 ms, `{success:true, provider:"9router", status:200, upstream_status:200, code:"ok", image_count:1, first_url:"data:image/jpeg;base64,/9j/4…"}`; log `images=1 duration_ms=13338 success=true`. Local 10180 mock **not needed** (upstream healthy) |
| Teardown | server + tunnel killed (a `pkill -f 'port-forward.*ninerouter'` self-matched the invoking shell, recovered with the bracket pattern `'port-forward svc/nineroute[r]'`); `PODS_UNCHANGED` (diff empty; `9router-768bd98b94-5h6t9` + `oauth2-proxy-76554b65b4-bgdvm`, restarts 0); `PORTS_CLEAN` (4321/20128/10180 free, **8088 never touched**); throwaway home + SQLite DB removed; `/tmp/9router.key` preserved; worktree `git status` empty |

---

## Disclosures the leader must carry

- **D1 — `base_url` must end in `/v1` (operator trap).** The 9router adapter appends only `/images/generations` to `base_url`. The commission's prescribed `base_url=http://127.0.0.1:20128` therefore produced an **HTML 404 in 79 ms** (9router's Next.js dashboard catch-all), not a JSON error. Verification corrected it config-side to `http://127.0.0.1:20128/v1` (no code change) and the call succeeded. The FE already ships the correct default (`…svc.cluster.local:20128/v1`), so this is a commissioning/ops footgun rather than a product defect — but the commission report must state the `/v1` requirement explicitly.
- **D2 — latency tail.** 30.2 s this run vs the 5-hour-old in-cluster p50 17.7 s / max 21.4 s. Still 4.0× inside the 120 s `ImageGenTimeout`, but the tail is drifting upward and worth a watch item.
- **D3 — tsc gate is policy-pass, not zero-error.** Observed 19 errors / 9 files = the frozen baseline exactly (per-file identical). Two erroring files (`CredentialsTab.tsx`, `ModelForm.tsx`) were touched by the lane, but the errors are pre-existing unused-symbol debt at untouched lines. By the ensure.md criterion ("zero NEW error locations vs the documented baseline") this is PASS; by a stricter "no error may exist in a lane-touched file" reading it would fail. The ensure.md wording supports PASS.
- **D4 — local `/v1/*` needs a token.** The proxy requires a Bearer token even on a fresh local boot (`/v1/*` auth store is always non-nil) — `POST /fe/api/tokens` first. Not a defect; a local-smoke prerequisite future gates must know.
- **D5 — one test-coverage gap, no behavioral gap.** The PUT image-gen gate (`pkg/ui/server.go:1581`) rejects `openai` with logic byte-identical to POST, but there is **no dedicated standalone test** that PUTs an image-gen model with an `openai` credential and asserts 400. POST has one (`TestHandleModels_POST_RejectsImageGen_NonMiniMaxProvider`), and the model layer covers the rule. Also, `GetProviderTypes` (`pkg/providers/factory_test.go`) asserts the 9router registration only indirectly — it ran green in slice B, but the lane added no test asserting the factory registry lists `9router`. Both are nice-to-have hardening items, not gate failures.
- **D6 — `TestStoreEngine_CloseLifecycle` fired once this gate** (pkg/store/database verbose run) and passed in slice A ⇒ the quarantine row is a real Linux flake, not macOS-only. QUARANTINE row 7 wording corrected; skip-wiring remains deferred (unchanged policy).

### Also noted (not defects)
- `pkg/mcp` deterministic pair remains QUARANTINED (row 11) with skip-wiring still deferred — unchanged by this lane.
- `TestValidateUpstreamURL` SSRF subtests passed again (2nd consecutive gate) — formal un-quarantine still needs 3× isolated runs.

---

## ensure.md status

| # | Requirement | Status |
|---|---|---|
| Critical 1 | All Go unit tests pass (`go test ./...`) | **PASS with quarantine** — full suite green except 2 deterministic `pkg/mcp` failures already quarantined (row 11) |
| Critical 2 | `go vet ./...` clean | **PASS** |
| Critical 3 | Full project builds | **PASS** (`go build ./cmd`) |
| Critical 4 | `tsc --noEmit` zero NEW errors vs baseline | **PASS** (19/9 = baseline, per-file identical) |
| Important | Peak-hour logic / API 400 / round-trip / migration 018 | **not in blast radius** (no peak-hour or migration code touched by this lane); not re-validated this gate |
| Nice-to-have | no races / boundary coverage | Not run (no `-race` in this gate; repo-wide `-race` known red in `pkg/proxy` — pre-existing) |

---

## Artifacts

- Slice A raw log `/tmp/sliceA_release_gate.log` (worker-side, ephemeral)
- Anti-vacuous log `/tmp/antivac_9r.log` (3,957 lines)
- Parity log `/tmp/parity_9r.log` (worker-side)
- Mutation logs `/tmp/mut1.log` (worker-side, scratch deleted)
- This report; worktree HEAD unchanged at `7a6a45b`, no commits made
