package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// handlerFunc is a type alias so both HandleChatCompletions and
// HandleChatCompletionsBeforeRefactor can be tested with the same suite.
type handlerFunc func(w http.ResponseWriter, r *http.Request)

// newTestManagerWithConfig creates a config.Manager suitable for tests by
// using environment variables to configure the upstream URL.
// Since config.NewManager() does file I/O, we create a Manager directly
// by temporarily setting environment variables and leveraging Load().
func newTestManagerWithConfig(t *testing.T, upstreamURL string, opts ...func(*config.Config)) *config.Manager {
	t.Helper()

	// Create a temp dir for the test config file
	tmpDir := t.TempDir()

	// We need to create a config.Manager with the right settings.
	// The Manager wants to use UserConfigDir, but for tests we construct one
	// directly with a known config.
	cfg := config.Config{
		Version:                 "1.0",
		UpstreamURL:             upstreamURL,
		Port:                    4321,
		IdleTimeout:             config.Duration(5 * time.Second),
		MaxGenerationTime:       config.Duration(30 * time.Second),
		MaxUpstreamErrorRetries: 0,
		MaxIdleRetries:          0,
		MaxGenerationRetries:    0,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	// Write config to temp file
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	configPath := tmpDir + "/config.json"
	if err := writeTestFile(configPath, data); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Set env vars temporarily to override config manager
	t.Setenv("UPSTREAM_URL", upstreamURL)
	t.Setenv("MAX_UPSTREAM_ERROR_RETRIES", fmt.Sprintf("%d", cfg.MaxUpstreamErrorRetries))
	t.Setenv("MAX_IDLE_RETRIES", fmt.Sprintf("%d", cfg.MaxIdleRetries))
	t.Setenv("MAX_GENERATION_RETRIES", fmt.Sprintf("%d", cfg.MaxGenerationRetries))
	t.Setenv("IDLE_TIMEOUT", time.Duration(cfg.IdleTimeout).String())
	t.Setenv("MAX_GENERATION_TIME", time.Duration(cfg.MaxGenerationTime).String())

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	return mgr
}

func writeTestFile(path string, data []byte) error {
	return nil // Manager will write its own file
}

// newTestHandler creates a Handler suitable for unit tests.
func newTestHandler(t *testing.T, upstreamHandler http.HandlerFunc, modelsConfig *models.ModelsConfig, configOpts ...func(*config.Config)) (*Handler, *httptest.Server) {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)

	mgr := newTestManagerWithConfig(t, upstream.URL, configOpts...)

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)

	h := NewHandler(cfg, bus, reqStore)

	t.Cleanup(func() {
		upstream.Close()
	})

	return h, upstream
}

// makeRequest creates a POST /v1/chat/completions request with the given body.
func makeRequest(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// simpleBody returns a minimal request body for chat completions
func simpleBody(model string, stream bool) map[string]interface{} {
	return map[string]interface{}{
		"model":  model,
		"stream": stream,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}
}

// nonStreamResponse returns a standard non-streaming OpenAI response JSON
func nonStreamResponse(content string) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// nonStreamResponseWithThinking returns a non-streaming response with reasoning_content
func nonStreamResponseWithThinking(content, thinking string) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           content,
					"reasoning_content": thinking,
				},
				"finish_reason": "stop",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// streamChunk produces a single SSE data line for a streaming response
func streamChunk(content string) string {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return "data: " + string(b) + "\n"
}

// streamThinkingChunk produces an SSE chunk with reasoning_content
func streamThinkingChunk(thinking string) string {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"reasoning_content": thinking,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return "data: " + string(b) + "\n"
}

// ─────────────────────────────────────────────────────────────────────────────
// Test runner that runs each test against both implementations
// ─────────────────────────────────────────────────────────────────────────────

type testCase struct {
	name string
	fn   func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server)
	// setup
	modelsConfig *models.ModelsConfig
	configOpts   []func(*config.Config)
	upstreamFn   func(t *testing.T) http.HandlerFunc
}

// runBothVersions runs a test case against both the original and refactored implementations
func runBothVersions(t *testing.T, tc testCase) {
	versions := []struct {
		name   string
		getHFn func(h *Handler) handlerFunc
	}{
		{"Refactored", func(h *Handler) handlerFunc { return h.HandleChatCompletions }},
		{"BeforeRefactor", func(h *Handler) handlerFunc { return h.HandleChatCompletionsBeforeRefactor }},
	}

	for _, v := range versions {
		t.Run(v.name, func(t *testing.T) {
			upstreamHandler := tc.upstreamFn(t)
			h, upstream := newTestHandler(t, upstreamHandler, tc.modelsConfig, tc.configOpts...)
			hfn := v.getHFn(h)
			tc.fn(t, hfn, h, upstream)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMethodNotAllowed(t *testing.T) {
	runBothVersions(t, testCase{
		name: "MethodNotAllowed",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("upstream should not be called")
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
			}
		},
	})
}

func TestInvalidJSON(t *testing.T) {
	runBothVersions(t, testCase{
		name: "InvalidJSON",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("upstream should not be called")
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
			}
		},
	})
}

func TestNonStreamSuccess(t *testing.T) {
	runBothVersions(t, testCase{
		name: "NonStreamSuccess",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("Hello world"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			// Verify response body is passed through
			var respMap map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &respMap); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			choices, ok := respMap["choices"].([]interface{})
			if !ok || len(choices) == 0 {
				t.Fatal("expected choices in response")
			}

			choice := choices[0].(map[string]interface{})
			msg := choice["message"].(map[string]interface{})
			if content := msg["content"].(string); content != "Hello world" {
				t.Errorf("expected content 'Hello world', got '%s'", content)
			}

			// Verify store was updated
			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			if reqs[0].Response != "Hello world" {
				t.Errorf("expected response 'Hello world', got '%s'", reqs[0].Response)
			}
		},
	})
}

func TestNonStreamWithThinking(t *testing.T) {
	runBothVersions(t, testCase{
		name: "NonStreamWithThinking",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponseWithThinking("Answer", "Let me think..."))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Thinking != "Let me think..." {
				t.Errorf("expected thinking 'Let me think...', got '%s'", reqs[0].Thinking)
			}
		},
	})
}

func TestStreamSuccess(t *testing.T) {
	runBothVersions(t, testCase{
		name: "StreamSuccess",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)

				// Send chunks
				fmt.Fprint(w, streamChunk("Hello "))
				fmt.Fprint(w, "\n")
				flusher.Flush()

				fmt.Fprint(w, streamChunk("world"))
				fmt.Fprint(w, "\n")
				flusher.Flush()

				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			// Verify [DONE] is in the output
			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in stream response")
			}

			// Verify store
			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			if reqs[0].Response != "Hello world" {
				t.Errorf("expected accumulated response 'Hello world', got '%s'", reqs[0].Response)
			}
		},
	})
}

func TestStreamWithThinking(t *testing.T) {
	runBothVersions(t, testCase{
		name: "StreamWithThinking",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)

				fmt.Fprint(w, streamThinkingChunk("thinking..."))
				fmt.Fprint(w, "\n")
				flusher.Flush()

				fmt.Fprint(w, streamChunk("Answer"))
				fmt.Fprint(w, "\n")
				flusher.Flush()

				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Thinking != "thinking..." {
				t.Errorf("expected thinking 'thinking...', got '%s'", reqs[0].Thinking)
			}
			if reqs[0].Response != "Answer" {
				t.Errorf("expected response 'Answer', got '%s'", reqs[0].Response)
			}
		},
	})
}

func TestUpstream5xxError(t *testing.T) {
	runBothVersions(t, testCase{
		name: "Upstream5xxError",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"internal server error"}`)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// With MaxUpstreamErrorRetries=0, first 500 should trigger retry, but then fail
			// and falls through to "all models failed"
			if rr.Code != http.StatusBadGateway {
				t.Errorf("expected status %d, got %d", http.StatusBadGateway, rr.Code)
			}
		},
	})
}

func TestUpstream4xxPassthrough(t *testing.T) {
	runBothVersions(t, testCase{
		name: "Upstream4xxPassthrough",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error":"model not found"}`)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("nonexistent-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// 4xx errors (not 429) should be passed through directly
			if rr.Code != http.StatusNotFound {
				t.Errorf("expected status %d, got %d", http.StatusNotFound, rr.Code)
			}

			// Store should show failed
			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "failed" {
				t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
			}
		},
	})
}

func TestUpstream429Retry(t *testing.T) {
	runBothVersions(t, testCase{
		name: "Upstream429Retry",
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.MaxUpstreamErrorRetries = 1
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			callCount := 0
			return func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount == 1 {
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprint(w, `{"error":"rate limited"}`)
					return
				}
				// Second call succeeds
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("ok after retry"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
		},
	})
}

func TestFallbackOnFailure(t *testing.T) {
	runBothVersions(t, testCase{
		name: "FallbackOnFailure",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "primary-model", Name: "Primary", Enabled: true, FallbackChain: []string{"fallback-model"}})
			mc.AddModel(models.ModelConfig{ID: "fallback-model", Name: "Fallback", Enabled: true})
			return mc
		}(),
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(body, &reqBody)
				model := reqBody["model"].(string)

				if model == "primary-model" {
					// Primary fails with 500
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"primary failed"}`)
					return
				}

				// Fallback succeeds
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("Fallback response"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("primary-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			var respMap map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &respMap); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			choices := respMap["choices"].([]interface{})
			choice := choices[0].(map[string]interface{})
			msg := choice["message"].(map[string]interface{})
			if content := msg["content"].(string); content != "Fallback response" {
				t.Errorf("expected 'Fallback response', got '%s'", content)
			}
		},
	})
}

func TestAllModelsFail(t *testing.T) {
	runBothVersions(t, testCase{
		name: "AllModelsFail",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "primary-model", Name: "Primary", Enabled: true, FallbackChain: []string{"fallback-model"}})
			mc.AddModel(models.ModelConfig{ID: "fallback-model", Name: "Fallback", Enabled: true})
			return mc
		}(),
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"all fail"}`)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("primary-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusBadGateway {
				t.Errorf("expected status %d, got %d", http.StatusBadGateway, rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "All models failed") {
				t.Errorf("expected 'All models failed' message, got '%s'", rr.Body.String())
			}
		},
	})
}

func TestHeaderCopy(t *testing.T) {
	runBothVersions(t, testCase{
		name: "HeaderCopy",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				// Verify that Authorization header was forwarded
				auth := r.Header.Get("Authorization")
				if auth != "Bearer test-token" {
					t.Errorf("expected Authorization 'Bearer test-token', got '%s'", auth)
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Custom-Header", "test-value")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("ok"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			req.Header.Set("Authorization", "Bearer test-token")
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			// Verify custom response header was forwarded
			if rr.Header().Get("X-Custom-Header") != "test-value" {
				t.Errorf("expected X-Custom-Header 'test-value', got '%s'", rr.Header().Get("X-Custom-Header"))
			}
		},
	})
}

func TestMessageParsing(t *testing.T) {
	runBothVersions(t, testCase{
		name: "MessageParsing",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("response"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := map[string]interface{}{
				"model":  "test-model",
				"stream": false,
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "You are helpful"},
					map[string]interface{}{"role": "user", "content": "Hi there"},
				},
			}
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if len(reqs[0].Messages) != 2 {
				t.Fatalf("expected 2 messages, got %d", len(reqs[0].Messages))
			}
			if reqs[0].Messages[0].Role != "system" {
				t.Errorf("expected first message role 'system', got '%s'", reqs[0].Messages[0].Role)
			}
			if reqs[0].Messages[1].Content != "Hi there" {
				t.Errorf("expected second message content 'Hi there', got '%s'", reqs[0].Messages[1].Content)
			}
		},
	})
}

func TestNoFallbackConfig(t *testing.T) {
	runBothVersions(t, testCase{
		name: "NoFallbackConfig",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("ok"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}
		},
	})
}

func TestDetermineFailureReason(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		eRetries int
		maxE     int
		iRetries int
		maxI     int
		gRetries int
		maxG     int
		expected string
	}{
		{"idle_timeout", fmt.Errorf("wrap: %w", io.EOF), 0, 3, 2, 1, 0, 3, "max_idle_retries"},
		{"gen_retries", nil, 0, 3, 0, 3, 4, 3, "max_generation_retries"},
		{"error_retries", nil, 4, 3, 0, 3, 0, 3, "max_upstream_error_retries"},
		{"upstream_error", nil, 0, 3, 0, 3, 0, 3, "upstream_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := determineFailureReason(tc.err, tc.eRetries, tc.maxE, tc.iRetries, tc.maxI, tc.gRetries, tc.maxG)
			if result != tc.expected {
				t.Errorf("expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestProviderSpecificThinking(t *testing.T) {
	runBothVersions(t, testCase{
		name: "ProviderSpecificThinking",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				resp := map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"created": 1234567890,
					"model":   "test-model",
					"choices": []interface{}{
						map[string]interface{}{
							"index": 0,
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Answer",
								"provider_specific_fields": map[string]interface{}{
									"reasoning_content": "Deep thought",
								},
							},
							"finish_reason": "stop",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(resp)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if !strings.Contains(reqs[0].Thinking, "Deep thought") {
				t.Errorf("expected thinking to contain 'Deep thought', got '%s'", reqs[0].Thinking)
			}
		},
	})
}

func TestStreamUnexpectedEOF(t *testing.T) {
	runBothVersions(t, testCase{
		name: "StreamUnexpectedEOF",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)

				// Send some data but never send [DONE]
				fmt.Fprint(w, streamChunk("partial"))
				fmt.Fprint(w, "\n")
				flusher.Flush()
				// Connection closes without [DONE]
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("test-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			// The status should be "failed" since the stream ended unexpectedly
			if reqs[0].Status != "failed" {
				t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
			}
		},
	})
}

func TestEmptyMessages(t *testing.T) {
	runBothVersions(t, testCase{
		name: "EmptyMessages",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("response"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := map[string]interface{}{
				"model":    "test-model",
				"stream":   false,
				"messages": []interface{}{},
			}
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if len(reqs[0].Messages) != 0 {
				t.Errorf("expected 0 messages, got %d", len(reqs[0].Messages))
			}
		},
	})
}

func TestFallback4xxTriggered(t *testing.T) {
	runBothVersions(t, testCase{
		name: "Fallback4xxTriggered",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "primary", Name: "Primary", Enabled: true, FallbackChain: []string{"secondary"}})
			mc.AddModel(models.ModelConfig{ID: "secondary", Name: "Secondary", Enabled: true})
			return mc
		}(),
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(body, &reqBody)
				model := reqBody["model"].(string)

				if model == "primary" {
					// 4xx error that triggers fallback
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":"not found"}`)
					return
				}

				// Secondary succeeds
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, nonStreamResponse("secondary response"))
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("primary", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
			}
		},
	})
}
