package fingerprint

import (
	"hash/fnv"
	"math/bits"
	"strings"
	"unicode"
)

// ComputeSimHash computes a 64-bit SimHash fingerprint of the given text.
// SimHash is a locality-sensitive hash: similar texts produce similar hashes.
func ComputeSimHash(text string) uint64 {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return 0
	}

	var weights [64]int64

	for _, token := range tokens {
		h := fnv1a64(token)
		for i := 0; i < 64; i++ {
			if h&(1<<uint(i)) != 0 {
				weights[i]++
			} else {
				weights[i]--
			}
		}
	}

	var result uint64
	for i := 0; i < 64; i++ {
		if weights[i] > 0 {
			result |= 1 << uint(i)
		}
	}
	return result
}

// HammingDistance returns the number of differing bits between two SimHash values.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// Similarity returns a similarity score between 0.0 and 1.0 for two SimHash values.
// 1.0 means identical, 0.0 means completely different.
func Similarity(a, b uint64) float64 {
	dist := HammingDistance(a, b)
	return 1.0 - float64(dist)/64.0
}

// Tokenize splits text into lowercase tokens, stripping punctuation.
func Tokenize(text string) []string {
	// Normalize: lowercase and split on non-alphanumeric
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return words
}

// EstimateTokenCount gives a rough token count by splitting on whitespace.
// This is a fast approximation, not a proper tokenizer.
func EstimateTokenCount(text string) int {
	return len(strings.Fields(text))
}

// fnv1a64 hashes a string using FNV-1a 64-bit.
func fnv1a64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}
