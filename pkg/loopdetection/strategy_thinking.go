package loopdetection

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

const maxThinkingBufferSize = 1 * 1024 * 1024 // 1MB limit

// Minimum time between trigram analyses to prevent CPU/memory thrashing
// With reasoning models generating content rapidly, this prevents running
// expensive trigram analysis on every small chunk
const minAnalysisInterval = 500 * time.Millisecond

// ThinkingStrategy detects repetitive thinking/reasoning patterns.
// Reasoning models (o1, o3-mini, etc.) naturally produce iterative thinking
// that can loop when stuck. This strategy uses trigram repetition analysis
// to detect when the reasoning content becomes repetitive.
//
// The strategy tracks accumulated thinking content and analyzes it once
// enough tokens have been accumulated (ThinkingMinTokens).
type ThinkingStrategy struct {
	trigramThreshold          float64  // Repetition ratio below this = loop (default: 0.3)
	thinkingMinTokens         int      // Minimum tokens before analysis (default: 100)
	reasoningModelPatterns    []string // Regex patterns for reasoning models
	reasoningTrigramThreshold float64  // More forgiving threshold for reasoning models (default: 0.15)
	compiledPatterns          []*regexp.Regexp

	// Internal state: accumulated thinking content across the window
	accumulatedThinking strings.Builder
	thinkingTokenCount  int
	currentModel        string

	// Rate limiting for expensive analysis
	lastAnalysisTime time.Time
	mu               sync.Mutex
}

// NewThinkingStrategy creates a new thinking loop detection strategy.
func NewThinkingStrategy(trigramThreshold float64, thinkingMinTokens int,
	reasoningModelPatterns []string, reasoningTrigramThreshold float64) *ThinkingStrategy {

	if trigramThreshold <= 0 {
		trigramThreshold = 0.3
	}
	if thinkingMinTokens <= 0 {
		thinkingMinTokens = 100
	}
	if reasoningTrigramThreshold <= 0 {
		reasoningTrigramThreshold = 0.15
	}

	s := &ThinkingStrategy{
		trigramThreshold:          trigramThreshold,
		thinkingMinTokens:         thinkingMinTokens,
		reasoningModelPatterns:    reasoningModelPatterns,
		reasoningTrigramThreshold: reasoningTrigramThreshold,
	}

	// Compile regex patterns for reasoning model detection
	for _, pattern := range reasoningModelPatterns {
		if re, err := regexp.Compile("(?i)" + pattern); err == nil {
			s.compiledPatterns = append(s.compiledPatterns, re)
		}
	}

	return s
}

// Name returns the strategy identifier.
func (s *ThinkingStrategy) Name() string {
	return "thinking"
}

// SetModel sets the current model name for threshold selection.
func (s *ThinkingStrategy) SetModel(model string) {
	s.currentModel = model
}

// AddThinkingContent accumulates thinking/reasoning content for analysis.
func (s *ThinkingStrategy) AddThinkingContent(text string) {
	// Enforce memory limit by keeping only the tail (most recent content)
	if s.accumulatedThinking.Len() >= maxThinkingBufferSize {
		current := s.accumulatedThinking.String()
		keepLen := maxThinkingBufferSize / 2 // Keep last 512KB
		if keepLen > len(current) {
			keepLen = len(current)
		}
		tail := current[len(current)-keepLen:]
		s.accumulatedThinking.Reset()
		s.accumulatedThinking.WriteString(tail)
		s.thinkingTokenCount = fingerprint.EstimateTokenCount(tail)
	}

	s.accumulatedThinking.WriteString(text)
	s.thinkingTokenCount += fingerprint.EstimateTokenCount(text)
}

// Analyze checks the accumulated thinking content for repetitive patterns.
// It only runs when enough thinking tokens have been accumulated.
// Rate limited to prevent CPU/memory thrashing on fast reasoning models.
func (s *ThinkingStrategy) Analyze(window []MessageContext) *DetectionResult {
	if s.thinkingTokenCount < s.thinkingMinTokens {
		return nil
	}

	// Rate limit: skip analysis if called too recently
	// This prevents memory explosion when reasoning models generate content rapidly
	s.mu.Lock()
	now := time.Now()
	if now.Sub(s.lastAnalysisTime) < minAnalysisInterval {
		s.mu.Unlock()
		return nil
	}
	s.lastAnalysisTime = now
	s.mu.Unlock()

	text := s.accumulatedThinking.String()
	ratio := fingerprint.TrigramRepetitionRatio(text)

	threshold := s.trigramThreshold
	if s.isReasoningModel() {
		threshold = s.reasoningTrigramThreshold
	}

	if ratio < threshold {
		severity := SeverityWarning
		confidence := 0.7

		// Very low ratio = very repetitive = higher severity
		if ratio < threshold*0.5 {
			severity = SeverityCritical
			confidence = 0.9
		}

		// Truncate for evidence
		evidenceText := text
		if len(evidenceText) > 200 {
			evidenceText = evidenceText[:200] + "..."
		}

		return &DetectionResult{
			LoopDetected: true,
			Severity:     severity,
			Strategy:     s.Name(),
			Evidence: fmt.Sprintf("Thinking content has high trigram repetition (ratio=%.3f, threshold=%.3f, tokens=%d)",
				ratio, threshold, s.thinkingTokenCount),
			Confidence:  confidence,
			Pattern:     []string{evidenceText},
			RepeatCount: int(1.0 / ratio), // approximate repeats
		}
	}

	return nil
}

// Reset clears accumulated thinking content.
func (s *ThinkingStrategy) Reset() {
	s.accumulatedThinking.Reset()
	s.thinkingTokenCount = 0
	s.lastAnalysisTime = time.Time{} // Reset rate limiter
}

// isReasoningModel checks if the current model matches any reasoning model pattern.
func (s *ThinkingStrategy) isReasoningModel() bool {
	if s.currentModel == "" {
		return false
	}
	for _, re := range s.compiledPatterns {
		if re.MatchString(s.currentModel) {
			return true
		}
	}
	return false
}
