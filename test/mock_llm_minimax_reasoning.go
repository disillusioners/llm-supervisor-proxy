//go:build ignore

// Mock LLM Server for MiniMax reasoning_details testing.
//
// This server is the upstream side of the x-proxy-interleaved-thinking
// translation feature. The proxy translates:
//
//	request:   messages[i].reasoning_content (string)
//	         → messages[i].reasoning_details = [{type:"reasoning.text", id, format, index, text}]
//	         + top-level reasoning_split:true
//	         + strips original reasoning_content
//
//	response: choices[0].message.reasoning_details (array) → reasoning_content (concat)
//	         choices[0].delta.reasoning_details (SSE)      → reasoning_content deltas
//
// Mode selection is driven by a marker token in the LAST user message.
// Markers (recognized in last user content):
//
//	MODE-NS-DETAILS         : non-stream with reasoning_details array
//	MODE-NS-BOTH            : non-stream with reasoning_details AND reasoning_content
//	MODE-NS-CUMULATIVE-TEXT : non-stream cumulative-text reasoning_details
//	MODE-STREAM-INCREMENTAL : SSE 3 incremental reasoning chunks + content + DONE
//	MODE-STREAM-CUMULATIVE  : SSE cumulative reasoning (A, AB, ABC) + content + DONE
//	MODE-STREAM-BOTH        : SSE chunks with BOTH reasoning_content + reasoning_details
//	MODE-STREAM-EMPTYTEXT   : SSE with one empty-text entry + one real entry
//	MODE-STREAM-MULTIENTRY  : SSE ONE chunk with reasoning_details array of 2 entries
//	MODE-PLAIN              : vanilla OpenAI reasoning_content for legacy passthrough
//	MODE-ERROR-500          : HTTP 500 with error body
//
// Usage:
//
//	go run test/mock_llm_minimax_reasoning.go -port=4005
//
// Capture: every request is appended (one JSON object per line) to
//
//	/tmp/minimax_reasoning_capture_<port>.jsonl
//
// Each line: {"headers": {<flat header map>}, "body": <parsed json body>}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	captureMu sync.Mutex
	captureFP *os.File
)

func main() {
	port := flag.String("port", "4005", "Port to listen on")
	flag.Parse()

	// Open capture file (truncate on start).
	capturePath := fmt.Sprintf("/tmp/minimax_reasoning_capture_%s.jsonl", *port)
	f, err := os.OpenFile(capturePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatalf("failed to open capture file %s: %v", capturePath, err)
	}
	captureFP = f
	defer captureFP.Close()
	log.Printf("[MiniMax-Mock] capturing requests to %s", capturePath)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)

	log.Printf("[MiniMax-Mock] Server listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, mux); err != nil {
		log.Fatal(err)
	}
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	log.Println("[MiniMax-Mock] Received request")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Capture request (headers + parsed body).
	var parsed map[string]interface{}
	_ = json.Unmarshal(bodyBytes, &parsed)
	captureRequest(r, parsed)

	// Extract mode from last user message.
	mode := extractMode(parsed)
	log.Printf("[MiniMax-Mock] mode=%s", mode)

	switch mode {
	case "MODE-NS-DETAILS":
		respondNSDetails(w, false)
	case "MODE-NS-BOTH":
		respondNSDetails(w, true)
	case "MODE-NS-CUMULATIVE-TEXT":
		respondNSCumulative(w)
	case "MODE-STREAM-INCREMENTAL":
		respondStreamIncremental(w)
	case "MODE-STREAM-CUMULATIVE":
		respondStreamCumulative(w)
	case "MODE-STREAM-BOTH":
		respondStreamBoth(w)
	case "MODE-STREAM-EMPTYTEXT":
		respondStreamEmptyText(w)
	case "MODE-STREAM-MULTIENTRY":
		respondStreamMultiEntry(w)
	case "MODE-PLAIN":
		// Auto-detect stream vs non-stream from request.
		if s, ok := parsed["stream"].(bool); ok && s {
			respondStreamPlain(w)
		} else {
			respondNSPlain(w)
		}
	case "MODE-ERROR-500":
		respondError500(w)
	default:
		// Unknown mode: echo a basic non-stream response.
		log.Printf("[MiniMax-Mock] unknown mode %q — echoing default", mode)
		respondDefault(w)
	}
}

// extractMode picks the MODE-* marker from the last user message content.
// Returns the longest prefix of the first token starting with "MODE-" that
// matches a known handler (so a trailing suffix like "-3msg" is tolerated
// and T3/T3b can share the same wire behavior with distinct content).
func extractMode(parsed map[string]interface{}) string {
	known := []string{
		"MODE-STREAM-MULTIENTRY",
		"MODE-STREAM-EMPTYTEXT",
		"MODE-STREAM-CUMULATIVE",
		"MODE-STREAM-INCREMENTAL",
		"MODE-STREAM-BOTH",
		"MODE-NS-CUMULATIVE-TEXT",
		"MODE-NS-BOTH",
		"MODE-NS-DETAILS",
		"MODE-PLAIN",
		"MODE-ERROR-500",
	}
	msgs, ok := parsed["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		return ""
	}
	last, ok := msgs[len(msgs)-1].(map[string]interface{})
	if !ok {
		return ""
	}
	content, _ := last["content"].(string)
	if content == "" {
		return ""
	}
	for _, m := range known {
		if strings.Contains(content, m) {
			return m
		}
	}
	return ""
}

// captureRequest appends one JSONL record.
func captureRequest(r *http.Request, body map[string]interface{}) {
	hdr := map[string]string{}
	for k, vs := range r.Header {
		// Collapse multi-value headers to comma-joined for readability.
		if len(vs) == 1 {
			hdr[k] = vs[0]
		} else {
			hdr[k] = strings.Join(vs, ",")
		}
	}
	rec := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"method":    r.Method,
		"path":      r.URL.Path,
		"headers":   hdr,
		"body":      body,
	}
	recBytes, err := json.Marshal(rec)
	if err != nil {
		log.Printf("[MiniMax-Mock] capture marshal err: %v", err)
		return
	}
	captureMu.Lock()
	defer captureMu.Unlock()
	_, _ = captureFP.Write(append(recBytes, '\n'))
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Standard usage block (added to all success responses).
var usageBlock = map[string]interface{}{
	"prompt_tokens":     11,
	"completion_tokens": 7,
	"total_tokens":      18,
}

// === Non-stream handlers ===

func respondNSDetails(w http.ResponseWriter, both bool) {
	msg := map[string]interface{}{
		"role":    "assistant",
		"content": "final answer",
		"reasoning_details": []map[string]interface{}{
			{
				"type":   "reasoning.text",
				"id":     "reasoning-text-1",
				"format": "MiniMax-response-v1",
				"index":  0,
				"text":   "think-A",
			},
			{
				"type":   "reasoning.text",
				"id":     "reasoning-text-2",
				"format": "MiniMax-response-v1",
				"index":  0,
				"text":   " think-B",
			},
		},
	}
	if both {
		msg["reasoning_content"] = "think-A think-B"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-minimax-reasoning",
		"choices": []map[string]interface{}{
			{"index": 0, "message": msg, "finish_reason": "stop"},
		},
		"usage": usageBlock,
	})
}

func respondNSCumulative(w http.ResponseWriter) {
	// Two entries: second.text == concat("A","B") i.e. "AB" — cumulative variant.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-minimax-reasoning",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "ok",
					"reasoning_details": []map[string]interface{}{
						{
							"type": "reasoning.text", "id": "reasoning-text-1",
							"format": "MiniMax-response-v1", "index": 0, "text": "AB",
						},
					},
				},
				"finish_reason": "stop",
			},
		},
		"usage": usageBlock,
	})
}

func respondNSPlain(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-minimax-reasoning",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           "hello",
					"reasoning_content": "legacy-think",
				},
				"finish_reason": "stop",
			},
		},
		"usage": usageBlock,
	})
}

func respondError500(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": "upstream exploded",
			"type":    "server_error",
			"code":    500,
		},
	})
}

func respondDefault(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-minimax-reasoning",
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       map[string]interface{}{"role": "assistant", "content": "default"},
				"finish_reason": "stop",
			},
		},
		"usage": usageBlock,
	})
}

// === Stream handlers (SSE) ===

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload map[string]interface{}) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "data: %s\n\n", b)
	if flusher != nil {
		flusher.Flush()
	}
}

func writeSSERaw(w http.ResponseWriter, flusher http.Flusher, rawData string) {
	fmt.Fprintf(w, "data: %s\n\n", rawData)
	if flusher != nil {
		flusher.Flush()
	}
}

func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func streamChunk(id string, delta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "mock-minimax-reasoning",
		"choices": []map[string]interface{}{
			{"index": 0, "delta": delta},
		},
	}
}

func openStream(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	flusher.Flush()
	return flusher, true
}

func respondStreamIncremental(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	// 3 reasoning chunks then content then finish.
	thinkTexts := []string{"think-1", "think-2", "think-3"}
	for _, t := range thinkTexts {
		writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
			"reasoning_details": []map[string]interface{}{
				{
					"type": "reasoning.text", "id": "reasoning-text-1",
					"format": "MiniMax-response-v1", "index": 0, "text": t,
				},
			},
		}))
		time.Sleep(20 * time.Millisecond)
	}
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"content": "answer",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}

func respondStreamCumulative(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	// Cumulative text: "A", "AB", "ABC" — proxy must emit suffixes "A","B","C".
	cumulative := []string{"A", "AB", "ABC"}
	for _, t := range cumulative {
		writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
			"reasoning_details": []map[string]interface{}{
				{
					"type": "reasoning.text", "id": "reasoning-text-1",
					"format": "MiniMax-response-v1", "index": 0, "text": t,
				},
			},
		}))
		time.Sleep(20 * time.Millisecond)
	}
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"content": "done",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}

func respondStreamBoth(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	// Single chunk with BOTH reasoning_content and reasoning_details.
	// Translation: details win (single-winner) — client sees "from-details" once.
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"reasoning_content": "from-reasoning_content",
		"reasoning_details": []map[string]interface{}{
			{
				"type": "reasoning.text", "id": "reasoning-text-1",
				"format": "MiniMax-response-v1", "index": 0, "text": "from-details",
			},
		},
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"content": "answer",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}

func respondStreamEmptyText(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	// First chunk with empty text — must NOT produce an empty reasoning_content delta.
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"reasoning_details": []map[string]interface{}{
			{
				"type": "reasoning.text", "id": "reasoning-text-1",
				"format": "MiniMax-response-v1", "index": 0, "text": "",
			},
		},
	}))
	// Then a real one.
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"reasoning_details": []map[string]interface{}{
			{
				"type": "reasoning.text", "id": "reasoning-text-2",
				"format": "MiniMax-response-v1", "index": 0, "text": "real-think",
			},
		},
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"content": "answer",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}

func respondStreamMultiEntry(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	// ONE chunk with TWO reasoning_details entries — ordered emission.
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"reasoning_details": []map[string]interface{}{
			{
				"type": "reasoning.text", "id": "reasoning-text-1",
				"format": "MiniMax-response-v1", "index": 0, "text": "first",
			},
			{
				"type": "reasoning.text", "id": "reasoning-text-2",
				"format": "MiniMax-response-v1", "index": 0, "text": "second",
			},
		},
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"content": "answer",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}

func respondStreamPlain(w http.ResponseWriter) {
	flusher, ok := openStream(w)
	if !ok {
		return
	}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"reasoning_content": "legacy-think",
		"content":           "hello",
	}))
	writeSSE(w, flusher, "", streamChunk(id, map[string]interface{}{
		"finish_reason": "stop",
	}))
	writeSSEDone(w, flusher)
}
