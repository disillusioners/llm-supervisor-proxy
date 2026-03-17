package normalizers

import (
	"testing"
)

func TestFixEmptyRoleNormalizer(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()

	tests := []struct {
		name     string
		input    string
		want     string
		wantMod  bool
	}{
		{
			name:    "empty role in delta",
			input:   `data: {"delta": {"role": ""}}`,
			want:    `data: {"delta":{"role":"assistant"}}`,
			wantMod: true,
		},
		{
			name:    "non-empty role",
			input:   `data: {"delta": {"role": "assistant"}}`,
			want:    `data: {"delta": {"role": "assistant"}}`,
			wantMod: false,
		},
		{
			name:    "no role field",
			input:   `data: {"delta": {"content": "hello"}}`,
			want:    `data: {"delta": {"content": "hello"}}`,
			wantMod: false,
		},
		{
			name:    "done marker",
			input:   `data: [DONE]`,
			want:    `data: [DONE]`,
			wantMod: false,
		},
		{
			name:    "empty line",
			input:   ``,
			want:    ``,
			wantMod: false,
		},
		{
			name:    "without data prefix",
			input:   `{"delta": {"role": ""}}`,
			want:    `{"delta":{"role":"assistant"}}`,
			wantMod: true,
		},
		{
			name:    "invalid JSON",
			input:   `not valid json`,
			want:    `not valid json`,
			wantMod: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &NormalizeContext{}
			got, gotMod := n.Normalize([]byte(tt.input), ctx)
			if string(got) != tt.want {
				t.Errorf("Normalize() = %v, want %v", string(got), tt.want)
			}
			if gotMod != tt.wantMod {
				t.Errorf("Normalize() modified = %v, want %v", gotMod, tt.wantMod)
			}
		})
	}
}

func TestFixMissingToolCallIndexNormalizer(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		checkFn func(t *testing.T, output string, modified bool)
		wantMod bool
	}{
		{
			name:    "tool_calls without index",
			input:   `data: {"delta": {"tool_calls": [{"id": "call_1", "type": "function"}]}}`,
			wantMod: true,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should contain "index":0
				if !containsSubstring(output, `"index":0`) {
					t.Errorf("Expected index 0 in output, got: %s", output)
				}
			},
		},
		{
			name:    "tool_calls with index",
			input:   `data: {"delta": {"tool_calls": [{"index": 0, "id": "call_1"}]}}`,
			wantMod: false,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should remain unchanged
				if containsSubstring(output, `"index":1`) {
					t.Errorf("Should not add new index, got: %s", output)
				}
			},
		},
		{
			name:    "no tool_calls",
			input:   `data: {"delta": {"content": "hello"}}`,
			wantMod: false,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should remain unchanged
			},
		},
		{
			name:    "done marker",
			input:   `data: [DONE]`,
			wantMod: false,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should remain unchanged
			},
		},
		{
			name:    "tool_call without ID uses position",
			input:   `data: {"delta": {"tool_calls": [{"type": "function"}]}}`,
			wantMod: true,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should contain index 0 (based on position)
				if !containsSubstring(output, `"index":0`) {
					t.Errorf("Expected index 0 in output, got: %s", output)
				}
			},
		},
		{
			name:    "invalid JSON",
			input:   `not valid json`,
			wantMod: false,
			checkFn: func(t *testing.T, output string, modified bool) {
				// Should remain unchanged
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new normalizer for each test to ensure clean state
			n := NewFixMissingToolCallIndexNormalizer()
			ctx := &NormalizeContext{}
			n.Reset(ctx)

			got, gotMod := n.Normalize([]byte(tt.input), ctx)
			if gotMod != tt.wantMod {
				t.Errorf("Normalize() modified = %v, want %v", gotMod, tt.wantMod)
			}
			tt.checkFn(t, string(got), gotMod)
		})
	}
}

func TestFixMissingToolCallIndexNormalizer_Stateful(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}

	// First chunk - call_1
	line1 := `data: {"delta": {"tool_calls": [{"id": "call_1", "type": "function"}]}}`
	got1, mod1 := n.Normalize([]byte(line1), ctx)
	if !mod1 {
		t.Error("Expected first chunk to be modified")
	}
	// Index should be 0
	if !containsIndex(string(got1), `"index":0`) {
		t.Errorf("Expected index 0 in first chunk, got: %s", string(got1))
	}

	// Second chunk - call_1 (same ID)
	line2 := `data: {"delta": {"tool_calls": [{"id": "call_1", "function": {"arguments": "test"}}]}}`
	got2, mod2 := n.Normalize([]byte(line2), ctx)
	if !mod2 {
		t.Error("Expected second chunk to be modified")
	}
	// Index should still be 0
	if !containsIndex(string(got2), `"index":0`) {
		t.Errorf("Expected index 0 in second chunk, got: %s", string(got2))
	}

	// Third chunk - call_2 (new ID)
	line3 := `data: {"delta": {"tool_calls": [{"id": "call_2", "type": "function"}]}}`
	got3, mod3 := n.Normalize([]byte(line3), ctx)
	if !mod3 {
		t.Error("Expected third chunk to be modified")
	}
	// Index should be 1 (second unique tool call)
	if !containsIndex(string(got3), `"index":1`) {
		t.Errorf("Expected index 1 in third chunk, got: %s", string(got3))
	}
}

func containsIndex(s, idx string) bool {
	return len(s) > 0 && len(idx) > 0 && len(s) >= len(idx) && (s == idx || len(s) > len(idx) && (containsSubstring(s, idx)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()

	// Register normalizers
	r.Register(NewFixEmptyRoleNormalizer())
	r.Register(NewFixMissingToolCallIndexNormalizer())

	// Test enable/disable
	r.Disable("fix_empty_role")
	if r.IsEnabled("fix_empty_role") {
		t.Error("Expected fix_empty_role to be disabled")
	}

	r.Enable("fix_empty_role")
	if !r.IsEnabled("fix_empty_role") {
		t.Error("Expected fix_empty_role to be enabled")
	}
}

func TestNormalize_EmptyLine(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
		mod    bool
	}{
		{
			name:   "empty string",
			input:  "",
			output: "",
			mod:    false,
		},
		{
			name:   "whitespace only",
			input:  "   ",
			output: "   ",
			mod:    false,
		},
		{
			name:   "done marker",
			input:  "data: [DONE]",
			output: "data: [DONE]",
			mod:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, mod := Normalize([]byte(tt.input), "test", "req")
			if string(got) != tt.output || mod != tt.mod {
				t.Errorf("Normalize() = (%s, %v), want (%s, %v)", string(got), mod, tt.output, tt.mod)
			}
		})
	}
}
