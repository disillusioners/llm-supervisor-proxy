package fingerprint

import (
	"strings"
	"testing"
)

func TestGenerateTrigrams(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantTrigrams map[string]int
		wantNil      bool
	}{
		{
			name:    "empty string returns nil",
			text:    "",
			wantNil: true,
		},
		{
			name:    "single word returns nil",
			text:    "hello",
			wantNil: true,
		},
		{
			name:    "two words returns nil",
			text:    "hello world",
			wantNil: true,
		},
		{
			name:         "three words returns 1 trigram",
			text:         "hello world today",
			wantTrigrams: map[string]int{"hello world today": 1},
			wantNil:      false,
		},
		{
			name: "four words returns 2 trigrams",
			text: "one two three four",
			wantTrigrams: map[string]int{
				"one two three":  1,
				"two three four": 1,
			},
			wantNil: false,
		},
		{
			name: "repeated trigrams counted correctly",
			text: "a b a b a b",
			wantTrigrams: map[string]int{
				"a b a": 2,
				"b a b": 2,
			},
			wantNil: false,
		},
		{
			name: "longer text with unique trigrams",
			text: "the quick brown fox jumps over the lazy dog",
			wantTrigrams: map[string]int{
				"the quick brown": 1,
				"quick brown fox": 1,
				"brown fox jumps": 1,
				"fox jumps over":  1,
				"jumps over the":  1,
				"over the lazy":   1,
				"the lazy dog":    1,
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTrigrams(tt.text)

			if tt.wantNil {
				if got != nil {
					t.Errorf("GenerateTrigrams(%q) = %v, want nil", tt.text, got)
				}
				return
			}

			if got == nil {
				t.Errorf("GenerateTrigrams(%q) = nil, want non-nil map %v", tt.text, tt.wantTrigrams)
				return
			}

			// Check count
			if len(got) != len(tt.wantTrigrams) {
				t.Errorf("GenerateTrigrams(%q) has %d trigrams, want %d", tt.text, len(got), len(tt.wantTrigrams))
			}

			// Check content
			for trigram, count := range tt.wantTrigrams {
				if got[trigram] != count {
					t.Errorf("GenerateTrigrams(%q)[%q] = %d, want %d", tt.text, trigram, got[trigram], count)
				}
			}

			// Verify exact trigram exists
			for trigram := range got {
				if _, ok := tt.wantTrigrams[trigram]; !ok {
					t.Errorf("GenerateTrigrams(%q) contains unexpected trigram %q", tt.text, trigram)
				}
			}
		})
	}
}

func TestGenerateTrigramsFromWords(t *testing.T) {
	tests := []struct {
		name         string
		words        []string
		wantTrigrams map[string]int
		wantNil      bool
	}{
		{
			name:    "nil slice returns nil",
			words:   nil,
			wantNil: true,
		},
		{
			name:    "empty slice returns nil",
			words:   []string{},
			wantNil: true,
		},
		{
			name:    "single word returns nil",
			words:   []string{"hello"},
			wantNil: true,
		},
		{
			name:    "two words returns nil",
			words:   []string{"hello", "world"},
			wantNil: true,
		},
		{
			name:         "three words returns 1 trigram",
			words:        []string{"hello", "world", "today"},
			wantTrigrams: map[string]int{"hello world today": 1},
			wantNil:      false,
		},
		{
			name:  "four words returns 2 trigrams",
			words: []string{"one", "two", "three", "four"},
			wantTrigrams: map[string]int{
				"one two three":  1,
				"two three four": 1,
			},
			wantNil: false,
		},
		{
			name:  "repeated trigrams counted correctly",
			words: []string{"a", "b", "a", "b", "a", "b"},
			wantTrigrams: map[string]int{
				"a b a": 2,
				"b a b": 2,
			},
			wantNil: false,
		},
		{
			name:  "mixed content",
			words: []string{"the", "cat", "sat", "on", "the", "mat", "the", "cat"},
			wantTrigrams: map[string]int{
				"the cat sat": 1,
				"cat sat on":  1,
				"sat on the":  1,
				"on the mat":  1,
				"the mat the": 1,
				"mat the cat": 1,
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTrigramsFromWords(tt.words)

			if tt.wantNil {
				if got != nil {
					t.Errorf("GenerateTrigramsFromWords(%v) = %v, want nil", tt.words, got)
				}
				return
			}

			if got == nil {
				t.Errorf("GenerateTrigramsFromWords(%v) = nil, want non-nil map %v", tt.words, tt.wantTrigrams)
				return
			}

			if len(got) != len(tt.wantTrigrams) {
				t.Errorf("GenerateTrigramsFromWords(%v) has %d trigrams, want %d", tt.words, len(got), len(tt.wantTrigrams))
			}

			for trigram, count := range tt.wantTrigrams {
				if got[trigram] != count {
					t.Errorf("GenerateTrigramsFromWords(%v)[%q] = %d, want %d", tt.words, trigram, got[trigram], count)
				}
			}
		})
	}
}

func TestTrigramRepetitionRatio(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantRatio float64
		wantExact bool // if true, wantRatio must be exact; if false, just check range
	}{
		{
			name:      "empty string returns 1.0",
			text:      "",
			wantRatio: 1.0,
			wantExact: true,
		},
		{
			name:      "single word returns 1.0",
			text:      "hello",
			wantRatio: 1.0,
			wantExact: true,
		},
		{
			name:      "9 words returns 1.0 (below threshold)",
			text:      "one two three four five six seven eight nine",
			wantRatio: 1.0,
			wantExact: true,
		},
		{
			name:      "10 words returns unique ratio",
			text:      "a b c d e f g h i j",
			wantRatio: 1.0, // All unique trigrams
			wantExact: true,
		},
		{
			name:      "fully repeated returns low ratio",
			text:      strings.Repeat("hello world test ", 20),
			wantRatio: 0.1, // Should be very low (all same trigrams)
			wantExact: false,
		},
		{
			name:      "mixed content returns reasonable ratio",
			text:      "the quick brown fox jumps over the lazy dog and the quick brown fox",
			wantRatio: 0.75, // Most trigrams unique
			wantExact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrigramRepetitionRatio(tt.text)

			if tt.wantExact {
				if got != tt.wantRatio {
					t.Errorf("TrigramRepetitionRatio(%q) = %v, want %v", tt.text, got, tt.wantRatio)
				}
			} else {
				if got < tt.wantRatio-0.2 || got > tt.wantRatio+0.2 {
					t.Errorf("TrigramRepetitionRatio(%q) = %v, want ~%v (±0.2)", tt.text, got, tt.wantRatio)
				}
			}
		})
	}
}

func TestTrigramRepetitionRatio_LargeText(t *testing.T) {
	// Test that large text (> MaxWordsForFullAnalysis) doesn't panic
	// and handles correctly by analyzing only the tail
	largeText := strings.Repeat("word1 word2 word3 word4 word5 ", 2000) // 10000 words

	got := TrigramRepetitionRatio(largeText)

	// Should not panic and should return a valid ratio
	if got < 0.0 || got > 1.0 {
		t.Errorf("TrigramRepetitionRatio(largeText) = %v, want value between 0 and 1", got)
	}
}

func TestComputeSimHash(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantHash uint64
		wantZero bool
	}{
		{
			name:     "empty string returns 0",
			text:     "",
			wantZero: true,
		},
		{
			name:     "whitespace only returns 0",
			text:     "   ",
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSimHash(tt.text)
			if tt.wantZero && got != 0 {
				t.Errorf("ComputeSimHash(%q) = %d, want 0", tt.text, got)
			}
		})
	}

	// Test determinism - same input should produce same hash
	hash1 := ComputeSimHash("hello world")
	hash2 := ComputeSimHash("hello world")
	if hash1 != hash2 {
		t.Errorf("ComputeSimHash not deterministic: first = %d, second = %d", hash1, hash2)
	}

	// Test that different strings produce (likely) different hashes
	hashA := ComputeSimHash("hello world")
	hashB := ComputeSimHash("goodbye world")
	if hashA == hashB {
		t.Errorf("ComputeSimHash produced same hash for different inputs: %d", hashA)
	}

	// Test similar strings have low hamming distance
	hashShort := ComputeSimHash("the quick brown fox")
	hashSimilar := ComputeSimHash("the quick brown fox jumps")
	dist := HammingDistance(hashShort, hashSimilar)
	if dist > 20 { // Similar texts should have low hamming distance
		t.Errorf("Similar texts have high hamming distance: %d", dist)
	}
}

func TestHammingDistance(t *testing.T) {
	tests := []struct {
		name     string
		a, b     uint64
		wantDist int
	}{
		{
			name:     "same values return 0",
			a:        0xFFFFFFFFFFFFFFFF,
			b:        0xFFFFFFFFFFFFFFFF,
			wantDist: 0,
		},
		{
			name:     "zero values return 0",
			a:        0,
			b:        0,
			wantDist: 0,
		},
		{
			name:     "single bit difference",
			a:        0xFFFFFFFFFFFFFFFE,
			b:        0xFFFFFFFFFFFFFFFF,
			wantDist: 1,
		},
		{
			name:     "half bits different",
			a:        0xFFFFFFFFFFFFFFFF,
			b:        0x0000000000000000,
			wantDist: 64,
		},
		{
			name:     "known value 0x0000 and 0xFFFF",
			a:        0x0000,
			b:        0xFFFF,
			wantDist: 16, // lower 16 bits differ
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HammingDistance(tt.a, tt.b)
			if got != tt.wantDist {
				t.Errorf("HammingDistance(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.wantDist)
			}
		})
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		name        string
		a, b        uint64
		wantSimilar float64
		wantExact   bool
	}{
		{
			name:        "same value returns 1.0",
			a:           0xFFFFFFFFFFFFFFFF,
			b:           0xFFFFFFFFFFFFFFFF,
			wantSimilar: 1.0,
			wantExact:   true,
		},
		{
			name:        "completely different returns 0.0",
			a:           0xFFFFFFFFFFFFFFFF,
			b:           0x0000000000000000,
			wantSimilar: 0.0,
			wantExact:   true,
		},
		{
			name:        "single bit different returns high similarity",
			a:           0xFFFFFFFFFFFFFFFE,
			b:           0xFFFFFFFFFFFFFFFF,
			wantSimilar: 63.0 / 64.0, // 63/64
			wantExact:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Similarity(tt.a, tt.b)
			if tt.wantExact && got != tt.wantSimilar {
				t.Errorf("Similarity(%d, %d) = %v, want %v", tt.a, tt.b, got, tt.wantSimilar)
			}
		})
	}

	// Verify Similarity is symmetric
	hash1 := ComputeSimHash("hello world")
	hash2 := ComputeSimHash("hello world test")
	sim1 := Similarity(hash1, hash2)
	sim2 := Similarity(hash2, hash1)
	if sim1 != sim2 {
		t.Errorf("Similarity not symmetric: Sim(a,b)=%v, Sim(b,a)=%v", sim1, sim2)
	}

	// Verify similarity is in range [0, 1]
	for _, text := range []string{"a", "test string", "the quick brown fox"} {
		h1 := ComputeSimHash(text)
		h2 := ComputeSimHash(strings.ToLower(text))
		sim := Similarity(h1, h2)
		if sim < 0.0 || sim > 1.0 {
			t.Errorf("Similarity out of range: %v", sim)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "empty string",
			text: "",
			want: nil,
		},
		{
			name: "whitespace only",
			text: "   \t\n  ",
			want: nil,
		},
		{
			name: "simple words",
			text: "hello world",
			want: []string{"hello", "world"},
		},
		{
			name: "case normalization to lowercase",
			text: "Hello WORLD Test",
			want: []string{"hello", "world", "test"},
		},
		{
			name: "punctuation stripped",
			text: "hello, world! how are you?",
			want: []string{"hello", "world", "how", "are", "you"},
		},
		{
			name: "mixed alphanumeric",
			text: "hello123 world456",
			want: []string{"hello123", "world456"},
		},
		{
			name: "numbers preserved",
			text: "test123 and 456number",
			want: []string{"test123", "and", "456number"},
		},
		{
			name: "unicode characters",
			text: "héllo wörld 123",
			want: []string{"héllo", "wörld", "123"},
		},
		{
			name: "underscores split (not alphanumeric)",
			text: "hello_world test_case",
			want: []string{"hello", "world", "test", "case"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.text)

			if tt.want == nil {
				if len(got) != 0 {
					t.Errorf("Tokenize(%q) = %v, want empty/nil", tt.text, got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("Tokenize(%q) returned %d tokens, want %d", tt.text, len(got), len(tt.want))
				return
			}

			for i, token := range got {
				if token != tt.want[i] {
					t.Errorf("Tokenize(%q)[%d] = %q, want %q", tt.text, i, token, tt.want[i])
				}
			}
		})
	}
}

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "empty string",
			text: "",
			want: 0,
		},
		{
			name: "whitespace only",
			text: "   \t\n  ",
			want: 0,
		},
		{
			name: "single word",
			text: "hello",
			want: 1,
		},
		{
			name: "two words",
			text: "hello world",
			want: 2,
		},
		{
			name: "multiple words with spaces",
			text: "the quick brown fox",
			want: 4,
		},
		{
			name: "multiple spaces between words",
			text: "hello    world   test",
			want: 3,
		},
		{
			name: "tabs and newlines",
			text: "hello\tworld\ntest",
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokenCount(tt.text)
			if got != tt.want {
				t.Errorf("EstimateTokenCount(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

// Benchmark tests for performance reference
func BenchmarkGenerateTrigrams(b *testing.B) {
	text := strings.Repeat("the quick brown fox jumps over the lazy dog ", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateTrigrams(text)
	}
}

func BenchmarkComputeSimHash(b *testing.B) {
	text := strings.Repeat("the quick brown fox jumps over the lazy dog ", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeSimHash(text)
	}
}

func BenchmarkTokenize(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog, testing 123!", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Tokenize(text)
	}
}
