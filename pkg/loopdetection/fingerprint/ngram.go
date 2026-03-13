package fingerprint

import (
	"math/rand"
	"strings"
	"time"
)

// TODO(Phase 3): GenerateTrigrams and TrigramRepetitionRatio are prepared for
// the ThinkingStrategy (Phase 3 — Thinking/Reasoning Loop Detection).
// They will be used to detect repetitive reasoning patterns in models like o1, o3-mini.

// Sampling constants to prevent memory explosion on large texts
const (
	// MaxWordsForFullAnalysis limits full trigram analysis to prevent OOM
	// For larger texts, we sample instead of analyzing all words
	MaxWordsForFullAnalysis = 5000 // ~25KB of text, ~5000 trigrams

	// SampleSizeForLargeText is the number of words to sample from large texts
	SampleSizeForLargeText = 2000 // ~10KB sample, ~2000 trigrams
)

// init seeds the random number generator for sampling
func init() {
	rand.Seed(time.Now().UnixNano())
}

// GenerateTrigrams produces a map of trigram → count from the input text.
// If usePool is true, attempts to reuse a map from the pool (recommended for hot paths).
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

// sampleWords returns a random sample of words from the input slice.
// If the input is smaller than sampleSize, returns the original slice.
func sampleWords(words []string, sampleSize int) []string {
	if len(words) <= sampleSize {
		return words
	}

	// Create a copy to avoid modifying the original
	sampled := make([]string, sampleSize)

	// Systematic sampling: pick every nth word to get representative coverage
	// This is faster than random sampling and provides better coverage
	step := float64(len(words)) / float64(sampleSize)
	for i := 0; i < sampleSize; i++ {
		idx := int(float64(i) * step)
		if idx >= len(words) {
			idx = len(words) - 1
		}
		sampled[i] = words[idx]
	}

	return sampled
}

// TrigramRepetitionRatio returns a ratio of unique trigrams to total trigrams.
// Higher ratio = more unique content = less repetition.
// Lower ratio = more repeated content = possible loop.
// Returns 1.0 if text is too short to analyze.
//
// Memory optimization: For large texts (>5000 words), samples a subset
// to prevent memory explosion while maintaining accuracy.
func TrigramRepetitionRatio(text string) float64 {
	words := strings.Fields(text)
	if len(words) < 10 {
		return 1.0 // Not enough to analyze
	}

	// For large texts, sample instead of analyzing all words
	// This prevents creating huge temporary maps (200K+ entries for 1MB text)
	var analyzedWords []string
	var totalTrigrams int

	if len(words) > MaxWordsForFullAnalysis {
		analyzedWords = sampleWords(words, SampleSizeForLargeText)
		totalTrigrams = len(words) - 2 // Use actual count for ratio calculation
	} else {
		analyzedWords = words
		totalTrigrams = len(words) - 2
	}

	trigrams := GenerateTrigrams(strings.Join(analyzedWords, " "))
	if trigrams == nil {
		return 1.0
	}

	uniqueTrigrams := len(trigrams)

	// Scale unique count if we sampled
	if len(words) > MaxWordsForFullAnalysis {
		// Scale factor: actual words / sampled words
		scaleFactor := float64(len(words)) / float64(len(analyzedWords))
		uniqueTrigrams = int(float64(uniqueTrigrams) * scaleFactor)
	}

	return float64(uniqueTrigrams) / float64(totalTrigrams)
}
