# E2E Reproduction: reasoning_content on Ultimate-Internal Path (verify 83814b0)

**Date:** 2026-08-18
**New asset:** `test/e2e_ultimate_internal_reasoning/e2e_ultimate_internal_reasoning_test.go` (commit `c3a4b35`, 376 lines, test-code only)

## Scenario
Client sends 3-message conversation to `/v1/chat/completions` with
`reasoning_content` on the assistant message (index 1); ultimate fallback resolves to an
INTERNAL model; capturing mock upstream records the forwarded body.

**Trigger used:** `X-Force-Ultimate-Model: true` admin header (fail-closed gate at
`pkg/proxy/handler.go:464-469`) + `ULTIMATE_MODEL_MAX_RETRIES=0` so the ultimate path
fires on the first call (with default retries=2, `shouldTrigger` needs count ≥ 2 per
`pkg/ultimatemodel/handler.go:107-111`). Hash-duplicate trigger also exists but is
impractical for single-call in-process tests.

## Negative-control proof (test detects the original bug)
| Run | State | Result |
|---|---|---|
| a | HEAD 83814b0 (fix present) | **PASS** — `messages[1].reasoning_content` present in captured upstream body |
| b | fix reverted (3 lines at handler_internal.go:453-455 deleted) | **FAIL** — `has-field=false, got="", want="Let me reason deeply..."`, captured `messages[1]` had only role/content (the exact original symptom) |
| c | fix restored | **PASS** — byte-identical to run (a) |

Assertions: slot-precise (`messages[1]`), value-exact, message-count 3, all `content`
fields verbatim, `model` rewritten to ultimate model's InternalModel.

## Why the in-process harness (not the shell harness)
The shell harness (`test_mock_ultimate_model.sh`) does NOT capture upstream request
bodies — only response-side greps. For "what did the proxy FORWARD" assertions, use the
in-process Go pattern from `test/e2e_reasoning_content/e2e_reasoning_content_test.go`
(httptest mockUpstream with `capturedRequests[].BodyParsed`).

## Lesson
When verifying a field-preservation fix, the assertion must be on the CAPTURED UPSTREAM
body at the exact message slot — client-response assertions cannot see this bug class.
