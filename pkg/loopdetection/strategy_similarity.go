package loopdetection

import (
	"fmt"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

// SimilarityStrategy detects near-identical messages using SimHash comparison.
// It only activates for messages with enough tokens to avoid false positives.
type SimilarityStrategy struct {
	threshold  float64 // Similarity threshold (e.g., 0.85)
	minTokens  int     // Minimum tokens before SimHash is applied
	windowSize int     // How many recent messages to compare
}

// NewSimilarityStrategy creates a new similarity-based detection strategy.
func NewSimilarityStrategy(threshold float64, minTokens, windowSize int) *SimilarityStrategy {
	return &SimilarityStrategy{
		threshold:  threshold,
		minTokens:  minTokens,
		windowSize: windowSize,
	}
}

// Name returns the strategy identifier.
func (s *SimilarityStrategy) Name() string {
	return "similarity"
}

// Analyze compares SimHash fingerprints of recent assistant messages.
func (s *SimilarityStrategy) Analyze(window []MessageContext) *DetectionResult {
	// Collect eligible assistant messages (must have enough tokens for SimHash)
	var eligible []MessageContext
	for _, msg := range window {
		if msg.Role == "assistant" && msg.ContentType == "text" && msg.TokenCount >= s.minTokens {
			eligible = append(eligible, msg)
		}
	}

	if len(eligible) < 2 {
		return nil
	}

	// Limit to most recent messages within window
	if len(eligible) > s.windowSize {
		eligible = eligible[len(eligible)-s.windowSize:]
	}

	// Compare the most recent message against previous ones
	latest := eligible[len(eligible)-1]
	similarCount := 0
	var matchedPatterns []string

	for i := 0; i < len(eligible)-1; i++ {
		sim := fingerprint.Similarity(latest.ContentHash, eligible[i].ContentHash)
		if sim >= s.threshold {
			similarCount++
			content := eligible[i].Content
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			matchedPatterns = append(matchedPatterns, content)
		}
	}

	if similarCount >= 2 {
		return &DetectionResult{
			LoopDetected: true,
			Severity:     SeverityCritical,
			Strategy:     s.Name(),
			Evidence:     fmt.Sprintf("Found %d similar messages (threshold: %.0f%%) in the last %d messages", similarCount+1, s.threshold*100, len(eligible)),
			Confidence:   0.85,
			Pattern:      matchedPatterns,
			RepeatCount:  similarCount + 1,
		}
	}

	if similarCount >= 1 {
		return &DetectionResult{
			LoopDetected: true,
			Severity:     SeverityWarning,
			Strategy:     s.Name(),
			Evidence:     fmt.Sprintf("Found 2 similar messages (threshold: %.0f%%)", s.threshold*100),
			Confidence:   0.6,
			Pattern:      matchedPatterns,
			RepeatCount:  2,
		}
	}

	return nil
}
