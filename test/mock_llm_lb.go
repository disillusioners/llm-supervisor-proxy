//go:build ignore

// Mock LLM Server for Credential Load Balancing E2E Tests
//
// Per-credential mock with optional identity echo, atomic 429 simulation,
// and JSONL hit-counter file. Used by test/test_mock_credential_lb.sh
// (Round 3c W9: three concurrent processes on ports 4001/4002/4003).
//
// Usage:
//
//	go run test/mock_llm_lb.go -port=4001 -credential-identity=A
//	go run test/mock_llm_lb.go -port=4002 -credential-identity=B -fail-429-once=2 \
//	    -hit-counter-file=/tmp/hits_B.jsonl
//
// Options:
//
//	-port                   string  Port to listen on (default "4001")
//	-credential-identity    string  Identity echoed in responses (default ""; empty = omit)
//	-fail-429-once          int     First N requests return HTTP 429 with Retry-After:1 (default 0 = disabled)
//	-hit-counter-file       string  Append one JSONL line per request to this file (default "" = disabled)
//
// Endpoints:
//
//	POST /v1/chat/completions   OpenAI format; supports stream=true (SSE) and stream=false (JSON)
//	POST /v1/messages           Anthropic-format stub (non-streaming only)
//	* /                         404 (default ServeMux behavior)
//
// Legacy default behavior (no flags): identical to mock_llm.go — responds with
// content "Hello from mock LLM" on /v1/chat/completions. Identity, 429, and
// hit-counter features only activate when their respective flags are set.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	flagPort           = flag.String("port", "4001", "Port to listen on")
	flagIdentity       = flag.String("credential-identity", "", "Identity echoed in responses (empty = omit)")
	flagFail429Once    = flag.Int("fail-429-once", 0, "First N requests return HTTP 429 (0 = disabled)")
	flagHitCounterFile = flag.String("hit-counter-file", "", "Append one JSONL line per request (empty = disabled)")
)

var (
	fail429Count atomic.Int64 // process-global counter for -fail-429-once
	hitFileMu    sync.Mutex    // serializes concurrent writes to the JSONL hit counter
	hitFile      *os.File      // opened once in main() when -hit-counter-file is set
)

func main() {
	flag.Parse()

	if *flagHitCounterFile != "" {
		f, err := os.OpenFile(*flagHitCounterFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("mock_llm_lb: failed to open -hit-counter-file=%q: %v", *flagHitCounterFile, err)
		}
		hitFile = f
		defer hitFile.Close()
	}

	if *flagIdentity != "" {
		log.Printf("mock_llm_lb: starting identity=%q port=%s", *flagIdentity, *flagPort)
	} else {
		log.Printf("mock_llm_lb: starting (no identity) port=%s", *flagPort)
	}
	if *flagFail429Once > 0 {
		log.Printf("mock_llm_lb: first %d requests will return HTTP 429", *flagFail429Once)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/v1/messages", handleMessages)

	addr := ":" + *flagPort
	log.Printf("mock_llm_lb: listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// responseContent returns the content string embedded in chat completions
// and Anthropic messages responses. When identity is set, it is included
// verbatim so callers can verify which credential handled the request.
func responseContent() string {
	if *flagIdentity != "" {
		return fmt.Sprintf("Hello from mock LLM %s", *flagIdentity)
	}
	return "Hello from mock LLM"
}

// requestLogPrefix returns the per-request log-line prefix. Empty string when
// no identity is configured (legacy behavior — keeps existing log greps clean).
func requestLogPrefix() string {
	if *flagIdentity != "" {
		return fmt.Sprintf("[identity=%s]", *flagIdentity)
	}
	return ""
}

// shouldRateLimit atomically increments the process-global counter and
// returns true if this request should be rate-limited. Once the counter
// exceeds -fail-429-once, no further 429s are emitted for the lifetime of
// this process.
func shouldRateLimit() bool {
	if *flagFail429Once <= 0 {
		return false
	}
	n := fail429Count.Add(1)
	return n <= int64(*flagFail429Once)
}

// writeRateLimited writes the standard 429 response with Retry-After:1,
// appends a JSONL hit-counter record (outcome="rate_limited"), and returns
// true to signal the caller should bail out.
func writeRateLimited(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s mock_llm_lb: 429 on %s (counter=%d/%d)",
		requestLogPrefix(), r.URL.Path, fail429Count.Load(), *flagFail429Once)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit"}}`))
	writeHitRecord(r, http.StatusTooManyRequests, "rate_limited")
}

// writeHitRecord appends one JSONL line to the hit counter file when one is
// configured. The schema is fixed (see spec): ts, path, outcome, status, and
// conditionally identity when non-empty. Safe to call from multiple goroutines.
func writeHitRecord(r *http.Request, status int, outcome string) {
	if hitFile == nil {
		return
	}
	rec := map[string]interface{}{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"path":    r.URL.Path,
		"outcome": outcome,
		"status":  status,
	}
	if *flagIdentity != "" {
		rec["identity"] = *flagIdentity
	}
	line, err := json.Marshal(rec)
	if err != nil {
		log.Printf("mock_llm_lb: failed to marshal hit record: %v", err)
		return
	}
	line = append(line, '\n')

	hitFileMu.Lock()
	defer hitFileMu.Unlock()
	if _, err := hitFile.Write(line); err != nil {
		log.Printf("mock_llm_lb: failed to write hit record: %v", err)
	}
}

// handleChatCompletions serves /v1/chat/completions in OpenAI format.
// Supports both stream=true (SSE) and stream=false (JSON).
func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		writeHitRecord(r, http.StatusMethodNotAllowed, "success")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("%s mock_llm_lb: read body error: %v", requestLogPrefix(), err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		writeHitRecord(r, http.StatusInternalServerError, "success")
		return
	}
	defer r.Body.Close()

	if shouldRateLimit() {
		writeRateLimited(w, r)
		return
	}

	var reqBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
		log.Printf("%s mock_llm_lb: json parse error: %v", requestLogPrefix(), err)
		http.Error(w, "invalid json body", http.StatusBadRequest)
		writeHitRecord(r, http.StatusBadRequest, "success")
		return
	}

	model := "mock-model"
	if m, ok := reqBody["model"].(string); ok && m != "" {
		model = m
	}

	isStream := true
	if s, ok := reqBody["stream"].(bool); ok && !s {
		isStream = false
	}

	content := responseContent()
	logPrefix := requestLogPrefix()

	if !isStream {
		log.Printf("%s mock_llm_lb: /v1/chat/completions non-stream model=%s", logPrefix, model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"id":      fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": content,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("%s mock_llm_lb: encode error: %v", logPrefix, err)
		}
		writeHitRecord(r, http.StatusOK, "success")
		return
	}

	// SSE streaming: emit a single content chunk, then a final chunk with
	// finish_reason="stop", then the [DONE] sentinel.
	log.Printf("%s mock_llm_lb: /v1/chat/completions stream model=%s", logPrefix, model)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		writeHitRecord(r, http.StatusInternalServerError, "success")
		return
	}

	// Content chunk (per spec: id/object/delta.content/index shape)
	fmt.Fprintf(w, "data: %s\n\n", buildChunk(model, content, false))
	flusher.Flush()

	// Final chunk: empty delta with finish_reason:"stop"
	fmt.Fprintf(w, "data: %s\n\n", buildChunk(model, "", true))
	flusher.Flush()

	// [DONE] sentinel
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	writeHitRecord(r, http.StatusOK, "success")
}

// buildChunk returns a serialized chat.completion.chunk JSON string.
// When final is true, the chunk carries finish_reason:"stop" with an empty delta.
func buildChunk(model, content string, final bool) string {
	choice := map[string]interface{}{
		"index": 0,
	}
	if final {
		choice["delta"] = map[string]interface{}{}
		choice["finish_reason"] = "stop"
	} else {
		choice["delta"] = map[string]interface{}{"content": content}
	}
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{choice},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// handleMessages serves /v1/messages in Anthropic format (non-streaming stub).
// Streaming is intentionally not implemented for this endpoint; the
// credential-affinity E2E test exercises the non-streaming shape only.
func handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		writeHitRecord(r, http.StatusMethodNotAllowed, "success")
		return
	}

	// Drain the body so the client connection can be reused, but we don't
	// parse any Anthropic fields in this stub.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		log.Printf("%s mock_llm_lb: read body error: %v", requestLogPrefix(), err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		writeHitRecord(r, http.StatusInternalServerError, "success")
		return
	}
	_ = r.Body.Close()

	if shouldRateLimit() {
		writeRateLimited(w, r)
		return
	}

	content := responseContent()
	log.Printf("%s mock_llm_lb: /v1/messages stub", requestLogPrefix())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := map[string]interface{}{
		"id":          "msg_mock",
		"type":        "message",
		"role":        "assistant",
		"content":     []map[string]interface{}{{"type": "text", "text": content}},
		"stop_reason": "end_turn",
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("%s mock_llm_lb: encode error: %v", requestLogPrefix(), err)
	}
	writeHitRecord(r, http.StatusOK, "success")
}