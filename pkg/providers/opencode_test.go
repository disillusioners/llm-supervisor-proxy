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
// OpenCode Zen provider commission — behavioral tests (2026-10-17).
//
// Scope: config-only OpenAI-compatible chat-completions integration. The
// provider layer dispatches OpenAIProvider at the locked default base URL
// https://opencode.ai/inference/openai/v1. The tests below exercise the
// dispatch path through httptest fake upstreams and cover:
//
//   (a) non-stream chat completion (success),
//   (b) streaming SSE (success),
//   (c) the Anthropic-shaped error envelope quirk — HTTP 403 body
//       {"type":"error","error":{"type":"FreeTierError","message":"..."}}
//       on the OpenAI route. The proxy must relay such bodies gracefully
//       without a parse crash and preserve the upstream HTTP status.
//
// No live network calls. No API key in this VM.
// =============================================================================

const opencodeLockedDefaultBaseURL = "https://opencode.ai/inference/openai/v1"

// TestNewProvider_Opencode_BaseURL_Override verifies that
// NewProvider("opencode", ...) honors a user-provided base URL when
// one is supplied (override preservation). The locked default is
// itself pinned by TestOpencode_ProviderMetadata_DefaultBaseURLConstant
// and set at factory.go:87.
func TestNewProvider_Opencode_BaseURL_Override(t *testing.T) {
	p, err := NewProvider("opencode", "test-key", "https://opencode.ai/zen/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
	if p.Name() != "openai" {
		t.Errorf("expected provider Name() 'openai' (shared OpenAIProvider impl), got %q", p.Name())
	}
}

// TestOpencode_ChatCompletion_Success exercises the non-stream chat path
// through an httptest fake upstream. The fake asserts the request URL
// path is /chat/completions and the Authorization header carries the
// configured Bearer token — the canonical OpenCode Zen credential
// transport on the v2 Console path.
func TestOpencode_ChatCompletion_Success(t *testing.T) {
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
			ID:      "oc-test-id",
			Object:  "chat.completion",
			Created: 1735000000,
			Model:   "kimi-k3",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:    "assistant",
						Content: "Hello from OpenCode Zen",
					},
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     12,
				CompletionTokens: 6,
				TotalTokens:      18,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Resolve the OpenCode provider via the factory — empty baseURL
	// falls back to the locked v2 Console default inside NewProvider;
	// the fake upstream URL above is what the handler actually hits
	// when callers pass a non-empty override. Both routes must work.
	overrideProvider, err := NewProvider("opencode", "test-key", server.URL)
	if err != nil {
		t.Fatalf("override: unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "kimi-k3",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}
	resp, err := overrideProvider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatCompletion: unexpected error: %v", err)
	}
	if resp.ID != "oc-test-id" {
		t.Errorf("ID: got %q, want %q", resp.ID, "oc-test-id")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices: got %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "Hello from OpenCode Zen" {
		t.Errorf("Choices[0].Message.Content: got %v, want %q", resp.Choices[0].Message, "Hello from OpenCode Zen")
	}
	if resp.Usage.TotalTokens != 18 {
		t.Errorf("Usage.TotalTokens: got %d, want 18", resp.Usage.TotalTokens)
	}
}

// TestOpencode_ChatCompletion_AnthropicShapedErrorEnvelope exercises
// the documented OpenCode Zen quirk: upstream errors on the OpenAI
// route can be Anthropic-shaped — e.g. HTTP 403 body
//
//	{"type":"error","error":{"type":"FreeTierError","message":"..."}}
//
// The proxy must (1) not panic, (2) return a *ProviderError (not a raw
// json error), (3) preserve the upstream HTTP status (403), and (4)
// relay the body gracefully without crash.
//
// Relay contract observed: the OpenAI-shaped handleError schema
// `{Error:{Message,Type,Code}}` happens to share field names with the
// Anthropic inner envelope `{error:{type,message}}`, so the JSON
// decode succeeds and ProviderError.Message / ProviderError.ErrorType
// (Round 3 R3-1) capture the inner Anthropic values verbatim. The
// outer Anthropic envelope `type` ("error") is dropped, but that's
// fine — the FE surfaces Message + ErrorType and the upstream status
// is preserved.
func TestOpencode_ChatCompletion_AnthropicShapedErrorEnvelope(t *testing.T) {
	const (
		anthropicShapedBody = `{"type":"error","error":{"type":"FreeTierError","message":"free tier exhausted for this model"}}`
		innerMessage        = "free tier exhausted for this model"
		innerType           = "FreeTierError"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(anthropicShapedBody))
	}))
	defer server.Close()

	p, err := NewProvider("opencode", "test-key", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "kimi-k3",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}
	_, err = p.ChatCompletion(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for HTTP 403, got nil")
	}

	var perr *ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if perr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode: got %d, want %d (upstream status must be preserved)", perr.StatusCode, http.StatusForbidden)
	}
	// The Anthropic inner message surfaces verbatim in ProviderError.Message
	// (handleError's {Error:{Message,Type,Code}} schema matches the
	// Anthropic {error:{message,type,...}} envelope field-for-field for
	// these two fields).
	if !strings.Contains(perr.Message, innerMessage) {
		t.Errorf("Message: got %q, expected to contain inner Anthropic message %q (graceful body relay)", perr.Message, innerMessage)
	}
	// The Anthropic inner type lands in ProviderError.ErrorType (Round 3
	// R3-1). FreeTierError survives — FEs/clients that key off ErrorType
	// for retry decisions get a usable signal.
	if perr.ErrorType != innerType {
		t.Errorf("ErrorType: got %q, want %q (Anthropic inner type should land in ErrorType)", perr.ErrorType, innerType)
	}
	// 403 is not in the retryable set (429/5xx only).
	if perr.Retryable {
		t.Errorf("Retryable: got true, want false (403 is non-retryable)")
	}
}

// TestOpencode_StreamChatCompletion exercises the streaming SSE path
// through an httptest fake upstream, asserting end-to-end normalized
// StreamEvent emission (content → done).
func TestOpencode_StreamChatCompletion(t *testing.T) {
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
			t.Errorf("response writer does not support Flusher")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		chunks := []string{
			`data: {"id":"oc-stream-1","object":"chat.completion.chunk","created":1,"model":"kimi-k3","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
			`data: {"id":"oc-stream-1","object":"chat.completion.chunk","created":1,"model":"kimi-k3","choices":[{"index":0,"delta":{"content":" from"}}]}`,
			`data: {"id":"oc-stream-1","object":"chat.completion.chunk","created":1,"model":"kimi-k3","choices":[{"index":0,"delta":{"content":" Zen"}}]}`,
			`data: {"id":"oc-stream-1","object":"chat.completion.chunk","created":1,"model":"kimi-k3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
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

	p, err := NewProvider("opencode", "test-key", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := &ChatCompletionRequest{
		Model:    "kimi-k3",
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
	if gotText != "Hello from Zen" {
		t.Errorf("concatenated content: got %q, want %q", gotText, "Hello from Zen")
	}
	if len(events) < 4 {
		// 3 content chunks + 1 done event minimum.
		t.Errorf("expected at least 4 events, got %d", len(events))
	}
	if events[0].Type != "content" || events[0].Content != "Hello" {
		t.Errorf("first event: got type=%q content=%q, want content=\"Hello\"", events[0].Type, events[0].Content)
	}
}

// TestOpencode_ProviderMetadata_DefaultBaseURLConstant locks the
// documented v2 Console default base URL. If this test fails, the
// commission's locked decision has been silently drifted — fix
// factory.go:ProviderOpencode before merging.
func TestOpencode_ProviderMetadata_DefaultBaseURLConstant(t *testing.T) {
	if opencodeLockedDefaultBaseURL != "https://opencode.ai/inference/openai/v1" {
		t.Fatalf("locked default base URL drifted: got %q", opencodeLockedDefaultBaseURL)
	}

	found := false
	for _, info := range GetProviders() {
		if info.Type == string(ProviderOpencode) {
			found = true
			if info.BaseURL != opencodeLockedDefaultBaseURL {
				t.Errorf("ProviderInfo.BaseURL: got %q, want %q", info.BaseURL, opencodeLockedDefaultBaseURL)
			}
			if info.Name != "OpenCode Zen" {
				t.Errorf("ProviderInfo.Name: got %q, want %q", info.Name, "OpenCode Zen")
			}
			if info.Color == "" {
				t.Errorf("ProviderInfo.Color: empty (FE dropdown needs a non-empty badge color)")
			}
			break
		}
	}
	if !found {
		t.Errorf("ProviderOpencode missing from GetProviders()")
	}
}