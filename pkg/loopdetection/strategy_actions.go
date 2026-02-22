package loopdetection

import (
	"fmt"
	"strings"
)

// ActionPatternStrategy detects repeated tool/file operations without progress.
// It catches two patterns:
//  1. Consecutive identical action+target pairs (e.g., "read:config.go" x3)
//  2. Action oscillation (e.g., read→write→read→write same file)
type ActionPatternStrategy struct {
	repeatThreshold  int // Consecutive identical actions to trigger (default: 3)
	oscillationLimit int // Oscillation cycles to trigger (default: 4)
}

// NewActionPatternStrategy creates a new action pattern detection strategy.
func NewActionPatternStrategy(repeatThreshold, oscillationLimit int) *ActionPatternStrategy {
	return &ActionPatternStrategy{
		repeatThreshold:  repeatThreshold,
		oscillationLimit: oscillationLimit,
	}
}

// Name returns the strategy identifier.
func (s *ActionPatternStrategy) Name() string {
	return "action_pattern"
}

// Analyze checks the message window for repeated action patterns.
func (s *ActionPatternStrategy) Analyze(window []MessageContext) *DetectionResult {
	// Collect all actions from all messages in the window
	var allActions []Action
	for _, msg := range window {
		allActions = append(allActions, msg.Actions...)
	}

	if len(allActions) < 2 {
		return nil
	}

	// Check 1: Consecutive identical actions
	if result := s.checkConsecutiveRepeats(allActions); result != nil {
		return result
	}

	// Check 2: Oscillation detection (A→B→A→B pattern)
	if result := s.checkOscillation(allActions); result != nil {
		return result
	}

	return nil
}

// checkConsecutiveRepeats looks for N+ identical consecutive action+target pairs.
func (s *ActionPatternStrategy) checkConsecutiveRepeats(actions []Action) *DetectionResult {
	if len(actions) < s.repeatThreshold {
		return nil
	}

	consecutiveCount := 1
	for i := 1; i < len(actions); i++ {
		if actions[i].ActionKey() == actions[i-1].ActionKey() {
			consecutiveCount++
		} else {
			consecutiveCount = 1
		}

		if consecutiveCount >= s.repeatThreshold {
			return &DetectionResult{
				LoopDetected: true,
				Severity:     SeverityCritical,
				Strategy:     s.Name(),
				Evidence:     fmt.Sprintf("Action %q repeated %d times consecutively", actions[i].ActionKey(), consecutiveCount),
				Confidence:   0.95,
				Pattern:      []string{actions[i].ActionKey()},
				RepeatCount:  consecutiveCount,
			}
		}
	}

	return nil
}

// checkOscillation detects A→B→A→B→... patterns.
func (s *ActionPatternStrategy) checkOscillation(actions []Action) *DetectionResult {
	if len(actions) < s.oscillationLimit*2 {
		return nil
	}

	// Check for a 2-element repeating pattern in recent actions
	recent := actions
	if len(recent) > s.oscillationLimit*2+2 {
		recent = recent[len(recent)-(s.oscillationLimit*2+2):]
	}

	for i := 0; i <= len(recent)-4; i++ {
		patternA := recent[i].ActionKey()
		patternB := recent[i+1].ActionKey()

		if patternA == patternB {
			continue // Not an oscillation, it's a repeat
		}

		// Check how many times A→B repeats
		oscillations := 0
		for j := i; j+1 < len(recent); j += 2 {
			if recent[j].ActionKey() == patternA && recent[j+1].ActionKey() == patternB {
				oscillations++
			} else {
				break
			}
		}

		if oscillations >= s.oscillationLimit {
			return &DetectionResult{
				LoopDetected: true,
				Severity:     SeverityWarning,
				Strategy:     s.Name(),
				Evidence: fmt.Sprintf("Oscillation detected: %s ↔ %s repeated %d times",
					patternA, patternB, oscillations),
				Confidence:  0.8,
				Pattern:     []string{patternA, patternB},
				RepeatCount: oscillations,
			}
		}
	}

	return nil
}

// FormatActions returns a human-readable string of an action sequence.
func FormatActions(actions []Action) string {
	keys := make([]string, len(actions))
	for i, a := range actions {
		keys[i] = a.ActionKey()
	}
	return strings.Join(keys, " → ")
}
