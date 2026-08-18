# Reachability Probe: Third Converter `InternalHandler.convertRequest` (no fix applied, by instruction)

**Date:** 2026-08-18
**Trigger:** Reviewer follow-up on commit 83814b0 verification — third field-rebuilding
converter at `pkg/proxy/internal_handler.go:272` lacks `reasoning_content` handling.
**Instruction:** Evidence only, DO NOT fix.

## Verdict: REACHABLE-UNDER-CONDITION — bug DORMANT (no live reasoning loss today)

Live route (executes on every matching request, streaming or not):

```
POST /v1/messages                          cmd/main.go:139 (mux.HandleFunc)
→ HandleAnthropicMessages                  pkg/proxy/handler_anthropic.go:39
→ attemptAnthropicModel (fallback loop)    handler_anthropic.go:242 (re-translate per model at :233)
→ gate: modelConfig.Internal               handler_anthropic.go:282
→ gate: credential provider != "anthropic" handler_anthropic.go:300-339 (anthropic-cred → passthrough, skips InternalHandler)
→ doAnthropicInternalRequest               handler_anthropic.go:340
→ json.Unmarshal(arc.requestBody)          handler_anthropic.go:449-450 (OpenAI-translated body)
→ internalHandler.HandleRequest            handler_anthropic.go:461-462
→ convertRequest                           pkg/proxy/internal_handler.go:95 → :272
```

Single call site (`internal_handler.go:95`); `/v1/chat/completions` does NOT use this
converter (internal models there route via `race_executor.go:655`, which already preserves
`reasoning_content` at `:724` — the twin the fix 83814b0 mirrored).

## Why dormant (two independent blockers)
1. `AnthropicRequest`/`AnthropicMessage` types (`pkg/proxy/translator/types.go:15-40`) have
   no `reasoning_content` field — dropped at client-body unmarshal.
2. Native Anthropic `thinking` blocks explicitly discarded in translation
   (`pkg/proxy/translator/request.go:554-556`: `case "thinking": return nil`).

So the map fed to `convertRequest` on this route never contains per-message
`reasoning_content` — there is nothing to lose. Message-rebuild loop (`:282-349`) copies
role/content/name/tool_call_id/tool_calls only; top-level unknown keys survive via
`req.Extra` (`:424-432`) but that loop does not descend into message maps.

## Latent risk (recommendation for leader — NOT fixed here)
If the translator ever learns forward thinking→`reasoning_content` mapping (the REVERSE
direction already exists at `translator/response.go:117-133`), this becomes a live bug
overnight. Also: the Anthropic internal branch is untested (no test configures
`Internal: true` for `/v1/messages`; `internal_handler_test.go:200` calls convertRequest
directly only). Suggest: parity fix + coverage in a follow-up change.

## Test-evidence notes
- `test/test_mock_openai_internal_buffered.sh` + `test_mock_openai_internal.sh` POST only
  to `/v1/chat/completions` — neither reaches InternalHandler.
- Only `test/test_anthropic_e2e.sh` hits `/v1/messages`, and it uses no internal models.
