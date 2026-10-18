package ultimatemodel

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

func TestExtractUsageFromResponse(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		wantNil   bool
		wantUsage *store.Usage
	}{
		{
			name:    "valid usage data in response body",
			body:    []byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1677652288,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		{
			name:      "no usage field in response",
			body:      []byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1677652288,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "malformed JSON in response",
			body:      []byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1677652288`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "empty body",
			body:      []byte{},
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "nil body",
			body:      nil,
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:    "usage with zero values returns struct not nil",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
		},
		{
			name:    "usage with only prompt_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":100}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     100,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
		},
		{
			name:    "usage with only completion_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"completion_tokens":50}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 50,
				TotalTokens:      0,
			},
		},
		{
			name:    "usage with only total_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"total_tokens":200}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      200,
			},
		},
		{
			name:    "usage with missing completion_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":10,"total_tokens":25}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 0,
				TotalTokens:      25,
			},
		},
		{
			name:    "usage with non-numeric string value for prompt_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":"ten","completion_tokens":5,"total_tokens":15}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		{
			name:    "usage with non-numeric string value for completion_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":10,"completion_tokens":"five","total_tokens":15}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 0,
				TotalTokens:      15,
			},
		},
		{
			name:    "usage with non-numeric string value for total_tokens",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":"fifteen"}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      0,
			},
		},
		{
			name:      "usage field is null",
			body:      []byte(`{"id":"chatcmpl-123","usage":null}`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "usage field is not a map (string)",
			body:      []byte(`{"id":"chatcmpl-123","usage":"invalid"}`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "usage field is not a map (array)",
			body:      []byte(`{"id":"chatcmpl-123","usage":["invalid"]}`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "empty JSON object",
			body:      []byte(`{}`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "JSON with only whitespace",
			body:      []byte(`   `),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:    "usage with large token values",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":1000000,"completion_tokens":500000,"total_tokens":1500000}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     1000000,
				CompletionTokens: 500000,
				TotalTokens:      1500000,
			},
		},
		{
			name:    "usage with float values (valid JSON numbers)",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":10.5,"completion_tokens":5.9,"total_tokens":16.4}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     10, // Truncated from 10.5
				CompletionTokens: 5,  // Truncated from 5.9
				TotalTokens:      16, // Truncated from 16.4
			},
		},
		{
			name:      "invalid JSON with extra characters",
			body:      []byte(`{"usage":{}}not json`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "completely invalid JSON",
			body:      []byte(`this is not json at all`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "not a JSON object (just a string)",
			body:      []byte(`"just a string"`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:      "not a JSON object (just a number)",
			body:      []byte(`12345`),
			wantNil:   true,
			wantUsage: nil,
		},
		{
			name:    "usage with negative values",
			body:    []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":-10,"completion_tokens":-5,"total_tokens":-15}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     -10,
				CompletionTokens: -5,
				TotalTokens:      -15,
			},
		},
		{
			name:    "Anthropic-style response with usage",
			body:    []byte(`{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":10,"output_tokens":5}}`),
			wantNil: false,
			wantUsage: &store.Usage{
				PromptTokens:     0, // Anthropic fields not mapped
				CompletionTokens: 0,
				TotalTokens:      0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUsageFromResponse(tt.body)
			if tt.wantNil {
				if got != nil {
					t.Errorf("extractUsageFromResponse() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("extractUsageFromResponse() = nil, want %+v", tt.wantUsage)
				return
			}
			if got.PromptTokens != tt.wantUsage.PromptTokens {
				t.Errorf("extractUsageFromResponse().PromptTokens = %d, want %d", got.PromptTokens, tt.wantUsage.PromptTokens)
			}
			if got.CompletionTokens != tt.wantUsage.CompletionTokens {
				t.Errorf("extractUsageFromResponse().CompletionTokens = %d, want %d", got.CompletionTokens, tt.wantUsage.CompletionTokens)
			}
			if got.TotalTokens != tt.wantUsage.TotalTokens {
				t.Errorf("extractUsageFromResponse().TotalTokens = %d, want %d", got.TotalTokens, tt.wantUsage.TotalTokens)
			}
		})
	}
}
