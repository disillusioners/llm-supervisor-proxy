# M2 Re-validation + Scenario E/F Extension — Non-Stream Anthropic Wire Parity (gate @ 61fa02a)

**Date**: 2026-08-28
**Script**: `test/mock_rsd_m2_anthropic_ultimate_ui.sh` (extended, **NOT committed** — E failed)
**Branch**: feature/real-streaming-default — task pinned @ 61fa02a; mid-session sibling commit 6259b69 (test-only, `test/e2e_fe_reasoning_observability/`, no pkg/cmd changes → binary behavior identical)
**Binary gate under test**: S3 fix 64da4ae (capture-only non-stream capture in `handleNonStream`)

## RESULT: **FAIL — Scenario E (non-stream wire parity). Scenarios A/B/D re-validated PASS; F (S3 fix records) PASS; C documented-impractical.**

| Scenario | Result | Numbers |
|---|---|---|
| A — default live stream | **PASS** | TTFB=2ms, spread=1533ms, 14 data events, 5 gaps ≥150ms, thinking_delta on wire, text="Hello, world from Anthropic-mock." |
| B — buffered stream | **PASS** | TTFB=1549ms, spread=0ms (single burst), 7 data events, no thinking on wire (pre-feature D8 shape), text identical to A |
| C — ultimate external | **DOCUMENTED-IMPRACTICAL** | unchanged architectural constraint (OpenAI SSE to Anthropic client) |
| D — UI records (streams) | **PASS** | /ui/ 200 (548B HTML), both stream records content+assistant+thinking, usage=null (pre-existing documented gap) |
| **E — non-stream wire parity** | **FAIL** | **live=352B (sha256 a0b93dceb763f4ca…) vs buffered=336B (sha256 fce385e7a43bc840…); first divergence char 3, line 1; STRUCTURAL SPLIT (OpenAI vs Anthropic shape)** |
| F — S3 fix at binary level | **PASS** | both non-stream records: exact sentinel content + thinking, assistant role, is_stream=false, status=completed |

## Scenario E evidence (the failure)

Identical `stream:false` `/v1/messages` requests, deterministic canned upstream (fixed id `chatcmpl-rsd-m2-nonstream`, fixed created 1700000000, fixed content/reasoning strings):

**LIVE (no header) — 352 bytes, OpenAI shape** (written directly by `handleNonStream` → `json.NewEncoder(w).Encode(resp)`):
```json
{"id":"chatcmpl-rsd-m2-nonstream","object":"chat.completion","created":1700000000,"model":"mock-openai","choices":[{"index":0,"message":{"role":"assistant","content":"RSD-M2 non-stream visible answer.","reasoning_content":"RSD-M2 non-stream deliberation."},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":9,"total_tokens":20}}
```

**BUFFERED (`X-LLMProxy-Buffer-Response: true`) — 336 bytes, Anthropic shape** (recorder → `TranslateNonStreamResponse`):
```json
{"content":[{"type":"thinking","content":null,"thinking":"RSD-M2 non-stream deliberation."},{"type":"text","text":"RSD-M2 non-stream visible answer.","content":null}],"id":"msg_uibcNN289K0dSyneIvvzOKDw","model":"mock-anth-model","role":"assistant","stop_reason":"end_turn","type":"message","usage":{"input_tokens":11,"output_tokens":9}}
```

Both: HTTP 200, `Content-Type: application/json`, no SSE framing ✓. Bodies: NOT byte-identical ✗.

## Classification: drift is REAL but NOT fix-induced

Direct A/B probe (same harness mock, isolated HOME, ports 10120/10121) at **pre-fix 1d0c750** (64da4ae^):

| | live wire | buffered wire | live record | buffered record |
|---|---|---|---|---|
| 1d0c750 (pre-fix) | 311B OpenAI shape | 299B Anthropic shape | **EMPTY (S3 bug)** | populated |
| 61fa02a/HEAD (post-fix) | 311B OpenAI shape — **byte-identical to pre-fix** | 299B Anthropic shape | **populated (fix works)** | populated |

1. The across-mode byte difference **pre-dates the S3 fix** — introduced with the phase-3 live branch (e717be3), where `doAnthropicInternalRequest` routes live non-stream through `HandleRequest(..., w, isStream=false)` → `handleNonStream` → raw OpenAI JSON, while buffered keeps `handleAnthropicInternalNonStreamResponse` → `TranslateNonStreamResponse` → Anthropic JSON.
2. **The fix is wire-neutral at the binary level**: live wire bytes identical pre/post fix (same mock bytes in, same wire bytes out).
3. Doc conflict: `docs/real-streaming-default.md` TL;DR row "Non-stream requests: Unaffected — header is a no-op" does NOT hold for the internal-Anthropic path — live vs buffered non-stream emit different wire protocols. Also note `TranslateNonStreamResponse` generates a random `msg_…` id, so even same-shape responses could never be byte-identical across requests.
4. No mock tuning was applied to mask the difference; the failure is reported as-is.

## Scenario F evidence (S3 fix verified at binary level)

Both non-stream records from E (`GET /fe/api/requests`, filtered `is_stream=false`):
```json
{"id":"f852d3a9-486a-43de-baf3-bcdb7a4593de","model":"mock-anth-model","is_stream":false,"status":"completed","last_msg_role":"assistant","content":"RSD-M2 non-stream visible answer.","thinking":"RSD-M2 non-stream deliberation.","usage":null}
{"id":"dc8af9eb-1e26-4611-b24f-29828b4c4bb4","model":"mock-anth-model","is_stream":false,"status":"completed","last_msg_role":"assistant","content":"RSD-M2 non-stream visible answer.","thinking":"RSD-M2 non-stream deliberation.","usage":null}
```
Exact sentinel match for content AND thinking on both records → the S3 contract (post-64da4ae) holds in BOTH modes at the binary level. (Pre-fix, the LIVE record persisted empty strings — proven at 1d0c750.)

## Script changes (uncommitted, per E-fail rule)

- Header doc: E/F added to Verifies
- Mock `/v1/chat/completions` non-stream branch → deterministic canned response (fixed id/created + `reasoning_content` to drive thinking capture)
- Scenario E section (byte-compare, sha256, content-type, SSE-framing guard, structural classification on failure)
- Scenario F section (record filter `is_stream=false`, sentinel exact-match assertions, JSON evidence)
- results.json: scenario_E/F blocks; final gate now A+B+D+E+F hard (C documented-impractical)

## Cleanup

Ports 10120/10121 freed post-run; 8088 untouched; no stray processes; no push/merge; no production changes.

## Options for dispatcher

1. **Adjudicate the E contract**: if live non-stream OpenAI-shape is acceptable-by-design (matching the code comment "deliberate, documented wire-shape change… in live mode"), then E should be re-specified (e.g., per-mode byte-stability + shape documentation) and the docs TL;DR "header is a no-op for non-stream" corrected; script then committed.
2. **Treat as a wire bug**: route live non-stream internal-Anthropic through the translator for Anthropic-shape parity (production change — outside this task's scope).
3. Commit the harness as-is with E expected-FAIL documented (not recommended without a decision — E is a hard gate).
