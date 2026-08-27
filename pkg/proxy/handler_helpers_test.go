package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestExtractNonStreamContent verifies that the helper used by
// handleNonStreamResult correctly extracts content AND reasoning variants
// from non-stream response bodies. The "none" case must match legacy behavior
// (content extracted, thinking empty).
func TestExtractNonStreamContent(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantContent string
		wantThinking string
	}{
		{
			name: "reasoning_content top-level",
			body: `{"choices":[{"message":{"role":"assistant","content":"answer","reasoning_content":"thought-A"}}]}`,
			wantContent: "answer",
			wantThinking: "thought-A",
		},
		{
			name: "reasoning top-level",
			body: `{"choices":[{"message":{"role":"assistant","content":"answer","reasoning":"thought-B"}}]}`,
			wantContent: "answer",
			wantThinking: "thought-B",
		},
		{
			name: "thinking top-level",
			body: `{"choices":[{"message":{"role":"assistant","content":"answer","thinking":"thought-C"}}]}`,
			wantContent: "answer",
			wantThinking: "thought-C",
		},
		{
			name: "provider_specific_fields.reasoning_content",
			body: `{"choices":[{"message":{"role":"assistant","content":"answer","provider_specific_fields":{"reasoning_content":"thought-D"}}}]}`,
			wantContent: "answer",
			wantThinking: "thought-D",
		},
		{
			name: "none - legacy plain content",
			body: `{"choices":[{"message":{"role":"assistant","content":"plain answer"}}]}`,
			wantContent: "plain answer",
			wantThinking: "",
		},
		{
			name: "empty content + reasoning_content",
			body: `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"thought-E"}}]}`,
			wantContent: "",
			wantThinking: "thought-E",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp, thinking strings.Builder
			extractNonStreamContent([]byte(tt.body), &resp, &thinking)

			if got := resp.String(); got != tt.wantContent {
				t.Errorf("content: got %q, want %q", got, tt.wantContent)
			}
			if got := thinking.String(); got != tt.wantThinking {
				t.Errorf("thinking: got %q, want %q", got, tt.wantThinking)
			}
		})
	}
}

// TestRequestContext_BufferModeReset verifies the per-request lifecycle of
// rc.bufferMode (real-streaming-default plan, Phase 1): initRequestContext
// populates it from the X-LLMProxy-Buffer-Response header, and reset()
// returns it to false so a recycled requestContext cannot leak buffered
// mode from a prior request. Also pins the Q4 companion reset: the
// streamingNonRetryable flag is now a sync/atomic.Bool whose reset goes
// through Store(false).
func TestRequestContext_BufferModeReset(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	h := newTestHandlerWithURL(t, upstream.URL)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}
	req := buildRequestBodyForInitContext(t, body)
	// PRESENT + truthy ⇒ buffered. Direct map access under the canonical
	// key (mirrors the server-side header parser; see
	// TestBufferModeHeaderParsing for the canonicalization rationale).
	req.Header[http.CanonicalHeaderKey("X-LLMProxy-Buffer-Response")] = []string{"true"}

	rc, err := h.initRequestContext(req)
	if err != nil {
		t.Fatalf("initRequestContext() error = %v", err)
	}
	if !rc.bufferMode {
		t.Fatal("initRequestContext() bufferMode = false, want true (header PRESENT + \"true\" must opt into buffered mode)")
	}

	rc.reset()
	if rc.bufferMode {
		t.Error("after reset(): bufferMode = true, want false (delivery mode must not leak across request lifecycles)")
	}

	// Q4 companion: streamingNonRetryable shares the reset path and is now
	// an atomic.Bool — its reset must go through Store(false).
	rc.streamingNonRetryable.Store(true)
	rc.reset()
	if rc.streamingNonRetryable.Load() {
		t.Error("after reset(): streamingNonRetryable = true, want false (atomic Bool reset must Store(false))")
	}
}
