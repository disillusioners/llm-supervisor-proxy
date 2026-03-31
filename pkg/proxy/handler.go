package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ultimatemodel"
	usage "github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// Config holds runtime configuration for the proxy handler
type Config struct {
	ConfigMgr    config.ManagerInterface      // Config manager for dynamic updates
	ModelsConfig models.ModelsConfigInterface // Models config for fallback chains
	EventBus     *events.Bus                  // Event bus for publishing events
}

// Clone returns a snapshot of the current config values
func (c *Config) Clone() ConfigSnapshot {
	cfg := c.ConfigMgr.Get()
	return ConfigSnapshot{
		UpstreamURL:            cfg.UpstreamURL,
		UpstreamCredentialID:   cfg.UpstreamCredentialID,
		IdleTimeout:            cfg.IdleTimeout.Duration(),
		StreamDeadline:         cfg.StreamDeadline.Duration(),
		MaxGenerationTime:      cfg.MaxGenerationTime.Duration(),
		MaxStreamBufferSize:    cfg.MaxStreamBufferSize,
		ModelsConfig:           c.ModelsConfig,
		LoopDetection:          cfg.LoopDetection,
		ToolRepair:             cfg.ToolRepair,
		SSEHeartbeatEnabled:    cfg.SSEHeartbeatEnabled,
		RaceRetryEnabled:       cfg.RaceRetryEnabled,
		RaceParallelOnIdle:     cfg.RaceParallelOnIdle,
		RaceMaxParallel:        cfg.RaceMaxParallel,
		RaceMaxBufferBytes:     cfg.RaceMaxBufferBytes,
		ToolCallBufferDisabled: cfg.ToolCallBufferDisabled,
		ToolCallBufferMaxSize:  cfg.ToolCallBufferMaxSize,
		LogRawUpstreamResponse: cfg.LogRawUpstreamResponse,
		LogRawUpstreamOnError:  cfg.LogRawUpstreamOnError,
		LogRawUpstreamMaxKB:    cfg.LogRawUpstreamMaxKB,
		EventBus:               c.EventBus,
	}
}

type ConfigSnapshot struct {
	UpstreamURL          string
	UpstreamCredentialID string
	IdleTimeout          time.Duration
	StreamDeadline       time.Duration // Time limit before picking best buffer and continuing streaming
	MaxGenerationTime    time.Duration // Absolute hard timeout for entire request lifecycle
	MaxStreamBufferSize  int
	ModelsConfig         models.ModelsConfigInterface
	LoopDetection        config.LoopDetectionConfig
	ToolRepair           toolrepair.Config
	SSEHeartbeatEnabled  bool

	// Race Retry
	RaceRetryEnabled   bool
	RaceParallelOnIdle bool
	RaceMaxParallel    int
	RaceMaxBufferBytes int
	ModelID            string // Primary model for this request

	// Tool Call Buffering
	ToolCallBufferDisabled bool
	ToolCallBufferMaxSize  int64

	// Raw Upstream Response Logging
	LogRawUpstreamResponse bool
	LogRawUpstreamOnError  bool
	LogRawUpstreamMaxKB    int

	// Event Bus for publishing events during request handling
	EventBus *events.Bus
}

type Handler struct {
	config          *Config
	bus             *events.Bus
	store           *store.RequestStore
	client          *http.Client
	bufferStore     *bufferstore.BufferStore
	tokenStore      *auth.TokenStore
	ultimateHandler *ultimatemodel.Handler
	counter         *usage.Counter
}

func NewHandler(config *Config, bus *events.Bus, store *store.RequestStore, bufferStore *bufferstore.BufferStore, tokenStore *auth.TokenStore, counter *usage.Counter) *Handler {
	h := &Handler{
		config: config,
		bus:    bus,
		store:  store,
		client: &http.Client{
			// IMPORTANT: Timeout is set to 0 for streaming support.
			// We use context deadlines (attemptCtx) instead of http.Client.Timeout because:
			// 1. http.Client.Timeout applies to entire request including response body reading
			// 2. For streaming, we need to allow reading the response body indefinitely
			// 3. Context deadline in doSingleAttempt handles cancellation properly
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       300 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second, // Timeout waiting for response headers (prevents stuck requests)
			},
		},
		bufferStore: bufferStore,
		tokenStore:  tokenStore,
		counter:     counter,
	}

	// Initialize ultimate model handler
	h.ultimateHandler = ultimatemodel.NewHandler(config.ConfigMgr, config.ModelsConfig, bus)

	// Wire up tool call buffer configuration for ultimate model handler
	cfg := config.ConfigMgr.Get()
	h.ultimateHandler.SetToolCallBufferConfig(
		cfg.ToolCallBufferMaxSize,
		cfg.ToolCallBufferDisabled,
		&cfg.ToolRepair,
	)

	return h
}

func (h *Handler) publishEvent(eventType string, data interface{}) {
	if h.bus != nil {
		h.bus.Publish(events.Event{
			Type:      eventType,
			Timestamp: time.Now().Unix(),
			Data:      data,
		})
	}
}

// saveRawResponse saves the raw upstream response to disk and emits an event.
// This is a best-effort operation - errors are logged but don't fail the request.
// Should be called in a goroutine to avoid blocking the response.
func (h *Handler) saveRawResponse(requestID string, rawBytes []byte, rawRequestBody []byte, maxKB int) {
	if h.bufferStore == nil {
		return
	}
	if len(rawBytes) == 0 {
		return
	}

	maxBytes := int64(maxKB) * 1024

	// Skip if too large
	if int64(len(rawBytes)) > maxBytes {
		log.Printf("[RAW-LOG] Response too large: %d > %d limit (request=%s)",
			len(rawBytes), maxBytes, requestID)
		return
	}

	// Save response
	bufferID := fmt.Sprintf("%s-response", requestID)
	if err := h.bufferStore.Save(bufferID, rawBytes); err != nil {
		log.Printf("[RAW-LOG] Failed to save response: %v (request=%s)", err, requestID)
		return
	}

	// Save request body (for correlation) - optional but useful for debugging
	var requestBodyID string
	if len(rawRequestBody) > 0 && int64(len(rawRequestBody)) <= maxBytes {
		requestBodyID = fmt.Sprintf("%s-request", requestID)
		if err := h.bufferStore.Save(requestBodyID, rawRequestBody); err != nil {
			log.Printf("[RAW-LOG] Failed to save request body: %v (request=%s)", err, requestID)
			requestBodyID = "" // Clear on error
		}
	}

	// Emit event
	h.publishEvent("response_logged", map[string]interface{}{
		"id":              requestID,
		"buffer_id":       bufferID,
		"request_body_id": requestBodyID,
		"size_bytes":      len(rawBytes),
	})
}

// HandleModels returns the list of available models in OpenAI-compatible format.
// GET /v1/models
func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Clone()
	enabledModels := cfg.ModelsConfig.GetEnabledModels()

	// Build OpenAI-compatible response
	models := make([]map[string]interface{}, 0, len(enabledModels))
	for _, m := range enabledModels {
		models = append(models, map[string]interface{}{
			"id":       m.ID,
			"object":   "model",
			"created":  1700000000, // Static timestamp
			"owned_by": "llm-supervisor-proxy",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// extractAPIKey extracts the API key from Authorization Bearer or X-API-Key header
func (h *Handler) extractAPIKey(r *http.Request) string {
	// Check Authorization: Bearer <token> header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Check X-API-Key header (capitalized)
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != "" {
		return apiKey
	}

	// Check x-api-key header (lowercase, used by Anthropic SDK)
	apiKey = r.Header.Get("x-api-key")
	if apiKey != "" {
		return apiKey
	}

	return ""
}

// authenticate validates the API key and returns the token + true if valid
func (h *Handler) authenticate(r *http.Request) (*auth.AuthToken, bool) {
	// If tokenStore is nil, skip validation (auth disabled)
	if h.tokenStore == nil {
		return nil, true
	}

	apiKey := h.extractAPIKey(r)
	if apiKey == "" {
		return nil, false
	}

	// Create a timeout context for database query
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	token, err := h.tokenStore.ValidateToken(ctx, apiKey)
	if err != nil {
		return nil, false
	}
	return token, true
}

// requiresInternalAuth checks if any model in the request chain uses internal upstream
// and therefore requires client API key validation
func (h *Handler) requiresInternalAuth(rc *requestContext) bool {
	// If tokenStore is nil, auth is disabled
	if h.tokenStore == nil {
		return false
	}

	// Check all models in the chain (primary + fallbacks)
	for _, modelID := range rc.modelList {
		modelConfig := rc.conf.ModelsConfig.GetModel(modelID)
		if modelConfig != nil && modelConfig.Internal {
			return true
		}
	}

	return false
}

// sendAuthError sends a 401 Unauthorized JSON error response
func (h *Handler) sendAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeAuthenticationError,
		"",
		"Invalid or expired API key",
	))
}

// sendError sends a JSON error response in OpenAI-compatible format
func (h *Handler) sendError(w http.ResponseWriter, code int, message, errType, errorCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(models.NewOpenAIError(errType, errorCode, message))
}

// sendSSEError sends an error as an SSE event to the client.
// This is used when a streaming error occurs after headers have been sent,
// so we can't send a regular HTTP error response.
// OpenAI format: data: {"error":{"type":"...","message":"..."}}
func (h *Handler) sendSSEError(w http.ResponseWriter, errType, message string) {
	errResp := models.NewOpenAIError(errType, "", message)
	data, _ := json.Marshal(errResp)
	// OpenAI streaming error format: just data, no custom event type
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// HandleChatCompletions is the main entry point for proxying chat completions.
func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rc, err := h.initRequestContext(r)
	if err != nil {
		if err.Error() == "invalid_upstream_url" {
			http.Error(w, "Invalid Upstream URL configuration", http.StatusInternalServerError)
		} else if err.Error() == "read_body_failed" {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
		} else if err.Error() == "invalid_json" {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		}
		return
	}

	// Only authenticate if using internal upstream
	// For external upstream, the upstream provider handles authentication
	if h.requiresInternalAuth(rc) {
		token, ok := h.authenticate(r)
		if !ok {
			// Update request log to failed status
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "Authentication failed: invalid or expired API key"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)

			// Publish event so frontend refreshes
			h.publishEvent("auth_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": "invalid_api_key",
			})

			h.sendAuthError(w)
			return
		}
		if token != nil {
			rc.tokenID = token.ID
			rc.tokenName = token.Name
		}
	}

	// === ULTIMATE MODEL CHECK (EARLY EXIT) ===
	// Check if ultimate model should be triggered for duplicate requests
	if h.ultimateHandler != nil {
		// Extract messages from request body
		if messages, ok := rc.requestBody["messages"].([]interface{}); ok && len(messages) > 0 {
			// Convert to map[string]interface{} format for hashing
			msgMaps := make([]map[string]interface{}, len(messages))
			for i, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					msgMaps[i] = m
				}
			}

			result := h.ultimateHandler.ShouldTrigger(msgMaps)
			if result.Triggered {
				// Check if retry limit exhausted
				if result.RetryExhausted {
					log.Printf("[UltimateModel] Retry limit exhausted for hash=%s (attempt %d/%d)",
						result.Hash[:8], result.CurrentRetry, result.MaxRetries)

					// Determine if streaming
					isStream := false
					if stream, ok := rc.requestBody["stream"].(bool); ok {
						isStream = stream
					}

					// Update request log
					rc.reqLog.Status = "failed"
					rc.reqLog.Error = fmt.Sprintf("Ultimate model retry limit exceeded (attempt %d/%d)", result.CurrentRetry, result.MaxRetries)
					rc.reqLog.EndTime = time.Now()
					rc.reqLog.Duration = time.Since(rc.startTime).String()
					rc.reqLog.UltimateModelUsed = true
					rc.reqLog.UltimateModelID = h.ultimateHandler.GetModelID()
					h.store.Add(rc.reqLog)

					// Publish event
					h.publishEvent("ultimate_model_retry_exhausted", map[string]interface{}{
						"id":            rc.reqID,
						"hash":          result.Hash[:8],
						"current_retry": result.CurrentRetry,
						"max_retries":   result.MaxRetries,
					})

					// Send error response (HTTP 200 with JSON stream error)
					h.ultimateHandler.SendRetryExhaustedError(w, result.Hash, result.CurrentRetry, result.MaxRetries, isStream)
					return
				}

				ultimateModelID := h.ultimateHandler.GetModelID()
				log.Printf("[UltimateModel] Triggered for duplicate request, using %s, hash=%s, retry=%d/%d",
					ultimateModelID, result.Hash[:8], result.CurrentRetry, result.MaxRetries)

				// Update request log with ultimate model info
				rc.reqLog.UltimateModelUsed = true
				rc.reqLog.UltimateModelID = ultimateModelID
				rc.reqLog.Status = "running"
				h.store.Add(rc.reqLog)

				// Publish event
				h.publishEvent("ultimate_model_triggered", map[string]interface{}{
					"id":             rc.reqID,
					"ultimate_model": ultimateModelID,
					"original_model": rc.reqLog.Model,
					"hash":           result.Hash[:8],
					"current_retry":  result.CurrentRetry,
					"max_retries":    result.MaxRetries,
				})

				// Execute with ultimate model (raw proxy, no retry/fallback)
				// The Execute method determines streaming from requestBody["stream"]
				err := h.ultimateHandler.Execute(r.Context(), w, r, rc.requestBody, rc.reqLog.Model, result.Hash, &rc.headersSent)
				if err != nil {
					log.Printf("[UltimateModel] Error: %v", err)
					rc.reqLog.Status = "failed"
					rc.reqLog.Error = err.Error()
					rc.reqLog.EndTime = time.Now()
					rc.reqLog.Duration = time.Since(rc.startTime).String()
					h.store.Add(rc.reqLog)

					h.publishEvent("ultimate_model_failed", map[string]interface{}{
						"id":    rc.reqID,
						"error": err.Error(),
					})

					// If headers not sent, send error response
					if !rc.headersSent {
						if strings.Contains(err.Error(), "not found") {
							http.Error(w, "Ultimate model not found in database", http.StatusBadGateway)
						} else {
							http.Error(w, err.Error(), http.StatusBadGateway)
						}
					} else {
						// Headers already sent (streaming) - send SSE error
						h.sendSSEError(w, models.ErrorTypeUpstreamError, err.Error())
					}
					return
				}

				// Success - update log
				rc.reqLog.Status = "completed"
				rc.reqLog.EndTime = time.Now()
				rc.reqLog.Duration = time.Since(rc.startTime).String()
				h.store.Add(rc.reqLog)

				// Count this request for hourly usage tracking
				if rc.tokenID != "" && h.counter != nil {
					var promptTokens, completionTokens, totalTokens int
					if rc.reqLog.Usage != nil {
						promptTokens = rc.reqLog.Usage.PromptTokens
						completionTokens = rc.reqLog.Usage.CompletionTokens
						totalTokens = rc.reqLog.Usage.TotalTokens
					}
					hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
					go func() {
						if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
							log.Printf("failed to increment usage counter: %v", err)
						}
					}()
				}

				h.publishEvent("request_completed", map[string]interface{}{
					"id":             rc.reqID,
					"model":          ultimateModelID,
					"duration":       rc.reqLog.Duration,
					"ultimate_model": true,
				})

				return // DONE - no fallback, no retry
			}
		}
	}
	// === END ULTIMATE MODEL CHECK ===

	h.publishEvent("request_started", map[string]interface{}{"id": rc.reqID})

	// Unified Race Retry Design (Parallel Race)
	log.Printf("[RACE] Parallel race retry started for request %s", rc.reqID)
	coordinator := newRaceCoordinatorWithEvents(rc.baseCtx, &rc.conf, r, rc.rawBody, rc.modelList, h.bus, rc.reqID)
	coordinator.Start()

	winner := coordinator.WaitForWinner()

	// Capture upstream request statuses for UI display
	statuses := coordinator.GetRequestStatuses()
	rc.reqLog.UpstreamRequests = store.UpstreamRequestStatus{
		Main:     statuses["main"],
		Second:   statuses["second"],
		Fallback: statuses["fallback"],
	}

	if winner != nil {
		defer func() {
			if winner.cancel != nil {
				winner.cancel()
			}
		}()

		if rc.isStream {
			// Stream the final result from the winner's buffer
			h.streamResult(w, rc, winner)
		} else {
			// Send a single JSON response from the winner's buffer
			h.handleNonStreamResult(w, rc, winner)
		}
		return
	}

	// If winner is nil, it means either context cancelled or all models failed
	select {
	case <-rc.baseCtx.Done():
		return
	default:
		// All attempts failed - mark for ultimate model retry
		log.Printf("All models failed for request %s (Race Retry)", rc.reqID)

		// Mark this request as failed so ultimate model can be triggered on retry
		if h.ultimateHandler != nil {
			if messages, ok := rc.requestBody["messages"].([]interface{}); ok && len(messages) > 0 {
				msgMaps := make([]map[string]interface{}, len(messages))
				for i, msg := range messages {
					if m, ok := msg.(map[string]interface{}); ok {
						msgMaps[i] = m
					}
				}
				h.ultimateHandler.MarkFailed(msgMaps)
			}
		}

		// Get final error info from coordinator (OpenCode-compatible format)
		// First check if stream deadline fired with no content (specific timeout message)
		var errInfo FinalErrorInfo
		if deadlineErr := coordinator.GetStreamDeadlineError(); deadlineErr != nil {
			errInfo = *deadlineErr
		} else {
			errInfo = coordinator.GetFinalErrorInfo()
		}

		// Log failure
		rc.reqLog.Status = "failed"
		rc.reqLog.Error = errInfo.Message
		rc.reqLog.EndTime = time.Now()
		rc.reqLog.Duration = time.Since(rc.startTime).String()
		h.store.Add(rc.reqLog)

		h.publishEvent("request_failed", map[string]interface{}{
			"id":    rc.reqID,
			"error": rc.reqLog.Error,
		})

		h.sendError(w, errInfo.HTTPStatus, errInfo.Message, errInfo.ErrorType, errInfo.ErrorCode)
		return
	}
}

// streamResult flushes the winner's buffer to the client
func (h *Handler) streamResult(w http.ResponseWriter, rc *requestContext, winner *upstreamRequest) {
	buffer := winner.GetBuffer()
	readIndex := 0

	// Capture raw bytes BEFORE any pruning happens - this is the complete response
	if rc.conf.LogRawUpstreamResponse {
		if buf := winner.GetBuffer(); buf != nil {
			capturedBytes := buf.GetAllRawBytes()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}

	// Set headers if not already sent
	if !rc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true
	}

	flusher, _ := w.(http.Flusher)

	// Send initial connected message for SSE
	if rc.isStream {
		if _, err := w.Write([]byte(": connected\n\n")); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Start heartbeat goroutine
	heartbeatCancel := h.startSSEHeartbeat(w, rc.baseCtx)
	defer heartbeatCancel()

	// Stream existing chunks first
	chunks, _ := buffer.GetChunksFrom(readIndex)
	for _, chunk := range chunks {
		if _, err := w.Write(chunk); err != nil {
			return
		}
		// Extract content for logging
		if bytes.HasPrefix(chunk, []byte("data: ")) {
			data := bytes.TrimPrefix(chunk, []byte("data: "))
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
		}
		readIndex++
	}
	if flusher != nil {
		flusher.Flush()
	}
	buffer.Prune(readIndex)

	// Continue streaming until complete
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-rc.baseCtx.Done():
			return
		case <-buffer.NotifyCh():
			// New data available
			chunks, _ = buffer.GetChunksFrom(readIndex)
			for _, chunk := range chunks {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				// Extract content for logging
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
				}
				readIndex++
			}
			if flusher != nil {
				flusher.Flush()
			}
			buffer.Prune(readIndex)
		case <-buffer.Done():
			// Stream complete - drain remaining data
			chunks, _ = buffer.GetChunksFrom(readIndex)
			for _, chunk := range chunks {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
				// Extract content for logging
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
				}
				readIndex++
			}
			if flusher != nil {
				flusher.Flush()
			}

			// If stream failed, send error event to client
			if err := buffer.Err(); err != nil {
				log.Printf("[ERROR] Stream buffer closed with error: %v", err)

				// Log raw response on error
				if rc.conf.LogRawUpstreamOnError {
					if buf := winner.GetBuffer(); buf != nil {
						capturedBytes := buf.GetAllRawBytes()
						go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
					}
				}

				// Now safe to prune (after capturing raw response)
				buffer.Prune(readIndex)

				// Mark this request as failed so ultimate model can be triggered on retry
				if h.ultimateHandler != nil {
					if messages, ok := rc.requestBody["messages"].([]interface{}); ok && len(messages) > 0 {
						msgMaps := make([]map[string]interface{}, len(messages))
						for i, msg := range messages {
							if m, ok := msg.(map[string]interface{}); ok {
								msgMaps[i] = m
							}
						}
						h.ultimateHandler.MarkFailed(msgMaps)
					}
				}

				// Send OpenAI-compatible error response
				errResp := models.NewOpenAIError(models.ErrorTypeServerError, "", fmt.Sprintf("Streaming error: %v", err))
				data, _ := json.Marshal(errResp)
				fmt.Fprintf(w, "data: %s\n\n", string(data))
				if flusher != nil {
					flusher.Flush()
				}
				return
			}

			// Log raw response on success - capture bytes BEFORE final pruning
			if rc.conf.LogRawUpstreamResponse {
				if buf := winner.GetBuffer(); buf != nil {
					capturedBytes := buf.GetAllRawBytes()
					go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
				}
			}

			// Now safe to prune (after capturing raw response)
			buffer.Prune(readIndex)

			// Extract usage from the last SSE chunk (it contains the "usage" field)
			chunks, _ = buffer.GetChunksFrom(0)
			for i := len(chunks) - 1; i >= 0; i-- {
				chunk := chunks[i]
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					if string(data) == "[DONE]" || string(data) == "" {
						continue
					}
					if usage := extractUsageFromChunk(data); usage != nil {
						rc.reqLog.Usage = usage
						break
					}
				}
			}

			// Finalize tool call arguments from builders
			for i := range rc.accumulatedToolCalls {
				if i < len(rc.toolCallArgBuilders) {
					args := rc.toolCallArgBuilders[i].String()
					rc.accumulatedToolCalls[i].Function.Arguments = args

					// Validate JSON arguments
					if args != "" {
						var js interface{}
						if err := json.Unmarshal([]byte(args), &js); err != nil {
							log.Printf("[WARN] Tool call[%d] has invalid JSON arguments: %v (args length: %d)",
								i, err, len(args))
						}
					}
				}
			}

			// Check for duplicate tool call IDs
			seenIDs := make(map[string]int)
			for i, tc := range rc.accumulatedToolCalls {
				if tc.ID != "" {
					if firstIdx, exists := seenIDs[tc.ID]; exists {
						log.Printf("[WARN] Duplicate tool call ID '%s' at indices %d and %d", tc.ID, firstIdx, i)
					} else {
						seenIDs[tc.ID] = i
					}
				}
			}

			// Validate function names are present
			for i, tc := range rc.accumulatedToolCalls {
				if tc.Function.Name == "" {
					log.Printf("[WARN] Tool call[%d] has empty function name", i)
				}
			}

			// Store assistant message
			assistantMsg := store.Message{
				Role:    "assistant",
				Content: rc.accumulatedResponse.String(),
			}
			if rc.accumulatedThinking.Len() > 0 {
				assistantMsg.Thinking = rc.accumulatedThinking.String()
			}
			if len(rc.accumulatedToolCalls) > 0 {
				assistantMsg.ToolCalls = rc.accumulatedToolCalls
			}

			// Log success
			rc.reqLog.Status = "completed"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)
			h.store.Add(rc.reqLog)

			// Count this request for hourly usage tracking
			if rc.tokenID != "" && h.counter != nil {
				var promptTokens, completionTokens, totalTokens int
				if rc.reqLog.Usage != nil {
					promptTokens = rc.reqLog.Usage.PromptTokens
					completionTokens = rc.reqLog.Usage.CompletionTokens
					totalTokens = rc.reqLog.Usage.TotalTokens
				}
				hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
				go func() {
					if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
						log.Printf("failed to increment usage counter: %v", err)
					}
				}()
			}

			h.publishEvent("request_completed", map[string]interface{}{
				"id":       rc.reqID,
				"model":    winner.GetModelID(),
				"duration": rc.reqLog.Duration,
				"race":     true,
			})

			// Log raw response on success if enabled - we capture at beginning of streamResult
			return
		case <-ticker.C:
			// Safety backup if notification missed
			chunks, _ = buffer.GetChunksFrom(readIndex)
			if len(chunks) > 0 {
				for _, chunk := range chunks {
					if _, err := w.Write(chunk); err != nil {
						return
					}
					if bytes.HasPrefix(chunk, []byte("data: ")) {
						data := bytes.TrimPrefix(chunk, []byte("data: "))
						extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
					}
					readIndex++
				}
				if flusher != nil {
					flusher.Flush()
				}
				buffer.Prune(readIndex)
			}
		}
	}
}

// handleNonStreamResult sends a single JSON response from the winner's buffer
func (h *Handler) handleNonStreamResult(w http.ResponseWriter, rc *requestContext, winner *upstreamRequest) {
	buffer := winner.GetBuffer()

	// Wait for buffer to be complete if not already
	select {
	case <-buffer.Done():
	case <-rc.baseCtx.Done():
		return
	}

	// Check for buffer error
	if err := buffer.Err(); err != nil {
		// Log raw response on error if enabled - capture bytes before returning
		if rc.conf.LogRawUpstreamOnError {
			if buf := winner.GetBuffer(); buf != nil {
				capturedBytes := buf.GetAllRawBytes()
				go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		errResp := models.NewOpenAIError(models.ErrorTypeUpstreamError, "", fmt.Sprintf("Upstream error: %v", err))
		data, _ := json.Marshal(errResp)
		w.Write(data)
		return
	}

	chunks, _ := buffer.GetChunksFrom(0)
	var finalBody []byte

	// Concatenate chunks, stripping SSE prefixes if present
	for _, chunk := range chunks {
		line := string(chunk)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)
			if data == "[DONE]" || data == "" {
				continue
			}
			finalBody = append(finalBody, []byte(data)...)
		} else {
			finalBody = append(finalBody, chunk...)
		}
	}

	// Set headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(finalBody)

	// Extract content for logging
	var resp map[string]interface{}
	if err := json.Unmarshal(finalBody, &resp); err == nil {
		if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if msg, ok := choice["message"].(map[string]interface{}); ok {
					if content, ok := msg["content"].(string); ok {
						rc.accumulatedResponse.WriteString(content)
					}
				}
			}
		}

		// Extract usage from response
		if usageData, ok := resp["usage"].(map[string]interface{}); ok {
			usage := &store.Usage{}
			if v, ok := usageData["prompt_tokens"].(float64); ok {
				usage.PromptTokens = int(v)
			}
			if v, ok := usageData["completion_tokens"].(float64); ok {
				usage.CompletionTokens = int(v)
			}
			if v, ok := usageData["total_tokens"].(float64); ok {
				usage.TotalTokens = int(v)
			}
			rc.reqLog.Usage = usage
		}
	}

	// Log success
	rc.reqLog.Status = "completed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()

	assistantMsg := store.Message{
		Role:    "assistant",
		Content: rc.accumulatedResponse.String(),
	}
	rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)
	h.store.Add(rc.reqLog)

	// Count this request for hourly usage tracking
	if rc.tokenID != "" && h.counter != nil {
		var promptTokens, completionTokens, totalTokens int
		if rc.reqLog.Usage != nil {
			promptTokens = rc.reqLog.Usage.PromptTokens
			completionTokens = rc.reqLog.Usage.CompletionTokens
			totalTokens = rc.reqLog.Usage.TotalTokens
		}
		hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
		go func() {
			if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
				log.Printf("failed to increment usage counter: %v", err)
			}
		}()
	}

	h.publishEvent("request_completed", map[string]interface{}{
		"id":       rc.reqID,
		"model":    winner.GetModelID(),
		"duration": rc.reqLog.Duration,
		"race":     true,
	})

	// Log raw response on success if enabled - capture bytes before returning
	if rc.conf.LogRawUpstreamResponse {
		if buf := winner.GetBuffer(); buf != nil {
			capturedBytes := buf.GetAllRawBytes()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}
}
