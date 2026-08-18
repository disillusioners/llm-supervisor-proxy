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
