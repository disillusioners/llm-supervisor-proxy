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
// Mock LLM server (replicates test/mock_llm.go scenarios)
// ─────────────────────────────────────────────────────────────────────────────

func mockCreateChunk(content string) string {
	chunk := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func mockCreateReasoningChunk(content string) string {
	chunk := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"reasoning_content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func mockNonStreamResponse(content string) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
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

// mockLLMHandler creates an http.Handler that mimics test/mock_llm.go behavior.
// It responds differently based on prompt keywords: mock-hang, mock-500,
// mock-think, mock-tool, mock-long, or normal.
func mockLLMHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		var reqBody map[string]interface{}
		if err := json.Unmarshal(reqBodyBytes, &reqBody); err != nil {
			http.Error(w, "Bad JSON", http.StatusBadRequest)
			return
		}

		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		// Extract prompt from all messages
		var prompt string
		if msgs, ok := reqBody["messages"].([]interface{}); ok {
			for _, mb := range msgs {
				if msg, ok := mb.(map[string]interface{}); ok {
					if content, ok := msg["content"].(string); ok {
						prompt += content + " "
					}
				}
			}
		}

		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, mockNonStreamResponse("Hello world! I am a useful token stream."))
			return
		}

		// Check for special scenarios BEFORE setting headers
		if strings.Contains(prompt, "mock-500") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"Internal Server Error"}`)
			return
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		tokens := []string{"Hello", " world", "!", " I", " am", " a", " useful", " token", " stream", "."}

		if strings.Contains(prompt, "mock-hang") {
			// Send some tokens then hang until context cancels
			for i, token := range tokens {
				if i > 5 {
					<-r.Context().Done()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(token))
				flusher.Flush()
			}
		} else if strings.Contains(prompt, "mock-think") {
			// Send thinking content then real content
			thinkTokens := []string{"Hmm", ", ", "let", " me", " think", " about", " that", "."}
			for _, t := range thinkTokens {
				fmt.Fprintf(w, "data: %s\n\n", mockCreateReasoningChunk(t))
				flusher.Flush()
			}
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Here is the answer."))
			flusher.Flush()
		} else if strings.Contains(prompt, "mock-tool") {
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Sure, checking the weather."))
			flusher.Flush()
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("\n[TOOL CALL: get_weather]"))
			flusher.Flush()
		} else if strings.Contains(prompt, "mock-long") {
			for i := 0; i < 100; i++ {
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(fmt.Sprintf(" word%d", i)))
				flusher.Flush()
			}
		} else {
			// Normal response
			for _, token := range tokens {
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(token))
				flusher.Flush()
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

type handlerFunc func(w http.ResponseWriter, r *http.Request)

func newTestManagerWithConfig(t *testing.T, upstreamURL string, opts ...func(*config.Config)) *config.Manager {
	t.Helper()

	t.Setenv("UPSTREAM_URL", upstreamURL)

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	// Apply option overrides via Save (to set non-env values)
	if len(opts) > 0 {
		cfg := mgr.Get()
		for _, opt := range opts {
			opt(&cfg)
		}
		// Only override env-controllable settings
		t.Setenv("MAX_UPSTREAM_ERROR_RETRIES", fmt.Sprintf("%d", cfg.MaxUpstreamErrorRetries))
		t.Setenv("MAX_IDLE_RETRIES", fmt.Sprintf("%d", cfg.MaxIdleRetries))
		t.Setenv("MAX_GENERATION_RETRIES", fmt.Sprintf("%d", cfg.MaxGenerationRetries))
		t.Setenv("IDLE_TIMEOUT", time.Duration(cfg.IdleTimeout).String())
		t.Setenv("MAX_GENERATION_TIME", time.Duration(cfg.MaxGenerationTime).String())

		// Re-create to pick up env vars
		mgr, err = config.NewManager()
		if err != nil {
			t.Fatalf("new manager after opts: %v", err)
		}
	}

	return mgr
}

func newTestHandler(t *testing.T, upstreamHandler http.HandlerFunc, modelsConfig models.ModelsConfigInterface, configOpts ...func(*config.Config)) (*Handler, *httptest.Server) {
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

func bodyWithPrompt(model string, stream bool, prompt string) map[string]interface{} {
	return map[string]interface{}{
		"model":  model,
		"stream": stream,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": prompt,
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test runner
// ─────────────────────────────────────────────────────────────────────────────

type testCase struct {
	name         string
	fn           func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server)
	modelsConfig models.ModelsConfigInterface
	configOpts   []func(*config.Config)
	upstreamFn   func(t *testing.T) http.HandlerFunc
}

func runBothVersions(t *testing.T, tc testCase) {
	// Only test the current implementation (refactored version)
	hfn := func(h *Handler) handlerFunc { return h.HandleChatCompletions }
	t.Run("Refactored", func(t *testing.T) {
		upstreamHandler := tc.upstreamFn(t)
		h, upstream := newTestHandler(t, upstreamHandler, tc.modelsConfig, tc.configOpts...)
		fn := hfn(h)
		tc.fn(t, fn, h, upstream)
	})
}

// ═══════════════════════════════════════════════════════════════════════════════
// Unit tests (simple mock upstream)
// ═══════════════════════════════════════════════════════════════════════════════

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

// ═══════════════════════════════════════════════════════════════════════════════
// Integration tests using mock LLM (mirrors test/mock_llm.go scenarios)
// ═══════════════════════════════════════════════════════════════════════════════

func TestMockLLM_NormalStreaming(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_NormalStreaming",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			respBody := rr.Body.String()

			// Should contain [DONE]
			if !strings.Contains(respBody, "[DONE]") {
				t.Error("expected [DONE] in stream response")
			}

			// Verify accumulated content in store
			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			expectedContent := "Hello world! I am a useful token stream."
			if reqs[0].Response != expectedContent {
				t.Errorf("expected response '%s', got '%s'", expectedContent, reqs[0].Response)
			}
		},
	})
}

func TestMockLLM_NonStreaming(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_NonStreaming",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", false)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Parse response
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
			content := msg["content"].(string)
			if content != "Hello world! I am a useful token stream." {
				t.Errorf("unexpected content: '%s'", content)
			}

			// Verify store
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

func TestMockLLM_ThinkingStream(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_ThinkingStream",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := bodyWithPrompt("mock-model", true, "mock-think please")
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in stream response")
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			// Verify thinking was accumulated
			expectedThinking := "Hmm, let me think about that."
			if reqs[0].Thinking != expectedThinking {
				t.Errorf("expected thinking '%s', got '%s'", expectedThinking, reqs[0].Thinking)
			}
			// Verify response content
			if reqs[0].Response != "Here is the answer." {
				t.Errorf("expected response 'Here is the answer.', got '%s'", reqs[0].Response)
			}
		},
	})
}

func TestMockLLM_ToolCall(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_ToolCall",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := bodyWithPrompt("mock-model", true, "mock-tool call")
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			// Response should contain both messages
			if !strings.Contains(reqs[0].Response, "checking the weather") {
				t.Errorf("expected response to contain 'checking the weather', got '%s'", reqs[0].Response)
			}
			if !strings.Contains(reqs[0].Response, "TOOL CALL") {
				t.Errorf("expected response to contain 'TOOL CALL', got '%s'", reqs[0].Response)
			}
		},
	})
}

func TestMockLLM_LongStream(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_LongStream",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := bodyWithPrompt("mock-model", true, "mock-long content")
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in stream response")
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}

			// Should contain all 100 words
			for i := 0; i < 100; i++ {
				expected := fmt.Sprintf("word%d", i)
				if !strings.Contains(reqs[0].Response, expected) {
					t.Errorf("expected response to contain '%s'", expected)
					break
				}
			}
		},
	})
}

func TestMockLLM_500Error(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_500Error",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := bodyWithPrompt("mock-model", true, "mock-500 trigger")
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// With MaxUpstreamErrorRetries=0 (default in test), should return 502
			if rr.Code != http.StatusBadGateway {
				t.Errorf("expected status %d, got %d", http.StatusBadGateway, rr.Code)
			}

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

func TestMockLLM_500WithRetryThenSuccess(t *testing.T) {
	callCount := 0
	runBothVersions(t, testCase{
		name: "MockLLM_500WithRetryThenSuccess",
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.MaxUpstreamErrorRetries = 2
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			callCount = 0
			return func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount <= 1 {
					// First call: 500
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"temporary failure"}`)
					return
				}
				// Second call: success
				mockLLMHandler(t)(w, r)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in stream response")
			}

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "completed" {
				t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
			}
			if reqs[0].Retries < 1 {
				t.Errorf("expected at least 1 retry, got %d", reqs[0].Retries)
			}
		},
	})
}

func TestMockLLM_HangWithIdleTimeout(t *testing.T) {
	runBothVersions(t, testCase{
		name: "MockLLM_HangWithIdleTimeout",
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.IdleTimeout = config.Duration(500 * time.Millisecond) // Fast timeout for test
				c.MaxIdleRetries = 0
				c.MaxGenerationTime = config.Duration(10 * time.Second)
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := bodyWithPrompt("mock-model", true, "mock-hang please")
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// The handler should detect idle timeout
			// With MaxIdleRetries=0, it should fail after the first idle timeout

			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if reqs[0].Status != "failed" {
				t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
			}

			// Should have received some partial content (tokens before hang)
			respBody := rr.Body.String()
			if !strings.Contains(respBody, "Hello") {
				t.Error("expected partial content before hang")
			}
		},
	})
}

func TestMockLLM_FallbackAfter500(t *testing.T) {
	runBothVersions(t, testCase{
		name: "MockLLM_FallbackAfter500",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "primary-mock", Name: "Primary", Enabled: true, FallbackChain: []string{"fallback-mock"}})
			mc.AddModel(models.ModelConfig{ID: "fallback-mock", Name: "Fallback", Enabled: true})
			return mc
		}(),
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				reqBodyBytes, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(reqBodyBytes, &reqBody)
				model, _ := reqBody["model"].(string)

				if model == "primary-mock" {
					// Primary always fails
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"primary down"}`)
					return
				}

				// re-set the body for the mock handler to read
				r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
				mockLLMHandler(t)(w, r)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("primary-mock", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in fallback stream response")
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

func TestMockLLM_HeadersForwarded(t *testing.T) {
	runBothVersions(t, testCase{
		name: "MockLLM_HeadersForwarded",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				// Verify Authorization header was forwarded
				auth := r.Header.Get("Authorization")
				if auth != "Bearer mock-test-token" {
					t.Errorf("expected Authorization 'Bearer mock-test-token', got '%s'", auth)
				}

				// Run the mock handler
				mockLLMHandler(t)(w, r)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", false)
			req := makeRequest(t, body)
			req.Header.Set("Authorization", "Bearer mock-test-token")
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Verify SSE headers are NOT set (non-streaming)
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected Content-Type 'application/json', got '%s'", ct)
			}
		},
	})
}

func TestMockLLM_MultipleMessages(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_MultipleMessages",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := map[string]interface{}{
				"model":  "mock-model",
				"stream": false,
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "You are a helpful assistant"},
					map[string]interface{}{"role": "user", "content": "What is 2+2?"},
					map[string]interface{}{"role": "assistant", "content": "4"},
					map[string]interface{}{"role": "user", "content": "And 3+3?"},
				},
			}
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Verify all messages are stored
			reqs := h.store.List()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request in store, got %d", len(reqs))
			}
			if len(reqs[0].Messages) != 4 {
				t.Errorf("expected 4 messages stored, got %d", len(reqs[0].Messages))
			}
			if reqs[0].Messages[0].Role != "system" {
				t.Errorf("expected first message role 'system', got '%s'", reqs[0].Messages[0].Role)
			}
			if reqs[0].Messages[3].Content != "And 3+3?" {
				t.Errorf("expected last message 'And 3+3?', got '%s'", reqs[0].Messages[3].Content)
			}
		},
	})
}

func TestMockLLM_StreamSSEFormat(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "MockLLM_StreamSSEFormat",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Verify the output has proper SSE format
			lines := strings.Split(rr.Body.String(), "\n")
			dataLineCount := 0
			doneFound := false
			for _, line := range lines {
				if strings.HasPrefix(line, "data: ") {
					dataLineCount++
					if strings.Contains(line, "[DONE]") {
						doneFound = true
					}
				}
			}

			if dataLineCount < 2 {
				t.Errorf("expected at least 2 data lines (chunks + DONE), got %d", dataLineCount)
			}
			if !doneFound {
				t.Error("expected [DONE] data line in SSE output")
			}
		},
	})
}

// ═══════════════════════════════════════════════════════════════════════════════
// Edge case tests using simple upstream
// ═══════════════════════════════════════════════════════════════════════════════

func TestUpstream4xxNoPassthrough_RetriesExhausted(t *testing.T) {
	// 4xx errors should NOT pass through - they trigger retry/fallback instead.
	// When all retries are exhausted, the proxy returns 502 Bad Gateway.
	runBothVersions(t, testCase{
		name: "Upstream4xxNoPassthrough_RetriesExhausted",
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

			// 4xx errors now trigger retry, not passthrough
			// When retries are exhausted, proxy returns 502 Bad Gateway
			if rr.Code != http.StatusBadGateway {
				t.Errorf("expected status %d (Bad Gateway), got %d", http.StatusBadGateway, rr.Code)
			}

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

func TestStreamUnexpectedEOF(t *testing.T) {
	runBothVersions(t, testCase{
		name: "StreamUnexpectedEOF",
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("partial"))
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
			if reqs[0].Status != "failed" {
				t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
			}
		},
	})
}

func TestEmptyMessages(t *testing.T) {
	runBothVersions(t, testCase{
		name:       "EmptyMessages",
		upstreamFn: func(t *testing.T) http.HandlerFunc { return mockLLMHandler(t) },
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
				bodyBytes, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(bodyBytes, &reqBody)
				model := reqBody["model"].(string)

				if model == "primary" {
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":"not found"}`)
					return
				}

				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				mockLLMHandler(t)(w, r)
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

// ═══════════════════════════════════════════════════════════════════════════════
// Loop detection integration tests
// ═══════════════════════════════════════════════════════════════════════════════

// mockLoopExactHandler returns a handler that always sends the exact same response.
// When called multiple times (via retries), the proxy's loop detector should flag
// identical messages in its sliding window.
func mockLoopExactHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqBodyBytes, _ := io.ReadAll(r.Body)
		r.Body.Close()

		var reqBody map[string]interface{}
		json.Unmarshal(reqBodyBytes, &reqBody)

		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, mockNonStreamResponse("Hello world! I am a useful token stream."))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Always send the SAME exact response — this simulates an LLM stuck in a loop.
		// The entire chunk is sent as one large piece so token count is sufficient.
		loopMsg := "Let me check the configuration file and read the database settings for the connection strings and timeout values again"
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(loopMsg))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// mockLoopSimilarHandler returns a handler that sends slightly different responses each time.
func mockLoopSimilarHandler() http.HandlerFunc {
	callCount := 0
	variations := []string{
		"Let me check the configuration file and read the database settings for the connection strings and timeout values",
		"Let me check the configuration file and read the database settings for the connection values and timeout limits",
		"Let me check the configuration file and read the database settings for the connection setup and timeout params",
	}

	return func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()

		callCount++
		msg := variations[(callCount-1)%len(variations)]

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(msg))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func TestLoopDetection_ExactMatch(t *testing.T) {
	// This test only works with the refactored handler (loop detection is only there)
	h, upstream := newTestHandler(t, mockLoopExactHandler(), nil)
	defer upstream.Close()

	// Subscribe to events to capture loop_detected
	eventCh := h.bus.Subscribe()
	defer h.bus.Unsubscribe(eventCh)

	// Send 3 identical requests — each gets the same response from mock.
	// Within each stream, only 1 message is added to the detector window.
	// After 2+ identical messages in the window, exact match should trigger.
	for i := 0; i < 3; i++ {
		body := simpleBody("mock-model", true)
		req := makeRequest(t, body)
		rr := httptest.NewRecorder()
		h.HandleChatCompletions(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected status 200, got %d", i+1, rr.Code)
		}
	}

	// All 3 requests should complete successfully (shadow mode — no interruption)
	reqs := h.store.List()
	if len(reqs) != 3 {
		t.Fatalf("expected 3 requests in store, got %d", len(reqs))
	}
	for i, req := range reqs {
		if req.Status != "completed" {
			t.Errorf("request %d: expected status 'completed', got '%s'", i+1, req.Status)
		}
	}
}

func TestLoopDetection_NormalNoTrigger(t *testing.T) {
	// Normal varied responses should NOT trigger loop detection
	h, upstream := newTestHandler(t, mockLLMHandler(t), nil)
	defer upstream.Close()

	eventCh := h.bus.Subscribe()
	defer h.bus.Unsubscribe(eventCh)

	// Send different types of requests — each is a separate request with its own detector
	prompts := []string{"Hello there", "mock-think please", "mock-tool call"}
	for _, prompt := range prompts {
		body := bodyWithPrompt("mock-model", true, prompt)
		req := makeRequest(t, body)
		rr := httptest.NewRecorder()
		h.HandleChatCompletions(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}
	}

	// Drain events — should NOT find any loop_detected events
	drainTimeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-eventCh:
			if evt.Type == "loop_detected" {
				t.Errorf("unexpected loop_detected event for normal varied responses: %+v", evt.Data)
			}
		case <-drainTimeout:
			return // Done draining, no loop detected — correct
		}
	}
}

func TestLoopDetection_ToolCallExtraction(t *testing.T) {
	// Test that tool calls in SSE chunks are extracted and fed to the detector
	toolCallHandler := func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Send some content
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Let me read that file for you to check the settings and configuration. "))
		flusher.Flush()

		// Send a tool call chunk
		toolChunk := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"delta": map[string]interface{}{
						"tool_calls": []interface{}{
							map[string]interface{}{
								"index": 0,
								"id":    "call_abc",
								"type":  "function",
								"function": map[string]interface{}{
									"name":      "read_file",
									"arguments": `{"path": "config.go"}`,
								},
							},
						},
					},
				},
			},
		}
		b, _ := json.Marshal(toolChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	h, upstream := newTestHandler(t, toolCallHandler, nil)
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()
	h.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify request completed (tool call extraction shouldn't break anything)
	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	if reqs[0].Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Stream error chunk detection tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestIsStreamErrorChunk(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // non-empty means error should be detected
	}{
		{
			name:     "valid SSE data line",
			input:    `data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			expected: "",
		},
		{
			name:     "valid SSE done",
			input:    `data: [DONE]`,
			expected: "",
		},
		{
			name:     "LiteLLM error object",
			input:    `{"error":{"message":"litellm.APIError: Error building chunks","type":"APIError"}}`,
			expected: "litellm.APIError: Error building chunks",
		},
		{
			name:     "simple error string",
			input:    `{"error":"Internal Server Error"}`,
			expected: "Internal Server Error",
		},
		{
			name:     "detail error format",
			input:    `{"detail":"Service unavailable"}`,
			expected: "Service unavailable",
		},
		{
			name:     "plain text (not JSON)",
			input:    `Some plain text`,
			expected: "",
		},
		{
			name:     "empty string",
			input:    ``,
			expected: "",
		},
		{
			name:     "JSON without error",
			input:    `{"status":"ok","data":"something"}`,
			expected: "",
		},
		// Plain text error patterns (new detection)
		{
			name:     "plain text litellm error",
			input:    `litellm.APIError: Error building chunks for logging/streaming usage calculation`,
			expected: "litellm.APIError",
		},
		{
			name:     "plain text Error: prefix",
			input:    `Error: something went wrong`,
			expected: "Error:",
		},
		{
			name:     "plain text exception",
			input:    `Exception: runtime panic occurred`,
			expected: "Exception:",
		},
		{
			name:     "Internal Server Error text",
			input:    `Internal Server Error - upstream crashed`,
			expected: "Internal Server Error",
		},
		{
			name:     "error wrapped in SSE data",
			input:    `data: {"error":{"message":"stream failed"}}`,
			expected: "stream failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isStreamErrorChunk([]byte(tc.input))
			if tc.expected == "" {
				if result != "" {
					t.Errorf("expected no error detected, got: %s", result)
				}
			} else {
				if result == "" {
					t.Errorf("expected error '%s' to be detected, but got empty string", tc.expected)
				} else if !strings.Contains(result, tc.expected) {
					t.Errorf("expected result to contain '%s', got: %s", tc.expected, result)
				}
			}
		})
	}
}

func TestStreamErrorChunkDetectionInStream(t *testing.T) {
	// Simulate a stream that starts successfully, then crashes and dumps an error JSON
	callCount := 0
	runBothVersions(t, testCase{
		name: "StreamErrorChunkDetection",
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.MaxUpstreamErrorRetries = 2
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				callCount++
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(http.StatusOK)
				flusher := w.(http.Flusher)

				if callCount == 1 {
					// First call: send some tokens, then dump error JSON (no [DONE])
					fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Hello"))
					flusher.Flush()
					// Simulate LiteLLM crash - dump raw error instead of proper SSE
					fmt.Fprintf(w, `{"error":{"message":"litellm.APIError: Error building chunks for logging/streaming usage calculation","type":"APIError"}}`)
					flusher.Flush()
					// Connection closes - no [DONE]
					return
				}

				// Retry: success
				for _, token := range []string{"Hello", " world", "!"} {
					fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(token))
					flusher.Flush()
				}
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// Should succeed after retry
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Should contain [DONE] from successful retry
			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in stream response after retry")
			}

			// Should NOT contain the error JSON (it should have been intercepted)
			if strings.Contains(rr.Body.String(), "litellm.APIError") {
				t.Error("error JSON should not have been passed to client")
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

// ═══════════════════════════════════════════════════════════════════════════════
// Fallback with headersSent tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestFallbackAfterStreamErrorWithHeadersSent(t *testing.T) {
	// Test the scenario from stream_error_fallback_issue.md:
	// 1. First attempt starts stream (headers sent)
	// 2. Stream fails mid-way, triggers retry
	// 3. Retry gets 500 error
	// 4. Should fallback to next model (not give up)
	callCount := 0
	runBothVersions(t, testCase{
		name: "FallbackAfterStreamErrorWithHeadersSent",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "primary-mock", Name: "Primary", Enabled: true, FallbackChain: []string{"fallback-mock"}})
			mc.AddModel(models.ModelConfig{ID: "fallback-mock", Name: "Fallback", Enabled: true})
			return mc
		}(),
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.MaxUpstreamErrorRetries = 1
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				reqBodyBytes, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(reqBodyBytes, &reqBody)
				model, _ := reqBody["model"].(string)

				callCount++

				if model == "primary-mock" {
					if callCount <= 2 {
						// First two calls to primary: start stream, send some tokens, then fail
						w.Header().Set("Content-Type", "text/event-stream")
						w.Header().Set("Cache-Control", "no-cache")
						w.WriteHeader(http.StatusOK)
						flusher := w.(http.Flusher)
						fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Partial"))
						flusher.Flush()
						// Connection closes without [DONE]
						return
					}
					// Third call (retry after stream error): return 500
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"primary still down"}`)
					return
				}

				// Fallback model: success
				r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
				mockLLMHandler(t)(w, r)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("primary-mock", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// Should succeed with fallback
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Should contain [DONE] from fallback
			if !strings.Contains(rr.Body.String(), "[DONE]") {
				t.Error("expected [DONE] in fallback stream response")
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

func TestFallbackDuringStreamRetry500Error(t *testing.T) {
	// Test that when headersSent is true and retry returns 500, fallback is triggered
	// Scenario:
	// 1. Primary model: start stream (headers sent), then close without [DONE]
	// 2. Retry on primary: return 500 (simulating broken upstream)
	// 3. Should fallback to model-b
	var lastModel string
	callCount := 0
	runBothVersions(t, testCase{
		name: "FallbackDuringStreamRetry500Error",
		modelsConfig: func() *models.ModelsConfig {
			mc := models.NewModelsConfig()
			mc.AddModel(models.ModelConfig{ID: "model-a", Name: "Model A", Enabled: true, FallbackChain: []string{"model-b"}})
			mc.AddModel(models.ModelConfig{ID: "model-b", Name: "Model B", Enabled: true})
			return mc
		}(),
		configOpts: []func(*config.Config){
			func(c *config.Config) {
				c.MaxUpstreamErrorRetries = 2 // Allow retry
			},
		},
		upstreamFn: func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				reqBodyBytes, _ := io.ReadAll(r.Body)
				r.Body.Close()

				var reqBody map[string]interface{}
				json.Unmarshal(reqBodyBytes, &reqBody)
				model, _ := reqBody["model"].(string)
				lastModel = model
				callCount++

				if model == "model-a" {
					if callCount == 1 {
						// First call: start stream then close without [DONE]
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(http.StatusOK)
						flusher := w.(http.Flusher)
						fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Start"))
						flusher.Flush()
						return // No [DONE] - triggers retry
					}
					// Retry attempt: return 500 (simulating broken upstream after stream started)
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"error":"upstream broken"}`)
					return
				}

				// Fallback (model-b): success
				r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
				mockLLMHandler(t)(w, r)
			}
		},
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("model-a", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			// Should succeed with fallback
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			// Verify fallback was used
			if lastModel != "model-b" {
				t.Errorf("expected fallback model 'model-b' to be used, last model was '%s'", lastModel)
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

// TestPrepareRetryNoMessageDuplication verifies that multiple retries don't cause
// message duplication. The messages array should be rebuilt from originalMessages
// on each retry, not mutated in-place.
func TestPrepareRetryNoMessageDuplication(t *testing.T) {
	callCount := 0
	var lastMessages []interface{}

	// Test the current implementation
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		reqBodyBytes, _ := io.ReadAll(r.Body)
		r.Body.Close()

		var reqBody map[string]interface{}
		json.Unmarshal(reqBodyBytes, &reqBody)

		callCount++

		// Capture the messages sent to upstream
		lastMessages, _ = reqBody["messages"].([]interface{})

		if callCount < 3 {
			// First two calls: start stream then close without [DONE]
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(fmt.Sprintf("Chunk%d", callCount)))
			flusher.Flush()
			return // No [DONE] - triggers retry
		}

		// Third call: success
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Final"))
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, nil, func(c *config.Config) {
		c.MaxUpstreamErrorRetries = 3 // Allow multiple retries
	})
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()
	h.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify message count - should be original (1) + assistant (1) + user (1) = 3
	// NOT duplicated with each retry
	if len(lastMessages) != 3 {
		t.Errorf("expected 3 messages (original + assistant + user prompt), got %d", len(lastMessages))
		for i, msg := range lastMessages {
			t.Logf("Message %d: %+v", i, msg)
		}
		return
	}

	// Verify the structure
	// Message 0: original user message
	if msg, ok := lastMessages[0].(map[string]interface{}); ok {
		if msg["role"] != "user" {
			t.Errorf("expected message 0 role 'user', got '%v'", msg["role"])
		}
	} else {
		t.Error("message 0 is not a map")
	}

	// Message 1: accumulated assistant response
	if msg, ok := lastMessages[1].(map[string]interface{}); ok {
		if msg["role"] != "assistant" {
			t.Errorf("expected message 1 role 'assistant', got '%v'", msg["role"])
		}
		// Should contain accumulated content from previous attempts
		content, _ := msg["content"].(string)
		if content == "" {
			t.Error("expected message 1 to have content")
		}
	} else {
		t.Error("message 1 is not a map")
	}

	// Message 2: continue prompt
	if msg, ok := lastMessages[2].(map[string]interface{}); ok {
		if msg["role"] != "user" {
			t.Errorf("expected message 2 role 'user', got '%v'", msg["role"])
		}
		if !strings.Contains(fmt.Sprintf("%v", msg["content"]), "interrupted") {
			t.Error("expected message 2 to contain 'interrupted'")
		}
	} else {
		t.Error("message 2 is not a map")
	}
}
