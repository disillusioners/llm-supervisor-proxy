package fingerprint

import "strings"

// TODO(Phase 3): GenerateTrigrams and TrigramRepetitionRatio are prepared for
// the ThinkingStrategy (Phase 3 — Thinking/Reasoning Loop Detection).
// They will be used to detect repetitive reasoning patterns in models like o1, o3-mini.

// GenerateTrigrams produces a map of trigram → count from the input text.
func GenerateTrigrams(text string) map[string]int {
	words := strings.Fields(text)
	if len(words) < 3 {
		return nil
	}

	trigrams := make(map[string]int)
	for i := 0; i <= len(words)-3; i++ {
		trigram := strings.Join(words[i:i+3], " ")
		trigrams[trigram]++
	}
	return trigrams
}

// TrigramRepetitionRatio returns a ratio of unique trigrams to total trigrams.
// Higher ratio = more unique content = less repetition.
// Lower ratio = more repeated content = possible loop.
// Returns 1.0 if text is too short to analyze.
func TrigramRepetitionRatio(text string) float64 {
	words := strings.Fields(text)
	if len(words) < 10 {
		return 1.0 // Not enough to analyze
	}

	trigrams := GenerateTrigrams(text)
	if trigrams == nil {
		return 1.0
	}

	uniqueTrigrams := len(trigrams)
	totalTrigrams := len(words) - 2

	return float64(uniqueTrigrams) / float64(totalTrigrams)
}
