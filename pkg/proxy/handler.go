package proxy

import (
	"context"
	"encoding/json"
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
)

// Config holds runtime configuration for the proxy handler
type Config struct {
	ConfigMgr    config.ManagerInterface      // Config manager for dynamic updates
	ModelsConfig models.ModelsConfigInterface // Models config for fallback chains
}

// Clone returns a snapshot of the current config values
func (c *Config) Clone() ConfigSnapshot {
	cfg := c.ConfigMgr.Get()
	return ConfigSnapshot{
		UpstreamURL:             cfg.UpstreamURL,
		UpstreamCredentialID:    cfg.UpstreamCredentialID,
		IdleTimeout:             cfg.IdleTimeout.Duration(),
		MaxGenerationTime:       cfg.MaxGenerationTime.Duration(),
		MaxUpstreamErrorRetries: cfg.MaxUpstreamErrorRetries,
		MaxIdleRetries:          cfg.MaxIdleRetries,
		MaxGenerationRetries:    cfg.MaxGenerationRetries,
		MaxStreamBufferSize:     cfg.MaxStreamBufferSize,
		ModelsConfig:            c.ModelsConfig,
		LoopDetection:           cfg.LoopDetection,
		ToolRepair:              cfg.ToolRepair,
	}
}

// ConfigSnapshot is an immutable snapshot of config values for a single request
type ConfigSnapshot struct {
	UpstreamURL             string
	UpstreamCredentialID    string
	IdleTimeout             time.Duration
	MaxGenerationTime       time.Duration
	MaxUpstreamErrorRetries int
	MaxIdleRetries          int
	MaxGenerationRetries    int
	MaxStreamBufferSize     int
	ModelsConfig            models.ModelsConfigInterface
	LoopDetection           config.LoopDetectionConfig
	ToolRepair              toolrepair.Config
}

type Handler struct {
	config      *Config
	bus         *events.Bus
	store       *store.RequestStore
	client      *http.Client
	bufferStore *bufferstore.BufferStore
	tokenStore  *auth.TokenStore
}

func NewHandler(config *Config, bus *events.Bus, store *store.RequestStore, bufferStore *bufferstore.BufferStore, tokenStore *auth.TokenStore) *Handler {
	return &Handler{
		config: config,
		bus:    bus,
		store:  store,
		client: &http.Client{
			Timeout: 5 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		bufferStore: bufferStore,
		tokenStore:  tokenStore,
	}
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

// authenticate validates the API key and returns true if valid
func (h *Handler) authenticate(r *http.Request) bool {
	// If tokenStore is nil, skip validation (auth disabled)
	if h.tokenStore == nil {
		return true
	}

	apiKey := h.extractAPIKey(r)
	if apiKey == "" {
		return false
	}

	// Create a timeout context for database query
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err := h.tokenStore.ValidateToken(ctx, apiKey)
	return err == nil
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
	json.NewEncoder(w).Encode(map[string]string{
		"error":   "invalid_api_key",
		"message": "Invalid or expired API key",
	})
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
		if !h.authenticate(r) {
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
	}

	h.publishEvent("request_started", map[string]interface{}{"id": rc.reqID})

	// Outer loop: iterate through models (original + fallbacks)
	for modelIndex, currentModel := range rc.modelList {
		if modelIndex > 0 {
			log.Printf("Attempting fallback model: %s (index %d)", currentModel, modelIndex)
		}

		if rc.baseCtx.Err() != nil {
			log.Printf("Client disconnected, failing request")
			break
		}

		rc.requestBody["model"] = currentModel

		success := h.attemptModel(w, rc, modelIndex, currentModel)
		if success {
			return
		}

		h.handleModelFailure(rc, modelIndex, currentModel)
	}

	// All models have failed
	if !rc.headersSent {
		log.Printf("All models failed, sending error response to client")
		h.publishEvent("all_models_failed", map[string]interface{}{"id": rc.reqID})
		http.Error(w, "All models failed after retries", http.StatusBadGateway)
	} else {
		// Headers already sent (streaming) - send SSE error event
		log.Printf("All models failed after headers sent, sending SSE error event")
		h.publishEvent("all_models_failed", map[string]interface{}{"id": rc.reqID})
		h.sendSSEError(w, "All models failed after retries")
	}
}
