# Mock Test M3 — Real-Streaming-Default: Header Truth Table + Edge Cases

**Date**: 2026-08-28
**Branch/HEAD at run**: `feature/real-streaming-default` @ `22e76d6` (dispatch referenced `03a5339`, but HEAD advanced
to `22e76d6` between dispatch and run due to a parallel test rebase — see `git log`:
`22e76d6 test(e2e): re-base thinking-leak S1 to real-streaming-default D8 contract`).
Both SHAs sit on the same `feature/real-streaming-default` branch; the M3 assertions hold against `22e76d6`.
**Script**: `test/mock_rsd_m3_edge_cases.sh`
**Ports**: 10130 (proxy) + 10131 (mock upstream)
**Outer guard**: `timeout 300`; **Internal alarm**: 240s
**Cleanup**: trap + port-free verification, never touches 8088 or other workers' ports (10110/10111/10120/10121)
**Result**: **PASS** — all 12 truth-table rows + 3 multi-value + 3 stream=false identity + 3 disconnect-safety checks pass

---

## Summary

| Scenario | Result | Notes |
|---|---|---|
| 1. Truth table (12 rows) | **PASS 12/12** | All rows classified as documented |
| 2. Multi-value (3 rows)  | **PASS 3/3**  | 2a first-wins true\|false → BUFFERED; 2b first-wins false\|true → LIVE; 2c comma-joined → LIVE (per docs caveat) |
| 3. stream=false identity (3 checks) | **PASS 3/3** | byte-identical bodies (285 B each); valid JSON in both; no SSE framing |
| 4. Client disconnect safety (3 checks) | **PASS 3/3** | /healthz=200, no panic/SIGSEGV, proxy PID alive after 2s grace |
| **OVERALL** | **PASS** | 21/21 |

**Runtime**: ≈50s (well under 240s internal alarm)
**Cleanup verified**: ports 10130/10131 freed, proxy + mock processes killed, alarm subprocess reaped, no leaks

---

## Mock Upstream

A Python `ThreadingHTTPServer` on 127.0.0.1:10131 serves:
- `POST /v1/chat/completions` (stream=true) → real OpenAI SSE: role chunk + 4 content chunks (`alpha`, `beta`, `gamma`, `delta`) × 250ms inter-chunk delay + final stop chunk + `[DONE]`. Total ≈1s, 6 data: events.
- `POST /v1/chat/completions` (stream=false) → deterministic canned JSON response (same body in both header/no-header comparison).

Self-verify: 6 data lines each parse as valid OpenAI `chat.completion.chunk` JSON; ends with `[DONE]`.

---

## Scenario 1 — Truth Table (12 rows, all PASS)

DOCUMENTED TRUTH TABLE (`docs/real-streaming-default.md` lines 42-48):
- ABSENT → LIVE
- PRESENT + empty (bare) → BUFFERED
- PRESENT + truthy (`true`/`1`/`yes`/`on` case-insensitive) → BUFFERED
- PRESENT + falsy (`false`/`0`/`no`/`off` case-insensitive) → LIVE
- PRESENT + any other non-empty value → LIVE

Classification (refined after first iteration):
- **LIVE**: `big_gaps ≥ 2` (≥2 inter-chunk gaps ≥ 150ms = incremental streaming)
- **BUFFERED**: `big_gaps == 0` AND `spread ≤ 250ms` (single burst)
- **INDETERMINATE**: edge case (would trigger 1 retry per row on boundary only)

Results table (all 12/12 PASS):

| # | Label | Header Value | Expected | TTFB (ms) | Spread (ms) | n_data | big_gaps | Classified | Result |
|---|---|---|---|---:|---:|---:|---:|---|---|
| 1 | absent | (no header) | LIVE | 102 | 664 | 7 | 3 | LIVE | PASS |
| 2 | bare | `X-LLMProxy-Buffer-Response` (empty) | BUFFERED | 802 | 0 | 7 | 0 | BUFFERED | PASS |
| 3 | true-lower | `true` | BUFFERED | 802 | 0 | 7 | 0 | BUFFERED | PASS |
| 4 | TRUE-upper | `TRUE` (case test) | BUFFERED | 802 | 0 | 7 | 0 | BUFFERED | PASS |
| 5 | one | `1` | BUFFERED | 802 | 0 | 7 | 0 | BUFFERED | PASS |
| 6 | yes | `yes` | BUFFERED | 802 | 0 | 7 | 0 | BUFFERED | PASS |
| 7 | on | `on` | BUFFERED | 802 | 1 | 7 | 0 | BUFFERED | PASS |
| 8 | false-lower | `false` | LIVE | 102 | 663 | 7 | 3 | LIVE | PASS |
| 9 | zero | `0` | LIVE | 102 | 665 | 7 | 3 | LIVE | PASS |
| 10 | no | `no` | LIVE | 102 | 662 | 7 | 3 | LIVE | PASS |
| 11 | off | `off` | LIVE | 102 | 664 | 7 | 3 | LIVE | PASS |
| 12 | garbage-banana | `banana` (unknown) | LIVE | 101 | 662 | 7 | 3 | LIVE | PASS |

**Truth table conformance: 12/12 PASS.**

Note on TTFB for buffered rows: absolute TTFB ≈ 802ms (not 1000ms as initially expected). The local
mock-to-proxy loopback latency is fast enough that the buffered TTFB sits below the 1.0s threshold.
The pattern-based classifier (big_gaps == 0 → BUFFERED) is the reliable signal here. Both metrics
are recorded for diagnostics.

---

## Scenario 2 — Multi-value Header (3 rows, all PASS)

DOCUMENTED MULTI-VALUE BEHAVIOR (`docs/real-streaming-default.md` lines 55-66):
- Multiple separate `X-LLMProxy-Buffer-Response:` lines → FIRST value wins
- Single comma-joined header line → Go canonical-form parser collapses to ONE value `["a, b"]` → "a, b" not in truth table → LIVE (caveat)

| # | Form | Sent As | Expected | Classified | TTFB (ms) | Spread (ms) | big_gaps | Result |
|---|---|---|---|---|---:|---:|---:|---|
| 2a | First-wins: true THEN false | Two separate header lines | BUFFERED | BUFFERED | 802 | 0 | 0 | PASS |
| 2b | First-wins: false THEN true | Two separate header lines | LIVE | LIVE | 102 | 669 | 3 | PASS |
| 2c | Comma-joined caveat (informational) | One line `X-LLMProxy-Buffer-Response: true, false` | LIVE | LIVE | 103 | 672 | (incremental) | PASS |

**Multi-value conformance: 3/3 PASS.**

---

## Scenario 3 — stream=false Identity (3 checks, all PASS)

The `X-LLMProxy-Buffer-Response` header is documented as a no-op for non-streaming requests. Verified
that with-header and without-header produce byte-identical bodies.

| Check | Expected | Observed |
|---|---|---|
| Body byte count (no header) | 285 | 285 |
| Body byte count (with `true`) | 285 | 285 |
| Bodies byte-equal after trim | equal | **EQUAL** |
| Both bodies parse as JSON | OK | OK / OK |
| No `data: ` SSE framing in either | absent | absent in both |

**stream=false identity: 3/3 PASS.**

---

## Scenario 4 — Client Disconnect Mid-Stream (3 checks, all PASS)

`9842c77` regression class: client reads 1 data: chunk, then hard-closes the socket (SO_LINGER l_onoff=1, l_linger=0 → TCP RST).
2-second grace period. Verify proxy survives.

| Check | Expected | Observed |
|---|---|---|
| Proxy `/healthz` 200 after disconnect | yes | **200** |
| No panic/SIGSEGV/fatal in proxy stderr tail (last 50 lines) | 0 | **0** |
| Proxy PID alive after disconnect | yes | **yes** (kill -0 succeeds) |

Disconnection details: data_count=1, t_first_data=103ms, t_close=163ms (RST sent 60ms after first chunk).
Proxy log shows clean handling: `[STREAM] Client write failed for request ...: client disconnected (context cancelled by HTTP server)` followed by `[RACE] Request 0 failed: context canceled` — no crash.

**Disconnect safety: 3/3 PASS.**

---

## What Was Different from Dispatch Notes

The dispatch message described two minor modeling differences, both addressed:

1. **`UPSTREAM_URL` must NOT include `/v1`** — the proxy appends `/v1/chat/completions` to UPSTREAM_URL
   for external models. Setting `UPSTREAM_URL="http://host:port/v1"` causes the proxy to call
   `http://host:port/v1/v1/chat/completions` (404). Fix: `UPSTREAM_URL="http://host:port"`. The
   `Credential.base_url` separately carries `/v1` for the OpenAI client SDK.

2. **Initial classifier was TTFB-only** — local-loopback latency makes absolute TTFB ≤ 800 / ≥ 1000
   brittle. Switched to pattern-based: `big_gaps >= 2 ⇒ LIVE`; `big_gaps == 0 + spread <= 250ms ⇒ BUFFERED`.
   Both TTFB and streaming-pattern metrics are recorded for diagnostics.

---

## Quick Fixes (script-only — no production code modified)

| # | Where | Fix |
|---|---|---|
| 1 | `mock_rsd_m3_edge_cases.sh` line ~262 | `UPSTREAM_URL` without `/v1` |
| 2 | `classify_stream()` | Pattern-based classification instead of absolute TTFB |
| 3 | `probe_disconnect_after_first()` | `struct.pack('ii', 1, 0)` for SO_LINGER (8 bytes) |
| 4 | Scenario 3 implementation | Replaced mixed-probe with unified `probe_nonstream_raw()` |

---

## Cleanup Verification

After script exit (trap-killed processes):

- `lsof -ti:10130` → empty
- `lsof -ti:10131` → empty
- `lsof -ti:8088` → empty (never touched)
- `lsof -ti:{10110,10111,10120,10121}` → empty (other workers' ports untouched)
- `pgrep -fl "rsd_m3_proxy|mock_openai_m3"` → none

---

## Files

- Script: `test/mock_rsd_m3_edge_cases.sh` (1055 lines, executable)
- Spec + Last Run: `.agents/tester/MOCK_TESTS.md` (M3 section)
- Truth table source: `docs/real-streaming-default.md` lines 42-48 (multi-value: 55-66)
- Proxy header parser (single source of truth): `pkg/proxy/handler_functions.go:bufferModeFor`
