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

---

## Mock Test: E2E Anthropic Thinking-Leak Spot-Check (byte-identity constraint, Task 4)

### Metadata
- **Created**: 2026-08-21
- **Script**: `test/e2e_anthropic_thinking_leak/` (Go test package: `harness_test.go` + `e2e_anthropic_thinking_leak_test.go`)
- **Language**: Go (in-process handler harness, httptest mock upstream)
- **Status**: IMPLEMENTED

### Configuration
- **Timeout**: dual-layer — outer `timeout 300` wrapping `go test ... -timeout 240s` (inner)
- **Service Port**: N/A (in-process `proxy.Handler.HandleAnthropicMessages` driven directly via `httptest.NewRecorder`; no production port used, 8088 never touched)
- **Mock Ports**: httptest ephemeral only (mock upstream + SQLite temp file)
- **Cleanup**: `t.Cleanup` closes httptest servers; `t.TempDir` auto-removes SQLite DBs; no port residue by construction

### What It Tests
Closes the specific leak the dev-verify pass caught pre-fix on `fix/ui-reasoning-observability` (fix `2e183d3`): **anthropic-format client streaming with thinking events must send ZERO thinking blocks/bytes to the client wire** on the INTERNAL path (thinking-sink invariant at `internal_handler.go:225-242` + sink install at `handler_anthropic.go:482`), while the EXTERNAL path still translates reasoning_content → thinking_delta by design (`TestAnthropic_ThinkingStream` contract).

### Mock Services Required
- Mock OpenAI SSE upstream (httptest): emits `delta.reasoning_content` chunks (multi-chunk reasoning "Hmm, internal deliberation..." split across ≥2 chunks) + content delta + finish chunk + `[DONE]`; non-stream variant returns 200 JSON with `message.reasoning_content`.
- Temp SQLite + migrations (real `database.Store`), real `auth.TokenStore` with created tokens.

### Test Scenarios
1. **S1 INTERNAL stream — the leak spot-check**: registered model `internal:true` (openai-provider credential → mock SSE with reasoning). Anthropic streaming request. Assert (a) wire (recorder body) contains ZERO occurrences of the reasoning substrings, `"thinking_delta"`, `"type":"thinking"`; (b) persisted assistant `Thinking` (in-memory reqStore) non-empty AND equal to concatenated reasoning (both sides of the sink invariant); (c) content intact on wire.
2. **S2 EXTERNAL stream — over-swallow regression guard**: unregistered model → external credential at mock SSE. Anthropic streaming. Assert wire DOES contain `thinking_delta` matching upstream reasoning (by-design translation) — proves the sink did not over-swallow.
3. **S3 INTERNAL non-stream**: same internal model, mock 200 JSON with `message.reasoning_content`. Assert persisted thinking == reasoning value; wire semantics verified against pre-fix base (`fea5874`) differential — the non-stream translator (`response.go`, unchanged since base) may emit an Anthropic thinking block by design; a base-vs-fix differential run classifies any occurrence as pre-existing (byte-identity OK) vs fix-introduced (FAIL).

### Success Criteria
- [ ] S1 zero-leak + captured-thinking (both sides) + content intact
- [ ] S2 thinking_delta present (external translation alive)
- [ ] S3 persisted thinking exact; wire classified via base differential
- [ ] All scenarios pass under dual-layer timeout; runtime ≪ cap
- [ ] Commit `test: e2e anthropic thinking leak spot-check ...`

### Implementation Notes
- Driver calls `h.HandleAnthropicMessages(rr, req)` directly (NOT HandleChatCompletions).
- Request: `POST /v1/messages`, headers `Content-Type: application/json`, `x-api-key: <token>`, `anthropic-version: 2023-06-01`; body model/max_tokens/stream/messages (makeAnthropicRequest shape).
- Auth on the anthropic endpoint is passthrough (no tokenStore check in `HandleAnthropicMessages`); the harness keeps the real tokenStore anyway for parity with T1.

### Last Run
- **Date**: 2026-08-21 · **Session**: Task-4 spot-check (worker instance f7ddb362)
- **Result**: **PASS 3/3 scenarios** (~0.11s; dual-layer timeout 300s/240s; commit on `fix/ui-reasoning-observability`)
- **Non-vacuity**: leak re-injected in isolated worktree → S1 FAILs with 4 detections (leak shape confirmed); S3 wire thinking block classified pre-existing via base differential at `effc345` (identical 371-byte wire; `response.go` unchanged since base `fea5874`)
- **Quick Fixes**: none to production code; harness compile-fix iteration only
- **Report**: RESULTS/2026-08-21-anthropic-thinking-leak-spotcheck.md

---

# Mock Test: real-streaming-default M1 — OpenAI path live-binary TTFB smoke (merge gate)

### Metadata
- **Created**: 2026-08-28
- **Script**: `test/mock_rsd_m1_openai_ttfb.sh` (created from scratch; prior attempt left no script)
- **Language**: Bash + python3 (timestamped chunk reader)
- **Status**: ACTIVE — A+B+D hard PASS, C informational byte-difference noted

### Configuration
- **Timeout**: dual-layer — internal alarm 240s; outer `timeout 300 bash test/mock_rsd_m1_openai_ttfb.sh`
- **Service Port**: 10110 (proxy under test)
- **Mock Ports**: 10111 (mock OpenAI upstream)
- **Cleanup**: kill PIDs on 10110/10111 before start and on EXIT trap; verify ports freed; NEVER touch 8088 or any process not bound to 10110/10111
- **Isolation (HARD)**: proxy config MUST live in a temp dir (temp HOME and/or explicit config path / DATABASE_URL to temp SQLite). MUST NOT read/write the developer's real ~/.config/llm-supervisor-proxy. Follow the config-isolation precedent of test/test_mock_ultimate_model.sh.

### What It Tests
The user's original complaint, against the REAL binary: default = REAL streaming on the OpenAI race path (/v1/chat/completions); `X-LLMProxy-Buffer-Response: true` = buffered (legacy) — first byte reaches client only after upstream completes.

### Mock Services Required
- OpenAI-format SSE upstream on 10111: POST /v1/chat/completions → stream of 5 content chunks, 300ms inter-chunk delay (total ≈1.5s). Chunks MUST be real OpenAI wire: `data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":"chunkN"},"finish_reason":null}]}\n\n`, final chunk with `finish_reason="stop"` + `usage`, then `data: [DONE]\n\n`. Script MUST self-verify mock output parses as OpenAI chunks (JSON fields, SSE framing) before trusting results.

### Test Scenarios
1. DEFAULT (no header), stream:true — assert: TTFB(first `data:` line at client) ≤ 1000ms AND < 60% of total stream duration; ≥3 distinct chunk-arrival timestamps ≥150ms apart (incremental, NOT one burst); assembled content == expected concatenation.
2. WITH `X-LLMProxy-Buffer-Response: true` — assert: TTFB ≥ 1300ms (nothing before upstream completes); all bytes arrive in a single burst (arrival spread ≤ 250ms); assembled content identical to scenario 1.
3. Soft check: raw SSE body bytes scenario1 vs scenario2 — report equal/diff verbatim (content equality is the hard assert; byte equality is informational for the byte-for-byte claim).
4. After both: proxy /healthz still 200 (no panic).

### Why TTFB uses first `data:` line (not first body byte)
The proxy emits an SSE preamble `: connected\n\n` IMMEDIATELY in both modes (`pkg/proxy/handler.go:897`). Naive TTFB (first body byte) returns ~0ms for both modes — the test cannot distinguish live vs buffered. The probe measures TTFB as the time of the FIRST `data: ` line arrival at the client. This correctly distinguishes live (~102ms) vs buffered (~1602ms). The probe handles HTTP/1.1 chunked transfer encoding transparently by treating the body as opaque bytes.

### Success Criteria
- [x] All hard asserts pass (no timing-boundary retry needed; numbers stable across 2 runs)
- [x] No process leaks; ports freed; /healthz 200 at end
- [x] Mock wire-format self-verification included in output (`[mock-openai] self-check OK: 5 content deltas`)

### Implementation Notes
- Client probe: python3 socket, recv() loop with time.time() timestamps per arrival; byte-position-walk attributes each `data:` line to its containing recv() arrival.
- Timing margins assume 5×300ms upstream stream; margins are generous — do not tighten without reason.
- Build binary from HEAD: `go build -o <tmpdir>/rsd_m1_proxy ./cmd` (or repo equivalent).
- UNREGISTERED model name in client request triggers race-external path (no auth required, no credential config needed).
- Result bodies differ between A and B by 54 bytes (live mode emits extra `: keepalive` heartbeats during the 1.5s upstream stream; buffered mode emits none); both bodies assemble to identical text.

### Last Run
- **Date**: 2026-08-28
- **Session**: Worker (executor session)
- **Result**: PASS — A (ttfb=102ms, total=1523ms, big_gaps=5), B (ttfb=1602ms, total=1602ms, spread=0ms), D (healthz=200); stable across 2 runs
- **Quick Fixes**: probe TTFB definition changed from "first body byte" to "first `data:` line" to skip the proxy's `: connected\n\n` preamble — without this fix, both modes show TTFB≈0ms and the test is unable to distinguish them
- **Report**: RESULTS/2026-08-28-real-streaming-default-m1-openai.md

---

# Mock Test: real-streaming-default M2 — Anthropic path + ultimate path + UI records (merge gate)

### Metadata
- **Created**: 2026-08-28
- **Script**: `test/mock_rsd_m2_anthropic_ultimate_ui.sh`
- **Language**: Bash + python3
- **Status**: ACTIVE (merge-gate for feature/real-streaming-default; final gate round @ e60de91)

### Configuration
- **Timeout**: internal alarm 240s; outer `timeout 300`
- **Service Port**: 10120 (proxy)
- **Mock Ports**: 10121 (mock Anthropic upstream)
- **Cleanup / Isolation**: same rules as M1 (trap-kill 10120/10121 only; temp-dir config; never 8088)

### What It Tests
Real streaming default on /v1/messages (Anthropic client path) and on the ultimate external path; UI request records fed by capture taps are mode-independent.

### Mock Services Required
- Anthropic-format SSE upstream on 10121: POST /v1/messages → proper event sequence: `message_start`, `content_block_start`(thinking), `content_block_delta`(thinking_delta), `content_block_stop`, `content_block_start`(text), 5× `content_block_delta`(text_delta) with 300ms delays, `content_block_stop`, `message_delta`(usage), `message_stop`. Real wire framing (`event: X\ndata: {...}\n\n`). Self-verify mock parses as Anthropic events before trusting results.
- Same mock also serves stream=false with a deterministic canned JSON response (for M3 reuse and any non-stream sanity).

### Test Scenarios
1. /v1/messages DEFAULT (no header) — TTFB ≤ 1000ms, incremental (≥3 arrivals ≥150ms apart), thinking_delta present on the client wire (live mode, D8 shape), assembled text correct.
2. /v1/messages WITH buffer header — TTFB ≥ 1300ms, single burst (≤250ms spread), assembled content identical to scenario 1.
3. Ultimate external path: request with `X-Force-Ultimate-Model: 1` + token with ultimate permission, ultimate model = external type routed to mock 10121. DEFAULT: incremental (TTFB ≤ 1000ms). WITH buffer header: buffered. If not practical (config blockers), document exactly why + what was verified instead.
4. UI records: GET /ui/ → 200 + HTML. One live-mode request + one buffered-mode request, then GET /fe/api/requests → BOTH records present, each with content and usage fields populated (mode-independent capture). Report record JSON snippets as evidence.
5. (E, HARD since e60de91) NON-STREAM /v1/messages wire parity: identical `stream:false` requests, one live (no header) vs one buffered (`X-LLMProxy-Buffer-Response: true`). Hard asserts: BOTH bodies Anthropic-shape (`"type":"message"` present, `"object":"chat.completion"` ABSENT — negative OpenAI-shape guard) and byte-identity MODULO the proxy-random `id` (normalize `"id":"msg_[^"]*"` to a constant in both bodies, then require EXACT byte equality). Reports raw lengths + post-normalization sha256. Drives the overall gate.
6. (F, HARD since 64da4ae) NON-STREAM UI records: both E-pair records carry non-empty assistant content AND non-empty thinking (S3 fix contract).

### Success Criteria
- [ ] Scenarios 1,2,4,5,6 hard-pass (A+B+D+E+F drive the overall gate); scenario 3 pass or documented-impractical with evidence
- [ ] No leaks; /healthz 200 at end; both attempts reported if timing retry used

### Implementation Notes
- Anthropic client requests need x-api-key + anthropic-version headers (project precedent).
- Ultimate: X-Force-Ultimate-Model fires immediately (no retry-counter env needed post trigger-schedule change); requires token ultimate permission.

### Last Run (final gate round @ e60de91 — E restored to HARD GATE)
- **Date**: 2026-08-28
- **Worker Instance**: rsd-mock-m2-anthropic (E advisory→hard conversion + final gate re-run)
- **Result**: **PASS (A+B+D+E+F hard; C documented-impractical)** — A PASS (TTFB=1ms, spread=1540ms, 5 big gaps, thinking_delta on wire); B PASS (TTFB=1551ms, spread=1ms, buffered shape); C documented-impractical (architectural: external ultimate hardcodes OpenAI wire for Anthropic clients); D PASS (2/2 records content+assistant, usage=null known gap); **E PASS as HARD GATE (shape split fixed by e60de91): live=336B anthropic-shape, buffered=336B anthropic-shape; negative OpenAI-shape guard clean (`"object":"chat.completion"` count=0 both sides, `"type":"message"` count=1 both sides); after normalizing `"id":"msg_[^"]*"` to a constant both bodies are 322B with IDENTICAL sha256 `47644debe104c3b5d4692ac780da9e39b31cbb66b09ea07ba284b2fbd5702080` — EXACT byte equality modulo the proxy-random id (deterministic: same normalized sha in two consecutive runs)**; F PASS (both non-stream records exact sentinel content+thinking — S3 contract). E re-enters the overall gate: `A && B && D && E && F`. /healthz 200 at end; ports 10120/10121 freed.
- **Quick Fixes**: E_SHAPE_NOTE classifier embedded the proxy-random id inside the compared strings → false "STRUCTURAL SPLIT" printed even when both bodies were Anthropic-shape; fixed to compare (shape, keys) only and print the id separately as evidence (harness-only reporter fix — no assertion weakened or changed).
- **Report**: RESULTS/2026-08-28-rsd-m2-e-hard-gate-final.md

### Previous Run (advisory era @ 61fa02a + S3 fix 64da4ae)
- **Date**: 2026-08-28 (gate re-run; extended with E/F; E → ADVISORY while wire-shape divergence under adjudication)
- **Worker Instance**: rsd-mock-m2-anthropic (M2 re-validation + E/F extension + E→ADVISORY conversion)
- **Result**: **PASS (A+B+D+F hard; E ADVISORY pending adjudication; C documented-impractical)** — A PASS (TTFB=2ms, 5 big gaps, thinking_delta on wire); B PASS (TTFB=1549ms, spread=0, pre-feature shape); C documented-impractical; D PASS (both stream records content+thinking, usage=null known gap); **E ADVISORY (does NOT drive overall pass/fail): non-stream live=352B OpenAI-shape vs buffered=336B Anthropic-shape — NOT byte-identical (structural split)**; F PASS (both non-stream records exact sentinel content+thinking — S3 fix verified at binary level). Drift classified NOT fix-induced (identical split at pre-fix 1d0c750; live wire bytes identical pre/post fix) — phase-3-era live-branch behavior; conflicts with docs TL;DR "non-stream: header is a no-op". **Per the no-red-harness rule (E is under adjudication, NOT fix-induced), E was converted from hard-fail to ADVISORY in test/mock_rsd_m2_anthropic_ultimate_ui.sh** — it now computes and reports per-mode byte lengths + sha256, shape classification of each body (OpenAI vs Anthropic), and first divergence offset, but does NOT drive the overall gate. Production code (cmd/, pkg/) is FROZEN. Script + RESULTS committed.
- **Quick Fixes**: F_CHECK python quoting (f-string backslash-escape → %-format) — applied and re-run green. E→ADVISORY conversion — applied and re-run green (OVERALL PASS, A/B/D/F hard).
- **Report**: RESULTS/2026-08-28-rsd-m2-ef-nonstream-parity-gate.md (E-fail evidence, captured before advisory conversion)

### Previous Run (pre-extension baseline @ 03a5339)
- **Date**: 2026-08-28
- **Worker Instance**: rsd-mock-m2-anthropic (e2e97da3)
- **Result**: **PASS** — A (default): TTFB=2ms, spread=1520ms, 5 big gaps, thinking_delta on wire (D8 live shape); B (buffered): TTFB=1524ms, spread=0ms, no thinking blocks (pre-feature shape); C (ultimate external): documented-impractical (OpenAI-wire mismatch vs Anthropic client; partial evidence: force-header accepted, mock received POST, 1897B OpenAI SSE); D (UI records): /ui/ 200, both records content+thinking, status=completed, usage=null (pre-existing Anthropic-path gap — proven pre-existing via buffered-mode parity). /healthz 200 end; ports freed.
- **Quick Fixes**: none (script authored fresh, 994 lines)
- **Report**: RESULTS/2026-08-28-real-streaming-default-m2-anthropic.md

---

# Mock Test: real-streaming-default M3 — header truth table + edge cases (merge gate)

### Metadata
- **Created**: 2026-08-28
- **Script**: `test/mock_rsd_m3_edge_cases.sh`
- **Language**: Bash + python3
- **Status**: PLANNED (merge-gate for feature/real-streaming-default @ 03a5339)

### Configuration
- **Timeout**: internal alarm 240s; outer `timeout 300`
- **Service Port**: 10130 (proxy)
- **Mock Ports**: 10131 (mock upstream, OpenAI wire; 4 chunks × 250ms for speed)
- **Cleanup / Isolation**: same rules as M1 (trap-kill 10130/10131 only; temp-dir config; never 8088)

### What It Tests
Documented `X-LLMProxy-Buffer-Response` truth-table conformance on the live binary, multi-value first-wins, stream=false neutrality, client-disconnect safety.

### Test Scenarios
1. Truth table: for EACH documented header variant (expected values: TRUE/1/yes/on/empty/false/0/no/off/garbage + ABSENT — exact expected mode per docs/real-streaming-default.md, embedded in dispatch): one streaming request; classify LIVE (TTFB ≤ ~800ms, incremental) vs BUFFERED (TTFB ≥ ~1.0s with 4×250ms mock, single burst); assert classification == documented expectation.
2. Multi-value: header sent twice — (`true` then `false`) ⇒ buffered; (`false` then `true`) ⇒ live (documented first-wins).
3. stream=false: with header vs without — responses must be identical (deterministic canned mock response; compare full bodies); content-type JSON, no SSE framing in either.
4. Client disconnect mid-stream (live mode): read 1 chunk then hard-close socket; wait 2s; assert proxy still alive (/healthz 200), no panic/SIGSEGV in captured proxy stderr (the 9842c77 class).

### Success Criteria
- [ ] Every truth-table row matches the documented expectation (any mismatch = FAIL with evidence)
- [ ] First-wins both directions; stream=false identical; disconnect leaves healthy proxy
- [ ] No leaks; ports freed

### Implementation Notes
- Timing classifier margins: 4×250ms ⇒ live TTFB ≤ 800ms; buffered TTFB ≥ 1000ms. One retry per row on boundary only, both attempts reported.
- Truth table to be embedded verbatim from docs (via W1 extraction) in the dispatch message — assert against the DOC, not assumptions.
- AFTER-RUN FINDING (refines classifier): the absolute TTFB is too sensitive to local-loopback latency
  (proxy→mock on 127.0.0.1 adds variable latency). The reliable MODE signal is the streaming PATTERN:
  big_gaps ≥ 2 ⇒ LIVE; big_gaps == 0 + spread ≤ 250ms ⇒ BUFFERED. Implementation classifies on pattern,
  with TTFB recorded as a sanity hint only. Buffered rows observed at TTFB ≈ 800ms (not 1000ms) on
  this machine — the local mock-to-proxy loop is fast; docs line 42-48 truth table is what we assert.

### Last Run
- **Date**: 2026-08-28
- **Session**: Worker (executor session)
- **Branch/HEAD**: `feature/real-streaming-default` @ `22e76d6` (dispatch said `03a5339`; HEAD advanced to `22e76d6`
  before run due to a parallel test rebase — same branch, M3 assertions hold)
- **Result**: PASS — 12/12 truth-table rows + 3/3 multi-value + 3/3 stream=false identity + 3/3 disconnect safety
- **Runtime**: ≈50s (well under 240s internal alarm)
- **Quick Fixes** (script-only, no production changes):
  1. `UPSTREAM_URL` must NOT include `/v1` — the proxy appends `/v1/chat/completions` for external models, so
     `http://host:port/v1` becomes `http://host:port/v1/v1/chat/completions` (404). Fix: `UPSTREAM_URL="http://host:port"`.
  2. Initial classifier used absolute TTFB (LIVE ≤ 800ms, BUFFERED ≥ 1000ms) which is brittle on local
     loopback — observed buffered rows at TTFB ≈ 800ms. Switched to pattern-based: big_gaps ≥ 2 ⇒ LIVE;
     big_gaps == 0 + spread ≤ 250ms ⇒ BUFFERED. Both absolute TTFB and pattern are reported.
  3. SO_LINGER for hard-close on macOS: `struct.pack('ii', 1, 0)` (l_onoff=1, l_linger=0); the raw
     `(1).to_bytes(2, 'little') + (0).to_bytes(2, 'little')` form (4-byte total) raises OSError [Errno 22]
     on macOS — `struct linger` is 8 bytes (int+int). Included an explicit byte fallback for safety.
  4. Scenario 3 initially mixed two body-capture mechanisms (PROBE_PY jsonl vs raw socket write); replaced
     with a single `probe_nonstream_raw` so both 3a (no header) and 3b (with header) use the same raw-body
     capture path — this fixed the byte-count mismatch (was 351 vs 285; now both 285).
- **Cleanup verified**: ports 10130/10131 freed; proxy + mock processes killed; alarm subprocess reaped; no leaks.
- **Report**: RESULTS/2026-08-28-real-streaming-default-m3-edge-cases.md

