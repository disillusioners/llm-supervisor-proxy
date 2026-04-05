# Review: handler_anthropic.go Commits c4cf706 + 9cda349

## Date: 2026-04-02
## Verdict: 🔴 REQUEST CHANGES

## Key Findings
- CRITICAL: State mutation bug in fallback loop (arc fields never reset)
- CRITICAL: Fallback loop uses outer `isAnthropicUpstream` var instead of `arc.isAnthropicUpstream`
- WARNING: json.Marshal error silently discarded at line 300
- WARNING: Accumulators/headersSent not reset between fallback iterations
- INFO: Dead parameter, double lookup, no internal-passthrough tests

## Architecture Notes
- External upstream = always LiteLLM (OpenAI protocol)
- Internal upstream with anthropic credential = direct Anthropic passthrough
- arc created once before fallback loop, mutated in attemptAnthropicModel
- UPSTREAM_PROTOCOL env var still respected via config.go:363 (not removed)
