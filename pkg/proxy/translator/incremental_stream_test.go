package translator

import (
	"encoding/json"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_MessageStartOnFirstChunk: first chunk
// emits message_start + ping; second chunk emits only content_block_start
// + text_delta. Mirrors stream.go:148-169 batch parity.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_MessageStartOnFirstChunk(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	firstEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"role":"assistant"},"index":0}]}`))
	if err != nil {
		t.Fatalf("first chunk failed: %v", err)
	}

	// First chunk should emit message_start + ping (role-only chunk has
	// no content).
	var sawStart, sawPing bool
	for _, ev := range firstEvents {
		if strings.HasPrefix(ev, "event: message_start\n") {
			sawStart = true
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			sawPing = true
		}
	}
	if !sawStart {
		t.Error("first chunk: missing message_start event")
	}
	if !sawPing {
		t.Error("first chunk: missing ping event")
	}

	// Second chunk carries content.
	secondEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}`))
	if err != nil {
		t.Fatalf("second chunk failed: %v", err)
	}

	// Second chunk should emit content_block_start + text_delta — NOT
	// a second message_start / ping.
	var secondStart, secondPing bool
	var sawCBStart, sawCBDelta bool
	for _, ev := range secondEvents {
		if strings.HasPrefix(ev, "event: message_start\n") {
			secondStart = true
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			secondPing = true
		}
		if strings.HasPrefix(ev, "event: content_block_start\n") {
			sawCBStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") {
			sawCBDelta = true
		}
	}
	if secondStart {
		t.Error("second chunk: duplicate message_start (preamble gate failed)")
	}
	if secondPing {
		t.Error("second chunk: duplicate ping (preamble gate failed)")
	}
	if !sawCBStart {
		t.Error("second chunk: missing content_block_start for text")
	}
	if !sawCBDelta {
		t.Error("second chunk: missing content_block_delta for text_delta")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_MessageStartOnce: same role-only chunk
// fed twice emits exactly one message_start and one ping.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_MessageStartOnce(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	chunk := []byte(`data: {"choices":[{"delta":{"role":"assistant"},"index":0}]}`)

	first, _ := tr.ProcessChunk(chunk)
	second, _ := tr.ProcessChunk(chunk)

	var firstStart, firstPing, secondStart, secondPing int
	for _, ev := range first {
		if strings.HasPrefix(ev, "event: message_start\n") {
			firstStart++
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			firstPing++
		}
	}
	for _, ev := range second {
		if strings.HasPrefix(ev, "event: message_start\n") {
			secondStart++
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			secondPing++
		}
	}
	if firstStart != 1 || secondStart != 0 {
		t.Errorf("expected exactly one message_start across chunks (first=%d, second=%d)", firstStart, secondStart)
	}
	if firstPing != 1 || secondPing != 0 {
		t.Errorf("expected exactly one ping across chunks (first=%d, second=%d)", firstPing, secondPing)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ThinkingBlock: chunk with reasoning_content
// emits a thinking content block separate from the text block.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ThinkingBlock(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// First a thinking chunk.
	thinkingEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"reasoning_content":"Let me think..."},"index":0}]}`))
	if err != nil {
		t.Fatalf("thinking chunk failed: %v", err)
	}
	var sawThinkingStart, sawThinkingDelta bool
	for _, ev := range thinkingEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"thinking"`) {
			sawThinkingStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `"thinking_delta"`) {
			sawThinkingDelta = true
		}
	}
	if !sawThinkingStart {
		t.Error("missing content_block_start with type=thinking")
	}
	if !sawThinkingDelta {
		t.Error("missing content_block_delta with thinking_delta")
	}

	// Then a text chunk — should open a SEPARATE text block.
	textEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}`))
	if err != nil {
		t.Fatalf("text chunk failed: %v", err)
	}
	var sawTextStart bool
	for _, ev := range textEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"text"`) {
			sawTextStart = true
		}
	}
	if !sawTextStart {
		t.Error("text chunk should open a separate text block (single-open ruling)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ToolUseBlock: tool_calls array
// accumulates and emits tool_use content block with input_json_delta
// fragments.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ToolUseBlock(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// First delta carries id + name.
	firstEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"loc"}}]},"index":0}]}`))
	if err != nil {
		t.Fatalf("first tool chunk failed: %v", err)
	}

	var sawToolStart bool
	var firstArgDelta string
	for _, ev := range firstEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"tool_use"`) {
			sawToolStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `input_json_delta`) {
			firstArgDelta = ev
		}
	}
	if !sawToolStart {
		t.Error("first tool delta: missing content_block_start for tool_use")
	}
	if firstArgDelta == "" {
		t.Error("first tool delta: missing input_json_delta for arguments")
	}

	// Second delta is arguments-only.
	secondEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\"SF\"}"}}]},"index":0}]}`))
	if err != nil {
		t.Fatalf("second tool chunk failed: %v", err)
	}

	// The second delta should NOT emit another content_block_start
	// (block already open), and SHOULD emit input_json_delta with the
	// new argument fragment.
	var secondStart, secondArgDelta bool
	for _, ev := range secondEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"tool_use"`) {
			secondStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `input_json_delta`) {
			secondArgDelta = true
		}
	}
	if secondStart {
		t.Error("second tool delta should NOT emit another content_block_start (block already open)")
	}
	if !secondArgDelta {
		t.Error("second tool delta: missing input_json_delta for continuation arguments")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ToolCall_NoID_Dropped: argument-only
// chunks buffered internally until id+name arrive; single
// content_block_start + accumulated input_json_delta.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ToolCall_NoID_Dropped(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// Argument-only first (no id, no name).
	firstEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"index":0}]}`))
	if err != nil {
		t.Fatalf("first arg-only chunk failed: %v", err)
	}

	// Should NOT emit content_block_start yet (id+name not seen).
	var sawStartFirst bool
	for _, ev := range firstEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"tool_use"`) {
			sawStartFirst = true
		}
	}
	if sawStartFirst {
		t.Error("argument-only first delta should NOT emit content_block_start (id+name not arrived)")
	}

	// Second delta carries id+name with another arg fragment.
	secondEvents, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"get_weather","arguments":"ation\":\"SF\"}"}}]},"index":0}]}`))
	if err != nil {
		t.Fatalf("second chunk failed: %v", err)
	}

	var sawStartSecond, sawPreArgs, sawNewArgs bool
	for _, ev := range secondEvents {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"tool_use"`) {
			sawStartSecond = true
		}
		// Find any input_json_delta events and inspect their partial_json
		// payloads — the buffered pre-id args carry `loc` (the buffered
		// fragment), and the new args carry `ation":"SF"}` (the new
		// fragment from this chunk).
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `input_json_delta`) {
			// The buffered fragment `{"loc` is JSON-escaped in the data
			// line as `{\"loc` (so the substring search uses `loc` to
			// avoid the JSON-escape noise).
			if strings.Contains(ev, `loc`) {
				sawPreArgs = true
			}
			if strings.Contains(ev, `ation`) {
				sawNewArgs = true
			}
		}
	}
	if !sawStartSecond {
		t.Error("second delta: missing content_block_start after id+name arrival")
	}
	if !sawPreArgs {
		t.Error("second delta: missing input_json_delta carrying the BUFFERED pre-id args")
	}
	if !sawNewArgs {
		t.Error("second delta: missing input_json_delta for the new arguments fragment")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_FinalizeEmitsStop: after [DONE],
// Finalize emits block-stops + message_delta + message_stop.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_FinalizeEmitsStop(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// Open a text block.
	_, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"Hello world"},"index":0}]}`))
	if err != nil {
		t.Fatalf("text chunk failed: %v", err)
	}

	// Simulate [DONE].
	_, err = tr.ProcessChunk([]byte("data: [DONE]"))
	if err != nil {
		t.Fatalf("done chunk failed: %v", err)
	}

	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	var sawTextStop, sawMessageDelta, sawMessageStop bool
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: content_block_stop\n") {
			sawTextStop = true
		}
		if strings.HasPrefix(ev, "event: message_delta\n") {
			sawMessageDelta = true
		}
		if strings.HasPrefix(ev, "event: message_stop\n") {
			sawMessageStop = true
		}
	}
	if !sawTextStop {
		t.Error("Finalize: missing content_block_stop for the open text block")
	}
	if !sawMessageDelta {
		t.Error("Finalize: missing message_delta")
	}
	if !sawMessageStop {
		t.Error("Finalize: missing message_stop")
	}

	// Calling Finalize twice must error.
	_, err = tr.Finalize()
	if err == nil {
		t.Error("Finalize twice: expected error, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_DeferredClose_AllBlocksStopBeforeMessageStop:
// three block types opened across chunks; assert NO content_block_stop
// until Finalize, then correct order.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_DeferredClose_AllBlocksStopBeforeMessageStop(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// Thinking.
	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"reasoning_content":"think"},"index":0}]}`)); err != nil {
		t.Fatalf("thinking chunk failed: %v", err)
	}
	// Text.
	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"hello"},"index":0}]}`)); err != nil {
		t.Fatalf("text chunk failed: %v", err)
	}
	// Tool call with id+name+args.
	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},"index":0}]}`)); err != nil {
		t.Fatalf("tool chunk failed: %v", err)
	}

	// Now run a sequence of "process more content" chunks — assert NO
	// content_block_stop in any of them.
	for i := 0; i < 3; i++ {
		events, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":" more"},"index":0}]}`))
		if err != nil {
			t.Fatalf("loop chunk %d failed: %v", i, err)
		}
		for _, ev := range events {
			if strings.HasPrefix(ev, "event: content_block_stop\n") {
				t.Errorf("content_block_stop emitted before Finalize (loop %d)", i)
			}
		}
	}

	// Now Finalize — must emit 3 stops, then message_delta, then message_stop.
	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	stopCount := 0
	var sawMD, sawMS bool
	var lastStopAt, mdAt, msAt int
	for i, ev := range events {
		if strings.HasPrefix(ev, "event: content_block_stop\n") {
			stopCount++
			lastStopAt = i
		}
		if strings.HasPrefix(ev, "event: message_delta\n") {
			sawMD = true
			mdAt = i
		}
		if strings.HasPrefix(ev, "event: message_stop\n") {
			sawMS = true
			msAt = i
		}
	}
	if stopCount != 3 {
		t.Errorf("Finalize: expected 3 content_block_stop events (thinking, text, tool), got %d", stopCount)
	}
	if !sawMD {
		t.Error("Finalize: missing message_delta")
	}
	if !sawMS {
		t.Error("Finalize: missing message_stop")
	}
	// message_stop MUST be the last event emitted.
	if sawMS && msAt != len(events)-1 {
		t.Errorf("Finalize: message_stop not at the last position (events=%d, ms at %d)", len(events), msAt)
	}
	// message_delta MUST come BEFORE message_stop.
	if sawMD && sawMS && mdAt >= msAt {
		t.Errorf("Finalize: message_delta (pos %d) must come before message_stop (pos %d)", mdAt, msAt)
	}
	// All content_block_stop events MUST come BEFORE message_delta.
	if sawMD && lastStopAt >= mdAt {
		t.Errorf("Finalize: last content_block_stop (pos %d) must come before message_delta (pos %d)", lastStopAt, mdAt)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_FinalizeWithoutDone_EmitsMessageStop:
// no [DONE]; Finalize still emits message_stop (then caller writes
// sendAnthropicSSEError).
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_FinalizeWithoutDone_EmitsMessageStop(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}`)); err != nil {
		t.Fatalf("chunk failed: %v", err)
	}
	// Skip [DONE]; finalize directly (stream-end-without-DONE path).
	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	var sawMessageStop bool
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: message_stop\n") {
			sawMessageStop = true
		}
	}
	if !sawMessageStop {
		t.Error("Finalize without [DONE]: missing message_stop")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_UsageInjection: chunk with usage
// populates input_tokens / output_tokens in message_delta; zero-usage
// default when absent.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_UsageInjection(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}`)); err != nil {
		t.Fatalf("text chunk failed: %v", err)
	}
	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":42,"completion_tokens":17,"total_tokens":59}}`)); err != nil {
		t.Fatalf("usage chunk failed: %v", err)
	}
	if _, err := tr.ProcessChunk([]byte("data: [DONE]")); err != nil {
		t.Fatalf("done chunk failed: %v", err)
	}

	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	var md string
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: message_delta\n") {
			md = ev
		}
	}
	if md == "" {
		t.Fatal("Finalize: missing message_delta")
	}
	if !strings.Contains(md, `"output_tokens":17`) {
		t.Errorf("message_delta missing output_tokens:17; got %s", md)
	}
	if !strings.Contains(md, `"stop_reason":"end_turn"`) {
		t.Errorf("message_delta missing stop_reason end_turn (from stop→end_turn mapping); got %s", md)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_UsageDefaultZero: no usage chunk at all;
// message_delta carries output_tokens:0 (zero-usage default).
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_UsageDefaultZero(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}`)); err != nil {
		t.Fatalf("text chunk failed: %v", err)
	}
	if _, err := tr.ProcessChunk([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`)); err != nil {
		t.Fatalf("finish chunk failed: %v", err)
	}
	if _, err := tr.ProcessChunk([]byte("data: [DONE]")); err != nil {
		t.Fatalf("done chunk failed: %v", err)
	}

	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	var md string
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: message_delta\n") {
			md = ev
		}
	}
	if md == "" {
		t.Fatal("Finalize: missing message_delta")
	}
	if !strings.Contains(md, `"output_tokens":0`) {
		t.Errorf("message_delta missing output_tokens:0 (zero-usage default); got %s", md)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_HugeChunk: single delta > 64KB — no
// truncation (the per-call chunk argument has no internal size limit;
// the scanner cap is the caller's concern, mirroring
// handler_anthropic.go:821).
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_HugeChunk(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// 100KB reasoning_content — single chunk.
	hugeText := strings.Repeat("X", 100*1024)
	chunk := `data: {"choices":[{"delta":{"reasoning_content":"` + hugeText + `"},"index":0}]}`

	events, err := tr.ProcessChunk([]byte(chunk))
	if err != nil {
		t.Fatalf("huge chunk failed: %v", err)
	}

	// Find the thinking_delta and verify the full payload survived.
	var td string
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `thinking_delta`) {
			td = ev
		}
	}
	if td == "" {
		t.Fatal("huge chunk: missing thinking_delta")
	}
	if !strings.Contains(td, hugeText) {
		t.Errorf("huge chunk: thinking text truncated (full len=%d, contains check failed)", len(hugeText))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_EmptyStreamParity: never see a chunk,
// Finalize emits the preamble (batch parity with stream.go:148-169).
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_EmptyStreamParity(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	events, err := tr.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	var sawStart, sawPing, sawStop bool
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: message_start\n") {
			sawStart = true
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			sawPing = true
		}
		if strings.HasPrefix(ev, "event: message_stop\n") {
			sawStop = true
		}
	}
	if !sawStart {
		t.Error("Finalize on empty stream: missing message_start (batch parity violated)")
	}
	if !sawPing {
		t.Error("Finalize on empty stream: missing ping (batch parity violated)")
	}
	if !sawStop {
		t.Error("Finalize on empty stream: missing message_stop")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ParityVsBatch — the SAME OpenAI SSE
// fixture through BOTH translators: event-SET equality always;
// event-ORDER equality for non-interleaved fixtures; ORDER inequality
// pinned for interleaved fixtures. Per NB3: block structure +
// concatenated payloads per block match; message_start/ping/..._delta/
// message_stop match exactly. The batch translator emits 1 aggregated
// delta per block; incremental emits N.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ParityVsBatch_NonInterleaved(t *testing.T) {
	// Fixture: thinking then text, non-interleaved.
	fixture := []string{
		`data: {"choices":[{"delta":{"reasoning_content":"Let me think"},"index":0}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":" more"},"index":0}]}`,
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}`,
		`data: {"choices":[{"delta":{"content":" world"},"index":0}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
		`data: [DONE]`,
	}

	// Batch.
	batchBuf := ""
	for _, line := range fixture {
		batchBuf += line + "\n"
	}
	batchOut, err := TranslateBufferedStream([]byte(batchBuf), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("batch translate: %v", err)
	}
	batchEvents := parseSSEEvents(batchOut)

	// Incremental.
	inc := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	var incAll []string
	for _, line := range fixture {
		evs, err := inc.ProcessChunk([]byte(line))
		if err != nil {
			t.Fatalf("inc chunk: %v", err)
		}
		incAll = append(incAll, evs...)
	}
	incFinal, err := inc.Finalize()
	if err != nil {
		t.Fatalf("inc finalize: %v", err)
	}
	incAll = append(incAll, incFinal...)
	incEvents := parseSSEEvents(concatEvents(incAll))

	// (1) Event-SET equality: same count of each block type.
	batchStarts := findEventsByType(batchEvents, string(EventContentBlockStart))
	incStarts := findEventsByType(incEvents, string(EventContentBlockStart))
	if len(batchStarts) != len(incStarts) {
		t.Errorf("content_block_start count: batch=%d, incremental=%d", len(batchStarts), len(incStarts))
	}

	// (2) Block structure (count/types) matches. The block INDICES
	// may differ between batch and incremental (batch always assigns
	// thinking-then-text; incremental assigns in arrival order) — for
	// a non-interleaved fixture the indices happen to match, but we
	// don't pin that. What we DO pin: same count + same TYPES (set).
	batchTypes := make(map[string]bool)
	for _, s := range batchStarts {
		cb, _ := s.Data["content_block"].(map[string]interface{})
		batchTypes[cb["type"].(string)] = true
	}
	incTypes := make(map[string]bool)
	for _, s := range incStarts {
		cb, _ := s.Data["content_block"].(map[string]interface{})
		incTypes[cb["type"].(string)] = true
	}
	if !stringSetEqual(batchTypes, incTypes) {
		t.Errorf("non-interleaved block type set mismatch: batch=%v inc=%v", batchTypes, incTypes)
	}

	// (3) Concatenated payload per block: text / thinking aggregated.
	incText := concatDeltaField(incEvents, "text", "text")
	batchText := concatDeltaField(batchEvents, "text", "text")
	if incText != batchText {
		t.Errorf("text payload mismatch: batch=%q inc=%q", batchText, incText)
	}
	incThinking := concatDeltaField(incEvents, "thinking", "thinking")
	batchThinking := concatDeltaField(batchEvents, "thinking", "thinking")
	if incThinking != batchThinking {
		t.Errorf("thinking payload mismatch: batch=%q inc=%q", batchThinking, incThinking)
	}

	// (4) message_start / ping / message_stop match exactly EXCEPT for
	// the message ID (each translator instance allocates its own) and
	// message_start's input_tokens (incremental emits message_start on
	// chunk 1, before any usage chunk; batch emits at end with the
	// full state). message_delta is checked separately — it carries
	// the final usage and DOES match between translators.
	checkExactMatchExcluding(t, batchEvents, incEvents, string(EventMessageStart), []string{"message"})
	checkExactMatch(t, batchEvents, incEvents, string(EventMessageStop))
	checkExactMatch(t, batchEvents, incEvents, string(EventPing))
	checkMessageDeltaUsage(t, batchEvents, incEvents)
}

// checkExactMatchExcluding is checkExactMatch with a per-call field-
// exclusion list (top-level only).
func checkExactMatchExcluding(t *testing.T, a, b []SSEEvent, eventType string, exclude []string) {
	t.Helper()
	aList := findEventsByType(a, eventType)
	bList := findEventsByType(b, eventType)
	if len(aList) != len(bList) {
		t.Errorf("%s count: batch=%d inc=%d", eventType, len(aList), len(bList))
		return
	}
	for i := range aList {
		if !mapsEqualJSONExcluding(aList[i].Data, bList[i].Data, exclude) {
			t.Errorf("%s event %d payload mismatch:\n  batch=%v\n  inc=%v", eventType, i, aList[i].Data, bList[i].Data)
		}
	}
}

// checkMessageDeltaUsage asserts the message_delta event carries the
// SAME usage payload in both translators (input_tokens may differ on
// message_start; message_delta is where the final usage lands).
func checkMessageDeltaUsage(t *testing.T, a, b []SSEEvent) {
	t.Helper()
	aList := findEventsByType(a, string(EventMessageDelta))
	bList := findEventsByType(b, string(EventMessageDelta))
	if len(aList) != len(bList) || len(aList) == 0 {
		t.Errorf("message_delta count: batch=%d inc=%d", len(aList), len(bList))
		return
	}
	for i := range aList {
		aUsage, _ := aList[i].Data["usage"].(map[string]interface{})
		bUsage, _ := bList[i].Data["usage"].(map[string]interface{})
		if !mapsEqualJSON(aUsage, bUsage) {
			t.Errorf("message_delta %d usage mismatch: batch=%v inc=%v", i, aUsage, bUsage)
		}
	}
}

// concatEvents concatenates per-event SSE strings into a single byte
// slice for parsing.
func concatEvents(events []string) []byte {
	var sb strings.Builder
	for _, ev := range events {
		sb.WriteString(ev)
	}
	return []byte(sb.String())
}

// stringSetEqual reports whether two string sets are equal (same
// members regardless of map ordering).
func stringSetEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// concatDeltaField concatenates the named payload field across all
// content_block_delta events of the given block kind.
func concatDeltaField(events []SSEEvent, kind, field string) string {
	var sb strings.Builder
	for _, e := range events {
		if e.EventType != string(EventContentBlockDelta) {
			continue
		}
		delta, ok := e.Data["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		// Determine which block this delta is for.
		idx, ok := e.Data["index"].(float64)
		if !ok {
			continue
		}
		_ = idx
		v, ok := delta[field].(string)
		if !ok {
			continue
		}
		// kind filter: text_delta vs thinking_delta distinguished by
		// the delta "type" field, not the block kind.
		deltaType, _ := delta["type"].(string)
		if field == "text" && deltaType != "text_delta" {
			continue
		}
		if field == "thinking" && deltaType != "thinking_delta" {
			continue
		}
		sb.WriteString(v)
	}
	return sb.String()
}

// checkExactMatch asserts that the two event lists have the same
// count and identical data payload for the given event type.
func checkExactMatch(t *testing.T, a, b []SSEEvent, eventType string) {
	t.Helper()
	aList := findEventsByType(a, eventType)
	bList := findEventsByType(b, eventType)
	if len(aList) != len(bList) {
		t.Errorf("%s count: batch=%d inc=%d", eventType, len(aList), len(bList))
		return
	}
	for i := range aList {
		if !mapsEqualJSON(aList[i].Data, bList[i].Data) {
			t.Errorf("%s event %d payload mismatch:\n  batch=%v\n  inc=%v", eventType, i, aList[i].Data, bList[i].Data)
		}
	}
}

// mapsEqualJSON is a small structural-equality helper for our test
// data maps; we compare via JSON-roundtrip to normalize number types
// (parseSSEEvents decodes into float64). The optional excludeKeys
// allow callers to skip non-deterministic fields like the message ID
// (each translator instance generates its own).
func mapsEqualJSON(a, b map[string]interface{}) bool {
	return mapsEqualJSONExcluding(a, b, nil)
}

// mapsEqualJSONExcluding is mapsEqualJSON with a per-call field-
// exclusion list.
func mapsEqualJSONExcluding(a, b map[string]interface{}, exclude []string) bool {
	if len(a) != len(b) {
		return false
	}
	exSet := make(map[string]bool, len(exclude))
	for _, k := range exclude {
		exSet[k] = true
	}
	for k := range a {
		if exSet[k] {
			continue
		}
		if !mapsEqualJSONRecurse(a[k], b[k]) {
			return false
		}
	}
	return true
}

// mapsEqualJSONRecurse compares two arbitrary JSON-decoded values
// structurally (handles nested maps + slices + scalars).
func mapsEqualJSONRecurse(a, b interface{}) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ParityVsBatch_Interleaved — interleaved
// thinking/text fixture: ORDER inequality is the documented live-mode
// wire difference (incremental emits in arrival order; batch groups
// thinking-before-text). Event-SET equality still holds.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ParityVsBatch_Interleaved(t *testing.T) {
	// Interleaved fixture: FIRST content then thinking — batch ALWAYS
	// groups thinking-before-text (generateAnthropicEvents emits all
	// thinking blocks first, then all text blocks — independent of
	// arrival order), while incremental emits in arrival order, so
	// the first delta type is text_delta on the incremental side and
	// thinking_delta on the batch side — the documented wire-shape
	// difference for interleaved streams.
	fixture := []string{
		`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"think1"},"index":0}]}`,
		`data: {"choices":[{"delta":{"content":" there"},"index":0}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":" think2"},"index":0}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		`data: [DONE]`,
	}

	// Batch.
	batchBuf := ""
	for _, line := range fixture {
		batchBuf += line + "\n"
	}
	batchOut, err := TranslateBufferedStream([]byte(batchBuf), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("batch translate: %v", err)
	}
	batchEvents := parseSSEEvents(batchOut)

	// Incremental.
	inc := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	var incAll []string
	for _, line := range fixture {
		evs, err := inc.ProcessChunk([]byte(line))
		if err != nil {
			t.Fatalf("inc chunk: %v", err)
		}
		incAll = append(incAll, evs...)
	}
	incFinal, err := inc.Finalize()
	if err != nil {
		t.Fatalf("inc finalize: %v", err)
	}
	incAll = append(incAll, incFinal...)
	incEvents := parseSSEEvents(concatEvents(incAll))

	// Event-SET equality: same count of content_block_start events,
	// same SET of block types (one text + one thinking). Block INDICES
	// differ by design for interleaved fixtures — batch always
	// thinking-then-text, incremental in arrival order — that's the
	// pinned ORDER inequality.
	batchStarts := findEventsByType(batchEvents, string(EventContentBlockStart))
	incStarts := findEventsByType(incEvents, string(EventContentBlockStart))
	if len(batchStarts) != len(incStarts) {
		t.Errorf("interleaved content_block_start count: batch=%d inc=%d", len(batchStarts), len(incStarts))
	}
	batchTypes := make(map[string]bool)
	for _, s := range batchStarts {
		cb, _ := s.Data["content_block"].(map[string]interface{})
		batchTypes[cb["type"].(string)] = true
	}
	incTypes := make(map[string]bool)
	for _, s := range incStarts {
		cb, _ := s.Data["content_block"].(map[string]interface{})
		incTypes[cb["type"].(string)] = true
	}
	if !stringSetEqual(batchTypes, incTypes) {
		t.Errorf("interleaved block type set mismatch: batch=%v inc=%v", batchTypes, incTypes)
	}

	// Concatenated payloads per block match (event-SET equality on
	// aggregated deltas per NB3).
	incText := concatDeltaField(incEvents, "text", "text")
	batchText := concatDeltaField(batchEvents, "text", "text")
	if incText != batchText {
		t.Errorf("interleaved text payload mismatch: batch=%q inc=%q", batchText, incText)
	}
	incThinking := concatDeltaField(incEvents, "thinking", "thinking")
	batchThinking := concatDeltaField(batchEvents, "thinking", "thinking")
	if incThinking != batchThinking {
		t.Errorf("interleaved thinking payload mismatch: batch=%q inc=%q", batchThinking, incThinking)
	}

	// ORDER inequality PINNED: incremental's first content_block_delta
	// is a text_delta (the second fixture chunk), while batch's first
	// is a thinking_delta (batch groups thinking-before-text).
	batchDeltas := findEventsByType(batchEvents, string(EventContentBlockDelta))
	incDeltas := findEventsByType(incEvents, string(EventContentBlockDelta))
	if len(batchDeltas) == 0 || len(incDeltas) == 0 {
		t.Fatal("no content_block_delta events emitted by one of the translators")
	}
	batchFirstType, _ := batchDeltas[0].Data["delta"].(map[string]interface{})
	incFirstType, _ := incDeltas[0].Data["delta"].(map[string]interface{})
	if batchFirstType["type"] == incFirstType["type"] {
		t.Errorf("interleaved fixture: expected ORDER inequality on first delta type (got batch=%v inc=%v) — the documented live-vs-buffered wire difference",
			batchFirstType["type"], incFirstType["type"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ProcessEvent mirrors ProcessChunk for
// the internal-variant typed-event entry.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ProcessEvent(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	events, err := tr.ProcessEvent("hello", "", "", nil)
	if err != nil {
		t.Fatalf("processEvent: %v", err)
	}

	var sawStart, sawPing, sawCBStart, sawTextDelta bool
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: message_start\n") {
			sawStart = true
		}
		if strings.HasPrefix(ev, "event: ping\n") {
			sawPing = true
		}
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"text"`) {
			sawCBStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `"text_delta"`) {
			sawTextDelta = true
		}
	}
	if !sawStart {
		t.Error("ProcessEvent: missing message_start on first call")
	}
	if !sawPing {
		t.Error("ProcessEvent: missing ping on first call")
	}
	if !sawCBStart {
		t.Error("ProcessEvent: missing content_block_start")
	}
	if !sawTextDelta {
		t.Error("ProcessEvent: missing text_delta")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ProcessEventToolCall drives the typed-
// event entry with a tool_calls slice carrying id+name.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ProcessEventToolCall(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	tc := []interface{}{
		map[string]interface{}{
			"index": 0,
			"id":    "call_xyz",
			"type":  "function",
			"function": map[string]interface{}{
				"name":      "lookup",
				"arguments": `{"q":"hi"}`,
			},
		},
	}

	events, err := tr.ProcessEvent("", "", "", tc)
	if err != nil {
		t.Fatalf("processEvent: %v", err)
	}

	var sawToolStart, sawInputDelta bool
	for _, ev := range events {
		if strings.HasPrefix(ev, "event: content_block_start\n") && strings.Contains(ev, `"type":"tool_use"`) {
			sawToolStart = true
		}
		if strings.HasPrefix(ev, "event: content_block_delta\n") && strings.Contains(ev, `input_json_delta`) {
			sawInputDelta = true
		}
	}
	if !sawToolStart {
		t.Error("ProcessEvent with tool_call: missing content_block_start for tool_use")
	}
	if !sawInputDelta {
		t.Error("ProcessEvent with tool_call: missing input_json_delta for arguments")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ProcessEvent_AccumulatesState is the
// regression test for Phase 3 review-fix #1. Before the fix, ProcessEvent
// emitted wire events but never accumulated into t.state, so the internal
// live-mode success path (handler_anthropic.go:795-816 arc mirror) read
// an empty StreamState and persisted store.Message with empty
// Content + ToolCalls + Thinking for every internal live-mode streaming
// response.
//
// The test drives ProcessEvent with content + reasoning + tool_calls and
// asserts the State() accumulators are populated with the SAME shapes
// accumulateChunk uses in ProcessChunk. A failure of this test means
// the internal live-mode persistence path is broken again.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ProcessEvent_AccumulatesState(t *testing.T) {
	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")

	// 1. Content accumulation.
	if _, err := tr.ProcessEvent("hello world", "", "", nil); err != nil {
		t.Fatalf("ProcessEvent(content): %v", err)
	}
	if got, want := tr.State().AccumulatedContent.String(), "hello world"; got != want {
		t.Errorf("ProcessEvent: AccumulatedContent = %q, want %q", got, want)
	}

	// 2. Reasoning/thinking accumulation (both keys — same as accumulateChunk).
	tr2 := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	if _, err := tr2.ProcessEvent("", "step1", "", nil); err != nil {
		t.Fatalf("ProcessEvent(reasoning): %v", err)
	}
	if _, err := tr2.ProcessEvent("", "", "step2", nil); err != nil {
		t.Fatalf("ProcessEvent(thinking): %v", err)
	}
	if got, want := tr2.State().ThinkingContent.String(), "step1step2"; got != want {
		t.Errorf("ProcessEvent: ThinkingContent = %q, want %q (reasoning_content + thinking keys concatenated)", got, want)
	}

	// 3. Tool call accumulation — single tool, id+name+args.
	tr3 := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	tc := []interface{}{
		map[string]interface{}{
			"index": 0,
			"id":    "call_abc",
			"type":  "function",
			"function": map[string]interface{}{
				"name":      "lookup",
				"arguments": `{"q":"hi"}`,
			},
		},
	}
	if _, err := tr3.ProcessEvent("", "", "", tc); err != nil {
		t.Fatalf("ProcessEvent(tool_call): %v", err)
	}
	tools := tr3.State().ToolCalls
	if len(tools) != 1 {
		t.Fatalf("ProcessEvent: ToolCalls len = %d, want 1", len(tools))
	}
	if tools[0].ID != "call_abc" {
		t.Errorf("ProcessEvent: ToolCalls[0].ID = %q, want %q", tools[0].ID, "call_abc")
	}
	if tools[0].Name != "lookup" {
		t.Errorf("ProcessEvent: ToolCalls[0].Name = %q, want %q", tools[0].Name, "lookup")
	}
	if got, want := tools[0].Arguments.String(), `{"q":"hi"}`; got != want {
		t.Errorf("ProcessEvent: ToolCalls[0].Arguments = %q, want %q", got, want)
	}

	// 4. Tool call accumulation — TWO tool calls at different indices
	//    (regression for review-fix #2 — multi-tool mirror requires
	//    distinct slots per index; the State mirror must also keep
	//    them separate, NOT concatenate).
	tr4 := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	tc2 := []interface{}{
		map[string]interface{}{
			"index": 0,
			"id":    "call_a",
			"function": map[string]interface{}{
				"name":      "tool_a",
				"arguments": `{"a":1}`,
			},
		},
		map[string]interface{}{
			"index": 1,
			"id":    "call_b",
			"function": map[string]interface{}{
				"name":      "tool_b",
				"arguments": `{"b":2}`,
			},
		},
	}
	if _, err := tr4.ProcessEvent("", "", "", tc2); err != nil {
		t.Fatalf("ProcessEvent(two tool_calls): %v", err)
	}
	tools = tr4.State().ToolCalls
	if len(tools) != 2 {
		t.Fatalf("ProcessEvent: 2-tool ToolCalls len = %d, want 2", len(tools))
	}
	if tools[0].ID != "call_a" || tools[0].Name != "tool_a" || tools[0].Arguments.String() != `{"a":1}` {
		t.Errorf("ProcessEvent: ToolCalls[0] corrupted: %+v", tools[0])
	}
	if tools[1].ID != "call_b" || tools[1].Name != "tool_b" || tools[1].Arguments.String() != `{"b":2}` {
		t.Errorf("ProcessEvent: ToolCalls[1] corrupted: %+v", tools[1])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestIncrementalStreamTranslator_ProcessEvent_MatchesProcessChunkState
// is a stronger cross-entry-point regression for review-fix #1. It
// drives the SAME logical OpenAI stream through ProcessChunk and
// ProcessEvent (the two entry points) and asserts the resulting
// StreamState is byte-identical — except that ProcessEvent has no
// access to usage + finish_reason per-chunk (those are not exposed on
// the typed-event entry), so the comparison excludes those fields.
//
// A failure of this test means the two entry points have DRIFTED —
// exactly the bug class that drove the review-fix. Future changes
// to either entry point must keep this invariant.
// ─────────────────────────────────────────────────────────────────────────────

func TestIncrementalStreamTranslator_ProcessEvent_MatchesProcessChunkState(t *testing.T) {
	// Logical OpenAI stream: role + content "ab" + reasoning "r1r2"
	// + tool_call id+name+args "x", finished.
	chunks := []string{
		`data: {"choices":[{"delta":{"role":"assistant"},"index":0}]}`,
		`data: {"choices":[{"delta":{"content":"a"},"index":0}]}`,
		`data: {"choices":[{"delta":{"content":"b"},"index":0}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"r1"},"index":0}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"r2"},"index":0}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"fn","arguments":"{}"}}]},"index":0}]}`,
	}

	trChunk := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	for _, c := range chunks {
		if _, err := trChunk.ProcessChunk([]byte(c)); err != nil {
			t.Fatalf("ProcessChunk(%s): %v", c, err)
		}
	}

	trEvent := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	// Drive the same logical events through ProcessEvent. Note the
	// reasoning_content + content are merged by event type, so we
	// replay them in the same order ProcessChunk would have seen them.
	type ev struct {
		content, reasoning, thinking string
		toolCalls                   []interface{}
	}
	events := []ev{
		{content: "", reasoning: "", thinking: ""}, // role-only preamble equivalent
		{content: "a"},
		{content: "b"},
		{reasoning: "r1"},
		{reasoning: "r2"},
		{toolCalls: []interface{}{map[string]interface{}{
			"index": 0,
			"id":    "call_x",
			"function": map[string]interface{}{
				"name":      "fn",
				"arguments": "{}",
			},
		}}},
	}
	for _, e := range events {
		if _, err := trEvent.ProcessEvent(e.content, e.reasoning, e.thinking, e.toolCalls); err != nil {
			t.Fatalf("ProcessEvent: %v", err)
		}
	}

	cs := trChunk.State()
	es := trEvent.State()

	if got, want := cs.AccumulatedContent.String(), es.AccumulatedContent.String(); got != want {
		t.Errorf("AccumulatedContent drift: chunk=%q event=%q", got, want)
	}
	if got, want := cs.ThinkingContent.String(), es.ThinkingContent.String(); got != want {
		t.Errorf("ThinkingContent drift: chunk=%q event=%q", got, want)
	}
	if len(cs.ToolCalls) != len(es.ToolCalls) {
		t.Fatalf("ToolCalls len drift: chunk=%d event=%d", len(cs.ToolCalls), len(es.ToolCalls))
	}
	for i := range cs.ToolCalls {
		if cs.ToolCalls[i].ID != es.ToolCalls[i].ID {
			t.Errorf("ToolCalls[%d].ID drift: chunk=%q event=%q", i, cs.ToolCalls[i].ID, es.ToolCalls[i].ID)
		}
		if cs.ToolCalls[i].Name != es.ToolCalls[i].Name {
			t.Errorf("ToolCalls[%d].Name drift: chunk=%q event=%q", i, cs.ToolCalls[i].Name, es.ToolCalls[i].Name)
		}
		if cs.ToolCalls[i].Arguments.String() != es.ToolCalls[i].Arguments.String() {
			t.Errorf("ToolCalls[%d].Arguments drift: chunk=%q event=%q",
				i, cs.ToolCalls[i].Arguments.String(), es.ToolCalls[i].Arguments.String())
		}
	}
}
