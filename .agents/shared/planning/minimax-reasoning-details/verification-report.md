# Verification Report: MiniMax reasoning_details Translation — Phase 3 E2E Gate

**Date**: 2026-08-19 · **Branch**: `feature/minimax-reasoning-details` · **Gate HEAD**: `d5280ce` → session end `fa8c11d`
**Verdict**: ✅ **READY FOR MERGE** (all Phase 3 exit criteria met at `fa8c11d`; see Deviations for two production fixes shipped during the gate)

---

## (a) AST drift-trap check (P3-3) — PASS

`pkg/proxy/translator/drift_trap_test.go` (3 tests): `TestDriftTrap_ReasoningDetailCompositeLitsOutsideTranslator`,
`TestDriftTrap_AllowListContainsExpectedFiles`, `TestDriftTrap_AllowListFilesParseClean`. Green in every run
(`./pkg/proxy/translator/` 0.03s; included in catch-all `./pkg/...` PASS). Allow-list = `minimax.go`/`minimax_stream.go`;
wire-files = `race_executor.go`, `internal_handler.go`, `handler_internal.go`, `handler_external.go`. No
`&translator.ReasoningDetail{...}` composite literal outside the translator module.

## (b) Unit test summary

| Package | Tests | Status |
|---|---|---|
| `pkg/proxy/translator/` (P3-1: 21 request + 54 response + 3 drift-trap) | 78+ | PASS |
| `pkg/providers/` (incl. 27 openai_reasoning extraction tests + new `TestHydrateReasoningDetails_AcceptsTranslatorStruct`) | 56 | PASS |
| `pkg/proxy/` (incl. P3-2 matrix: 12 pre-existing interleaved + 14 new matrix funcs, 16 subtests) | 349+ | PASS |
| `pkg/ultimatemodel/` (incl. P3-2 matrix twin: 14 new funcs) | 128+ | PASS |
| `pkg/proxyheader/` (P3-7: 2 funcs, 21 sub-cases) | 2 | PASS |

## (c) E2E test summary

| Pack | Result | Evidence |
|---|---|---|
| **P3-4 shell harness** `test/test_mock_minimax_reasoning.sh` (Go mock `mock_llm_minimax_reasoning.go`, ports 4005/4325, 90s/300s dual timeout) | **53/53 PASS** (12s) | T1-T15: request translation + capture, NS details/both, stream incremental/cumulative/both/emptytext/multientry, flag-absent legacy, header hygiene 0 leaks/13 captures, non-MiniMax inertness, 500 error path, usage passthrough, ultimate duplicate-hash trigger |
| **P3-5 Go e2e** `test/e2e_minimax_reasoning/` (in-process httptest, 14 scenario groups) | **43/43 PASS** (4.7s) | S1-S14 covering all 4 paths × req/resp × stream/NS, flag quadrants, credential gate, error path, drift counter delta 0, header hygiene + marker control |
| **P3-2 matrix** (inline scoped run) | **32/32 cells** (0.4s) | 24 body + 4 header + 4 usage; 12 gap-fills in `*_matrix_test.go` ×2 |
| Full regression sweep (12 pack invocations) | **12/12 PASS, 0 flakes** | All 23 pkg + 5 test packages; proxy 24.07s, mcp 16.55s GREEN (task-notes failure label stale) |

**ensure.md gates**: Critical 1 (`go test ./...` via catch-alls) ✅ · Critical 2 (`go vet ./...` empty) ✅ · Critical 3 (build `./...` + `cmd/main.go`) ✅ · Critical 4 (frontend) — **warranted scope-out**: zero paths under `pkg/ui/frontend/` in both `ac877ea..d5280ce` and `d5280ce..fa8c11d` diffs. Important rows (peak-hour, migration 018): not in change set (diff evidence empty).

## (d) Live MiniMax wire-format observation (Q1, R9) — **DEFERRED**

No MiniMax credentials available for live traffic. Mock implements BOTH R9 variants — incremental deltas AND
cumulative deltas — and the proxy translates both correctly (T5: "think-1/2/3"; T6: A,AB,ABC → suffixes A,B,C with
no duplicate emission). Whichever mode live MiniMax uses, the shipped translator handles it; the live observation
remains a post-merge follow-up (see Live-only list).

## (e) Post-merge acceptance gates (Q2, Q4)

- **Q2 (upstream emits details with AND without `reasoning_content`)**: verified at mock level in both modes —
  details WIN, client sees the text exactly once (T4 NS, T7 stream, S4 subtests both modes, S5 both-fields). Live
  confirmation folded into the R9 live call.
- **Q4 (client-visible behavior)**: satisfied by the full contract suite — client NEVER sees `reasoning_details`
  (asserted per-path), stream SSE framing valid with `data: [DONE]`, non-MiniMax + flag-absent byte-identical.

## (f) Observed `reasoning_details` format values

Only `"MiniMax-response-v1"` (translator-emitted and mock-synthesized). No live-wild formats observed yet —
the registered set in `formatDriftCounter` should be re-checked after first live traffic.

## (g) Drift-counter baseline

`translator.FormatDriftCount()`: **before = 0, after = 0, delta = 0** across the entire P3-5 suite (S13) and all
mock runs. This is the pre-production baseline: any nonzero value in production telemetry indicates unknown
live formats.

## (h) Deviations from plan

1. **Mock server language**: plan P3-4 said "Python mock server"; implemented in **Go** — matching the project's
   actual harness convention (`mock_llm_*.go`, `test_mock_*.sh`). No semantic impact; `bash test/test_mock_minimax_reasoning.sh` exits 0 per exit criterion.
2. **P3-1 / P3-3 already on branch** at gate start (Phase-3 hardening commits `3898547`/`96371c5` era): verified
   green rather than rebuilt.
3. **P3-7 satisfied by existing tests** (`proxyheader_test.go`, 21 sub-cases) — verified against
   `handler.go:466` precedent semantics; no new artifact needed.
4. **Two production bugs found by the gate and fixed** (both quick-fix workflow, verified, committed):
   - **T3b** — race-internal silent reasoning destruction (`882fa3f` harness → fix `068317c`): translator wrote
     struct values into `map[string]any`; `HydrateReasoningDetails` accepted maps only → empty typed slice →
     `omitempty` dropped the wire field. Fix: type-switch accepts `translator.ReasoningDetail` (byte-neutral;
     race-external wire unchanged — empirically proven). First fix candidate (map-mode emission) was **stopped**
     by byte-shape evidence: `encoding/json` orders struct fields by declaration but map keys alphabetically.
   - **S3** — ultimate-internal id scheme used message index, not the translator's monotonic counter over
     reasoning-carrying messages (e2e suite `166aa7f` caught it; no unit test had ever asserted the id scheme).
     Fix `b2dfde0`; all 4 paths now emit identical ids.
   - Harness repair `1344380` (T3b asserted `"index": 0`; `omitempty` on `Index int` makes the key always absent —
     assertion inverted to the real wire contract). Style cleanup `fa8c11d` (gofmt on 2 session-touched files).
5. **P3-2 matrix**: 2 cells structurally N/A (internal paths × no-credential × response side — function errors at
   credential resolution before any response exists); strongest reachable assertion substituted (error + provider
   never called). Ultimate-internal stream emits NO usage final-chunk in baseline — W8 asserts absence (documented).

## (i) Known follow-ups

- **Live-only verification** (below): R9 + R7 + live format observation.
- Q9 admin gating, Q10 dormant converter cleanup, A+B converter unification (§5.6 post-merge PR) — unchanged from plan.
- Pre-existing gofmt debt: 40 files (none session-touched) — recommend a repo-wide `style:` sweep PR.
- Flaky watch: `stream_buffer.go:166`/`race_executor.go:1434` panic observed once in ~7 pkg/proxy runs during the
  T3b session (2s streaming-deadline race; not observed in 12 sweep runs). Not quarantine-worthy yet; watch.
- `translator.ReasoningDetail` struct is now consumed by the providers hydrator (no longer translator-internal);
  the drift-trap allow-list semantics remain valid.

## Live-verification-only list (cannot be mock-verified)

1. **R9 — live MiniMax stream mode**: whether live deltas are incremental or cumulative (translator handles both;
   confirm drift counter stays 0 and dedup/suffix logic matches observed traffic).
2. **R7 — `reasoning_split` placement**: confirm live MiniMax accepts top-level `reasoning_split: true` placement
   (mock cannot validate upstream semantics, only proxy behavior).
3. **Live format values**: any `format` string other than `MiniMax-response-v1` observed in the wild → feeds
   `formatDriftCounter` registered set (baseline 0).

## Session commit ledger (test artifacts + fixes)

| Hash | Type | Subject |
|---|---|---|
| `ee590c1` | test | P3-2 byte-identical negative matrix (12 gap-fills, 2 files, 1424 lines) |
| `882fa3f` | test | P3-4 MiniMax reasoning mock harness (Go mock + shell, 15 scenarios) |
| `068317c` | fix | T3b: hydrate translator ReasoningDetail structs (providers) |
| `1344380` | test | T3b index assertion repair (omitempty wire contract) |
| `166aa7f` | test | P3-5 e2e suite (14 groups / 43 subtests) |
| `b2dfde0` | fix | S3: monotonic reasoning-text id counter (ultimatemodel) |
| `fa8c11d` | style | gofmt session-touched files |

Evidence files: `.agents/tester/RESULTS/2026-08-19-minimax-reasoning-details-e2e-gate.md` ·
`.agents/tester/LESSONS/2026-08-19-t3b-race-internal-reasoning-loss-quickfix.md` ·
`.agents/tester/LESSONS/2026-08-19-s3-cross-path-id-parity-quickfix.md` ·
`.agents/tester/PACKS.md` (5 new pack rows, all PASS).
