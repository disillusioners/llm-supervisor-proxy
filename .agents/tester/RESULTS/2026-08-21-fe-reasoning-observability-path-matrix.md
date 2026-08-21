# E2E FE Reasoning Observability — PATH MATRIX Results (Task 2)

**Date**: 2026-08-21
**Suite**: `test/e2e_fe_reasoning_observability/` (extended with `path_matrix_test.go`)
**Branch**: `fix/ui-reasoning-observability` (after commits 2a3cf7e/355f06c)
**Command**: `timeout 300 go test ./test/e2e_fe_reasoning_observability/ -v -count=1 -timeout 240s`

## RESULT: **PASS** — 16/16 matrix rows + 4/4 closure-gate scenarios, runtime **1.764s** (dual-layer caps 240s/300s)

## Per-row table

| Row | Path | Stream | Result | Evidence |
|-----|------|--------|--------|----------|
| R1 | race-external | non-stream | ✅ PASS | id=bde57655… thinking == expected (128 bytes, byte-exact) |
| R2 | race-external | stream | ✅ PASS | id=66a866b5… thinking == expected (128 bytes, byte-exact; 2 SSE deltas accumulated) |
| R3 | race-internal | non-stream | ✅ PASS | id=50e2fee6… thinking == expected (128 bytes, byte-exact) |
| R4 | race-internal | stream | ✅ PASS | id=15c308bb… thinking == expected (128 bytes, byte-exact; typed thinking events) |
| R5 | ultimate-external | non-stream | ✅ PASS | id=58d11983… thinking == expected (128 bytes, byte-exact) |
| R6 | ultimate-external | stream | ✅ PASS | id=9fb8c64f… thinking == expected (128 bytes, byte-exact; passive SSE capture) |
| R7 | ultimate-internal | non-stream | ✅ PASS | id=a21fd581… thinking == expected (128 bytes, byte-exact) |
| R8 | ultimate-internal | stream | ✅ PASS | id=38f079dd… thinking == expected (128 bytes, byte-exact) |
| R9 | anthropic-client /v1/messages, internal | stream | ✅ PASS | id=bcaf552c… thinking == expected (128 bytes, byte-exact; thinking-sink side channel) |
| R10 | anthropic-client /v1/messages, external | non-stream | ✅ PASS | id=d9025687… thinking == expected (128 bytes, byte-exact) |
| N1 | race-external | non-stream | ✅ PASS | id=945b9efe… ZERO "thinking" occurrences (omitempty clean) |
| N2 | race-internal | non-stream | ✅ PASS | id=3a7f1843… ZERO "thinking" occurrences |
| N3 | ultimate-external | non-stream | ✅ PASS | id=aced5010… ZERO "thinking" occurrences |
| N4 | ultimate-internal | non-stream | ✅ PASS | id=cd404ec7… ZERO "thinking" occurrences |
| M1 | minimax translated race-external | stream | ✅ PASS | id=e18a8aaf… thinking == translated text (25 bytes, byte-exact; client deltas verified) |
| M2 | minimax translated ultimate-external | non-stream | ✅ PASS | id=7f301720… thinking == translated text (21 bytes, byte-exact) |

Positive-row reasoning fixture (R-rows): 128-byte multi-sentence constant split
across two stream chunks (accumulation exercised). MiniMax rows use
`reasoning_details` fixtures (translated-suffix format, cribbed from
`test/e2e_minimax_reasoning` S1/S6) and assert the FE thinking equals the
translated reasoning text the client sees.

## Path mechanics verified during the run

- **Ultimate rows**: `X-Force-Ultimate-Model: true` + token with
  `ultimateModelEnabled=true` + `ULTIMATE_MODEL_MAX_RETRIES=0` → ForceTrigger
  fires the ultimate path on the FIRST call (no duplicate-hash dance needed).
- **Anthropic rows (R9/R10)**: driven via `handler.HandleAnthropicMessages`
  with `{"model","max_tokens":1024,"stream",...}`, headers `x-api-key` +
  `anthropic-version: 2023-06-01`. R9 uses a registered Internal:true model
  (openai credential ⇒ translation mode → `doAnthropicInternalRequest` →
  thinking sink); R10 uses the unregistered glm-5.3 (external upstream,
  OpenAI translation mode → `handleAnthropicNonStreamResponse`).
- **MiniMax gate rows**: M1 = global upstream credential provider=minimax +
  `X-Proxy-Interleaved-Thinking`; M2 = the MODEL's CredentialID is minimax
  (the ultimate-external gate resolves the provider from modelCfg.CredentialID).

## Harness-bug fix applied (one)

- **M2 initially failed with empty FE thinking.** Root cause: HARNESS BUG,
  not a product bug. The M2 row first pointed `ULTIMATE_MODEL_ID` at
  `matrix-ultimate-external`, which was bound to the *openai* credential —
  but the ultimate-external translation gate reads the **model's**
  CredentialID (documented in `test/e2e_minimax_reasoning` S9: "Ultimate
  paths gate on the MODEL's CredentialID"). With the gate off, the response
  passed through byte-identically carrying `reasoning_details` (no
  `reasoning_content` ⇒ legitimately nothing to capture — passthrough is the
  correct legacy contract for gate-off). Fix: registered
  `matrix-ultimate-external-minimax` bound to the minimax credential and
  pointed the M2 row at it. No assertion weakened; no production change.

## Conclusion

Thinking capture + FE API visibility hold on **every** proxy path: 4 race
variants, 4 ultimate variants, 2 anthropic-client variants, plus the MiniMax
translated formats — with strict byte-exact equality on positives and strict
zero-occurrence absence on negatives. No real findings against production
code; the only failure encountered was the M2 harness wiring bug above.

Suite total (20 tests incl. closure gate A–D): **1.764s**, stable across two
consecutive runs (1.815s / 1.758s). No fixed ports (httptest ephemeral only);
no leaked listeners post-run; port 8088 never touched.
