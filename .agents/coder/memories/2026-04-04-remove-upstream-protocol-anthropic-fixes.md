# Remove UpstreamProtocol Config + Fix Anthropic Fallback Bugs

**Date:** 2026-04-04
**Commit:** 2d989be

## What was done
- Removed `UpstreamProtocol` field from Config struct, env var loading, Clone(), ConfigSnapshot
- Removed `resolveUpstreamProtocol()` function entirely
- Simplified detection: external = always OpenAI, internal = detect via `credential.Provider == "anthropic"`
- Fixed critical fallback state mutation bug: save/restore `arc` mutable fields around `attemptAnthropicModel` calls
- Handle `json.Marshal` error (return false instead of silent proceed)
- Remove dead `currentModel` param from `doAnthropicRequest`
- Remove double credential lookup (cache from first call)
- Updated README.md to remove all UPSTREAM_PROTOCOL references

## Key files
- `pkg/config/config.go` - Config struct + env var
- `pkg/proxy/handler.go` - ConfigSnapshot + Clone
- `pkg/proxy/handler_anthropic.go` - Main changes (detection, fallback, quality)
- `README.md` - Documentation

## Lessons
- Always check README.md for removed config options - code cleanup often misses docs
- Fallback loops with mutable shared pointers need save/restore pattern
