# Mock Test M1 — real-streaming-default: OpenAI path live-binary TTFB smoke (merge gate)

**Date**: 2026-08-28
**Branch**: `feature/real-streaming-default` @ 22e76d6
**Script**: `test/mock_rsd_m1_openai_ttfb.sh` (created from scratch — prior attempt left no script)
**Mock port**: 10111 (mock OpenAI upstream)
**Proxy port**: 10110 (HEAD binary under test)
**Outer timeout**: `timeout 300`; internal alarm: 240s
**Cleanup**: trap-based; verified ports freed post-run (no leaks)

## Result: **PASS** (A+B+D hard; C informational)

| Scenario | Result | TTFB (ms) | Total (ms) | Spread (ms) | Big gaps | Content match | Notes |
|----------|--------|-----------|------------|-------------|----------|----------------|-------|
| **A** Default (no header) | **PASS** | 102 | 1523 | n/a | 5 | "Hello, world from mock-openai." | Live streaming, incremental (~300ms inter-chunk) |
| **B** `X-LLMProxy-Buffer-Response: true` | **PASS** | 1602 | 1602 | 0 | n/a | identical to A | Buffered, single burst |
| **C** Raw SSE bytes A vs B | **INFO** | — | — | — | — | — | A=1249 bytes, B=1195 bytes (DIFFER); informational only — content equality is the hard assert |
| **D** `/healthz` after both | **PASS** | — | — | — | — | — | /healthz=200 (no panic, no leak) |

## Reproduction summary

Ran twice back-to-back; both runs identical outcome, runtime well under 240s internal cap. Ports 10110 / 10111 verified freed post-cleanup. 8088 not touched.

## Setup details

- **HEAD pinned**: `22e76d6` (re-base thinking-leak S1 to real-streaming-default D8 contract; test-only commit)
- **Build**: `go build -o $TMPDIR/rsd_m1_proxy ./cmd` from project root (default $HOME for module cache; runtime-only $HOME isolation to $TMPDIR/home)
- **Proxy runtime isolation**: `HOME=$TMPDIR/home`, `XDG_CONFIG_HOME=$HOME/.config`; never touches `~/.config/llm-supervisor-proxy`
- **Client model**: UNREGISTERED model name `mock-m1-unregistered-model` in the client request — proxy falls through to race-external path with env-pinned `UPSTREAM_URL` (no auth required since `requiresAuth=false` for external/unregistered models and fresh DB has no `tokenStore`)
- **Mock upstream**: Python `ThreadingHTTPServer` on 127.0.0.1:10111 serving
  - `POST /v1/chat/completions` → real OpenAI SSE wire: 5 content chunks × 300ms inter-chunk delay (≈1.5s total), final chunk with `finish_reason=stop` + `usage`, then `data: [DONE]\n\n`
  - `GET /healthz` → 200 OK (for proxy readiness check)
  - **Self-verification**: mock parses its own prebuilt wire as OpenAI JSON before serving — confirmed 5 content deltas with assembled content `"Hello, world from mock-openai."` matching the expected concatenation

## Why TTFB uses first `data:` line (not first body byte)

The proxy emits an SSE preamble `: connected\n\n` IMMEDIATELY in both modes (`pkg/proxy/handler.go:897`) — this is a "stream open" signal unrelated to user content. Naive TTFB (first body byte) returns ~0ms for both modes and the test cannot distinguish live vs buffered. The probe therefore measures TTFB as the time of the FIRST `data: ` line arrival at the client. This correctly distinguishes:
- Live mode: first `data: ` line arrives ~immediately after connect (~102ms including the preamble flush + first recv of upstream's first chunk)
- Buffered mode: first `data: ` line arrives only at the end of upstream stream (~1602ms, after the proxy has buffered everything)

The proxy wraps the body in HTTP/1.1 chunked transfer encoding (size prefixes `d\r\n` for the 13-byte `: connected\n\n` preamble, then `48d\r\n` for the 1165-byte data section, etc.). The probe treats the entire response body as opaque bytes and locates `data: ` lines via byte position within the body.

## D8 evidence (Scenario A — live mode)

Live mode emits incremental `data: ` lines at ~300ms intervals:

```
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":", "},"finish_reason":null}]}
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":" from "},"finish_reason":null}]}
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{"content":"mock-openai."},"finish_reason":null}]}
data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","model":"mock-openai-m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{...}}
data: [DONE]
```

Content arrivals (ms relative to connect): `[102, 308, 612, 916, 1222, 1525, 1525]`. Inter-chunk gaps: `[206, 304, 304, 306, 303, 0]`. Big gaps (≥150ms): 5. TTFB=102ms, total=1523ms. TTFB < 60% of total ✓ (102 < 913.8).

## D8 evidence (Scenario B — buffered mode)

Buffered mode holds everything until upstream completes, then writes all data in one burst:

```
data: {"...content":"Hello"...}
data: {"...content":", "...}
data: {"...content":"world"...}
data: {"...content":" from "...}
data: {"...content":"mock-openai."...}
data: {"...finish_reason":"stop","usage":{...}}
data: [DONE]
```

All content arrivals at the same TCP packet: `[1601, 1601, 1601, 1601, 1601, 1601, 1601]`. Spread = 0ms (single burst). TTFB=1602ms (waits for upstream's 1.5s + preamble + first chunk boundary). Content assembled: `"Hello, world from mock-openai."` (identical to A).

## Scenario C: raw SSE body bytes differ (informational)

- A body_len=1249 bytes
- B body_len=1195 bytes
- Verdict: DIFFER

The 54-byte size delta is **expected**:
- Buffered mode (B) does NOT emit the proxy's per-second `: keepalive` SSE heartbeats during upstream streaming (because the proxy holds everything until upstream EOF before writing any heartbeat comment; live mode (A) emits multiple heartbeats during the 1.5s upstream stream). The byte difference is the accumulation of A's extra heartbeat comments.
- Both bodies assemble to the IDENTICAL text content — this is the hard assert.

## Cleanup verification

After trap-driven EXIT:
- `lsof -nP -iTCP:10110 -iTCP:10111 -sTCP:LISTEN` → empty (no LISTEN sockets)
- `ps aux | grep -E "rsd_m1_proxy|mock_openai_upstream"` → empty (no leaking processes)
- `~/.config/llm-supervisor-proxy/` → only pre-existing `config.db` (empty, dated May 21) and `models.json` (dated Feb 21); NO files modified by this test run
- 8088 not touched at any point

## Mock wire self-check

The mock server runs a self-check at module import:
```
[mock-openai] self-check OK: 5 content deltas, assembled='Hello, world from mock-openai.'
```

This proves the mock's wire shape (5 SSE `data:` lines with `delta.content` matching the expected concatenation) parses correctly as OpenAI JSON chunks before any test traffic is accepted. If the self-check had failed, the mock would have exited with code 3 and the test would have failed fast at step [3/8].