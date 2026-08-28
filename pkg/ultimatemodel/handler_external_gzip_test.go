// Integration tests for transparent gzip request-body decompression
// at the ultimate-model external raw passthrough seam.
//
// pkg/ultimatemodel.handler_external.go's executeExternal is the
// "raw proxy" path: it re-emits the parsed request body bytes
// verbatim to the upstream URL configured in cfg.UpstreamURL. The
// re-emit happens via http.NewRequestWithContext at
// handler_external.go:121, where the body comes from
// bytes.NewReader(bodyBytes) — and bodyBytes is the JSON-encoded
// version of the parsed requestBody map that the proxy's
// initRequestContext produced by reading r.Body.
//
// Chain that makes this test meaningful:
//
//	Client → gzipmw.DecompressRequest (the middleware) →
//	  proxy.HandleChatCompletions → initRequestContext (reads r.Body) →
//	    ultimateHandler.Execute → executeExternal → upstream POST
//
// The middleware MUST swap r.Body early enough that initRequestContext
// sees the decompressed bytes, otherwise initRequestContext would
// fail to parse the body and Execute would never run. By the time
// executeExternal issues the upstream POST, the body bytes are
// already decompressed — so the upstream receives the same payload
// it would receive for an uncompressed request. That's the contract
// we assert in this test.
package ultimatemodel

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/middleware/gzipmw"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// gzipBytesUlt returns the gzip-compressed form of plain.
func gzipBytesUlt(t *testing.T, plain []byte) []byte {
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

// TestGzipMiddleware_ExternalRawPassthrough_DecompressedBodyOnWire
// proves that the gzip middleware swaps r.Body BEFORE the proxy
// handler parses it, so the ultimate-model external raw passthrough
// emits the DECOMPRESSED bytes to the upstream.
//
// The test wraps Handler.Execute (the ultimate-model entry point) in
// a small HTTP handler that:
//
//  1. Captures the original request body, headers, etc. (proves the
//     middleware transformed them BEFORE Execute was invoked).
//  2. Calls Execute exactly as proxy.HandleChatCompletions does on
//     the ultimate trigger path (handler.go:733-734).
//
// Then it compares the captured upstream body (what the raw
// passthrough sent to the configured upstream URL) against the
// body the same request would have produced uncompressed.
func TestGzipMiddleware_ExternalRawPassthrough_DecompressedBodyOnWire(t *testing.T) {
	// upstreamCaptured captures the raw-passthrough upstream POST.
	var upstreamCaptured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCaptured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ult-1","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL

	modelsCfg := newMockModelsConfig()
	// Non-internal model so executeExternal is the chosen path
	// (Handler.Execute: modelCfg.Internal == false →
	// h.executeExternal(...)). Reference: handler.go:851.
	modelsCfg.AddModel(models.ModelConfig{
		ID: "ultimate-model", Name: "ultimate-model",
		Enabled: true, Internal: false,
	})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Wrap the entire Execute call in the gzip middleware so we
	// can directly drive Handler.Execute with a gzip-encoded r.
	wrappedExecute := gzipmw.DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler.Execute is what proxy.HandleChatCompletions
		// calls when the ultimate-model triggers; we recreate
		// the call shape here with a minimal parsed body so
		// executeExternal's raw-passthrough path runs end-to-end.
		plain, _ := io.ReadAll(r.Body) // post-decompression
		var body map[string]interface{}
		_ = json.Unmarshal(plain, &body)
		headersSent := false
		hash := "test-hash"
		_, err := h.Execute(context.Background(), w, r, body,
			"ultimate-model", hash, &headersSent, nil, "",
		)
		if err != nil {
			t.Errorf("Execute returned error: %v", err)
		}
	}))

	payload := map[string]interface{}{
		"model":  "ultimate-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "gzip me"},
		},
	}
	plainBytes, _ := json.Marshal(payload)

	// ── CONTROL: uncompressed ──
	upstreamCaptured = nil
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(plainBytes))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	wrappedExecute.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("control: status = %d, want 200; body=%q", rr1.Code, rr1.Body.String())
	}
	controlUpstream := append([]byte(nil), upstreamCaptured...)

	// ── TREATMENT: gzip ──
	upstreamCaptured = nil
	compressed := gzipBytesUlt(t, plainBytes)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Content-Encoding", "gzip")
	rr2 := httptest.NewRecorder()
	wrappedExecute.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("treatment: status = %d, want 200; body=%q", rr2.Code, rr2.Body.String())
	}
	treatmentUpstream := append([]byte(nil), upstreamCaptured...)

	// PARITY: the raw passthrough must send identical bytes
	// regardless of whether the client gzip-encoded them.
	if !bytes.Equal(controlUpstream, treatmentUpstream) {
		t.Errorf("upstream body differs between uncompressed and gzip:\n  control:   %s\n  treatment: %s",
			controlUpstream, treatmentUpstream)
	}

	// Sanity: confirm the bodies actually parse as JSON and carry
	// the expected user message — guards against silent empty-body
	// regressions in the middleware / Execute path.
	var sentBody, seenBody map[string]interface{}
	if err := json.Unmarshal(plainBytes, &sentBody); err != nil {
		t.Fatalf("control body is not JSON: %v", err)
	}
	if err := json.Unmarshal(treatmentUpstream, &seenBody); err != nil {
		t.Fatalf("treatment upstream body is not JSON: %v body=%s", err, treatmentUpstream)
	}
	if msgs, ok := seenBody["messages"].([]interface{}); !ok || len(msgs) == 0 {
		t.Errorf("treatment upstream lost messages field")
	}
}

// TestGzipMiddleware_ExternalRawPassthrough_NoContentEncodingOnWire
// asserts the secondary contract: the raw-passthrough upstream POST
// must NOT carry Content-Encoding: gzip (the middleware strips it
// before the request reaches any handler).
func TestGzipMiddleware_ExternalRawPassthrough_NoContentEncodingOnWire(t *testing.T) {
	var upstreamHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeaders = r.Header.Clone()
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{
		ID: "ultimate-model", Name: "ultimate-model",
		Enabled: true, Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	wrapped := gzipmw.DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]interface{}
		_ = json.Unmarshal(body, &parsed)
		headersSent := false
		_, _ = h.Execute(context.Background(), w, r, parsed,
			"ultimate-model", "hash", &headersSent, nil, "",
		)
	}))

	payload, _ := json.Marshal(map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []interface{}{},
	})
	compressed := gzipBytesUlt(t, payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if got := upstreamHeaders.Get("Content-Encoding"); got != "" {
		t.Errorf("upstream Content-Encoding = %q, want empty (must not forward gzip)", got)
	}
	if upstreamHeaders.Get("Content-Length") != "" {
		// http.Client may add Content-Length matching the
		// forwarded body size; if so, it must equal the
		// decompressed length. (http.Client will normally NOT
		// set Content-Length for chunked transfers.)
		if got := upstreamHeaders.Get("Content-Length"); got != strconv.Itoa(len(payload)) {
			t.Errorf("upstream Content-Length = %q, want %d", got, len(payload))
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Sanity: a corrupt gzip body at the entry must be rejected before
// Handler.Execute is ever called.
// ─────────────────────────────────────────────────────────────────────────────

func TestGzipMiddleware_ExternalRawPassthrough_CorruptGzip_Returns400(t *testing.T) {
	var executeInvoked bool

	wrapped := gzipmw.DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executeInvoked = true
		w.WriteHeader(http.StatusOK)
	}))

	corrupt := []byte{0x1f, 0x8b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(corrupt))
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if executeInvoked {
		t.Errorf("downstream was invoked despite corrupt gzip")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "invalid_gzip_body" {
		t.Errorf("error.code = %q, want invalid_gzip_body", env.Error.Code)
	}
}
