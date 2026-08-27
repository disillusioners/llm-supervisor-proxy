package translator

import (
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Incremental Streaming Translator (real-streaming-default plan, Phase 3 / Q2)
//
// Translates OpenAI SSE chunks to Anthropic SSE events as the chunks
// arrive (not batched at end-of-stream). Reuses the same per-chunk
// accumulation primitive (accumulateChunk) and the same wire-shaping
// helpers as TranslateBufferedStream, but emits:
//   - message_start + ping ONCE on the first chunk (even for empty streams,
//     batch parity with stream.go:148-169);
//   - content_block_start / content_block_delta per delta, with a single
//     open block of each kind (text + thinking at most one each open
//     concurrently — deltas route by kind without close-and-reopen);
//   - content_block_stop DEFERRED to Finalize (differs from the batch
//     translator's per-tool close — SDK accepts blocks-close-at-message-end);
//   - message_delta (stop_reason + usage, ALWAYS emitted with zero-usage
//     default) before message_stop, emitted at Finalize;
//   - message_stop at Finalize even when [DONE] was absent
//     (Finalize always emits it; caller writes sendAnthropicSSEError after).
//
// Concurrency: per-request, called serially from the upstream scanner
// loop. NOT safe for concurrent calls on the same instance.
// ─────────────────────────────────────────────────────────────────────────────

// IncrementalStreamTranslator is a per-request stateful translator that
// emits Anthropic SSE events as OpenAI chunks arrive. Constructed in
// the live-mode Anthropic path; absent in buffered mode (the buffered
// mode uses TranslateBufferedStream).
type IncrementalStreamTranslator struct {
	originalModel string
	state         *StreamState

	// Single-open-block-of-each-kind ruling: at most one text block AND
	// one thinking block open concurrently. New deltas of an already-open
	// kind continue the existing block (no close-and-reopen).
	textBlockOpen     bool
	textBlockIndex    int
	thinkingBlockOpen bool
	thinkingBlockIdx  int

	// Per-tool tracking keyed by OpenAI tool_calls[i].index.
	toolBlocksOpen map[int]bool
	toolBlockIndex map[int]int // tool-index → Anthropic block-index
	preIDArgs      map[int]*strings.Builder

	// messageStartSent + pingSent gate double-emission of the preamble.
	messageStartSent bool
	pingSent         bool

	// nextBlockIndex is the next Anthropic content_block index to assign
	// when a new block opens. Starts at 0 and increments on each open.
	nextBlockIndex int

	// finalized guards against double-Finalize.
	finalized bool
}

// NewIncrementalStreamTranslator constructs a per-request incremental
// translator for the given Anthropic client-facing model name (carried
// into message_start). The MessageID is allocated here (matches batch
// translator's allocation timing at stream.go:21-24).
func NewIncrementalStreamTranslator(originalModel string) *IncrementalStreamTranslator {
	return &IncrementalStreamTranslator{
		originalModel:  originalModel,
		state:          newStreamState(originalModel),
		toolBlocksOpen: make(map[int]bool),
		toolBlockIndex: make(map[int]int),
		preIDArgs:      make(map[int]*strings.Builder),
		nextBlockIndex: 0,
	}
}

// newStreamState builds a fresh StreamState pre-populated with the
// allocated MessageID + OriginalModel — same shape as the batch
// translator's preamble at stream.go:21-24.
func newStreamState(originalModel string) *StreamState {
	return &StreamState{
		MessageID:     generateAnthropicMessageID(),
		OriginalModel: originalModel,
	}
}

// State exposes the underlying StreamState for callers that need to
// observe accumulated Content/Thinking/ToolCalls (e.g. the internal
// variant's typed-event entry populates arc.accumulated* from the
// same source). Callers MUST NOT mutate the returned pointer.
func (t *IncrementalStreamTranslator) State() *StreamState {
	return t.state
}

// MessageStartSent returns true after the first ProcessChunk emitted
// the message_start+ping preamble. Useful for tests asserting the
// preamble is gated on the first chunk only.
func (t *IncrementalStreamTranslator) MessageStartSent() bool {
	return t.messageStartSent
}

// emitPreamble emits message_start + ping if not already emitted.
// Returns the events to append, mutates the sent flags.
func (t *IncrementalStreamTranslator) emitPreamble(out *[]string) {
	if !t.messageStartSent {
		*out = append(*out, formatMessageStartEvent(t.state)...)
		t.messageStartSent = true
	}
	if !t.pingSent {
		*out = append(*out, formatPingEvent())
		t.pingSent = true
	}
}

// emitTextDelta opens the text block on first text delta, continues
// the existing block on subsequent deltas per the single-open ruling.
func (t *IncrementalStreamTranslator) emitTextDelta(text string, out *[]string) {
	if !t.textBlockOpen {
		idx := t.nextBlockIndex
		t.nextBlockIndex++
		*out = append(*out, formatContentBlockStart(idx, "text"))
		t.textBlockOpen = true
		t.textBlockIndex = idx
	}
	*out = append(*out, formatContentBlockDelta(t.textBlockIndex, "text_delta", text))
}

// emitThinkingDelta opens the thinking block on first thinking delta,
// continues the existing block on subsequent deltas.
func (t *IncrementalStreamTranslator) emitThinkingDelta(thinking string, out *[]string) {
	if !t.thinkingBlockOpen {
		idx := t.nextBlockIndex
		t.nextBlockIndex++
		*out = append(*out, formatContentBlockStart(idx, "thinking"))
		t.thinkingBlockOpen = true
		t.thinkingBlockIdx = idx
	}
	*out = append(*out, formatThinkingBlockDelta(t.thinkingBlockIdx, thinking))
}

// emitToolDelta routes one OpenAI tool_calls[i] delta into the per-tool
// Anthropic block. Opens a new tool_use block on first id+name arrival;
// buffers argument-only fragments received before id+name (and emits
// them as input_json_delta after the open). If the block is ALREADY open,
// subsequent deltas (with or without id+name) emit input_json_delta
// directly — the buffering only applies pre-open.
func (t *IncrementalStreamTranslator) emitToolDelta(toolIdx int, id, name, args string, out *[]string) {
	if id != "" && name != "" {
		if !t.toolBlocksOpen[toolIdx] {
			idx := t.nextBlockIndex
			t.nextBlockIndex++
			*out = append(*out, formatToolUseBlockStart(idx, id, name))
			t.toolBlocksOpen[toolIdx] = true
			t.toolBlockIndex[toolIdx] = idx
		}
		// Flush any pre-id args buffered for this tool index.
		if buf, ok := t.preIDArgs[toolIdx]; ok && buf.Len() > 0 {
			*out = append(*out, formatInputJsonDelta(t.toolBlockIndex[toolIdx], buf.String()))
			buf.Reset()
		}
		if args != "" {
			*out = append(*out, formatInputJsonDelta(t.toolBlockIndex[toolIdx], args))
		}
		return
	}
	// id+name not in THIS delta — check if the block is already open.
	if t.toolBlocksOpen[toolIdx] {
		// Open block, just a fragment: emit directly as input_json_delta.
		if args != "" {
			*out = append(*out, formatInputJsonDelta(t.toolBlockIndex[toolIdx], args))
		}
		return
	}
	// Block not yet open AND id+name absent: buffer the arguments.
	buf, ok := t.preIDArgs[toolIdx]
	if !ok {
		buf = &strings.Builder{}
		t.preIDArgs[toolIdx] = buf
	}
	buf.WriteString(args)
}

// ProcessChunk accepts a SINGLE raw OpenAI SSE data line (with or
// without the "data: " prefix) and returns the Anthropic SSE events
// to emit for THIS chunk only. The caller drives the upstream scanner
// and is responsible for writing each returned event to w and flushing.
//
// Empty lines / non-data lines / the [DONE] marker / unparseable JSON
// are all silently ignored at the chunk layer (the caller handles
// [DONE] explicitly by calling Finalize).
func (t *IncrementalStreamTranslator) ProcessChunk(rawOpenAIChunk []byte) ([]string, error) {
	if t.finalized {
		return nil, fmt.Errorf("IncrementalStreamTranslator.ProcessChunk called after Finalize")
	}

	var emitted []string

	// Emit the message_start + ping preamble on the FIRST chunk.
	t.emitPreamble(&emitted)

	chunk, isDone, err := ParseOpenAISSEChunk(string(rawOpenAIChunk))
	if err != nil {
		return emitted, err
	}
	if isDone || chunk == nil {
		return emitted, nil
	}

	// Reuse the shared per-chunk accumulator so batch + incremental
	// cannot drift on Content/Thinking/ToolCalls/Usage/StopReason
	// semantics.
	accumulateChunk(chunk, t.state)

	// Route each delta kind through its dedicated emit path.
	choices, _ := chunk["choices"].([]interface{})
	if len(choices) == 0 {
		return emitted, nil
	}
	choice, _ := choices[0].(map[string]interface{})
	if choice == nil {
		return emitted, nil
	}
	delta, _ := choice["delta"].(map[string]interface{})
	if delta == nil {
		return emitted, nil
	}

	// Text delta.
	if content, ok := delta["content"].(string); ok && content != "" {
		t.emitTextDelta(content, &emitted)
	}

	// Thinking delta (both reasoning_content and thinking keys).
	if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
		t.emitThinkingDelta(reasoning, &emitted)
	}
	if thinking, ok := delta["thinking"].(string); ok && thinking != "" {
		t.emitThinkingDelta(thinking, &emitted)
	}

	// Tool call deltas — per-index tracking.
	if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}
			index := 0
			if idx, ok := tcMap["index"].(float64); ok {
				index = int(idx)
			}

			id, _ := tcMap["id"].(string)
			var name, args string
			if function, ok := tcMap["function"].(map[string]interface{}); ok {
				name, _ = function["name"].(string)
				args, _ = function["arguments"].(string)
			}

			t.emitToolDelta(index, id, name, args, &emitted)
		}
	}

	return emitted, nil
}

// ProcessEvent is the typed-event entry used by the internal variant
// (internal_handler.go case "content"/"thinking"/"tool_call") which
// already has a parsed providers.StreamEvent at hand. It mirrors the
// ProcessChunk emit path so wire behavior is identical between the
// two entry points.
//
// content populates text; reasoning/thinking populate the thinking block;
// toolCalls is the OpenAI-shape []interface{} of {index,id,function{...}}
// maps to populate tool_use blocks.
func (t *IncrementalStreamTranslator) ProcessEvent(content, reasoning, thinking string, toolCalls []interface{}) ([]string, error) {
	if t.finalized {
		return nil, fmt.Errorf("IncrementalStreamTranslator.ProcessEvent called after Finalize")
	}

	var emitted []string

	t.emitPreamble(&emitted)

	if content != "" {
		t.emitTextDelta(content, &emitted)
	}
	if reasoning != "" {
		t.emitThinkingDelta(reasoning, &emitted)
	}
	if thinking != "" {
		t.emitThinkingDelta(thinking, &emitted)
	}

	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}
		index := 0
		if idx, ok := tcMap["index"].(float64); ok {
			index = int(idx)
		}

		id, _ := tcMap["id"].(string)
		var name, args string
		if function, ok := tcMap["function"].(map[string]interface{}); ok {
			name, _ = function["name"].(string)
			args, _ = function["arguments"].(string)
		}

		t.emitToolDelta(index, id, name, args, &emitted)
	}

	return emitted, nil
}

// Finalize emits the closing sequence in canonical order:
//   - content_block_stop for every still-open block (text, thinking, tools);
//   - message_delta carrying stop_reason + usage (ALWAYS emitted — zero-usage default);
//   - message_stop (ALWAYS emitted, even when [DONE] was absent).
//
// After Finalize the translator refuses further ProcessChunk/ProcessEvent
// calls.
func (t *IncrementalStreamTranslator) Finalize() ([]string, error) {
	if t.finalized {
		return nil, fmt.Errorf("IncrementalStreamTranslator.Finalize called twice")
	}

	var emitted []string

	// Emit preamble if the translator was Finalize()-d without ever
	// seeing a chunk (batch parity — stream.go:148-169 emits
	// message_start+ping even for an empty stream).
	t.emitPreamble(&emitted)

	// Close any still-open text block.
	if t.textBlockOpen {
		emitted = append(emitted, formatContentBlockStop(t.textBlockIndex))
		t.textBlockOpen = false
	}
	// Close any still-open thinking block.
	if t.thinkingBlockOpen {
		emitted = append(emitted, formatContentBlockStop(t.thinkingBlockIdx))
		t.thinkingBlockOpen = false
	}
	// Close any still-open tool blocks (in block-index order so the
	// wire stops match the opens).
	for idx := 0; idx < t.nextBlockIndex; idx++ {
		for toolIdx, blockIdx := range t.toolBlockIndex {
			if blockIdx == idx && t.toolBlocksOpen[toolIdx] {
				emitted = append(emitted, formatContentBlockStop(idx))
				t.toolBlocksOpen[toolIdx] = false
				break
			}
		}
	}

	// message_delta — ALWAYS emitted, with zero-usage default if no
	// usage chunk was seen (batch parity with stream.go:226-235).
	emitted = append(emitted, formatMessageDeltaEvent(t.state))

	// message_stop — ALWAYS emitted (even without [DONE]).
	emitted = append(emitted, formatMessageStopEvent())

	t.finalized = true
	return emitted, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Preamble / final helpers (single-emit functions mirroring the batch
// translator's inline emit sites at stream.go:148-241)
// ─────────────────────────────────────────────────────────────────────────────

// formatMessageStartEvent returns the message_start SSE event pair
// (single event, two SSE lines). Mirrors stream.go:148-163.
func formatMessageStartEvent(state *StreamState) []string {
	messageStart := map[string]interface{}{
		"type": EventMessageStart,
		"message": map[string]interface{}{
			"id":          state.MessageID,
			"type":        "message",
			"role":        "assistant",
			"content":     []interface{}{},
			"model":       state.OriginalModel,
			"stop_reason": nil,
			"usage": map[string]interface{}{
				"input_tokens":  state.Usage.InputTokens,
				"output_tokens": 0,
			},
		},
	}
	return []string{formatSSEEvent(string(EventMessageStart), messageStart)}
}

// formatPingEvent returns the ping SSE event. Mirrors stream.go:165-169.
func formatPingEvent() string {
	pingEvent := map[string]interface{}{
		"type": EventPing,
	}
	return formatSSEEvent(string(EventPing), pingEvent)
}

// formatMessageDeltaEvent returns the message_delta SSE event carrying
// stop_reason + usage. Mirrors stream.go:226-235 (zero-usage default).
func formatMessageDeltaEvent(state *StreamState) string {
	messageDelta := map[string]interface{}{
		"type": EventMessageDelta,
		"delta": map[string]interface{}{
			"stop_reason": state.StopReason,
		},
		"usage": map[string]interface{}{
			"output_tokens": state.Usage.OutputTokens,
		},
	}
	return formatSSEEvent(string(EventMessageDelta), messageDelta)
}

// formatMessageStopEvent returns the message_stop SSE event.
// Mirrors stream.go:238-241.
func formatMessageStopEvent() string {
	messageStop := map[string]interface{}{
		"type": EventMessageStop,
	}
	return formatSSEEvent(string(EventMessageStop), messageStop)
}
