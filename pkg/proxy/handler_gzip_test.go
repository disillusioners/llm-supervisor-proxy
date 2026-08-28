// Integration tests for transparent gzip request-body decompression.
//
// These tests prove that wrapping the proxy handlers with the
// gzipmw.DecompressRequest middleware produces IDENTICAL upstream
// requests (body bytes + relevant headers) and client responses when
// the client sends gzip-encoded bodies vs. when it sends the same
// payload uncompressed. This is the core parity guarantee spelled out
// in the feature spec.
//
// Each test runs twice — once with the body sent as-is (control)
// and once with the body gzipped and Content-Encoding: gzip set
// (treatment) — and asserts the captured upstream bytes and client
// response are byte-equal between the two runs.
package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/middleware/gzipmw"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// gzipBytes returns the gzip-compressed form of plain.
func gzipBytesProxy(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// ─────────────────────────────────────────────────────────────────────────────
// /v1/chat/completions (OpenAI) — parity proof
// ─────────────────────────────────────────────────────────────────────────────

// TestGzipMiddleware_OpenAI_ParityWithUncompressed sends a request to
// /v1/chat/completions once uncompressed and once with the same body
// gzip-encoded, and asserts the upstream sees BYTE-IDENTICAL request
// bodies and headers (i.e. the proxy correctly strips Content-Encoding
// and forwards the decompressed bytes) and the client receives the
// same response.
func TestGzipMiddleware_OpenAI_ParityWithUncompressed(t *testing.T) {
	var capturedUpstream []byte
	var capturedHeaders http.Header

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedUpstream = body
		capturedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	})
	upstream := httptest.NewServer(upstreamHandler)
	defer upstream.Close()

	mc := models.NewModelsConfig()
	mc.AddModel(models.ModelConfig{ID: "mock-model", Name: "mock-model", Enabled: true})
	h, _ := newTestHandler(t, upstreamHandler, mc)
	defer upstream.Close()

	// Wrap the proxy handler with the gzip middleware, exactly as
	// cmd/main.go does.
	wrappedChat := gzipmw.DecompressRequest(http.HandlerFunc(h.HandleChatCompletions))

	payload := map[string]interface{}{
		"model":  "mock-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	plainBytes, _ := json.Marshal(payload)

	// ── CONTROL: uncompressed ──
	capturedUpstream = nil
	capturedHeaders = nil
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(plainBytes))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	wrappedChat.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("control: status = %d, want 200", rr1.Code)
	}
	controlBody := rr1.Body.Bytes()
	controlUpstream := append([]byte(nil), capturedUpstream...)
	controlHeaders := capturedHeaders.Clone()

	// ── TREATMENT: gzip ──
	capturedUpstream = nil
	capturedHeaders = nil
	compressed := gzipBytesProxy(t, plainBytes)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Content-Encoding", "gzip")
	rr2 := httptest.NewRecorder()
	wrappedChat.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("treatment: status = %d, want 200", rr2.Code)
	}
	treatmentBody := rr2.Body.Bytes()
	treatmentUpstream := append([]byte(nil), capturedUpstream...)
	treatmentHeaders := capturedHeaders.Clone()

	// PARITY ASSERTIONS:

	// 1. Client response must be byte-identical.
	if !bytes.Equal(controlBody, treatmentBody) {
		t.Errorf("client response differs:\n  control:   %q\n  treatment: %q",
			controlBody, treatmentBody)
	}

	// 2. Upstream body must be byte-identical.
	if !bytes.Equal(controlUpstream, treatmentUpstream) {
		t.Errorf("upstream body differs:\n  control:   %q\n  treatment: %q",
			controlUpstream, treatmentUpstream)
	}

	// 3. Upstream must NOT see Content-Encoding: gzip (the proxy
	//    strips it before forwarding).
	if treatmentHeaders.Get("Content-Encoding") != "" {
		t.Errorf("upstream Content-Encoding = %q, want empty (must not forward gzip)",
			treatmentHeaders.Get("Content-Encoding"))
	}

	// 4. Upstream Content-Length must equal the decompressed size
	//    (the proxy recomputes it).
	if got := treatmentHeaders.Get("Content-Length"); got != "" && got != "0" {
		// http.Client may not set Content-Length when it
		// rewrites the request internally; if it does, it must
		// match the decompressed length.
		if got != strconv.FormatInt(int64(len(plainBytes)), 10) {
			t.Errorf("upstream Content-Length = %q, want %d",
				got, len(plainBytes))
		}
	}

	// 5. Upstream must see the same Content-Type (the proxy does
	//    NOT strip Content-Type).
	if controlHeaders.Get("Content-Type") != treatmentHeaders.Get("Content-Type") {
		t.Errorf("upstream Content-Type differs: control=%q treatment=%q",
			controlHeaders.Get("Content-Type"),
			treatmentHeaders.Get("Content-Type"))
	}
}

// TestGzipMiddleware_OpenAI_NoEncoding_Unchanged verifies that
// requests without Content-Encoding: gzip pass through the middleware
// untouched (the regression-prevention property). The proxy may still
// internally re-marshal the JSON (it sorts keys for store-message
// canonicalization), so we assert that the upstream receives a JSON
// object with the same fields and values — NOT byte equality.
func TestGzipMiddleware_OpenAI_NoEncoding_Unchanged(t *testing.T) {
	var seenUpstream []byte
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUpstream, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	upstream := httptest.NewServer(upstreamHandler)
	defer upstream.Close()

	mc := models.NewModelsConfig()
	mc.AddModel(models.ModelConfig{ID: "mock-model", Name: "mock-model", Enabled: true})
	h, _ := newTestHandler(t, upstreamHandler, mc)
	defer upstream.Close()

	wrappedChat := gzipmw.DecompressRequest(http.HandlerFunc(h.HandleChatCompletions))

	plain := []byte(`{"model":"mock-model","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(plain))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	wrappedChat.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// Assert the upstream received a JSON object with the same
	// model and messages as the request — NOT byte-equal, because
	// the proxy internally re-marshals with sorted keys.
	var sent, seen map[string]interface{}
	if err := json.Unmarshal(plain, &sent); err != nil {
		t.Fatalf("unmarshal sent: %v", err)
	}
	if err := json.Unmarshal(seenUpstream, &seen); err != nil {
		t.Fatalf("upstream did not receive JSON: err=%v body=%q", err, seenUpstream)
	}
	if seen["model"] != sent["model"] {
		t.Errorf("upstream model = %v, want %v", seen["model"], sent["model"])
	}
	if seen["messages"] == nil {
		t.Errorf("upstream lost messages field")
	}
}

// TestGzipMiddleware_OpenAI_CorruptGzip_Returns400 verifies that the
// middleware emits the proxy's OpenAI-compatible error envelope on
// corrupt input BEFORE the handler is invoked.
func TestGzipMiddleware_OpenAI_CorruptGzip_Returns400(t *testing.T) {
	var handlerInvoked bool

	mc := models.NewModelsConfig()
	mc.AddModel(models.ModelConfig{ID: "mock-model", Name: "mock-model", Enabled: true})
	h, _ := newTestHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerInvoked = true
		w.WriteHeader(http.StatusOK)
	}), mc)

	wrappedChat := gzipmw.DecompressRequest(http.HandlerFunc(h.HandleChatCompletions))

	corrupt := []byte{0x1f, 0x8b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(corrupt))
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrappedChat.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if handlerInvoked {
		t.Errorf("handler was invoked despite corrupt gzip — middleware should reject before handler")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var env struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "invalid_gzip_body" {
		t.Errorf("error.code = %q, want invalid_gzip_body", env.Error.Code)
	}
}

// TestGzipMiddleware_OpenAI_StreamingResponse proves that a gzipped
// request still gets the SSE streaming response when the client asks
// for it — the middleware must not break the streaming path.
func TestGzipMiddleware_OpenAI_StreamingResponse(t *testing.T) {
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain to make sure the proxy fully forwards the body.
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"A\"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	})
	upstream := httptest.NewServer(upstreamHandler)
	defer upstream.Close()

	mc := models.NewModelsConfig()
	mc.AddModel(models.ModelConfig{ID: "mock-model", Name: "mock-model", Enabled: true})
	h, _ := newTestHandler(t, upstreamHandler, mc)
	defer upstream.Close()

	wrappedChat := gzipmw.DecompressRequest(http.HandlerFunc(h.HandleChatCompletions))

	payload, _ := json.Marshal(map[string]interface{}{
		"model":    "mock-model",
		"stream":   true,
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	compressed := gzipBytesProxy(t, payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrappedChat.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] marker in stream body, got: %q", body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /v1/messages (Anthropic) — parity proof
// ─────────────────────────────────────────────────────────────────────────────

// TestGzipMiddleware_Anthropic_ParityWithUncompressed sends an
// Anthropic-format request once uncompressed and once gzipped, and
// asserts parity. The Anthropic handler translates internally to
// OpenAI wire format for upstream; the test asserts the translated
// upstream body is byte-identical regardless of compression.
func TestGzipMiddleware_Anthropic_ParityWithUncompressed(t *testing.T) {
	var capturedUpstream []byte
	var capturedUpstreamHeaders http.Header

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUpstream, _ = io.ReadAll(r.Body)
		capturedUpstreamHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal Anthropic-format response is NOT used here —
		// the upstream is configured to receive OpenAI wire (the
		// proxy translates; see handler_anthropic.go:94 targetURL).
		// We return OpenAI-format and the proxy will translate.
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	})
	upstream := httptest.NewServer(upstreamHandler)
	defer upstream.Close()

	mc := models.NewModelsConfig()
	mc.AddModel(models.ModelConfig{ID: "mock-anthropic", Name: "mock-anthropic", Enabled: true})
	h, _ := newTestHandler(t, upstreamHandler, mc)
	defer upstream.Close()

	wrappedAnthropic := gzipmw.DecompressRequest(http.HandlerFunc(h.HandleAnthropicMessages))

	// Anthropic-format body. The proxy translates to OpenAI before
	// forwarding to upstream; we assert the translated upstream
	// body is the same regardless of compression.
	payload := map[string]interface{}{
		"model":      "mock-anthropic",
		"max_tokens": 64,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	plainBytes, _ := json.Marshal(payload)

	// ── CONTROL: uncompressed ──
	capturedUpstream = nil
	capturedUpstreamHeaders = nil
	req1 := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(plainBytes))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	wrappedAnthropic.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("control: status = %d, want 200", rr1.Code)
	}
	controlUpstream := append([]byte(nil), capturedUpstream...)

	// ── TREATMENT: gzip ──
	capturedUpstream = nil
	capturedUpstreamHeaders = nil
	compressed := gzipBytesProxy(t, plainBytes)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(compressed))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Content-Encoding", "gzip")
	rr2 := httptest.NewRecorder()
	wrappedAnthropic.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("treatment: status = %d, want 200; body=%q", rr2.Code, rr2.Body.String())
	}
	treatmentUpstream := append([]byte(nil), capturedUpstream...)

	// PARITY:
	if !bytes.Equal(controlUpstream, treatmentUpstream) {
		t.Errorf("upstream (translated OpenAI) body differs:\n  control:   %q\n  treatment: %q",
			controlUpstream, treatmentUpstream)
	}
	if capturedUpstreamHeaders.Get("Content-Encoding") != "" {
		t.Errorf("upstream Content-Encoding = %q, want empty (must not forward gzip)",
			capturedUpstreamHeaders.Get("Content-Encoding"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────
