//go:build ignore

// Mock LLM Server for Race Retry Testing
//
// This server simulates specific scenarios to test the race retry system:
// 1. mock-idle-timeout: Stream that pauses longer than idle timeout (should spawn parallel requests)
// 2. mock-streaming-deadline: Stream that takes longer than MaxGenerationTime (should pick best buffer)
// 3. mock-fast-complete: Fast completion for comparison (should win race)
//
// Usage:
//
//	go run test/mock_llm_race.go [options]
//
// Options:
//
//	-port string       Port to listen on (default "4001")
//	-idle-pause int    Pause duration in seconds for idle-timeout test (default 10)
//	-deadline-interval Interval between tokens in seconds for deadline test (default 3)
//
// Default port: 4001

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	flagIdlePause        = flag.Int("idle-pause", 10, "Pause duration in seconds for idle-timeout test")
	flagDeadlineInterval = flag.Int("deadline-interval", 3, "Interval between tokens in seconds for deadline test")
	flagSlowStart        = flag.Int("slow-start", 5, "Delay before first token in seconds for slow-start test")
)

func main() {
	port := flag.String("port", "4001", "Port to listen on")
	flag.Parse()

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received request")

		// Read the request body
		reqBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			log.Printf("Error reading request body: %v", err)
			return
		}
		defer r.Body.Close()

		var reqBody map[string]interface{}
		if err := json.Unmarshal(reqBodyBytes, &reqBody); err != nil {
			http.Error(w, "Failed to parse request body as JSON", http.StatusBadRequest)
			log.Printf("Error unmarshaling request body: %v", err)
			return
		}

		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		// Extract model name
		model := "mock-model"
		if m, ok := reqBody["model"].(string); ok {
			model = m
		}

		// Extract prompt for scenario detection
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

		log.Printf("Mock: Model=%s, Stream=%v, Prompt preview=%s", model, isStream, truncate(prompt, 50))

		// For error scenarios, return HTTP error immediately (don't use SSE streaming)
		// This ensures the proxy correctly detects the HTTP status code
		if strings.Contains(prompt, "mock-rate-limit-error") || strings.Contains(prompt, "mock-rate-limit-http") {
			log.Printf("[%s] Simulating RATE LIMIT ERROR (429)", model)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit"}}`))
			return
		}

		if strings.Contains(prompt, "mock-context-overflow") {
			log.Printf("[%s] Simulating CONTEXT OVERFLOW ERROR", model)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"Context window exceeded - maximum context length is 8192 tokens","type":"context_length_exceeded"}}`))
			return
		}

		if strings.Contains(prompt, "mock-upstream-unavailable") {
			log.Printf("[%s] Simulating UPSTREAM UNAVAILABLE (502)", model)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":{"message":"Upstream service unavailable","type":"upstream_error"}}`))
			return
		}

		if !isStream {
			handleNonStream(w, model, prompt)
			return
		}

		// Handle streaming scenarios
		handleStream(w, r, model, prompt)
	})

	log.Printf("Mock LLM Race Server listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleNonStream(w http.ResponseWriter, model, prompt string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": fmt.Sprintf("Non-streaming response from %s", model),
				},
				"finish_reason": "stop",
			},
		},
	}
	json.NewEncoder(w).Encode(response)
}

func handleStream(w http.ResponseWriter, r *http.Request, model, prompt string) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Send initial connection message
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Scenario 1: Idle timeout test
	// Send a few tokens, then pause for longer than idle timeout
	// The proxy should spawn parallel requests when this happens
	if strings.Contains(prompt, "mock-idle-timeout") {
		log.Printf("[%s] Simulating IDLE TIMEOUT scenario (pause=%ds)", model, *flagIdlePause)
		tokens := []string{"Hello", " from", " idle-timeout", " test."}

		// Send initial tokens
		for i, token := range tokens {
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			time.Sleep(100 * time.Millisecond)
		}

		// Now pause for a configurable time (simulating idle)
		// The proxy should detect idle timeout and spawn parallel requests
		// During this pause, the proxy should:
		// 1. Detect idle timeout
		// 2. Spawn parallel requests (second + fallback)
		// 3. NOT cancel this request (it should continue)
		pauseDuration := time.Duration(*flagIdlePause) * time.Second
		log.Printf("[%s] Starting LONG PAUSE (%v) to trigger idle timeout...", model, pauseDuration)

		// Use select with context to allow early termination
		select {
		case <-time.After(pauseDuration):
			log.Printf("[%s] Pause finished, resuming stream", model)
		case <-r.Context().Done():
			log.Printf("[%s] Context cancelled during pause", model)
			return
		}

		// After pause, send more tokens
		moreTokens := []string{" Resumed", " after", " pause", "."}
		for i, token := range moreTokens {
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			time.Sleep(100 * time.Millisecond)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Printf("[%s] Stream completed (idle-timeout scenario)", model)
		return
	}

	// Scenario 2: Streaming deadline test
	// Stream continuously but slowly, exceeding MaxGenerationTime
	// The proxy should pick the best buffer when deadline is reached
	if strings.Contains(prompt, "mock-streaming-deadline") {
		log.Printf("[%s] Simulating STREAMING DEADLINE scenario (interval=%ds)", model, *flagDeadlineInterval)

		// Stream slowly with configurable interval
		// Send tokens every flagDeadlineInterval seconds for a long time
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				log.Printf("[%s] Context cancelled at iteration %d", model, i)
				return
			default:
			}

			token := fmt.Sprintf(" word%d", i)
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)

			// Sleep with configurable interval
			time.Sleep(time.Duration(*flagDeadlineInterval) * time.Second)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Printf("[%s] Stream completed (streaming-deadline scenario)", model)
		return
	}

	// Scenario 3: Fast complete (for race winning)
	// Complete quickly to win the race against slow requests
	if strings.Contains(prompt, "mock-fast-complete") {
		log.Printf("[%s] Simulating FAST COMPLETE scenario", model)
		tokens := []string{"Fast", " response", " from", " winner", "."}

		for i, token := range tokens {
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			time.Sleep(50 * time.Millisecond) // Fast!
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Printf("[%s] Stream completed (fast-complete scenario)", model)
		return
	}

	// Scenario 4: Immediate error (should spawn parallel immediately)
	if strings.Contains(prompt, "mock-immediate-error") {
		log.Printf("[%s] Simulating IMMEDIATE ERROR scenario", model)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "data: {\"error\": \"Simulated immediate error\"}\n\n")
		flusher.Flush()
		return
	}

	// Scenario 5: Slow start then fast complete
	// Wait before sending first token, then complete quickly
	if strings.Contains(prompt, "mock-slow-start") {
		log.Printf("[%s] Simulating SLOW START scenario (delay=%ds)", model, *flagSlowStart)

		// Wait configurable seconds before starting
		select {
		case <-time.After(time.Duration(*flagSlowStart) * time.Second):
			log.Printf("[%s] Slow start wait finished", model)
		case <-r.Context().Done():
			log.Printf("[%s] Context cancelled during slow start", model)
			return
		}

		tokens := []string{"Slow", " start", " but", " fast", " finish", "."}
		for i, token := range tokens {
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			time.Sleep(50 * time.Millisecond)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Printf("[%s] Stream completed (slow-start scenario)", model)
		return
	}

	// Scenario 6: Partial stream then hang (for testing best buffer selection)
	// Send some tokens, then hang until cancelled
	if strings.Contains(prompt, "mock-partial-hang") {
		log.Printf("[%s] Simulating PARTIAL HANG scenario", model)
		tokens := []string{"Partial", " stream", " then", " hang"}

		for i, token := range tokens {
			fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
			flusher.Flush()
			log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			time.Sleep(100 * time.Millisecond)
		}

		log.Printf("[%s] Now hanging until cancelled...", model)
		<-r.Context().Done()
		log.Printf("[%s] Context cancelled, exiting", model)
		return
	}

	// Scenario 7: Slow stream for heartbeat testing
	// Sends data every 2 seconds for a long time to keep connection alive
	// Used for testing heartbeat feature
	if strings.Contains(prompt, "mock-slow-stream") {
		log.Printf("[%s] Simulating SLOW STREAM for heartbeat testing (2s interval, long duration)", model)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				log.Printf("[%s] Context cancelled at iteration %d", model, i)
				return
			case <-ticker.C:
				token := fmt.Sprintf(" heartbeat-test-%d", i)
				fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
				flusher.Flush()
				log.Printf("[%s] Sent token %d: '%s'", model, i, token)
			}
		}
	}

	// Default: Normal response
	log.Printf("[%s] Normal streaming response", model)
	tokens := []string{"Hello", " world", "!", " I", " am", " a", " useful", " token", " stream", "."}
	for i, token := range tokens {
		fmt.Fprintf(w, "data: %s\n\n", createChunk(model, token))
		flusher.Flush()
		log.Printf("[%s] Sent token %d: '%s'", model, i, token)
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Printf("[%s] Stream completed (normal)", model)
}

func createChunk(model, content string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
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
	return string(b)
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
