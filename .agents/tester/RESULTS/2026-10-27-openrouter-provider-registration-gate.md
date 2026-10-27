# Test Report: openrouter provider-registration gate

- **Date:** 2026-10-27 (18:16–01:31 UTC window; worker execution 01:21–01:31)
- **Branch/HEAD:** `feature/openrouter-provider` @ `2be5d47` (base `63df316`) + one NEW test-file commit `815a9ff` added by this gate
- **Instance IDs (workers):** f2546964 (full regression), a81466d5 (runtime smoke ×2 turns), a2349199 (build/vet gate), 72d39e9a (FE vitest ×2 turns)
- **Change under test:** "openrouter" registered as plain OpenAI-compatible chat brand — 5 sites `pkg/providers/factory.go`, `validProviders` `pkg/models/credential.go`, FE `types.ts` union (1 line), README row, new `pkg/providers/openrouter_test.go` (362 lines). Config-only; reviewer verdict PASS (0 critical, 0 warnings).

## VERDICT: **PASS — SHIP** (4/4 gate packs green; all 4 ensure.md Critical requirements validated)

### Summary
- Gate packs: 4/4 PASS (full Go regression, runtime registration smoke, build+vet, FE vitest+tsc-baseline)
- Go suite: 41/41 packages ok (38 test-bearing + 3 no-test-files), 86s wall, `-count=1`, go1.26.1 darwin/arm64
- Runtime smoke: 18/18 assertions; credential "openrouter" ACCEPTED (201); both negative controls 400
- ensure.md Critical: 4/4 passed. Quarantined tests skipped/fired: 0 fired (all passed run-1)
- Quick fixes applied: 0 to repo source (gate-independence preserved); 2 to the throwaway→committed smoke script itself
- New committed test artifact: `test/test_provider_registration_smoke.sh` @ **815a9ff**

### Scope Decision
Full suite **warranted**: ensure.md Critical #1 maps to `go test ./...`; `pkg/providers/factory.go` is a cross-module seam (proxy, credentiallb, ultimatemodel, ui all consume the provider registry); the leader explicitly required full regression after scoped-only developer runs. FE heavy automation descoped per leader (types.ts documentation-grade, embedded bundle not rebuilt); the cheap FE unit gate was retained because `types.ts` is in the change set and ensure.md Critical #4 (zero NEW tsc errors) depends on it.

### ensure.md Validation Results (blast-radius scoped: change set = providers/models/types.ts/README/tests)
- **Critical Requirements: 4/4 passed**
  - ✅ All Go unit tests pass (`go test ./...`) — 41/41 packages, 0 failures, isolated HOME
  - ✅ `go vet ./...` passes with no issues — via build_gate_test pack
  - ✅ Full project builds without compilation errors — `go build ./...` exit 0 (also independently type-checked the new _test.go via vet)
  - ✅ tsc zero NEW error locations vs baseline — via in-vitest comparator (5/5): TOTAL_ERRORS=19 / TOTAL_FILES=9 unchanged, `types.ts` not among error files
- **Important Requirements: 0/4 in scope** — peak-hour logic (×3) and migration 018 are untouched by this change set (no peak-hour fields, no migration in the diff); not validated this gate
- **Nice-to-have: not in scope** — no `-race` run this gate (plain `-count=1` per the leader's task 1); prior race slices green at ancestors

### Per-task results (leader's task list)

**Task 1 — Full regression: PASS.** `timeout 300 go test ./... -count=1 -timeout 270s`, isolated HOME (`/tmp/or-gate-regression`), real GOCACHE/GOMODCACHE pinned, `DATABASE_URL` unset. 38 `ok` + 3 `[no test files]` = 41/41 = `go list ./...` count. 86s wall (long pole pkg/proxy 77.9s). Slowest others: mcp 16.5s, store/database 9.0s, test 6.0s, ultimatemodel 5.5s. Zero failures; zero `--- FAIL`; zero panic; zero DATA RACE. New tripwire executed all 5 tests unguarded (no Skip/Short guards): `TestNewProvider_OpenRouter_AcceptsOverrideURL`, `TestNewProvider_OpenRouter_DefaultBaseURL_Locked`, `TestOpenRouter_ChatCompletion_Success`, `TestOpenRouter_ErrorEnvelope`, `TestOpenRouter_StreamChatCompletion`. All 5 factory sites + `credential.go:53` present and exercised. Two documented command deviations (both required by the task's own constraints, neither altering semantics): cache env pinning (HOME override alone relocates Go caches → cold re-download would blow the cap) and `set -o pipefail` + `PIPESTATUS[0]` (tee masks exit codes).

**Task 2 — Runtime registration smoke: PASS (18/18).** Real binary (`go build ./cmd`), isolated HOME + XDG_CONFIG_HOME, `APPLY_ENV_OVERRIDES=1 PORT=18240`, SQLite landed in scratch (126976 bytes); developer real DB mtime unchanged (only ever `stat`ed). Evidence:
- `GET /fe/api/providers` → 200, entry verbatim: `{"type":"openrouter","name":"OpenRouter","base_url":"https://openrouter.ai/api/v1","color":"purple","description":"OpenRouter (OpenAI-compatible chat-completions; multi-vendor model router; default base URL https://openrouter.ai/api/v1)"}` — the credential dropdown's data source is correct.
- Credential create `{"id":"smoke-openrouter-cred","provider":"openrouter","api_key":"sk-smoke-openrouter-test"}` → **201** `{"id":"smoke-openrouter-cred","provider":"openrouter","api_key":"sk-smoke***"}`; persisted via GET /fe/api/credentials. **The DeepSeek-class bug cannot recur through this path for openrouter.**
- Model validate → 200 `{"valid":true}`; model create → **201** with `credentials:[{credential_id:"smoke-openrouter-cred",weight:1,position:0}]`.
- NEG `"openrouter "` → **400** `{"error":"credential smoke-openrouter-trailspace: invalid provider: openrouter "}`; NEG `"OpenRouter"` → **400** `{"error":"credential smoke-openrouter-wrongcase: invalid provider: OpenRouter"}` — lowercase-exact confirmed at the runtime allowlist, not just in unit tests.
- EXTRA shared-provider rule: openrouter+deepseek credentials on one model → **400** `provider "deepseek" does not match primary provider "smoke-openrouter-cred"`.
- `/healthz` 200; `/ui/` 200 `text/html; charset=utf-8`, `<!DOCTYPE html>`; SIGTERM → graceful `Shutting down server...` → `Server exiting`, exit 0, port freed. Boot banner: `LLM Supervisor Proxy (build dev) starting on port 18240` / `Using SQLite database for local development`.

**Task 3 — No live network to openrouter.ai: HONORED.** Verified by source read BEFORE probing: `pkg/models` + `pkg/store/database` contain zero outbound HTTP (`http.NewRequest|http.Post|http.Get|Client.Do` greps empty) — credential-create/model-validate are offline allowlist/schema checks. Upstream-dialing endpoints (`/fe/api/models/test`, `/v1/chat/completions`) never called. Committed pack enforces `GOPROXY=off` — zero egress, no key needed.

**Task 4 — Build/boot smoke: PASS.** `go build ./...` exit 0 + `go vet ./...` clean via registered `test/packs/build_gate_test.sh` (5s). Boot/healthz/UI-serve/shutdown proven in Task 2. Embedded UI serves without rebuild (static assets pre-existing; types.ts is type-level only).

### Quarantine status (no coverage impact this run)
All 4 active QUARANTINE.md entries ran and PASSED first-try (store CloseLifecycle, mcp SSRF subtests, usage stress pair, credentiallb TOCTOU) — no adjudications, no isolation re-runs needed. `TestB1_UltimateModelAllowed` (skip-carried by the 2026-10-23 gate citing a QUARANTINE.md 2026-10-18 entry that no longer exists) PASSED run-1 with no skip and no panic — evidence that citation is stale on this host. Quarantined-and-skipped count contributing to pack results: 0.

### Known backlog items (NOT filed as failures, per brief)
FE placeholder for openrouter model-id; tokenizer prefix fallback for vendor-prefixed IDs; duplicate purple badge color; pre-existing gofmt drift in 8 untouched files. Environmental (not product): repo `pkg/ui/frontend/node_modules` stale since 2026-05-22 (lockfile is 2026-09-22; vitest/happy-dom/@testing-library absent) — FE pack ran from an isolated copy; `npm ci` in the repo remains the owner's call.

### Quick Fixes Applied
- None to repo source (verification-gate independence preserved — no feature code touched during the gate).
- Worker a81466d5, own smoke script only (pre-commit): (a) platform-agnostic DB-path discovery (macOS `os.UserConfigDir()` → `~/Library/Application Support`, blueprint's `~/.config` is wrong on macOS); (b) watchdog fd redirect (background subshell held the tee pipe open). Both carried into the committed pack along with a third hardening (8088 guard was unreachable dead code behind the generic <10000 branch — reordered).

### Code Changes Summary (committed BEFORE this report)
- `test/test_provider_registration_smoke.sh` (+526 lines, executable) — reusable parameterized provider-registration runtime gate; proven brand-generic (openrouter defaults + explicit args + deepseek cross-brand, 18/18 each); safety-hardened (isolated HOME+XDG, 8088/<10000/foreign-holder aborts, own-PID-only cleanup, dual-layer timeout, zero egress)
- **Commit: `815a9ff`** (`test: add reusable provider-registration runtime smoke pack (binary boot, isolated HOME, parameterized brand)`) — exactly one file staged; `.agents/tidier/notes.md` never staged/stashed/reverted; branch unchanged.
- Documentation (PACKS.md, MOCK_TESTS.md, this file) updated in working tree, intentionally left UNCOMMITTED per the leader's "commit only the new test file" constraint.

### Documentation Updated
- [x] PACKS.md — new pack row + dated gate section (4 ad-hoc gate packs)
- [x] MOCK_TESTS.md — full spec for provider_registration_smoke
- [x] RESULTS/2026-10-27-openrouter-provider-registration-gate.md — this report
- [ ] rules/ensure.md — no changes (user-maintained, read-only)
- [ ] QUARANTINE.md — no changes (nothing fired; B1 note recorded here and in PACKS.md)
- [x] KB — provider-registration verification standard + macOS SQLite path + stale node_modules gotcha recorded via experience()

### Gaps
None. All leader tasks covered; no incomplete nodes.

## Overall Status
- Full regression: ✅ PASS · Runtime registration: ✅ PASS (18/18) · No-network rule: ✅ honored · Build/boot: ✅ PASS
- ensure.md Critical: ✅ 4/4
- **Testing Complete: ✅ READY — SHIP**
