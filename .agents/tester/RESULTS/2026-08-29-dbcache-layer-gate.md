# Test Report: DB Caching/Resilience Layer MVP — incident close-out gate

Date: 2026-08-29
Branch: `feature/db-cache-layer` @ 42d98f3 (+ test-only commits c9559cf, b48a14f, 0be8b8f, a753698)
Workdir: /Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy
Instance IDs (workers): 23571671 (W1 discovery) · 6fd3754d (W2 flake) · 86b9d6c0 (W3 mock-audit) · f066e786 (W4 harness) · fd6b599d (W5 pack A) · 4e190715 (W6 pack B) · 6f276b7c (W8 ensure) · 11 race-slice workers (e99bf50a, 39b877e2, d7b56235, f46a9500, 3ef789e0, f7bef392, 8ddd26fe, 912356ed, 48b17fea, e2c52629, 1fee2ab9, 2dbe1035)

## Summary

- **Original incident failure mode (silent internal→external misroute): FIXED and proven fixed** — 3 deterministic process-level runs with a real corrupted SQLite DB: zero misroutes to `localhost:4001`, zero `[WARN] Race attempt` credential lookups, unknown-model → 503 `config_store_unavailable` on both wire protocols, recovery ≤120s.
- **One HIGH product finding (F1)**: the outage-resilience goal breaks at t≈60s on SQLite file-corruption outages — `isInfraError` doesn't classify real SQLite error shapes, so the token stale-tier never engages → valid tokens 401. Confirmed independently by package-level real-driver audit AND 3× process-level E2E. PG-shaped outages are covered (incident dialect).
- Full `go test ./... -race` equivalence: **ALL GREEN** (12 scoped slices + repo root). `pkg/modelscache -race -count=10`: 10/10 clean.
- Flake stabilized (test-only, c9559cf). Rollback lever, write-through (models/tokens), UI: PASS. Credential write-through is reconciler-latent by design (findings F2/F3).
- ensure.md Critical: **4/4 PASS** (one standing contradiction notice re TS wording).
- **Overall: NOT READY to declare the ≥1h-zero-failure goal met for SQLite (default) deployments until F1 is fixed.** The incident's specific misroute chain, however, is closed.

---

## Scenario Results (original incident close-out)

| # | Scenario | Verdict | Evidence |
|---|----------|---------|----------|
| 1 | Warm cache → DB outage → zero 4001 hits, zero `external` credential lookups, all known models served | **PARTIAL — misroute goal PASS, sustained-serving FAIL>60s (F1)** | Pack A ×3 (deterministic): sentinel misroutes 0 (8 hits all = deliberate external model routing to global default upstream); `[WARN] Race attempt` count 0; known internal models 200 through t≈45s; **t=60–90s: OpenAI-path internal requests → 401 (F1)** while ext-known + `/v1/messages` stayed 200 through t=90. Reconciler correctly held last-known-good (`strict read failed (database disk image is malformed (11)) — no swap`) |
| 2 | Unknown model + DB down → 503 `config_store_unavailable`, both endpoints, never passthrough | **PASS** | Pack A A5 at t=10 and t=80, ×3 runs: 503 + `config_store_unavailable` on `/v1/chat/completions` AND `/v1/messages`; package boundary tests (6) + `TestProxyIntegration_UnhealthyDecoratorReturns503EndToEnd` |
| 3 | Valid token survives >60s (stale tier); revoked/deleted token NEVER authenticates | **SPLIT — valid>60s FAIL on SQLite (F1); revoked/garbage PASS** | T_valid → 401 at t=70/t=90 (expected 200) — 3× deterministic; T_revoked (deleted pre-outage) → 401 always; `sk-garbage` → 401 always. Package-level: `RowA2` stale-tier PASS for infra-class (PG) errors; `RowB2` verdicts fail-closed PASS. Anthropic-endpoint parity gap: invalid tokens degrade to anonymous instead of 401 (F5, "accepted residual risk per A-1") |
| 4 | API mutation during healthy DB → cache reflects immediately | **PARTIAL** | Models add/update/remove: immediate (B6.1/B6.4). Tokens create/delete: immediate (B6.5, incl. revoked-token 401 in pack A). **Credentials: NOT immediate** — attach of just-created cred rejected until reconciler tick (F2, `references an unknown credential`); key rotation propagates ~55s / iter-25 (F3, zero stale-key leakage). Invalidate-only is the documented architecture choice (plan §3) — it meets the plan's semantics but not the scenario's "immediately" bar |
| 5 | `CACHE_LAYER_ENABLED=false` → pre-cache behavior | **PASS** | Pack B B1–B4: flag-off log line present; **original misroute bug reproduced 8/8** (unknown-model → sentinel, no 503-CSU) proving full bypass; boot 2 default-on healthy. Note (F4): on SQLite, flag-off + file-corruption does not instantly nil known models (hot page caches) — on PG the original instant-nil shape applies |

## Acceptance Criteria A–O

| # | Verdict | Evidence / caveat |
|---|---------|-------------------|
| A | **PARTIAL** | Simulated 1h outage test PASS (package, PG-shaped errors, clock-advanced, incl. token stale-tier past TTL). Process-level: model serving + zero misroutes hold; **token continuity >60s FAILS under real SQLite file corruption (F1)**. credFailover spawn path not exercised at process level (package/unit coverage only) |
| B | **PASS** | Process ×3 both endpoints + package Row4 |
| C | **PARTIAL** | Stale-tier >60s: PASS for infra-class (PG) shapes (RowA2); **FAIL for real SQLite NOTADB/malformed shapes (F1)**. Verdict-class fail-closed: PASS (RowB2 + E2E revoked/garbage 401s) |
| D | **PASS** (OpenAI path) | Never-seen token + DB down → 401 (E2E garbage-token ×3; RowC; NeverSeenDuringOutage). Anthropic path degrades to anonymous (F5) — flagged, matrix row C's literal "401" not met on that endpoint |
| E | **PARTIAL** | 9 mutators: model ×3 + token ×3 synchronous ✅; credential ×3 invalidate-only by design → ≤60s latency (F2/F3). Contract tests green for the implemented semantics |
| F | **PASS** | Pack B boot-1 rollback repro + boot-2 default-on |
| G | **PASS** | Full race sweep green; legacy-safe boundary tests (`NoHealthCapabilityLegacySafe` ×2); pack B boot-2 |
| H | **PASS** (caveat) | 59s process-level (≤120s) via out-of-band direct-DB insert surfacing through `/fe/api/models`; package `Reconciler_HappyDownRecover`. **Caveat:** after SQLite file-corruption, the live process never self-recovers even after byte-identical restore — recovery verified post-restart. PG-class outages reconnect without restart |
| I | **PASS** | `DeepCopy_FallbackChain` + copy-helper tests, race-clean |
| J | **PASS** | stress test + `-race -count=10` 10/10 + 12-slice sweep, 0 races |
| K | **PASS** | Boot tripwire WARN observed 3× at process level (A0) + `Warn_DeadDefault` unit test |
| L | **PASS** | 12 scoped `-race` slices + repo root (no-test-files) all green @ b48a14f; changed-package re-confirmation @ a753698 |
| M | **PASS** | Compile-time (wired at handler_functions.go:144 / handler_anthropic.go:166) + runtime 503 E2E |
| N | **PASS** | Row5 + `DecryptFailedCredentialIsNeverServed` + NEW `DecryptFailedInScan_AbortsList` (scan aborts, bad row never in partial, ciphertext never served as APIKey) |
| O | **PARTIAL** | Models/tokens: zero drift (immediate). Credentials: bounded ≤60s drift by design (F2/F3) |

## Mock Quality Audit (W3, commit b48a14f)

- Fakes' dominant error shape = `*net.OpError("dial tcp … connection refused")` — a PG/TCP shape. Production driver = `modernc.org/sqlite` (in-process, never emits net.OpError).
- **FINDING-1 (HIGH, production, report-only):** `isInfraError` (health.go:66-113) matches only: net.OpError-with-Op, context.DeadlineExceeded, driver.ErrBadConn, and fragments {connection refused, no such host, i/o timeout, connection reset, database is closed, unexpected eof, server closed the connection unexpectedly}. Real SQLite outage errors — `unable to open database file: out of memory (14)` (SQLITE_CANTOPEN), `file is not a database (26)`/`database disk image is malformed (11)` (corruption), `SQL logic error: no such table` (truncation) — all classify **false**. Consequence: token stale-tier (tokens.go:214) never engages for file-level SQLite outages → the ≥1h-zero-failure goal is unmet on the default deployment dialect. **Independently confirmed end-to-end 3× by pack A (F1).** Models-cache path is unaffected (any non-notfound error marks unhealthy → 503 gate works — proven by A5).
- **FINDING-2 (LOW):** outage/contract matrix tests are spec-correct but exercise a PG-shaped failure the default deployment cannot produce; they should be read as PG-dialect validation.
- Permanent additions: `realerror_classification_test.go` (real-driver error classification audit, 6-row matrix) + `edge_cases_outage_test.go` (empty-DB-at-boot ✅ healthy; decrypt-failed scan abort ✅; token created during outage → valid after TTL post-recovery ✅; model renamed during outage → old name serves until reconciler converges ✅). All deterministic 10×.
- Harness empirical catalog (W4): M1 rm+mkdir → pool serves deleted inodes (no read outage); M2 dd-garbage → chosen (real live-process read errors); M3 truncate-0 → **SIGBUS crash** (modernc mmaps -shm); M4 chmod-000 → open fds unaffected.

## Flake Stabilization (W2, commit c9559cf)

`TestStoreEngine_BusDrainForwardsCredentialsChanged` (store_test.go): not reproducible in 180 attempts, but static diagnosis found a real race window — `time.Sleep(20ms)` is not a barrier; the drain goroutine can process the AddModel event after `RebindFromStore`, resetting the engine and making the sanity check a coin flip. Fix (test-only, +35/−4): bus sentinel-subscriber synchronization pre/post publish. Verified 20/20 plain + 20/20 race + full package green; race-slice re-confirmed. Production code untouched (root cause is test-side).

## Web UI Check (pack B B5)

PASS — cache-on boot: `/ui/` 200 + HTML; `/fe/api/models`, `/fe/api/tokens` (ListTokens pass-through), `/fe/api/requests` all 200 with seeded content; decorator does not break the UI path. Frontend build: vite OK (1.11s, 48 modules).

## ensure.md Validation (W8)

**Critical 4/4 PASS**: (1) Go tests — scoped slice 4/4 ok + full race sweep cited; (2) `go vet ./...` silent; (3) `go build ./...` clean; (4) `npm run build` exit 0 — with **contradiction notice**: requirement's "without TypeScript errors" wording is unmeetable against the standing 30-error tsc baseline (measured: exactly 30, unchanged); `npm run build` is vite-only by design. Suggested rewrite in the notice. Important items (peak-hour, migration 018) out of scope for this change set. Nice-to-have race-freedom: evidenced by sweep.

## Race Sweep (12 slices + root)

All PASS @ b48a14f: proxy root 57.6s · proxy subpkgs ~2s · **modelscache -race -count=10 10/10 (36.2s)** · store 14.2s · mcp 19s · ultimatemodel/loop/tool 8s · 13 misc pkgs ~21s · test-root+cmd+build/vet 10s · e2e g1 4.3s · e2e g2 18.5s · anthropic-leak **4/4 — S3 FIXED on this branch** (fixes 64da4ae+e60de91 ancestors; prior PARTIAL superseded) · repo root no-test-files. Logs under /tmp/race_*.log.

## Product Findings Register (report-only; no production code changed)

| ID | Severity | Finding | Evidence |
|----|----------|---------|----------|
| F1 | 🔴 HIGH | Token stale-tier never engages for SQLite file-level outages (isInfraError classifier gap) → valid tokens 401 after 60s TTL on default dialect | realerror_classification_test.go; pack A A4/A6 ×3 deterministic |
| F2 | 🟠 MED | Credential creation → model attachment rejected up to 60s ("references an unknown credential") — invalidate-only design vs immediate-visibility expectation | pack B B6.2 ×2 |
| F3 | 🟠 MED | Credential key rotation propagates ≤60s (reconciler-bound), not immediately | pack B B6.3 ×2 (iter-25 ≈ 55s; zero stale-key leakage) |
| F4 | 🟢 LOW | Flag-off + SQLite file-corruption: known-model reads survive via page caches (PG nils instantly) — rollback semantics differ by dialect | pack B B2 |
| F5 | 🟠 MED | `/v1/messages` auth-parity gap: invalid/expired tokens degrade to anonymous rather than 401 (documented residual risk A-1) | pack A A4 rounds 5-7 |
| F6 | 🟢 LOW | External models route to the GLOBAL UpstreamURL, never model credentials | harness F6 note |
| F7 | 🟠 MED | Live process never self-recovers after SQLite file corruption even with byte-identical restore (perpetual `malformed (11)`); restart required | W4 empirical; pack A A8 restart |

## Gaps / Not Verifiable

- ≥1h wall-clock outage at process level not run (pack cap 5 min; process-level outage was ~95s crossing both the 60s token TTL and one reconciler tick; the 1h duration is covered by clock-simulated package test ×10).
- credFailover spawn path under outage not exercised end-to-end (requires live 429 injection; covered by unit/package tests of engine eligibility + executor seams).
- `UpdateTokenPermission` write-through validated at package level only (E2E covered create/delete).
- PG dialect not exercised at process level (no local PG in harness; PG-shaped error coverage is package-level by design; the original incident was PG).

## Code Changes (all test-only, committed on feature/db-cache-layer)

- `c9559cf` test: stabilize credentiallb poll-timing test (store_test.go +35/−4)
- `b48a14f` test: real-driver error classification audit + outage edge cases for modelscache (+798, 3 files)
- `0be8b8f` test: E2E harness for db-cache-layer outage/rollback scenarios + modelscache pack (3 new files)
- `a753698` test: make pack A exit code reliable (+14/−17)
- docs: `.agents/tester/*` updates (this gate) — committed separately

## Documentation Updated

- [x] PACKS.md — modelscache pack row, dbcache E2E rows, race-slices table, anthropic row un-staled
- [x] MOCK_TESTS.md — both pack specs finalized with empirical mechanism + Last Run
- [x] LESSONS/ — 2026-08-29-credentiallb-busdrain-flake-stabilization.md, 2026-08-29-s3-anthropic-green-on-dbcache-branch.md, 2026-08-29-dbcache-outage-mechanisms.md (this gate's harness lessons folded here)
- [x] RESULTS/2026-08-29-dbcache-layer-gate.md — this report
- [ ] rules/ensure.md — untouched (user-owned)

### Overall Status

- Incident misroute chain (the 42-failed-request bug): ✅ **CLOSED** — proven fixed under the original scenario shape
- Race/build/vet/ensure gates: ✅ ALL GREEN
- ≥1h zero-failure outage goal: ❌ **NOT met for SQLite (default) deployments** — blocked by F1 (fix classifier to cover SQLite error shapes, or document PG-only resilience); F2/F3/F5/F7 to triage
- **Testing Complete: ❌ NOT READY — F1 must be fixed (or explicitly accepted as PG-only) before ship**


---

# DELTA RE-GATE (2026-08-29, fix commit a09877d) — VERDICT: ✅ READY-TO-MERGE

Fix under test: `a09877d` (5 files, +394/−33, all pkg/modelscache) — F1 classifier SQLite shapes + F2/F3 credential write-through + comment fold-in.

## 1. Pack A re-run (worker regate-packA, /tmp/packA_regate.log)
**RESULT: PASS, exit 0, 163.7s** — A4 **28/28 requests 200 through the entire ~90s SQLite-corruption outage** (was: 401s from t≈60s); A6 **T_valid 200 via stale tier at t=70 AND t=90** (was 401); T_revoked/garbage still 401; A7 zero misroutes + zero Race-attempt WARNs + strict-read WARN now shows the matched SQLite shape (`database disk image is malformed (11)`); A8 recovery 61s ≤120s; A5 503+CSU both endpoints unchanged. **F1 CONFIRMED FIXED at process level on the default (SQLite) dialect.**

## 2. Commit a09877d review (worker regate-diff-review) — PASS, 0 blockers
- Classifier: 3 new fragments (`unable to open database file` / `file is not a database` / `database disk image is malformed`) — all ≥22 chars, multi-word, SQLite-specific; no over-breadth. **Non-infra control verified**: `no such table` asserted `want=false` (tokens_test.go:535-540). Matrix forms (26)/(11)/(14) shape-injection tests present (tokens_test.go:522-524). Verdict-class precedence intact ahead of infra classification on both tokens and models paths.
- Write-through: AddCredential upserts decrypted (no ciphertext path exists in credEntry); UpdateCredential re-fetches via GetCredentialStrict (5s StrictFillTimeout), never trusts caller plaintext, re-fetch failure → WARN + preserve OLD entry; RemoveCredential evicts with engine/event side effects preserved; inner-store error → cache untouched on all 3 mutators (early-return guards, 4 sub-tests).
- Old invalidate-only test deleted; 4 new immediate-visibility tests assert **no reconciler list reads** during write-through. Regression surface contained to pkg/modelscache. outage_test.go change is comment-only (one word, line 358).
- 1 informational non-blocker: UpdateCredential returns nil (not error) on post-write re-fetch failure — intentional + commented; suggest a godoc note in a follow-up.

## 3. Scenario verdicts (delta)
- **Scenario 1: PASS** (SQLite included) — 28/28 through outage, zero misroutes, zero external-credential lookups.
- **Scenario 3: PASS** (SQLite included) — stale tier serves >60s; revoked/garbage fail-closed. (F5 /v1/messages auth-parity gap remains a pre-existing, documented residual risk — unchanged, non-blocking.)
- **Scenario 4: PASS** — credential attach accepted first-try (0 bounded-wait iterations, was: rejected until tick); key rotation visible at iter 1 (was iter ~25/~55s); zero stale-key leakage. Models/tokens were already immediate.
- F4/F6/F7 unchanged (informational; F7 restart-after-file-corruption remains a documented operational caveat, not a blocker).

## 4. Flake fix holds
c9559cf (`TestStoreEngine_BusDrainForwardsCredentialsChanged`): green in the full-suite re-gate slice (pkg/store/database ok 13.6s, zero FAIL lines). Quarantined CloseLifecycle did not fire.

## 5. Full `go test ./... -race` @ a09877d — ALL GREEN (5 consolidated whitelisted slices)
- s1 `./pkg/proxy/` ok 57.2s (run twice: 57.2s/57.3s, deterministic)
- s2 `./pkg/modelscache/` **-race -count=10 → 10/10**, ok 36.6s
- s3 `./pkg/store/...` + proxy subpkgs ok (store/database 13.6s, translator/normalizers/token ok)
- s4 mcp/ultimatemodel/loopdetection/toolrepair/toolcall ok (6 pkgs, 20s)
- s5 13 misc pkgs + test/ + 5 e2e dirs + cmd — **20 ok packages**, 0 FAIL, build+vet silent (anthropic_thinking_leak green as expected; S3 fix holds)

## Verdict
**✅ READY-TO-MERGE.** All gate findings resolved (F1/F2/F3 verified fixed end-to-end and by review; F4/F5/F6/F7 documented non-blockers), full -race suite green, ensure.md Critical 4/4 (unchanged by delta; build+vet re-verified), E2E regression harnesses in place and green (`test/test_e2e_dbcache_outage.sh` PASS exit 0; `test/test_e2e_dbcache_rollback_ui.sh` PASS).
