package ultimatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxyheader"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// ShouldTriggerResult contains the result of ShouldTrigger check
type ShouldTriggerResult struct {
	Triggered      bool   // True if ultimate model should be used
	Hash           string // The computed hash
	RetryExhausted bool   // True if max retries exceeded (after increment)
	CurrentRetry   int    // Current retry count (after increment)
	MaxRetries     int    // Configured max retries
}

// Handler manages ultimate model requests.
// It detects duplicate requests via message hash and triggers
// the ultimate model as a raw proxy, bypassing all normal logic.
type Handler struct {
	config    config.ManagerInterface
	modelsMgr models.ModelsConfigInterface // Database-backed
	hashCache *HashCache
	eventBus  *events.Bus

	// Tool call buffer configuration
	toolCallBufferMaxSize  int64              // Max size for tool call buffer
	toolCallBufferDisabled bool               // Disable tool call buffering
	toolRepairConfig       *toolrepair.Config // Tool repair config for buffer
}

// NewHandler creates a new ultimate model handler
func NewHandler(cfg config.ManagerInterface, modelsMgr models.ModelsConfigInterface, eventBus *events.Bus) *Handler {
	maxHash := cfg.Get().UltimateModel.MaxHash
	if maxHash <= 0 {
		maxHash = 100
	}

	return &Handler{
		config:    cfg,
		modelsMgr: modelsMgr,
		hashCache: NewHashCache(maxHash),
		eventBus:  eventBus,
	}
}

// ShouldTrigger checks if ultimate model should be triggered.
// Hash is registered on entry - first call returns false, subsequent calls
// with same hash trigger ultimate model.
// On success, hash is removed from cache; on failure, hash remains.
func (h *Handler) ShouldTrigger(messages []map[string]interface{}) ShouldTriggerResult {
	return h.shouldTriggerInternal(messages, false)
}

// ForceTrigger always triggers ultimate model, bypassing hash cache check.
// Used for testing/debugging via X-Force-Ultimate-Model header.
func (h *Handler) ForceTrigger(messages []map[string]interface{}) ShouldTriggerResult {
	return h.shouldTriggerInternal(messages, true)
}

func (h *Handler) shouldTriggerInternal(messages []map[string]interface{}, force bool) ShouldTriggerResult {
	cfg := h.config.Get()
	if cfg.UltimateModel.ModelID == "" {
		return ShouldTriggerResult{Triggered: false}
	}

	// Handle empty messages
	if len(messages) == 0 {
		return ShouldTriggerResult{Triggered: false}
	}

	// Generate hash from messages (role + content only)
	hash := HashMessages(messages)

	// Store hash and check if it was already there (atomic operation)
	// Returns true if duplicate, false if first time
	alreadyExists := h.hashCache.StoreAndCheck(hash)

	// First time seeing this request - store hash, don't trigger, don't increment
	if !force && !alreadyExists {
		return ShouldTriggerResult{Triggered: false, Hash: hash}
	}

	// Hash was already in cache (duplicate) - increment retry counter
	maxRetries := cfg.UltimateModel.MaxRetries
	if maxRetries <= 0 {
		// MaxRetries=0 means unlimited - trigger on any duplicate after first
		return ShouldTriggerResult{
			Triggered:      true,
			Hash:           hash,
			RetryExhausted: false,
			CurrentRetry:   1,
			MaxRetries:     0,
		}
	}

	// ATOMIC increment and check - prevents race condition
	newCount, exhausted := h.hashCache.IncrementAndCheckRetry(hash, maxRetries)

	// Trigger if we've seen this hash 2+ times (3rd call onwards)
	// This gives 2 retries without ultimate model before triggering
	shouldTrigger := newCount >= 2

	return ShouldTriggerResult{
		Triggered:      shouldTrigger,
		Hash:           hash,
		RetryExhausted: exhausted,
		CurrentRetry:   newCount,
		MaxRetries:     maxRetries,
	}
}

// MarkFailed stores the hash to mark this request as failed.
// DEPRECATED: Hash is now stored automatically on ShouldTrigger entry.
// This function is kept for backward compatibility with existing code.
// Returns the computed hash for reference.
func (h *Handler) MarkFailed(messages []map[string]interface{}) string {
	if len(messages) == 0 {
		return ""
	}

	hash := HashMessages(messages)
	h.hashCache.StoreAndCheck(hash) // Store hash to mark as failed
	return hash
}

// GetModelID returns the configured ultimate model ID
func (h *Handler) GetModelID() string {
	return h.config.Get().UltimateModel.ModelID
}

// SetToolCallBufferConfig sets the tool call buffer configuration
func (h *Handler) SetToolCallBufferConfig(maxSize int64, disabled bool, repairConfig *toolrepair.Config) {
	h.toolCallBufferMaxSize = maxSize
	h.toolCallBufferDisabled = disabled
	h.toolRepairConfig = repairConfig
}

// OnConfigChange handles config change events.
// When ultimate_model.model_id changes, reset the hash cache.
func (h *Handler) OnConfigChange(event events.Event) {
	// Get previous config from event data if available
	if data, ok := event.Data.(map[string]interface{}); ok {
		if field, ok := data["field"].(string); ok && field == "ultimate_model.model_id" {
			log.Printf("[UltimateModel] Model ID changed, resetting hash cache")
			h.hashCache.Reset()
		}
	}
}

// ExecuteResult bundles the outcome of an ultimate-model request.
//
// In addition to the token usage the outer proxy handler already
// consumes, it carries the passively-captured assistant content,
// thinking text, AND tool calls so the outer handler can persist a
// store.Message{Role: "assistant", Content, Thinking, ToolCalls}
// alongside the existing usage store. Without this, ultimate-model
// paths persisted NO assistant message and the Web UI showed no
// reply at all — visible especially when the assistant turn was
// purely a tool call (empty Content + empty Thinking + real
// tool_calls): Fix 3 closed the Content/Thinking hole; W7 extends
// the same capture contract to tool calls so tool-call-only turns
// also persist.
//
// Capture is purely passive: it observes bytes / events that are
// already being written to the client and never mutates them.
// Wire bytes to the client are byte-identical with or without
// capture enabled — see pkg/ultimatemodel/handler_capture_test.go.
type ExecuteResult struct {
	Usage     *store.Usage
	Content   string
	Thinking  string
	ToolCalls []store.ToolCall
}

// coerceContentString coerces a possibly-string interface{} value
// to a string (S1). It is the single shared content-coercion point
// for all passive capture paths: JSON-decoded map values (delta
// fields, message fields) and the provider struct's interface{}
// ChatMessage.Content all funnel through here, so the "what counts
// as capturable text" rule lives in exactly one place.
//
// Non-string values (nil, numbers, []interface{} multimodal parts)
// coerce to "" — capture is best-effort and must never fail or
// alter the wire path. Append-only callers simply skip the write
// when the result is empty.
func coerceContentString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// captureFromSSEChunk parses a single SSE `data: <json>` payload
// (WITHOUT the `data: ` prefix) and appends any delta.content /
// delta.reasoning_content it finds into the provided builders.
//
// Returns true if the chunk was a JSON data chunk that could be
// parsed (even if it had no content/thinking fields). Lines that
// are not data chunks, are [DONE], or fail to parse are silently
// ignored — capture is best-effort and must never break the wire
// path.
//
// This is the shared parser used by both streamResponse
// (handler_external.go) and handleInternalStream
// (handler_internal.go). Centralising here keeps the extraction
// logic in one place and matches the Web UI's expectation that
// Content is the aggregated assistant text and Thinking is the
// aggregated reasoning_content string.
func captureFromSSEChunk(data []byte, content, thinking *strings.Builder) {
	if len(data) == 0 {
		return
	}
	// Skip SSE terminator and stray whitespace-only payloads.
	if bytes.Equal(data, []byte("[DONE]")) || bytes.HasPrefix(data, []byte("\n")) {
		return
	}
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return
	}
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}
	c, ok := choices[0].(map[string]interface{})
	if !ok {
		return
	}
	delta, ok := c["delta"].(map[string]interface{})
	if !ok {
		return
	}
	if s := coerceContentString(delta["content"]); s != "" {
		content.WriteString(s)
	}
	if s := coerceContentString(delta["reasoning_content"]); s != "" {
		thinking.WriteString(s)
	}
}

// captureFromSSEChunkBytes parses a chunk written on the wire and
// appends any delta.content / delta.reasoning_content it finds into
// the provided builders. It is a pure reader: it never mutates the
// chunk bytes and has zero effect on what the client receives.
//
// SSE FRAMING COUPLING (W6): this helper assumes OpenAI SSE framing
// (`data: {...}\n\n`). Historically it hard-stripped a `data: `
// prefix; a chunk not starting with that prefix was treated as a
// non-data line and silently captured NOTHING — meaning that if an
// upstream ever changes its framing convention, capture would
// silently yield empty content/thinking. That silent degradation is
// ACCEPTED by design: capture is best-effort and observational, so
// it must never log per-chunk noise on the hot path, never error,
// and above all never mutate or re-frame the wire bytes to make
// parsing easier. A missing capture is a UI observability gap, not
// a protocol failure.
//
// The coupling is softened capture-side only: if the chunk does NOT
// start with `data: `, the bytes are attempted as bare JSON (some
// upstreams emit unframed JSON lines). If that parse also fails,
// the chunk is silently ignored (no error, no capture) — identical
// to the pre-fallback behavior for garbage input. This fallback is
// parse-only: it reads the bytes and cannot affect them, so wire
// byte-identity is preserved by construction.
func captureFromSSEChunkBytes(line []byte, content, thinking *strings.Builder) {
	payload := line
	if bytes.HasPrefix(payload, []byte("data: ")) {
		payload = bytes.TrimPrefix(payload, []byte("data: "))
		// Trim trailing newline if present (lines include the trailing \n
		// because reader.ReadString('\n') keeps it).
		payload = bytes.TrimRight(payload, "\r\n")
	} else {
		// Bare-JSON fallback (W6): attempt the raw bytes directly as
		// JSON. captureFromSSEChunk already ignores anything that is
		// not a parseable chat-completion chunk ([DONE], event lines,
		// whitespace, garbage) — so delegating with the unstripped
		// bytes gives us "capture if parseable, silent otherwise"
		// with no framing knowledge required on this branch.
		payload = bytes.TrimRight(payload, "\r\n")
	}
	captureFromSSEChunk(payload, content, thinking)
}

// captureFromNonStreamResponse parses the post-write non-stream
// response body (bodyBytes, including any translator mutations
// applied before write) and returns the aggregated content and
// reasoning_content. Returns "", "" if the body is not a valid
// JSON chat completion.
//
// The body is read-only — bytes that the proxy writes to the
// client are unchanged.
func captureFromNonStreamResponse(bodyBytes []byte) (content, thinking string) {
	if len(bodyBytes) == 0 {
		return "", ""
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return "", ""
	}
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", ""
	}
	c, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", ""
	}
	msg, ok := c["message"].(map[string]interface{})
	if !ok {
		return "", ""
	}
	content = coerceContentString(msg["content"])
	thinking = coerceContentString(msg["reasoning_content"])
	return content, thinking
}

// captureToolCallsFromNonStreamResponse parses the post-write
// non-stream response body (bodyBytes, including any translator
// mutations applied before write) and returns the assembled
// choices[0].message.tool_calls as []store.ToolCall — or nil if
// none are present or the body is not a chat-completion JSON.
//
// The body is read-only — bytes that the proxy writes to the
// client are unchanged. W7: this is the capture-side hook that
// lets outer handler.go persist a store.Message when the
// assistant turn is purely a tool call (empty Content + empty
// Thinking), which previously left the UI with nothing to show.
func captureToolCallsFromNonStreamResponse(bodyBytes []byte) []store.ToolCall {
	if len(bodyBytes) == 0 {
		return nil
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil
	}
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	c, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	msg, ok := c["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawCalls, ok := msg["tool_calls"].([]interface{})
	if !ok || len(rawCalls) == 0 {
		return nil
	}
	out := make([]store.ToolCall, 0, len(rawCalls))
	for _, raw := range rawCalls {
		tcMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		var tc store.ToolCall
		if id, ok := tcMap["id"].(string); ok {
			tc.ID = id
		}
		if typ, ok := tcMap["type"].(string); ok {
			tc.Type = typ
		}
		if fn, ok := tcMap["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				tc.Function.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				tc.Function.Arguments = args
			}
		}
		out = append(out, tc)
	}
	return out
}

// captureToolCallsFromSSEChunk extracts delta.tool_calls from a
// single SSE payload (the SAME parsed JSON content+thinking were
// observed from in captureFromSSEChunkBytes — see that helper for
// the byte-identity contract this shares) and merges them into
// the provided accumulator and per-index arguments builder.
//
// W7 mirror of pkg/proxy/handler_helpers.go's
// extractStreamChunkContent tool-call accumulation: across OpenAI
// streaming events, the first delta for a given index carries
// ID/Type/Name and subsequent deltas carry additional arguments
// fragments that are concatenated into the per-index builder. The
// caller is responsible for collapsing the argument builders
// into store.ToolCall.Function.Arguments once streaming ends.
//
// The chunk is read-only — bytes are never mutated. Chunks that
// are not data lines, [DONE], or have parse failures are silently
// ignored, matching captureFromSSEChunkBytes's best-effort
// posture.
func captureToolCallsFromSSEChunk(data []byte, accum *[]store.ToolCall, argBuilders *[]*strings.Builder) {
	if len(data) == 0 || accum == nil || argBuilders == nil {
		return
	}
	// W7: mirror the framing-coupling of captureFromSSEChunkBytes
	// — strip the leading `data: ` SSE prefix and trailing newlines
	// before parsing. Some upstreams emit unframed JSON lines (the
	// bare-JSON fallback at W6); for tool-call chunks in
	// practice these are always `data: ` framed (OpenAI SSE)
	// because the encoding is hard-wired into the tool-buffer's
	// emitToolCall output. Try the framed path first; if that
	// fails, retry against the un-stripped bytes so capture is
	// best-effort across both shapes.
	payload := data
	if bytes.HasPrefix(payload, []byte("data: ")) {
		payload = bytes.TrimPrefix(payload, []byte("data: "))
		payload = bytes.TrimRight(payload, "\r\n")
	} else {
		payload = bytes.TrimRight(payload, "\r\n")
	}
	var chunk map[string]interface{}
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return
	}
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return
	}
	rawCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(rawCalls) == 0 {
		return
	}
	for _, raw := range rawCalls {
		tcMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		// Get index (fallback to 0 if missing per OpenAI
		// streaming convention).
		index := 0
		if idx, ok := tcMap["index"].(float64); ok {
			index = int(idx)
		}
		if index < 0 {
			index = 0
		}
		// Ensure accumulator has enough capacity for this index.
		for len(*accum) <= index {
			*accum = append(*accum, store.ToolCall{})
			b := &strings.Builder{}
			b.Grow(1024)
			*argBuilders = append(*argBuilders, b)
		}
		tc := &(*accum)[index]
		if id, ok := tcMap["id"].(string); ok && id != "" {
			tc.ID = id
		}
		if typ, ok := tcMap["type"].(string); ok && typ != "" {
			tc.Type = typ
		}
		if fn, ok := tcMap["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				tc.Function.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				(*argBuilders)[index].WriteString(args)
			}
		}
	}
}

// finalizeAccumulatedToolCalls collapses the per-index
// arguments builders into store.ToolCall.Function.Arguments on
// each accumulated tool call, returning the assembled slice.
// Empty-builder slots leave Arguments as-is (already-empty
// default).
func finalizeAccumulatedToolCalls(accum []store.ToolCall, argBuilders []*strings.Builder) []store.ToolCall {
	if len(accum) == 0 {
		return nil
	}
	for i := range accum {
		if i < len(argBuilders) && argBuilders[i] != nil {
			accum[i].Function.Arguments = argBuilders[i].String()
		}
	}
	return accum
}

// SendRetryExhaustedError sends a JSON stream error response.
// This uses HTTP 200 with SSE error format to make streaming clients stop gracefully.
func (h *Handler) SendRetryExhaustedError(
	w http.ResponseWriter,
	hash string,
	currentRetry int,
	maxRetries int,
	isStream bool,
) error {
	// Safely extract hash prefix (defensive against short/empty hashes)
	hashPrefix := hash
	if len(hash) > 8 {
		hashPrefix = hash[:8]
	}

	message := fmt.Sprintf(
		"Ultimate model retry limit exceeded (attempt %d of %d max). Hash: %s...",
		currentRetry, maxRetries, hashPrefix,
	)

	// Build OpenCode-compatible error response
	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "ultimate_model_retry_exhausted",
			"code":    "exhausted",
			"message": message,
			"hash":    hash,
		},
	}

	errorJSON, err := json.Marshal(errorResp)
	if err != nil {
		// Fallback to static error message if marshaling fails
		errorJSON = []byte(`{"type":"error","error":{"type":"ultimate_model_retry_exhausted","code":"exhausted","message":"Ultimate model retry limit exceeded"}}`)
	}

	// Set headers based on response type FIRST
	if isStream {
		// SSE format for streaming requests
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		fmt.Fprintf(w, "data: %s\n\n", string(errorJSON))
		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		// Regular JSON response for non-streaming
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		w.Write(errorJSON)
	}

	// Flush if possible
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// Execute handles request with ultimate model - RAW PROXY
// No retry, no fallback, no loop detection, no buffering.
// On failure: KEEPS retry counter to enforce max retry limit.
// On success: clears retry counter but keeps hash in cache.
// Returns ExecuteResult with usage statistics extracted from the
// response AND the passively-captured assistant content and
// thinking (see ExecuteResult for the capture contract).
// tokenModelID is an optional per-token override for the ultimate model ID (nil = use global config).
func (h *Handler) Execute(
	parentCtx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	originalModelID string,
	hash string,
	headersSent *bool,
	tokenModelID *string,
) (*ExecuteResult, error) {
	cfg := h.config.Get()
	modelID := cfg.UltimateModel.ModelID

	// Get model config from DATABASE
	// Per-token override uses model name (stored by TokenForm), so search by name
	// Global config uses database ID (stored by ProxySettings), so search by ID
	var modelCfg *models.ModelConfig
	if tokenModelID != nil && *tokenModelID != "" {
		// Per-token override - search by ID (TokenForm stores model.id)
		modelID = *tokenModelID
		modelCfg = h.modelsMgr.GetModel(modelID)
	} else {
		// Global config - search by ID
		modelCfg = h.modelsMgr.GetModel(modelID)
	}

	if modelCfg == nil {
		// Model not found - this is a config error, clear everything
		h.hashCache.Remove(hash) // Also clears retry counter
		return nil, &ultimateModelError{
			message:  "ultimate model not found in database",
			internal: false,
		}
	}

	// Set response header to indicate ultimate mode
	w.Header().Set("X-LLMProxy-Ultimate-Model", modelID)
	*headersSent = true

	// Check if streaming
	isStream := false
	if stream, ok := requestBody["stream"].(bool); ok {
		isStream = stream
	}

	// B3 + H4: parse the X-Proxy-Interleaved-Thinking flag here (Handler
	// has r, requestContext does not cross this package boundary). Use the
	// shared helper from pkg/proxyheader for identical semantics with the
	// race path parser at pkg/proxy/handler.go. Empty/missing/wrong-case
	// values resolve to false here too (defence in depth — the gate also
	// rechecks via providerIsMiniMax).
	interleaved := proxyheader.ParseInterleavedThinkingHeaderValue(r.Header.Get(proxyheader.InterleavedThinkingHeader))

	// D3: derive upstream provider from credential when available so the
	// executeExternal gate can short-circuit non-MiniMax paths without
	// touching the request body (H5 invariant). CredentialID empty ⇒
	// upstreamProvider="" ⇒ gate=false (caller of executeExternal treats
	// empty as not-MiniMax).
	var upstreamProvider string
	if modelCfg.CredentialID != "" {
		cred := h.modelsMgr.GetCredential(modelCfg.CredentialID)
		if cred != nil {
			upstreamProvider = strings.ToLower(cred.Provider)
		}
	}

	// Start heartbeat for streaming requests - runs until request ends
	// Heartbeat is started here (after headers are set) to keep connection alive
	var heartbeatCancel context.CancelFunc
	heartbeatCtx, heartbeatCancel := context.WithCancel(parentCtx)
	if isStream {
		go startSSEHeartbeat(w, heartbeatCtx)
	}

	// Apply MaxGenerationTime as the absolute hard timeout
	ctx, cancel := context.WithTimeout(parentCtx, cfg.MaxGenerationTime.Duration())
	defer cancel()

	// Marshal request body for token counting
	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		heartbeatCancel()
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Route to internal or external handler
	var result *ExecuteResult
	if modelCfg.Internal {
		result, err = h.executeInternal(ctx, w, requestBody, requestBodyBytes, modelCfg, isStream, interleaved)
	} else {
		result, err = h.executeExternal(ctx, w, r, requestBody, requestBodyBytes, modelCfg, isStream, interleaved, upstreamProvider)
	}

	// Stop heartbeat
	heartbeatCancel()

	if err != nil {
		// On failure: KEEP retry counter to enforce limit
		// DON'T remove hash - client can retry until MaxRetries exhausted
		log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
		return nil, err
	}

	// On success: remove hash from cache so same content can be processed normally again
	// This also clears the retry counter for the hash
	h.hashCache.Remove(hash)

	return result, nil
}

// ultimateModelError is an error type for ultimate model errors
type ultimateModelError struct {
	message  string
	internal bool
}

func (e *ultimateModelError) Error() string {
	return e.message
}
