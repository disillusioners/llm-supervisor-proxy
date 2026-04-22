package ultimatemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// ShouldTriggerResult contains the result of ShouldTrigger check
type ShouldTriggerResult struct {
	Triggered      bool   // True if ultimate model should be used
	Hash           string // The computed hash
	RetryExhausted bool   // True if max retries exceeded (after increment)
	CurrentRetry   int    // Current retry count (after increment)
	MaxRetries     int    // Configured max retries
}

// Handler manages ultimate model requests.
// It detects duplicate requests via message hash and triggers
// the ultimate model as a raw proxy, bypassing all normal logic.
type Handler struct {
	config    config.ManagerInterface
	modelsMgr models.ModelsConfigInterface // Database-backed
	hashCache *HashCache
	eventBus  *events.Bus

	// Tool call buffer configuration
	toolCallBufferMaxSize  int64              // Max size for tool call buffer
	toolCallBufferDisabled bool               // Disable tool call buffering
	toolRepairConfig       *toolrepair.Config // Tool repair config for buffer
}

// NewHandler creates a new ultimate model handler
func NewHandler(cfg config.ManagerInterface, modelsMgr models.ModelsConfigInterface, eventBus *events.Bus) *Handler {
	maxHash := cfg.Get().UltimateModel.MaxHash
	if maxHash <= 0 {
		maxHash = 100
	}

	return &Handler{
		config:    cfg,
		modelsMgr: modelsMgr,
		hashCache: NewHashCache(maxHash),
		eventBus:  eventBus,
	}
}

// ShouldTrigger checks if ultimate model should be triggered.
// It ONLY checks if hash exists (does NOT store hash).
// Returns true if:
// 1. Ultimate model is configured (non-empty ModelID)
// 2. This request hash was already in cache (previously failed)
//
// IMPORTANT: Hash is only stored when MarkFailed is called after a failure.
// This prevents simple messages like "hi", "hello" from triggering ultimate model.
func (h *Handler) ShouldTrigger(messages []map[string]interface{}) ShouldTriggerResult {
	return h.shouldTriggerInternal(messages, false)
}

// ForceTrigger always triggers ultimate model, bypassing hash cache check.
// Used for testing/debugging via X-Force-Ultimate-Model header.
func (h *Handler) ForceTrigger(messages []map[string]interface{}) ShouldTriggerResult {
	return h.shouldTriggerInternal(messages, true)
}

func (h *Handler) shouldTriggerInternal(messages []map[string]interface{}, force bool) ShouldTriggerResult {
	cfg := h.config.Get()
	if cfg.UltimateModel.ModelID == "" {
		return ShouldTriggerResult{Triggered: false}
	}

	// Handle empty messages
	if len(messages) == 0 {
		return ShouldTriggerResult{Triggered: false}
	}

	// Generate hash from messages (role + content only)
	hash := HashMessages(messages)

	// Force mode bypasses hash cache check
	if !force && !h.hashCache.Contains(hash) {
		return ShouldTriggerResult{Triggered: false, Hash: hash}
	}

	// Get max retries config
	maxRetries := cfg.UltimateModel.MaxRetries
	if maxRetries <= 0 {
		// MaxRetries=0 means unlimited - don't track retries
		return ShouldTriggerResult{
			Triggered:      true,
			Hash:           hash,
			RetryExhausted: false,
			CurrentRetry:   0,
			MaxRetries:     0,
		}
	}

	// ATOMIC increment and check - prevents race condition
	newCount, exhausted := h.hashCache.IncrementAndCheckRetry(hash, maxRetries)

	return ShouldTriggerResult{
		Triggered:      true,
		Hash:           hash,
		RetryExhausted: exhausted,
		CurrentRetry:   newCount,
		MaxRetries:     maxRetries,
	}
}

// MarkFailed stores the hash to mark this request as failed.
// This should be called when a normal request fails, so subsequent
// retries can trigger the ultimate model.
// Returns the computed hash for reference.
func (h *Handler) MarkFailed(messages []map[string]interface{}) string {
	if len(messages) == 0 {
		return ""
	}

	hash := HashMessages(messages)
	h.hashCache.StoreAndCheck(hash) // Store hash to mark as failed
	return hash
}

// GetModelID returns the configured ultimate model ID
func (h *Handler) GetModelID() string {
	return h.config.Get().UltimateModel.ModelID
}

// SetToolCallBufferConfig sets the tool call buffer configuration
func (h *Handler) SetToolCallBufferConfig(maxSize int64, disabled bool, repairConfig *toolrepair.Config) {
	h.toolCallBufferMaxSize = maxSize
	h.toolCallBufferDisabled = disabled
	h.toolRepairConfig = repairConfig
}

// OnConfigChange handles config change events.
// When ultimate_model.model_id changes, reset the hash cache.
func (h *Handler) OnConfigChange(event events.Event) {
	// Get previous config from event data if available
	if data, ok := event.Data.(map[string]interface{}); ok {
		if field, ok := data["field"].(string); ok && field == "ultimate_model.model_id" {
			log.Printf("[UltimateModel] Model ID changed, resetting hash cache")
			h.hashCache.Reset()
		}
	}
}

// SendRetryExhaustedError sends a JSON stream error response.
// This uses HTTP 200 with SSE error format to make streaming clients stop gracefully.
func (h *Handler) SendRetryExhaustedError(
	w http.ResponseWriter,
	hash string,
	currentRetry int,
	maxRetries int,
	isStream bool,
) error {
	// Safely extract hash prefix (defensive against short/empty hashes)
	hashPrefix := hash
	if len(hash) > 8 {
		hashPrefix = hash[:8]
	}

	message := fmt.Sprintf(
		"Ultimate model retry limit exceeded (attempt %d of %d max). Hash: %s...",
		currentRetry, maxRetries, hashPrefix,
	)

	// Build OpenCode-compatible error response
	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "ultimate_model_retry_exhausted",
			"code":    "exhausted",
			"message": message,
			"hash":    hash,
		},
	}

	errorJSON, err := json.Marshal(errorResp)
	if err != nil {
		// Fallback to static error message if marshaling fails
		errorJSON = []byte(`{"type":"error","error":{"type":"ultimate_model_retry_exhausted","code":"exhausted","message":"Ultimate model retry limit exceeded"}}`)
	}

	// Set headers based on response type FIRST
	if isStream {
		// SSE format for streaming requests
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		fmt.Fprintf(w, "data: %s\n\n", string(errorJSON))
		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		// Regular JSON response for non-streaming
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
		w.Write(errorJSON)
	}

	// Flush if possible
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// Execute handles request with ultimate model - RAW PROXY
// No retry, no fallback, no loop detection, no buffering.
// On failure: KEEPS retry counter to enforce max retry limit.
// On success: clears retry counter but keeps hash in cache.
// Returns usage statistics extracted from the response.
func (h *Handler) Execute(
	parentCtx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	originalModelID string,
	hash string,
	headersSent *bool,
) (*store.Usage, error) {
	cfg := h.config.Get()
	modelID := cfg.UltimateModel.ModelID

	// Get model config from DATABASE
	modelCfg := h.modelsMgr.GetModel(modelID)
	if modelCfg == nil {
		// Model not found - this is a config error, clear everything
		h.hashCache.Remove(hash) // Also clears retry counter
		return nil, &ultimateModelError{
			message:  "ultimate model not found in database",
			internal: false,
		}
	}

	// Set response header to indicate ultimate mode
	w.Header().Set("X-LLMProxy-Ultimate-Model", modelID)
	*headersSent = true

	// Check if streaming
	isStream := false
	if stream, ok := requestBody["stream"].(bool); ok {
		isStream = stream
	}

	// Apply MaxGenerationTime as the absolute hard timeout
	ctx, cancel := context.WithTimeout(parentCtx, cfg.MaxGenerationTime.Duration())
	defer cancel()

	// Marshal request body for token counting
	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Route to internal or external handler
	var usage *store.Usage
	if modelCfg.Internal {
		usage, err = h.executeInternal(ctx, w, requestBody, requestBodyBytes, modelCfg, isStream)
	} else {
		usage, err = h.executeExternal(ctx, w, r, requestBody, requestBodyBytes, modelCfg, isStream)
	}

	if err != nil {
		// On failure: KEEP retry counter to enforce limit
		// DON'T remove hash - client can retry until MaxRetries exhausted
		log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
		return nil, err
	}

	// On success: clear retry counter but keep hash in cache
	// This prevents immediate re-triggering of ultimate model for same content
	h.hashCache.ClearRetryCount(hash)

	return usage, nil
}

// ultimateModelError is an error type for ultimate model errors
type ultimateModelError struct {
	message  string
	internal bool
}

func (e *ultimateModelError) Error() string {
	return e.message
}
