package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxyheader"
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
		RaceRetryEnabled:       cfg.RaceRetryEnabled,
		RaceParallelOnIdle:     cfg.RaceParallelOnIdle,
		RaceMaxParallel:        cfg.RaceMaxParallel,
		RaceMaxBufferBytes:     cfg.RaceMaxBufferBytes,
		ToolCallBufferDisabled: cfg.ToolCallBufferDisabled,
		ToolCallBufferMaxSize:  cfg.ToolCallBufferMaxSize,
		LogRawUpstreamResponse: cfg.LogRawUpstreamResponse,
		LogRawUpstreamOnError:  cfg.LogRawUpstreamOnError,
		LogRawUpstreamMaxKB:    cfg.LogRawUpstreamMaxKB,
		IdleTerminationEnabled: cfg.IdleTerminationEnabled,
		IdleTerminationTimeout: cfg.IdleTerminationTimeout.Duration(),
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

	// Idle Termination
	IdleTerminationEnabled bool
	IdleTerminationTimeout time.Duration

	// Event Bus for publishing events during request handling
	EventBus *events.Bus
}

type Handler struct {
	config          *Config
	bus             *events.Bus
	store           *store.RequestStore
	client          *http.Client
	bufferStore     *bufferstore.BufferStore
	tokenStore      auth.TokenStoreInterface
	ultimateHandler *ultimatemodel.Handler
	counter         *usage.Counter

	// credEngine is the load-balancing engine injected at construction
	// (Phase 3 / Tasks 3 + W-3). The Config struct has NO CredEngine
	// field — constructor-only injection is the law (single source of
	// injection; no second Config.CredEngine path).
	//
	// MAY BE NIL: legacy single-credential paths and unit tests that
	// don't wire the engine pass nil. The race coordinator's
	// ResolveInternalConfigWithAffinity seam handles nil-engine by
	// returning the legacy single-credential resolution (Phase 2).
	credEngine *credentiallb.Engine
}

// NewHandler (Phase 3 / Task 3 + W-3): 7th constructor parameter is the
// load-balancing engine. NO Config.CredEngine field — the 7th positional
// arg is the SOLE injection path.
//
// If credEngine is nil, the race coordinator's nil-engine branch serves
// the legacy single-credential fast path (Phase 2 / E-3); behavior is
// byte-identical to pre-Phase-3.
//   credEngine may be nil.
func NewHandler(
	config *Config,
	bus *events.Bus,
	store *store.RequestStore,
	bufferStore *bufferstore.BufferStore,
	tokenStore auth.TokenStoreInterface,
	counter *usage.Counter,
	credEngine *credentiallb.Engine,
) *Handler {
	h := &Handler{
		config:     config,
		bus:        bus,
		store:      store,
		credEngine: credEngine,
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

	// Initialize ultimate model handler (Phase 3 / Task 3): the engine
	// is forwarded to ultimatemodel.NewHandler so its internal executeInternal
	// path can call ExcludeAndReselect for the rate-limit failover hook
	// (Task 21). The package owns the ult-internal hook end-to-end.
	h.ultimateHandler = ultimatemodel.NewHandler(config.ConfigMgr, config.ModelsConfig, bus, credEngine)

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

	// Check if resolved model (primary) is internal
	if rc.resolvedModel != nil && rc.resolvedModel.Internal {
		return true
	}

	// Check all models in the chain (IDs for resolved models, raw names for external)
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

// sendModelNotAllowedError sends a 403 Forbidden JSON error response for model access violations
func (h *Handler) sendModelNotAllowedError(w http.ResponseWriter, modelName string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"type":    "access_denied",
			"message": "model not allowed for this token",
			"model":   modelName,
		},
	})
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
		if errors.Is(err, modelscache.ErrConfigUnavailable) {
			// db-cache-layer 1D — fail-fast 503: the model could not be
			// resolved AND the config store is unhealthy. Never a silent
			// external passthrough on a DB error (2026-08-27 incident
			// class). The existing sendError helper already supports any
			// code/message (1.D.3).
			h.sendError(w, http.StatusServiceUnavailable, err.Error(), "config_store_unavailable", "")
			if rc != nil {
				h.publishEvent("config_store_unavailable", map[string]interface{}{
					"id":    rc.reqID,
					"model": rc.reqLog.OriginalModel,
				})
			}
			return
		}
		if err.Error() == "invalid_upstream_url" {
			http.Error(w, "Invalid Upstream URL configuration", http.StatusInternalServerError)
		} else if err.Error() == "read_body_failed" {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
		} else if err.Error() == "invalid_json" {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		}
		return
	}

	// Reset accumulated buffers after request completes to prevent memory leaks
	// from strings.Builder internal buffers retaining capacity
	defer func() {
		if rc != nil {
			rc.reset()
		}
	}()

	// Check if this request requires internal authentication
	// (model is internal and needs client API key validation)
	requiresAuth := h.requiresInternalAuth(rc)

	// Try to authenticate the request
	// Authentication is required for internal models OR when a token is provided
	var authToken *auth.AuthToken
	if requiresAuth || h.tokenStore != nil {
		authToken, _ = h.authenticate(r)
		if authToken == nil && requiresAuth {
			// Authentication required but failed
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "Authentication failed: invalid or expired API key"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)

			h.publishEvent("auth_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": "invalid_api_key",
			})

			h.sendAuthError(w)
			return
		}
	}

	// Populate token info if authenticated
	if authToken != nil {
		rc.tokenID = authToken.ID
		rc.tokenName = authToken.Name
		rc.ultimateModelEnabled = authToken.UltimateModelEnabled
		rc.ultimateModelID = authToken.UltimateModelID
	}

	// Phase 3 / Task 2a — POST-AUTH wiring site (A-1, A-1-wiring).
	// HOISTED OUT of the `if authToken != nil` branch (Round 3j W3):
	// anonymous / unauthenticated requests must STILL get a conversation
	// key — they receive the unsalted key because rc.tokenID is the
	// zero value "" (the requestContext defaults it to empty). The
	// key is NEVER computed in initRequestContext (tokenID is unset
	// there at handler.go:352). The key is only meaningful for
	// INTERNAL models — external / unknown models leave
	// rc.conversationKey == "" and the engine path is skipped at every
	// call site.
	//
	// W-2 + C2: when no first user message was extracted, the key
	// stays "" — the engine then picks FRESH per request and stores
	// NO binding (the ""-as-own-bucket reading is REMOVED per W-2;
	// ComputeConversationKey never returns "" by itself, so the
	// caller owns this gate). The DEBUG log below is the canonical
	// C2 observability mechanism.
	if rc.resolvedModel != nil && rc.resolvedModel.Internal {
		if rc.firstUserMessage == "" {
			rc.conversationKey = ""
			log.Printf("[LB] empty conversationKey for modelID=%s; engine will pick fresh per request (no binding stored)",
				rc.resolvedModel.ID)
		} else {
			rc.conversationKey = credentiallb.ComputeConversationKey(
				rc.resolvedModel.ID,
				rc.tokenID, // "" for anonymous — unsalted key
				rc.firstUserMessage,
			)
			log.Printf("[LB] computed conversationKey hash=%s modelID=%s tokenID=%s len(firstUserMessage)=%d",
				rc.conversationKey[:8],
				rc.resolvedModel.ID,
				rc.tokenIDDisplay(),
				len(rc.firstUserMessage),
			)
		}
	}

	// === MODEL ACCESS CHECK ===
	// Check if the requested model is allowed for this token
	// This check happens AFTER authentication for internal models
	// For external models: only check if token has restrictions (fail-closed)
	if authToken != nil {
		var modelID string
		if rc.resolvedModel != nil {
			modelID = rc.resolvedModel.ID
		}
		if modelID != "" {
			// Model exists in our DB - check if allowed by token
			if !authToken.IsModelAllowed(modelID) {
				log.Printf("[AUTH] Model '%s' (ID: %s) not allowed for token %s (%s)", rc.reqLog.Model, modelID, rc.tokenID, rc.tokenName)

				rc.reqLog.Status = "failed"
				rc.reqLog.Error = fmt.Sprintf("model '%s' not allowed for this token", rc.reqLog.Model)
				rc.reqLog.EndTime = time.Now()
				rc.reqLog.Duration = time.Since(rc.startTime).String()
				h.store.Add(rc.reqLog)

				h.publishEvent("model_not_allowed", map[string]interface{}{
					"id":    rc.reqID,
					"model": rc.reqLog.Model,
					"token": rc.tokenID,
				})

				h.sendModelNotAllowedError(w, rc.reqLog.Model)
				return
			}
		} else if len(authToken.AllowedModels) > 0 {
			// Model not in DB AND token has restrictions → deny (fail-closed)
			// Unknown models should not bypass allowed_models restrictions
			log.Printf("[AUTH] Model '%s' not found in DB, token %s (%s) has restrictions - denying", rc.reqLog.Model, rc.tokenID, rc.tokenName)

			rc.reqLog.Status = "failed"
			rc.reqLog.Error = fmt.Sprintf("model '%s' not allowed for this token", rc.reqLog.Model)
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)

			h.publishEvent("model_not_allowed", map[string]interface{}{
				"id":    rc.reqID,
				"model": rc.reqLog.Model,
				"token": rc.tokenID,
			})

			h.sendModelNotAllowedError(w, rc.reqLog.Model)
			return
		}
		// else: model not in DB and no restrictions → allow (external/open models)
	}
	// === END MODEL ACCESS CHECK ===

	// Debug log when ultimate model is skipped due to missing permission
	if h.ultimateHandler != nil && !rc.ultimateModelEnabled && rc.tokenID != "" {
		log.Printf("[DEBUG] ultimate model skipped: token %s (%s) lacks ultimate_model_enabled, model: %s", rc.tokenID, rc.tokenName, rc.reqLog.Model)
	}

	// Header override for forcing ultimate model (fail-closed: requires auth AND admin must have enabled it)
	forceUltimate := r.Header.Get("X-Force-Ultimate-Model") == "true" || r.Header.Get("X-Force-Ultimate-Model") == "1"
	if forceUltimate && authToken != nil && rc.ultimateModelEnabled {
		rc.ultimateModelEnabled = true
		log.Printf("[DEBUG] ultimate model forced via X-Force-Ultimate-Model header")
	}

	// Header-driven flag for MiniMax reasoning_details split-mode translation.
	// Mirrors the X-Force-Ultimate-Model value-list semantics (R8/Q3):
	// accepted values are case-sensitive lowercase "true" or "1" only.
	// Parsed via proxyheader.ParseInterleavedThinkingHeader (single source
	// of truth shared with pkg/ultimatemodel.Execute). Stored on rc for
	// race paths; ultimate paths re-parse the header because rc does not
	// cross the pkg/proxy → pkg/ultimatemodel package boundary.
	rc.interleavedThinking = proxyheader.ParseInterleavedThinkingHeader(r)

	// === ULTIMATE MODEL ACCESS CHECK ===
	// For ultimate model, we need to check access control separately
	// because the ultimate model might not be in the original request's model list
	ultimateModelID := ""
	if h.ultimateHandler != nil && (rc.ultimateModelEnabled || forceUltimate) {
		// Determine which ultimate model will be used
		ultimateModelID = h.ultimateHandler.GetModelID()
		if rc.ultimateModelID != "" {
			ultimateModelID = rc.ultimateModelID
		}

		// If ultimate model is different from the requested model, check access control
		if ultimateModelID != "" && authToken != nil {
			var requestedModelID string
			if rc.resolvedModel != nil {
				requestedModelID = rc.resolvedModel.ID
			}
			if ultimateModelID != requestedModelID {
				// Ultimate model is different - check if allowed
				if !authToken.IsModelAllowed(ultimateModelID) {
					log.Printf("[AUTH] Ultimate model '%s' not allowed for token %s (%s)", ultimateModelID, rc.tokenID, rc.tokenName)

					rc.reqLog.Status = "failed"
					rc.reqLog.Error = fmt.Sprintf("ultimate model '%s' not allowed for this token", ultimateModelID)
					rc.reqLog.EndTime = time.Now()
					rc.reqLog.Duration = time.Since(rc.startTime).String()
					rc.reqLog.UltimateModelUsed = true
					rc.reqLog.UltimateModelID = ultimateModelID
					h.store.Add(rc.reqLog)

					h.publishEvent("ultimate_model_not_allowed", map[string]interface{}{
						"id":    rc.reqID,
						"model": ultimateModelID,
						"token": rc.tokenID,
					})

					h.sendModelNotAllowedError(w, ultimateModelID)
					return
				}
			}
		}
	}
	// === END ULTIMATE MODEL ACCESS CHECK ===

	// === ULTIMATE MODEL CHECK (EARLY EXIT) ===
	// Check if ultimate model should be triggered for duplicate requests
	// Permission gate: skip if token lacks ultimate_model_enabled permission
	if h.ultimateHandler != nil && rc.ultimateModelEnabled {
		// Extract messages from request body
		if messages, ok := rc.requestBody["messages"].([]interface{}); ok && len(messages) > 0 {
			// Convert to map[string]interface{} format for hashing
			msgMaps := make([]map[string]interface{}, len(messages))
			for i, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					msgMaps[i] = m
				}
			}

			var result ultimatemodel.ShouldTriggerResult
			if forceUltimate {
				result = h.ultimateHandler.ForceTrigger(msgMaps)
				log.Printf("[DEBUG] ForceTrigger hash=%s", result.Hash[:8])
			} else {
				result = h.ultimateHandler.ShouldTrigger(msgMaps)
			}
			if result.Triggered {
				// Exclusion gate: skip ultimate model switching for excluded models
				// Note: forceUltimate always bypasses exclusion (admin override)
				if !forceUltimate && rc.resolvedModel != nil && rc.resolvedModel.ExcludeFromUltimateSwitching {
					log.Printf("[UltimateModel] ultimate model switching excluded for model=%s hash=%s", rc.resolvedModel.ID, result.Hash[:8])
					h.publishEvent("ultimate_model_excluded", map[string]interface{}{
						"id":    rc.reqID,
						"model": rc.resolvedModel.ID,
						"hash":  result.Hash[:8],
					})
					// Fall through to normal flow (no ultimate model switch)
				} else {
					// Check if the per-hash attempt limit is exhausted
					// (strictly beyond the hardcoded 40-attempt cap)
					if result.AttemptsExhausted {
						// Resolve ultimate model ID with per-token override
						ultimateModelID := h.ultimateHandler.GetModelID()
						if rc.ultimateModelID != "" {
							ultimateModelID = rc.ultimateModelID
						}

						log.Printf("[UltimateModel] Attempt limit exhausted for hash=%s (attempt %d/%d)",
							result.Hash[:8], result.CurrentAttempt, result.MaxAttempts)

						// Determine if streaming
						isStream := false
						if stream, ok := rc.requestBody["stream"].(bool); ok {
							isStream = stream
						}

						// Update request log
						rc.reqLog.Status = "failed"
						rc.reqLog.Error = fmt.Sprintf("Request attempt limit exceeded (attempt %d/%d)", result.CurrentAttempt, result.MaxAttempts)
						rc.reqLog.EndTime = time.Now()
						rc.reqLog.Duration = time.Since(rc.startTime).String()
						rc.reqLog.UltimateModelUsed = true
						rc.reqLog.UltimateModelID = ultimateModelID
						h.store.Add(rc.reqLog)

						// Publish event.
						//
						// COMPATIBILITY NOTE: the payload keys "current_retry" /
						// "max_retries" are KEPT for frontend compatibility
						// (EventLog.tsx and any external consumers read them);
						// their values now mean TOTAL attempts against the
						// hardcoded 40 cap (e.g. 41/40), not the removed
						// max-retries config knob.
						h.publishEvent("ultimate_model_retry_exhausted", map[string]interface{}{
							"id":            rc.reqID,
							"hash":          result.Hash[:8],
							"current_retry": result.CurrentAttempt,
							"max_retries":   result.MaxAttempts,
						})

						// Send error response (HTTP 200 with JSON stream error)
						h.ultimateHandler.SendRetryExhaustedError(w, result.Hash, result.CurrentAttempt, result.MaxAttempts, isStream)
						return
					}

					// Resolve ultimate model ID with per-token override
					ultimateModelID := h.ultimateHandler.GetModelID()
					if rc.ultimateModelID != "" {
						ultimateModelID = rc.ultimateModelID
						log.Printf("[UltimateModel] Using per-token override model=%s (token=%s)", ultimateModelID, rc.tokenID)
					}
					log.Printf("[UltimateModel] Triggered for duplicate request (schedule milestone), using %s, hash=%s, attempt=%d/%d",
						ultimateModelID, result.Hash[:8], result.CurrentAttempt, result.MaxAttempts)

					// Note: Ultimate model access check was already done above in the ULTIMATE MODEL ACCESS CHECK section

					// Update request log with ultimate model info
					rc.reqLog.UltimateModelUsed = true
					rc.reqLog.UltimateModelID = ultimateModelID
					rc.reqLog.Status = "running"
					h.store.Add(rc.reqLog)

					// Publish event.
					//
					// COMPATIBILITY NOTE: the payload keys "current_retry" /
					// "max_retries" are KEPT for frontend compatibility; their
					// values now mean TOTAL attempts against the hardcoded 40
					// cap (milestone values are 5/10/20/30/40 of 40).
					h.publishEvent("ultimate_model_triggered", map[string]interface{}{
						"id":             rc.reqID,
						"ultimate_model": ultimateModelID,
						"original_model": rc.reqLog.Model,
						"hash":           result.Hash[:8],
						"current_retry":  result.CurrentAttempt,
						"max_retries":    result.MaxAttempts,
					})

					// Execute with ultimate model (raw proxy, no retry/fallback)
					// The Execute method determines streaming from requestBody["stream"]
					// Heartbeat is started by the ultimate handler after headers are sent
					//
					// execResult carries both the existing usage stats AND the
					// passively-captured assistant Content / Thinking (Fix 3 of
					// the reasoning-observability effort). The ultimatemodel
					// package observes bytes/events as they are written to the
					// client and never mutates the wire path — see
					// pkg/ultimatemodel/handler_capture_test.go for the
					// byte-identity proofs.
					//
					// Phase 3 / Task 8: conversationKey (post-auth wired at
					// handler.go:401+) is threaded down so ultimate-internal's
					// resolution can use the LB engine (Tasks 8/9); the
					// intermediate Execute gains the 9th positional arg with
					// no semantic change to trigger / capture behavior.
					//
					// Phase 4 / dispatcher addendum Item 1: thread the
					// per-request delivery mode into the variadic opts.
					// Execute's zero value = live (the header-absent
					// default), so omitting this arg forced ultimate
					// paths live even when the client opted into
					// buffering via X-LLMProxy-Buffer-Response —
					// violating the "header = current behavior"
					// guarantee. Purely additive: BufferMode=false keeps
					// Phase 4's live default verbatim.
					execResult, err := h.ultimateHandler.Execute(r.Context(), w, r, rc.requestBody, rc.reqLog.Model, result.Hash, &rc.headersSent, &ultimateModelID, rc.conversationKey,
						ultimatemodel.ExecuteOptions{BufferMode: rc.bufferMode})
					if err != nil {
						log.Printf("[UltimateModel] Error: %v", err)
						rc.reqLog.Status = "failed"
						rc.reqLog.Error = err.Error()
						rc.reqLog.EndTime = time.Now()
						rc.reqLog.Duration = time.Since(rc.startTime).String()
						h.store.Add(rc.reqLog)

						h.publishEvent("ultimate_model_failed", map[string]interface{}{
							"id":             rc.reqID,
							"ultimate_model": ultimateModelID,
							"error":          err.Error(),
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

					// Success - update log with usage
					rc.reqLog.Status = "completed"
					rc.reqLog.EndTime = time.Now()
					rc.reqLog.Duration = time.Since(rc.startTime).String()
					if execResult != nil {
						rc.reqLog.Usage = execResult.Usage
					}

					// Fix 3 (reasoning-observability): ultimate paths
					// previously persisted NO assistant message at all,
					// so the Web UI showed no reply (neither content nor
					// reasoning). Now we persist
					// store.Message{Role: assistant, Content, Thinking,
					// ToolCalls} from the passively-captured result.
					// Only persist when there is something to persist:
					// any of Content, Thinking, or ToolCalls must be
					// non-empty. An empty capture (e.g. an error path
					// that returned early without a body, or a
					// successful response with no observable assistant
					// turn at all) leaves the Messages slice as it was
					// so we don't pollute the Web UI with blank
					// assistant entries.
					//
					// W7 extends the Fix 3 conditional with the
					// ToolCalls presence check: a tool-call-only
					// assistant turn (empty Content + empty Thinking
					// + real tool_calls) used to fall through this
					// gate, leaving the UI blank for that turn. The
					// ExecuteResult.ToolCalls are populated by the
					// capture-side hooks at all four Execute paths.
					if execResult != nil &&
						(execResult.Content != "" ||
							execResult.Thinking != "" ||
							len(execResult.ToolCalls) > 0) {
						rc.reqLog.Messages = append(rc.reqLog.Messages, store.Message{
							Role:      "assistant",
							Content:   execResult.Content,
							Thinking:  execResult.Thinking,
							ToolCalls: execResult.ToolCalls,
						})
					}
					h.store.Add(rc.reqLog)

					// Fallback token counting: if provider didn't return usage, estimate from request
					if token.FallbackEnabled() {
						if rc.reqLog.Usage == nil || (rc.reqLog.Usage.PromptTokens == 0 && rc.reqLog.Usage.CompletionTokens == 0 && rc.reqLog.Usage.TotalTokens == 0) {
							tokenizer := token.GetTokenizer()
							model := ultimateModelID
							requestBytes, _ := json.Marshal(rc.requestBody)
							promptTokens, err := tokenizer.CountPromptTokens(requestBytes, model)
							if err != nil {
								log.Printf("failed to count prompt tokens: %v", err)
							} else {
								completionTokens := 0
								if rc.reqLog.Usage != nil {
									completionTokens = rc.reqLog.Usage.CompletionTokens
								}
								rc.reqLog.Usage = &store.Usage{
									PromptTokens:     promptTokens,
									CompletionTokens: completionTokens,
									TotalTokens:      promptTokens + completionTokens,
								}
								h.store.Add(rc.reqLog)
							}
						}
					}

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

					// Count this request for model hourly usage tracking
					if rc.reqLog.Model != "" && h.counter != nil {
						var promptTokens, completionTokens, totalTokens int
						if rc.reqLog.Usage != nil {
							promptTokens = rc.reqLog.Usage.PromptTokens
							completionTokens = rc.reqLog.Usage.CompletionTokens
							totalTokens = rc.reqLog.Usage.TotalTokens
						}
						hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
						go func() {
							if err := h.counter.IncrementModelUsage(context.Background(), rc.reqLog.Model, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
								log.Printf("failed to increment model usage counter: %v", err)
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
	}
	// === END ULTIMATE MODEL CHECK ===

	h.publishEvent("request_started", map[string]interface{}{"id": rc.reqID})

	// For streaming requests, send headers early and start heartbeat
	// This ensures client receives heartbeats during race retry wait time
	var heartbeatCancel context.CancelFunc
	if rc.isStream {
		// SSE headers — Cache-Control: no-cache alone is insufficient
		// for Cloudflare (and similar CDNs). `no-transform` is required
		// to disable CF's response buffering and asset optimization, otherwise
		// even frequent SSE heartbeats sit in CF's buffer until CF's
		// 100/180s read timeout triggers (524 — proxy disconnects the
		// client even though the upstream response is still streaming).
		// See: CF docs §"no-transform" + §"Streaming responses".
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true

		// Send initial connected message (no mutex needed for initial setup)
		fmt.Fprint(w, ": connected\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Create channel for heartbeat to signal client disconnection
		rc.clientGoneCh = make(chan struct{})

		// Use sync.Once to ensure clientGoneCh is only closed once
		// Even if heartbeat tries to send multiple times after disconnect
		var closeClientGoneCh sync.Once
		closeOnce := func() { closeClientGoneCh.Do(func() { close(rc.clientGoneCh) }) }

		// Start heartbeat - runs through race retry and streaming until request ends
		// Heartbeat checks connection every 5 seconds; on failure, closes clientGoneCh
		// Pass writeMu to synchronize heartbeat writes with streamResult writes
		heartbeatCancel = h.startSSEHeartbeat(w, &rc.writeMu, closeOnce)
	}

	// Ensure heartbeat is cancelled when this function returns
	defer func() {
		if heartbeatCancel != nil {
			heartbeatCancel()
		}
	}()

	// Unified Race Retry Design (Parallel Race)
	log.Printf("[RACE] Parallel race retry started for request %s", rc.reqID)
	// rc.interleavedThinking parsed at handler entry (handler.go:465+) is
	// threaded here so race-internal sites can read it via executeRequest
	// without taking a *requestContext dependency (B3 — executeInternalRequest
	// has neither rc nor *http.Request).
	//
	// Phase 3 / Round 3c C3 — the engine + conversationKey reach the
	// coordinator through this construction site:
	//   cmd/main.go → NewHandler(…, credLB) → Handler.credEngine → here
	// Constructor-only injection per W-3 holds end-to-end (no
	// Config.CredEngine second injection path).
	//
	// Phase 2 / real-streaming-default — compute liveFirstByteGate
	// HERE (NOT inside the constructor): isStream is a *requestContext
	// field, not a *ConfigSnapshot, and the constructor signature is
	// ConfigSnapshot-based. liveFirstByteGate is true only in real-
	// streaming mode (header absent AND isStream=true); the gate
	// redefines the winner-eligibility predicate from `IsCompleted()`
	// to "first forwardable data chunk". Buffered mode (header present)
	// and isStream=false requests keep the original IsCompleted gate
	// verbatim (M3 amendment, regression-pinned by
	// TestLiveRelay_NonStreamUnaffected).
	liveFirstByteGate := !rc.bufferMode && rc.isStream
	coordinator := newRaceCoordinatorWithEvents(rc.baseCtx, &rc.conf, r, rc.rawBody, rc.modelList, h.bus, rc.reqID, rc.interleavedThinking, h.credEngine, rc.conversationKey, liveFirstByteGate)
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
			if err := h.streamResult(w, rc, winner); err != nil {
				// Client write failed (e.g., Cloudflare/proxy dropped connection).
				// Not a race failure: the attempt counter for the hash was
				// already recorded at the early-exit gate, so a client retry
				// still progresses through the trigger schedule.
				log.Printf("[STREAM] Client write failed for request %s: %v", rc.reqID, err)

				// Cancel all other parallel requests to stop wasted upstream work.
				// The winner's cancel is handled by the defer below.
				coordinator.cancelAllExcept(winner)

				// Don't send error response - connection is already broken
				return
			}
		} else {
			// Send a single JSON response from the winner's buffer
			h.handleNonStreamResult(w, rc, winner)
		}
		return
	}

	// If winner is nil, it means either context cancelled or all models failed
	select {
	case <-rc.baseCtx.Done():
		// Context cancelled (timeout, client disconnect, etc.) - treat as failure
		log.Printf("Request %s context cancelled (timeout or client disconnect)", rc.reqID)
		h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "context_cancelled")
		return
	default:
		// All attempts failed with errors
		log.Printf("All models failed for request %s (Race Retry)", rc.reqID)
		h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "all_models_failed")
		return
	}
}

// handleRaceFailure handles the common failure case for race retry.
// It finalizes the failed request (log, event, error response).
// This is called when either all models failed with errors OR the context was cancelled.
func (h *Handler) handleRaceFailure(w http.ResponseWriter, rc *requestContext, coordinator *raceCoordinator, heartbeatCancel context.CancelFunc, reason string) {
	// Cancel heartbeat first to prevent concurrent writes with sendSSEError
	// This is safe to call even if nil (no-op)
	if heartbeatCancel != nil {
		heartbeatCancel()
	}
	// Note: no hash marking is needed here — the attempt counter is
	// recorded at request entry (early-exit gate), so a client retry
	// still progresses through the trigger schedule.

	// Get final error info from coordinator (OpenCode-compatible format)
	var errInfo FinalErrorInfo
	switch reason {
	case "context_cancelled":
		// Create error info for context cancellation
		errInfo = FinalErrorInfo{
			HTTPStatus: http.StatusGatewayTimeout,
			Message:    "Request timeout or client disconnected",
			ErrorType:  "timeout",
			ErrorCode:  "request_timeout",
		}
	case "all_models_failed":
		// First check if stream deadline fired with no content (specific timeout message)
		if deadlineErr := coordinator.GetStreamDeadlineError(); deadlineErr != nil {
			errInfo = *deadlineErr
		} else {
			errInfo = coordinator.GetFinalErrorInfo()
		}
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

	// If headers already sent (streaming request), send SSE error instead of HTTP error
	if rc.headersSent {
		h.sendSSEError(w, models.ErrorTypeServerError, errInfo.Message)
	} else {
		h.sendError(w, errInfo.HTTPStatus, errInfo.Message, errInfo.ErrorType, errInfo.ErrorCode)
	}
}

// streamResult flushes the winner's buffer to the client.
// Note: Headers and heartbeat are already set up in HandleChatCompletions.
// Returns an error if client write failed (e.g., connection dropped by Cloudflare/proxy).
func (h *Handler) streamResult(w http.ResponseWriter, rc *requestContext, winner *upstreamRequest) error {
	buffer := winner.GetBuffer()
	if buffer == nil {
		// Buffer released by Cancel() before relay started — the request
		// was cancelled (client disconnect / coordinator shutdown) and no
		// data can ever arrive. Streaming an empty 200 would hang the
		// relay loop on a permanently-closed notify channel.
		return fmt.Errorf("winner buffer released before streaming (request cancelled)")
	}
	readIndex := 0

	// Track if we've already sent an error (to avoid duplicates)
	idleTerminated := false

	// Capture raw bytes BEFORE any pruning happens - this is the complete response
	//
	// Phase 2 / real-streaming-default (task 2.4 / R2 fix) — gate the
	// entry raw-capture to BUFFERED mode. In live mode the relay runs
	// concurrently with Adds, so the entry capture would save a partial
	// prefix and double-save with the Done capture (:1194-1200 below).
	// The Done capture is authoritative in live mode (full stream,
	// single-save); entry capture is byte-identical to today's buffered
	// double-save.
	if rc.bufferMode && rc.conf.LogRawUpstreamResponse {
		if buf := winner.GetBuffer(); buf != nil {
			capturedBytes := buf.GetAllRawBytesOnce()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}

	flusher, _ := w.(http.Flusher)

	// Stream existing chunks first (with mutex to sync with heartbeat writes)
	chunks, _ := buffer.GetChunksFrom(readIndex)
	rc.writeMu.Lock()
	for _, chunk := range chunks {
		if _, err := w.Write(chunk); err != nil {
			rc.writeMu.Unlock()
			return fmt.Errorf("client write failed: %w", err)
		}
		// Extract content for logging
		if bytes.HasPrefix(chunk, []byte("data: ")) {
			data := bytes.TrimPrefix(chunk, []byte("data: "))
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
		}
		readIndex++
		// Phase 2 / real-streaming-default (task 2.5) — set
		// `streamingNonRetryable` on the FIRST successful client
		// write, inside the existing rc.writeMu critical section.
		// The atomic Store(true) is inherently idempotent — no
		// Load-before-Store guard exists — and the writeMu critical
		// section provides the ordering/visibility boundary between
		// the relay write and any concurrent reader (reset() clears
		// it per request lifecycle — see handler_helpers.go:156).
		// After this point: no racing / fallback / mid-stream
		// credential failover — the stream is live.
		rc.streamingNonRetryable.Store(true)
	}
	if flusher != nil {
		flusher.Flush()
	}
	rc.writeMu.Unlock()
	// Phase 2 / real-streaming-default (task 2.4 / R2 fix) — gate
	// Prune to BUFFERED mode. In live mode the relay runs concurrently
	// with Adds, so Prune would non-deterministically truncate the
	// raw bytes consumed by the usage estimator (line ~1244) and
	// the bufferstore Done capture (~:1194-1200). Buffered mode
	// keeps today's behavior byte-identical (Prune never fires
	// there today anyway — ShouldPrune is false when readIndex==len).
	if rc.bufferMode && buffer.ShouldPrune(readIndex) {
		buffer.Prune(readIndex)
	}

	// Continue streaming until complete
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-rc.baseCtx.Done():
			// Context cancelled - could be client disconnect OR timeout
			// Go's HTTP server cancels r.Context() on client disconnect
			if rc.baseCtx.Err() == context.Canceled {
				return fmt.Errorf("client disconnected (context cancelled by HTTP server)")
			}
			return nil // Deadline exceeded - handled by coordinator
		case <-rc.clientGoneCh:
			// Heartbeat detected client disconnection - signal as error so handler cancels all requests
			return fmt.Errorf("client disconnected (heartbeat detected)")
		case <-buffer.NotifyCh():
			// New data available
			chunks, _ = buffer.GetChunksFrom(readIndex)
			rc.writeMu.Lock()
			for _, chunk := range chunks {
				if _, err := w.Write(chunk); err != nil {
					rc.writeMu.Unlock()
					return fmt.Errorf("client write failed: %w", err)
				}
				// Extract content for logging
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
				}
				readIndex++
				// Phase 2 / task 2.5 — set streamingNonRetryable on
				// the first successful write (idempotent — atomic
				// Store is a no-op if already true; reset() in
				// handler_helpers.go clears it per request).
				rc.streamingNonRetryable.Store(true)
			}
			if flusher != nil {
				flusher.Flush()
			}
			rc.writeMu.Unlock()
			// Phase 2 / R2 fix — Prune gated to buffered mode (see
			// the entry-site comment at ~:1103 for rationale).
			if rc.bufferMode && buffer.ShouldPrune(readIndex) {
				buffer.Prune(readIndex)
			}
		case <-buffer.Done():
			// Stream complete - drain remaining data
			chunks, _ = buffer.GetChunksFrom(readIndex)
			rc.writeMu.Lock()
			for _, chunk := range chunks {
				if _, err := w.Write(chunk); err != nil {
					rc.writeMu.Unlock()
					return fmt.Errorf("client write failed: %w", err)
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
				// Phase 2 / task 2.5 — see entry-site comment.
				rc.streamingNonRetryable.Store(true)
			}
			if flusher != nil {
				flusher.Flush()
			}
			rc.writeMu.Unlock()

			// If stream failed, send error event to client
			if err := buffer.Err(); err != nil {
				// If idle termination already handled this, skip duplicate error
				if idleTerminated {
					log.Printf("[STREAM] Buffer closed after idle termination, skipping duplicate error")
					return nil
				}

				log.Printf("[ERROR] Stream buffer closed with error: %v", err)

				// Log raw response on error
				if rc.conf.LogRawUpstreamOnError {
					if buf := winner.GetBuffer(); buf != nil {
						capturedBytes := buf.GetAllRawBytesOnce()
						go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
					}
				}

				// Now safe to prune (after capturing raw response)
				// Phase 2 / R2 fix — Prune gated to buffered mode (see
				// the entry-site comment at ~:1103 for rationale). In
				// live mode the buffer is captured at ~:1206-1213 below
				// (Done case) and at the bufferstore Done site; this
				// error-path Prune is no longer needed in live mode.
				if rc.bufferMode {
					buffer.Prune(readIndex)
				}

				// Send OpenAI-compatible error response
				errResp := models.NewOpenAIError(models.ErrorTypeServerError, "", fmt.Sprintf("Streaming error: %v", err))
				data, _ := json.Marshal(errResp)
				rc.writeMu.Lock()
				fmt.Fprintf(w, "data: %s\n\n", string(data))
				if flusher != nil {
					flusher.Flush()
				}
				rc.writeMu.Unlock()
				return nil // Buffer error already handled with error response to client
			}

			// Log raw response on success if enabled - capture bytes BEFORE final pruning
			if rc.conf.LogRawUpstreamResponse {
				if buf := winner.GetBuffer(); buf != nil {
					capturedBytes := buf.GetAllRawBytesOnce()
					go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
				}
			}

			// Extract usage from the last SSE chunk (it contains the "usage" field)
			// IMPORTANT: Must do this BEFORE Prune, as Prune sets chunks to nil
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

			// Fallback token counting: if provider didn't return usage, estimate from buffer
			if token.FallbackEnabled() {
				if rc.reqLog.Usage == nil || (rc.reqLog.Usage.PromptTokens == 0 && rc.reqLog.Usage.CompletionTokens == 0 && rc.reqLog.Usage.TotalTokens == 0) {
					tokenizer := token.GetTokenizer()
					model := winner.GetModelID()
					if model == "" {
						model, _ = rc.requestBody["model"].(string)
					}
					requestBytes, _ := json.Marshal(rc.requestBody)
					promptTokens, err := tokenizer.CountPromptTokens(requestBytes, model)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, model)
					}

					rawBytes := winner.GetBuffer().GetAllRawBytesOnce()
					completionText := token.ExtractCompletionTextFromChunks(rawBytes)
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, model)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, model)
					}

					rc.reqLog.Usage = &store.Usage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					}
					log.Printf("[DEBUG][fallback-token-count] model=%s prompt=%d completion=%d total=%d (streaming)",
						model, promptTokens, completionTokens, promptTokens+completionTokens)
				}
			}

			// Now safe to prune (after extracting usage)
			// Phase 2 / R2 fix — Prune gated to buffered mode (see the
			// entry-site comment at ~:1103 for rationale). In live
			// mode, GetAllRawBytesOnce at line ~1252 was called BEFORE
			// this site, so the estimator has already seen the full
			// stream; further Prune would be a no-op for live mode but
			// keeps a single consistent shape across both modes.
			if rc.bufferMode {
				buffer.Prune(readIndex)
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

			// Count this request for model hourly usage tracking
			if rc.reqLog.Model != "" && h.counter != nil {
				var promptTokens, completionTokens, totalTokens int
				if rc.reqLog.Usage != nil {
					promptTokens = rc.reqLog.Usage.PromptTokens
					completionTokens = rc.reqLog.Usage.CompletionTokens
					totalTokens = rc.reqLog.Usage.TotalTokens
				}
				hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
				go func() {
					if err := h.counter.IncrementModelUsage(context.Background(), rc.reqLog.Model, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
						log.Printf("failed to increment model usage counter: %v", err)
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
			return nil
		case <-ticker.C:
			// Safety backup if notification missed
			chunks, _ = buffer.GetChunksFrom(readIndex)
			if len(chunks) > 0 {
				rc.writeMu.Lock()
				for _, chunk := range chunks {
					if _, err := w.Write(chunk); err != nil {
						rc.writeMu.Unlock()
						return fmt.Errorf("client write failed: %w", err)
					}
					if bytes.HasPrefix(chunk, []byte("data: ")) {
						data := bytes.TrimPrefix(chunk, []byte("data: "))
						extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
					}
					readIndex++
					// Phase 2 / task 2.5 — see entry-site comment.
					rc.streamingNonRetryable.Store(true)
				}
				if flusher != nil {
					flusher.Flush()
				}
				rc.writeMu.Unlock()
				// Phase 2 / R2 fix — Prune gated to buffered mode (see
				// the entry-site comment at ~:1103 for rationale).
				if rc.bufferMode && buffer.ShouldPrune(readIndex) {
					buffer.Prune(readIndex)
				}
			}

			// Idle termination: check if upstream has stopped sending data
			if rc.conf.IdleTerminationEnabled && rc.conf.IdleTerminationTimeout > 0 && !buffer.IsComplete() {
				if winner.IsIdle(rc.conf.IdleTerminationTimeout) {
					log.Printf("[STREAM] Idle termination: upstream idle for %v, terminating stream",
						time.Since(winner.GetLastActivity()))

					// Mark as terminated to avoid duplicate error in buffer.Done()
					idleTerminated = true

					// Cancel the winner to close the buffer and stop upstream goroutine.
					// Don't send SSE error - connection is already broken (Cloudflare dropped).
					// The buffer.Done() case will handle cleanup with idleTerminated guard.
					winner.Cancel()

					return nil
				}
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
				capturedBytes := buf.GetAllRawBytesOnce()
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

	// Extract content AND thinking for logging. The helper handles
	// reasoning_content / reasoning / thinking / provider_specific_fields.reasoning_content
	// variants in addition to plain string content, so non-stream race-path
	// responses now persist thinking alongside content (S3 cross-path parity).
	extractNonStreamContent(finalBody, &rc.accumulatedResponse, &rc.accumulatedThinking)

	// Extract usage from response
	var resp map[string]interface{}
	if err := json.Unmarshal(finalBody, &resp); err == nil {
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

		// Fallback token counting: if provider didn't return usage, estimate from response
		if token.FallbackEnabled() {
			if rc.reqLog.Usage == nil || (rc.reqLog.Usage.PromptTokens == 0 && rc.reqLog.Usage.CompletionTokens == 0 && rc.reqLog.Usage.TotalTokens == 0) {
				tokenizer := token.GetTokenizer()
				model := winner.GetModelID()
				if model == "" {
					model, _ = rc.requestBody["model"].(string)
				}
				requestBytes, _ := json.Marshal(rc.requestBody)
				promptTokens, err := tokenizer.CountPromptTokens(requestBytes, model)
				if err != nil {
					log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, model)
				}

				completionText := token.ExtractCompletionTextFromJSON(finalBody)
				completionTokens, err := tokenizer.CountCompletionTokens(completionText, model)
				if err != nil {
					log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, model)
				}

				rc.reqLog.Usage = &store.Usage{
					PromptTokens:     promptTokens,
					CompletionTokens: completionTokens,
					TotalTokens:      promptTokens + completionTokens,
				}
				log.Printf("[DEBUG][fallback-token-count] model=%s prompt=%d completion=%d total=%d (non-streaming)",
					model, promptTokens, completionTokens, promptTokens+completionTokens)
			}
		}
	}

	// Log success
	rc.reqLog.Status = "completed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()

	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  rc.accumulatedResponse.String(),
		Thinking: rc.accumulatedThinking.String(),
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

	// Count this request for model hourly usage tracking
	if rc.reqLog.Model != "" && h.counter != nil {
		var promptTokens, completionTokens, totalTokens int
		if rc.reqLog.Usage != nil {
			promptTokens = rc.reqLog.Usage.PromptTokens
			completionTokens = rc.reqLog.Usage.CompletionTokens
			totalTokens = rc.reqLog.Usage.TotalTokens
		}
		hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
		go func() {
			if err := h.counter.IncrementModelUsage(context.Background(), rc.reqLog.Model, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
				log.Printf("failed to increment model usage counter: %v", err)
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
			capturedBytes := buf.GetAllRawBytesOnce()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}
}
