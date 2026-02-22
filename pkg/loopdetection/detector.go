package loopdetection

import (
	"log"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

// Detector is the main loop detection orchestrator.
// It manages a sliding window of MessageContext entries and runs
// configured strategies against them.
type Detector struct {
	mu         sync.Mutex
	config     Config
	window     []MessageContext
	strategies []Strategy
	msgCounter int
}

// NewDetector creates a new Detector with the specified configuration.
// It initializes the Phase 1 strategies: exact match, similarity, and action pattern.
func NewDetector(config Config) *Detector {
	d := &Detector{
		config: config,
		window: make([]MessageContext, 0, config.MessageWindow),
	}

	if config.Enabled {
		d.strategies = []Strategy{
			NewExactMatchStrategy(config.ExactMatchCount),
			NewSimilarityStrategy(config.SimilarityThreshold, config.MinTokensForSimHash, config.MessageWindow),
			NewActionPatternStrategy(config.ActionRepeatCount, config.OscillationCount),
		}
	}

	return d
}

// NewStreamBuffer creates a StreamBuffer configured with this detector's settings.
func (d *Detector) NewStreamBuffer() *StreamBuffer {
	return NewStreamBuffer(d.config.MinTokensForAnalysis)
}

// Analyze processes a completed text chunk and runs all strategies.
// It creates a MessageContext, appends it to the sliding window,
// and checks each strategy for loop patterns.
//
// If ShadowMode is enabled (default), detection results are logged
// but the returned result's LoopDetected will still be true — callers
// should check config.ShadowMode before taking action.
func (d *Detector) Analyze(text string, actions []Action) *DetectionResult {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.config.Enabled || len(d.strategies) == 0 {
		return nil
	}

	// Build message context
	d.msgCounter++
	ctx := MessageContext{
		ID:          generateMsgID(d.msgCounter),
		Timestamp:   time.Now(),
		Role:        "assistant",
		ContentType: "text",
		Content:     text,
		ContentHash: fingerprint.ComputeSimHash(text),
		TokenCount:  fingerprint.EstimateTokenCount(text),
		Actions:     actions,
	}

	// Append to sliding window
	d.window = append(d.window, ctx)
	if len(d.window) > d.config.MessageWindow {
		d.window = d.window[len(d.window)-d.config.MessageWindow:]
	}

	// Run all strategies
	for _, strategy := range d.strategies {
		result := strategy.Analyze(d.window)
		if result != nil && result.LoopDetected {
			if d.config.ShadowMode {
				log.Printf("[LOOP-DETECTION][SHADOW] Strategy=%s Severity=%s Evidence=%q Confidence=%.2f",
					result.Strategy, result.Severity, result.Evidence, result.Confidence)
			} else {
				log.Printf("[LOOP-DETECTION] Strategy=%s Severity=%s Evidence=%q Confidence=%.2f",
					result.Strategy, result.Severity, result.Evidence, result.Confidence)
			}
			return result
		}
	}

	return nil
}

// AnalyzeActions runs only the action pattern strategy on a set of actions.
// This can be called immediately when a tool call completes, without
// waiting for the text buffer threshold.
func (d *Detector) AnalyzeActions(actions []Action) *DetectionResult {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.config.Enabled {
		return nil
	}

	strategy := NewActionPatternStrategy(d.config.ActionRepeatCount, d.config.OscillationCount)
	window := []MessageContext{{Actions: actions}}
	result := strategy.Analyze(window)
	if result != nil && result.LoopDetected {
		if d.config.ShadowMode {
			log.Printf("[LOOP-DETECTION][SHADOW] Strategy=%s Evidence=%q",
				result.Strategy, result.Evidence)
		} else {
			log.Printf("[LOOP-DETECTION] Strategy=%s Evidence=%q",
				result.Strategy, result.Evidence)
		}
	}
	return result
}

// IsShadowMode returns whether the detector is in shadow mode (log only).
func (d *Detector) IsShadowMode() bool {
	return d.config.ShadowMode
}

// IsEnabled returns whether loop detection is enabled.
func (d *Detector) IsEnabled() bool {
	return d.config.Enabled
}

// Reset clears the detector's message window. Should be called when
// a new request starts.
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = d.window[:0]
	d.msgCounter = 0
}

// WindowSize returns the current number of messages in the sliding window.
func (d *Detector) WindowSize() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.window)
}

// generateMsgID creates a simple message ID.
func generateMsgID(counter int) string {
	return "msg-" + itoa(counter)
}

// itoa is a simple integer to ASCII string converter (avoids strconv import for this tiny use).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	result := ""
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		result = string(rune('0'+i%10)) + result
		i /= 10
	}
	if neg {
		result = "-" + result
	}
	return result
}
