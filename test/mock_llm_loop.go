//go:build ignore

// mock_llm_loop.go — A mock LLM server that simulates various loop behaviors
// for testing the loop detection feature.
//
// Usage:
//   go run mock_llm_loop.go
//
// Trigger keywords in user prompt:
//   "loop-exact"       → Responds with identical messages across multiple requests
//   "loop-similar"     → Responds with near-identical messages (minor word swaps)
//   "loop-action"      → Emits the same tool call repeatedly
//   "loop-oscillate"   → Alternates between two tool calls (read/write pattern)
//   "loop-thinking"    → Repeats the same reasoning/thinking content
//   (default)          → Normal, non-looping response
//
// Runs on port :4002 by default (different from the main mock on :4001).

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const loopPort = ":4002"

// requestCounter tracks how many times a given loop scenario has been called.
// In a real scenario the proxy sends retried/multi-turn requests to the same
// upstream. This counter lets us simulate "the LLM keeps doing the same thing
// across multiple calls" which is what triggers multi-turn loop detection.
var requestCounter int

func main() {
	http.HandleFunc("/v1/chat/completions", handleCompletion)

	log.Printf("🔁 Mock Loop LLM Server listening on %s", loopPort)
	log.Println("Trigger keywords: loop-exact, loop-similar, loop-action, loop-oscillate, loop-thinking")
	if err := http.ListenAndServe(loopPort, nil); err != nil {
		log.Fatal(err)
	}
}

func handleCompletion(w http.ResponseWriter, r *http.Request) {
	requestCounter++
	callNum := requestCounter
	log.Printf("[#%d] Received request", callNum)

	var reqBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	prompt := extractPrompt(reqBody)

	isStream := true
	if s, ok := reqBody["stream"].(bool); ok && !s {
		isStream = false
	}

	switch {
	case strings.Contains(prompt, "loop-exact"):
		log.Printf("[#%d] Simulating EXACT loop", callNum)
		if isStream {
			streamExactLoop(w, r)
		}
	case strings.Contains(prompt, "loop-similar"):
		log.Printf("[#%d] Simulating SIMILARITY loop", callNum)
		if isStream {
			streamSimilarLoop(w, r, callNum)
		}
	case strings.Contains(prompt, "loop-action"):
		log.Printf("[#%d] Simulating ACTION REPEAT loop", callNum)
		if isStream {
			streamActionLoop(w, r)
		}
	case strings.Contains(prompt, "loop-oscillate"):
		log.Printf("[#%d] Simulating ACTION OSCILLATION loop", callNum)
		if isStream {
			streamOscillationLoop(w, r, callNum)
		}
	case strings.Contains(prompt, "loop-thinking"):
		log.Printf("[#%d] Simulating THINKING loop", callNum)
		if isStream {
			streamThinkingLoop(w, r)
		}
	default:
		log.Printf("[#%d] Normal response", callNum)
		if isStream {
			streamNormalResponse(w, r)
		} else {
			writeNonStreamResponse(w)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 1: Exact Loop
// The LLM responds with the SAME exact message every time.
// Should trigger: ExactMatchStrategy (after 2+ identical responses)
// ─────────────────────────────────────────────────────────────────────────────

func streamExactLoop(w http.ResponseWriter, r *http.Request) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	// Always send the exact same response — this simulates an LLM stuck
	// repeating the same output across multiple completions within one request.
	loopMessage := "Let me check that file for you. I will read the configuration file and look at the settings. Let me check the database configuration section."

	tokens := strings.Fields(loopMessage)
	for _, token := range tokens {
		fmt.Fprintf(w, "data: %s\n\n", createContentChunk(token+" "))
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("  → Sent exact-loop response")
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 2: Similarity Loop
// The LLM responds with near-identical messages — same structure, minor word swaps.
// Should trigger: SimilarityStrategy (SimHash similarity > 0.85)
// ─────────────────────────────────────────────────────────────────────────────

func streamSimilarLoop(w http.ResponseWriter, r *http.Request, callNum int) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	// Slight variations of the same message — different enough to not be
	// byte-identical, but similar enough to trigger SimHash detection.
	variations := []string{
		"Let me check the configuration file and read the database settings for the connection strings and timeout values",
		"Let me check the configuration file and read the database settings for the connection values and timeout limits",
		"Let me check the configuration file and read the database settings for the connection setup and timeout params",
		"Let me check the configuration file and read the database settings for the connection config and timeout values",
		"Let me check the configuration file and read the database settings for the connection parameters and timeout settings",
	}

	msg := variations[(callNum-1)%len(variations)]
	tokens := strings.Fields(msg)
	for _, token := range tokens {
		fmt.Fprintf(w, "data: %s\n\n", createContentChunk(token+" "))
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Printf("  → Sent similar-loop response (variation %d)", (callNum-1)%len(variations)+1)
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 3: Action Repeat Loop
// The LLM keeps calling the SAME tool call over and over.
// Should trigger: ActionPatternStrategy (3+ consecutive identical actions)
//
// In a real agentic flow this looks like:
//   read_file("config.go") → read_file("config.go") → read_file("config.go")
// ─────────────────────────────────────────────────────────────────────────────

func streamActionLoop(w http.ResponseWriter, r *http.Request) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	// Send some content then a tool call — always the same tool call
	fmt.Fprintf(w, "data: %s\n\n", createContentChunk("Let me read that file again to check. "))
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	fmt.Fprintf(w, "data: %s\n\n", createContentChunk("I need to verify the configuration. "))
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Simulate tool call chunk (the same one every time)
	toolCallChunk := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index": 0,
							"id":    "call_abc123",
							"type":  "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": `{"path": "config.go"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(toolCallChunk)
	fmt.Fprintf(w, "data: %s\n\n", string(b))
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("  → Sent action-loop response (read_file config.go)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 4: Oscillation Loop
// The LLM alternates between two actions: read → write → read → write
// Should trigger: ActionPatternStrategy oscillation detection
// ─────────────────────────────────────────────────────────────────────────────

func streamOscillationLoop(w http.ResponseWriter, r *http.Request, callNum int) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	// Alternate between read and write
	var actionName, actionArgs string
	if callNum%2 == 1 {
		actionName = "read_file"
		actionArgs = `{"path": "config.go"}`
		fmt.Fprintf(w, "data: %s\n\n", createContentChunk("Let me read the config file to understand the current state. "))
	} else {
		actionName = "write_file"
		actionArgs = `{"path": "config.go", "content": "updated"}`
		fmt.Fprintf(w, "data: %s\n\n", createContentChunk("Now I'll update the config file with the corrected values. "))
	}
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	toolCallChunk := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index": 0,
							"id":    fmt.Sprintf("call_%d", callNum),
							"type":  "function",
							"function": map[string]interface{}{
								"name":      actionName,
								"arguments": actionArgs,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(toolCallChunk)
	fmt.Fprintf(w, "data: %s\n\n", string(b))
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Printf("  → Sent oscillation-loop response (%s)", actionName)
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 5: Thinking Loop
// The LLM's reasoning/thinking content is highly repetitive.
// Should trigger: Trigram analysis (future Phase 3), but the repeated
// text content will also trigger SimHash similarity in Phase 1.
// ─────────────────────────────────────────────────────────────────────────────

func streamThinkingLoop(w http.ResponseWriter, r *http.Request) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	// Send repetitive thinking content
	thinkingPhrases := []string{
		"I need to check the file. ",
		"Let me look at the configuration. ",
		"I need to check the file. ",
		"Let me look at the configuration. ",
		"I need to check the file. ",
		"Let me look at the configuration. ",
		"I need to check the file. ",
		"Let me look at the configuration again. ",
	}

	for _, phrase := range thinkingPhrases {
		fmt.Fprintf(w, "data: %s\n\n", createReasoningChunk(phrase))
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
	}

	// Then send actual content
	fmt.Fprintf(w, "data: %s\n\n", createContentChunk("The configuration looks correct. "))
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("  → Sent thinking-loop response")
}

// ─────────────────────────────────────────────────────────────────────────────
// Normal Response (baseline)
// ─────────────────────────────────────────────────────────────────────────────

func streamNormalResponse(w http.ResponseWriter, r *http.Request) {
	setupSSE(w)
	flusher := w.(http.Flusher)

	tokens := []string{"Hello! ", "This is ", "a normal ", "response ", "with no ", "looping ", "behavior ", "at all. "}
	for _, token := range tokens {
		fmt.Fprintf(w, "data: %s\n\n", createContentChunk(token))
		flusher.Flush()
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("  → Sent normal response")
}

func writeNonStreamResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"id":      "chatcmpl-loop-mock",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-loop-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": "Hello! This is a normal non-streaming response.",
				},
				"finish_reason": "stop",
			},
		},
	}
	json.NewEncoder(w).Encode(response)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func setupSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}

func extractPrompt(reqBody map[string]interface{}) string {
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
	return prompt
}

func createContentChunk(content string) string {
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
