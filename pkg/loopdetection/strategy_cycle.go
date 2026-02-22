package loopdetection

import (
	"fmt"
	"strings"
)

// CycleStrategy detects circular patterns in multi-step action workflows.
// It looks for repeating action subsequences of length 2-5 within the
// recent action history. This catches patterns like A→B→C→A→B→C that
// the simpler ActionPatternStrategy (which only checks consecutive
// identical or oscillating pairs) would miss.
type CycleStrategy struct {
	maxCycleLength int // Maximum cycle length to check (default: 5)
	minOccurrences int // Minimum cycle repetitions to trigger (default: 2)
}

// NewCycleStrategy creates a new cycle detection strategy.
func NewCycleStrategy(maxCycleLength, minOccurrences int) *CycleStrategy {
	if maxCycleLength <= 0 {
		maxCycleLength = 5
	}
	if minOccurrences <= 0 {
		minOccurrences = 2
	}
	return &CycleStrategy{
		maxCycleLength: maxCycleLength,
		minOccurrences: minOccurrences,
	}
}

// Name returns the strategy identifier.
func (s *CycleStrategy) Name() string {
	return "cycle"
}

// Analyze checks the action history from all messages in the window
// for repeating subsequences of length 2-maxCycleLength.
func (s *CycleStrategy) Analyze(window []MessageContext) *DetectionResult {
	// Collect all actions from the window
	var allActions []Action
	for _, msg := range window {
		allActions = append(allActions, msg.Actions...)
	}

	// Need enough actions to detect a cycle of any length
	if len(allActions) < 4 {
		return nil
	}

	// Convert actions to string keys for pattern matching
	keys := make([]string, len(allActions))
	for i, a := range allActions {
		keys[i] = a.ActionKey()
	}

	// Check for repeating patterns of increasing length
	for patternLen := 2; patternLen <= s.maxCycleLength; patternLen++ {
		if result := s.findRepeatingPattern(keys, patternLen); result != nil {
			return result
		}
	}

	return nil
}

// findRepeatingPattern checks if the last N*patternLen actions contain
// a repeating pattern of the given length.
func (s *CycleStrategy) findRepeatingPattern(keys []string, patternLen int) *DetectionResult {
	minRequired := patternLen * s.minOccurrences
	if len(keys) < minRequired {
		return nil
	}

	// Slide a window from the end of the action list backward
	// Check if the last actions form a repeating pattern
	for startIdx := len(keys) - minRequired; startIdx >= 0; startIdx-- {
		pattern := keys[startIdx : startIdx+patternLen]
		occurrences := 0

		for j := startIdx; j+patternLen <= len(keys); j += patternLen {
			candidate := keys[j : j+patternLen]
			if sliceEqual(candidate, pattern) {
				occurrences++
			} else {
				break
			}
		}

		if occurrences >= s.minOccurrences {
			// Verify it's a genuine cycle (not all same action, which ActionPattern already catches)
			if allSame(pattern) {
				continue
			}

			severity := SeverityWarning
			confidence := 0.75

			if occurrences >= 3 {
				severity = SeverityCritical
				confidence = 0.9
			}

			return &DetectionResult{
				LoopDetected: true,
				Severity:     severity,
				Strategy:     s.Name(),
				Evidence: fmt.Sprintf("Action cycle [%s] repeated %d times (cycle length: %d)",
					strings.Join(pattern, " → "), occurrences, patternLen),
				Confidence:  confidence,
				Pattern:     pattern,
				RepeatCount: occurrences,
			}
		}
	}

	return nil
}

// sliceEqual compares two string slices for equality.
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// allSame returns true if all elements in the slice are identical.
func allSame(s []string) bool {
	if len(s) <= 1 {
		return true
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}
