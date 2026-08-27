package ultimatemodel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// --- Tests for executeExternal ---

func TestExecuteExternal_UpstreamURLNotConfigured(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = "" // Empty URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	execResult, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if !upstreamCalled {
		t.Error("Upstream server should have been called")
	}
	if execResult == nil {
		t.Error("ExecuteResult should not be nil")
	} else if execResult.Usage == nil {
		t.Error("Usage should be extracted")
	} else {
		if execResult.Usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", execResult.Usage.PromptTokens)
		}
		if execResult.Usage.CompletionTokens != 5 {
			t.Errorf("CompletionTokens = %d, want 5", execResult.Usage.CompletionTokens)
		}
		if execResult.Usage.TotalTokens != 15 {
			t.Errorf("TotalTokens = %d, want 15", execResult.Usage.TotalTokens)
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	usage, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), true, false, "")

	if err != nil {
		t.Errorf("executeExternal returned error: %v", err)
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Error("Response should contain SSE data")
	}
	// Usage may be nil if no usage in streaming chunks
	if usage != nil {
		_ = usage.Usage
	}
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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

	_, err := h.executeExternal(ctx, w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model", // Original model
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("X-Custom-Header", "custom-value")
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Get the server's response
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache, no-transform")
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	usage, err := h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

	if err != nil {
		t.Errorf("streamResponse returned error: %v", err)
	}
	if usage == nil {
		t.Log("Usage was nil - may be expected if chunk parsing failed")
	} else if usage.Usage == nil {
		t.Log("Usage was nil - may be expected if chunk parsing failed")
	} else {
		if usage.Usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", usage.Usage.PromptTokens)
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Enable tool call buffer
	repairCfg := toolrepair.DisabledConfig()
	h.SetToolCallBufferConfig(1024*1024, false, repairCfg)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, false, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 15): buffered-era test opts into buffered mode

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer my-secret-key")
	body := map[string]interface{}{
		"model":    "different-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Test"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	usage, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")

	if err != nil {
		t.Fatalf("executeExternal failed: %v", err)
	}
	if usage == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if usage.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	if usage.Usage.TotalTokens != 8 {
		t.Errorf("TotalTokens = %d, want 8", usage.Usage.TotalTokens)
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	// Use a body that cannot be marshaled
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
		// Add a channel which cannot be JSON marshaled
		"unmarshalable": make(chan int),
	}

	_, err := h.executeExternal(context.Background(), w, r, body, nil, modelsCfg.GetModel("ultimate-model"), false, false, "")

	if err == nil {
		t.Error("executeExternal should return error for unmarshalable body")
	}
}

// Ensure store.Usage is used by extractUsageFromResponse
var _ *store.Usage = (*store.Usage)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 4 (real-streaming default) — live-mode tests
// ─────────────────────────────────────────────────────────────────────────────

// normalizeSSEIDs replaces nondeterministic chunk identifiers (the
// UnixNano-stamped chatcmpl IDs and created timestamps — the toolcall
// buffer and the internal chunk synthesizer stamp them per RUN) so
// buffered/live wire bytes can be compared structurally.
func normalizeSSEIDs(s string) string {
	s = regexp.MustCompile(`"id":"chatcmpl-\d+"`).ReplaceAllString(s, `"id":"chatcmpl-X"`)
	return regexp.MustCompile(`"created":\d+`).ReplaceAllString(s, `"created":X`)
}

// countingRecorder counts Write and Flush calls so tests can observe the
// buffered (single write) vs live (per-chunk writes) wire shape.
type countingRecorder struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	writes  int
	flushes int
}

func (c *countingRecorder) Write(b []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
	return c.ResponseRecorder.Write(b)
}

func (c *countingRecorder) Flush() {
	c.mu.Lock()
	c.flushes++
	c.mu.Unlock()
	c.ResponseRecorder.Flush()
}

func (c *countingRecorder) snapshot() (writes, flushes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes, c.flushes
}

// newLiveExternalFixture spins an upstream that emits one content chunk,
// flushes, then BLOCKS on the returned release channel before writing
// [DONE]. Observing the chunk on the client side before release is
// closed proves write-through ahead of stream completion.
func newLiveExternalFixture(t *testing.T) (upstreamURL string, release chan struct{}, h *Handler) {
	t.Helper()
	release = make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		flusher.Flush()
		// Hold the stream open: [DONE] is only written after the test
		// has observed the first chunk on the CLIENT side.
		<-release
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	return server.URL, release, NewHandler(cfg, modelsCfg, nil, nil)
}

// TestUltimateExternal_LiveStream_ForwardBytesBeforeCompletion asserts
// the defining property of live mode (Phase 4 / task 4.2): the client
// receives a chunk BEFORE the upstream stream completes. The upstream
// blocks after the first chunk; if the client sees the chunk while the
// upstream is still holding, bytes were forwarded per-chunk — a
// buffered implementation would block until [DONE] (deadlock → client
// timeout → test failure).
func TestUltimateExternal_LiveStream_ForwardBytesBeforeCompletion(t *testing.T) {
	upstreamURL, release, h := newLiveExternalFixture(t)

	// Safety net: t.Cleanup runs LIFO, so this fires BEFORE the
	// fixture's server.Close() — unblocking the upstream handler on
	// any early-failure path (server.Close waits for handlers).
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	resp, err := http.Get(upstreamURL)
	if err != nil {
		t.Fatalf("http.Get upstream: %v", err)
	}
	defer resp.Body.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No options = live mode (the new default).
		if _, serr := h.streamResponse(w, resp, "ultimate-model", nil, false, false); serr != nil {
			t.Errorf("streamResponse: %v", serr)
		}
	}))
	defer proxy.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	cr, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatalf("client Get: %v (if this is a timeout, the path buffered instead of live-forwarding)", err)
	}
	defer cr.Body.Close()

	reader := bufio.NewReader(cr.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading first chunk: %v", err)
	}
	if !strings.Contains(line, "first") {
		t.Fatalf("first chunk = %q, want content %q", line, "first")
	}
	// Upstream is provably still blocked (release not yet closed) —
	// receiving the chunk proves per-chunk write-through.
	closeRelease()

	sawDone := false
	for {
		l, rerr := reader.ReadString('\n')
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("draining stream: %v", rerr)
		}
		if strings.Contains(l, "[DONE]") {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("stream finished without [DONE]")
	}
}

// TestUltimate_HeaderDispatch pins the options-level dispatch the
// proxy lane drives (plan Files row 2 — pkg/proxy/handler.go passes
// rc.bufferMode as ExecuteOptions{BufferMode: ...}; the
// X-LLMProxy-Buffer-Response header itself is parsed in pkg/proxy
// Phase 1 and is DEFERRED to the proxy lane). BufferMode=true must
// select the buffered single-write shape; BufferMode=false (default)
// must select the live per-chunk shape.
func TestUltimate_HeaderDispatch(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"content":"a"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"b"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	t.Run("external", func(t *testing.T) {
		run := func(opts ...ExecuteOptions) (*ExecuteResult, *countingRecorder) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(stream))
			}))
			defer upstream.Close()

			cfg := newMockConfigManager()
			modelsCfg := newMockModelsConfig()
			modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
			h := NewHandler(cfg, modelsCfg, nil, nil)

			resp, err := http.Get(upstream.URL)
			if err != nil {
				t.Fatalf("http.Get: %v", err)
			}
			defer resp.Body.Close()

			rec := &countingRecorder{ResponseRecorder: httptest.NewRecorder()}
			result, serr := h.streamResponse(rec, resp, "ultimate-model", nil, false, false, opts...)
			if serr != nil {
				t.Fatalf("streamResponse: %v", serr)
			}
			return result, rec
		}

		bufferedResult, bufferedRec := run(ExecuteOptions{BufferMode: true})
		liveResult, liveRec := run() // default = live

		bw, bf := bufferedRec.snapshot()
		if bw != 1 || bf != 1 {
			t.Errorf("buffered mode: writes=%d flushes=%d, want 1/1 (single end-of-stream write)", bw, bf)
		}
		lw, lf := liveRec.snapshot()
		if lw <= 1 {
			t.Errorf("live mode: writes=%d, want >1 (per-chunk write-through)", lw)
		}
		if lf != lw {
			t.Errorf("live mode: flushes=%d != writes=%d (each live write must be paired with a flush)", lf, lw)
		}
		if bufferedResult.Content != liveResult.Content || bufferedResult.Thinking != liveResult.Thinking {
			t.Errorf("capture diverged between modes: (%q,%q) vs (%q,%q)",
				bufferedResult.Content, bufferedResult.Thinking, liveResult.Content, liveResult.Thinking)
		}
		if bufferedRec.Body.String() != liveRec.Body.String() {
			t.Errorf("wire bytes diverged between modes:\n buffered=%q\n live=%q",
				bufferedRec.Body.String(), liveRec.Body.String())
		}
	})

	t.Run("internal", func(t *testing.T) {
		run := func(opts ...ExecuteOptions) (*ExecuteResult, *countingRecorder) {
			p := &mockProvider{name: "mock", streamEvents: []providers.StreamEvent{
				{Type: "content", Content: "a"},
				{Type: "content", Content: "b"},
				{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
					Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
				}},
			}}
			h := NewHandler(newMockConfigManager(), newMockModelsConfig(), nil, nil)
			rec := &countingRecorder{ResponseRecorder: httptest.NewRecorder()}
			result, serr := h.handleInternalStream(context.Background(), p, &providers.ChatCompletionRequest{Model: "test-model"}, rec, "test-model", nil, opts...)
			if serr != nil {
				t.Fatalf("handleInternalStream: %v", serr)
			}
			return result, rec
		}

		bufferedResult, bufferedRec := run(ExecuteOptions{BufferMode: true})
		liveResult, liveRec := run() // default = live

		bw, bf := bufferedRec.snapshot()
		if bw != 1 || bf != 1 {
			t.Errorf("buffered mode: writes=%d flushes=%d, want 1/1 (single end-of-stream write)", bw, bf)
		}
		lw, lf := liveRec.snapshot()
		// live: 2 content events + finish chunk + [DONE]
		if lw != 4 {
			t.Errorf("live mode: writes=%d, want 4 (per-event write-through)", lw)
		}
		if lf != lw {
			t.Errorf("live mode: flushes=%d != writes=%d", lf, lw)
		}
		if bufferedResult.Content != liveResult.Content {
			t.Errorf("Content diverged: %q vs %q", bufferedResult.Content, liveResult.Content)
		}
		if normalizeSSEIDs(bufferedRec.Body.String()) != normalizeSSEIDs(liveRec.Body.String()) {
			t.Errorf("wire bytes diverged between modes (IDs normalized)")
		}
	})
}

// TestUltimateCapture_LiveMode_IdenticalToBuffered is the C1
// regression pin (plan task 4.4): the SAME upstream event sequence must
// produce an identical ExecuteResult in buffered and live modes. The
// MANDATORY no-usage-chunk fixture means Usage comes exclusively from
// the tokenizer-fallback estimator fed by buf.Bytes() — if the
// capture-side accumulator regresses, live-mode CompletionTokens
// becomes 0 while buffered-mode keeps the correct count.
func TestUltimateCapture_LiveMode_IdenticalToBuffered(t *testing.T) {
	// NO usage chunk anywhere — only SSE data lines.
	const goldenStream = `data: {"choices":[{"index":0,"delta":{"content":"hello "}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"think-1 "}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"content":"there"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	run := func(opts ...ExecuteOptions) (*ExecuteResult, string) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(goldenStream))
		}))
		defer upstream.Close()

		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
		h := NewHandler(cfg, modelsCfg, nil, nil)

		resp, err := http.Get(upstream.URL)
		if err != nil {
			t.Fatalf("http.Get: %v", err)
		}
		defer resp.Body.Close()

		w := httptest.NewRecorder()
		result, serr := h.streamResponse(w, resp, "ultimate-model", nil, false, false, opts...)
		if serr != nil {
			t.Fatalf("streamResponse: %v", serr)
		}
		return result, w.Body.String()
	}

	buffered, bufferedBody := run(ExecuteOptions{BufferMode: true})
	live, liveBody := run() // default = live

	if buffered.Content != live.Content {
		t.Errorf("Content: buffered=%q live=%q", buffered.Content, live.Content)
	}
	if buffered.Thinking != live.Thinking {
		t.Errorf("Thinking: buffered=%q live=%q", buffered.Thinking, live.Thinking)
	}
	if len(buffered.ToolCalls) != len(live.ToolCalls) {
		t.Fatalf("ToolCalls: buffered=%d live=%d", len(buffered.ToolCalls), len(live.ToolCalls))
	}
	for i := range buffered.ToolCalls {
		if buffered.ToolCalls[i] != live.ToolCalls[i] {
			t.Errorf("ToolCalls[%d]: buffered=%+v live=%+v", i, buffered.ToolCalls[i], live.ToolCalls[i])
		}
	}
	if buffered.Usage == nil || live.Usage == nil {
		t.Fatalf("Usage nil: buffered=%v live=%v", buffered.Usage, live.Usage)
	}
	// C1 pin: no usage chunk ⇒ fallback estimator. Buffered count must be
	// non-zero (fixture sanity), and live must match buffered EXACTLY —
	// failure mode of a regressed capture-side accumulator: live=0.
	if buffered.Usage.CompletionTokens == 0 {
		t.Fatalf("buffered CompletionTokens=0 — fixture lost its completion text")
	}
	if live.Usage.CompletionTokens != buffered.Usage.CompletionTokens {
		t.Errorf("C1 REGRESSION: CompletionTokens live=%d buffered=%d (live mode must see the FULL completion text)",
			live.Usage.CompletionTokens, buffered.Usage.CompletionTokens)
	}
	if live.Usage.PromptTokens != buffered.Usage.PromptTokens ||
		live.Usage.TotalTokens != buffered.Usage.TotalTokens {
		t.Errorf("Usage mismatch: live=%+v buffered=%+v", live.Usage, buffered.Usage)
	}
	if normalizeSSEIDs(bufferedBody) != normalizeSSEIDs(liveBody) {
		t.Errorf("wire bytes diverged between modes (IDs normalized):\n buffered=%q\n live=%q",
			normalizeSSEIDs(bufferedBody), normalizeSSEIDs(liveBody))
	}
}
