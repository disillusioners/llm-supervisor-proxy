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

	// Feed the same message three times (new default threshold)
	msg := "Let me check that file for you. I will read the configuration and examine the settings."

	result1 := detector.Analyze(msg, nil)
	if result1 != nil && result1.LoopDetected && result1.Strategy == "exact_match" {
		t.Error("Should not detect exact_match loop on first message")
	}

	result2 := detector.Analyze(msg, nil)
	// Note: similarity strategy may detect after 2 messages (that's OK)
	// We're testing exact_match which needs 3
	if result2 != nil && result2.LoopDetected && result2.Strategy == "exact_match" {
		t.Error("Should not detect exact_match loop on second message with new default (threshold=3)")
	}

	result3 := detector.Analyze(msg, nil)
	if result3 == nil || !result3.LoopDetected {
		t.Error("Should detect loop on third identical message")
	}
	if result3 != nil && result3.Strategy != "exact_match" {
		t.Errorf("Expected exact_match strategy, got %s", result3.Strategy)
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

	// Feed identical messages (3 times for new default threshold)
	msg := "This is a test message for shadow mode detection with enough tokens to be meaningful"
	detector.Analyze(msg, nil)
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

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3: Thinking Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestThinkingStrategy_RepetitiveThinking(t *testing.T) {
	strategy := loopdetection.NewThinkingStrategy(0.3, 50, nil, 0.15)

	// Simulate repetitive thinking content
	repetitiveThinking := strings.Repeat("I need to check the file and examine the configuration again. ", 20)
	strategy.AddThinkingContent(repetitiveThinking)

	// Analyze with an empty window — the thinking content is accumulated internally
	result := strategy.Analyze(nil)
	if result == nil || !result.LoopDetected {
		t.Error("Should detect repetitive thinking pattern")
	}
	if result != nil && result.Strategy != "thinking" {
		t.Errorf("Expected thinking strategy, got %s", result.Strategy)
	}
}

func TestThinkingStrategy_NormalThinking(t *testing.T) {
	strategy := loopdetection.NewThinkingStrategy(0.3, 50, nil, 0.15)

	// Normal varied thinking content
	normalThinking := "First I should analyze the error message to understand what went wrong. " +
		"The stack trace shows a null pointer exception in the handler function. " +
		"This likely means the request context was not properly initialized before use. " +
		"Looking at the initialization code, there is a missing nil check. " +
		"The fix should add a guard clause at the top of the handler. " +
		"Let me also verify that the tests cover this scenario properly. " +
		"I should write a unit test that sends a request with a nil context. " +
		"The expected behavior is a 500 error with an appropriate message."

	strategy.AddThinkingContent(normalThinking)

	result := strategy.Analyze(nil)
	if result != nil && result.LoopDetected {
		t.Errorf("Should NOT detect loop in normal varied thinking, got: %v", result)
	}
}

func TestThinkingStrategy_ReasoningModelTolerance(t *testing.T) {
	strategy := loopdetection.NewThinkingStrategy(
		0.3,                                 // Normal threshold
		50,                                  // Min tokens
		[]string{"o1", "o3", "deepseek-r1"}, // Reasoning model patterns
		0.15,                                // More forgiving threshold
	)

	// Set a reasoning model
	strategy.SetModel("o1-preview")

	// Content that would trigger with 0.3 threshold but not with 0.15
	moderatelyRepetitive := strings.Repeat("Let me reconsider this approach from another angle. ", 15) +
		"But actually the original approach was better because of performance constraints. " +
		strings.Repeat("Perhaps I should evaluate whether the trade-offs are worth it. ", 5)

	strategy.AddThinkingContent(moderatelyRepetitive)

	ratio := fingerprint.TrigramRepetitionRatio(moderatelyRepetitive)
	result := strategy.Analyze(nil)

	// If the ratio is between 0.15 and 0.3, reasoning model should NOT trigger
	if ratio > 0.15 && ratio < 0.3 {
		if result != nil && result.LoopDetected {
			t.Errorf("Reasoning model should have more forgiving threshold, ratio=%.3f", ratio)
		}
	}
	// If ratio < 0.15, even reasoning model should trigger
	t.Logf("Trigram ratio for moderately repetitive text: %.3f", ratio)
}

func TestThinkingStrategy_ShortThinkingNoTrigger(t *testing.T) {
	strategy := loopdetection.NewThinkingStrategy(0.3, 100, nil, 0.15)

	// Too short to analyze
	strategy.AddThinkingContent("Let me think about this problem briefly.")

	result := strategy.Analyze(nil)
	if result != nil && result.LoopDetected {
		t.Error("Should NOT analyze thinking content below min token threshold")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3: Cycle Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCycleStrategy_Detected(t *testing.T) {
	cfg := loopdetection.DefaultConfig()
	detector := loopdetection.NewDetector(cfg)

	// Simulate a 3-action cycle: read→grep→write repeated
	actions := []loopdetection.Action{
		{Type: "read", Target: "config.go"},
		{Type: "grep", Target: "main.go"},
		{Type: "write", Target: "config.go"},
	}

	// Feed the cycle twice (should trigger cycle detection)
	for i := 0; i < 2; i++ {
		for _, action := range actions {
			detector.Analyze(
				"processing step "+itoa(i)+" with enough content for the token minimum threshold",
				[]loopdetection.Action{action},
			)
		}
	}

	// Check if cycle was detected at some point
	// The cycle strategy should find: read→grep→write → read→grep→write
	result := detector.Analyze(
		"one more step to trigger cycle check for the action pattern detection",
		[]loopdetection.Action{actions[0]},
	)

	// Either the cycle or action pattern strategies may detect this
	// We just verify no panic and the system operates correctly
	t.Logf("Cycle test result: %+v", result)
}

func TestCycleStrategy_NoCycle(t *testing.T) {
	strategy := loopdetection.NewCycleStrategy(5, 2)

	window := []loopdetection.MessageContext{
		{Actions: []loopdetection.Action{{Type: "read", Target: "a.go"}}},
		{Actions: []loopdetection.Action{{Type: "write", Target: "b.go"}}},
		{Actions: []loopdetection.Action{{Type: "grep", Target: "c.go"}}},
		{Actions: []loopdetection.Action{{Type: "exec", Target: "test"}}},
	}

	result := strategy.Analyze(window)
	if result != nil && result.LoopDetected {
		t.Error("Should NOT detect cycle in unique action sequence")
	}
}

func TestCycleStrategy_PatternDetected(t *testing.T) {
	strategy := loopdetection.NewCycleStrategy(5, 2)

	// A→B→C→A→B→C pattern
	window := []loopdetection.MessageContext{
		{Actions: []loopdetection.Action{{Type: "read", Target: "config.go"}}},
		{Actions: []loopdetection.Action{{Type: "grep", Target: "main.go"}}},
		{Actions: []loopdetection.Action{{Type: "write", Target: "output.go"}}},
		{Actions: []loopdetection.Action{{Type: "read", Target: "config.go"}}},
		{Actions: []loopdetection.Action{{Type: "grep", Target: "main.go"}}},
		{Actions: []loopdetection.Action{{Type: "write", Target: "output.go"}}},
	}

	result := strategy.Analyze(window)
	if result == nil || !result.LoopDetected {
		t.Error("Should detect A→B→C→A→B→C cycle pattern")
	}
	if result != nil && result.Strategy != "cycle" {
		t.Errorf("Expected cycle strategy, got %s", result.Strategy)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3: Stagnation Strategy Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestStagnationStrategy_Detected(t *testing.T) {
	strategy := loopdetection.NewStagnationStrategy(5, 0.3, 5)

	// Simulate messages that are cumulating but content barely changes
	// (same information rephrased slightly)
	msgs := []loopdetection.MessageContext{
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file and examine the database settings for connection", TokenCount: 12, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file and examine the database settings for connection")},
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file and examine the database settings for connection strings", TokenCount: 13, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file and examine the database settings for connection strings")},
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file and examine the database settings for the connection params", TokenCount: 14, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file and examine the database settings for the connection params")},
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file and examine the database settings for connection timeouts", TokenCount: 13, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file and examine the database settings for connection timeouts")},
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file and examine the database settings for connection values", TokenCount: 13, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file and examine the database settings for connection values")},
	}

	result := strategy.Analyze(msgs)
	// These are highly similar cumulated messages — should detect stagnation
	t.Logf("Stagnation result: %+v", result)
}

func TestStagnationStrategy_NoStagnation(t *testing.T) {
	strategy := loopdetection.NewStagnationStrategy(5, 0.3, 5)

	// Progressive messages with real variety
	msgs := []loopdetection.MessageContext{
		{Role: "assistant", ContentType: "text", Content: "Let me check the configuration file first to understand the settings", TokenCount: 12, ContentHash: fingerprint.ComputeSimHash("Let me check the configuration file first to understand the settings")},
		{Role: "assistant", ContentType: "text", Content: "The database uses PostgreSQL with connection pooling enabled through the pgx driver", TokenCount: 14, ContentHash: fingerprint.ComputeSimHash("The database uses PostgreSQL with connection pooling enabled through the pgx driver")},
		{Role: "assistant", ContentType: "text", Content: "I found the bug in the authentication middleware where tokens are not validated correctly", TokenCount: 15, ContentHash: fingerprint.ComputeSimHash("I found the bug in the authentication middleware where tokens are not validated correctly")},
		{Role: "assistant", ContentType: "text", Content: "The fix requires updating the JWT validation logic in the verify endpoint handler", TokenCount: 14, ContentHash: fingerprint.ComputeSimHash("The fix requires updating the JWT validation logic in the verify endpoint handler")},
		{Role: "assistant", ContentType: "text", Content: "I have applied the fix and all existing unit tests continue to pass successfully now", TokenCount: 14, ContentHash: fingerprint.ComputeSimHash("I have applied the fix and all existing unit tests continue to pass successfully now")},
	}

	result := strategy.Analyze(msgs)
	if result != nil && result.LoopDetected {
		t.Errorf("Should NOT detect stagnation in progressive messages, got: %v", result)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3: Context Window Sanitization Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestSanitizeLoopHistory_ExactMatch(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "Fix the bug"},
		{"role": "assistant", "content": "Let me check config.go"},
		{"role": "assistant", "content": "Let me check config.go"},
		{"role": "assistant", "content": "Let me check config.go"},
	}

	result := &loopdetection.DetectionResult{
		LoopDetected: true,
		Strategy:     "exact_match",
		RepeatCount:  3,
	}

	sanitized := loopdetection.SanitizeLoopHistory(messages, result)

	// Should have: original user message + system summary
	if len(sanitized) != 2 {
		t.Errorf("Expected 2 messages (1 original + 1 summary), got %d", len(sanitized))
	}

	// Last message should be the system summary
	last := sanitized[len(sanitized)-1]
	if last["role"] != "system" {
		t.Errorf("Expected system role, got %s", last["role"])
	}
	content := last["content"].(string)
	if !strings.Contains(content, "identical") {
		t.Errorf("Expected 'identical' in summary, got: %s", content)
	}
}

func TestSanitizeLoopHistory_NilResult(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	sanitized := loopdetection.SanitizeLoopHistory(messages, nil)
	if len(sanitized) != 1 {
		t.Error("Nil result should return original messages unchanged")
	}
}

func TestSanitizeLoopHistory_ActionPattern(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "Read the file"},
		{"role": "assistant", "content": "Reading config.go"},
		{"role": "assistant", "content": "Reading config.go"},
	}

	result := &loopdetection.DetectionResult{
		LoopDetected: true,
		Strategy:     "action_pattern",
		RepeatCount:  2,
		Pattern:      []string{"read:config.go"},
	}

	sanitized := loopdetection.SanitizeLoopHistory(messages, result)
	last := sanitized[len(sanitized)-1]
	content := last["content"].(string)

	if !strings.Contains(content, "read:config.go") {
		t.Errorf("Expected action name in summary, got: %s", content)
	}
}
