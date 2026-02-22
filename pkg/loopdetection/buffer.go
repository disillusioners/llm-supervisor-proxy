package loopdetection

import (
	"strings"
	"time"
	"unicode"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

// StreamBuffer accumulates stream chunks into meaningful units
// before triggering loop detection analysis.
//
// Detection should NOT run on every tiny 1-5 character SSE chunk.
// Instead, the buffer accumulates text until a threshold is hit:
//   - Token count >= MinTokensForAnalysis (default 20), OR
//   - A complete sentence is detected, OR
//   - The message stream ends
type StreamBuffer struct {
	textBuffer   strings.Builder
	tokenCount   int
	lastAnalysis time.Time
	actions      []Action
	minTokens    int
}

// NewStreamBuffer creates a new stream buffer with the given minimum token threshold.
func NewStreamBuffer(minTokens int) *StreamBuffer {
	if minTokens <= 0 {
		minTokens = 20
	}
	return &StreamBuffer{
		minTokens: minTokens,
	}
}

// AddText appends a text chunk to the buffer.
func (b *StreamBuffer) AddText(text string) {
	b.textBuffer.WriteString(text)
	b.tokenCount += fingerprint.EstimateTokenCount(text)
}

// AddAction records a completed tool call / action.
func (b *StreamBuffer) AddAction(action Action) {
	b.actions = append(b.actions, action)
}

// ShouldAnalyze returns true when the buffer has accumulated enough
// content to run loop detection heuristics.
func (b *StreamBuffer) ShouldAnalyze(isMessageEnd bool) bool {
	if isMessageEnd {
		return b.tokenCount > 0 || len(b.actions) > 0
	}
	if b.tokenCount >= b.minTokens {
		return true
	}
	if isCompleteSentence(b.textBuffer.String()) && b.tokenCount >= 5 {
		return true
	}
	return false
}

// Flush returns the accumulated text and actions, then resets the buffer.
func (b *StreamBuffer) Flush() (text string, actions []Action) {
	text = b.textBuffer.String()
	actions = b.actions

	b.textBuffer.Reset()
	b.tokenCount = 0
	b.actions = nil
	b.lastAnalysis = time.Now()

	return text, actions
}

// GetActions returns pending actions without flushing.
func (b *StreamBuffer) GetActions() []Action {
	return b.actions
}

// TokenCount returns the current estimated token count in the buffer.
func (b *StreamBuffer) TokenCount() int {
	return b.tokenCount
}

// isCompleteSentence checks if the text ends with sentence-ending punctuation.
func isCompleteSentence(text string) bool {
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	if len(text) == 0 {
		return false
	}
	last := rune(text[len(text)-1])
	return last == '.' || last == '!' || last == '?' || last == '\n'
}
