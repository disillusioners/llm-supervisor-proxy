package reasoning_content

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
)

// TestReasoningContentChain_NonStream tests that reasoning_content survives the full
// serialization chain: client JSON -> convertToProviderRequest -> provider request JSON
func TestReasoningContentChain_NonStream(t *testing.T) {
	tests := []struct {
		name           string
		rawBody        string
		expectReasoning bool
		expectedValue  string
		expectsName    bool
		expectedName   string
	}{
		{
			name:            "reasoning_content present",
			rawBody:         `{"messages":[{"role":"assistant","content":"The answer is 42.","reasoning_content":"Let me calculate..."}]}`,
			expectReasoning: true,
			expectedValue:   "Let me calculate...",
		},
		{
			name: "no reasoning_content",
			rawBody: `{"messages":[{"role":"user","content":"Hello"}]}`,
		},
		{
			name:            "empty reasoning_content - struct field set to empty (verified via serialization test)",
			rawBody:         `{"messages":[{"role":"assistant","content":"Answer","reasoning_content":""}]}`,
			expectReasoning: false, // Empty string behavior verified via TestReasoningContentSerializationChain
			// The struct field may be set, but we can't distinguish "set to empty" from "never set"
		},
		{
			name:            "multiple messages with reasoning_content",
			rawBody:         `{"messages":[{"role":"user","content":"What is 2+2?"},{"role":"assistant","content":"4","reasoning_content":"I added 2+2."},{"role":"user","content":"Thanks"}]}`,
			expectReasoning: true,
			expectedValue:   "I added 2+2.",
		},
		{
			name:          "name field present",
			rawBody:       `{"messages":[{"role":"user","content":"Hello","name":"human_user"}]}`,
			expectsName:   true,
			expectedName:  "human_user",
		},
		{
			name:            "combined reasoning_content and name",
			rawBody:         `{"messages":[{"role":"assistant","content":"The answer is 42.","reasoning_content":"Let me calculate...","name":"assistant_1"}]}`,
			expectReasoning: true,
			expectedValue:   "Let me calculate...",
			expectsName:     true,
			expectedName:    "assistant_1",
		},
		{
			name:    "null reasoning_content in JSON",
			rawBody: `{"messages":[{"role":"assistant","content":"Answer","reasoning_content":null}]}`,
			// null becomes zero value (empty string), but that's the same as missing
		},
		{
			name:    "missing reasoning_content key (vs null)",
			rawBody: `{"messages":[{"role":"assistant","content":"Answer without reasoning"}]}`,
		},
		{
			name:            "multiple messages mixed with and without reasoning_content and name",
			rawBody:         `{"messages":[{"role":"system","name":"system_prompt","content":"You are helpful."},{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi!","reasoning_content":"Thinking..."}]}`,
			expectReasoning: true,
			expectedValue:   "Thinking...",
			expectsName:     true,
			expectedName:    "system_prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test provider
			provider := proxy.NewTestProvider()
			provider.SetChatCompletionResponse(&providers.ChatCompletionResponse{
				ID:      "chatcmpl-test",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Choices: []providers.Choice{
					{
						Index: 0,
						Message: &providers.ChatMessage{
							Role:    "assistant",
							Content: "Test response",
						},
						FinishReason: "stop",
					},
				},
				Usage: providers.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
			})

			// Create models config
			modelsConfig := models.NewModelsConfig()
			modelsConfig.Models = []models.ModelConfig{
				{
					ID:            "test-internal",
					Name:          "Test Internal",
					Enabled:       true,
					Internal:      true,
					CredentialID:  "test-cred",
					InternalModel: "test-model",
				},
			}
			modelsConfig.Credentials = models.NewCredentialsConfig()
			_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
				ID:       "test-cred",
				Provider: "openai",
				APIKey:   "test-key",
			})

			// Create config snapshot
			cfg := &proxy.ConfigSnapshot{
				ModelID:            "test-internal",
				ModelsConfig:       modelsConfig,
				IdleTimeout:        60 * time.Second,
				StreamDeadline:     110 * time.Second,
				MaxGenerationTime:  300 * time.Second,
				RaceMaxBufferBytes: 1024 * 1024,
			}

			// Create upstream request using the test helper
			req := proxy.NewUpstreamRequest(0, proxy.ModelTypeMain, "test-internal", 1024*1024)

			// Execute the request through executeInternalRequest
			// This calls convertToProviderRequest internally
			err := proxy.ExecuteInternalRequestWithProvider(context.Background(), cfg, []byte(tt.rawBody), req, provider)
			if err != nil {
				t.Fatalf("executeInternalRequest failed: %v", err)
			}

			// Get the captured request
			capturedReq := provider.GetCapturedRequest()
			if capturedReq == nil {
				t.Fatal("provider did not receive any request")
			}

			// Verify reasoning_content
			hasReasoning := false
			var actualReasoning string
			for _, msg := range capturedReq.Messages {
				if msg.ReasoningContent != "" {
					hasReasoning = true
					actualReasoning = msg.ReasoningContent
					break
				}
			}

			if tt.expectReasoning {
				if !hasReasoning && !containsEmptyReasoning(capturedReq.Messages) {
					t.Errorf("expected reasoning_content to be set (even if empty), but it was not")
				}
				// Check the specific value if we have one
				if tt.expectedValue != "" && actualReasoning != tt.expectedValue {
					t.Errorf("reasoning_content = %q, want %q", actualReasoning, tt.expectedValue)
				}
			} else {
				if hasReasoning {
					t.Errorf("expected reasoning_content to be empty, got %q", actualReasoning)
				}
			}

			// Verify name field
			hasName := false
			var actualName string
			for _, msg := range capturedReq.Messages {
				if msg.Name != "" {
					hasName = true
					actualName = msg.Name
					break
				}
			}

			if tt.expectsName {
				if !hasName {
					t.Errorf("expected name to be set, but it was not")
				}
				if actualName != tt.expectedName {
					t.Errorf("name = %q, want %q", actualName, tt.expectedName)
				}
			} else {
				if hasName {
					t.Errorf("expected name to be empty, got %q", actualName)
				}
			}
		})
	}
}

// containsEmptyReasoning checks if any message has an explicitly set (possibly empty) reasoning_content
func containsEmptyReasoning(messages []providers.ChatMessage) bool {
	// This is tricky because empty string and "not set" are the same in Go.
	// We can only detect non-empty values.
	return false
}

// TestReasoningContentSerializationChain tests the full serialization chain:
// JSON -> map[string]interface{} -> convertToProviderRequest -> ChatCompletionRequest -> JSON
// This test verifies that reasoning_content appears in the final serialized JSON.
func TestReasoningContentSerializationChain(t *testing.T) {
	tests := []struct {
		name             string
		inputJSON        string
		wantInOutputJSON []string // Strings that should appear in serialized output
		notWantInOutput []string // Strings that should NOT appear in serialized output
	}{
		{
			name:      "reasoning_content preserved in serialization",
			inputJSON: `{"messages":[{"role":"assistant","content":"42","reasoning_content":"Thinking step by step"}]}`,
			wantInOutputJSON: []string{
				`"reasoning_content"`,
				`Thinking step by step`,
			},
		},
		{
			name:      "name field preserved in serialization",
			inputJSON: `{"messages":[{"role":"user","content":"Hello","name":"alice"}]}`,
			wantInOutputJSON: []string{
				`"name"`,
				`alice`,
			},
		},
		{
			name:      "both reasoning_content and name preserved",
			inputJSON: `{"messages":[{"role":"assistant","content":"Answer","reasoning_content":"Thinking...","name":"assistant_1"}]}`,
			wantInOutputJSON: []string{
				`"reasoning_content"`,
				`Thinking...`,
				`"name"`,
				`assistant_1`,
			},
		},
		{
			name:      "multiple messages mixed",
			inputJSON: `{"messages":[{"role":"system","name":"sys","content":"You are helpful."},{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello","reasoning_content":"Thinking"}]}`,
			wantInOutputJSON: []string{
				`"reasoning_content"`,
				`Thinking`,
				`"name"`,
				`sys`,
			},
		},
		{
			name:      "empty reasoning_content omits from JSON (due to omitempty)",
			inputJSON: `{"messages":[{"role":"assistant","content":"Answer","reasoning_content":""}]}`,
			notWantInOutput: []string{
				`"reasoning_content"`, // Empty strings are omitted due to omitempty
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test provider
			provider := proxy.NewTestProvider()
			provider.SetChatCompletionResponse(&providers.ChatCompletionResponse{
				ID:      "chatcmpl-test",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Choices: []providers.Choice{
					{
						Index: 0,
						Message: &providers.ChatMessage{
							Role:    "assistant",
							Content: "Test",
						},
						FinishReason: "stop",
					},
				},
				Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			})

			modelsConfig := models.NewModelsConfig()
			modelsConfig.Models = []models.ModelConfig{
				{
					ID:            "test-internal",
					Enabled:       true,
					Internal:      true,
					CredentialID:  "test-cred",
					InternalModel: "test-model",
				},
			}
			modelsConfig.Credentials = models.NewCredentialsConfig()
			_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
				ID:       "test-cred",
				Provider: "openai",
				APIKey:   "test-key",
			})

			cfg := &proxy.ConfigSnapshot{
				ModelID:            "test-internal",
				ModelsConfig:       modelsConfig,
				IdleTimeout:        60 * time.Second,
				StreamDeadline:     110 * time.Second,
				MaxGenerationTime:  300 * time.Second,
				RaceMaxBufferBytes: 1024 * 1024,
			}

			req := proxy.NewUpstreamRequest(0, proxy.ModelTypeMain, "test-internal", 1024*1024)

			// Execute through the full chain
			err := proxy.ExecuteInternalRequestWithProvider(context.Background(), cfg, []byte(tt.inputJSON), req, provider)
			if err != nil {
				t.Fatalf("executeInternalRequest failed: %v", err)
			}

			capturedReq := provider.GetCapturedRequest()
			if capturedReq == nil {
				t.Fatal("provider did not receive any request")
			}

			// Serialize the captured request back to JSON
			outputJSON, err := json.Marshal(capturedReq)
			if err != nil {
				t.Fatalf("failed to marshal captured request: %v", err)
			}

			// Verify expected strings are present
			for _, want := range tt.wantInOutputJSON {
				if !strings.Contains(string(outputJSON), want) {
					t.Errorf("output JSON does not contain %q.\nGot: %s", want, string(outputJSON))
				}
			}

			// Verify unwanted strings are NOT present
			for _, notWant := range tt.notWantInOutput {
				if strings.Contains(string(outputJSON), notWant) {
					t.Errorf("output JSON should not contain %q, but it does.\nGot: %s", notWant, string(outputJSON))
				}
			}
		})
	}
}
