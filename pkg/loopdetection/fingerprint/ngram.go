package fingerprint

import (
	"strings"
)

// TODO(Phase 3): GenerateTrigrams and TrigramRepetitionRatio are prepared for
// the ThinkingStrategy (Phase 3 — Thinking/Reasoning Loop Detection).
// They will be used to detect repetitive reasoning patterns in models like o1, o3-mini.

// MaxWordsForFullAnalysis limits trigram analysis to prevent OOM.
// For larger texts, we analyze the tail (most recent content) since loops
// typically occur in recent output.
const MaxWordsForFullAnalysis = 5000 // ~25KB of text, ~5000 trigrams

// GenerateTrigrams produces a map of trigram → count from the input text.
func GenerateTrigrams(text string) map[string]int {
	words := strings.Fields(text)
	return GenerateTrigramsFromWords(words)
}

// GenerateTrigramsFromWords produces a map of trigram → count from pre-split words.
// More efficient than GenerateTrigrams when words are already available.
func GenerateTrigramsFromWords(words []string) map[string]int {
	if len(words) < 3 {
		return nil
	}

	trigrams := make(map[string]int)
	for i := 0; i <= len(words)-3; i++ {
		// Use string builder for efficiency
		var sb strings.Builder
		sb.Grow(len(words[i]) + len(words[i+1]) + len(words[i+2]) + 2)
		sb.WriteString(words[i])
		sb.WriteByte(' ')
		sb.WriteString(words[i+1])
		sb.WriteByte(' ')
		sb.WriteString(words[i+2])
		trigrams[sb.String()]++
	}
	return trigrams
}

// TrigramRepetitionRatio returns a ratio of unique trigrams to total trigrams.
// Higher ratio = more unique content = less repetition.
// Lower ratio = more repeated content = possible loop.
// Returns 1.0 if text is too short to analyze.
//
// Memory optimization: For large texts (>5000 words), analyzes only the tail
// (most recent content) since loops typically occur in recent output.
// This bounds memory to ~5000 trigrams regardless of input size.
func TrigramRepetitionRatio(text string) float64 {
	words := strings.Fields(text)
	if len(words) < 10 {
		return 1.0 // Not enough to analyze
	}

	// For large texts, analyze only the tail (most recent content)
	// Loops typically occur in recent output, so this is optimal for loop detection
	var analyzedWords []string
	if len(words) > MaxWordsForFullAnalysis {
		analyzedWords = words[len(words)-MaxWordsForFullAnalysis:]
	} else {
		analyzedWords = words
	}

	trigrams := GenerateTrigramsFromWords(analyzedWords)
	if trigrams == nil {
		return 1.0
	}

	uniqueTrigrams := len(trigrams)
	totalTrigrams := len(analyzedWords) - 2

	return float64(uniqueTrigrams) / float64(totalTrigrams)
}
