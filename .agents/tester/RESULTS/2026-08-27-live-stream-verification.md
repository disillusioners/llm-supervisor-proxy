# Test Report: LIVE Stream + Non-Stream Verification — OpenAI path (user question)

Date: 2026-08-27 (UTC)
Target: user's running proxy @ localhost:4123 (untouched; listener PID 81144 cleaned; server 200 at close). Model `coding` (MiniMax-M3, 2-cred LB). Worker: 23c0ef26. Budget: 5/6 requests, max_tokens ≤ 8.

## Verdict: ✅ YES — OpenAI path works for BOTH stream and non-stream, live, under LB

| # | Item | Result | Evidence |
|---|---|---|---|
| 1 | Non-stream (`stream:false`) | ✅ PASS | 200, `Content-Type: application/json`, `object:"chat.completion"`, content present (finish=length @ 8 tokens). Served by minimax-company (conv 72e3a6fc) |
| 2 | Stream (`stream:true`, raw `curl -N`) | ✅ PASS | 200, `text/event-stream` + `X-Accel-Buffering: no`, **4 incremental `data:` chunks + `data: [DONE]`**; assembled deltas = `<think>The user has sent what appears to\n\n` (coherent, progressive — monotonic upstream chunk IDs µs apart). Served by minimax-mindspath (conv 3c829107) — also proves both credentials serve the STREAM path |
| 3 | Affinity across stream/non-stream mix | ✅ PASS | Fresh conv `streamcheck-mix-conv-c53d`: non-stream first → **exactly 1** `model_credential_selected` (→ minimax-company); stream second with full history → **0 new events** (binding reused). Sticky binding is request-shape-independent (conversation key = f(first user message) only) |
| 4 | Credential attribution | ✅ | 3 selection events for 3 fresh keys (company ×2, mindspath ×1); 2 reuses silent — attribution table in worker report |
| 5 | Failover | ✅ 0 events | No natural 429 (failover remains mock-verified; mid-stream reselection impossible by design — not triggered, not applicable) |

Verbatim first/middle/[DONE] chunks, both assembled texts, the single mix-conversation event, and the full attribution table are in the worker report; raw log `/tmp/live_stream_events.log` (272 lines).

## Notes
- Stream responses had PROPERLY FORMED closing `</think>` tags; the earlier `>/think>` mangling is response-by-response upstream variance, not stream-mode-specific (correcting the prior matrix's attribution).
- `model` field in responses echoes upstream `MiniMax-M3`, not the proxy name `coding` (expected forwarding behavior; noted for clients distinguishing names).
- Bonus validation: an accidental duplicate non-stream request (same first-message) silently reused the binding — second live confirmation of the C2 no-republish invariant.
- Server: untouched, alive (200) at close; 0 stray processes.

## Code Changes: NONE
## Documentation Updated
- [x] RESULTS/2026-08-27-live-stream-verification.md (this file)
- [x] README.md history row (appended)
