//go:build ignore

// Mock LLM Server for MiniMax-Style Streaming
//
// This server mimics MiniMax API responses for testing:
// 1. Thinking content (<think>/</think>)
// 2. 3 tool calls in a single request
// 3. Chunked arguments
// 4. No [DONE] marker (MiniMax doesn't send it)
//
// Usage:
//
//	go run test/mock_llm_minimax.go -port=4003
//
// Trigger keywords in prompt:
//
//	mock-minimax-3tools: Request 3 tool calls (default)
//	mock-minimax-thinking: Response with thinking content
//	mock-minimax-chunked: Chunked tool call arguments
//	mock-minimax-complete: Complete JSON in one chunk
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

func main() {
	port := flag.String("port", "4003", "Port to listen on")
	flag.Parse()

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Println("[MiniMax-Mock] Received request")

		// Read the request body
		reqBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		var reqBody map[string]interface{}
		if err := json.Unmarshal(reqBodyBytes, &reqBody); err != nil {
			http.Error(w, "Failed to parse request body as JSON", http.StatusBadRequest)
			return
		}

		// Extract parameters
		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		model := "MiniMax-M2.7"
		if m, ok := reqBody["model"].(string); ok {
			model = m
		}

		// Extract prompt
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

		log.Printf("[MiniMax-Mock] Model=%s, Stream=%v, Prompt=%s", model, isStream, truncate(prompt, 50))

		if !isStream {
			handleNonStreamMiniMax(w, model, prompt)
			return
		}

		handleStreamMiniMax(w, r, model, prompt)
	})

	log.Printf("[MiniMax-Mock] Server listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleNonStreamMiniMax(w http.ResponseWriter, model, prompt string) {
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
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Non-streaming response from " + model,
				},
				"finish_reason": "stop",
			},
		},
	}

	json.NewEncoder(w).Encode(response)
	log.Println("[MiniMax-Mock] Non-streaming response sent")
}

func handleStreamMiniMax(w http.ResponseWriter, r *http.Request, model, prompt string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Determine which response to send
	switch {
	case strings.Contains(prompt, "mock-minimax-chunked"):
		handleMiniMaxChunkedToolCalls(w, flusher, model)
	case strings.Contains(prompt, "mock-minimax-complete"):
		handleMiniMaxCompleteToolCalls(w, flusher, model)
	case strings.Contains(prompt, "mock-minimax-thinking"):
		handleMiniMaxThinking(w, flusher, model)
	case strings.Contains(prompt, "mock-minimax-3tools"):
		handleMiniMax3ToolCalls(w, flusher, model)
	default:
		handleMiniMax3ToolCalls(w, flusher, model)
	}
}

// handleMiniMax3ToolCalls sends 3 tool calls similar to real MiniMax
func handleMiniMax3ToolCalls(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[MiniMax-Mock] Sending 3 tool calls (MiniMax-style)")

	// Chunk 1: Thinking content starts
	chunk1 := createChunkWithContent(model, "<think>\nThe user")
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 2: More thinking
	chunk2 := createChunkWithContent(model, " wants me to perform three tasks:")
	fmt.Fprintf(w, "data: %s\n\n", chunk2)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 3: Thinking continues
	chunk3 := createChunkWithContent(model, " I'll call three tools at once.")
	fmt.Fprintf(w, "data: %s\n\n", chunk3)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 4: First tool call - get_weather (index 0)
	chunk4 := createToolCallStartChunk(model, "call_func_1", 0, "get_weather")
	fmt.Fprintf(w, "data: %s\n\n", chunk4)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 5: Arguments for get_weather - partial JSON
	chunk5 := createToolCallArgsChunk(model, 0, `{"location": "Tokyo`)
	fmt.Fprintf(w, "data: %s\n\n", chunk5)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 6: Complete get_weather arguments
	chunk6 := createToolCallArgsChunk(model, 0, `"}`)
	fmt.Fprintf(w, "data: %s\n\n", chunk6)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 7: Second tool call - search_code (index 1)
	chunk7 := createToolCallStartChunk(model, "call_func_2", 1, "search_code")
	fmt.Fprintf(w, "data: %s\n\n", chunk7)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 8: Arguments for search_code - complete JSON
	chunk8 := createToolCallArgsChunk(model, 1, `{"query": "authentication", "language": "python"}`)
	fmt.Fprintf(w, "data: %s\n\n", chunk8)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 9: Third tool call - calculate (index 2)
	chunk9 := createToolCallStartChunk(model, "call_func_3", 2, "calculate")
	fmt.Fprintf(w, "data: %s\n\n", chunk9)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 10: Arguments for calculate
	chunk10 := createToolCallArgsChunk(model, 2, `{"expression": "123 * 456"}`)
	fmt.Fprintf(w, "data: %s\n\n", chunk10)
	flusher.Flush()
	time.Sleep(100 * time.Millisecond)

	// Chunk 11: finish_reason with thinking
	chunk11 := createFinishReasonChunk(model, "tool_calls", "<think>\n")
	fmt.Fprintf(w, "data: %s\n\n", chunk11)
	flusher.Flush()

	// Chunk 12: Another finish_reason (MiniMax sends duplicate)
	chunk12 := createFinishReasonChunk(model, "tool_calls", "")
	fmt.Fprintf(w, "data: %s\n\n", chunk12)
	flusher.Flush()

	// NOTE: MiniMax does NOT send [DONE] marker!
	// The stream just ends after finish_reason
	log.Println("[MiniMax-Mock] 3-tool-call stream completed (no [DONE] marker)")
}

// handleMiniMaxChunkedToolCalls sends tool calls with very chunked arguments
func handleMiniMaxChunkedToolCalls(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[MiniMax-Mock] Sending chunked tool calls")

	// Content
	chunk1 := createChunkWithContent(model, "I'll get the weather.")
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Tool call with very granular arguments
	toolCallID := "call_chunk_1"
	argParts := []string{"{\"location\": ", "\"Tokyo\", ", "\"unit\": ", "\"celsius\"}"}

	// First chunk with tool call
	chunkT := createToolCallStartChunk(model, toolCallID, 0, "get_weather")
	fmt.Fprintf(w, "data: %s\n\n", chunkT)
	flusher.Flush()

	for _, part := range argParts {
		chunkA := createToolCallArgsChunk(model, 0, part)
		fmt.Fprintf(w, "data: %s\n\n", chunkA)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	chunkF := createFinishReasonChunk(model, "tool_calls", "")
	fmt.Fprintf(w, "data: %s\n\n", chunkF)
	flusher.Flush()

	log.Println("[MiniMax-Mock] Chunked tool call completed")
}

// handleMiniMaxCompleteToolCalls sends complete JSON in one chunk
func handleMiniMaxCompleteToolCalls(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[MiniMax-Mock] Sending complete JSON tool calls")

	// Content
	chunk1 := createChunkWithContent(model, "Here are the results.")
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Tool call with complete JSON in one chunk
	chunkT := createCompleteToolCallChunk(model, "call_complete_1", 0, "get_weather", `{"location": "Tokyo", "unit": "celsius"}`)
	fmt.Fprintf(w, "data: %s\n\n", chunkT)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Final chunk
	chunkF := createFinishReasonChunk(model, "tool_calls", "")
	fmt.Fprintf(w, "data: %s\n\n", chunkF)
	flusher.Flush()

	log.Println("[MiniMax-Mock] Complete JSON tool call completed")
}

// handleMiniMaxThinking sends thinking content
func handleMiniMaxThinking(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[MiniMax-Mock] Sending thinking content")

	// Thinking start
	chunk1 := createChunkWithContent(model, "<think>\nLet me think about this.")
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// More thinking
	chunk2 := createChunkWithContent(model, " The user is asking for information.")
	fmt.Fprintf(w, "data: %s\n\n", chunk2)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Thinking end
	chunk3 := createChunkWithContent(model, "\n</think>\nHere is the answer.")
	fmt.Fprintf(w, "data: %s\n\n", chunk3)
	flusher.Flush()

	// Final chunk
	chunkF := createFinishReasonChunk(model, "stop", "")
	fmt.Fprintf(w, "data: %s\n\n", chunkF)
	flusher.Flush()

	log.Println("[MiniMax-Mock] Thinking content completed")
}

// Helper functions

func createChunkWithContent(model, content string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"content": content,
					"role":   "assistant",
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createToolCallStartChunk(model, toolCallID string, index int, name string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"role": "assistant",
					"tool_calls": []map[string]interface{}{
						{
							"id":    toolCallID,
							"type":  "function",
							"index": index,
							"function": map[string]interface{}{
								"name": name,
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createToolCallArgsChunk(model string, index int, args string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index": index,
							"function": map[string]interface{}{
								"arguments": args,
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createCompleteToolCallChunk(model, toolCallID string, index int, name, args string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"role": "assistant",
					"tool_calls": []map[string]interface{}{
						{
							"id":    toolCallID,
							"type":  "function",
							"index": index,
							"function": map[string]interface{}{
								"name":      name,
								"arguments": args,
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createFinishReasonChunk(model, finishReason, content string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":        0,
				"finish_reason": finishReason,
				"delta": map[string]interface{}{
					"content": content,
					"role":   "assistant",
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
