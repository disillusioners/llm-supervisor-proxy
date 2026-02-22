package loopdetection

import (
	"fmt"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

// StagnationStrategy detects when messages are being produced but
// no meaningful progress is being made. It compares SimHash fingerprints
// of the accumulated content over a window — if the content remains
// highly similar despite multiple messages, the model is stagnating.
//
// This is different from SimilarityStrategy which compares individual
// messages pairwise. StagnationStrategy looks at cumulative content
// fingerprints to detect when the conversation is "going in circles"
// without advancing.
type StagnationStrategy struct {
	windowSize      int     // How many recent messages to consider (default: 5)
	changeThreshold float64 // Minimum content change ratio required (default: 0.3)
	minMessages     int     // Minimum messages before stagnation check (default: 5)
}

// NewStagnationStrategy creates a new progress stagnation detection strategy.
func NewStagnationStrategy(windowSize int, changeThreshold float64, minMessages int) *StagnationStrategy {
	if windowSize <= 0 {
		windowSize = 5
	}
	if changeThreshold <= 0 {
		changeThreshold = 0.3
	}
	if minMessages <= 0 {
		minMessages = 5
	}
	return &StagnationStrategy{
		windowSize:      windowSize,
		changeThreshold: changeThreshold,
		minMessages:     minMessages,
	}
}

// Name returns the strategy identifier.
func (s *StagnationStrategy) Name() string {
	return "stagnation"
}

// Analyze checks whether recent messages show meaningful progress.
// It compares the pairwise SimHash similarity of recent assistant messages.
// If the average similarity is above the threshold, the model is repeating itself.
func (s *StagnationStrategy) Analyze(window []MessageContext) *DetectionResult {
	// Only look at assistant messages with real content
	var eligible []MessageContext
	for _, msg := range window {
		if msg.Role == "assistant" && msg.ContentType == "text" && msg.TokenCount >= 10 {
			eligible = append(eligible, msg)
		}
	}

	if len(eligible) < s.minMessages {
		return nil
	}

	// Take the most recent messages within our window
	if len(eligible) > s.windowSize {
		eligible = eligible[len(eligible)-s.windowSize:]
	}

	// Compare the latest message against all earlier ones
	latest := eligible[len(eligible)-1]
	totalSimilarity := 0.0
	comparisons := 0

	for i := 0; i < len(eligible)-1; i++ {
		sim := fingerprint.Similarity(latest.ContentHash, eligible[i].ContentHash)
		totalSimilarity += sim
		comparisons++
	}

	if comparisons == 0 {
		return nil
	}

	avgSimilarity := totalSimilarity / float64(comparisons)

	// changeThreshold represents the minimum expected change
	// If average similarity is above (1 - changeThreshold), content is stagnating
	maxAcceptableSimilarity := 1.0 - s.changeThreshold

	if avgSimilarity > maxAcceptableSimilarity {
		severity := SeverityWarning
		confidence := 0.6

		if avgSimilarity > maxAcceptableSimilarity+0.15 {
			severity = SeverityCritical
			confidence = 0.85
		}

		return &DetectionResult{
			LoopDetected: true,
			Severity:     severity,
			Strategy:     s.Name(),
			Evidence: fmt.Sprintf("Content stagnation detected: %.1f%% average similarity across %d messages (max acceptable: %.0f%%)",
				avgSimilarity*100, len(eligible), maxAcceptableSimilarity*100),
			Confidence:  confidence,
			Pattern:     nil,
			RepeatCount: len(eligible),
		}
	}

	return nil
}
