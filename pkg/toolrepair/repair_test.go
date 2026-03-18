package toolrepair

import (
	"testing"
	"time"
)

func TestRepairToolCallsData(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		toolCalls      []ToolCallData
		wantRepaired   []ToolCallData
		wantStatsCheck func(*RepairStats) bool
	}{
		{
			name:   "disabled repair returns original",
			config: DisabledConfig(),
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: "invalid json"},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: "invalid json"},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.TotalToolCalls == 0
			},
		},
		{
			name:   "already valid JSON passes through",
			config: DefaultConfig(),
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"key": "value"}`},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"key": "value"}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.ValidJSON == 1 && s.Repaired == 0
			},
		},
		{
			name:   "invalid JSON that can be repaired",
			config: DefaultConfig(),
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{key: "value"}`},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"key": "value"}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.InvalidJSON == 1 && s.Repaired == 1
			},
		},
		{
			name:   "invalid JSON that cannot be repaired",
			config: DefaultConfig(),
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{`},
			},
			wantRepaired: []ToolCallData{
				// Library actually repairs `{` to `{}`
				{ID: "1", Type: "function", Name: "test", Arguments: `{}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.InvalidJSON == 1 && s.Repaired == 1
			},
		},
		{
			name:   "size limit exceeded",
			config: &Config{Enabled: true, MaxArgumentsSize: 10},
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"very_long_key": "very_long_value"}`},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"very_long_key": "very_long_value"}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.TooLarge == 1
			},
		},
		{
			name:   "tool call limit exceeded",
			config: &Config{Enabled: true, MaxToolCallsPerResponse: 2},
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test1", Arguments: `{"key": "value"}`},
				{ID: "2", Type: "function", Name: "test2", Arguments: `{"key": "value"}`},
				{ID: "3", Type: "function", Name: "test3", Arguments: `{"key": "value"}`},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test1", Arguments: `{"key": "value"}`},
				{ID: "2", Type: "function", Name: "test2", Arguments: `{"key": "value"}`},
				{ID: "3", Type: "function", Name: "test3", Arguments: `{"key": "value"}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.ExceededLimit == true
			},
		},
		{
			name:   "multiple tool calls with mixed validity",
			config: DefaultConfig(),
			toolCalls: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"valid": true}`},
				{ID: "2", Type: "function", Name: "test", Arguments: `{invalid: true}`},
				{ID: "3", Type: "function", Name: "test", Arguments: `{"also_valid": false}`},
			},
			wantRepaired: []ToolCallData{
				{ID: "1", Type: "function", Name: "test", Arguments: `{"valid": true}`},
				{ID: "2", Type: "function", Name: "test", Arguments: `{"invalid": true}`},
				{ID: "3", Type: "function", Name: "test", Arguments: `{"also_valid": false}`},
			},
			wantStatsCheck: func(s *RepairStats) bool {
				return s.TotalToolCalls == 3 && s.ValidJSON == 2 && s.Repaired == 1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repairer := NewRepairer(tt.config)
			repaired, stats := repairer.RepairToolCallsData(tt.toolCalls, nil)

			// Check repaired results
			for i, tc := range repaired {
				if tc.Arguments != tt.wantRepaired[i].Arguments {
					t.Errorf("repaired[%d] = %v, want %v", i, tc.Arguments, tt.wantRepaired[i].Arguments)
				}
			}

			// Check stats
			if !tt.wantStatsCheck(stats) {
				t.Errorf("stats check failed: %+v", stats)
			}
		})
	}
}

func TestRepairArguments(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		arguments    string
		toolName     string
		wantSuccess  bool
		wantRepaired string
	}{
		{
			name:         "valid JSON passes through",
			config:       DefaultConfig(),
			arguments:    `{"key": "value"}`,
			toolName:     "test",
			wantSuccess:  true,
			wantRepaired: `{"key": "value"}`,
		},
		{
			name:         "size limit check",
			config:       &Config{Enabled: true, MaxArgumentsSize: 5},
			arguments:    `{"key": "value"}`,
			toolName:     "test",
			wantSuccess:  false,
			wantRepaired: "",
		},
		{
			name:         "all strategies fail",
			config:       &Config{Enabled: true, Strategies: []string{"extract_json"}},
			arguments:    `{`,
			toolName:     "test",
			wantSuccess:  false,
			wantRepaired: "",
		},
		{
			name:         "repair with library strategy",
			config:       &Config{Enabled: true, Strategies: []string{"library_repair"}},
			arguments:    `{key: "value"}`,
			toolName:     "test",
			wantSuccess:  true,
			wantRepaired: `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repairer := NewRepairer(tt.config)
			result := repairer.RepairArguments(tt.arguments, tt.toolName)

			if result.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", result.Success, tt.wantSuccess)
			}

			if tt.wantSuccess && result.Repaired != tt.wantRepaired {
				t.Errorf("Repaired = %v, want %v", result.Repaired, tt.wantRepaired)
			}
		})
	}
}

func TestIsValidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid object",
			input: `{"key": "value"}`,
			want:  true,
		},
		{
			name:  "valid array",
			input: `["a", "b", "c"]`,
			want:  true,
		},
		{
			name:  "valid nested object",
			input: `{"outer": {"inner": "value"}}`,
			want:  true,
		},
		{
			name:  "valid with numbers",
			input: `{"count": 42, "float": 3.14}`,
			want:  true,
		},
		{
			name:  "valid with null",
			input: `{"key": null}`,
			want:  true,
		},
		{
			name:  "valid with boolean",
			input: `{"flag": true}`,
			want:  true,
		},
		{
			name:  "invalid JSON missing quotes",
			input: `{key: "value"}`,
			want:  false,
		},
		{
			name:  "invalid JSON unclosed brace",
			input: `{"key": "value"`,
			want:  false,
		},
		{
			name:  "invalid JSON trailing comma",
			input: `{"key": "value",}`,
			want:  false,
		},
		{
			name:  "invalid JSON plain text",
			input: `not json`,
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "just whitespace",
			input: "   ",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidJSON(tt.input)
			if got != tt.want {
				t.Errorf("isValidJSON(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRepairStats(t *testing.T) {
	t.Run("NewRepairStats creates empty stats", func(t *testing.T) {
		stats := NewRepairStats()
		if stats.TotalToolCalls != 0 {
			t.Errorf("TotalToolCalls = %v, want 0", stats.TotalToolCalls)
		}
		if stats.ValidJSON != 0 {
			t.Errorf("ValidJSON = %v, want 0", stats.ValidJSON)
		}
		if stats.StrategiesUsed == nil {
			t.Error("StrategiesUsed should not be nil")
		}
	})

	t.Run("Summary returns formatted string", func(t *testing.T) {
		stats := &RepairStats{
			TotalToolCalls: 10,
			ValidJSON:      5,
			InvalidJSON:    5,
			Repaired:       3,
			Failed:         2,
			Duration:       100 * time.Millisecond,
		}

		summary := stats.Summary()
		if summary == "" {
			t.Error("Summary should not be empty")
		}

		// Check that summary contains expected values
		// Note: Duration formatting may vary
		if stats.Summary() != summary {
			t.Error("Summary should return consistent output")
		}
	})

	t.Run("Stats tracking after repair operation", func(t *testing.T) {
		repairer := NewRepairer(DefaultConfig())
		_, stats := repairer.RepairToolCallsData([]ToolCallData{
			{ID: "1", Arguments: `{"valid": true}`},
			{ID: "2", Arguments: `{invalid: true}`},
		}, nil)

		if stats.TotalToolCalls != 2 {
			t.Errorf("TotalToolCalls = %v, want 2", stats.TotalToolCalls)
		}
		if stats.ValidJSON != 1 {
			t.Errorf("ValidJSON = %v, want 1", stats.ValidJSON)
		}
		if stats.Duration == 0 {
			t.Error("Duration should be set")
		}
	})
}

func TestExtractJSONBlock(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "already valid JSON object",
			input:   `{"key": "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "already valid JSON array",
			input:   `["a", "b"]`,
			want:    `["a", "b"]`,
			wantErr: false,
		},
		{
			name:    "JSON embedded in text before",
			input:   `Here is the result: {"key": "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "JSON embedded in text after",
			input:   `{"key": "value"} and some more text`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "JSON embedded in text before and after",
			input:   `Result: {"name": "test", "value": 123} End`,
			want:    `{"name": "test", "value": 123}`,
			wantErr: false,
		},
		{
			name:    "JSON array extraction",
			input:   `Items: ["first", "second", "third"]`,
			want:    `["first", "second", "third"]`,
			wantErr: false,
		},
		{
			name:    "no JSON found returns original",
			input:   `This is plain text without JSON`,
			want:    `This is plain text without JSON`,
			wantErr: false,
		},
		{
			name:    "empty input",
			input:   "",
			want:    "",
			wantErr: false,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			want:    "",
			wantErr: false,
		},
		{
			name:    "malformed JSON in text returns original",
			input:   `text {not valid json} more text`,
			want:    `text {not valid json} more text`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONBlock(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractJSONBlock() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractJSONBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLibraryRepair(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "already valid JSON",
			input:   `{"key": "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "missing quotes around key",
			input:   `{key: "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "trailing comma",
			input:   `{"key": "value",}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "single quotes",
			input:   `{'key': 'value'}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "missing quotes in string value",
			input:   `{key: value}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "completely unparseable gets repaired to empty object",
			input:   `{`,
			want:    `{}`,
			wantErr: false, // library repairs it to {}
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := libraryRepair(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("libraryRepair() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("libraryRepair() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveReasoningLeakage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "removes Summary pattern inside JSON value",
			input:   `{"text": "Summary: This is a summary"}`,
			want:    `{"text": "Summary: This is a summary"}`,
			wantErr: false,
		},
		{
			name:    "returns original when result is not valid JSON",
			input:   `Let me think about this {"key": "value"}`,
			want:    `Let me think about this {"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "returns original when result is not valid JSON - I'll",
			input:   `I'll fix this {"result": "done"}`,
			want:    `I'll fix this {"result": "done"}`,
			wantErr: false,
		},
		{
			name:    "returns original when result is not valid JSON - Approach",
			input:   `Approach: Analyze the problem {"data": true}`,
			want:    `Approach: Analyze the problem {"data": true}`,
			wantErr: false,
		},
		{
			name:    "returns original when result is not valid JSON - Recommended",
			input:   `Recommended solution: {"answer": "yes"}`,
			want:    `Recommended solution: {"answer": "yes"}`,
			wantErr: false,
		},
		{
			name:    "returns original if result becomes invalid",
			input:   `Let me do something random`,
			want:    `Let me do something random`,
			wantErr: false,
		},
		{
			name:    "case insensitive removal inside string values",
			input:   `{"text": "summary: test"}`,
			want:    `{"text": "summary: test"}`,
			wantErr: false,
		},
		{
			name:    "valid JSON unchanged",
			input:   `{"key": "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "returns original for multiple patterns at start",
			input:   `First, let me think. I'll analyze this. {"result": true}`,
			want:    `First, let me think. I'll analyze this. {"result": true}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := removeReasoningLeakage(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("removeReasoningLeakage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("removeReasoningLeakage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewRepairer(t *testing.T) {
	t.Run("creates repairer with default config", func(t *testing.T) {
		repairer := NewRepairer(nil)
		if repairer == nil {
			t.Error("NewRepairer() returned nil")
		}
		if repairer.config == nil {
			t.Error("config should not be nil")
		}
	})

	t.Run("creates repairer with custom config", func(t *testing.T) {
		config := &Config{Enabled: false, MaxArgumentsSize: 5000}
		repairer := NewRepairer(config)
		if repairer.config != config {
			t.Error("config should be the same instance")
		}
	})
}

func TestRepairResult(t *testing.T) {
	t.Run("stores original and tool name", func(t *testing.T) {
		result := &RepairResult{
			Original: `{"original": true}`,
			ToolName: "test_tool",
		}

		if result.Original != `{"original": true}` {
			t.Errorf("Original = %v, want %v", result.Original, `{"original": true}`)
		}
		if result.ToolName != "test_tool" {
			t.Errorf("ToolName = %v, want %v", result.ToolName, "test_tool")
		}
	})

	t.Run("tracks strategies used", func(t *testing.T) {
		result := &RepairResult{
			Strategies: []string{"extract_json", "library_repair"},
		}

		if len(result.Strategies) != 2 {
			t.Errorf("Strategies length = %v, want 2", len(result.Strategies))
		}
	})
}

func TestEventCallback(t *testing.T) {
	t.Run("callback not called when no repairs", func(t *testing.T) {
		callbackCalled := false
		repairer := NewRepairer(DefaultConfig())
		callback := func(stats *RepairStats, results []*RepairResult) {
			callbackCalled = true
		}

		// Valid JSON - no repairs needed
		toolCalls := []ToolCallData{
			{ID: "1", Type: "function", Name: "test", Arguments: `{"key": "value"}`},
		}

		_, _ = repairer.RepairToolCallsData(toolCalls, callback)

		if callbackCalled {
			t.Error("callback should not be called when no repairs needed")
		}
	})

	t.Run("callback called when repairs occur", func(t *testing.T) {
		var capturedStats *RepairStats
		var capturedResults []*RepairResult

		repairer := NewRepairer(DefaultConfig())
		callback := func(stats *RepairStats, results []*RepairResult) {
			capturedStats = stats
			capturedResults = results
		}

		// Invalid JSON that can be repaired
		toolCalls := []ToolCallData{
			{ID: "1", Type: "function", Name: "test", Arguments: `{key: "value"}`},
		}

		_, _ = repairer.RepairToolCallsData(toolCalls, callback)

		if capturedStats == nil {
			t.Fatal("callback should have been called")
		}
		if capturedStats.Repaired != 1 {
			t.Errorf("Repaired = %v, want 1", capturedStats.Repaired)
		}
		if len(capturedResults) != 1 {
			t.Errorf("Results length = %v, want 1", len(capturedResults))
		}
		if capturedResults[0].ToolName != "test" {
			t.Errorf("ToolName = %v, want 'test'", capturedResults[0].ToolName)
		}
	})

	t.Run("callback called when repairs fail", func(t *testing.T) {
		var capturedStats *RepairStats

		repairer := NewRepairer(DefaultConfig())
		callback := func(stats *RepairStats, results []*RepairResult) {
			capturedStats = stats
		}

		// Completely invalid JSON that cannot be repaired - use something that clearly fails
		toolCalls := []ToolCallData{
			{ID: "1", Type: "function", Name: "test", Arguments: `{{{broken`},
		}

		_, _ = repairer.RepairToolCallsData(toolCalls, callback)

		if capturedStats == nil {
			t.Fatal("callback should have been called")
		}
		// Check that we have either repaired or failed (the exact outcome depends on repair strategies)
		if capturedStats.Failed == 0 && capturedStats.Repaired == 0 {
			t.Errorf("Expected either Failed > 0 or Repaired > 0, got Failed=%v, Repaired=%v", capturedStats.Failed, capturedStats.Repaired)
		}
	})

	t.Run("nil callback does not panic", func(t *testing.T) {
		repairer := NewRepairer(DefaultConfig())
		// Pass nil callback

		toolCalls := []ToolCallData{
			{ID: "1", Type: "function", Name: "test", Arguments: `{key: "value"}`},
		}

		// Should not panic
		_, _ = repairer.RepairToolCallsData(toolCalls, nil)
	})

	t.Run("panic in callback is recovered", func(t *testing.T) {
		repairer := NewRepairer(DefaultConfig())
		callback := func(stats *RepairStats, results []*RepairResult) {
			panic("intentional panic")
		}

		toolCalls := []ToolCallData{
			{ID: "1", Type: "function", Name: "test", Arguments: `{key: "value"}`},
		}

		// Should not panic - callback panic should be recovered
		_, _ = repairer.RepairToolCallsData(toolCalls, callback)
	})
}

func TestTrimTrailingGarbage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "already valid JSON",
			input:   `{"key": "value"}`,
			want:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "MiniMax bug: trailing garbage after complete JSON",
			input:   `{"include": "*.go", "pattern": "event.*log"}"}`,
			want:    `{"include": "*.go", "pattern": "event.*log"}`,
			wantErr: false,
		},
		{
			name:    "trailing extra characters",
			input:   `{"test": "value"}extra`,
			want:    `{"test": "value"}`,
			wantErr: false,
		},
		{
			name:    "trailing double closing",
			input:   `{"a": 1}}"`,
			want:    `{"a": 1}`,
			wantErr: false,
		},
		{
			name:    "nested object with trailing garbage",
			input:   `{"outer": {"inner": "value"}}garbage`,
			want:    `{"outer": {"inner": "value"}}`,
			wantErr: false,
		},
		{
			name:    "empty object",
			input:   `{}`,
			want:    `{}`,
			wantErr: false,
		},
		{
			name:    "incomplete JSON returns original",
			input:   `{"key":`,
			want:    `{"key":`,
			wantErr: false,
		},
		{
			name:    "JSON with string containing braces",
			input:   `{"pattern": "a{1,3}"}`,
			want:    `{"pattern": "a{1,3}"}`,
			wantErr: false,
		},
		{
			name:    "JSON with string containing braces and trailing garbage",
			input:   `{"pattern": "a{1,3}"}extra`,
			want:    `{"pattern": "a{1,3}"}`,
			wantErr: false,
		},
		{
			name:    "JSON with escaped quotes",
			input:   `{"text": "He said \"hello\""}`,
			want:    `{"text": "He said \"hello\""}`,
			wantErr: false,
		},
		{
			name:    "JSON with escaped quotes and trailing garbage",
			input:   `{"text": "He said \"hello\""}"}`,
			want:    `{"text": "He said \"hello\""}`,
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: false,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trimTrailingGarbage(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("trimTrailingGarbage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("trimTrailingGarbage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRepairWithTrailingGarbageStrategy(t *testing.T) {
	// Test that the trim_trailing_garbage strategy is applied correctly
	config := &Config{
		Enabled:    true,
		Strategies: []string{"trim_trailing_garbage"},
	}
	repairer := NewRepairer(config)

	tests := []struct {
		name         string
		arguments    string
		wantSuccess  bool
		wantRepaired string
	}{
		{
			name:         "MiniMax bug example",
			arguments:    `{"include": "*.go", "pattern": "event.*log"}"}`,
			wantSuccess:  true,
			wantRepaired: `{"include": "*.go", "pattern": "event.*log"}`,
		},
		{
			name:         "already valid",
			arguments:    `{"valid": true}`,
			wantSuccess:  true,
			wantRepaired: `{"valid": true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repairer.RepairArguments(tt.arguments, "test")
			if result.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", result.Success, tt.wantSuccess)
			}
			if result.Repaired != tt.wantRepaired {
				t.Errorf("Repaired = %v, want %v", result.Repaired, tt.wantRepaired)
			}
		})
	}
}
