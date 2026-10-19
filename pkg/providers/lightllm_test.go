package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// LightLLM provider commission — behavioral tests (2026-10-18).
//
// Scope: config-only OpenAI-compatible chat-completions integration
// (remove-external-upstream phase 2). LightLLM becomes a REGULAR
// provider — same shape as zhipu/zai/deepseek — reusing the plain
// OpenAIProvider at the inherited default base URL
// http://localhost:4001 (the retired external-upstream config
// default), overridable via credential.base_url for remote hosts.
// The tests below exercise the dispatch path through httptest fake
// upstreams and cover:
//
//   (a) override base URL preserved verbatim,
//   (b) non-stream chat completion (success),
//   (c) the OpenAI-shaped error envelope — HTTP 500 body
//       {"error":{"message":"...","type":"server_error"}} relayed as
//       a *ProviderError with the upstream status preserved,
//   (d) streaming SSE (success),
//   (e) the locked default base URL — drift tripwire.
//
// No live network calls. No API key in this VM.
// =============================================================================

const lightllmLockedDefaultBaseURL = "http://localhost:4001"

// TestNewProvider_LightLLM_AcceptsOverrideURL verifies that
// NewProvider("lightllm", ...) honors a user-provided base URL when
// one is supplied (override preservation — verbatim, no path
// rewriting). The locked default is itself pinned by
// TestLightLLM_ProviderMetadata_DefaultBaseURLConstant and set at the
// factory.go ProviderLightLLM case.
func TestNewProvider_LightLLM_AcceptsOverrideURL(t *testing.T) {
	p, err := NewProvider("lightllm", "test-key", "https://my-litellm.example/mypass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
	if p.Name() != "openai" {
		t.Errorf("expected provider Name() 'openai' (shared OpenAIProvider impl), got %q", p.Name())
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", p)
	}
	if op.baseURL != "https://my-litellm.example/mypass" {
		t.Errorf("override baseURL not preserved verbatim: got %q", op.baseURL)
	}
	// LightLLM carries no OpenCode flavour flag — no attribution seam
	// may ever engage for it.
	if op.openCodeGo || op.openCodeZen {
		t.Errorf("lightllm must carry no OpenCode flavour flag (openCodeGo=%v openCodeZen=%v)", op.openCodeGo, op.openCodeZen)
	}
}

// TestLightLLM_ChatCompletion_Success exercises the non-stream chat
// path through an httptest fake upstream. The fake asserts the request
// URL path is /chat/completions and the Authorization header carries
// the configured Bearer token — the canonical LiteLLM gateway
// transport.
func TestLightLLM_ChatCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Errorf("Authorization: got %q, want %q", got, want)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type: got %q, want application/json", got)
		}

		resp := ChatCompletionResponse{
			ID:      "llm-test-id",
			Object:  "chat.completion",
			Created: 1735000000,
			Model:   "my-local-model",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:    "assistant",
						Content: "Hello from LightLLM",
					},
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     9,
				CompletionTokens: 5,
				TotalTokens:      14,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Resolve the LightLLM provider via the factory — the fake
	// upstream URL above is what the handler actually hits when
	// callers pass a non-empty override (the credential-level
	// base_url). The empty-baseURL default route is covered by the
	// locked-default metadata test below.
	p, err := NewProvider("lightllm", "test-key", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "my-local-model",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}
	resp, err := p.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatCompletion: unexpected error: %v", err)
	}
	if resp.ID != "llm-test-id" {
		t.Errorf("ID: got %q, want %q", resp.ID, "llm-test-id")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices: got %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "Hello from LightLLM" {
		t.Errorf("Choices[0].Message.Content: got %v, want %q", resp.Choices[0].Message, "Hello from LightLLM")
	}
	if resp.Usage.TotalTokens != 14 {
		t.Errorf("Usage.TotalTokens: got %d, want 14", resp.Usage.TotalTokens)
	}
}

// TestLightLLM_ErrorEnvelope exercises the canonical LiteLLM gateway
// error shape: HTTP 500 with an OpenAI-shaped error body
//
//	{"error":{"message":"...","type":"server_error","code":null}}
//
// The proxy must (1) not panic, (2) return a *ProviderError (not a raw
// json error), (3) preserve the upstream HTTP status (500), and (4)
// surface the upstream message/type for retry logic.
func TestLightLLM_ErrorEnvelope(t *testing.T) {
	const (
		errorBody   = `{"error":{"message":"liteLLM Error: model not available","type":"server_error","code":null}}`
		innerType   = "server_error"
		innerPhrase = "model not available"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(errorBody))
	}))
	defer server.Close()

	p, err := NewProvider("lightllm", "test-key", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "my-local-model",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}
	_, err = p.ChatCompletion(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}

	var perr *ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if perr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode: got %d, want %d (upstream status must be preserved)", perr.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(perr.Message, innerPhrase) {
		t.Errorf("Message: got %q, expected to contain %q (graceful body relay)", perr.Message, innerPhrase)
	}
	if perr.ErrorType != innerType {
		t.Errorf("ErrorType: got %q, want %q (upstream error type should land in ErrorType)", perr.ErrorType, innerType)
	}
	// 500 is in the retryable set (429/5xx only) — the supervisor may
	// fail over to a fallback chain.
	if !perr.Retryable {
		t.Errorf("Retryable: got false, want true (5xx is retryable)")
	}
}

// TestLightLLM_StreamChatCompletion exercises the streaming SSE path
// through an httptest fake upstream, asserting end-to-end normalized
// StreamEvent emission (content → done).
func TestLightLLM_StreamChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Errorf("Authorization: got %q, want %q", got, want)
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			// Fatal, not Errorf: Goexit-ing the handler goroutine
			// aborts the connection so the client call returns an
			// error instead of hanging the SSE test.
			t.Fatalf("response writer does not support Flusher")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		chunks := []string{
			`data: {"id":"llm-stream-1","object":"chat.completion.chunk","created":1,"model":"my-local-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
			`data: {"id":"llm-stream-1","object":"chat.completion.chunk","created":1,"model":"my-local-model","choices":[{"index":0,"delta":{"content":" from"}}]}`,
			`data: {"id":"llm-stream-1","object":"chat.completion.chunk","created":1,"model":"my-local-model","choices":[{"index":0,"delta":{"content":" LightLLM"}}]}`,
			`data: {"id":"llm-stream-1","object":"chat.completion.chunk","created":1,"model":"my-local-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			if _, err := w.Write([]byte(c + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer server.Close()

	p, err := NewProvider("lightllm", "test-key", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "my-local-model",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}

	eventCh, err := p.StreamChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChatCompletion: unexpected error: %v", err)
	}

	var (
		events   []StreamEvent
		gotText  string
		gotDone  bool
		gotError bool
	)
	for ev := range eventCh {
		events = append(events, ev)
		switch ev.Type {
		case "content":
			gotText += ev.Content
		case "done":
			gotDone = true
		case "error":
			gotError = true
			t.Errorf("unexpected error event: %v", ev.Error)
		}
	}

	if gotError {
		t.Fatal("stream emitted an error event")
	}
	if !gotDone {
		t.Errorf("expected final 'done' event, got %d events: %+v", len(events), events)
	}
	if gotText != "Hello from LightLLM" {
		t.Errorf("concatenated content: got %q, want %q", gotText, "Hello from LightLLM")
	}
	if len(events) < 4 {
		// 3 content chunks + 1 done event minimum. Fatal, not Errorf:
		// events[0] is indexed below — a short stream must fail clean
		// instead of panicking on the index.
		t.Fatalf("expected at least 4 events, got %d", len(events))
	}
	if events[0].Type != "content" || events[0].Content != "Hello" {
		t.Errorf("first event: got type=%q content=%q, want content=\"Hello\"", events[0].Type, events[0].Content)
	}
}

// TestLightLLM_ProviderMetadata_DefaultBaseURLConstant locks the
// inherited default base URL. If this test fails, the commission's
// locked decision has been silently drifted — fix
// factory.go:ProviderLightLLM before merging.
func TestLightLLM_ProviderMetadata_DefaultBaseURLConstant(t *testing.T) {
	found := false
	for _, info := range GetProviders() {
		if info.Type == string(ProviderLightLLM) {
			found = true
			if info.BaseURL != lightllmLockedDefaultBaseURL {
				t.Errorf("ProviderInfo.BaseURL: got %q, want %q", info.BaseURL, lightllmLockedDefaultBaseURL)
			}
			if info.Name != "LightLLM" {
				t.Errorf("ProviderInfo.Name: got %q, want %q", info.Name, "LightLLM")
			}
			if info.Color == "" {
				t.Errorf("ProviderInfo.Color: empty (FE dropdown needs a non-empty badge color)")
			}
			break
		}
	}
	if !found {
		t.Errorf("ProviderLightLLM missing from GetProviders()")
	}
}
