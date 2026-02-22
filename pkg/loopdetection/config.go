package loopdetection

// Config holds all tunable parameters for loop detection.
type Config struct {
	Enabled bool `json:"enabled"`

	// Window sizes
	MessageWindow int `json:"message_window"` // Default: 10
	ActionWindow  int `json:"action_window"`  // Default: 15

	// Exact matching
	ExactMatchCount int `json:"exact_match_count"` // Default: 3

	// Similarity detection
	SimilarityThreshold float64 `json:"similarity_threshold"`   // Default: 0.85
	MinTokensForSimHash int     `json:"min_tokens_for_simhash"` // Default: 15

	// Action pattern detection
	ActionRepeatCount int `json:"action_repeat_count"` // Default: 3
	OscillationCount  int `json:"oscillation_count"`   // Default: 4

	// Stream processing
	MinTokensForAnalysis int `json:"min_tokens_for_analysis"` // Default: 20

	// Thinking analysis (Phase 3)
	ThinkingMinTokens int     `json:"thinking_min_tokens"` // Default: 100
	TrigramThreshold  float64 `json:"trigram_threshold"`   // Default: 0.3

	// Cycle detection (Phase 3)
	MaxCycleLength int `json:"max_cycle_length"` // Default: 5

	// Reasoning model special handling (Phase 3)
	ReasoningModelPatterns    []string `json:"reasoning_model_patterns"`    // Default: ["o1", "o3", "deepseek-r1"]
	ReasoningTrigramThreshold float64  `json:"reasoning_trigram_threshold"` // Default: 0.15

	// Actions
	ShadowMode bool `json:"shadow_mode"` // Default: true (log only, no interrupt)
}

// DefaultConfig returns the default loop detection configuration.
// Shadow mode is ON by default — detection results are logged but
// never interrupt the request.
func DefaultConfig() Config {
	return Config{
		Enabled:                   true,
		MessageWindow:             10,
		ActionWindow:              15,
		ExactMatchCount:           3,
		SimilarityThreshold:       0.85,
		MinTokensForSimHash:       15,
		ActionRepeatCount:         3,
		OscillationCount:          4,
		MinTokensForAnalysis:      20,
		ThinkingMinTokens:         100,
		TrigramThreshold:          0.3,
		MaxCycleLength:            5,
		ReasoningModelPatterns:    []string{"o1", "o3", "deepseek-r1"},
		ReasoningTrigramThreshold: 0.15,
		ShadowMode:                true,
	}
}
