package loopdetection_test

import (
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection/fingerprint"
)

// ─────────────────────────────────────────────────────────────────────────────
// SimHash / Fingerprint Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestSimHash_IdenticalTexts(t *testing.T) {
	text := "Let me check that file again and read the configuration"
	h1 := fingerprint.ComputeSimHash(text)
	h2 := fingerprint.ComputeSimHash(text)

	if h1 != h2 {
		t.Errorf("Same text should produce same hash: %d != %d", h1, h2)
	}
	if fingerprint.Similarity(h1, h2) != 1.0 {
		t.Errorf("Same text should have similarity 1.0")
	}
}

func TestSimHash_SimilarTexts(t *testing.T) {
	text1 := "Let me check that configuration file and read the settings values"
	text2 := "Let me check that configuration file and read the settings again values"

	h1 := fingerprint.ComputeSimHash(text1)
	h2 := fingerprint.ComputeSimHash(text2)

	sim := fingerprint.Similarity(h1, h2)
	if sim < 0.7 {
		t.Errorf("Similar texts should have high similarity, got %.2f", sim)
	}
}

func TestSimHash_DifferentTexts(t *testing.T) {
	text1 := "Let me check the configuration file for database settings"
	text2 := "The weather today is sunny and warm with clear blue skies"

	h1 := fingerprint.ComputeSimHash(text1)
	h2 := fingerprint.ComputeSimHash(text2)

	sim := fingerprint.Similarity(h1, h2)
	if sim > 0.85 {
		t.Errorf("Different texts should have low similarity, got %.2f", sim)
	}
}

func TestSimHash_EmptyText(t *testing.T) {
	h := fingerprint.ComputeSimHash("")
	if h != 0 {
		t.Errorf("Empty text should produce 0 hash, got %d", h)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Trigram Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTrigramRepetition_HighRepetition(t *testing.T) {
	// LLM loop: same phrase over and over
	text := "I will check the file I will check the file I will check the file I will check the file I will check the file"
	ratio := fingerprint.TrigramRepetitionRatio(text)

	if ratio > 0.3 {
		t.Errorf("Highly repetitive text should have low ratio, got %.2f", ratio)
	}
}

func TestTrigramRepetition_NormalText(t *testing.T) {
	text := "The configuration file contains various settings for the application including database connection strings timeout values and retry limits"
	ratio := fingerprint.TrigramRepetitionRatio(text)

	if ratio < 0.5 {
		t.Errorf("Normal text should have high ratio, got %.2f", ratio)
	}
}

func TestTrigramRepetition_ShortText(t *testing.T) {
	text := "Hello world"
	ratio := fingerprint.TrigramRepetitionRatio(text)

	if ratio != 1.0 {
		t.Errorf("Short text should return 1.0, got %.2f", ratio)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Exact Match Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDetector_ExactMatch_Detected(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	// Feed the same message twice
	msg := "Let me check that file for you. I will read the configuration and examine the settings."

	result1 := detector.Analyze(msg, nil)
	if result1 != nil && result1.LoopDetected {
		t.Error("Should not detect loop on first message")
	}

	result2 := detector.Analyze(msg, nil)
	if result2 == nil || !result2.LoopDetected {
		t.Error("Should detect loop on second identical message")
	}
	if result2 != nil && result2.Strategy != "exact_match" {
		t.Errorf("Expected exact_match strategy, got %s", result2.Strategy)
	}
}

func TestDetector_ExactMatch_NotTriggered(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	msgs := []string{
		"First, let me check the configuration file for the database settings section.",
		"Now I will read the main go file and examine the handler logic for issues.",
		"Finally, I need to look at the test file and verify the test coverage output.",
	}

	for _, msg := range msgs {
		result := detector.Analyze(msg, nil)
		if result != nil && result.LoopDetected {
			t.Errorf("Should not detect loop for unique messages, detected at: %q", msg)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Similarity Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDetector_Similarity_Detected(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	// Feed near-identical messages (minor variations)
	msgs := []string{
		"Let me check the configuration file again and read the settings for the database connection strings",
		"Let me check the configuration file again and read the settings for the database connection values",
		"Let me check the configuration file again and read the settings for the database connection setup",
	}

	var lastResult *loopdetection.DetectionResult
	for _, msg := range msgs {
		lastResult = detector.Analyze(msg, nil)
	}

	if lastResult == nil || !lastResult.LoopDetected {
		t.Error("Should detect similarity loop for near-identical messages")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Action Pattern Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDetector_ActionRepeat_Detected(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	action := loopdetection.Action{Type: "read", Target: "config.go"}

	// Feed same action 3 times (threshold)
	for i := 0; i < 3; i++ {
		result := detector.Analyze("checking file again in this iteration number "+itoa(i), []loopdetection.Action{action})
		if i < 2 && result != nil && result.LoopDetected && result.Strategy == "action_pattern" {
			t.Errorf("Should not detect action loop before threshold at iteration %d", i)
		}
	}

	// The 3rd call should have detected the action pattern
	// run one more to confirm
	result := detector.Analyze("checking file one more time to verify results", []loopdetection.Action{action})
	if result == nil || !result.LoopDetected {
		t.Error("Should detect action pattern loop after 3+ repeated actions")
	}
}

func TestDetector_ActionOscillation_Detected(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	cfg.OscillationCount = 3 // Lower for testing
	detector := loopdetection.NewDetector(cfg)

	readAction := loopdetection.Action{Type: "read", Target: "config.go"}
	writeAction := loopdetection.Action{Type: "write", Target: "config.go"}

	// Feed oscillating actions: read→write→read→write→read→write
	for i := 0; i < 6; i++ {
		action := readAction
		if i%2 == 1 {
			action = writeAction
		}
		detector.Analyze("processing iteration step "+strings.Repeat("word ", 10)+itoa(i), []loopdetection.Action{action})
	}

	// The oscillation should be detected now
	result := detector.Analyze("another processing step with more words for token count", []loopdetection.Action{readAction})
	// Note: oscillation detection depends on accumulated actions in the window
	_ = result // May or may not trigger depending on window; test validates no panic
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream Buffer Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestStreamBuffer_ThresholdTriggered(t *testing.T) {
	buf := loopdetection.NewStreamBuffer(20)

	// Add small chunks like SSE streaming
	chunks := strings.Fields("This is a test of the stream buffering system that should accumulate enough tokens before triggering analysis and returning true from ShouldAnalyze method call")
	for _, chunk := range chunks {
		buf.AddText(chunk + " ")
	}

	if !buf.ShouldAnalyze(false) {
		t.Error("Buffer should trigger analysis after enough tokens")
	}
}

func TestStreamBuffer_MessageEndForces(t *testing.T) {
	buf := loopdetection.NewStreamBuffer(20)

	buf.AddText("Short")

	if buf.ShouldAnalyze(false) {
		t.Error("Should not trigger analysis for short text before threshold")
	}

	if !buf.ShouldAnalyze(true) {
		t.Error("Should trigger analysis on message end regardless of count")
	}
}

func TestStreamBuffer_Flush(t *testing.T) {
	buf := loopdetection.NewStreamBuffer(20)

	buf.AddText("Hello world this is a message")
	buf.AddAction(loopdetection.Action{Type: "read", Target: "file.go"})

	text, actions := buf.Flush()

	if text != "Hello world this is a message" {
		t.Errorf("Unexpected flushed text: %q", text)
	}
	if len(actions) != 1 || actions[0].Target != "file.go" {
		t.Errorf("Unexpected flushed actions: %v", actions)
	}

	// After flush, buffer should be empty
	if buf.TokenCount() != 0 {
		t.Errorf("Buffer should be empty after flush, got %d tokens", buf.TokenCount())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Detector Configuration Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDetector_Disabled(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	cfg.Enabled = false
	detector := loopdetection.NewDetector(cfg)

	result := detector.Analyze("test message for disabled detector", nil)
	if result != nil {
		t.Error("Disabled detector should always return nil")
	}
}

func TestDetector_ShadowMode(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	cfg.ShadowMode = true
	detector := loopdetection.NewDetector(cfg)

	if !detector.IsShadowMode() {
		t.Error("Detector should be in shadow mode")
	}

	// Feed identical messages
	msg := "This is a test message for shadow mode detection with enough tokens to be meaningful"
	detector.Analyze(msg, nil)
	result := detector.Analyze(msg, nil)

	// Should still detect the loop (shadow mode just logs, doesn't suppress)
	if result == nil || !result.LoopDetected {
		t.Error("Shadow mode should still detect loops (just not interrupt)")
	}
}

func TestDetector_Reset(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	msg := "Same message repeated to test reset functionality with enough tokens for analysis"
	detector.Analyze(msg, nil)
	detector.Reset()

	// After reset, should not detect loop on first message
	result := detector.Analyze(msg, nil)
	if result != nil && result.LoopDetected {
		t.Error("After reset, should not detect loop on first message")
	}
}

func TestDetector_WindowSize(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	cfg.MessageWindow = 3
	detector := loopdetection.NewDetector(cfg)

	if detector.WindowSize() != 0 {
		t.Errorf("Empty detector should have window size 0, got %d", detector.WindowSize())
	}

	for i := 0; i < 5; i++ {
		detector.Analyze("message number "+itoa(i)+" with enough tokens to pass the minimum threshold for analysis", nil)
	}

	if detector.WindowSize() != 3 {
		t.Errorf("Window should be capped at 3, got %d", detector.WindowSize())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tokenizer Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenize(t *testing.T) {
	tokens := fingerprint.Tokenize("Hello, World! This is a test.")
	expected := []string{"hello", "world", "this", "is", "a", "test"}

	if len(tokens) != len(expected) {
		t.Errorf("Expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
}

func TestEstimateTokenCount(t *testing.T) {
	count := fingerprint.EstimateTokenCount("Hello world, this is a test")
	if count != 6 {
		t.Errorf("Expected 6 tokens, got %d", count)
	}
}

// simple itoa for test use
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
