# E2E FE Reasoning Observability — Closure Gate Results

**Date**: 2026-08-21
**Suite**: `test/e2e_fe_reasoning_observability/` (new)
**Branch**: `fix/ui-reasoning-observability` @ db7aca0 (tree clean before work)
**Command**: `timeout 300 go test ./test/e2e_fe_reasoning_observability/ -v -count=1 -timeout 240s`

## RESULT: **PASS** — 4/4 scenarios, suite runtime **0.443s** (well under the 240s inner / 300s outer caps)

## The bug being closed

A glm-5.3 NON-STREAMING response carrying `choices[0].message.reasoning_content`
was visible in raw server JSON but `GET /fe/api/requests/{id}` returned JSON
with ZERO occurrences of "thinking" and the UI 🧠 block never rendered.
Root cause was capture-side (10 commits on fea5874, head db7aca0, populate
`store.Message.Thinking` on previously non-capturing paths).
`pkg/ui/server.go` was already correct — it serializes the whole RequestLog.

## Scenario evidence

### Scenario A — CLOSURE GATE (glm-5.3 non-stream) — ✅ PASS (0.14s)
- Request id used: `7298ed43-0364-4731-a9a4-025b82dacb6b`
- Proxy log confirms the fixed capture path fired:
  `extractNonStreamContent: top-level reasoning_content len=217` /
  `total response len=20, thinking len=217`
- FE API `GET /fe/api/requests/{id}` (real HTTP, unauthenticated) → 200;
  raw payload contains **1 occurrence of `"thinking"`** (original symptom was ZERO).
- `messages[1].thinking` (assistant) == the exact 217-byte glm reasoning
  constant, **byte-exact** (multi-sentence: "Okay, the user is asking about
  the capital of France. Let me break this down step by step. …").
- Belt-and-braces: `reqStore.Get(id).Messages[last].Thinking` matches too.
- Path exercised: race-external (unregistered model `glm-5.3`, external
  credential), client request omits the `stream` field entirely — exactly the
  original bug shape.

### Scenario B — request-side reasoning (DeepSeek-replay) — ✅ PASS (0.13s)
- Request id used: `2ec54d17-8a3c-4076-98c9-cf9de2bbd9c9`
- messages = [user, assistant w/ `reasoning_content`, user]; upstream returns
  plain completion (no reasoning).
- FE API: request-side `messages[1].thinking` == sent `reasoning_content`
  (99 bytes, **byte-exact**).
- Response-side `messages[3]` has **NO thinking field** (omitempty → key
  absent in JSON; also asserted empty-string after typed decode).

### Scenario C — negative cleanliness — ✅ PASS (0.13s)
- Request id used: `21665f3a-b348-4744-9723-39db336fefb2`
- No reasoning sent or returned → FE detail payload contains **ZERO
  occurrences of the substring `thinking`**; every message decodes without a
  thinking field. omitempty keeps the payload clean.

### Scenario D — payload-shape static check — ✅ PASS (0.00s)
- `pkg/ui/frontend/src/components/RequestDetail.tsx` (🧠 block, ~line 697)
  guards on `message.thinking &&` and renders `message.thinking` via
  `<CollapsibleText>` inside the 🧠 Thinking `<details>` block — exactly the
  field the FE API returns (`messages[i].thinking`, store.Message.Thinking
  `json:"thinking,omitempty"`). Field names MATCH → the 🧠 block will render
  with a correct API payload.

## Harness notes / fixes applied

1. **Harness bug (fixed)**: Scenario D initially read the component at
   `pkg/ui/frontend/src/components/RequestDetail.tsx`, but a Go test binary's
   CWD is its *package* dir. Fixed to `../../pkg/ui/...`. (Harness fix only;
   no assertion weakened.)
2. Reused the `test/e2e_minimax_reasoning` harness pattern, extended with:
   - `testEnv.reqStore` exposure (the gap called out in recon)
   - real-HTTP FE API via `ui.NewServer(...)` + `RegisterHandlers` on an
     `httptest.Server` sharing the same `*store.RequestStore`
3. Ports: `httptest` ephemeral loopback only; no fixed ports, no 8088, no
   post-run cleanup needed (in-process, reaped on exit). Verified no leaked
   listeners post-run.

## Conclusion

The original symptom is **reproducibly gone** end-to-end on HEAD of
`fix/ui-reasoning-observability`: reasoning from a glm-5.3 non-stream
response is captured, stored, served over the real FE API under the exact
field name the UI renders. Closure gate satisfied.
