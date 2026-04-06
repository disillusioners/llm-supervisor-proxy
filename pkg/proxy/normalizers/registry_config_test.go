package normalizers

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// --- Mock implementations for testing ---

// mockModelsConfig is a test mock for ModelsConfigInterface
type mockModelsConfig struct {
	models map[string]*models.ModelConfig
}

func newMockModelsConfig() *mockModelsConfig {
	return &mockModelsConfig{
		models: make(map[string]*models.ModelConfig),
	}
}

func (m *mockModelsConfig) GetModel(modelID string) *models.ModelConfig {
	return m.models[modelID]
}

func (m *mockModelsConfig) ResolveInternalConfig(modelID string) (string, string, string, string, bool) {
	if cfg, ok := m.models[modelID]; ok && cfg.Internal {
		return "anthropic", "test-key", "https://api.anthropic.com", cfg.InternalModel, true
	}
	return "", "", "", "", false
}

func (m *mockModelsConfig) AddModelForTest(id string, internal bool) {
	m.models[id] = &models.ModelConfig{
		ID:           id,
		Name:         id,
		Enabled:      true,
		Internal:     internal,
		CredentialID: "cred-1",
	}
}

// Satisfy the full interface with no-op implementations
func (m *mockModelsConfig) GetModels() []models.ModelConfig                         { return nil }
func (m *mockModelsConfig) GetEnabledModels() []models.ModelConfig                  { return nil }
func (m *mockModelsConfig) GetTruncateParams(modelID string) []string               { return nil }
func (m *mockModelsConfig) GetFallbackChain(modelID string) []string                { return nil }
func (m *mockModelsConfig) AddModel(mc models.ModelConfig) error                    { return nil }
func (m *mockModelsConfig) UpdateModel(modelID string, mc models.ModelConfig) error { return nil }
func (m *mockModelsConfig) RemoveModel(modelID string) error                        { return nil }
func (m *mockModelsConfig) Save() error                                             { return nil }
func (m *mockModelsConfig) Validate() error                                         { return nil }
func (m *mockModelsConfig) GetCredential(id string) *models.CredentialConfig        { return nil }
func (m *mockModelsConfig) GetCredentials() []models.CredentialConfig               { return nil }
func (m *mockModelsConfig) AddCredential(cred models.CredentialConfig) error        { return nil }
func (m *mockModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	return nil
}
func (m *mockModelsConfig) RemoveCredential(id string) error { return nil }

// mockToolCallRepairNormalizer is a test double implementing StreamNormalizer
type mockToolCallRepairNormalizer struct {
	name          string
	enabledByDef  bool
	modifiedCount int
	resetCount    int
}

func newMockToolCallRepairNormalizer(name string, enabledByDefault bool) *mockToolCallRepairNormalizer {
	return &mockToolCallRepairNormalizer{
		name:         name,
		enabledByDef: enabledByDefault,
	}
}

func (n *mockToolCallRepairNormalizer) Name() string {
	return n.name
}

func (n *mockToolCallRepairNormalizer) EnabledByDefault() bool {
	return n.enabledByDef
}

func (n *mockToolCallRepairNormalizer) Reset(ctx *NormalizeContext) {
	n.resetCount++
}

func (n *mockToolCallRepairNormalizer) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	n.modifiedCount++
	return line, false
}

// --- DetectProvider tests ---

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name     string
		cfg      models.ModelsConfigInterface
		modelID  string
		expected string
	}{
		{
			name:     "nil config returns external",
			cfg:      nil,
			modelID:  "some-model",
			expected: "external",
		},
		{
			name:     "model not found returns external",
			cfg:      newMockModelsConfig(),
			modelID:  "unknown-model",
			expected: "external",
		},
		{
			name: "external model returns external",
			cfg: func() models.ModelsConfigInterface {
				m := newMockModelsConfig()
				m.AddModelForTest("gpt-4", false)
				return m
			}(),
			modelID:  "gpt-4",
			expected: "external",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectProvider(tt.cfg, tt.modelID)
			if got != tt.expected {
				t.Errorf("DetectProvider() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// --- Registry tests ---

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	if r.normalizers == nil {
		t.Error("Registry.normalizers is nil")
	}
	if r.enabled == nil {
		t.Error("Registry.enabled is nil")
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	n1 := newMockToolCallRepairNormalizer("test-1", true)
	n2 := newMockToolCallRepairNormalizer("test-2", false)

	r.Register(n1)
	r.Register(n2)

	names := r.ListNormalizers()
	if len(names) != 2 {
		t.Errorf("ListNormalizers() returned %d names, want 2", len(names))
	}

	// Check that n1 is enabled by default
	if !r.IsEnabled("test-1") {
		t.Error("test-1 should be enabled by default")
	}

	// Check that n2 is NOT enabled by default
	if r.IsEnabled("test-2") {
		t.Error("test-2 should not be enabled by default")
	}
}

func TestRegistry_EnableDisable(t *testing.T) {
	r := NewRegistry()
	n := newMockToolCallRepairNormalizer("test", false) // disabled by default
	r.Register(n)

	// Should not be enabled initially
	if r.IsEnabled("test") {
		t.Error("Expected test to be disabled after registration")
	}

	// Enable it
	r.Enable("test")
	if !r.IsEnabled("test") {
		t.Error("Expected test to be enabled after Enable()")
	}

	// Disable it
	r.Disable("test")
	if r.IsEnabled("test") {
		t.Error("Expected test to be disabled after Disable()")
	}
}

func TestRegistry_EnableNonExistent(t *testing.T) {
	r := NewRegistry()
	// Should not panic
	r.Enable("non-existent")
	r.Disable("non-existent")
}

func TestRegistry_IsEnabledNonExistent(t *testing.T) {
	r := NewRegistry()
	// Should return false (zero value) for non-existent
	if r.IsEnabled("non-existent") {
		t.Error("IsEnabled() should return false for non-existent normalizer")
	}
}

func TestRegistry_ListNormalizers(t *testing.T) {
	r := NewRegistry()

	// Empty registry
	names := r.ListNormalizers()
	if len(names) != 0 {
		t.Errorf("Empty registry should have 0 normalizers, got %d", len(names))
	}

	// Add normalizers
	r.Register(newMockToolCallRepairNormalizer("first", true))
	r.Register(newMockToolCallRepairNormalizer("second", true))

	names = r.ListNormalizers()
	if len(names) != 2 {
		t.Errorf("ListNormalizers() returned %d, want 2", len(names))
	}

	// Order is not guaranteed, so just check both names are present
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["first"] || !found["second"] {
		t.Error("Expected both 'first' and 'second' in list")
	}
}

func TestRegistry_ResetAll(t *testing.T) {
	r := NewRegistry()

	n1 := newMockToolCallRepairNormalizer("n1", true)
	n2 := newMockToolCallRepairNormalizer("n2", true)
	r.Register(n1)
	r.Register(n2)

	ctx := &NormalizeContext{}
	r.ResetAll(ctx)

	if n1.resetCount != 1 {
		t.Errorf("n1.Reset() called %d times, want 1", n1.resetCount)
	}
	if n2.resetCount != 1 {
		t.Errorf("n2.Reset() called %d times, want 1", n2.resetCount)
	}
}

func TestRegistry_Normalize_NoModifications(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFixEmptyRoleNormalizer())
	// Disable it to ensure no modifications
	r.Disable("fix_empty_role")

	ctx := &NormalizeContext{}
	line := []byte(`data: {"delta": {"role": ""}}`)

	result, modified := r.Normalize(line, ctx)
	if modified {
		t.Error("Expected no modification when normalizer is disabled")
	}
	if string(result) != string(line) {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestRegistry_Normalize_WithModifications(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFixEmptyRoleNormalizer())
	// Enabled by default

	ctx := &NormalizeContext{}
	line := []byte(`data: {"delta": {"role": ""}}`)

	result, modified := r.Normalize(line, ctx)
	if !modified {
		t.Error("Expected modification with fix_empty_role enabled")
	}
	// The role should be fixed
	if !containsSubstring(string(result), `"role":"assistant"`) {
		t.Errorf("Expected role to be fixed to 'assistant', got %s", string(result))
	}
}

func TestRegistry_Normalize_RecordsFirstModifier(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFixEmptyRoleNormalizer())

	ctx := &NormalizeContext{}
	line := []byte(`data: {"delta": {"role": ""}}`)

	_, modified, name := r.NormalizeWithName(line, ctx)
	if !modified {
		t.Error("Expected modification")
	}
	if name != "fix_empty_role" {
		t.Errorf("Expected normalizer name 'fix_empty_role', got %q", name)
	}
}

func TestRegistry_NormalizeWithName_NoModification(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFixEmptyRoleNormalizer())
	r.Disable("fix_empty_role")

	ctx := &NormalizeContext{}
	line := []byte(`data: {"delta": {"role": "assistant"}}`)

	_, modified, name := r.NormalizeWithName(line, ctx)
	if modified {
		t.Error("Expected no modification")
	}
	if name != "" {
		t.Errorf("Expected empty normalizer name, got %q", name)
	}
}

func TestRegistry_Normalize_Concurrent(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 5; i++ {
		n := newMockToolCallRepairNormalizer("n"+string(rune('0'+i)), true)
		r.Register(n)
	}

	ctx := &NormalizeContext{}
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				r.Normalize([]byte(`data: {}`), ctx)
				r.ListNormalizers()
				r.IsEnabled("n0")
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// --- FixEmptyRoleNormalizer extended tests ---

func TestFixEmptyRoleNormalizer_Name(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()
	if n.Name() != "fix_empty_role" {
		t.Errorf("Name() = %q, want %q", n.Name(), "fix_empty_role")
	}
}

func TestFixEmptyRoleNormalizer_EnabledByDefault(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()
	if !n.EnabledByDefault() {
		t.Error("EnabledByDefault() should return true")
	}
}

func TestFixEmptyRoleNormalizer_Reset(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()
	ctx := &NormalizeContext{}
	// Should not panic
	n.Reset(ctx)
}

func TestFixEmptyRoleNormalizer_WhitespaceLine(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()
	ctx := &NormalizeContext{}

	tests := []string{
		"   ",
		"\t",
		"\n\t  ",
	}

	for _, input := range tests {
		result, modified := n.Normalize([]byte(input), ctx)
		if modified {
			t.Errorf("Whitespace input %q should not be modified", input)
		}
		if string(result) != input {
			t.Errorf("Result should be unchanged, got %s", string(result))
		}
	}
}

func TestFixEmptyRoleNormalizer_BracketDone(t *testing.T) {
	n := NewFixEmptyRoleNormalizer()
	ctx := &NormalizeContext{}

	tests := []string{
		"[DONE]",
		"  [DONE]  ",
	}

	for _, input := range tests {
		_, modified := n.Normalize([]byte(input), ctx)
		if modified {
			t.Errorf("[DONE] input %q should not be modified", input)
		}
	}
}

// --- FixMissingToolCallIndexNormalizer extended tests ---

func TestFixMissingToolCallIndexNormalizer_Name(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	if n.Name() != "fix_tool_call_index" {
		t.Errorf("Name() = %q, want %q", n.Name(), "fix_tool_call_index")
	}
}

func TestFixMissingToolCallIndexNormalizer_EnabledByDefault(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	if !n.EnabledByDefault() {
		t.Error("EnabledByDefault() should return true")
	}
}

func TestFixMissingToolCallIndexNormalizer_Reset(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	ctx.SeenToolCallIDs = map[string]int{"existing": 0}

	n.Reset(ctx)

	if ctx.SeenToolCallIDs == nil {
		t.Error("Reset() should initialize SeenToolCallIDs")
	}
	if len(ctx.SeenToolCallIDs) != 0 {
		t.Error("Reset() should clear SeenToolCallIDs")
	}
}

func TestFixMissingToolCallIndexNormalizer_MultipleToolCalls(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	n.Reset(ctx)

	// Two tool calls with IDs
	input := `data: {"choices": [{"delta": {"tool_calls": [{"id": "call_1"}, {"id": "call_2"}]}}]}`
	result, modified := n.Normalize([]byte(input), ctx)

	if !modified {
		t.Error("Expected modification for tool_calls without index")
	}
	// Should have both indices
	if !containsSubstring(string(result), `"index":0`) {
		t.Error("Expected index 0")
	}
	if !containsSubstring(string(result), `"index":1`) {
		t.Error("Expected index 1")
	}

	// Verify state was updated
	if len(ctx.SeenToolCallIDs) != 2 {
		t.Errorf("SeenToolCallIDs should have 2 entries, got %d", len(ctx.SeenToolCallIDs))
	}
}

func TestFixMissingToolCallIndexNormalizer_FunctionNameBased(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	n.Reset(ctx)

	// Tool call without ID but with function name
	input := `data: {"choices": [{"delta": {"tool_calls": [{"function": {"name": "get_weather"}}]}}]}`
	result, modified := n.Normalize([]byte(input), ctx)

	if !modified {
		t.Error("Expected modification for tool_calls without ID")
	}
	if !containsSubstring(string(result), `"index":0`) {
		t.Error("Expected index 0 for function-name-based tool call")
	}
}

func TestFixMissingToolCallIndexNormalizer_MixedWithoutIDs(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	n.Reset(ctx)

	// Multiple tool calls without IDs - should use position-based index
	input := `data: {"choices": [{"delta": {"tool_calls": [{"type": "function"}, {"type": "function"}]}}]}`
	result, modified := n.Normalize([]byte(input), ctx)

	if !modified {
		t.Error("Expected modification")
	}
	if !containsSubstring(string(result), `"index":0`) {
		t.Error("Expected index 0")
	}
	if !containsSubstring(string(result), `"index":1`) {
		t.Error("Expected index 1")
	}
}

func TestFixMissingToolCallIndexNormalizer_NilSeenToolCallIDs(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	// Don't call Reset - leave SeenToolCallIDs nil

	// Should handle nil gracefully
	input := `data: {"choices": [{"delta": {"tool_calls": [{"id": "call_1"}]}}]}`
	result, modified := n.Normalize([]byte(input), ctx)

	if !modified {
		t.Error("Expected modification")
	}
	if len(result) == 0 {
		t.Error("Result should not be empty")
	}
}

func TestFixMissingToolCallIndexNormalizer_InvalidChoices(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	n.Reset(ctx)

	tests := []string{
		`data: {"choices": []}`,                                  // empty choices
		`data: {"choices": "not an array"}`,                      // string choices
		`data: {"choices": [123]}`,                               // number in choices
		`data: {"delta": {}}`,                                    // no choices
		`data: {"choices": [{"delta": {}}]}`,                     // no tool_calls
		`data: {"choices": [{"delta": {"tool_calls": []}}]}`,     // empty tool_calls
		`data: {"choices": [{"delta": {"tool_calls": "bad"}]}]}`, // string tool_calls
	}

	for _, input := range tests {
		result, modified := n.Normalize([]byte(input), ctx)
		if modified {
			t.Errorf("Invalid input %q should not be modified, got %s", input, string(result))
		}
	}
}

func TestFixMissingToolCallIndexNormalizer_AlreadyHasIndex(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	ctx := &NormalizeContext{}
	n.Reset(ctx)

	// Already has index - should not modify
	input := `data: {"choices": [{"delta": {"tool_calls": [{"index": 5, "id": "call_1"}]}}]}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Errorf("Already indexed tool call should not be modified, got %s", string(result))
	}
}

func TestFixMissingToolCallIndexNormalizer_ConcurrentState(t *testing.T) {
	n := NewFixMissingToolCallIndexNormalizer()
	// Each goroutine needs its own context
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			ctx := &NormalizeContext{}
			n.Reset(ctx)
			for j := 0; j < 50; j++ {
				input := `data: {"choices": [{"delta": {"tool_calls": [{"id": "call_` + string(rune('0'+idx)) + `_` + string(rune('0'+j%10)) + `"}]}}]}`
				n.Normalize([]byte(input), ctx)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// --- ToolCallArgumentsRepairNormalizer tests ---

func TestToolCallArgumentsRepairNormalizer_Name(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	if n.Name() != "tool_call_arguments_repair" {
		t.Errorf("Name() = %q, want %q", n.Name(), "tool_call_arguments_repair")
	}
}

func TestToolCallArgumentsRepairNormalizer_EnabledByDefault(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	if n.EnabledByDefault() {
		t.Error("EnabledByDefault() should return false")
	}
}

func TestToolCallArgumentsRepairNormalizer_NilConfig(t *testing.T) {
	// Should not panic
	n := NewToolCallArgumentsRepairNormalizer(nil)
	if n == nil {
		t.Error("Should return valid normalizer even with nil config")
	}
}

func TestToolCallArgumentsRepairNormalizer_SetConfig(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)

	newCfg := toolrepair.DisabledConfig()
	newCfg.Enabled = true

	n.SetConfig(newCfg)
	// Should not panic - just verify the normalizer still works
	ctx := &NormalizeContext{}
	result, _ := n.Normalize([]byte(`data: {}`), ctx)
	if string(result) != `data: {}` {
		t.Error("Should return input unchanged when no tool_calls")
	}
}

func TestToolCallArgumentsRepairNormalizer_DisabledConfig(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// Should return unchanged for any input when disabled
	input := `data: {"delta": {"tool_calls": [{"function": {"arguments": "bad json"}}]}}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("Should not modify when repair is disabled")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_SkipsDone(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	tests := []string{
		"data: [DONE]",
		"[DONE]",
		"  [DONE]  ",
		"",
		"   ",
	}

	for _, input := range tests {
		result, modified := n.Normalize([]byte(input), ctx)
		if modified {
			t.Errorf("[DONE]/empty input %q should not be modified", input)
		}
		if string(result) != input {
			t.Errorf("Result should be unchanged for %q", input)
		}
	}
}

func TestToolCallArgumentsRepairNormalizer_InvalidJSON(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// Invalid JSON should not be modified
	input := `not valid json at all`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("Invalid JSON should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_NoDelta(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// No delta field
	input := `data: {"choices": []}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("No delta should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_NoToolCalls(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// No tool_calls in delta
	input := `data: {"delta": {"content": "hello"}}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("No tool_calls should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_NoFunction(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// tool_calls without function
	input := `data: {"delta": {"tool_calls": [{"id": "call_1"}]}}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("tool_calls without function should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_EmptyArguments(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// Empty arguments should be skipped
	input := `data: {"delta": {"tool_calls": [{"function": {"arguments": ""}}]}}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("Empty arguments should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_ValidJSONArguments(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// Valid JSON arguments should be skipped
	input := `data: {"delta": {"tool_calls": [{"function": {"arguments": "{\"key\": \"value\"}"}}]}}`
	result, modified := n.Normalize([]byte(input), ctx)

	if modified {
		t.Error("Valid JSON arguments should not be modified")
	}
	if string(result) != input {
		t.Errorf("Result should be unchanged, got %s", string(result))
	}
}

func TestToolCallArgumentsRepairNormalizer_MultipleToolCalls(t *testing.T) {
	cfg := toolrepair.DisabledConfig()
	n := NewToolCallArgumentsRepairNormalizer(cfg)
	ctx := &NormalizeContext{}

	// Multiple tool calls, some valid some not
	input := `data: {"delta": {"tool_calls": [{"function": {"arguments": "{\"key\": \"value\"}"}}, {"function": {"arguments": "bad"}}]}}`
	result, modified := n.Normalize([]byte(input), ctx)

	// Even with disabled config, format should be preserved
	if string(result) == "" {
		t.Error("Result should not be empty")
	}
	_ = modified // Just ensure no panics
}

func TestIsValidJSONArgs(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`{}`, true},
		{`[]`, true},
		{`"string"`, true},
		{`123`, true},
		{`true`, true},
		{`null`, true},
		{`{key: value}`, false},     // unquoted key
		{`{key: "value"}`, false},   // unquoted key with quoted value
		{`{'key': 'value'}`, false}, // single quotes
		{``, false},                 // empty string is not valid JSON
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidJSONArgs(tt.input)
			if got != tt.expected {
				t.Errorf("isValidJSONArgs(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
