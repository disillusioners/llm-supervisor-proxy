# Mock Test: E2E FE Reasoning Observability PATH MATRIX (Task 2)

### Metadata
- **Created**: 2026-08-21
- **Script**: `test/e2e_fe_reasoning_observability/path_matrix_test.go` (+ harness extension in `harness_test.go`)
- **Language**: Go (in-process; reuses the FE-API-over-real-HTTP harness from the closure-gate suite)
- **Status**: ACTIVE — 16/16 matrix rows pass

### Configuration
- **Timeout**: dual-layer — outer `timeout 300 go test ./test/e2e_fe_reasoning_observability/ -v -count=1 -timeout 240s`
- **Ports**: NONE fixed — `httptest` ephemeral loopback only; no 8088
- **Cleanup**: in-process; reaped on test exit

### What It Tests
Thinking visible in the FE API `GET /fe/api/requests/{id}` on EVERY capture
path: race x stream/non-stream, ultimate x stream/non-stream x internal/external,
anthropic-client (internal stream + external non-stream), plus MiniMax
translated rows (reasoning_details → reasoning_content) and negative
omitempty-cleanliness rows.

### Test Scenarios
- R1..R10 positive core matrix (byte-exact thinking via FE API)
- N1..N4 negative rows (ZERO "thinking" occurrences when no reasoning upstream)
- M1 minimax race-external stream translated; M2 minimax ultimate-external non-stream translated

### Success Criteria
- [x] All 16 rows pass; byte-exact equality on positives; strict absence on negatives
- [x] Suite runtime 1.764s (well under caps)
- [x] No process/port leaks

### Implementation Notes
- Ultimate rows: X-Force-Ultimate-Model + ultimateModelEnabled token + ULTIMATE_MODEL_MAX_RETRIES=0 (ForceTrigger fires on first call)
- Anthropic rows: HandleAnthropicMessages with x-api-key + anthropic-version headers
- **GOTCHA (cost one fail/re-run)**: the ultimate-external MiniMax translation gate reads the MODEL's CredentialID, NOT the global upstream credential — M2 needs a minimax-credentialed ultimate model (`matrix-ultimate-external-minimax`)
- R1 is the closure-gate scenario kept as matrix row 1 (Scenario A test still exists)

### Last Run
- **Date**: 2026-08-21
- **Session**: Worker (executor session)
- **Result**: PASS — 16/16 rows + 4/4 closure gate, runtime 1.764s (stable across 2 runs)
- **Quick Fixes**: M2 harness wiring (minimax-credentialed ultimate model) — harness-only, no assertion weakened
- **Report**: RESULTS/2026-08-21-fe-reasoning-observability-path-matrix.md


---

# Mock Test: E2E FE Reasoning Observability (closure gate for glm-5.3 non-stream)

### Metadata
- **Created**: 2026-08-21
- **Script**: `test/e2e_fe_reasoning_observability/` (Go in-process test: harness_test.go + e2e_fe_reasoning_observability_test.go)
- **Language**: Go (mirrors `test/e2e_minimax_reasoning/` harness; FE API mounted on real `httptest.Server`)
- **Status**: ACTIVE — closure gate for fix branch `fix/ui-reasoning-observability` @ db7aca0

### Configuration
- **Timeout**: dual-layer — outer `timeout 300 go test ./test/e2e_fe_reasoning_observability/ -v -count=1 -timeout 240s`
- **Ports**: NONE fixed — `httptest.NewServer` ephemeral loopback only (mock upstream + FE API); no port cleanup needed; never touches 8088
- **Cleanup**: Go test process exit reaps httptest servers; `t.Cleanup` for DB/store

### What It Tests
The ORIGINAL user bug end-to-end: glm-5.3 NON-STREAM response carrying
`choices[0].message.reasoning_content` showed in raw server JSON but `GET /fe/api/requests/{id}`
returned ZERO occurrences of "thinking" and the 🧠 UI block never rendered.
The fix (10 commits on fea5874, head db7aca0) populates `store.Message.Thinking` on previously
non-capturing paths. This suite proves: proxy handler → reqStore → ui.Server FE API (real HTTP) →
payload shape the 🧠 block renders.

### Mock Services Required
- Capturing mock upstream (httptest, ephemeral): OpenAI-shaped non-stream responses, with or without `reasoning_content`.
- FE API server (httptest, ephemeral): `ui.NewServer(...)` + `RegisterHandlers` on a `http.ServeMux`, sharing the SAME `*store.RequestStore` the proxy handler was built with.

### Test Scenarios
1. **A — CLOSURE GATE (glm-5.3 non-stream, race-external)**: unregistered model `glm-5.3`, upstream returns non-stream 200 with multi-sentence `reasoning_content` (fixed constant). After client 200: GET list `GET /fe/api/requests` (newest-first, take [0]) → GET detail → assert assistant `thinking` == EXACT string (byte-exact), raw body contains `"thinking"`, and `reqStore.Get(id).Messages` belt-and-braces.
2. **B — request-side capture (DeepSeek-replay shape)**: messages = [user, assistant WITH reasoning_content, user]; upstream returns NO reasoning. Assert FE API: request-side messages[1].thinking == sent value; response-side (last) thinking ABSENT (omitempty).
3. **C — negative cleanliness**: upstream returns NO reasoning; assert FE API detail payload has ZERO occurrences of substring `thinking`; assistant message decodes without a thinking field.
4. **D — payload-shape static check**: read `pkg/ui/frontend/src/components/RequestDetail.tsx` (🧠 block, line ~697) and assert it renders `message.thinking` — matching FE API `messages[i].thinking`. Different field name ⇒ FAIL reported loudly.

### Success Criteria
- [ ] All scenarios PASS with byte-exact thinking equality
- [ ] Suite runtime well under 240s (expected seconds)
- [ ] No process leaks (in-process httptest; reaped on exit)
- [ ] Negative control: Scenario C proves omitempty keeps payload clean when absent

### Implementation Notes
- Model `glm-5.3` is UNREGISTERED ⇒ `initRequestContext` leaves resolvedModel nil ⇒ race-external path (same trick as `raceExtModel` in minimax harness).
- FE API is unauthenticated (no middleware) — direct GET works.
- List endpoint newest-first: `store.List()` reverses insertion order (memory.go:131-142).
- Request bodies OMIT the "stream" field for A/C scenario realism (original bug was non-streaming).

### Last Run
- **Date**: 2026-08-21
- **Session**: Worker (executor session)
- **Result**: PASS — 4/4 scenarios, suite runtime 0.443s (under 240s/300s caps)
- **Quick Fixes**: Scenario D path was package-relative (`../../pkg/ui/...`) — harness bug, fixed (no assertion changed)
- **Report**: RESULTS/2026-08-21-fe-reasoning-observability.md


---

# Mock Test: Peak Hour Fallback

### Metadata
- **Created**: 2026-03-30
- **Script**: `test/mock_llm_peak_hour_fallback.go`
- **Runner**: `test/test_peak_hour_fallback.sh`
- **Language**: Go
- **Status**: ACTIVE — tests verified, code is correct, no bug found

### Configuration
- **Timeout**: 60 seconds
- **Mock LLM Port**: 19001
- **Proxy Port**: 19002
- **Cleanup**: Kill processes on all ports before/after

### What It Tests
- Peak hour model switching + fallback chain behavior
- 4 scenarios covering fallback with/without peak hour

### Test Scenarios

| Test | Primary Model | Fallback Model | Expected Behavior | Result |
|------|--------------|----------------|-------------------|--------|
| A | test-peak (with peak hour, upstream FAILS) | test-fallback-no-peak (NO peak hour) | Fallback succeeds with mock-fallback-normal | ✅ PASS |
| B | test-peak (with peak hour, upstream FAILS) | test-fallback-with-peak (WITH peak hour, upstream succeeds) | Fallback succeeds with mock-fallback-peak-upstream | ✅ PASS |
| C | test-peak (with peak hour, upstream FAILS) | test-fallback-no-peak (upstream also FAILS) | Both fail, error returned | ✅ PASS |
| D | test-normal (NO peak hour, upstream FAILS) | test-fallback-no-peak (NO peak hour) | Fallback succeeds | N/A (Ultimate Model interference) |

### Model Configuration

**Model 1: "test-peak"** (primary with peak hour)
- internal: true, internal_model: "mock-normal-upstream"
- fallback_chain: ["test-fallback-no-peak"]
- peak_hour_enabled: true, 00:00-23:59 +0, peak_hour_model: "mock-peak-upstream"

**Model 2: "test-fallback-no-peak"** (fallback, NO peak hour)
- internal: true, internal_model: "mock-fallback-normal"
- NO peak hour config

**Model 3: "test-fallback-with-peak"** (fallback WITH peak hour)
- internal: true, internal_model: "mock-fallback-normal-upstream"
- peak_hour_enabled: true, 00:00-23:59 +0, peak_hour_model: "mock-fallback-peak-upstream"

**Model 4: "test-normal"** (baseline, NO peak hour)
- internal: true, internal_model: "mock-normal-upstream"
- fallback_chain: ["test-fallback-no-peak"]

### Investigation Outcome
- **Original "bug"**: Caused by misconfigured test — fallback model had peak_hour_enabled=true
- **Code verification**: Peak hour fallback logic is correct
- **Each model evaluates its own peak hour config independently**
- **Debug logging added** at key decision points for future investigation

### Last Run
- **Date**: 2026-03-30
- **Session**: ses_2c141c7dfffekSVLJ0jMSD58CK
- **Result**: Tests A/B/C PASS, Test D affected by Ultimate Model
- **Quick Fixes**: None (no bug in code)
- **Report**: RESULTS/2026-03-30-peak-hour-fallback-investigation-final.md
- **Commit**: a0e00e4


---

# Mock Test: Ultimate Internal Reasoning-Content Reproduction (commit 83814b0 verification)

### Metadata
- **Created**: 2026-08-18
- **Script**: `test/e2e_ultimate_internal_reasoning/e2e_ultimate_internal_reasoning_test.go` (Go test, in-process)
- **Language**: Go (reuses `httptest` mockUpstream pattern from `test/e2e_reasoning_content/`)
- **Status**: ACTIVE — verified against fix 83814b0 on 2026-08-18 (negative-control proven)

### Configuration
- **Timeout**: dual-layer — outer `timeout 300 go test ./test/e2e_ultimate_internal_reasoning/ -v -count=1 -timeout 110s`
- **Ports**: NONE fixed — `httptest.NewServer` ephemeral loopback ports (no collision risk; harness-fixed ports not needed in-process)
- **Cleanup**: Go test process exit reaps httptest servers; `t.Cleanup` for store

### What It Tests
The original bug scenario end-to-end: client chat-completion whose `messages` carry
`reasoning_content` (DeepSeek R1-style replayed assistant reasoning) + ultimate-model
fallback resolving to an INTERNAL model. The mock upstream CAPTURES the request body it
receives; the test asserts the captured upstream body CONTAINS `reasoning_content` in the
expected message slot (the assistant message that carried it, correct position preserved).
Fix under test: `pkg/ultimatemodel/handler_internal.go` convertRequest (commit 83814b0).

### Mock Services Required
- Single OpenAI-shaped mock upstream (httptest): returns HTTP 500 on the hash-trigger
  request, 200 chat-completion on the ultimate-internal replay; captures every request body.

### Test Scenarios
1. **Reproduction (positive)**: messages = [user, assistant w/ reasoning_content, user];
   first request fails 500 (hash recorded); identical resend → proxy rewrites to
   ULTIMATE_MODEL_ID model which is `internal=true` → routes through
   `ultimatemodel` internal handler convertRequest → mock upstream. Assert captured
   upstream body: `messages[1].reasoning_content` == sent value (slot-precise), and
   `content` fields not lost.
2. **Negative control (bug-detection proof)**: temporarily revert ONLY the 4-line fix
   (`git stash` / reverse-patch of handler_internal.go), re-run scenario 1 → test MUST FAIL
   (missing reasoning_content in captured body); restore file to 83814b0 state → test MUST PASS.

### Success Criteria
- [x] Scenario 1 PASS on fixed HEAD (83814b0)
- [x] Scenario 2: test fails with fix reverted (proves detection power), passes after restore
- [x] Captured-body assertion is slot-precise (index-1 assistant message), not "contains anywhere"
- [x] Working tree restored exactly to 83814b0 state afterwards (only new test file committed)

### Implementation Notes
- Fallback trigger mechanics (from recon): duplicate-hash match — send request with
  `mock-error-500` trigger, resend identical body; env `ULTIMATE_MODEL_ID`,
  `ULTIMATE_MODEL_MAX_HASH=100`, `ULTIMATE_MODEL_MAX_RETRIES=2`.
- Ultimate model seeding (in-process store): `internal: true`, `internal_model` set,
  credential + base_url pointing at mock upstream.
- Reuse `testEnv`/`mockUpstream`/`capturedRequests` patterns from `test/e2e_reasoning_content/`.

### Last Run
- **Date**: 2026-08-18T11:52:42Z
- **Worker Instance**: Worker (MiniMax-M3, CONTEXT_KEY f7ddb362-6d23-41a2-93fa-736979c7a8de)
- **Result**: PASS on HEAD (83814b0) / FAIL with reverted fix / PASS after restore
- **Trigger**: `X-Force-Ultimate-Model: true` admin header (pkg/proxy/handler.go:465)
  combined with `ULTIMATE_MODEL_MAX_RETRIES=0` so the ultimate path fires on
  the first call. Hash-duplicate trigger was impractical in-process.
- **Quick Fixes**: env `ULTIMATE_MODEL_MAX_RETRIES` set to `0` so the
  force-trigger fires immediately (default `MaxRetries=2` requires newCount>=2).
- **Report**: RESULTS/2026-08-18-reasoning-content-ultimate-internal.md


---

# Mock Test: MiniMax reasoning_details Shell Harness (P3-4)

### Metadata
- **Created**: 2026-08-19
- **Script**: `test/test_mock_minimax_reasoning.sh` (to be created)
- **Mock server**: `test/mock_llm_minimax_reasoning.go` (Go, per project mock convention — NOT Python)
- **Language**: Go mock + Bash harness
- **Status**: ACTIVE (executed 2026-08-19 — see Last Run)

### Configuration
- **Timeout**: 90s internal (alarm trap) / `timeout 300` outer wrapper
- **Mock Port**: 4005 (harness-fixed, next pair after 4003/4324; verified free)
- **Proxy Port**: 4325 (harness-fixed; verified free)
- **Cleanup**: `source test/test_mock_clean_ports.sh` + `clean_ports 4005 4325`; EXIT trap kills PIDs; **NEVER touch port 8088**

### What It Tests
Real-process E2E of the `x-proxy-interleaved-thinking` translation feature against a capturing
mock MiniMax upstream: request-direction translation (reasoning_content → reasoning_details +
reasoning_split), response-direction translation (non-stream + SSE, incremental AND cumulative),
flag-absent legacy passthrough, header hygiene on external paths, non-MiniMax inertness,
error path, usage passthrough, ultimate-path trigger via duplicate-hash retry.

### Mock Services Required
- Single MiniMax-shaped mock upstream (Go): OpenAI-compatible `/v1/chat/completions`;
  mode selected by marker in last user message (MODE-NS-DETAILS, MODE-NS-BOTH,
  MODE-STREAM-INCREMENTAL, MODE-STREAM-CUMULATIVE, MODE-STREAM-BOTH, MODE-EMPTY-TEXT,
  MODE-MULTI-ENTRY, MODE-PLAIN, MODE-ERROR-500); captures every request (headers + body)
  to JSONL file for shell assertions.

### Credential / Model Setup (admin API)
- Credential: `{"id":"mock-minimax-reasoning-cred","provider":"minimax","api_key":"mock-api-key","base_url":"http://localhost:4005/v1"}`
- Non-MiniMax credential (inertness case): provider `openai`, same shape
- Models: internal=true, `internal_model`, `internal_base_url` → mock; ultimate model via `ULTIMATE_MODEL_ID` + duplicate-hash retry trigger

### Test Scenarios (target ≥ 14 assertions)
1. Boot sanity
2. NS details → client reasoning_content concat, no details leak
3. Captured upstream req: reasoning_details shape + reasoning_content stripped + reasoning_split:true + monotonic ids
4. NS both (details+content) → single winner, exactly once
5. Stream incremental → ordered deltas, valid SSE framing, `data: [DONE]`
6. Stream cumulative → suffix emission, no duplication
7. Stream both → single winner
8. Empty-text entry → no empty delta (H7)
9. Multi-entry chunk → ordered deltas
10. Flag absent + MODE-PLAIN → legacy passthrough (captured upstream un-translated, no reasoning_split)
11. Header hygiene: zero `x-proxy-interleaved-thinking` (any case) in captured upstream headers — race-external AND ultimate-external
12. Non-MiniMax cred + flag ON → no translation
13. MODE-ERROR-500 → clean client error, no reasoning leakage
14. usage passthrough on translated path
15. Ultimate-path duplicate-hash trigger → translated ultimate upstream request

### Success Criteria
- [ ] `bash test/test_mock_minimax_reasoning.sh` exits 0 under `timeout 300`
- [ ] All scenarios PASS with capture-based evidence
- [ ] Ports freed; no process leaks

### Last Run
- **Date**: 2026-08-19
- **Result**: **PASS 53/53** (2026-08-19 final; initial run 47/53 exposed the T3b race-internal product bug — translator writes `[]any{Struct}` but `HydrateReasoningDetails` type-asserted to `map[string]interface{}` → silent empty slice → `omitempty` dropped the wire field. Fixed at hydrate boundary: `068317c`; index-omitempty assertion repaired: `1344380`. Full evidence: `.agents/tester/RESULTS/2026-08-19-minimax-reasoning-details-p3-4.md`)
- **Session**: worker (MiniMax-M3)
- **Quick Fixes**: bash heredoc terminator could not have trailing args — switched helpers to env-var passing; mock mode extractor switched to prefix-match (MODE-* in content) so T3/T3b share `MODE-NS-DETAILS` semantics with distinct content suffixes; assertion patterns updated for Go marshaling space (`"key": value` not `"key":value`)
- **Report**: RESULTS/2026-08-19-minimax-reasoning-details-p3-4.md

---

# Mock Test: MiniMax reasoning_details Go E2E Suite (P3-5)

### Metadata
- **Created**: 2026-08-19
- **Script**: `test/e2e_minimax_reasoning/e2e_minimax_reasoning_test.go` (to be created)
- **Language**: Go, in-process (mirrors `test/e2e_ultimate_internal_reasoning/`)
- **Status**: PLANNED

### Configuration
- **Timeout**: `go test -timeout 240s` internal / `timeout 300` outer
- **Ports**: none fixed — `httptest.NewServer` ephemeral loopback
- **Cleanup**: `t.Cleanup` + httptest auto-reap

### What It Tests
All 4 proxy paths (race-internal, race-external, ultimate-internal, ultimate-external) × request/response × stream/non-stream translation behavior, flag quadrants, header hygiene, credential gate, error path, drift counter.

### Key mechanics (from recon @ d5280ce)
- Ultimate paths: `t.Setenv ULTIMATE_MODEL_ID/MAX_HASH/MAX_RETRIES` + token with `ultimateModelEnabled=true` + `X-Force-Ultimate-Model: true`
- Race paths: plain token, model `Internal` true/false + `credential_id` → minimax cred
- Gate: `interleaved && provider=="minimax"` (case-insensitive) at race_executor.go:173/244/939/1221, ultimamodel handler_internal.go:88, handler_external.go:99/171/289
- Response on internal paths: typed extraction in pkg/providers/openai.go (NOT translator)
- Drift counter: `translator.FormatDriftCount()` — suite must leave delta == 0

### Test Scenarios (14 groups)
S1/S2 request translation (race-ext, ult-ext) · S3 typed internal request paths (+negative nil) ·
S4 NS response external (concat, strip details/audio/name, Q2 both-modes single-winner) ·
S5 NS response internal (typed extraction) · S6 stream external (incremental+cumulative+multi-entry+H7+H2+[DONE]) ·
S7 stream internal (thinking events) · S8 flag-absent all 4 paths (legacy identity) ·
S9 non-MiniMax + flag ON all 4 paths · S10 credential gate (no cred → inert) ·
S11 error 500 (clean error, no leakage) · S12 usage preservation · S13 drift counter delta==0 ·
S14 header-value table at integration level (true/1 activate; True/TRUE/false/no/garbage/absent legacy)

### Success Criteria
- [ ] All scenarios PASS under dual-layer timeout
- [ ] Captured-upstream assertions slot-precise; client-view assertions leak-free
- [ ] Commit `test: ...`

### Last Run
- **Date**: 2026-08-19 · **Worker Instance**: e7e6551c
- **Result**: **PASS 43/43 subtests** (4.7s; suite commit `166aa7f`; S3 id-parity product fix `b2dfde0` landed after the suite caught the divergence — 42/43 → 43/43)
- **Quick Fixes**: S3 ultimate-internal monotonic id counter (fix `b2dfde0`); no test expectations changed (no prior test asserted id scheme)
- **Report**: RESULTS/2026-08-19-minimax-reasoning-details-e2e-gate.md
