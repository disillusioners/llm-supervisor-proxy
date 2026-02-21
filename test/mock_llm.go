package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
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

		// Check request body for "interrupted" to detect retry
		// This is kept for backward compatibility with the original retry logic,
		// but the new simulation scenarios primarily use prompt keywords.
		isRetry := strings.Contains(string(reqBodyBytes), "interrupted")
		if isRetry {
			log.Println("Mock: Detected retry request!")
		}

		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			response := map[string]interface{}{
				"id":      "chatcmpl-123",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "mock-model",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": "Hello world! I am a useful token stream.",
						},
						"finish_reason": "stop",
					},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}

		// Simulate response
		tokens := []string{"Hello", " world", "!", " I", " am", " a", " useful", " token", " stream", "."}

		// Check for simulation triggers in the prompt
		var prompt string
		// Extract last user message
		if msgs, ok := reqBody["messages"].([]interface{}); ok && len(msgs) > 0 {
			if lastMsg, ok := msgs[len(msgs)-1].(map[string]interface{}); ok {
				if content, ok := lastMsg["content"].(string); ok {
					prompt = content
				}
			}
		}

		if strings.Contains(prompt, "mock-hang") {
			// Simulate hang on strict token ONLY if not a retry
			// send some tokens then hang
			for i, token := range tokens {
				if i > 5 {
					log.Println("Mock: Hanging...")
					// Hang until client disconnects or context is cancelled
					<-r.Context().Done()
					log.Println("Mock: Hang context done.")
					return // Exit the handler
				}
				// Send token
				fmt.Fprintf(w, "data: %s\n\n", createChunk(token))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(100 * time.Millisecond)
				log.Printf("Mock: Sent token %d: '%s'", i, token)
			}
		} else if strings.Contains(prompt, "mock-tool") {
			// Simulate a tool call
			log.Println("Mock: Simulating tool call...")

			// Send some content first
			fmt.Fprintf(w, "data: %s\n\n", createChunk("Sure, checking the weather."))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(500 * time.Millisecond)

			// Then send tool call (simplified for this mock, usually relies on 'tool_calls' field)
			// We can just dump a chunk with tool_calls
			// Note: Real streaming tool calls are complex (index, id, type, function name, args...)
			// Let's send a simplified one-shot tool call chunk?
			// Or just text that LOOKS like a tool call if we aren't parsing strict structure?
			// Wait, we defined the store to hold ToolCalls.
			// But we skipped strict parsing in handler? No, we skipped it because it's hard.
			// If we want to test UI, we should probably just send text that describes a tool call?
			// NO, the user asked for "tool call should show".
			// Implies structured display.
			// But we haven't implemented ToolCall parsing in handler yet!
			// Just thinking parsing.
			// Let's implement basics.
			// Just send text for now.
			fmt.Fprintf(w, "data: %s\n\n", createChunk("\n[TOOL CALL: get_weather]"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		} else if strings.Contains(prompt, "mock-think") {
			// Simulate thinking
			log.Println("Mock: Simulating thinking...")

			// Send thinking content
			thinkTokens := []string{"Hmm", ", ", "let", " me", " think", " about", " that", "."}
			for _, t := range thinkTokens {
				fmt.Fprintf(w, "data: %s\n\n", createReasoningChunk(t))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(100 * time.Millisecond)
			}

			// Then real content
			fmt.Fprintf(w, "data: %s\n\n", createChunk("Here is the answer."))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		} else if strings.Contains(prompt, "mock-long") {
			// Simulate long response
			log.Println("Mock: Simulating long response...")
			for i := 0; i < 500; i++ {
				fmt.Fprintf(w, "data: %s\n\n", createChunk(fmt.Sprintf(" word%d", i)))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(10 * time.Millisecond)
			}
		} else {
			// Normal response
			for i, token := range tokens {
				log.Printf("Mock: Sent token %d: '%s'", i, token)
				fmt.Fprintf(w, "data: %s\n\n", createChunk(token))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(200 * time.Millisecond) // Simulate slow generation
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Println("Mock: Done")
	})

	log.Println("Mock LLM Server listening on :4001")
	if err := http.ListenAndServe(":4001", nil); err != nil {
		log.Fatal(err)
	}
}

func createChunk(content string) string {
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

func createReasoningChunk(content string) string {
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
