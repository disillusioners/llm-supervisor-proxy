package ultimatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxyheader"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// triggerAttempts is the hardcoded escalation schedule: request numbers
// (total, per hash) on which the ultimate model is injected.
var triggerAttempts = [5]int{5, 10, 20, 30, 40}

// maxAttempts is the absolute per-hash request limit; attempts beyond it
// receive an un-retryable error.
const maxAttempts = 40

// isTriggerAttempt reports whether the given total attempt count lands on
// one of the schedule milestones (5/10/20/30/40).
func isTriggerAttempt(count int) bool {
	for _, milestone := range triggerAttempts {
		if count == milestone {
			return true
		}
	}
	return false
}

// ShouldTriggerResult contains the result of ShouldTrigger check
type ShouldTriggerResult struct {
	Triggered         bool   // True if ultimate model should be used
	Hash              string // The computed hash
	AttemptsExhausted bool   // True if the per-hash attempt limit was exceeded
	CurrentAttempt    int    // Total attempts recorded for this hash
	MaxAttempts       int    // Absolute per-hash attempt limit (hardcoded)
}

// Handler manages ultimate model requests.
// It detects duplicate requests via message hash and triggers
// the ultimate model as a raw proxy, bypassing all normal logic.
type Handler struct {
	config    config.ManagerInterface
	modelsMgr models.ModelsConfigInterface // Database-backed
	hashCache *HashCache
	eventBus  *events.Bus

	// credEngine (Phase 3 / Task 3 + W-3): injected at construction;
	// consumed by executeInternal's rate-limit failover hook (Task 21).
	// MAY BE NIL: legacy / test paths that don't wire the engine pass
	// nil; executeInternal's hook is gated on h.credEngine != nil.
	credEngine *credentiallb.Engine

	// Tool call buffer configuration
	toolCallBufferMaxSize  int64              // Max size for tool call buffer
	toolCallBufferDisabled bool               // Disable tool call buffering
	toolRepairConfig       *toolrepair.Config // Tool repair config for buffer
}

// NewHandler creates a new ultimate model handler
//
// credEngine is the load-balancing engine injected at construction
// (Phase 3 / Task 3 + W-3 — no second Config injection path; MAY BE NIL).
// The engine is consumed by executeInternal's rate-limit failover hook
// (Task 21) — exclusively; it never participates in policy decisions
// about whether to trigger ultimate (ShouldTrigger/ForceTrigger are
// rate-limit-blind).
func NewHandler(cfg config.ManagerInterface, modelsMgr models.ModelsConfigInterface, eventBus *events.Bus, credEngine *credentiallb.Engine) *Handler {
	maxHash := cfg.Get().UltimateModel.MaxHash
	if maxHash <= 0 {
		maxHash = 100
	}

	return &Handler{
		config:     cfg,
		modelsMgr:  modelsMgr,
		hashCache:  NewHashCache(maxHash),
		eventBus:   eventBus,
		credEngine: credEngine,
	}
}

// ShouldTrigger checks if ultimate model should be triggered.
// Every call records an attempt for the message hash: attempts 1-4 stay in
// the normal flow; the 5th, 10th, 20th, 30th, and 40th requests with the
// same hash trigger the ultimate model; requests beyond the 40th receive
// an un-retryable exhausted error (AttemptsExhausted).
// On ultimate success, the hash is removed from cache (schedule re-arms);
// on failure, the hash stays and counting continues to the next milestone.
func (h *Handler) ShouldTrigger(messages []map[string]interface{}) ShouldTriggerResult {
	return h.shouldTriggerInternal(messages, false)
}

// ForceTrigger always triggers ultimate model, bypassing the schedule.
// Used for testing/debugging via X-Force-Ultimate-Model header.
// Never increments the attempt counter; still stores the hash if new
// (insert-only). A force-seen hash counts as attempt 1 on the next normal
// ShouldTrigger call.
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

	// Force: trigger immediately without consuming an attempt slot. StoreIfAbsent
	// may initialize a missing hash and its counter to 1 (insert-only), but we
	// deliberately return CurrentAttempt=0 so force never claims a schedule slot.
	if force {
		h.hashCache.StoreIfAbsent(hash)
		// CurrentAttempt is deliberately zero: StoreIfAbsent may have
		// initialized the counter to 1, but force must not claim an attempt slot.
		return ShouldTriggerResult{
			Triggered:   true,
			Hash:        hash,
			MaxAttempts: maxAttempts,
		}
	}

	// Normal path: record this request atomically (insert-on-first-sight,
	// increment on every call) and evaluate the hardcoded schedule.
	count := h.hashCache.RecordAttempt(hash)

	// Attempts beyond the cap: exhausted result keeps Triggered=true so the
	// proxy's control flow (exhausted check inside the triggered branch)
	// stays structurally identical to the pre-schedule design.
	if count > maxAttempts {
		return ShouldTriggerResult{
			Triggered:         true,
			Hash:              hash,
			AttemptsExhausted: true,
			CurrentAttempt:    count,
			MaxAttempts:       maxAttempts,
		}
	}

	if isTriggerAttempt(count) {
		return ShouldTriggerResult{
			Triggered:      true,
			Hash:           hash,
			CurrentAttempt: count,
			MaxAttempts:    maxAttempts,
		}
	}

	return ShouldTriggerResult{
		Triggered:      false,
		Hash:           hash,
		CurrentAttempt: count,
		MaxAttempts:    maxAttempts,
	}
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

// getEffectivePrimaryCredentialID (Phase 3 / Task 7) returns the legacy
// single-credential view of the model's credential list: the first ref's
// CredentialID, or "" if the model has no credentials configured. This
// is the single-credential fast path used by the ultimate-external D3
// provider probe (Execute) — the post-Phase-1 modelCfg.CredentialID
// shadow is GONE; Credentials[0].CredentialID is the primary.
//
// Behavior:
//   - nil modelCfg ⇒ "" (defensive; the probe is gated on modelCfg
//     being non-nil in the caller, but the shim keeps the contract
//     safe).
//   - len(modelCfg.Credentials) == 0 ⇒ "".
//   - len(modelCfg.Credentials) >= 1 ⇒ Credentials[0].CredentialID.
//
// modelCfg.PrimaryCredentialID() (defined on models.ModelConfig) is the
// equivalent inline helper — this shim is the standalone replacement
// for the post-Phase-1 ultimatemodel/handler.go:625-635 site that
// previously read modelCfg.CredentialID directly.
func getEffectivePrimaryCredentialID(modelCfg *models.ModelConfig) string {
	if modelCfg == nil {
		return ""
	}
	if len(modelCfg.Credentials) == 0 {
		return ""
	}
	return modelCfg.Credentials[0].CredentialID
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
// currentAttempt/maxAttempt report TOTAL attempts for the hash against the
// hardcoded 40 cap. The wire error type string ("ultimate_model_retry_exhausted")
// and JSON shape are kept unchanged for client compatibility.
func (h *Handler) SendRetryExhaustedError(
	w http.ResponseWriter,
	hash string,
	currentAttempt int,
	maxAttempt int,
	isStream bool,
) error {
	// Safely extract hash prefix (defensive against short/empty hashes)
	hashPrefix := hash
	if len(hash) > 8 {
		hashPrefix = hash[:8]
	}

	message := fmt.Sprintf(
		"Request attempt limit exceeded (attempt %d of %d max). Hash: %s...",
		currentAttempt, maxAttempt, hashPrefix,
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
		errorJSON = []byte(`{"type":"error","error":{"type":"ultimate_model_retry_exhausted","code":"exhausted","message":"Request attempt limit exceeded"}}`)
	}

	// Set headers based on response type FIRST
	if isStream {
		// SSE format for streaming requests
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		// WIRE COMP: retain the legacy "retry-exhausted" header value for external consumers.
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		fmt.Fprintf(w, "data: %s\n\n", string(errorJSON))
		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		// Regular JSON response for non-streaming
		w.Header().Set("Content-Type", "application/json")
		// WIRE COMP: retain the legacy "retry-exhausted" header value for external consumers.
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		w.Write(errorJSON)
	}

	// Flush if possible
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// ExecuteOptions carries per-request wire-mode flags into Execute (and,
// internally, down the execute chain to the stream relay loops).
//
// The zero value — and calling Execute with no options at all — selects
// LIVE streaming, the post-real-streaming-default wire mode: every chunk
// the translator + toolcall-buffer + normalizer chain emits is written to
// the client immediately with a per-chunk flush. BufferMode=true opts
// back into the legacy buffered mode: the full response is accumulated
// and written in a single end-of-stream flush (H8 holds in that mode).
//
// Callers (e.g. pkg/proxy via the X-LLMProxy-Buffer-Response header
// parsed in Phase 1) pass ExecuteOptions{BufferMode: rc.bufferMode};
// absent options mean live, exactly matching the header-absent default.
type ExecuteOptions struct {
	// BufferMode selects the buffered wire mode (single end-of-stream
	// write+flush). false = live per-chunk write-through.
	BufferMode bool

	// writeMu is the per-request write mutex serializing client writes
	// (live per-chunk relay, buffered end-of-stream write, and the SSE
	// heartbeat goroutine) — approver iteration 001, Blocking 2,
	// mirroring the proxy-side rc.writeMu pattern (pkg/proxy/heartbeat.go).
	// It is created INSIDE Execute (resolveExecuteOptions) and threaded
	// down; external callers never set it. Deliberately NOT a Handler
	// field: concurrent Execute invocations must not serialize each
	// other, so the mutex is request-scoped.
	writeMu *sync.Mutex
}

// resolveExecuteOptions derives the effective per-request options.
// Zero options ⇒ live mode with a fresh request-scoped mutex. A
// pre-resolved option (writeMu set by Execute) is propagated verbatim
// so the heartbeat goroutine and the relay loops share ONE mutex.
// Direct calls into the internal chain (tests) resolve their own fresh
// mutex; the mode defaults to live in all cases.
func resolveExecuteOptions(opts []ExecuteOptions) ExecuteOptions {
	if len(opts) > 0 && opts[0].writeMu != nil {
		return opts[0]
	}
	resolved := ExecuteOptions{BufferMode: false, writeMu: &sync.Mutex{}}
	if len(opts) > 0 {
		resolved.BufferMode = opts[0].BufferMode
	}
	return resolved
}

// writeChunk performs ONE live-mode wire write: a writeMu-guarded
// write+flush pair (serialized against the SSE heartbeat goroutine —
// approver iteration 001, Blocking 2). DEADLOCK RULE: never hold
// writeMu across a blocking upstream read or channel receive — the
// lock covers ONLY the write+flush pair, and this helper guarantees
// that shape.
//
// The estimator accumulator (est) receives the SAME bytes in the SAME
// loop iteration as the wire write, so the capture-side accumulator
// cannot drift from the wire by construction (C1 estimator parity —
// NOT an accepted degradation; see plan task 4.2/4.3).
func (opt ExecuteOptions) writeChunk(w http.ResponseWriter, flusher http.Flusher, chunk []byte, est *bytes.Buffer) {
	opt.writeMu.Lock()
	w.Write(chunk)
	flusher.Flush()
	opt.writeMu.Unlock()
	est.Write(chunk)
}

// writeAll performs the buffered-mode end-of-stream single write
// (writeMu-guarded — free correctness against the heartbeat goroutine;
// today's concurrency window is microscopic but the lock costs nothing).
func (opt ExecuteOptions) writeAll(w http.ResponseWriter, flusher http.Flusher, b []byte) {
	opt.writeMu.Lock()
	w.Write(b)
	flusher.Flush()
	opt.writeMu.Unlock()
}

// Execute handles request with ultimate model - RAW PROXY
// No retry, no fallback, no loop detection, no buffering.
// On failure: KEEPS the attempt counter — subsequent requests flow normally
// until the next schedule milestone (5/10/20/30/40).
// On success: removes the hash (counter cleared) so the schedule re-arms
// and the same content starts fresh.
// Returns ExecuteResult with usage statistics extracted from the
// response AND the passively-captured assistant content and
// thinking (see ExecuteResult for the capture contract).
// tokenModelID is an optional per-token override for the ultimate model ID (nil = use global config).
//
// conversationKey (Phase 3 / Task 8) threads the per-request affinity key
// down to executeInternal so the resolution can use the LB engine's
// conversation-sticky weighted-random selection (single source of truth
// for the credential selection on the ultimate-internal path).
//
// opts (Phase 4 / task 4.1) selects the wire mode: absent/zero value =
// live streaming (per-chunk write-through, the post-Phase-4 default);
// ExecuteOptions{BufferMode: true} opts into the legacy buffered mode
// (single end-of-stream write). Execute creates the request-scoped
// writeMu here and shares it with the SSE heartbeat goroutine and both
// live relay loops (approver iteration 001, Blocking 2).
func (h *Handler) Execute(
	parentCtx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	originalModelID string,
	hash string,
	headersSent *bool,
	tokenModelID *string,
	conversationKey string,
	opts ...ExecuteOptions,
) (*ExecuteResult, error) {
	opt := resolveExecuteOptions(opts)

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
		h.hashCache.Remove(hash) // Also clears attempt counter
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
	// touching the request body (H5 invariant). Primary credential ID
	// empty ⇒ upstreamProvider="" ⇒ gate=false (caller of executeExternal
	// treats empty as not-MiniMax).
	var upstreamProvider string
	// Phase 3 / Task 7 — single source of truth for the legacy
	// single-credential view. Post-Phase-1 modelCfg.CredentialID is
	// GONE; Credentials[0].CredentialID is the primary. The shim
	// preserves the len(Credentials) == 0 ⇒ "" semantics.
	primaryCredentialID := getEffectivePrimaryCredentialID(modelCfg)
	if primaryCredentialID != "" {
		cred := h.modelsMgr.GetCredential(primaryCredentialID)
		if cred != nil {
			upstreamProvider = strings.ToLower(cred.Provider)
		}
	}

	// Start heartbeat for streaming requests - runs until request ends
	// Heartbeat is started here (after headers are set) to keep connection alive.
	// Phase 4 (approver iteration 001, Blocking 2): opt.writeMu serializes
	// the heartbeat's write+flush against the live relay loops' per-chunk
	// write+flush — both ultimate live paths share this ONE request-scoped
	// mutex with sendHeartbeat (mirrors pkg/proxy/heartbeat.go's rc.writeMu).
	var heartbeatCancel context.CancelFunc
	heartbeatCtx, heartbeatCancel := context.WithCancel(parentCtx)
	if isStream {
		go startSSEHeartbeat(w, opt.writeMu, heartbeatCtx)
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
		// Phase 3 / Task 21 — route through the rate-limit-failover
		// wrapper (Round 3b review #4 guard fix: initial-call
		// *ProviderError assertion, NOT headersSent). The wrapper owns
		// the SINGLE affinity resolution (Tasks 8+9): attempt #1's
		// credential is exactly what executeInternalWithResolved
		// consumes, and the retry resolves the SPECIFIC reselected
		// credential — no double resolution, no fresh re-roll.
		result, err = h.executeInternalWithRateLimitFailover(
			ctx, w, requestBody, requestBodyBytes, modelCfg, isStream, interleaved,
			conversationKey, opt,
		)
		// Phase 3 / Task 24 (Round 3 — R3-7 scope guard): ultimate-EXTERNAL
		// passthrough is UNCHANGED by credential load-balancing — provider
		// detection only (getEffectivePrimaryCredentialID shim, Task 7);
		// client auth passes through; no credential switching, no
		// ExcludeAndReselect, no rate-limit failover hook on this branch.
		// The failover family is the four LB'd INTERNAL paths only
		// (race-internal, ultimate-internal, /v1/messages internal,
		// anthropic-passthrough internal branch).
	} else {
		result, err = h.executeExternal(ctx, w, r, requestBody, requestBodyBytes, modelCfg, isStream, interleaved, upstreamProvider, opt)
	}
	// Stop heartbeat
	heartbeatCancel()

	if err != nil {
		// On failure: KEEP the attempt counter — subsequent requests flow
		// normally until the next schedule milestone (5/10/20/30/40)
		log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
		return nil, err
	}

	// On success: remove hash from cache so the schedule re-arms and the
	// same content starts fresh. This also clears the attempt counter.
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
