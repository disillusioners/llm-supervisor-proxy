package loopdetection

import "fmt"

// ExactMatchStrategy detects identical consecutive messages within a sliding window.
type ExactMatchStrategy struct {
	matchCount int // Number of identical messages needed to trigger detection
}

// NewExactMatchStrategy creates a new exact matching strategy.
func NewExactMatchStrategy(matchCount int) *ExactMatchStrategy {
	if matchCount < 2 {
		matchCount = 2
	}
	return &ExactMatchStrategy{matchCount: matchCount}
}

// Name returns the strategy identifier.
func (s *ExactMatchStrategy) Name() string {
	return "exact_match"
}

// Analyze checks for identical messages in the window by comparing content hashes.
func (s *ExactMatchStrategy) Analyze(window []MessageContext) *DetectionResult {
	if len(window) < s.matchCount {
		return nil
	}

	// Only look at assistant messages
	var assistantMsgs []MessageContext
	for _, msg := range window {
		if msg.Role == "assistant" && msg.ContentType == "text" && msg.TokenCount > 0 {
			assistantMsgs = append(assistantMsgs, msg)
		}
	}

	if len(assistantMsgs) < s.matchCount {
		return nil
	}

	// Check for consecutive identical content hashes
	consecutiveCount := 1
	for i := 1; i < len(assistantMsgs); i++ {
		if assistantMsgs[i].ContentHash == assistantMsgs[i-1].ContentHash &&
			assistantMsgs[i].Content == assistantMsgs[i-1].Content {
			consecutiveCount++
		} else {
			consecutiveCount = 1
		}

		if consecutiveCount >= s.matchCount {
			content := assistantMsgs[i].Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			return &DetectionResult{
				LoopDetected: true,
				Severity:     SeverityCritical,
				Strategy:     s.Name(),
				Evidence:     fmt.Sprintf("Identical message repeated %d times: %q", consecutiveCount, content),
				Confidence:   1.0,
				Pattern:      []string{content},
				RepeatCount:  consecutiveCount,
			}
		}
	}

	return nil
}
