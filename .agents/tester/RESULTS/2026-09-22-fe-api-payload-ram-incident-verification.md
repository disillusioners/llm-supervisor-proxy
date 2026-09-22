# Test Report: FE API Payload RAM Incident — Independent Functional Verification

Date: 2026-09-22 (06:17–07:05 UTC)
Branch: `fix/fe-api-payload-ram-incident` @ b832ea3f (HEAD = base) with **24 uncommitted entries** (12 M, 3 D, 9 ??)
Review status at start: APPROVED (2 rounds, probe-verified) — this run is independent functional verification + closure against original symptoms.
Plan ref: `docs/2026-09-22-fe-api-payload-ram-incident.md`
Worker instances: bde37197 (static+baseline) · fd84e564 (FE) · 5585cb45 (targeted matrix) · 50f1346f (runtime smoke) · 3157fc23 (mock pack)
Repo treatment: **read-only** — no modifications, no stash/reset/clean/checkout, no commit/push; baseline used separate worktree `/tmp/lsp-base-check` (removed after); main-tree dirty count 24 → 24 at every checkpoint. Port 8088 never touched.

## Scope Decision
Full suite WAS warranted: change is cross-module (pkg/ui + pkg/middleware/gzipmw + pkg/store + cmd/main.go + k8s/values.yaml + FE frontend) and this is an incident-closure gate. Executed as: full static suite + baseline proof (wave 1), then targeted -v / runtime smoke / legacy mock pack (wave 2) — serialized waves to keep the full-suite + baseline comparison free of concurrent-load distortion (SQLITE_BUSY flake amplification risk).

**FINAL VERDICT: PASS-WITH-RESIDUALS** (residual list at end)

---

## §1 Full static verification — PASS (with proven-pre-existing baseline failures)

Environment note (applies to every Go command this session): system go 1.22.2 < go.mod's `go 1.26`; `GOTOOLCHAIN=auto` fails on this VM (toolchain download unavailable) → all go commands ran with `GOTOOLCHAIN=go1.26.0` (env-only).

- `go build ./...` → **OK** (exit 0)
- `go vet ./...` → **OK** (exit 0)
- `GOTOOLCHAIN=go1.26.0 timeout 300 go test ./... -count=1` → **TEST_EXIT:1**, 57.7s, **31 ok / 2 FAIL** packages

Failing tests (verbatim, complete list):
```
--- FAIL: TestValidateUpstreamURL (pkg/mcp)
    --- FAIL: TestValidateUpstreamURL/SSRF_octal_IP_should_be_rejected   (validation_test.go:200, err=nil wantErr=true)
    --- FAIL: TestValidateUpstreamURL/SSRF_decimal_IP_should_be_rejected (validation_test.go:200)
    --- FAIL: TestValidateUpstreamURL/SSRF_hex_IP_should_be_rejected     (validation_test.go:200)
--- FAIL: TestConcurrentIncrementModelUsage_Stress (pkg/usage, 7.67s)   (counter_stress_test.go:205, SQLITE_BUSY "database is locked (5)")
--- FAIL: TestConcurrentDifferentBuckets_Stress (pkg/usage, 10.87s)     (counter_stress_test.go:495, SQLITE_BUSY)
```

Baseline proof (worktree `/tmp/lsp-base-check` @ b832ea3f, removed after, exit 0):
| Failing test | Fix branch | Base b832ea3f | Classification |
|---|---|---|---|
| SSRF octal / decimal / hex (×3) | FAIL (deterministic, 0.00s) | **FAIL byte-identical** | **PRE-EXISTING** (env-deterministic on Linux+go1.26; no proxy env vars set) |
| TestConcurrentIncrementModelUsage_Stress | FAIL 1/1 full-suite → **PASS 3/3** re-run | PASS full-suite → PASS stress 3/3 | **PRE-EXISTING flake** (timing SQLITE_BUSY) |
| TestConcurrentDifferentBuckets_Stress | FAIL 1/1 full-suite → **PASS 3/3** re-run | PASS full-suite → **FAIL 2/3** stress re-run | **PRE-EXISTING flake** (fails 2/3 on untouched base) |

- `git diff --stat HEAD -- pkg/usage pkg/mcp` → **0 lines** (fix touches neither).
- Quarantined `TestStoreEngine_CloseLifecycle` (macOS-only cleanup race): **PASSED** on this Linux VM (0.010s full-suite, 0.21s targeted).
- **No NEW regressions. No unexpected failures.** "ALL fix-branch failures genuinely pre-existing: YES."
- Worktree removed (`ls /tmp/lsp-base-check` → No such file); `git worktree list` = main tree only; dirty count 24 before/after.

## §2 Runtime smoke (symptom #1 — the 96 MB firehose) — PASS a–g

Real binary built from the uncommitted tree (`go build -o /tmp/lsp-smoke/lsp-proxy ./cmd`, exit 0), isolated (`HOME=/tmp/lsp-smoke/home`, `XDG_CONFIG_HOME` under it, `DATABASE_URL` unset), port 17891. Store populated with 5 synthetic failing proxy POSTs (dead upstream localhost:4001 — zero real external calls) so entry-shape + gzip assertions ran against a **populated** store.

| # | Check | Result | Key evidence |
|---|---|---|---|
| a | `/fe/api/requests` default | PASS | 200; array; 5 entries; `all(.[]; has("message_count") and has("total_size_bytes") and (has("messages")\|not))` = **true**; 2811 B total |
| b | `?include=messages` legacy | PASS | 200; `all(.[]; has("messages"))` = true |
| c | gzip round-trip | PASS | 2811 B → 592 B (**−79%**); `GZIP_BODY_IDENTICAL` after decode+diff |
| d | SSE exclusion | PASS | headers contain **no** Content-Encoding; `text/event-stream`; CURL_EXIT:28 (held open, expected) |
| e | `/fe/api/ram` | PASS | 200; `{"alloc_bytes":1049928,"alloc_mb":1.0012…}` |
| f | `/healthz` | PASS | 200 |
| g | UI embed | PASS | `/ui/` serves HTML; all 3 NEW bundles 200 (index-BrqJ5p4o.js 302,049 B; index-F0oa6lst.css 51,448 B; SettingsPage-C9t90y2b.js 126,988 B); referenced set == expected set; **zero 404s** |

Verbatim load-bearing headers:
```
[h3 — gzip]                       [h4 — SSE, no compression]
HTTP/1.1 200 OK                   HTTP/1.1 200 OK
Content-Encoding: gzip            Cache-Control: no-cache, no-transform
Content-Type: application/json    Connection: keep-alive
Vary: Accept-Encoding             Content-Type: text/event-stream
Content-Length: 592               Transfer-Encoding: chunked
```
Notes: (1) `MinResponseSize = 1024` — bodies < 1 KiB pass through uncompressed with `Vary` still emitted (by design; gzip engages above threshold, verified populated). (2) Old deleted bundle names return 200 only via the pre-existing SPA index.html fallback (HTML body) — no stale assets in the embed. (3) Deviation: `PORT` env is gated behind `APPLY_ENV_OVERRIDES=1` (`pkg/config` applyEnvOverrides) — first launch bound 4321; killed (identity-verified) and relaunched with the gate set. Env-only; upstream question, untouched. Shutdown: graceful (`Shutting down server...`/`Server exiting`), `ss -ltn | grep 17891` empty, `NO_STRAYS`, 8088 untouched.

## §3 Targeted test verification — PASS (8/8 matrix covered)

`GOTOOLCHAIN=go1.26.0 timeout 300 go test ./pkg/ui/... ./pkg/middleware/gzipmw/... ./pkg/store/... -v -count=1 -timeout 280s` → **GO_EXIT:0**, 15s warm.

| Package | Status | PASS | FAIL | SKIP |
|---|---|---|---|---|
| pkg/ui | ok | 114 | 0 | 0 |
| pkg/middleware/gzipmw | ok | 35 | 0 | 0 |
| pkg/store | ok | 17 | 0 | 0 |
| pkg/store/database | ok | 110 | 0 | 4 (PG-gated, expected) |
| **Total** | — | **276** | **0** | **4** |

Coverage matrix — all 8 acceptance-critical behaviors covered by PASSING tests:
| Behavior | Covering test | Result |
|---|---|---|
| a. same-pointer re-Add byte-accounting | TestRequestStore_SamePointerReAdd_TracksGrowth (memory_test.go:773) | ✅ |
| b. totals never negative | TestRequestStore_TotalBytesNeverNegative_AfterMutatedEviction (:818) | ✅ |
| c. overwrite-over-budget eviction | TestRequestStore_OverwriteOverBudget_EvictsPromptly (:856) + ByteBudget_EvictsOldestWhenOverBudget | ✅ |
| d. 204 not gzipped | TestCompressResponse_BodylessDelete_KeepsStatus204 (response_test.go:369) | ✅ |
| e. panic-truncation | TestCompressResponse_PanicAfterGzipWrite_StreamIsTruncated (:432) | ✅ |
| f. 200-clamp from oversized store | TestHandleRequests_Pagination_ClampAt200_FromOversizedStore (:749) + _ExplicitLimitWayAboveCap | ✅ |
| g. byte-level golden parity (include=messages list + detail) | TestHandleRequests_IncludeMessages_ByteLevelGoldenParity (:835) + TestHandleRequestDetail_ByteLevelGoldenParity (:869) — `bytes.Equal(wireBody, expectedBody)` | ✅ |
| h. SSE excluded from gzip | TestCompressResponse_ExcludesSSE (:255) + OnlyAffectsFeApiPaths | ✅ |

**NOT COVERED: none.**

Incident-remediation static evidence (accurate nuance):
- `cmd/main.go:43` — GOMEMLIMIT appears as **comment only**; NO `debug.SetMemoryLimit` anywhere in Go code. Code-level guard = store byte budget: `store.NewRequestStore(100, store.WithMaxBytes(256<<20))` (main.go:46-48).
- Soft memory limit ships at **deployment layer**: `k8s/templates/deployment.yaml:37-38` env `GOMEMLIMIT = .Values.goMemLimit | default resources.limits.memory | default "1Gi"`; `k8s/values.yaml:59 goMemLimit: "1Gi"` (~50% of the 2Gi limit; comment cites this incident doc); resources: limits 2Gi/1-cpu, requests 512Mi/100m.
- `-race` slice (pkg/store + gzipmw, `-count=1`): **PASS, 0 races**, RACE_EXIT:0 (71s).

## §4 FE verification — PASS

- Changed FE: App.tsx, EventLog.tsx, RequestDetail.tsx, RequestList.tsx, useApi.ts, useEvents.ts, types.ts; static assets swapped (3 deleted, 3 new).
- tsc --noEmit: **30 total errors = documented standing baseline debt**; errors in changed files: 1 (EventLog.tsx:87 `secondary_model`) — **proven pre-existing** (line 87 identical in HEAD; diff hunks at 32/107/159 only; field never declared in HEAD's EventData either). **NEW errors in changed files: 0.**
- Reproducible build (in /tmp copy, repo untouched; vite outDir overridden off `../static`): exit 0, 4 files, **byte-identical (sha256) to committed bundles** — SettingsPage-C9t90y2b.js, index-BrqJ5p4o.js, index-F0oa6lst.css, index.html. Reproducible: **YES**.
- Windowing (RequestDetail.tsx): WINDOW_MESSAGE_ESTIMATED_HEIGHT=220 (:20), WINDOW_OVERSCAN=6 (:21), initial mount=20 (:35), markdown LRU cache=100 (:88), RAF-coalesced scroll (:50-58) + ResizeObserver.
- Refresh model: optimistic `patchListEntry(id,{status:'completed'})` (useEvents.ts:290), `fetchAndPatchById` on request_started (:296), **3000 ms trailing debounce** full refresh (:277) bypassing SWR cache (App.tsx:96-103); RAM polling 5000 ms (useApi.ts:445). Comments document "metadata-only + limit=50 ⇒ ~50 KB" fetch economics.
- DOMPurify: repo-wide exactly ONE `dangerouslySetInnerHTML` site (RequestDetail.tsx:115), fed exclusively by `DOMPurify.sanitize(marked.parse(...))` (:105); no other markdown/HTML injection paths. **No gaps.**
- Repo integrity: dirty count 24 → 24.

## §5 Closure against ORIGINAL symptoms

| Original symptom | Verdict | Evidence map |
|---|---|---|
| **1. RAM ratchet — /fe/api/requests returned ~96 MB full-conversation payloads every 4-15s → 1508Mi/2Gi** | **RESOLVED-VERIFIED** | metadata-only default: smoke (a) populated store — no `messages` key, `message_count`+`total_size_bytes` present (2811 B / 5 entries) + ui handler tests (114 pass). gzip on wire: smoke (c) −79% + `Vary`, decode-identical; gzipmw 35 tests incl. 204/SSE-exclusion/panic-truncation. FE cadence: 3s trailing debounce + patch-by-id (useEvents.ts:277/290/296) replaces 4-15s full-payload polling; RAM poll 5s. GOMEMLIMIT/k8s: values.yaml goMemLimit=1Gi (50% of 2Gi) via deployment.yaml env + resources 2Gi/512Mi (inspection — runtime k8s not exercisable here). Byte-budget eviction: WithMaxBytes(256Mi) main.go:46-48 + eviction/never-negative/same-pointer tests + `-race` clean. |
| **2. Detail view hang on ~800-message conversations (794 msgs / 1.2 MB payload)** | **VERIFIED-BY-BUILD/INSPECTION — residual browser QA** | Backend contract unchanged: TestHandleRequestDetail_ByteLevelGoldenParity PASS (byte-identical full detail). FE windowing built + verified in build: 220px/6 overscan/20 initial mount, RAF+ResizeObserver, LRU=100; DOMPurify intact. Runtime browser verification not possible headlessly in this harness → residual. |
| **Regression: legacy consumers** | **RESOLVED-VERIFIED** | `?include=messages`: smoke (b) legacy shape 200 + byte-level golden parity test PASS. Mock script `test/mock_rsd_m2_anthropic_ultimate_ui.sh` (registered pack, ports 10120/10121, isolated HOME): **OVERALL: PASS**, exit 0 ×2 — scenarios D & F explicitly consume the full `?include=messages` payload (role/content/thinking per record) and pass; E hard gate id-normalized byte-identity holds (322B sha-identical both modes); ports freed, repo untouched. |

## ensure.md status (blast-radius scoped)
- Critical: build ✅ · vet ✅ · full-suite ✅-with-pre-existing-caveat (2 packages fail; every failure proven pre-existing on base b832ea3f; zero diff under those dirs) · frontend build ✅ + **0 NEW tsc errors in changed files**.
- Important (peak-hours, migration 018): out of this change's blast radius — not validated this run.
- Nice-to-have (races): `-race` clean on both changed Go packages.

Improvement notices (ensure.md is user-owned; not modified):
- "All Go unit tests pass (`go test ./...`)" assumes a clean baseline; on this VM the baseline is red (env-deterministic mcp SSRF ×3 + usage SQLITE_BUSY flakes — now documented in QUARANTINE.md). Suggested rewrite: "no NEW failures vs. base commit baseline, all failures classified pre-existing/flaky (QUARANTINE.md)".
- "Frontend builds successfully without TypeScript errors" vs 30 standing tsc errors (pre-existing debt): validated as "vite build exit 0 AND zero new tsc errors in changed files".

## Residuals (explicit)
1. **Browser QA of windowed RequestDetail** on a ~800-message conversation (symptom 2 runtime FE behavior) — verified by build + inspection only; headless browser not in this harness.
2. **k8s runtime behavior** (actual RSS curve with GOMEMLIMIT=1Gi under production traffic) — deployment-layer change verified by inspection only.
3. **No in-code `debug.SetMemoryLimit`** — GOMEMLIMIT applies only where the k8s manifest (or operator) sets it; non-k8s runs rely on the 256 MiB store byte budget. Noted, not a defect for the k8s incident; flagged for the author's intent (comment at cmd/main.go:43 suggests code-level was considered).
4. **Pre-existing baseline failures persist** on base AND fix branch (mcp SSRF ×3 env-deterministic; usage SQLITE_BUSY ×2 flaky) — not introduced by this fix; recommend upstream follow-up (both now in QUARANTINE.md with wiring deferred).
5. Operational: `PORT` env gated behind `APPLY_ENV_OVERRIDES` (surprised the smoke harness); old bundle names 200 via SPA fallback (pre-existing catch-all) — informational only.

## Documentation updated
- RESULTS/2026-09-22-fe-api-payload-ram-incident-verification.md (this file)
- QUARANTINE.md — added mcp SSRF (env-deterministic) + usage stress (flaky) rows, wiring deferred
- PACKS.md — rsd_m2_anthropic_ultimate_ui row: 2026-09-22 re-run PASS @ uncommitted fix branch
- LESSONS/2026-09-22-ensemble-vm-go-toolchain-and-shell-gotchas.md

No repo source files were modified at any point (read-only mandate held; all scratch in /tmp/lsp-*).
