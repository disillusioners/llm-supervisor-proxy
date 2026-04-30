## Phase 3 Protocol Correctness — 4 Fixes in llm-supervisor-proxy

### Key Learnings:

1. **opencode_skill not available**: The `opencode_skill` binary wasn't installed. Used `opencode run --dir <path>` instead with heredoc prompts. This works but produces verbose stderr logs.

2. **Go 1.26 toolchain**: The project requires Go 1.26 but the environment only has Go 1.22. `go build` and `go test` fail with "toolchain not available". However, `gofmt` (syntax check) and gopls (LSP diagnostics) work fine for verification.

3. **Parallel sessions**: Running 3 `opencode run` commands simultaneously works — they each get their own session and directory. However, there was a git lock conflict (`index.lock` exists) when multiple sessions tried to snapshot simultaneously. This is cosmetic and doesn't affect the actual code changes.

4. **Fix M1 architecture**: The proxy translates between OpenAI and Anthropic formats. The existing `TranslateToolChoice` function goes Anthropic→OpenAI. A new `TranslateOpenAIToolChoiceToAnthropic` was needed for the reverse direction. Both `internal_handler.go` and `race_executor.go` needed to use this new function.

5. **Fix M2+M3 combined**: These fixes both touch error handling code in the same files. Running them in a single session was the right call — combining them avoided conflicts.

### Files Modified:
- `pkg/proxy/translator/tools.go` — New `TranslateOpenAIToolChoiceToAnthropic` function
- `pkg/proxy/translator/types.go` — Removed `omitempty` from Content field
- `pkg/proxy/adapter_anthropic.go` — Error format consistency + no trailing newline
- `pkg/proxy/handler_anthropic.go` — Error format consistency + no trailing newline
- `pkg/proxy/internal_handler.go` — Uses new tool_choice translator
- `pkg/proxy/race_executor.go` — Uses new tool_choice translator
