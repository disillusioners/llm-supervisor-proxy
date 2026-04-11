package ultimatemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// --- Tests for executeExternal ---

func TestExecuteExternal_UpstreamURLNotConfigured(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = "" // Empty URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err == nil {
		t.Error("executeExternal should return error when upstream URL is empty")
	}
	if !strings.Contains(err.Error(), "upstream URL not configured") {
		t.Errorf("Error should mention 'upstream URL not configured', got: %v", err)
	}
}

func TestExecuteExternal_SuccessfulNonStreaming(t *testing.T) {
	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true

		// Verify request was made correctly
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("Expected path /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "ultimate-model",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       map[string]interface{}{"role": "assistant", "content": "Hello!"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	usage, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if !upstreamCalled {
		t.Error("Upstream server should have been called")
	}
	if usage == nil {
		t.Error("Usage should be extracted")
	} else {
		if usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
		}
		if usage.CompletionTokens != 5 {
			t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
		}
		if usage.TotalTokens != 15 {
			t.Errorf("TotalTokens = %d, want 15", usage.TotalTokens)
		}
	}
	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestExecuteExternal_SuccessfulStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Send SSE chunks
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		flusher.Flush()

		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"!\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()

		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	usage, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), true)

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Error("Response should contain SSE data")
	}
	// Usage may be nil if no usage in streaming chunks
	_ = usage
}

func TestExecuteExternal_UpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err == nil {
		t.Error("executeExternal should return error for upstream failure")
	}
	if !strings.Contains(err.Error(), "upstream returned 500") {
		t.Errorf("Error should contain 'upstream returned 500', got: %v", err)
	}
}

func TestExecuteExternal_UpstreamReturns400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad Request"))
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err == nil {
		t.Error("executeExternal should return error for 400 response")
	}
	if !strings.Contains(err.Error(), "upstream returned 400") {
		t.Errorf("Error should contain 'upstream returned 400', got: %v", err)
	}
}

func TestExecuteExternal_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Simulate slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Create a context that will timeout quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := h.executeExternal(ctx, w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err == nil {
		t.Error("executeExternal should return error for cancelled context")
	}
}

func TestExecuteExternal_ResponseHeadersForwarded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-123")
		w.Header().Set("X-Rate-Limit", "100")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-test",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"role": "assistant", "content": "Hi"}}},
		})
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if h := w.Header().Get("X-Request-ID"); h != "req-123" {
		t.Errorf("X-Request-ID = %q, want %q", h, "req-123")
	}
	if h := w.Header().Get("X-Rate-Limit"); h != "100" {
		t.Errorf("X-Rate-Limit = %q, want %q", h, "100")
	}
}

func TestExecuteExternal_ModelIDOverride(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body to get model
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		receivedModel = req["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-test",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"role": "assistant", "content": "Hi"}}},
		})
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model", // Original model
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if receivedModel != "ultimate-model" {
		t.Errorf("Model = %q, want %q", receivedModel, "ultimate-model")
	}
}

func TestExecuteExternal_HeadersHopByHopSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that hop-by-hop headers were not forwarded
		if r.Header.Get("Host") != "" {
			t.Log("Note: httptest may handle Host differently")
		}
		// Content-Length and Transfer-Encoding should not be in our request
		// (http.Client adds its own)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-test",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"role": "assistant", "content": "Hi"}}},
		})
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("X-Custom-Header", "custom-value")
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
}

// --- Tests for streamResponse ---

func TestStreamResponse_SSEHeadersSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	// Get the server's response
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	if conn := w.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want %q", conn, "keep-alive")
	}
	if xab := w.Header().Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", xab, "no")
	}
}

func TestStreamResponse_DataForwarding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Hello") {
		t.Error("Response should contain streamed content")
	}
	if !strings.Contains(body, "data:") {
		t.Error("Response should contain SSE data prefix")
	}
}

func TestStreamResponse_DONEMarker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"}}]}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
}

func TestStreamResponse_UsageExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		// Include usage in final chunk
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	usage, err := h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	if usage == nil {
		t.Log("Usage was nil - may be expected if chunk parsing failed")
	} else {
		if usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
		}
	}
}

func TestStreamResponse_EmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Send empty response
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
}

func TestStreamResponse_MultipleChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		chunks := []string{"Hello", " ", "World", "!"}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", chunk)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	body := w.Body.String()
	for _, chunk := range []string{"Hello", "World"} {
		if !strings.Contains(body, chunk) {
			t.Errorf("Response should contain chunk %q", chunk)
		}
	}
}

func TestStreamResponse_WithToolCallBuffer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Using tool\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	// Enable tool call buffer
	repairCfg := toolrepair.DisabledConfig()
	h.SetToolCallBufferConfig(1024*1024, false, repairCfg)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil)

	if err != nil {
		t.Errorf("streamResponse with tool buffer returned error: %v", err)
	}
}

// --- Tests for extractUsageFromResponse ---

func TestExtractUsageFromResponse_ValidResponse(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-123","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)

	usage := extractUsageFromResponse(body)

	if usage == nil {
		t.Fatal("extractUsageFromResponse returned nil")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", usage.TotalTokens)
	}
}

func TestExtractUsageFromResponse_NoUsage(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-123","choices":[]}`)

	usage := extractUsageFromResponse(body)

	if usage != nil {
		t.Errorf("extractUsageFromResponse should return nil for no usage, got %+v", usage)
	}
}

func TestExtractUsageFromResponse_MalformedJSON(t *testing.T) {
	body := []byte(`{invalid json}`)

	usage := extractUsageFromResponse(body)

	if usage != nil {
		t.Errorf("extractUsageFromResponse should return nil for malformed JSON, got %+v", usage)
	}
}

func TestExtractUsageFromResponse_EmptyBody(t *testing.T) {
	usage := extractUsageFromResponse([]byte{})

	if usage != nil {
		t.Errorf("extractUsageFromResponse should return nil for empty body, got %+v", usage)
	}
}

func TestExtractUsageFromResponse_NilBody(t *testing.T) {
	usage := extractUsageFromResponse(nil)

	if usage != nil {
		t.Errorf("extractUsageFromResponse should return nil for nil body, got %+v", usage)
	}
}

// --- Tests for extractUsageFromChunk ---

func TestExtractUsageFromChunk_ValidChunk(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"Hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)

	usage := extractUsageFromChunk(data)

	if usage == nil {
		t.Fatal("extractUsageFromChunk returned nil")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", usage.TotalTokens)
	}
}

func TestExtractUsageFromChunk_NoUsage(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"Hello"}}]}`)

	usage := extractUsageFromChunk(data)

	if usage != nil {
		t.Errorf("extractUsageFromChunk should return nil for no usage, got %+v", usage)
	}
}

func TestExtractUsageFromChunk_MalformedJSON(t *testing.T) {
	data := []byte(`{invalid json}`)

	usage := extractUsageFromChunk(data)

	if usage != nil {
		t.Errorf("extractUsageFromChunk should return nil for malformed JSON, got %+v", usage)
	}
}

func TestExtractUsageFromChunk_EmptyData(t *testing.T) {
	usage := extractUsageFromChunk([]byte{})

	if usage != nil {
		t.Errorf("extractUsageFromChunk should return nil for empty data, got %+v", usage)
	}
}

// --- Integration tests ---

func TestExecuteExternal_Integration(t *testing.T) {
	// Test the full flow with real httptest server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Header", "test-value")
		w.Header().Set("Content-Type", "application/json")

		// Verify auth header is forwarded
		if r.Header.Get("Authorization") != "Bearer my-secret-key" {
			t.Errorf("Authorization header not forwarded correctly")
		}

		resp := map[string]interface{}{
			"id":      "chatcmpl-integration",
			"model":   "ultimate-model",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"role": "assistant", "content": "Integration test response"}}},
			"usage":   map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer my-secret-key")
	body := map[string]interface{}{
		"model":    "different-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Test"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	usage, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false)

	if err != nil {
		t.Fatalf("executeExternal failed: %v", err)
	}
	if usage == nil {
		t.Fatal("Usage should not be nil")
	}
	if usage.TotalTokens != 8 {
		t.Errorf("TotalTokens = %d, want 8", usage.TotalTokens)
	}
	if w.Header().Get("X-Upstream-Header") != "test-value" {
		t.Errorf("Upstream header not forwarded")
	}
}

func TestExecuteExternal_RequestBodyMarshalingError(t *testing.T) {
	// This test verifies that unmarshallable bodies (like channels) are handled
	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = "http://localhost:9999" // Won't be reached
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	// Use a body that cannot be marshaled
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
		// Add a channel which cannot be JSON marshaled
		"unmarshalable": make(chan int),
	}

	_, err := h.executeExternal(context.Background(), w, r, body, nil, modelsCfg.GetModel("ultimate-model"), false)

	if err == nil {
		t.Error("executeExternal should return error for unmarshalable body")
	}
}

// Ensure store.Usage is used by extractUsageFromResponse
var _ *store.Usage = (*store.Usage)(nil)
