package proxy

import (
	"log"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
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
		IdleTimeout:             cfg.IdleTimeout.Duration(),
		MaxGenerationTime:       cfg.MaxGenerationTime.Duration(),
		MaxUpstreamErrorRetries: cfg.MaxUpstreamErrorRetries,
		MaxIdleRetries:          cfg.MaxIdleRetries,
		MaxGenerationRetries:    cfg.MaxGenerationRetries,
		MaxStreamBufferSize:     cfg.MaxStreamBufferSize,
		ModelsConfig:            c.ModelsConfig,
		LoopDetection:           cfg.LoopDetection,
	}
}

// ConfigSnapshot is an immutable snapshot of config values for a single request
type ConfigSnapshot struct {
	UpstreamURL             string
	IdleTimeout             time.Duration
	MaxGenerationTime       time.Duration
	MaxUpstreamErrorRetries int
	MaxIdleRetries          int
	MaxGenerationRetries    int
	MaxStreamBufferSize     int
	ModelsConfig            models.ModelsConfigInterface
	LoopDetection           config.LoopDetectionConfig
}

type Handler struct {
	config *Config
	bus    *events.Bus
	store  *store.RequestStore
	client *http.Client
}

func NewHandler(config *Config, bus *events.Bus, store *store.RequestStore) *Handler {
	return &Handler{
		config: config,
		bus:    bus,
		store:  store,
		client: &http.Client{},
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
