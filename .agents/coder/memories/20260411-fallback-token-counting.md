# Fallback Token Counting Feature

## Feature
When LLM providers don't return token usage in responses, proxy now counts tokens using tiktoken-go and injects estimated usage.

## Key Decisions
1. **Package location**: `pkg/proxy/token/` — separate package to avoid import cycles with `pkg/proxy/`
2. **Singleton pattern**: `GetTokenizer()` with `sync.Once` — tiktoken-go caches encodings internally
3. **Fallback condition**: Only triggers when ALL three fields are zero: `PromptTokens == 0 && CompletionTokens == 0 && TotalTokens == 0` — important to check all 3 to avoid overriding partial usage from providers
4. **Error handling**: Log errors from token counting but don't fail the request
5. **Feature flag**: `TOKEN_FALLBACK_ENABLED` env var (default: true)

## Integration Points (11 total)
- **handler.go**: 3 paths (ultimate model, streaming, non-streaming)
- **race_executor.go**: 4 paths (internal/external × streaming/non-streaming)
- **ultimatemodel/**: 4 paths (internal/external × streaming/non-streaming)

## Review Finding
Initially only checked `PromptTokens` and `CompletionTokens` in fallback condition. Review caught that providers might return only `TotalTokens` — fixed to check all 3 fields.

## Files Created
- `pkg/proxy/token/counter.go` — Tokenizer singleton, model→encoding map, counting methods
- `pkg/proxy/token/prompts.go` — extractPromptText, ExtractCompletionTextFromChunks, ExtractCompletionTextFromJSON

## Files Modified
- `pkg/proxy/handler.go` — 3 fallback insertions
- `pkg/proxy/race_executor.go` — 4 fallback insertions + rawBody parameter additions
- `pkg/ultimatemodel/handler.go` — requestBodyBytes parameter
- `pkg/ultimatemodel/handler_internal.go` — 2 fallback insertions + requestBodyBytes param
- `pkg/ultimatemodel/handler_external.go` — 2 fallback insertions + requestBodyBytes param
- Test files updated for new function signatures
- `go.mod`, `go.sum` — added tiktoken-go

## Dependency
- `github.com/pkoukk/tiktoken-go` — Go port of OpenAI's tiktoken tokenizer

## Commit: 475580a
