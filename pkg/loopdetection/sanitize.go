package loopdetection

import "fmt"

// SanitizeLoopHistory removes repetitive messages from a conversation
// history to break the loop pattern. This is critical for recovery because
// LLMs will repeat mistakes if the context window contains many repetitions.
//
// It replaces the looping messages with a concise summary message that
// tells the model to take a different approach.
func SanitizeLoopHistory(messages []map[string]interface{}, result *DetectionResult) []map[string]interface{} {
	if result == nil || !result.LoopDetected || len(messages) == 0 {
		return messages
	}

	// Calculate how many messages to trim from the end
	trimCount := result.RepeatCount
	if trimCount <= 0 {
		trimCount = 2
	}
	if trimCount > len(messages)-1 {
		trimCount = len(messages) - 1 // Keep at least 1 message
	}

	// Keep messages before the loop
	sanitized := make([]map[string]interface{}, len(messages)-trimCount)
	copy(sanitized, messages[:len(messages)-trimCount])

	// Build a descriptive summary of what was looping
	var summaryText string
	switch result.Strategy {
	case "exact_match":
		summaryText = fmt.Sprintf(
			"[System: Your previous %d responses were identical. Please take a completely different approach. Do NOT repeat what you just tried.]",
			result.RepeatCount)
	case "similarity":
		summaryText = fmt.Sprintf(
			"[System: Your previous %d responses were nearly identical (%.0f%% similar). Please try a fundamentally different approach.]",
			result.RepeatCount, result.Confidence*100)
	case "action_pattern":
		if len(result.Pattern) > 0 {
			summaryText = fmt.Sprintf(
				"[System: Action '%s' was repeated %d times without progress. Please try a different action or target.]",
				result.Pattern[0], result.RepeatCount)
		} else {
			summaryText = "[System: Repeated identical actions detected. Please try a different approach.]"
		}
	case "cycle":
		summaryText = fmt.Sprintf(
			"[System: Action cycle detected (%d repetitions). The same sequence of actions keeps repeating. Please break the cycle by trying something new.]",
			result.RepeatCount)
	case "thinking":
		summaryText = "[System: Your reasoning has become repetitive. Please stop re-evaluating the same points and move forward with a decision.]"
	case "stagnation":
		summaryText = fmt.Sprintf(
			"[System: No meaningful progress in the last %d messages. Please summarize what you know and take concrete action.]",
			result.RepeatCount)
	default:
		summaryText = "[System: Loop detected in your responses. Please take a different approach in your next response.]"
	}

	// Add the summary as a system message
	sanitized = append(sanitized, map[string]interface{}{
		"role":    "system",
		"content": summaryText,
	})

	return sanitized
}
