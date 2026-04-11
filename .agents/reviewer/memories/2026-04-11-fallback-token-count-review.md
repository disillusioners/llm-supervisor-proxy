# 2026-04-11-fallback-token-count-review.md

## Feature: Fallback Token Counting (branch: feature/fallback-token-count)
- Commits: 475580a, 20178f3
- Files: counter.go, prompts.go, + 4 integration files
- tiktoken-go v0.1.8 for tokenization

## Key Findings
1. CRITICAL: gpt-4o prefix collision — non-deterministic encoding resolution (confirmed with test)
2. WARNING: String concatenation O(n²) in extractPromptText
3. WARNING: Unnecessary sync.RWMutex on immutable map
4. WARNING: Missing tool_call argument extraction
5. INFO: Fallback executed on all racers (wasteful but correct)

## Patterns to Remember
- Map iteration for prefix matching is NON-DETERMINISTIC in Go — use sorted slice instead
- The fallback condition pattern `usage == nil || (all three fields zero)` is correct for preventing override of partial provider usage
- Integration points: 3 in handler.go, 4 in race_executor.go, 2 in handler_internal.go, 2 in handler_external.go = 11 total
