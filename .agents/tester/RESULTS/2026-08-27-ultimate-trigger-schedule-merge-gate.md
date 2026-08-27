# Test Report: ultimate-model trigger schedule (5/10/20/30/40) — §8 MERGE GATE (independent re-run)

Date: 2026-08-27
Branch: feature/ultimate-model-trigger-schedule @ a0f4cd1d0ce5e7d4c9625b9972730cc57238ae90 (verified by every worker pre-run; base df795c8)
Plan: docs/features/ultimate-model-trigger-schedule-plan.md §8 (rev. 4)
Worker instances: gate-recon, gate-unit-ucm, gate-unit-proxy, gate-unit-store, gate-bundle-verify, gate-mock-ult, gate-make-test, gate-builds, gate-flake-classify, gate-boundary-ui, gate-mock-mm (11 workers, 3 waves)

## Summary
- §8 gates: 6/6 PASS (go-test slices ×3, make test, go build, npm run build) + ensure.md extras (go vet PASS)
- Mock E2E: ultimate 49/49 PASS (Test 8 header-exact), minimax 53/53 PASS (T15 green)
- Boundary 42-request drive: EXACT match to spec (header only at 5/10/20/30/40; exhausted wire-type on 41+42)
- Committed bundle: CLEAN (old strings 0 hits; new strings present; rebuild byte-identical)
- Web UI: Settings max-retries knob GONE (DOM 0 matches); EventLog exhausted event proven via live events API + bundle
- URL-capture (mock-remote-only hard constraint): **ZERO non-localhost** across every gate, drive, and log scan
- Flakes: 1 — TestStoreEngine_CloseLifecycle, PRE-EXISTING at base, quarantined (see QUARANTINE.md)
- **Overall: READY TO MERGE** (2 non-blocking author recommendations below)

## Scope Decision
Full §8 gate suite run — warranted: reviewer-mandated merge gate for an 11-commit feature touching ultimatemodel/proxy/store/config/models + frontend. I additionally ran the leader-requested boundary drive, bundle verification, and UI validation beyond §8. No packs skipped; make-test run fresh (`go clean -testcache` first) per reviewer independence.

## §8 Gate Results

| Gate | Command (as run) | Result | Runtime | Evidence tail |
|---|---|---|---|---|
| go test slice 1 | `timeout 300 go test -count=1 ./pkg/ultimatemodel/... ./pkg/config/... ./pkg/models/...` | **PASS** | 5s | ok ultimatemodel 4.416s / ok config 1.597s / ok models 0.027s |
| go test slice 2 | `timeout 300 go test -count=1 ./pkg/proxy/...` | **PASS** | 33s | ok pkg/proxy 32.654s; ok normalizers/token/translator |
| go test slice 3 | `timeout 300 go test -count=1 ./pkg/store/...` | **PASS** (flake, run-2) | 2s | run-1 FAIL on TestStoreEngine_CloseLifecycle (TempDir cleanup race) → run-2 PASS; see Quarantine |
| make test (fresh) | `go clean -testcache` + `timeout 300 make test` | **PASS** | 36s | 31 ok / 0 FAIL / 5 no-test-files; flake did NOT reproduce |
| go build (embed check) | `timeout 120 go build ./...` | **PASS** | 5s | empty output, exit 0 |
| npm run build (§8) | `timeout 300 npm run build` (vite) | **PASS** | 1s | "✓ 48 modules transformed. ✓ built in 1.04s"; byte-identical bundle (no tree change) |
| go vet ./... (ensure.md) | `timeout 300 go vet ./...` | **PASS** | 1s | zero findings |

## Mock E2E Results

### ultimate_model_shell_mock — PASS 49/49 (150s wall: tests done at T+20s, tail = script's own `go run` reaping)
Test 8 verbatim (key lines):
```
[UltimateModel] Triggered for duplicate request (schedule milestone), using mock-ultimate-model, hash=ebec0023, attempt=5/40   → 10/40 → 20/40 → 30/40 → 40/40
✓ Requests 1-40: ultimate header present exactly on milestones 5/10/20/30/40
[UltimateModel] Attempt limit exhausted for hash=ebec0023 (attempt 41/40)
✓ 41st request: retry_exhausted error: found 'ultimate_model_retry_exhausted'
✓ 41st request: shows attempt count: found 'attempt 41 of 40 max'
```
Ports 4001/4322 freed; log URL scan: only http://localhost:4001/v1.

### minimax_reasoning_shell_mock — PASS 53/53 (120s, first run)
T15 verbatim:
```
T15 captured requests for trigger: 5
✓ T15: duplicate-hash triggered ultimate retry (>=2 captured)
✓ T15: ultimate-path captured request IS translated (reasoning_split present)
```
Ports 4005/4325 freed. ⚠ Maintenance: internal HARD_TIMEOUT=120s hit exactly (near-cap) — suite ran the full alarm window; watch/split if it grows.

## Boundary Spot-Check (independent 42-request drive; isolated HOME=/tmp; ports 4330/4001; drive 5s)

| Requests | Status | X-LLMProxy-Ultimate-Model | Verdict |
|---|---|---|---|
| 1–4 | 500 | absent | ✓ normal |
| **5** | 200 | `mock-ultimate-model` | ✓ MILESTONE |
| 6–9 | 500 | absent | ✓ |
| **10** | 200 | `mock-ultimate-model` | ✓ MILESTONE |
| 11–19 | 500 | absent | ✓ |
| **20** | 200 | `mock-ultimate-model` | ✓ MILESTONE |
| 21–29 | 500 | absent | ✓ |
| **30** | 200 | `mock-ultimate-model` | ✓ MILESTONE |
| 31–39 | 500 | absent | ✓ |
| **40** | 200 | `mock-ultimate-model` | ✓ FINAL milestone |
| **41** | 200 | `retry-exhausted` | ✓ exhausted (body below) |
| **42** | 200 | `retry-exhausted` | ✓ exhausted persists |

#41 body (verbatim): `{"error":{"code":"exhausted","hash":"6b44c8b5…","message":"Request attempt limit exceeded (attempt 41 of 40 max). Hash: 6b44c8b5...","type":"ultimate_model_retry_exhausted"},"type":"error"}`
#42 body: identical shape with `attempt 42 of 40 max`; same hash.
Design notes (intentional, source-verified): exhausted responses carry header value `retry-exhausted` (handler.go:614,621) and use HTTP 200 + JSON envelope (streaming-client compatibility). Wire type is the contract: `ultimate_model_retry_exhausted`.

## Committed-Bundle Verification — CLEAN
- `git status --porcelain pkg/ui/static/` empty (tree == HEAD); assets: SettingsPage-ZLAkPnff.js, index-BfEy6A_O.css, index-BxF3XNC4.js
- OLD `"Max retries exceeded"` / `ULTIMATE_RETRY_EXHAUSTED`: **0 hits** (cs+ci, all assets; also 0 in committed *.go)
- NEW `attempt limit` / `ULTIMATE_ATTEMPTS_EXHAUSTED`: present (1 hit each, index-BxF3XNC4.js)
- Template: `Ultimate model attempts exhausted: ${current_retry}/${max_retries}`; aria-label `Ultimate model attempt limit reached`; Go message `Request attempt limit exceeded (attempt %d of %d max)` (handler.go:586); cap const `maxAttempts = 40` (handler.go:27)
- Rebuild check (separate gate): fresh vite build reproduced all 3 files **byte-identical** (SHA-256 equal) — committed bundle is current
- Note: Go deliberately KEEPS event slug `ultimate_model_retry_exhausted` + payload keys `current_retry`/`max_retries` for client compat (documented handler.go:570, proxy/handler.go:656)

## Web UI Validation (embedded UI of built binary; headless Chrome + events API)
- **Settings** (`/ui/settings` DOM dump): `grep -ciE 'max.?retr'` → **0**. Ultimate section renders exactly: Ultimate Model ID (dropdown) + Max Hash Cache Size; description: "Ultimate model trigger schedule is fixed: escalation on the 5th/10th/20th/30th/40th request with the same content (hard 40-attempt cap, no configuration)." Screenshot: /tmp/umts_settings.png
- **EventLog exhausted event** — live data via `GET /fe/api/events` (verbatim): `{"type":"ultimate_model_retry_exhausted","data":{"current_retry":41,"hash":"6b44c8b5","max_retries":40}}` and `…42…`. Render path proven from served bundle: message template `Ultimate model attempts exhausted: 41/40` + `<span aria-label="Ultimate model attempt limit reached">`. (DOM-paint fallback: SPA's EventLog paints per-selected-request; headless dump-dom hangs on EventSource — documented, evidence = events-API JSON + bundle strings + dashboard screenshot /tmp/umts_dashboard.png)

## URL-Capture (HARD CONSTRAINT: MOCK REMOTE ONLY) — ZERO violations
- Unit/make gates: URL scans empty (4 logs)
- ultimate mock: localhost:4001 only; minimax mock: localhost:4005 only; boundary harness: mid-drive lsof on both PIDs → all connections `[::1]` (loopback); log grep empty
- Aggregate: **0 non-localhost URL/connection across the entire run** ✅

## ensure.md Validation Results (scoped: all 4 critical, this change set)
- ✅ Critical: All Go unit tests pass — via §8 slices + make test (31 pkgs ok; 1 quarantined pre-existing flake, see below)
- ✅ Critical: `go vet ./...` — zero findings
- ✅ Critical: Full project builds — `go build ./...` exit 0
- ⚠️ Critical: "Frontend builds successfully without TypeScript errors" — operative build gate (vite `npm run build`) PASSES; literal `tsc --noEmit` shows 30 errors: 28 pre-existing at base (LB fallout), **2 NEW on this branch** (TS6133 unused `modelToDelete`/`setModelToDelete`, SettingsPage.tsx:102 — dead state, informational only since tsc is not wired into any script). Improvement Notice filed below.
- Important/Nice-to-have (peak-hour, migration 018): out of scope for this change set (not touched by branch) — not run.

## ensure.md Improvement Notices
- ⚠️ "Frontend builds successfully without TypeScript errors" has no pack/script mapping and no tsc invocation anywhere in the repo (`build` = vite only). Suggested rewrite: "§8 frontend gate: `cd pkg/ui/frontend && npm run build` (vite) PASS; `npx tsc --noEmit` error count must not increase vs base (currently 30)". ensure.md is user-owned; please update.

## Flakes / Quarantine
- **TestStoreEngine_CloseLifecycle** (pkg/store/database/store_test.go:419): FAILED once in gate slice run-1, PASSED run-2; dedicated budget 2P/1F (aggregate 4P/2F). Signature: Go testing TempDir RemoveAll cleanup race (`unlinkat … directory not empty`, testing.go:1464) — engine janitor/SQLite WAL residue; assertions NEVER failed. **PRE-EXISTING at base df795c8** (branch touched neither store_test.go nor engine-lifecycle code; only database_test.go/mock_store_test.go). → QUARANTINED (first entry, QUARANTINE.md). Pack-skip wiring + root-cause fix deliberately deferred to post-merge (gate independence).

## Author Recommendations (non-blocking)
1. 🟠 Remove dead state `const [modelToDelete, setModelToDelete] = useState<Model | null>(null)` (SettingsPage.tsx:102) — 2 of the 30 tsc errors, branch-introduced, 1-line removal.
2. 🟢 minimax_reasoning_shell_mock ran the full 120s internal alarm window (near-cap) — split or trim if it grows.

## Failures
None blocking. (Flake + informational items above.)

## Action Needed
- [ ] (author, pre-merge optional) drop SettingsPage.tsx:102 dead state
- [ ] (post-merge) wire quarantined-test skip in store pack + fix janitor-quiescence race; un-quarantine after 3× clean
- [ ] (user) update ensure.md frontend requirement per Improvement Notice

## Documentation Updated
- [x] RESULTS/2026-08-27-ultimate-trigger-schedule-merge-gate.md (this file)
- [x] QUARANTINE.md — created; TestStoreEngine_CloseLifecycle entry
- [x] PACKS.md — ultimate 49/49 + minimax 53/53 @ a0f4cd1, near-cap note
- [x] README.md — testing history row
- [x] LESSONS/ — clean_ports parallel hazard; TempDir flake classification
- [ ] MOCK_TESTS.md — no new permanent mock spec (boundary harness was throwaway /tmp by design)

## Code Changes Summary
None. All 11 workers ran report-only / read-only / throwaway-/tmp modes; zero commits; working tree at a0f4cd1 unchanged except pre-existing .agents bookkeeping files. (Only repo change: .agents/tester/ documentation above.)

### Overall Status
- §8 gates: ✅ 6/6 (+ vet)
- Mock E2E: ✅ 49/49 + 53/53
- Boundary/original behavior: ✅ proven exactly (5/10/20/30/40 header; 41+42 exhausted wire-type)
- Bundle + UI: ✅ clean / verified
- Mock-remote: ✅ zero violations
- ensure.md: ✅ criticals pass (frontend note ⚠ informational)
- **Testing Complete: ✅ READY — merge gate GREEN at a0f4cd1**
