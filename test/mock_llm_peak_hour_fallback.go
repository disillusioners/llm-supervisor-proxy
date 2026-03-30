//go:build ignore

// Mock LLM Server for Peak Hour Fallback Testing
//
// This server simulates specific scenarios to test peak hour fallback behavior:
// - mock-peak-upstream -> 500 error
// - mock-fallback-normal -> 200 success
// - mock-fallback-peak-upstream -> 200 success
// - mock-normal-upstream -> 500 error
// - unknown -> 500 error
//
// Usage:
//   go run test/mock_llm_peak_hour_fallback.go -port 19001

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

var (
	flagPort = flag.String("port", "19001", "Port to listen on")
)

func main() {
	flag.Parse()

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		timestamp := time.Now().Format("2006-01-02T15:04:05.000Z07:00")

		// Read the request body
		reqBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			log.Printf("[%s] Error reading request body: %v", timestamp, err)
			return
		}
		defer r.Body.Close()

		var reqBody map[string]interface{}
		if err := json.Unmarshal(reqBodyBytes, &reqBody); err != nil {
			http.Error(w, "Failed to parse request body as JSON", http.StatusBadRequest)
			log.Printf("[%s] Error unmarshaling request body: %v", timestamp, err)
			return
		}

		// Extract model name
		model := "unknown"
		if m, ok := reqBody["model"].(string); ok {
			model = m
		}

		// Extract prompt for logging
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

		// Log EVERY request
		log.Printf("[%s] REQUEST: model=%q, response_code=N/A (request received)", timestamp, model)

		// Determine response based on model name
		var responseCode int
		var responseBody string

		switch model {
		case "mock-peak-upstream":
			// Peak upstream always fails
			responseCode = http.StatusInternalServerError
			responseBody = `{"error":{"message":"Internal Server Error","type":"internal_error"}}`
			log.Printf("[%s] RESPONSE: model=%q -> %d (peak upstream - always fails)", timestamp, model, responseCode)

		case "mock-fallback-normal":
			// Normal fallback - always succeeds
			responseCode = http.StatusOK
			responseBody = `{"id":"chatcmpl-test","object":"chat.completion","created":1234567890,"model":"mock-fallback-normal","choices":[{"index":0,"message":{"role":"assistant","content":"Fallback normal response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
			log.Printf("[%s] RESPONSE: model=%q -> %d (fallback normal - success)", timestamp, model, responseCode)

		case "mock-fallback-peak-upstream":
			// Fallback peak upstream - always succeeds
			responseCode = http.StatusOK
			responseBody = `{"id":"chatcmpl-test","object":"chat.completion","created":1234567890,"model":"mock-fallback-peak-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"Fallback peak response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
			log.Printf("[%s] RESPONSE: model=%q -> %d (fallback peak - success)", timestamp, model, responseCode)

		case "mock-normal-upstream":
			// Normal upstream always fails
			responseCode = http.StatusInternalServerError
			responseBody = `{"error":{"message":"Internal Server Error","type":"internal_error"}}`
			log.Printf("[%s] RESPONSE: model=%q -> %d (normal upstream - always fails)", timestamp, model, responseCode)

		default:
			// Unknown model - always fail
			responseCode = http.StatusInternalServerError
			responseBody = `{"error":{"message":"Unknown model","type":"invalid_request_error"}}`
			log.Printf("[%s] RESPONSE: model=%q -> %d (unknown model - fails)", timestamp, model, responseCode)
		}

		// Set headers and send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(responseCode)
		w.Write([]byte(responseBody))
	})

	log.Printf("Mock LLM Peak Hour Fallback Server listening on :%s", *flagPort)
	log.Printf("Available endpoints:")
	log.Printf("  - mock-peak-upstream -> 500 error")
	log.Printf("  - mock-fallback-normal -> 200 success")
	log.Printf("  - mock-fallback-peak-upstream -> 200 success")
	log.Printf("  - mock-normal-upstream -> 500 error")
	log.Printf("  - unknown -> 500 error")

	if err := http.ListenAndServe(":"+*flagPort, nil); err != nil {
		log.Fatal(err)
	}
}

// createChunk creates an SSE chunk for streaming responses (not used in this mock but kept for compatibility)
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
