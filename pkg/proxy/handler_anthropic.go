package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/google/uuid"
)

// Debug mode for Anthropic endpoint
var debugAnthropic = os.Getenv("DEBUG_ANTHROPIC") == "1"

func debugLog(format string, args ...interface{}) {
	if debugAnthropic {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Messages API Handler
// ─────────────────────────────────────────────────────────────────────────────

// HandleAnthropicMessages handles requests to the /v1/messages endpoint.
// It translates Anthropic Messages API requests to OpenAI Chat Completions format,
// proxies to upstream, and translates responses back to Anthropic format.
func (h *Handler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Close body when done (including error paths)
	defer r.Body.Close()

	// Parse Anthropic request
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		h.sendAnthropicError(w, "invalid_request_error", "Failed to read request body", http.StatusBadRequest)
		return
	}

	debugLog("=== INCOMING ANTHROPIC REQUEST ===")
	debugLog("Request Body: %s", string(bodyBytes))
	debugLog("Headers: %v", r.Header)

	var anthropicReq translator.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		h.sendAnthropicError(w, "invalid_request_error", "Invalid JSON body", http.StatusBadRequest)
		return
	}

	debugLog("Model: %s", anthropicReq.Model)
	debugLog("Stream: %v", anthropicReq.Stream)
	debugLog("Messages Count: %d", len(anthropicReq.Messages))
	debugLog("MaxTokens: %d", anthropicReq.MaxTokens)
	debugLog("System: %v", anthropicReq.System)

	// Validate request
	if err := validateAnthropicRequest(&anthropicReq); err != nil {
		h.sendAnthropicError(w, "invalid_request_error", err.Error(), http.StatusBadRequest)
		return
	}

	// Get config snapshot
	conf := h.config.Clone()

	// Build target URL for OpenAI upstream
	targetURL, err := url.JoinPath(conf.UpstreamURL, "/v1/chat/completions")
	if err != nil {
		h.sendAnthropicError(w, "api_error", "Invalid upstream URL configuration", http.StatusInternalServerError)
		return
	}

	// Translate to OpenAI format
	modelMapping := getModelMappingConfig(conf.ModelsConfig)
	openaiReq := translator.TranslateRequest(&anthropicReq, modelMapping)
	openaiBodyBytes, err := json.Marshal(openaiReq)
	if err != nil {
		h.sendAnthropicError(w, "api_error", "Failed to translate request", http.StatusInternalServerError)
		return
	}

	debugLog("=== TRANSLATED OPENAI REQUEST ===")
	debugLog("OpenAI Body: %s", string(openaiBodyBytes))

	// Create request log
	reqID := uuid.New().String()
	startTime := time.Now()
	storeMessages := convertAnthropicMessagesToStore(anthropicReq.Messages)

	// Add system message if present (Anthropic has System as separate field)
	if anthropicReq.System != nil {
		systemContent := translator.TranslateSystem(anthropicReq.System)
		if systemContent != "" {
			storeMessages = append([]store.Message{{Role: "system", Content: systemContent}}, storeMessages...)
		}
	}

	isStream := anthropicReq.Stream

	// Extract app tag from x-proxy-app header for request grouping
	appTag := r.Header.Get("x-proxy-app")

	// Safely extract original model name
	originalModel, _ := openaiReq["model"].(string)
	if originalModel == "" {
		originalModel = anthropicReq.Model
	}

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         anthropicReq.Model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		IsStream:      isStream,
		Parameters: map[string]interface{}{
			"max_tokens": anthropicReq.MaxTokens,
			"endpoint":   "anthropic",
		},
		AppTag: appTag,
	}
	h.store.Add(reqLog)

	h.publishEvent("request_started", map[string]interface{}{"id": reqID})

	// Build model list for fallback
	modelList := buildModelList(anthropicReq.Model, conf.ModelsConfig)

	// Create anthropic request context
	arc := &anthropicRequestContext{
		conf:            conf,
		targetURL:       targetURL,
		reqID:           reqID,
		startTime:       startTime,
		reqLog:          reqLog,
		modelList:       modelList,
		anthropicReq:    &anthropicReq,
		openaiBody:      openaiBodyBytes,
		isStream:        isStream,
		originalModel:   anthropicReq.Model,
		baseCtx:         r.Context(),
		method:          r.Method,
		originalHeaders: r.Header,
	}

	// Outer loop: iterate through models (original + fallbacks)
	for modelIndex, currentModel := range arc.modelList {
		if modelIndex > 0 {
			log.Printf("Attempting fallback model: %s (index %d)", currentModel, modelIndex)
		}

		if arc.baseCtx.Err() != nil {
			log.Printf("Client disconnected, failing request")
			break
		}

		// Update model in translated request
		openaiReq["model"] = currentModel
		arc.openaiBody, _ = json.Marshal(openaiReq)

		success := h.attemptAnthropicModel(w, arc, modelIndex, currentModel)
		if success {
			return
		}

		arc.reqLog.Status = "failed"
		arc.reqLog.Error = "Model failed"
	}

	// All models failed
	log.Printf("All models failed for Anthropic request")
	h.publishEvent("all_models_failed", map[string]interface{}{"id": arc.reqID})

	if !arc.headersSent {
		// Use stored error if available, otherwise send generic error
		if arc.lastError != nil && arc.lastStatusCode > 0 {
			translatedError, _ := translator.TranslateError(arc.lastError, arc.lastStatusCode)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(arc.lastStatusCode)
			w.Write(translatedError)
		} else {
			h.sendAnthropicError(w, "api_error", "All models failed after retries", http.StatusBadGateway)
		}
	} else {
		// Headers already sent (streaming) - send SSE error event
		h.sendAnthropicSSEError(w, "api_error", "All models failed after retries")
	}
}

// attemptAnthropicModel attempts a single model request
func (h *Handler) attemptAnthropicModel(w http.ResponseWriter, arc *anthropicRequestContext, modelIndex int, currentModel string) bool {
	// Check if this model uses internal upstream
	modelConfig := arc.conf.ModelsConfig.GetModel(currentModel)
	isInternal := modelConfig != nil && modelConfig.Internal

	if arc.baseCtx.Err() != nil {
		return true // Client disconnected
	}

	var success bool
	if isInternal {
		success = h.doAnthropicInternalRequest(w, arc, modelConfig)
	} else {
		success = h.doAnthropicRequest(w, arc, currentModel)
	}

	return success
}

// doAnthropicRequest performs a single upstream request
func (h *Handler) doAnthropicRequest(w http.ResponseWriter, arc *anthropicRequestContext, currentModel string) bool {
	// Create HTTP request
	req, err := http.NewRequestWithContext(arc.baseCtx, arc.method, arc.targetURL, bytes.NewReader(arc.openaiBody))
	if err != nil {
		log.Printf("Failed to create Anthropic upstream request: %v", err)
		return false
	}

	// Copy headers (translate auth)
	copyAnthropicHeaders(req, arc.originalHeaders)

	// If UpstreamCredentialID is configured, resolve the credential and set auth header
	// This allows the proxy to authenticate with external upstream providers
	// using a different token than what the client provided
	if arc.conf.UpstreamCredentialID != "" {
		// Remove all auth headers first to avoid conflicts
		req.Header.Del("Authorization")
		req.Header.Del("X-API-Key")
		req.Header.Del("x-api-key")
		req.Header.Del("api-key")

		// Resolve credential
		cred := arc.conf.ModelsConfig.GetCredential(arc.conf.UpstreamCredentialID)
		if cred != nil {
			apiKey := cred.ResolveAPIKey()
			if apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}
	}

	// Send request
	resp, err := h.client.Do(req)
	if err != nil {
		// Per Go's http.Client.Do docs: even on error, resp.Body may be non-nil and must be closed
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("Anthropic upstream request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	// Handle non-OK status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Anthropic upstream returned %d: %s", resp.StatusCode, string(bodyBytes))
		debugLog("=== UPSTREAM ERROR RESPONSE ===")
		debugLog("Status: %d", resp.StatusCode)
		debugLog("Body: %s", string(bodyBytes))
		// Don't send error response here - let the retry loop handle it
		// Store the error for potential use if all retries fail
		arc.lastError = bodyBytes
		arc.lastStatusCode = resp.StatusCode
		return false
	}

	// Handle response
	if arc.isStream {
		return h.handleAnthropicStreamResponse(w, resp, arc)
	}
	return h.handleAnthropicNonStreamResponse(w, resp, arc)
}

// flushingResponseRecorder wraps httptest.ResponseRecorder to implement http.Flusher
type flushingResponseRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushingResponseRecorder) Flush() {
	// No-op: the ResponseRecorder already captures all written data
}

// doAnthropicInternalRequest handles requests for internal models (direct provider calls)
// It uses the InternalHandler to call the provider directly, then translates the response
// from OpenAI format to Anthropic format.
func (h *Handler) doAnthropicInternalRequest(w http.ResponseWriter, arc *anthropicRequestContext, modelConfig *models.ModelConfig) bool {
	// Parse the OpenAI request body
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(arc.openaiBody, &openaiReq); err != nil {
		log.Printf("Failed to parse OpenAI request body: %v", err)
		return false
	}

	// Create a response recorder that supports flushing (for streaming)
	recorder := &flushingResponseRecorder{httptest.NewRecorder()}

	log.Printf("[DEBUG ANTHROPIC] Creating InternalHandler for model: %s", modelConfig.ID)

	// Use InternalHandler to make the request
	internalHandler := NewInternalHandler(modelConfig, arc.conf.ModelsConfig)
	err := internalHandler.HandleRequest(arc.baseCtx, openaiReq, recorder, arc.isStream)
	if err != nil {
		log.Printf("[DEBUG ANTHROPIC] Internal request failed: %v", err)
		arc.lastError = []byte(err.Error())
		arc.lastStatusCode = http.StatusBadGateway
		return false
	}

	// Check response status
	if recorder.Code != http.StatusOK {
		arc.lastError = recorder.Body.Bytes()
		arc.lastStatusCode = recorder.Code
		log.Printf("[DEBUG ANTHROPIC] Internal request returned %d: %s", recorder.Code, string(arc.lastError))
		return false
	}

	log.Printf("[DEBUG ANTHROPIC] Recorder body length: %d bytes", recorder.Body.Len())
	log.Printf("[DEBUG ANTHROPIC] Recorder body preview: %.200s", recorder.Body.String()[:200])

	// Translate response from OpenAI to Anthropic format
	if arc.isStream {
		log.Printf("[DEBUG ANTHROPIC] Calling handleAnthropicInternalStreamResponse")
		return h.handleAnthropicInternalStreamResponse(w, recorder.Body.Bytes(), arc)
	}
	log.Printf("[DEBUG ANTHROPIC] Calling handleAnthropicInternalNonStreamResponse")
	return h.handleAnthropicInternalNonStreamResponse(w, recorder.Body.Bytes(), arc)
}

// handleAnthropicInternalNonStreamResponse handles non-streaming internal responses
func (h *Handler) handleAnthropicInternalNonStreamResponse(w http.ResponseWriter, openaiBody []byte, arc *anthropicRequestContext) bool {
	debugLog("=== INTERNAL OPENAI RESPONSE ===")
	debugLog("OpenAI Body: %s", string(openaiBody))

	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContent(openaiBody)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate to Anthropic format
	anthropicResp, err := translator.TranslateNonStreamResponse(openaiBody, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic internal response: %v", err)
		return false
	}

	debugLog("=== ANTHROPIC RESPONSE ===")
	debugLog("Anthropic Body: %s", string(anthropicResp))

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(anthropicResp)

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicInternalStreamResponse handles streaming internal responses
func (h *Handler) handleAnthropicInternalStreamResponse(w http.ResponseWriter, openaiBody []byte, arc *anthropicRequestContext) bool {
	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContent(openaiBody)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate buffered OpenAI stream to Anthropic format
	anthropicEvents, err := translator.TranslateBufferedStream(openaiBody, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic internal stream: %v", err)
		h.sendAnthropicSSEError(w, "api_error", "Stream translation failed")
		return true // Don't retry after headers sent
	}

	// Send headers if not already sent
	if !arc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		arc.headersSent = true

		// Send initial comment to establish byte stream
		w.Write([]byte(": connected\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	// Write translated events
	w.Write(anthropicEvents)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicNonStreamResponse handles a non-streaming response
func (h *Handler) handleAnthropicNonStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		log.Printf("Failed to read Anthropic upstream response: %v", err)
		return false
	}

	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContent(bodyBytes)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate to Anthropic format
	anthropicResp, err := translator.TranslateNonStreamResponse(bodyBytes, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic response: %v", err)
		return false
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(anthropicResp)

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicStreamResponse handles a streaming response
func (h *Handler) handleAnthropicStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	debugLog("=== STREAM RESPONSE START ===")
	debugLog("Request ID: %s", arc.reqID)
	debugLog("Model: %s", arc.originalModel)

	// Send headers immediately for TTFB
	if !arc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		arc.headersSent = true

		// Send initial comment to establish byte stream
		w.Write([]byte(": connected\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		debugLog("Headers sent, connection established")
	}

	// Buffer all OpenAI chunks
	var buffer bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	chunkCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		chunkCount++

		// Buffer the line
		buffer.Write(line)
		buffer.WriteByte('\n')

		// Log chunk (truncated for large content)
		lineStr := string(line)
		if len(lineStr) > 200 {
			debugLog("Chunk #%d: %s...", chunkCount, lineStr[:200])
		} else {
			debugLog("Chunk #%d: %s", chunkCount, lineStr)
		}

		// Check for [DONE]
		if bytes.HasPrefix(line, []byte("data: [DONE]")) {
			debugLog("Stream complete, received %d chunks, translating...", chunkCount)

			// Extract content for storage before translation
			content, thinking, toolCalls := extractOpenAIResponseContent(buffer.Bytes())
			arc.accumulatedResponse.WriteString(content)
			arc.accumulatedThinking.WriteString(thinking)
			arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

			// Translate buffered stream and flush
			anthropicEvents, err := translator.TranslateBufferedStream(buffer.Bytes(), arc.originalModel)
			if err != nil {
				log.Printf("Failed to translate Anthropic stream: %v", err)
				h.sendAnthropicSSEError(w, "api_error", "Stream translation failed")
				return true // Don't retry after headers sent
			}

			// Log translated events
			eventLines := strings.Split(string(anthropicEvents), "\n")
			debugLog("=== TRANSLATED EVENTS (%d lines) ===", len(eventLines))
			for i, eventLine := range eventLines {
				if strings.TrimSpace(eventLine) != "" {
					debugLog("Event line %d: %s", i+1, eventLine)
				}
			}

			w.Write(anthropicEvents)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			// Finalize with assistant message
			h.finalizeAnthropicSuccess(arc)

			debugLog("=== STREAM COMPLETE ===")
			return true
		}
	}

	// Stream ended without [DONE]
	if err := scanner.Err(); err != nil {
		log.Printf("Anthropic stream error: %v", err)
	}

	h.sendAnthropicSSEError(w, "api_error", "Stream ended unexpectedly")
	return true // Don't retry after headers sent
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────────────────────────────────────

// anthropicRequestContext holds state for an Anthropic request
type anthropicRequestContext struct {
	conf            ConfigSnapshot
	targetURL       string
	reqID           string
	startTime       time.Time
	reqLog          *store.RequestLog
	modelList       []string
	anthropicReq    *translator.AnthropicRequest
	openaiBody      []byte
	isStream        bool
	originalModel   string
	baseCtx         context.Context
	method          string
	originalHeaders http.Header
	headersSent     bool
	lastError       []byte
	lastStatusCode  int

	// Response tracking (for storing assistant message)
	accumulatedResponse  strings.Builder
	accumulatedThinking  strings.Builder
	accumulatedToolCalls []store.ToolCall
}

// sendAnthropicError sends an error response in Anthropic format
func (h *Handler) sendAnthropicError(w http.ResponseWriter, errorType, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(errorResp)
}

// finalizeAnthropicSuccess updates the request log and appends the assistant message.
// This is the equivalent of finalizeSuccess in handler_functions.go.
func (h *Handler) finalizeAnthropicSuccess(arc *anthropicRequestContext) {
	// Build assistant message from accumulated response
	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  arc.accumulatedResponse.String(),
		Thinking: arc.accumulatedThinking.String(),
	}

	// Include tool calls if any were accumulated
	if len(arc.accumulatedToolCalls) > 0 {
		assistantMsg.ToolCalls = arc.accumulatedToolCalls
	}

	// Append to messages array
	arc.reqLog.Messages = append(arc.reqLog.Messages, assistantMsg)

	// Update status and timing
	arc.reqLog.Status = "completed"
	arc.reqLog.EndTime = time.Now()
	arc.reqLog.Duration = time.Since(arc.startTime).String()
	h.store.Add(arc.reqLog)
	h.publishEvent("request_completed", map[string]interface{}{"id": arc.reqID})
}

// extractOpenAIResponseContent extracts content, thinking, and tool calls from OpenAI response.
func extractOpenAIResponseContent(openaiBody []byte) (content, thinking string, toolCalls []store.ToolCall) {
	var resp map[string]interface{}
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return "", "", nil
	}

	choices, _ := resp["choices"].([]interface{})
	if len(choices) == 0 {
		return "", "", nil
	}

	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		return "", "", nil
	}

	// Extract content
	content, _ = message["content"].(string)

	// Extract thinking (from reasoning_content if present)
	thinking, _ = message["reasoning_content"].(string)

	// Extract tool calls
	if tcs, ok := message["tool_calls"].([]interface{}); ok {
		for _, tc := range tcs {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				toolCall := store.ToolCall{
					ID:   getStringVal(tcMap, "id"),
					Type: getStringVal(tcMap, "type"),
				}
				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					toolCall.Function.Name = getStringVal(fn, "name")
					toolCall.Function.Arguments = getStringVal(fn, "arguments")
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	return content, thinking, toolCalls
}

// getStringVal safely extracts a string from a map
func getStringVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// sendAnthropicSSEError sends an error as an SSE event in Anthropic format
func (h *Handler) sendAnthropicSSEError(w http.ResponseWriter, errorType, message string) {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	}
	eventBytes, _ := json.Marshal(errorEvent)
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(eventBytes))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// validateAnthropicRequest validates an Anthropic request
func validateAnthropicRequest(req *translator.AnthropicRequest) error {
	if req.Model == "" {
		return fmt.Errorf("model is required")
	}
	if req.MaxTokens == 0 {
		return fmt.Errorf("max_tokens is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	for _, msg := range req.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("invalid role: %s (must be 'user' or 'assistant')", msg.Role)
		}
	}
	return nil
}

// copyAnthropicHeaders copies headers for upstream request
func copyAnthropicHeaders(dst *http.Request, src http.Header) {
	for name, values := range src {
		// Skip certain headers
		switch strings.ToLower(name) {
		case "content-length", "host":
			continue
		case "x-api-key":
			// Translate x-api-key to Authorization Bearer for OpenAI upstream
			if len(values) > 0 {
				dst.Header.Set("Authorization", "Bearer "+values[0])
			}
			continue
		}
		for _, value := range values {
			dst.Header.Add(name, value)
		}
	}
	dst.Header.Set("Content-Type", "application/json")
}

// convertAnthropicMessagesToStore converts Anthropic messages to store format
func convertAnthropicMessagesToStore(messages []translator.AnthropicMessage) []store.Message {
	var result []store.Message
	for _, msg := range messages {
		content := ""
		switch c := msg.Content.(type) {
		case string:
			content = c
		case []interface{}:
			// Extract text from content blocks
			var sb strings.Builder
			for _, block := range c {
				if bm, ok := block.(map[string]interface{}); ok {
					if t, ok := bm["text"].(string); ok {
						sb.WriteString(t)
					}
				}
			}
			content = sb.String()
		}
		result = append(result, store.Message{
			Role:    msg.Role,
			Content: content,
		})
	}
	return result
}

// getModelMappingConfig extracts model mapping from config
func getModelMappingConfig(modelsConfig interface{}) *translator.ModelMappingConfig {
	// Return mapping config without default - unknown models pass through unchanged
	// This allows Anthropic clients to use any model configured in the proxy
	return &translator.ModelMappingConfig{
		// No DefaultModel - let unknown models pass through
		Mapping: map[string]string{
			// Claude model aliases can be mapped here if needed
			// e.g., "claude-sonnet-4-5": "claude-sonnet-4-5-20250929"
		},
	}
}
