package ultimatemodel

import (
	"context"
	"log"
	"net/http"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Handler manages ultimate model requests.
// It detects duplicate requests via message hash and triggers
// the ultimate model as a raw proxy, bypassing all normal logic.
type Handler struct {
	config    config.ManagerInterface
	modelsMgr models.ModelsConfigInterface // Database-backed
	hashCache *HashCache
	eventBus  *events.Bus
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
// It stores the hash and returns true if:
// 1. Ultimate model is configured (non-empty ModelID)
// 2. This request hash was already in cache (duplicate)
//
// Uses atomic StoreAndCheck to prevent race conditions.
// Returns (triggered, hash) where hash is the computed message hash.
func (h *Handler) ShouldTrigger(messages []map[string]interface{}) (bool, string) {
	cfg := h.config.Get()
	if cfg.UltimateModel.ModelID == "" {
		return false, ""
	}

	// Handle empty messages
	if len(messages) == 0 {
		return false, ""
	}

	// Generate hash from messages (role + content only)
	hash := HashMessages(messages)

	// StoreAndCheck: atomic store-first, returns true if was already present
	wasDuplicate := h.hashCache.StoreAndCheck(hash)

	// Trigger if this was a duplicate
	return wasDuplicate, hash
}

// GetModelID returns the configured ultimate model ID
func (h *Handler) GetModelID() string {
	return h.config.Get().UltimateModel.ModelID
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

// Execute handles request with ultimate model - RAW PROXY
// No retry, no fallback, no loop detection, no buffering.
// On failure, removes hash from cache to prevent infinite retry loop.
func (h *Handler) Execute(
	parentCtx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	originalModelID string,
	hash string,
	headersSent *bool,
) error {
	cfg := h.config.Get()
	modelID := cfg.UltimateModel.ModelID

	// Get model config from DATABASE
	modelCfg := h.modelsMgr.GetModel(modelID)
	if modelCfg == nil {
		// Remove hash to prevent infinite retry loop
		h.hashCache.Remove(hash)
		return &ultimateModelError{
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

	// Apply MaxRequestTime timeout using context
	maxRequestTime := cfg.MaxRequestTime.Duration()
	if maxRequestTime == 0 {
		// Default to MaxGenerationTime * 2 if not set
		maxRequestTime = cfg.MaxGenerationTime.Duration() * 2
	}

	// Create a context with deadline
	ctx, cancel := context.WithTimeout(parentCtx, maxRequestTime)
	defer cancel()

	// Route to internal or external handler
	var err error
	if modelCfg.Internal {
		err = h.executeInternal(ctx, w, requestBody, modelCfg, isStream)
	} else {
		err = h.executeExternal(ctx, w, r, requestBody, modelCfg, isStream)
	}

	if err != nil {
		// Remove hash on failure to prevent infinite retry loop
		h.hashCache.Remove(hash)
		log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
		return err
	}

	return nil
}

// ultimateModelError is an error type for ultimate model errors
type ultimateModelError struct {
	message  string
	internal bool
}

func (e *ultimateModelError) Error() string {
	return e.message
}
