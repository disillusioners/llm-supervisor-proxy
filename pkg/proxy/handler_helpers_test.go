package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

func TestExtractUsageFromChunk(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantUsage *store.Usage
		wantNil   bool
	}{
		{
			name: "valid usage data in chunk",
			data: []byte(`{"choices":[{"delta":{"content":"Hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
			wantNil: false,
		},
		{
			name:      "no usage field in chunk",
			data:      []byte(`{"choices":[{"delta":{"content":"Hello"}}]}`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name:      "malformed JSON",
			data:      []byte(`{"choices":[{"delta":{"content":`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name: "usage with zero values",
			data: []byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`),
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
			wantNil: false,
		},
		{
			name: "usage with only prompt_tokens",
			data: []byte(`{"usage":{"prompt_tokens":100}}`),
			wantUsage: &store.Usage{
				PromptTokens:     100,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
			wantNil: false,
		},
		{
			name: "usage with only completion_tokens",
			data: []byte(`{"usage":{"completion_tokens":50}}`),
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 50,
				TotalTokens:      0,
			},
			wantNil: false,
		},
		{
			name: "usage with only total_tokens",
			data: []byte(`{"usage":{"total_tokens":200}}`),
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      200,
			},
			wantNil: false,
		},
		{
			name:      "empty data",
			data:      []byte{},
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name:      "data is [DONE] string",
			data:      []byte(`[DONE]`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name: "usage with missing individual fields",
			data: []byte(`{"usage":{"prompt_tokens":10,"total_tokens":25}}`),
			wantUsage: &store.Usage{
				PromptTokens:     10,
				CompletionTokens: 0,
				TotalTokens:      25,
			},
			wantNil: false,
		},
		{
			name: "usage with non-numeric values (strings)",
			data: []byte(`{"usage":{"prompt_tokens":"ten","completion_tokens":5,"total_tokens":15}}`),
			wantUsage: &store.Usage{
				PromptTokens:     0,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
			wantNil: false,
		},
		{
			name:      "usage field is null",
			data:      []byte(`{"usage":null}`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name:      "usage field is not a map",
			data:      []byte(`{"usage":"invalid"}`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name:      "empty JSON object",
			data:      []byte(`{}`),
			wantUsage: nil,
			wantNil:   true,
		},
		{
			name: "usage with large token values",
			data: []byte(`{"usage":{"prompt_tokens":1000000,"completion_tokens":500000,"total_tokens":1500000}}`),
			wantUsage: &store.Usage{
				PromptTokens:     1000000,
				CompletionTokens: 500000,
				TotalTokens:      1500000,
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUsageFromChunk(tt.data)
			if tt.wantNil {
				if got != nil {
					t.Errorf("extractUsageFromChunk() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("extractUsageFromChunk() = nil, want %+v", tt.wantUsage)
				return
			}
			if got.PromptTokens != tt.wantUsage.PromptTokens {
				t.Errorf("extractUsageFromChunk().PromptTokens = %d, want %d", got.PromptTokens, tt.wantUsage.PromptTokens)
			}
			if got.CompletionTokens != tt.wantUsage.CompletionTokens {
				t.Errorf("extractUsageFromChunk().CompletionTokens = %d, want %d", got.CompletionTokens, tt.wantUsage.CompletionTokens)
			}
			if got.TotalTokens != tt.wantUsage.TotalTokens {
				t.Errorf("extractUsageFromChunk().TotalTokens = %d, want %d", got.TotalTokens, tt.wantUsage.TotalTokens)
			}
		})
	}
}
