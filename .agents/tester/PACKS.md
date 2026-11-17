# Test Packs

## Summary
- Total: 18 unit packs (17 prior + sticky_order_test added 2026-11-06 during the sticky-last-success-ordering gate) + mock/E2E packs below, across 40 Go packages; + 2 FE inline packs (fe_vitest_suite, fe_typecheck)
- Unit: 17 | Integration: 1 | E2E: 1 | Mock: 1
- All packs enforce dual-layer timeout (`timeout` command outer + script/go-test internal)

## Timeout Configuration
- **Script timeout**: 120s (`timeout 120s`)
- **Go test timeout**: 110s (`-timeout=110s`)
- **Buffer**: 10s for script overhead/cleanup
- **Exit codes**: 0=PASS, 1=FAIL, 124=TIMEOUT
- **2026-11-08 note (openrouter gate)**: pkg/proxy has OUTGROWN the 110s internal cap on full-package slice runs (99.5s @ 2026-11-06 sticky gate → 109.601s isolated → 110.753s `make test`; Pack A initial run tripped it by 59ms under package-parallel contention, clean on isolated re-run). RELEASE-GATE slice runs (Pack A/B, umbrella corroboration) now use internal `-timeout=240s` inside the unchanged outer `timeout 300` hard cap; unit-pack scripts keep 110s. Durable fix (split pkg/proxy test files / `-p 1`) = post-gate mainline watch item.
- **2026-11-09 note (9router imggen gate)**: full regression executed as the documented RELEASE-GATE slices — A (`./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`, 9 pkgs, 117s wall, `pkg/proxy` 109.334s) + B (33-pkg complement, 51s) — internal `-timeout=240s` inside `timeout 300`, both green except the 2 deterministic quarantined `pkg/mcp` httpbin.org redirect failures (row 11). `pkg/proxy` remains inside the 240s release-gate cap; no split required yet.
- **2026-11-14 note (retire-ultimate-fallback release gate, wi-7769a812)**: release-gate slices @ d3fe252 (base 637a683, 5 commits, 177 files +3675/−17939): A = `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (`-timeout=240s` inside `timeout 300`, HOME-isolated, config.json hash-guarded) — run-1 111s with a single FAIL `TestStoreEngine_PeakHourParity` @ 23:59:02Z, adjudicated time-of-day flake (code-proven `[00:00,23:59)` minute-1439 exclusion; isolated 3P/0F; QUARANTINED row 6) → full re-gate 00:10Z EXIT=0 all green (proxy 110.400s, store/database 38.580s); B = 32-pkg anchored complement, 56s, sole FAIL pkg/mcp = the 2 quarantined httpbin redirect tests, identical `handlers_api_test.go:1864/:1904` signature (reconfirmed; row 5). Anti-vacuous: 25 added tests (16 Pack A + 9 pkg/skipcache) each literal `--- PASS`; tool-repair 62/62 in pkg/proxy + toolcall/toolrepair/loopdetection siblings ok; `go vet ./...` + `go build ./...` exit 0; FE 269/269 vitest, tsc 19-error/9-file exact baseline parity, vite build green; isolated boot smoke green (healthz/version/models 200/200/200, zero panics, clean SIGTERM, config hash unchanged); boundaries `k8s/ .github/` + migrations/migrate.go diff = 0 bytes; gofmt zero-new (12 changed-set flags all pre-existing @base, per-file disambiguated). **Verdict GO** (deploy: daoduc backup only). Full report: RESULTS/2026-11-14-release-gate-wi-7769a812.md.
- **2026-11-14 note (anthropic-lane-auth release gate, anomaly 4a231ed0 closure)**: release-gate slices @ 9f4fd2b (base d9cdbea = origin/latest tip, 3 commits: 1c06b59 fix → 8113caf tests → 9f4fd2b style, 4 files — 1 production + NEW handler_anthropic_auth_test.go + parity test mods + e2e harness mods): A green FIRST-PASS 120s (`-timeout=240s` inside `timeout 300`, HOME-isolated, 1242 top-level `--- PASS` / 0 FAIL; proxy 112.849s, store/database 45.475s; pre-registered flakes CloseLifecycle + PeakHourParity both PASSED outright — no adjudication needed); B = 32-pkg anchored complement 63s, PASS-with-quarantine (sole FAILs = row-11 httpbin redirect pair, signature `handlers_api_test.go:1864/:1904` reconfirmed; TOCTOU + usage-stress not triggered, credentiallb ok 3.794s / usage ok 58.507s); anti-vacuous 5 added tests each literal `--- PASS` + `=== RUN` (2233 RUN lines in A; zero FAIL/SKIP for all 5 across A+B); gofmt ZERO-NEW-DRIFT (47 flagged files byte-identical base↔HEAD, F_HEAD−F_BASE=∅; 2 flagged∩changed test files carry base-identical branch-hunk-disjoint hunks; production handler_anthropic.go + NEW test file unflagged); e2e anthropic_thinking_leak 3/3 (authenticated harness). **Verdict GO** (no push/merge/deploy by tester). Full report: RESULTS/2026-11-14-release-gate-anthropic-lane-auth.md.
- **2026-11-16 note (cachelayer ship gate, wi-4d2c8e17)**: release-gate @ 30e3ec4 (base 24c51ef = v193 tree, 10 commits, 31 files +3293/−585): A = 9-pkg slice, `timeout 300` + `-timeout=240s`, HOME-isolated, **EXIT=0 wall 132s**, 1274 `--- PASS` / 0 FAIL / 11 pre-existing skips (proxy 129.621s, store/database 34.604s; CloseLifecycle + PeakHourParity passed outright); B = 33-pkg anchored complement (NEW pkg/modelcache 8/8 literal PASS) 52s, sole FAILs = quarantined httpbin pair `:1864/:1904` (pkg/mcp tree-hash bit-identical to base — branch-orthogonal); `make test` EXIT=2 ×2 runs (2nd `GOFLAGS=-count=1`) = same pair only + 3/3 import guards (make covers NEITHER FE nor vet — closed by the FE gate + build_gate pack); FE: tsc 19-error/9-file EXACT parity (baseline file untouched by branch), vitest 24 files/269 tests green, `vite build` byte-identical pkg/ui/static (zero-mod proof; bundle carries new model_fallback labels); gofmt ZERO-NEW-DRIFT (25/25 touched .go incl. all _test.go: flagged-set ∅ at BOTH base and HEAD; non-vacuity via drift injection); RED spot-check on base scratch = **4/4 mandatory flagships executed-FAIL** (T_A :203 mark-attempted regression, T_F :654 deadline spin, T_G :678 empty-200 winner, T_E :611 no hedge) + bonus T_B + structural-pin-RED-on-base (base store.go imported skipcache) — achieved via authorized 2-symbol scratch excision, 17/17 surviving test bodies cmp byte-identical; import pin non-vacuous (AST ImportSpec scan; 4/4 mutations caught incl. subpackage + build-tagged leaks; comment counter-probe green; positive control present; report-only gaps: one-hop reachability, exact-string match); `go vet ./...` + `go build ./...` EXIT=0 (build_gate pack dual-confirmation); ZERO flake adjudications. **Verdict SHIP-GATE-PASS** (landing by giter). Full report: RESULTS/2026-11-16-wi-4d2c8e17-cachelayer-shipgate.md.
- **2026-11-17 note (credential-groups release gate)**: release-gate @ ad6996ac (base 2b987bf, 31 commits, 72 files +14316/−182): A = 9-pkg slice EXIT=0 135.5s (2321 `--- PASS`/0 FAIL/13 environmental SKIP/2334 RUN — arithmetic closes exactly; PeakHourParity + CloseLifecycle passed outright, TOCTOU not in slice); B = 33-pkg anchored complement `ANCHOR_OK` 60s (1116 PASS, sole FAILs = quarantined httpbin pair `:1864/:1904`, TOCTOU passed outright in `pkg/credentiallb ok 3.832s`); pkg/modelscache DIRECT 107/107 (RUN=PASS parity; premise correction: 9f475ae's C1 edit lives in pkg/store/database, only ad6996ac touched modelscache); `make test` ×2 (cached + GOFLAGS=-count=1) EXIT=2 137s/136s = quarantine pair only + import guards 3/3; FE vitest 33 files/357 tests 12.36s (vacuity reconciliation clean); tsc EXIT=2 **EXACT-PARITY 18/9** (baseline untouched @ 3f94a69, no hard-coded counts, verifier mutation-proven 18→19 RED); static zero-mod **AUTHORITATIVE find-form 4/4 incl index.html, before/after byte-identical** (forbidden glob = 3 files, trap proven); gofmt ZERO-NEW-DRIFT (32 touched .go, NEW=∅, non-vacuity proven BOTH directions); build+vet+build_gate EXIT=0; **anti-vacuity 8/8 pins NON-VACUOUS** (AST pin forbidden-RED/comment-GREEN w/ documented `:=` hole; rebuild-lock CONTENTION-RED @ pre-C1 mutation; DirectSQL group-rows-only-RED; ModelForm.mixed testid/unguarded/specific-message; tabs before+after both-RED incl always-mount-25ms; 409 leak-append RED exactly on the absence assertion); O-2 pin STRONG byte-for-byte; BE↔FE CONTRACT-INTACT (7/8 confirmed; wire DTO 5-key = plan-conformant pre-existing wording gap); chart untouched 5-way; migration-033 scope exact (4 SQL + registry append); secrets clean; 22/22 lane SHAs ancestors of HEAD. **Verdict GATE-PASS** (2 non-blocking findings: 10 bare-`group` strings 🟠 leader-adjudicate; ensure.md locations-vs-counts wording 🟢). Full report: RESULTS/2026-11-17-credential-groups-release-gate.md + RESULTS/2026-11-17-credential-groups-static-zero-mod.md.

## Unit Test Packs

| Pack | Script | Scope | Timeout | Last Run | Status |
|------|--------|-------|---------|----------|--------|
| proxy_unit_test | test/packs/proxy_unit_test.sh | handler, race_executor, adapters, streaming, auth | 120s | 2026-10-06 | PASS (474P/0F/7S long-wait heartbeat skips; incl. NEW imggen misroute+filter tests; @ e2d93c3 imggen final verification) |
| store_unit_test | test/packs/store_unit_test.sh | database, querybuilder, mock_store | 120s | 2026-10-06 | PASS (182P/0F/4S PG-conditional skips; migration 029 fresh-DB green; imggen kind gates 8/8; CloseLifecycle ran+passed on Linux; re-verified post pack-script fix @ 3ea122c) |
| models_unit_test | test/packs/models_unit_test.sh | config, peak_hours, credentials, errors, secondary_upstream | 120s | 2026-10-06 | PASS (92 top/276 sub incl. full kind-discriminator validation matrix; @ e2d93c3) |
| toolrepair_unit_test | test/packs/toolrepair_unit_test.sh | repair, strategies, fixer | 120s | 2026-10-06 | PASS (17/105; @ e2d93c3) |
| loopdetection_unit_test | test/packs/loopdetection_unit_test.sh | detector, fingerprint, strategies | 120s | 2026-10-06 | PASS (33/33; @ e2d93c3) |
| auth_unit_test | test/packs/auth_unit_test.sh | token, store | 120s | 2026-10-06 | PASS (87P/0F/0S; @ e2d93c3) |
| token_unit_test | pkg/proxy/token/ (inline) | counter, prompts (scope text stale: encoding/extraction files no longer present) | 120s | 2026-10-06 | PASS (ok 0.878s non-verbose; @ e2d93c3) |
| mcp_unit_test | test/packs/mcp_unit_test.sh | pkg/mcp/ — store, validation, auth, proxy, handlers_sse, handlers_streamable, handlers_api, e2e, endpoint_split_validation | 120s | 2026-11-06 | FAIL-known (243/245 top; the 2 = TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked — httpbin.org now 403s redirect-to-internal targets server-side, DETERMINISTIC third-party failure, byte-identical on base 63df316, QUARANTINED 2026-11-06 skip-wiring deferred; SSRF octal/decimal/hex subtests still PASSING — stale 2026-09-22 row, formal clear pending; @ fee32a8 sticky gate; **reconfirmed 2026-11-08 openrouter gate** — fired identically in Pack B slice + make test, same :1865/:1905 signature, all other mcp tests green; **reconfirmed 2026-11-14 anthropic-auth gate** — same pair as the sole B-slice failures, signature :1864/:1904, all other mcp green; **reconfirmed 2026-11-16 cachelayer ship gate @ 30e3ec4** — sole failures in Pack B slice AND both make-test runs, same signature, pkg/mcp tree-hash identical to base 24c51ef) |
| misc_unit_test | test/packs/misc_unit_test.sh | config, crypto, events, bufferstore, providers, supervisor, toolcall, ui, usage | 120s | 2026-10-06 | PASS (422P/0F/1S file-gated log-skip; usage 44s incl. IncrementTokenImages/IncrementModelImages; @ e2d93c3) |
| translator_unit_test | test/packs/translator_unit_test.sh | pkg/proxy/translator — wire translation incl. incremental_stream (real-streaming-default Phase 3) | 120s | 2026-10-06 | PASS (125P/0F/0S; @ 2a7e6d4 pre-bundle — zero .go delta since) |
| gap_unit_test | test/packs/gap_unit_test.sh | credentiallb, proxyheader, proxy/normalizers, loopdetection/fingerprint, store parent (memory) | 120s | 2026-10-06 | PASS-with-quarantine (284/284 ×2 @ 2a7e6d4; @ 3ea122c 1 FAIL = TestEngine_GetOrSelect_TOCTOU_CoolingReverify — flaky, load-context only, QUARANTINED 2026-10-06; 4/5 pkgs ok incl. isolated 3×P) |
| testroot_unit_test | test/packs/testroot_unit_test.sh | test/ root — access_control + integration_allowed_models | 120s | 2026-10-06 | PASS (35 top + 8 sub; @ e2d93c3) |
| gzipmw_unit_test | test/packs/gzipmw_unit_test.sh | pkg/middleware/gzipmw — gzip request-body decompression middleware | 120s | 2026-10-06 | PASS (39P/0F/0S; @ e2d93c3) |
| build_gate_test | test/packs/build_gate_test.sh | go build ./... + go vet ./... (Go-only gate; npm/tsc excluded by design) | 120s | 2026-11-17 | PASS (toolchain-PATH required — system go 1.22.2 fails `go.mod requires go >= 1.26`; `go build ./...`+`-o /tmp/cmd-check ./cmd`+vet all EXIT=0 EMPTY, 26,554,063B binary; `go build ./cmd` no-`-o` = KNOWN cmd/-collision trap; @ ad6996ac credential-groups gate; prior: PASS 5s @ 30e3ec4) |
| reasoning_content_dir | inline: `go test ./test/reasoning_content/ -v -count=1 -timeout 240s` | serialization chain + non-stream reasoning_content | 240s go-test / `timeout 300` outer | 2026-10-06 | PASS (2 funcs / 14 subtests; @ e2d93c3) |
| imggen_unit_test | inline: `go test ./pkg/imggen/ -v -count=1 -timeout 110s` (outer `timeout 300`) | pkg/imggen — handler, minimax, no_key_in_artifacts (live_e2e_test.go EXCLUDED via `//go:build live_imggen`) | 300s outer / 110s go | 2026-10-06 | PASS (42P/0F/0S @ 2a7e6d4; re-run 42P @ 548006f after cov-adds +10 assertion lines; NoKeyInArtifacts confirmed EXECUTED untagged; FIRST registered run) — **re-test @ 0d08e3e PASS: 42/0/0 identical baseline (no regression from store fix)** — 2026-11-09 9router gate: 91 top-level RUN / 91 PASS / 0F / 0S verbose (`-timeout=240s` inside `timeout 300`, isolated HOME); 33 new 9router/b64 tests all literal `--- PASS` |
| orphan_unit_test | inline: `go test ./pkg/memlimit/ ./pkg/modelscache/ -count=1 -timeout 110s` (outer `timeout 120`) | pkg/memlimit + pkg/modelscache — pre-existing pack orphans surfaced by 2026-10-06 inventory (formerly one-off regC/regD sweeps) | 120s outer / 110s go | 2026-10-06 | PASS (both ok: 0.009s + 4.101s; FIRST registered run) |
| e2e_mock_regression_test | inline: `go test ./test/e2e_anthropic_thinking_leak/ ./test/e2e_fe_reasoning_observability/ ./test/e2e_minimax_reasoning/ ./test/e2e_reasoning_content/ -v -count=1 -timeout 240s` (outer `timeout 300`) | ALL registered in-process httptest e2e mock suites in one invocation | 300s outer / 240s go | 2026-10-06 | PASS (94 RUN / 94 PASS / 0F / 0S; 27s; zero network-shaped errors; FIRST registered combined run) |
| sticky_order_test | inline: `go test ./pkg/stickyorder -count=1 -timeout=110s` + `go test ./pkg/proxy -run 'Sticky' -count=1 -timeout=110s` (outer `timeout 300` each; sticky-scoped `-race` variant same scope) | pkg/stickyorder units (engine+order, 18) + pkg/proxy Sticky wiring matrix (27 top + 7 sub) + gate scenario pack `pkg/proxy/sticky_scenario_test.go` @ 5f75216 (RUN1/RUN2 same-prefix, unpin/re-pin/revert, handler-wired short-TTL) | 300s outer / 110s go | 2026-11-06 | PASS (stickyorder 18/18; proxy Sticky 27+7; scenario 4/4 incl. -count=2; sticky-scoped -race PASS 56.7s; registered during sticky-last-success-ordering gate @ fee32a8+5f75216 — full report RESULTS/2026-11-06-sticky-last-success-ordering-gate.md) |
| fe_vitest_suite | inline: `cd pkg/ui/frontend && timeout 300 npx vitest run` | FE full suite (windowing, RequestDetail, ImgGen tab/form/hook, chatFilter, SettingsPage tabs, credential-groups surface) | 300s outer | 2026-11-17 | PASS 357/357 (33 files, 12.36s; vacuity reconciliation clean 33↔33; @ ad6996ac credential-groups gate; prior: PASS 269/269 @ 30e3ec4) |
| fe_typecheck | inline: `cd pkg/ui/frontend && timeout 240 npx tsc --noEmit` | FE TypeScript error gate (vite build does NOT type-check) | 240s | 2026-11-17 | PASS-scoped (**18 errors / 9 files = EXACT parity** vs re-frozen baseline; baseline untouched @ 3f94a69; freeze-commit replay proves no error resolved-and-replaced; verifier mutation-proven non-vacuous; @ ad6996ac credential-groups gate) |

**2026-10-06 pack-script fix (commit 3ea122c)**: all 15 test/packs/*.sh — the timed `go test` line is now guarded (`|| EXIT_CODE=$?`) so `RESULT: FAIL/TIMEOUT` + verbose evidence are ALWAYS emitted (previously `set -euo pipefail` made failure output unreachable and the EXIT trap destroyed it). FAIL path proven live by the TOCTOU flake the same day.

## Race Slices (real-streaming-default merge gate 2026-08-28)

| Slice | Exact command | Result |
|-------|---------------|--------|
| raceA_proxy | `timeout 300 go test -race -run 'TestRealStreaming\|TestLiveRelay\|TestLiveMode' -count=1 ./pkg/proxy/ -timeout 280s` | **PASS** — 0 races, 24s |
| raceC_translator | `timeout 300 go test -race -count=1 ./pkg/proxy/translator/ -timeout 280s` | **PASS** — 0 races, 1.1s |

## Mock Test Packs

| Pack | Script | Type | Timeout | Last Run | Status |
|------|--------|------|---------|----------|--------|
| peak_hour_fallback_mock | test/mock_llm_peak_hour_fallback.go | E2E Mock | N/A (Go test) | 2026-04-09 | PASS |
| frontend_api_cache_mock | test/mock_frontend_api_cache.mjs | Unit | 60s | 2026-04-09 | PASS |
| allowed_models_integration | test/integration_allowed_models_test.go | Integration | N/A (Go test) | 2026-05-01 | PASS |
| e2e_reasoning_content | test/e2e_reasoning_content/ | E2E | N/A (Go test) | 2026-05-05 | PASS |
| openai_internal_buffered_shell_mock | test/test_mock_openai_internal_buffered.sh | Shell E2E (buffered openai internal; ports 4003/4324 harness-fixed, pre-existing convention) | 60s internal / `timeout 300` outer | 2026-08-28 (re-run) | **PASS 60/60** @ 61fa02a — harness repaired by 61fa02a (credentials[] payload + buffer header on all 15 curls); prior FAIL 0/60 (credential-LB schema rot) superseded |
| minimax_reasoning_shell_mock | test/test_mock_minimax_reasoning.sh | Shell E2E (mock MiniMax upstream w/ reasoning_details replay + capture; ports 4005/4325 harness-fixed) | 120s internal / `timeout 300` outer | 2026-08-28 | **PASS 53/53** @ 22e76d6 (rsd merge gate; 11s wall — previous near-cap warning RESOLVED, was a cold-build artifact) |
| e2e_minimax_reasoning | test/e2e_minimax_reasoning/ | E2E Mock (capturing in-process upstream; 4-path scenario suite, P3-5) | 240s go-test / `timeout 300` outer | 2026-09-26 | **PASS (all S1–S14, 4.101s) @ cc5e15c** — S8 flag-absent expectations updated to body-content gate (3 subtests `assertUntouched`→`assertTranslated`, +14/−7); prior 2026-08-28 PASS 43/43 @ 22e76d6 superseded (3 S8 subtests failed @ 6829d98 with stale header-only contract — see LESSONS/2026-09-26-stale-e2e-contract-after-gate-widening.md) |
| minimax_interleaved_matrix | inline (exact command in code block below — **NOT** `\|`, see warning) | Unit (P3-2 byte-identical negative matrix: 24 body + 4 header + 4 usage) | `timeout 300` | 2026-08-21 | **PASS** — 34/34 test funcs (46 PASS incl. 12 subtests) @ `355f06c`; quoting repair: registered `\|` form was vacuous (0 tests run, see LESSONS/2026-08-21-interleaved-matrix-regex-vacuous-pass.md); 2 N/A cells noted 2026-08-19 stand |
| proxyheader_header_table | inline: `go test ./pkg/proxyheader/ -run 'Interleaved' -count=1` | Unit (P3-7 header value truth table verify) | `timeout 300` | 2026-08-19 | **PASS** — satisfied by existing 21 sub-cases (all 7 plan values covered, file:line-cited); precedent match confirmed; single-source semantics confirmed (2 call sites via proxyheader.*); NO gap-fill needed |
| fe_reasoning_observability | test/e2e_fe_reasoning_observability/ | E2E Mock (in-process proxy + real-HTTP FE API mount; closure gate + 16-row path matrix) | 240s go-test / `timeout 300` outer | 2026-08-28 | **PASS 20/20** @ 22e76d6 (rsd merge gate: closure 4/4 + matrix 16/16; capture taps mode-independent; NOTE: no anthropic-internal-nonstream row — S3-class bug not covered here) |
| anthropic_thinking_leak | test/e2e_anthropic_thinking_leak/ | E2E Mock (in-process anthropic /v1/messages; sink-vs-wire dual assertion; harness AUTHENTICATES since lane-auth 1c06b59) | 240s go-test / `timeout 300` outer | 2026-11-14 | **PASS 3/3 @ 9f4fd2b (anthropic-lane-auth release gate)** — S1A/S1B/S3 each literal `--- PASS` (1.03s/1.32s/1.13s), `ok ... 3.501s`, EXIT=0; harness now mints a token (dummy x-api-key removed) per /v1/messages auth enforcement — current membership is 3 tests; superseded-era 4/4 @ b48a14f (older suite membership) |
| rsd_m1_openai_ttfb | test/mock_rsd_m1_openai_ttfb.sh | Shell E2E (real binary; OpenAI path TTFB default-vs-buffered; ports 10110/10111) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS** @ 22e76d6 (A: TTFB 103ms vs total 1536ms, 5 gaps ≥150ms; B: TTFB 1603ms spread 0ms; C info: byte-diff = live `: keepalive` only, content identical; D healthz 200; 3× stable) |
| rsd_m3_edge_cases | test/mock_rsd_m3_edge_cases.sh | Shell E2E (real binary; header truth table + first-wins + stream=false + disconnect; ports 10130/10131) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS 21/21** @ 22e76d6 (truth table 12/12 incl. case+garbage; multi-line first-wins both directions; stream=false byte-identical; disconnect: healthz 200, no panic) |
| e2e_reasoning_content | test/e2e_reasoning_content/ | E2E Mock (reasoning_content chain, streaming + non-stream) | 240s go-test / `timeout 300` outer | 2026-08-28 | PASS 5 top-level + 7 sub @ 22e76d6 (rsd merge gate) |
| gzip_request_decompression | test/mock_gzip_request_decompression.sh | Shell E2E (real binary; gzip request-body original scenario a-f; ports 10140/10141) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS 6/6** @ 7a9ecff (gzip feature gate; byte-identity both /v1 protocols, corrupt→400, 150 MiB bomb→413 + liveness, SSE passthrough; 3 harness-only fixes on first run — see LESSONS/2026-08-28-gzip-e2e-harness-lessons.md) |
| dbcache_outage | test/test_e2e_dbcache_outage.sh | Shell E2E (real binary + real SQLite; incident close-out scenarios 1–3; dd-corruption DB outage; ports 10150/10151 + 4001 misroute sentinel) | 280s internal / `timeout 300` outer | 2026-08-29 (re-gate) | **PASS @ a09877d** (exit 0, 163.7s — A4 28/28 through outage, stale-tier T_valid 200 at t70/t90, zero misroutes, recovery 61s). Pre-fix: FAIL ×3 deterministic (F1 stale-tier classifier gap) — F1 FIXED and regression-gated by this pack |
| dbcache_rollback_ui | test/test_e2e_dbcache_rollback_ui.sh | Shell E2E (real binary; rollback flag-off misroute repro + write-through mutations + UI checks; ports 10160/10161 + 4001 sentinel) | 240s internal / `timeout 300` outer | 2026-08-29 (re-gate) | **PASS @ a09877d** — B6.2 attach accepted first-try (0 bounded-wait iters; was rejected-until-tick), B6.3 rotation visible iter 1 (was ~55s/iter-25), zero stale-key leakage; UI + FE API healthy; flag-off misroute repro intact. Pre-fix PASS-with-findings (F2/F3) superseded — F2/F3 FIXED |
| provider_registration_smoke | test/test_provider_registration_smoke.sh | Shell E2E (real binary; **parameterized provider-brand registration gate**: providers listing name/base_url + credential-create ACCEPTED + model validate/create + lowercase-exact negatives + shared-provider rule + /healthz + /ui/ + SIGTERM graceful + port freed; args: brand, display name, base URL, port — default openrouter/OpenRouter/https://openrouter.ai/api/v1/18240) | 280s internal / `timeout 300` outer | 2026-10-27 | **PASS 18/18 @ 815a9ff** (defaults 3.0s; explicit-args 2.0s; cross-brand deepseek 18/18 = generic-proof; isolated HOME + XDG_CONFIG_HOME, real DB never opened, defensive aborts on port 8088/<10000/foreign-holder; FIRST registered run) |

#### anthropic_thinking_leak — historical note (superseded 2026-08-28)
- Pre-rsd-gate baseline: PASS 3/3 @ 63b7701; mutation-proven non-vacuous (leak re-injected in worktree → S1 FAILs with 4 detections); S3 wire byte-identical at parent effc345.

#### minimax_interleaved_matrix — exact runnable command

```bash
# ⚠️ NOT 'Interleaved\|MiniMax\|Reasoning' — in single quotes, go's RE2 reads
# \| as an ESCAPED LITERAL PIPE and matches ZERO tests (vacuous exit-0 PASS).
# The `\|` form only ever worked unquoted, where the shell strips the backslash.
timeout 300 go test ./pkg/proxy/ -run 'Interleaved|MiniMax|Reasoning' -count=1 -timeout 280s
```

Expected: 17 test funcs in `pkg/proxy` (historical: the ult-internal half was removed with the ultimate subsystem 2026-11), exit 0. If output says `[no tests to run]`, the quoting regression has returned — see `LESSONS/2026-08-21-interleaved-matrix-regex-vacuous-pass.md`.

---

## 2026-09-26 — fix/minimax-reasoning-translation-gate full sweep (inline packs, @ 6829d98→cc5e15c)

Full `go test ./...` equivalence executed as 5 parallel per-package-group inline packs, each `timeout 300` outer (see RESULTS/2026-09-26-minimax-reasoning-gate-verification.md):

| Inline pack | Command | Result |
|-------------|---------|--------|
| regA_proxy | `timeout 300 go test -count=1 ./pkg/proxy` | **PASS** ok 55.042s (incl. NEW race_minimax_fallback_e2e_test.go) |
| regC_store_cache_lb | `timeout 300 go test -count=1 ./pkg/store/database ./pkg/store ./pkg/modelscache ./pkg/credentiallb` | **PASS** (~4s) |
| regD_light | `timeout 300 go test -count=1 . ./cmd ./pkg/auth ./pkg/bufferstore ./pkg/config ./pkg/crypto ./pkg/events ./pkg/logger ./pkg/loopdetection ./pkg/loopdetection/fingerprint ./pkg/mcp ./pkg/memlimit ./pkg/middleware/gzipmw ./pkg/models ./pkg/proxyheader ./pkg/providers ./pkg/supervisor ./pkg/toolcall ./pkg/toolrepair ./pkg/ui ./pkg/usage` | **PASS** (18 ok + 3 no-test-files; slowest mcp 17.5s) |
| regE_test_tree | `timeout 300 go test -count=1 ./test/...` | **PASS after fix** — e2e_minimax_reasoning 3 stale S8 subtests failed @ 6829d98 (old header-only contract), fixed by cc5e15c, package re-run PASS 4.101s |

NEW pack member: `pkg/proxy/race_minimax_fallback_e2e_test.go` (commit 578512f) — E2E incident path: primary scripted streaming-429 → race coordinator fallback (`trigger=main_error`) → MiniMax wire body carries `reasoning_details` + `reasoning_split:true` + no raw `reasoning_content`; guard test pins non-MiniMax non-translation. Stable ×5, `-race` clean, 0.421s both.

Boot smoke: `go build ./...` exit 0; boot <1s; `/healthz` 200; one request graceful JSON 502, no panic. (PORT requires `APPLY_ENV_OVERRIDES=1`; default port 4321.)

## Updating PACKS.md

Update after each test run:
- **Last Run**: timestamp
- **Status**: PASS/FAIL/TIMEOUT
- Add new entry for new packs
- Mark deprecated packs as DEPRECATED

## Integrity Checks
- ✅ All 8 unit test pack scripts exist in `test/packs/`
- ✅ All scripts are executable (`chmod +x`)
- ✅ All scripts use `timeout 120s` for subprocess-based enforcement
- ✅ All scripts output `RESULT: PASS|FAIL|TIMEOUT`
- ✅ All scripts have cleanup traps on EXIT

## Recent Test Results (2026-04-13)

## Recent Test Results (2026-05-08)

### Commits d578427 + 585fe6a - Delete Model Button Fix
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | (historical run; the retired ult-internal package's failures no longer apply) |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | 1.46s, 4 chunks, no warnings |
| Browser Tests | ✅ 5/5 | Delete dialog, cancel, delete, loading state, overlay dismiss |
| Quick Fixes | 1 | Overlay click dismiss handler (commit 585fe6a) |

**Pre-existing failures (unrelated to fix):**
- `pkg/proxy` — nil pointer dereference in `streamBuffer.GetAllRawBytesOnce()` (stream_buffer.go:166)
- `test/e2e_reasoning_content` — same root cause, different call path through race_executor.go:621

### Commit 1a7bb68 - Model Usage Chart Tests (Branch: feature/model-usage-chart)

| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 24/24 packages |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | Built in 1.07s |
| Race Detector | ✅ PASS | 24/24 packages, 0 races (after 2 quick fixes) |
| New Test Lines | ✅ 1192 | counter_test.go (493), handlers_usage_test.go (699) |
| New Test Functions | ✅ 25 | 8 usage + 17 API handler tests |

**Quick fixes applied:**
- `2f15b2c` — Fix race condition in TestHeartbeat (safeResponseWriter wrapper)
- `02f1b84` — Fix race condition in TestRaceScenario_FallbackWins (atomic.Int64)

### Commits 725b115 + d5fe2d7 + ab85359 - MCP Test Connection Button Fix (Branch: fix/mcp-test-connection)

| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 25/25 packages, 4146 tests |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | Built in 2.84s |
| Browser Tests | ✅ 3/3 | Add Server, Edit Server, SSRF protection |
| Quick Fixes | 1 | Missing error field in testResult (commit `ab85359`) |

**Browser automation verified:**
- Test A: Add Server mode — button calls `/fe/api/mcp-servers/test-connection` ✅
- Test B: Edit Server mode — button calls API ✅
- Test C: SSRF protection — localhost URLs blocked ✅
 blocked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
ion verified:**
- Test A: Add Server mode — button calls `/fe/api/mcp-servers/test-connection` ✅
- Test B: Edit Server mode — button calls API ✅
- Test C: SSRF protection — localhost URLs blocked ✅
 blocked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
cked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
blocked ✅
✅


## Race Slices (db-cache-layer gate 2026-08-29 @ b48a14f — full `go test ./... -race` equivalence, split into 12 scoped slices each under 5 min)

| Slice | Exact scope | Result |
|-------|-------------|--------|
| race_proxy_root | `timeout 300 go test -race -count=1 -timeout 280s ./pkg/proxy/` | **PASS** — 57.6s, 468 funcs, 0 races |
| race_proxy_sub | same + `-race` on `./pkg/proxy/translator/ ./pkg/proxy/normalizers/ ./pkg/proxy/token/` | **PASS** — ~2s |
| race_modelscache_x10 | `timeout 300 go test -race -count=10 -timeout 280s ./pkg/modelscache/` | **PASS** — 10/10 clean, 36.2s (USER-MANDATED repetition; incl. new realerror + edge-case tests @ b48a14f) |
| race_store | `./pkg/store/...` | **PASS** — 14.2s; stabilized BusDrain test deterministic; quarantined CloseLifecycle did not fire |
| race_mcp | `./pkg/mcp/...` | **PASS** — 19s |
| race_misc_small | `./pkg/{models,auth,config,crypto,credentiallb,providers,proxyheader,events,bufferstore,supervisor,usage,ui,middleware}/...` | **PASS** — 13/13 pkgs, ~21s wall (gzipmw 20.7s dominant) |
| race_testroot_buildgate | `./test/ ./test/reasoning_content/ ./cmd/...` + `go build ./... && go vet ./...` | **PASS** — 10s; build+vet silent |
| race_e2e_g1 | `./test/e2e_reasoning_content/` | **PASS** — 4.3s |
| race_e2e_g2 | `./test/e2e_fe_reasoning_observability/ ./test/e2e_minimax_reasoning/` | **PASS** — 18.5s (240s historical plain budget NOT approached under race) |
| race_e2e_anthropic | `./test/e2e_anthropic_thinking_leak/` | **PASS 4/4** — S3 FIXED on this branch (see LESSONS/2026-08-29-s3-anthropic-green-on-dbcache-branch.md) |
| race_repo_root | `./` | **NO-TEST-FILES** (root pkg = scratch CLI test_load.go only) — sweep coverage complete |
| `./` | **NO-TEST-FILES** (root pkg = scratch CLI test_load.go only) — sweep coverage complete |

## Ad-hoc gate packs — 2026-10-23 auto-build-version functional gate (wt feature/auto-build-version @ e12c52b)

All packs: go1.26.0 toolchain on PATH + `GOTOOLCHAIN=local` + `GOMODCACHE/GOCACHE/GOPATH` pinned + **isolated HOME** (`/tmp/gate-*`). Full report: `RESULTS/2026-10-23-auto-build-version-functional.md`.

| Pack | Command (inline) | Scope | Timeout | Last Run | Status |
|------|------------------|-------|---------|----------|--------|
| gate_go_g1 | `timeout 300 go test ./pkg/proxy/... -count=1 -timeout 240s` | proxy, normalizers, token, translator (4/41) | 300s/240s | 2026-10-23 | PASS (4/4 ok, 92s; @ e12c52b) |
| gate_go_g2 | `timeout 300 go test ./pkg/store/... ./pkg/credentiallb ./pkg/grouplb ./pkg/modelscache ./pkg/auth ./pkg/crypto ./pkg/config ./pkg/models -count=1 -timeout 240s` | persistence/LB lane (9/41) | 300s/240s | 2026-10-23 | PASS (9/9 ok, 45s; TOCTOU green run-1) |
| gate_go_g3 | `timeout 300 go test . ./scripts ./pkg/providers ./pkg/mcp ./pkg/toolcall ./pkg/toolrepair ./pkg/supervisor ./pkg/bufferstore ./pkg/events ./pkg/usage ./pkg/memlimit ./pkg/middleware/... ./pkg/proxyheader ./pkg/loopdetection/... ./pkg/logger -count=1 -timeout 240s` | infra/misc lane (17/41; 3 no-test pkgs) | 300s/240s | 2026-10-23 | PASS (17/17 ok, ~50s; usage-stress + httpbin + SSRF all clean run-1) |
| gate_go_g4 | `timeout 300 go test ./cmd ./pkg/ui ./pkg/imggen ./pkg/decision/... ./test/... -count=1 -timeout 240s` | kinds + cmd + ui + test-root (11/41) | 300s/240s | 2026-10-23 | PASS (11/11 ok, 31s; NEW version guards green) |
| gate_fe_vitest | `cd pkg/ui/frontend && timeout 300 npx vitest run` | FE full suite incl. tsc-baseline comparator + NEW Header.test.tsx | 300s | 2026-10-23 | PASS (23 files / 237 tests / 0F / 0S; comparator 5/5; 19s) |
| gate_e2e_version_injection | 3× `go build` (default / `-X main.Version=424242 -s -w` / `-X main.Version= -s -w`) → serve (isolated HOME, `APPLY_ENV_OVERRIDES=1 PORT=1812x/1813x/1814x`) → curl `/fe/api/version` + banner grep → kill | version-injection closure proof (v150 symptom) | 290s outer / 240s per build | 2026-10-23 | PASS 3/3 (dev / 424242 / dev-on-empty; banners match; ports verified freed; 36s) |
| gate_version_guards | `timeout 120 go test ./cmd ./pkg/ui -run Version -count=1 -v` + `timeout 240 npx vitest run src/components/__tests__/Header.test.tsx` | fresh version-guard re-runs | 120s/240s | 2026-10-23 | PASS (4 tests + 3 subtests; Header 1/1; 7s) |

Partition audit fact: G1(4)+G2(9)+G3(17)+G4(11) = 41 = `go list ./...` count — union-proven full-suite coverage.

## Ad-hoc gate packs — 2026-10-27 openrouter provider-registration gate (feature/openrouter-provider @ 2be5d47, pack committed 815a9ff)

Full report: `RESULTS/2026-10-27-openrouter-provider-registration-gate.md`. All Go packs: isolated HOME `/tmp/or-gate-*` or `/tmp/or-smoke/home`, real GOCACHE/GOMODCACHE (pin them — overriding HOME alone relocates the caches), `DATABASE_URL` unset, real config dir never opened (macOS resolves `os.UserConfigDir()` → `~/Library/Application Support/llm-supervisor-proxy/`, NOT `~/.config` as the blueprint states).

| Pack | Command (inline) | Scope | Timeout | Last Run | Status |
|------|------------------|-------|---------|----------|--------|
| gate_or_full_regression | `timeout 300 go test ./... -count=1 -timeout 270s` | ENTIRE suite — 41/41 packages (`go list ./...` = 41) | 300s/270s | 2026-10-27 | **PASS** (38 ok + 3 no-test-files; 86s wall; go1.26.1 darwin/arm64; `pkg/providers` incl. NEW openrouter_test.go 5/5 executed unguarded; **zero quarantine adjudications needed** — all active QUARANTINE.md flakes passed run-1) |
| gate_or_runtime_smoke | `timeout 300 bash test/test_provider_registration_smoke.sh` | Real-binary boot (isolated HOME, `APPLY_ENV_OVERRIDES=1 PORT=18240`) → GET /fe/api/providers openrouter entry → credential-create 201 → model validate `{"valid":true}` → model create 201 → NEG `"openrouter "` 400 + NEG `"OpenRouter"` 400 + shared-provider mixed-brand 400 → /healthz 200 → /ui/ 200 html → SIGTERM exit 0 → port freed | 300s/280s | 2026-10-27 | **PASS 18/18** (pre-commit proof 1.3s; re-verified from repo path post-commit 3.0s @ 815a9ff; explicit-args run 18/18; deepseek cross-brand run 18/18 proves the pack is brand-generic) |
| gate_or_build | `timeout 300 bash test/packs/build_gate_test.sh` | go build + go vet | 300s/120s | 2026-10-27 | **PASS** (5s; ensure.md Critical #2/#3; new test file type-checked by vet even though go build skips _test.go) |
| gate_or_fe_vitest | isolated copy: `rsync -a --exclude node_modules --exclude .vite-temp pkg/ui/frontend/ /tmp/or-fe/frontend/` → `npm ci` in copy → `timeout 300 npx vitest run` in copy | FE suite + tsc-baseline comparator (types.ts in change set → ensure.md Critical #4) | 300s (+240s ci) | 2026-10-27 | **PASS** (23 files / 242 tests / 0F/0S; comparator 5/5 = zero NEW tsc error locations; repo node_modules stale since 2026-05-22 → vitest/happy-dom/@testing-library missing; worked around via isolated copy, repo tree mtime-diff verified clean — see skill `isolated-test-pack-run`) |

Notes:
- New committed pack: `test/test_provider_registration_smoke.sh` @ **815a9ff** (526 lines, executable, matches the 20 other test/*.sh convention; NOT run by CI — verified no workflow/Makefile/script globs test/*.sh; manual gate like every other shell pack here). Gate a NEW brand: `timeout 300 bash test/test_provider_registration_smoke.sh <brand> "<Display Name>" <base_url> <port 10000-19999>`.
