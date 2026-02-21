# Fallback Models Code Review (Simplified Fallback & XDG Paths)

I've reviewed the latest changes regarding the config path update and the removal of the fallback cycle checker.

### 1. XDG User Config Directory Update
Fantastic architectural decision! Migrating away from a relative `./config/models.json` to `~/.config/llm-supervisor-proxy/models.json` via `os.UserConfigDir()` is standard best practice for Linux/macOS daemon binaries.
- It ensures that users can run the `llm-supervisor-proxy` from anywhere on their system without having it pollute their current working directory with state files.
- The path creation logic heavily uses `os.MkdirAll` before flushing, which is structurally safe.

### 2. Removal of Cycle Detection (Safe!)
Removing the DFS Cyclic Graph detection and simplifying validation is completely functionally safe for your current implementation. Because `GetFallbackChain` does not execute recursively, if the user configures **Model A → Fallback B** and **Model B → Fallback A**:
- The proxy handler (`pkg/proxy/handler.go`) simply appends the chains as `[Model A, Model B]`.
- It iterating precisely once through that flat array.
- There is zero risk of an infinite retry loop or stack overflow! 

### Recommendation: Add Hard Bounds Checking
You mentioned that you wanted to restrict the fallback to "only one level" (a length of 1 item), and added comments in `Validate()` saying `// Fallback chain is now limited to max 1 item`.
However, because the `POST /api/models` endpoint takes a JSON array `["model-b", "model-c"]`, the API will still silently accept an array of 50 models because there is no strict length enforcement.

**Recommendation:** Inside `func (mc *ModelsConfig) Validate() error` in `pkg/models/config.go`, just before you validate references, add a strict array bounds check:
```go
if len(model.FallbackChain) > 1 {
    return fmt.Errorf("fallback chain is limited to maximum 1 fallback model")
}
```
This guarantees the API enforces your new 1-level invariant and throws a `400 Bad Request` if the UI erroneously submits multiple!

Overall, this greatly reduced code complexity. Excellent iteration!
