# M2 Final Gate Round @ e60de91 — Scenario E Restored to HARD GATE

**Date**: 2026-08-28
**Worker Instance**: rsd-mock-m2-anthropic
**Script**: `test/mock_rsd_m2_anthropic_ultimate_ui.sh`
**Branch**: `feature/real-streaming-default` @ `e60de91` (fix: "route live non-stream internal-Anthropic through TranslateNonStreamResponse for wire parity")
**Verdict**: **OVERALL PASS — RESULT: PASS (exit 0)** — gate condition now `A && B && D && E && F` (E re-entered).

## What Changed (harness-only; production FROZEN, no production edits)

Scenario E converted from ADVISORY back to a HARD gate, per the e60de91 shape-split fix:

1. **Shape hard-assert (both sides)**: positive Anthropic marker `"type":"message"` count ≥ 1; negative OpenAI-shape guard `"object":"chat.completion"` count == 0.
2. **Byte-identity modulo the proxy-random id**: normalize `"id":"msg_[^"]*"` → `"id":"MSG_NORMALIZED"` in both bodies (`sed -E`), then require EXACT byte equality (`cmp` + sha256 cross-check). Raw lengths + post-normalization sha256 reported. On failure the diff is printed verbatim (no mock tuning to mask).
3. **Sanity checks hardened**: HTTP 200, JSON content-type, no SSE framing — now gate-driving (previously advisory).
4. **E re-enters the OVERALL gate**: `A && B && D && E && F`.
5. `results.json` scenario_E block extended: per-side `*_shape`, `*_sha256_raw`, `*_sha256_id_normalized`, `id_normalized_byte_identical` boolean, `gate: "HARD (restored @ e60de91)"`.

Rationale: the id must be normalized because the proxy mints `msg_<24 random base62>` per response (`pkg/proxy/translator/response.go` `generateAnthropicMessageID`) — raw bodies can never be byte-equal across two requests.

## Per-Scenario Results (final run)

| Scenario | Result | Evidence |
|---|---|---|
| A — /v1/messages DEFAULT | PASS | TTFB=1ms, spread=1540ms, 14 data events, 5 gaps ≥150ms, thinking_delta on client wire, assembled text "Hello, world from Anthropic-mock." |
| B — /v1/messages + buffer header | PASS | TTFB=1551ms, spread=1ms (single burst), no thinking_delta (expected — buffered drops thinking per D8), same assembled text |
| C — ultimate external | DOCUMENTED-IMPRACTICAL | Architectural: `handler_anthropic.go:84` hardcodes `isAnthropicUpstream=false` for external models; ultimate-external answers on OpenAI wire that an Anthropic client cannot decode. Ultimate resolution + mock receipt verified instead (1897B OpenAI SSE). |
| D — UI records | PASS | /ui/ 200 (HTML), 2/2 stream records with assistant content + thinking; usage=null (pre-existing known gap, `finalizeAnthropicSuccess` doesn't set `reqLog.Usage`) |
| **E — non-stream wire parity** | **PASS (HARD GATE)** | see numbers below |
| F — non-stream records (S3) | PASS | 2/2 non-stream records: exact sentinel content "RSD-M2 non-stream visible answer." + thinking "RSD-M2 non-stream deliberation." |

## Scenario E — The Numbers (final run)

```
live:     HTTP=200  raw 336 B  ct=application/json  sha256(raw)=b273a2614115d079…
buffered: HTTP=200  raw 336 B  ct=application/json  sha256(raw)=c778ba4b89fa7bcc…

id-normalized ("id":"msg_[^"]*" → "id":"MSG_NORMALIZED"):
  live:      322 B  sha256=47644debe104c3b5d4692ac780da9e39b31cbb66b09ea07ba284b2fbd5702080
  buffered:  322 B  sha256=47644debe104c3b5d4692ac780da9e39b31cbb66b09ea07ba284b2fbd5702080
  → EXACT BYTE EQUALITY (cmp clean, sha match); only the proxy-random msg_* id differs

shape classification (both sides):
  live:      "type":"message"=1 (Anthropic)  "object":"chat.completion"=0 (negative guard CLEAN)
  buffered:  "type":"message"=1 (Anthropic)  "object":"chat.completion"=0 (negative guard CLEAN)
  JSON keys (identical both sides): [content, id, model, role, stop_reason, type, usage]
  ids observed: msg_wbJTXNWxynEVjACZFPtlOD3B (live) vs msg_zZtk8NSbPmX4eq99I6uGsIkM (buffered)
```

Determinism cross-check: an earlier run in the same session produced the **same** normalized sha256 (`47644deb…`) — the non-stream wire bytes are fully deterministic modulo the id.

Advisory-era contrast (@ 61fa02a, pre-fix): live=352B **OpenAI-shape** vs buffered=336B Anthropic-shape — the structural split that e60de91 eliminated (evidence: `RESULTS/2026-08-28-rsd-m2-ef-nonstream-parity-gate.md`).

## Quick Fix Applied (harness-only, reporter)

First hard-gate run exposed a stale advisory-era reporter artifact: `E_SHAPE_NOTE`'s classifier embedded the raw id inside the compared strings (`ANTHROPIC shape (id='msg_…', keys=…)`), so "STRUCTURAL SPLIT" printed even when both bodies were Anthropic-shape. Fixed to compare (shape, keys) only and print the id separately. No assertion was weakened or changed; re-run green.

## Isolation & Cleanup

- Ports 10120 (proxy) / 10121 (mock) exclusively; pre-run check found them free (stale-listener reap was a no-op); 8088 never touched.
- Isolated `HOME`/`XDG_CONFIG_HOME` under mktemp dir (never `~/.config/llm-supervisor-proxy`).
- Internal 240s alarm + outer `timeout 300` preserved; run completed well under cap (~2 min incl. build).
- Post-run: `/healthz` 200 at step 9/9; `lsof` confirms 10120/10121 free; no process leaks; temp dir removed by trap.
- Exit code 0; no commits made (working tree left dirty for the final docs sweep).
