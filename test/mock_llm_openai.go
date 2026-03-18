//go:build ignore

// Mock LLM Server for OpenAI Format Testing
//
// This server provides comprehensive OpenAI format responses for testing:
// 1. Basic streaming/non-streaming chat completions
// 2. Tool calls (streaming and non-streaming)
// 3. Reasoning content (for thinking models like DeepSeek)
// 4. Multiple tool calls in sequence
// 5. Error scenarios
// 6. Edge cases per OpenAI streaming tool calls spec
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
//	mock-tool-malformed: Tool call with malformed JSON arguments (missing quotes)
//	mock-tool-malformed-stream: Streaming tool call with malformed JSON arguments
//
// Edge case keywords (per OpenAI spec section 9 & 11):
//
//	mock-tool-no-index: Tool call missing index field (should default to 0)
//	mock-tool-no-id: Tool call missing ID field (should be tolerated)
//	mock-tool-interleaved: Interleaved tool calls (index 0, 1, 0, 1 pattern)
//	mock-tool-empty-delta: Chunks with empty delta
//	mock-tool-minimal: Tool call with only index field
//	mock-tool-no-function-name: Tool call without function name
//	mock-tool-partial-json: Tool call with partial JSON arguments
//	mock-tool-large-index: Tool call with large index value (edge case)
//	mock-tool-sparse-index: Tool calls with non-contiguous indices
//	mock-tool-duplicate-id: Multiple tool calls with same ID (edge case)
//	mock-tool-no-type: Tool call without type field
//	mock-tool-chunk-then-empty: Tool call chunks followed by empty deltas
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
	if strings.Contains(prompt, "mock-tool-malformed") {
		// Tool call with malformed JSON arguments (missing quotes, unquoted keys)
		response["choices"] = []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"id":   "call_malformed_123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{location: "San Francisco", unit: "celsius"}`, // Malformed: missing quotes around key
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		}
	} else if strings.Contains(prompt, "mock-tool-single") {
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
	} else if strings.Contains(prompt, "mock-tool-malformed") {
		// Tool call with malformed JSON arguments (missing quotes around keys)
		response["choices"] = []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]interface{}{
						{
							"id":   "call_malformed_123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{location: "San Francisco", unit: "celsius"}`, // Malformed: missing quotes around keys
							},
						},
					},
				},
				"finish_reason": "tool_calls",
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
	// Edge case handlers (per OpenAI spec section 9 & 11)
	case strings.Contains(prompt, "mock-tool-no-index"):
		handleToolCallNoIndex(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-no-id"):
		handleToolCallNoID(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-interleaved"):
		handleToolCallInterleaved(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-empty-delta"):
		handleToolCallEmptyDelta(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-minimal"):
		handleToolCallMinimal(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-no-function-name"):
		handleToolCallNoFunctionName(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-partial-json"):
		handleToolCallPartialJSON(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-large-index"):
		handleToolCallLargeIndex(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-sparse-index"):
		handleToolCallSparseIndex(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-duplicate-id"):
		handleToolCallDuplicateID(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-no-type"):
		handleToolCallNoType(w, flusher, model)
	case strings.Contains(prompt, "mock-tool-chunk-then-empty"):
		handleToolCallChunkThenEmpty(w, flusher, model)
	// Standard handlers
	case strings.Contains(prompt, "mock-tool-malformed-stream"):
		handleStreamingMalformedToolCall(w, flusher, model)
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

func handleStreamingMalformedToolCall(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call with malformed JSON arguments")

	// First, send text chunks
	textTokens := []string{"Let", " me", " check", " the", " weather", "."}
	for _, token := range textTokens {
		chunk := createOpenAIChunkSimple(model, token)
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Send tool call chunks with malformed JSON (missing quotes around keys)
	toolCallID := "call_malformed_stream_123"

	// Chunk 1: Tool call starts with name
	tc1 := createToolCallChunk(model, toolCallID, 0, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Chunk 2: Arguments streaming - MALFORMED JSON (missing quotes around keys)
	// This simulates an LLM that outputs: {location: "San Francisco", unit: "celsius"}
	// instead of: {"location": "San Francisco", "unit": "celsius"}
	malformedArgParts := []string{`{loc`, `ation:`, ` "`, `San`, ` Fran`, `cisco`, `", `, ` unit:`, ` "`, `celsius`, `"}`}
	for _, part := range malformedArgParts {
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
	log.Println("[OpenAI-Mock] Streaming malformed tool call completed")
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

// ============================================================================
// Edge Case Handlers (per OpenAI spec section 9 & 11)
// ============================================================================

// handleToolCallNoIndex simulates a provider that sends tool calls WITHOUT the index field
// Per spec section 11: "Fallback if index missing → assume 0"
func handleToolCallNoIndex(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call WITHOUT index field (Gemini-style)")

	// First chunk: Tool call with ID and name, but NO index
	tc1 := map[string]interface{}{
		// NOTE: index field is intentionally MISSING
		"id":   "call_no_index_123",
		"type": "function",
		"function": map[string]interface{}{
			"name": "get_weather",
		},
	}
	chunk1 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc1})
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Argument chunks - also without index
	argParts := []string{`{"loc`, `ation`, `": "`, `Tokyo`, `"}`}
	for _, part := range argParts {
		tc := map[string]interface{}{
			// NOTE: index field is intentionally MISSING
			"id": "call_no_index_123",
			"function": map[string]interface{}{
				"arguments": part,
			},
		}
		chunk := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk with finish_reason
	final := createToolCallFinalChunkNoIndex(model, "call_no_index_123")
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call without index completed")
}

// handleToolCallNoID simulates a provider that sends tool calls WITHOUT the ID field
// Per spec section 4: "id - Present only in the first chunk (usually)"
// Some providers like Ollama sometimes don't send ID at all
func handleToolCallNoID(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call WITHOUT ID field (Ollama-style)")

	// First chunk: Tool call with index and name, but NO id
	tc1 := map[string]interface{}{
		"index": 0,
		// NOTE: id field is intentionally MISSING
		"type": "function",
		"function": map[string]interface{}{
			"name": "get_weather",
		},
	}
	chunk1 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc1})
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Argument chunks - also without id
	argParts := []string{`{"loc`, `ation`, `": "`, `Paris`, `"}`}
	for _, part := range argParts {
		tc := map[string]interface{}{
			"index": 0,
			// NOTE: id field is intentionally MISSING
			"function": map[string]interface{}{
				"arguments": part,
			},
		}
		chunk := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk with finish_reason (no id)
	final := createToolCallFinalChunkNoID(model, 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call without ID completed")
}

// handleToolCallInterleaved simulates interleaved tool calls
// Per spec section 5: "chunks may interleave like: Chunk A → index 0, Chunk B → index 1, Chunk C → index 0"
func handleToolCallInterleaved(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming interleaved tool calls")

	// Interleaved pattern: 0, 1, 0, 1, 0, 1
	interleavedChunks := []struct {
		index int
		id    string
		name  string
		arg   string
	}{
		// First wave: both tools start
		{0, "call_inter_0", "get_weather", ""},
		{1, "call_inter_1", "get_time", ""},
		// Second wave: arguments interleaved
		{0, "call_inter_0", "", `{"loc`},
		{1, "call_inter_1", "", `{"time`},
		{0, "call_inter_0", "", `ation`},
		{1, "call_inter_1", "", `zone`},
		{0, "call_inter_0", "", `": "`},
		{1, "call_inter_1", "", `": "`},
		{0, "call_inter_0", "", `London`},
		{1, "call_inter_1", "", `UTC`},
		{0, "call_inter_0", "", `"}`},
		{1, "call_inter_1", "", `"}`},
	}

	for i, c := range interleavedChunks {
		var tc map[string]interface{}
		if c.name != "" {
			tc = map[string]interface{}{
				"index": c.index,
				"id":    c.id,
				"type":  "function",
				"function": map[string]interface{}{
					"name": c.name,
				},
			}
		} else {
			tc = map[string]interface{}{
				"index": c.index,
				"id":    c.id,
				"function": map[string]interface{}{
					"arguments": c.arg,
				},
			}
		}
		chunk := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Interleaved chunk %d: index=%d", i, c.index)
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createMultiToolCallFinalChunk(model, []string{"call_inter_0", "call_inter_1"})
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Interleaved tool calls completed")
}

// handleToolCallEmptyDelta simulates chunks with empty deltas
// Per spec section 9: "You may get: { 'delta': {} } - Ignore safely"
func handleToolCallEmptyDelta(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming with empty deltas mixed in")

	// First, send a tool call start
	tc1 := createToolCallChunk(model, "call_empty_123", 0, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Send empty delta chunks (simulating heartbeat or padding)
	for i := 0; i < 3; i++ {
		emptyChunk := createEmptyDeltaChunk(model)
		fmt.Fprintf(w, "data: %s\n\n", emptyChunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Empty delta %d", i)
		time.Sleep(30 * time.Millisecond)
	}

	// Continue with arguments
	argParts := []string{`{"ci`, `ty`, `": "`, `Berlin`, `"}`}
	for _, part := range argParts {
		tc := createToolCallChunk(model, "call_empty_123", 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// More empty deltas
	for i := 0; i < 2; i++ {
		emptyChunk := createEmptyDeltaChunk(model)
		fmt.Fprintf(w, "data: %s\n\n", emptyChunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_empty_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call with empty deltas completed")
}

// handleToolCallMinimal simulates a tool call with ONLY the index field
// Per spec section 9: "A chunk may contain ONLY: { 'index': 0 }"
func handleToolCallMinimal(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming minimal tool call (index only)")

	// First chunk: ONLY index field (no id, no type, no function)
	tc1 := map[string]interface{}{
		"index": 0,
		// NOTE: all other fields are intentionally MISSING
	}
	chunk1 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc1})
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Then send a more complete chunk
	tc2 := map[string]interface{}{
		"index": 0,
		"id":    "call_minimal_123",
		"type":  "function",
		"function": map[string]interface{}{
			"name": "get_weather",
		},
	}
	chunk2 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc2})
	fmt.Fprintf(w, "data: %s\n\n", chunk2)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments
	argParts := []string{`{"ci`, `ty`, `": "`, `Rome`, `"}`}
	for _, part := range argParts {
		tc := createToolCallChunk(model, "call_minimal_123", 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_minimal_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Minimal tool call completed")
}

// handleToolCallNoFunctionName simulates a tool call without function name
// Edge case: function.name should be present but sometimes isn't
func handleToolCallNoFunctionName(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call WITHOUT function name")

	// First chunk: Tool call with id and type, but NO function.name
	tc1 := map[string]interface{}{
		"index": 0,
		"id":    "call_no_name_123",
		"type":  "function",
		"function": map[string]interface{}{
			// NOTE: name field is intentionally MISSING
			"arguments": "",
		},
	}
	chunk1 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc1})
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments without name
	argParts := []string{`{"qu`, `ery`, `": "`, `test`, `"}`}
	for _, part := range argParts {
		tc := map[string]interface{}{
			"index": 0,
			"function": map[string]interface{}{
				"arguments": part,
			},
		}
		chunk := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_no_name_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call without function name completed")
}

// handleToolCallPartialJSON simulates tool call with partial JSON that's not valid until the end
// Per spec section 9: "Arguments not valid JSON until the end - Do NOT parse early"
func handleToolCallPartialJSON(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call with partial JSON arguments")

	// First chunk
	tc1 := createToolCallChunk(model, "call_partial_123", 0, "search", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Send very granular partial JSON (each chunk is NOT valid JSON on its own)
	// This tests that the proxy doesn't try to parse JSON mid-stream
	partialParts := []string{
		`{`, // Just opening brace
		`\n`, // Newline (some LLMs add whitespace)
		`  "`, // Start of key
		`query`, // Key name
		`"`, // End of key
		`:`, // Colon
		` `, // Space
		`"`, // Start of value
		`What`, // Partial value
		` is`, // More partial value
		` the`, // More partial value
		` wea`, // More partial value
		`ther`, // More partial value
		`?`, // End of value
		`"`, // Closing quote
		`\n`, // Newline
		`}`, // Closing brace
	}

	for i, part := range partialParts {
		tc := createToolCallChunk(model, "call_partial_123", 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Partial JSON chunk %d: %q", i, part)
		time.Sleep(20 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_partial_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Partial JSON tool call completed")
}

// handleToolCallLargeIndex simulates tool call with large index value
// Edge case: test index bounds handling
func handleToolCallLargeIndex(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call with large index (50)")

	// Use index 50 (not extreme, but tests if proxy handles non-zero indices correctly)
	tc1 := createToolCallChunk(model, "call_large_123", 50, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments
	argParts := []string{`{"ci`, `ty`, `": "`, `Sydney`, `"}`}
	for _, part := range argParts {
		tc := createToolCallChunk(model, "call_large_123", 50, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk with same index
	final := createToolCallFinalChunk(model, "call_large_123", 50)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Large index tool call completed")
}

// handleToolCallSparseIndex simulates tool calls with non-contiguous indices
// Edge case: indices 0, 5, 10 (not 0, 1, 2)
func handleToolCallSparseIndex(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool calls with sparse indices (0, 5, 10)")

	// Three tool calls with sparse indices
	toolCalls := []struct {
		index int
		id    string
		name  string
		args  []string
	}{
		{0, "call_sparse_0", "get_weather", []string{`{"loc":"NYC"}`}},
		{5, "call_sparse_5", "get_time", []string{`{"tz":"EST"}`}},
		{10, "call_sparse_10", "search", []string{`{"q":"test"}`}},
	}

	for _, tc := range toolCalls {
		// Start chunk
		chunk := createToolCallChunk(model, tc.id, tc.index, tc.name, "")
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)

		// Argument chunks
		for _, arg := range tc.args {
			chunk := createToolCallChunk(model, tc.id, tc.index, "", arg)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}

	// Final chunk with all tool calls
	final := createMultiToolCallFinalChunkWithIndices(model, []struct {
		id    string
		index int
	}{
		{"call_sparse_0", 0},
		{"call_sparse_5", 5},
		{"call_sparse_10", 10},
	})
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Sparse index tool calls completed")
}

// handleToolCallDuplicateID simulates multiple tool calls with the same ID
// Edge case: should be detected and logged
func handleToolCallDuplicateID(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool calls with DUPLICATE IDs")

	// First tool call with ID
	tc1 := createToolCallChunk(model, "call_duplicate", 0, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments for first
	argParts1 := []string{`{"loc":"NYC"}`}
	for _, part := range argParts1 {
		tc := createToolCallChunk(model, "call_duplicate", 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Second tool call with SAME ID but different index (edge case!)
	tc2 := createToolCallChunk(model, "call_duplicate", 1, "get_time", "")
	fmt.Fprintf(w, "data: %s\n\n", tc2)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments for second
	argParts2 := []string{`{"tz":"UTC"}`}
	for _, part := range argParts2 {
		tc := createToolCallChunk(model, "call_duplicate", 1, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createMultiToolCallFinalChunk(model, []string{"call_duplicate", "call_duplicate"})
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Duplicate ID tool calls completed")
}

// handleToolCallNoType simulates tool call without type field
// Per spec section 4: "type - Always 'function' - Often only appears in first chunk"
func handleToolCallNoType(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call WITHOUT type field")

	// First chunk: Tool call without type field
	tc1 := map[string]interface{}{
		"index": 0,
		"id":    "call_no_type_123",
		// NOTE: type field is intentionally MISSING
		"function": map[string]interface{}{
			"name": "get_weather",
		},
	}
	chunk1 := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc1})
	fmt.Fprintf(w, "data: %s\n\n", chunk1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	// Arguments
	argParts := []string{`{"ci`, `ty`, `": "`, `Moscow`, `"}`}
	for _, part := range argParts {
		tc := map[string]interface{}{
			"index": 0,
			"function": map[string]interface{}{
				"arguments": part,
			},
		}
		chunk := createChunkWithCustomToolCalls(model, []map[string]interface{}{tc})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_no_type_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call without type completed")
}

// handleToolCallChunkThenEmpty simulates tool call chunks followed by empty deltas
// Tests that proxy handles the transition correctly
func handleToolCallChunkThenEmpty(w http.ResponseWriter, flusher http.Flusher, model string) {
	log.Println("[OpenAI-Mock] Streaming tool call then empty deltas")

	// Tool call chunks
	tc1 := createToolCallChunk(model, "call_then_empty_123", 0, "get_weather", "")
	fmt.Fprintf(w, "data: %s\n\n", tc1)
	flusher.Flush()
	time.Sleep(50 * time.Millisecond)

	argParts := []string{`{"loc":"Tokyo"}`}
	for _, part := range argParts {
		tc := createToolCallChunk(model, "call_then_empty_123", 0, "", part)
		fmt.Fprintf(w, "data: %s\n\n", tc)
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
	}

	// Send many empty deltas after tool call (simulating keepalive)
	for i := 0; i < 5; i++ {
		emptyChunk := createEmptyDeltaChunk(model)
		fmt.Fprintf(w, "data: %s\n\n", emptyChunk)
		flusher.Flush()
		log.Printf("[OpenAI-Mock] Post-tool empty delta %d", i)
		time.Sleep(30 * time.Millisecond)
	}

	// Final chunk
	final := createToolCallFinalChunk(model, "call_then_empty_123", 0)
	fmt.Fprintf(w, "data: %s\n\n", final)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Println("[OpenAI-Mock] Tool call then empty deltas completed")
}

// ============================================================================
// Additional Helper Functions for Edge Cases
// ============================================================================

// createChunkWithCustomToolCalls creates a chunk with custom tool call objects
func createChunkWithCustomToolCalls(model string, toolCalls []map[string]interface{}) string {
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
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// createEmptyDeltaChunk creates a chunk with an empty delta
func createEmptyDeltaChunk(model string) string {
	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{}, // Empty delta
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// createToolCallFinalChunkNoIndex creates a final chunk without index field
func createToolCallFinalChunkNoIndex(model, toolCallID string) string {
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
							// NOTE: index intentionally missing
							"id":   toolCallID,
							"type": "function",
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

// createToolCallFinalChunkNoID creates a final chunk without ID field
func createToolCallFinalChunkNoID(model string, index int) string {
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
							// NOTE: id intentionally missing
							"type": "function",
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

// createMultiToolCallFinalChunkWithIndices creates a final chunk with specific indices
func createMultiToolCallFinalChunkWithIndices(model string, toolCalls []struct {
	id    string
	index int
}) string {
	var tcs []interface{}
	for _, tc := range toolCalls {
		tcs = append(tcs, map[string]interface{}{
			"index": tc.index,
			"id":    tc.id,
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
					"tool_calls": tcs,
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
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
