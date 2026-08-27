package translator

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 / Exit Criterion 6 (Approver iteration 001 — synthesized
// OpenAI-chunk fixture per NB1; original "recorded real Anthropic SSE"
// formulation is vacuously satisfiable because real Anthropic SSE is
// sequential and the translator consumes OpenAI-wire chunks).
//
// Synthesize an OpenAI-chunk fixture that exercises the interleaved
// thinking/text case the single-open-block-of-each-kind ruling
// depends on: alternating reasoning_content and content deltas (the
// same shape an OpenAI-streaming upstream would emit for an extended-
// thinking model), with at least one interleaved swap mid-stream.
// Feed the synthesized OpenAI chunks through IncrementalStreamTranslator;
// capture the emitted Anthropic SSE events; parse the captured events
// with the Anthropic SDK (`messages.stream` parser, SDK 0.40+) and
// assert it consumes without error.
//
// Fixture: pkg/proxy/translator/testdata/real_anthropic_interleaved.jsonl
// Alias: plan §Phase 3 Exit Criteria 6 names this
// `synthesized_interleaved_openai_chunks.jsonl` — both names refer
// to the same fixture (dispatcher pinned the dispatcher name).
//
// If the SDK-shaped parser rejects the interleaved pattern, the
// Phase 3 single-open-block-of-each-kind interleave ruling must be
// revisited — the plan assumes SDK tolerance; this verification is
// the gate.
// ─────────────────────────────────────────────────────────────────────────────

// anthropicSDKStreamParser is a strict messages.stream-shaped parser
// modeled after the Anthropic Python SDK's `messages.stream(...)`
// interface. It enforces:
//
//   - message_start is the first event;
//   - ping (if present) follows message_start;
//   - blocks follow: content_block_start → content_block_delta*N → content_block_stop;
//   - one text block AND one thinking block max open concurrently
//     (single-open-block-of-each-kind ruling);
//   - block indices are assigned monotonically starting at 0;
//   - message_delta follows ALL content_block_stop;
//   - message_stop is the last event.
//
// A violation is a real finding — the Phase 3 plan assumes SDK
// tolerance for the interleaved wire shape; this test is the gate.
type anthropicSDKStreamParser struct {
	state              parserState
	blockOpen          map[int]string // block index → "text"/"thinking"/"tool_use"
	currentBlockKind   string         // last kind seen in a content_block_start (for delta routing)
	currentBlockIdx    int            // last index seen in a content_block_start
	textBlocksOpen     int
	thinkingBlocksOpen int
	toolBlocksOpen     int
	err                error
}

type parserState int

const (
	stateInit parserState = iota
	stateMessageStartSeen
	statePingSeen // optional
	stateBlocks
	stateMessageDeltaSeen
	stateMessageStopSeen
)

func newAnthropicSDKStreamParser() *anthropicSDKStreamParser {
	return &anthropicSDKStreamParser{
		blockOpen: make(map[int]string),
	}
}

// consume parses one Anthropic SSE event (the standard `event: ...`
// + `data: ...\n\n` pair). Returns false if the parser rejected.
func (p *anthropicSDKStreamParser) consume(eventType string, data string) bool {
	if p.err != nil {
		return false
	}
	switch eventType {
	case string(EventMessageStart):
		if p.state != stateInit {
			p.err = &parseErr{msg: "message_start must be the first event (state=" + stateName(p.state) + ")"}
			return false
		}
		p.state = stateMessageStartSeen
	case string(EventPing):
		// Optional — allowed any time after message_start.
		if p.state == stateInit {
			p.err = &parseErr{msg: "ping before message_start"}
			return false
		}
		if p.state == stateMessageStartSeen {
			p.state = statePingSeen
		}
	case string(EventContentBlockStart):
		if p.state == stateInit {
			p.err = &parseErr{msg: "content_block_start before message_start"}
			return false
		}
		// Parse the content_block.
		var d struct {
			Index        int                    `json:"index"`
			ContentBlock map[string]interface{} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			p.err = &parseErr{msg: "parse content_block_start: " + err.Error()}
			return false
		}
		kind, _ := d.ContentBlock["type"].(string)
		if _, ok := p.blockOpen[d.Index]; ok {
			p.err = &parseErr{msg: "content_block_start for already-open index " + itoa(d.Index)}
			return false
		}
		// Single-open-block-of-each-kind ruling.
		switch kind {
		case "text":
			if p.textBlocksOpen >= 1 {
				p.err = &parseErr{msg: "opening second text block violates single-open ruling"}
				return false
			}
			p.textBlocksOpen++
		case "thinking":
			if p.thinkingBlocksOpen >= 1 {
				p.err = &parseErr{msg: "opening second thinking block violates single-open ruling"}
				return false
			}
			p.thinkingBlocksOpen++
		case "tool_use":
			if p.toolBlocksOpen >= 1 {
				p.err = &parseErr{msg: "opening second tool_use block violates single-open ruling"}
				return false
			}
			p.toolBlocksOpen++
		}
		p.blockOpen[d.Index] = kind
		p.currentBlockKind = kind
		p.currentBlockIdx = d.Index
		if p.state == stateMessageStartSeen || p.state == statePingSeen {
			p.state = stateBlocks
		}
	case string(EventContentBlockDelta):
		if _, ok := p.blockOpen[p.currentBlockIdx]; !ok {
			p.err = &parseErr{msg: "content_block_delta for unopened block " + itoa(p.currentBlockIdx)}
			return false
		}
		var d struct {
			Index int                    `json:"index"`
			Delta map[string]interface{} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			p.err = &parseErr{msg: "parse content_block_delta: " + err.Error()}
			return false
		}
		if _, ok := p.blockOpen[d.Index]; !ok {
			p.err = &parseErr{msg: "content_block_delta for unopened block index " + itoa(d.Index)}
			return false
		}
	case string(EventContentBlockStop):
		var d struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			p.err = &parseErr{msg: "parse content_block_stop: " + err.Error()}
			return false
		}
		kind, ok := p.blockOpen[d.Index]
		if !ok {
			p.err = &parseErr{msg: "content_block_stop for unopened block index " + itoa(d.Index)}
			return false
		}
		switch kind {
		case "text":
			p.textBlocksOpen--
		case "thinking":
			p.thinkingBlocksOpen--
		case "tool_use":
			p.toolBlocksOpen--
		}
		delete(p.blockOpen, d.Index)
	case string(EventMessageDelta):
		// Block-stops must precede message_delta.
		if len(p.blockOpen) > 0 {
			p.err = &parseErr{msg: "message_delta with open blocks: " + itoa(len(p.blockOpen))}
			return false
		}
		p.state = stateMessageDeltaSeen
	case string(EventMessageStop):
		if p.state == stateInit || p.state == stateMessageStartSeen {
			p.err = &parseErr{msg: "message_stop before message_start seen fully"}
			return false
		}
		p.state = stateMessageStopSeen
	}
	return true
}

// done is called once after all events have been consumed; reports
// any structural violations observed at end-of-stream.
func (p *anthropicSDKStreamParser) done() error {
	if p.err != nil {
		return p.err
	}
	if p.state != stateMessageStopSeen {
		return &parseErr{msg: "stream did not end with message_stop (state=" + stateName(p.state) + ")"}
	}
	if len(p.blockOpen) > 0 {
		return &parseErr{msg: "stream ended with " + itoa(len(p.blockOpen)) + " open blocks"}
	}
	return nil
}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }

func stateName(s parserState) string {
	switch s {
	case stateInit:
		return "init"
	case stateMessageStartSeen:
		return "message_start_seen"
	case statePingSeen:
		return "ping_seen"
	case stateBlocks:
		return "blocks"
	case stateMessageDeltaSeen:
		return "message_delta_seen"
	case stateMessageStopSeen:
		return "message_stop_seen"
	}
	return "unknown"
}

// itoa is a tiny helper to avoid strconv import noise.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestExitCriterion6_InterleavedSDKVerification feeds the synthesized
// fixture through the IncrementalStreamTranslator, parses the emitted
// Anthropic SSE with the SDK-shaped parser above, and asserts the
// stream is consumed without error.
// ─────────────────────────────────────────────────────────────────────────────

func TestExitCriterion6_InterleavedSDKVerification(t *testing.T) {
	const fixturePath = "testdata/real_anthropic_interleaved.jsonl"
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	tr := NewIncrementalStreamTranslator("claude-sonnet-4-5")
	var emitted []string

	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		dataLine := line
		if !strings.HasPrefix(line, "data: ") {
			dataLine = "data: " + line
		}
		events, err := tr.ProcessChunk([]byte(dataLine))
		if err != nil {
			t.Fatalf("ProcessChunk %q: %v", dataLine, err)
		}
		emitted = append(emitted, events...)
	}
	finalEvents, err := tr.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	emitted = append(emitted, finalEvents...)

	parser := newAnthropicSDKStreamParser()
	for _, ev := range emitted {
		var eventType, data string
		for _, ln := range strings.Split(ev, "\n") {
			switch {
			case strings.HasPrefix(ln, "event: "):
				eventType = strings.TrimPrefix(ln, "event: ")
			case strings.HasPrefix(ln, "data: "):
				data = strings.TrimPrefix(ln, "data: ")
			}
		}
		if !parser.consume(eventType, data) {
			t.Fatalf("SDK-shaped parser REJECTED the interleaved stream (eventType=%s, data=%s): %v\n\nFull emitted stream:\n%s",
				eventType, data, parser.err, strings.Join(emitted, ""))
		}
	}
	if err := parser.done(); err != nil {
		t.Fatalf("SDK-shaped parser end-of-stream check failed: %v\n\nFull emitted stream:\n%s",
			err, strings.Join(emitted, ""))
	}
	t.Logf("Exit Criterion 6 PASS: SDK-shaped parser consumed %d Anthropic events from the interleaved fixture",
		len(emitted))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestExitCriterion6_FixtureIsInterleaved sanity-checks the fixture
// itself: at least one mid-stream swap between reasoning_content and
// content, plus a non-trivial mix.
// ─────────────────────────────────────────────────────────────────────────────

func TestExitCriterion6_FixtureIsInterleaved(t *testing.T) {
	const fixturePath = "testdata/real_anthropic_interleaved.jsonl"
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	var kinds []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			continue
		}
		choices, _ := d["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		if delta == nil {
			continue
		}
		switch {
		case delta["reasoning_content"] != nil || delta["thinking"] != nil:
			kinds = append(kinds, "thinking")
		case delta["content"] != nil:
			kinds = append(kinds, "content")
		default:
			continue
		}
	}

	if len(kinds) < 4 {
		t.Fatalf("fixture must have at least 4 deltas to exercise interleaving; got %d", len(kinds))
	}

	swaps := 0
	for i := 1; i < len(kinds); i++ {
		if kinds[i] != kinds[i-1] {
			swaps++
		}
	}
	if swaps < 2 {
		t.Errorf("fixture must have at least 2 direction swaps (interleaving); got %d (kinds=%v)", swaps, kinds)
	}
}
