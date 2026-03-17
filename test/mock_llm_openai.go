//go:build ignore

// Mock LLM Server for OpenAI Format Testing
//
// This server provides comprehensive OpenAI format responses for testing:
// 1. Basic streaming/non-streaming chat completions
// 2. Tool calls (streaming and non-streaming)
// 3. Reasoning content (for thinking models like DeepSeek)
// 4. Multiple tool calls in sequence
// 5. Error scenarios
//
// Usage:
//
//	go run test/mock_llm_openai.go -port=4002
//
// Trigger keywords in prompt:
//
//	mock-tool-single: Single tool call
//	mock-tool-multi: Multiple tool calls
//	mock-tool-stream: Streaming tool call
//	mock-reasoning: Response with reasoning_content
//	mock-reasoning-tool: Reasoning followed by tool call
//	mock-error-500: Return 500 error
//	mock-error-timeout: Timeout (sleep)
//	mock-non-stream: Force non-streaming response
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
	port := flag.String("port", "4002", "Port to listen on")
	flag.Parse()

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Println("[OpenAI-Mock] Received request")

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

		model := "mock-model"
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

		log.Printf("[OpenAI-Mock] Model=%s, Stream=%v, Prompt=%s", model, isStream, truncate(prompt, 50))

		// Handle error scenarios first
		if strings.Contains(prompt, "mock-error-500") {
			log.Println("[OpenAI-Mock] Returning 500 error")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Simulated internal server error",
					"type":    "internal_error",
					"code":    500,
				},
			})
			return
		}

		if strings.Contains(prompt, "mock-error-timeout") {
			log.Println("[OpenAI-Mock] Simulating timeout (sleeping 60s)")
			time.Sleep(60 * time.Second)
			return
		}

		// Force non-streaming if requested
		if strings.Contains(prompt, "mock-non-stream") {
			isStream = false
		}

		if !isStream {
			handleNonStreamOpenAI(w, model, prompt)
			return
		}

		handleStreamOpenAI(w, r, model, prompt)
	})

	log.Printf("[OpenAI-Mock] Server listening on :%s", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleNonStreamOpenAI(w http.ResponseWriter, model, prompt string) {
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
		"usage": map[string]interface{}{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	}

	// Handle tool call scenarios
	if strings.Contains(prompt, "mock-tool-single") {
		response["choices"] = []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"id":   "call_abc123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{"location": "San Francisco", "unit": "celsius"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		}
	} else if strings.Contains(prompt, "mock-tool-multi") {
		response["choices"] = []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"id":   "call_weather",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{"location": "Tokyo"}`,
							},
						},
						{
							"id":   "call_time",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_time",
								"arguments": `{"timezone": "Asia/Tokyo"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		}
	} else if strings.Contains(prompt, "mock-reasoning") {
		response["choices"] = []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           "The answer is 42.",
					"reasoning_content": "Let me think about this... The user is asking about the meaning of life. According to Douglas Adams, it's 42.",
				},
				"finish_reason": "stop",
			},
		}
	}

	json.NewEncoder(w).Encode(response)
	log.Println("[OpenAI-Mock] Non-streaming response sent")
}

func handleStreamOpenAI(w http.ResponseWriter, r *http.Request, model, prompt string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Send initial connection comment
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	switch {
	case strings.Contains(prompt, "mock-tool-stream"):
		handleStreamingToolCall(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-multi-stream"):
		handleStreamingMultiToolCall(w, flusher, model)
	case strings.Contains(prompt, "mock-reasoning-tool"):
		handleReasoningWithTool(w, flusher, model)
	case strings.Contains(prompt, "mock-reasoning"):
		handleReasoningStream(w, flusher, model)
	default:
		handleNormalStream(w, flusher, model)
	}
}

func handleNormalStream(w http.ResponseWriter, flusher http.Flusher, model string) {
	tokens := []string{"Hello", "!", " This", " is", " a", " normal", " streaming", " response", " from", " ", model, "."}

	for i, token := range tokens {
		chunk := createOpenAIChunkSimple(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Sent token %d: '%s'", i, token)
		time.Sleep(50 * time.Millisecond)
	}

	// Send final chunk with finish_reason
	finalChunk := createOpenAIFinalChunk(model)
	fmt.Fprintf(w, "data: %s\n\n", finalChunk)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Normal stream completed")
}

func handleStreamingToolCall(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming single tool call")

	// First, send text chunks
	textTokens := []string{"Let", " me", " check", " the", " weather", " for", " you", "."}
	for _, token := range textTokens {
		chunk := createOpenAIChunkSimple(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Send tool call chunks
	toolCallID := "call_stream_123"

	// Chunk 1: Tool call starts with name
	tc1 := createToolCallChunk(model, toolCallID, 0, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Chunk 2: Arguments streaming
	argParts := []string{`{"loc`, `ation`, `": "`, `San`, ` Fran`, `cisco`, `", `, `"unit`, `": "`, `celsius`, `"}`}
	for _, part := range argParts {
		tc := createToolCallChunk(model, toolCallID, 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk with finish_reason
	final := createToolCallFinalChunk(model, toolCallID, 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Streaming tool call completed")
}

func handleStreamingMultiToolCall(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming multiple tool calls")

	// Tool call 1: get_weather
	tc1ID := "call_multi_weather"
	tc1Chunks := []struct {
		name string
		arg  string
	}{
		{"get_weather", ""},
		{"", `{"loc`},
		{"", `ation`},
		{"", `": "`},
		{"", `Tokyo`},
		{"", `"}`},
	}

	for i, c := range tc1Chunks {
		tc := createToolCallChunk(model, tc1ID, 0, c.name, c.arg)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Tool call 1 chunk %d", i)
		time.Sleep(30 * time.Millisecond)
	}

	// Tool call 2: get_time
	tc2ID := "call_multi_time"
	tc2Chunks := []struct {
		name string
		arg  string
	}{
		{"get_time", ""},
		{"", `{"time`},
		{"", `zone`},
		{"", `": "`},
		{"", `Asia/Tokyo`},
		{"", `"}`},
	}

	for i, c := range tc2Chunks {
		tc := createToolCallChunk(model, tc2ID, 1, c.name, c.arg)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Tool call 2 chunk %d", i)
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createMultiToolCallFinalChunk(model, []string{tc1ID, tc2ID})
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Streaming multi tool call completed")
}

func handleReasoningStream(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming with reasoning content")

	// Send reasoning tokens first
	reasoningTokens := []string{"Hmm", ", ", "let", " me", " think", "..."}
	for i, token := range reasoningTokens {
		chunk := createReasoningChunk(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Reasoning token %d: '%s'", i, token)
		time.Sleep(50 * time.Millisecond)
	}

	// Then send normal content
	contentTokens := []string{"The", " answer", " is", " 42", "."}
	for i, token := range contentTokens {
		chunk := createOpenAIChunkSimple(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Content token %d: '%s'", i, token)
		time.Sleep(50 * time.Millisecond)
	}

	// Final chunk
	finalChunk := createOpenAIFinalChunk(model)
	fmt.Fprintf(w, "data: %s\n\n", finalChunk)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Reasoning stream completed")
}

func handleReasoningWithTool(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming reasoning then tool call")

	// Send reasoning tokens
	reasoningTokens := []string{"I", " need", " to", " check", " the", " weather", "..."}
	for i, token := range reasoningTokens {
		chunk := createReasoningChunk(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Reasoning token %d: '%s'", i, token)
		time.Sleep(50 * time.Millisecond)
	}

	// Then send tool call
	tcID := "call_reasoning_tool"
	tcChunks := []struct {
		name string
		arg  string
	}{
		{"get_weather", ""},
		{"", `{"loc`},
		{"", `ation`},
		{"", `": "`},
		{"", `Paris`},
		{"", `"}`},
	}

	for i, c := range tcChunks {
		tc := createToolCallChunk(model, tcID, 0, c.name, c.arg)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Tool chunk %d", i)
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, tcID, 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Reasoning + tool stream completed")
}

// Helper functions to create OpenAI format chunks

func createOpenAIChunkSimple(model, content string) string {
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

func createReasoningChunk(model, content string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"reasoning_content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createToolCallChunk(model, toolCallID string, index int, name, arguments string) string {
	delta := map[string]interface{}{}

	if name != "" {
		// First chunk with tool call ID and name
		delta["tool_calls"] = []interface{}{
			map[string]interface{}{
				"index": index,
				"id":    toolCallID,
				"type":  "function",
				"function": map[string]interface{}{
					"name": name,
				},
			},
		}
	} else {
		// Subsequent chunks with arguments
		delta["tool_calls"] = []interface{}{
			map[string]interface{}{
				"index": index,
				"function": map[string]interface{}{
					"arguments": arguments,
				},
			},
		}
	}

	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": delta,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createToolCallFinalChunk(model, toolCallID string, index int) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index": index,
							"id":    toolCallID,
							"type":  "function",
							"function": map[string]interface{}{
								"name": "",
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createMultiToolCallFinalChunk(model string, toolCallIDs []string) string {
	var toolCalls []interface{}
	for i, id := range toolCallIDs {
		toolCalls = append(toolCalls, map[string]interface{}{
			"index": i,
			"id":    id,
			"type":  "function",
			"function": map[string]interface{}{
				"name": "",
			},
		})
	}

	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": toolCalls,
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func createOpenAIFinalChunk(model string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": "stop",
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
