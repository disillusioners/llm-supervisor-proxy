package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// Model represents a model with its fallback chain
type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	FallbackChain []string `json:"fallback_chain"`
}

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	bus          *events.Bus
	configMgr    *config.Manager // NEW: config manager
	proxyConfig  *proxy.Config   // Keep for models config access
	modelsConfig *models.ModelsConfig
	store        *store.RequestStore
	mu           sync.Mutex
}

func NewServer(bus *events.Bus, configMgr *config.Manager, proxyConfig *proxy.Config, modelsConfig *models.ModelsConfig, store *store.RequestStore) *Server {
	return &Server{
		bus:          bus,
		configMgr:    configMgr,
		proxyConfig:  proxyConfig,
		modelsConfig: modelsConfig,
		store:        store,
	}
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f, err := staticFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	// API
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/models/", s.handleModelDetail)
	mux.HandleFunc("/api/models/validate", s.handleValidateModel)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/requests/", s.handleRequestDetail)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requests := s.store.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requests)
}

func (s *Server) handleRequestDetail(w http.ResponseWriter, r *http.Request) {
	// /api/requests/{id}
	id := r.URL.Path[len("/api/requests/"):]
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	req := s.store.Get(id)
	if req == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.configMgr.Get())
		return

	case http.MethodPut: // Changed from POST to RESTful convention
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Clear read-only fields (prevent client manipulation)
		cfg.Version = ""   // Will be set by Save()
		cfg.UpdatedAt = "" // Will be set by Save()

		result, err := s.configMgr.Save(cfg)
		if err != nil {
			if strings.Contains(err.Error(), "validation failed") {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else if strings.Contains(err.Error(), "read-only") {
				http.Error(w, "Config file is read-only", http.StatusForbidden)
			} else {
				http.Error(w, fmt.Sprintf("Failed to save: %v", err), http.StatusInternalServerError)
			}
			return
		}

		// Include restart hint in response
		response := struct {
			config.Config
			RestartRequired bool     `json:"restart_required"`
			ChangedFields   []string `json:"changed_fields,omitempty"`
		}{
			Config:          s.configMgr.Get(),
			RestartRequired: result.RestartRequired,
			ChangedFields:   result.ChangedFields,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub := s.bus.Subscribe()

	// Clean up on exit
	// We need to know when client disconnects
	ctx := r.Context()

	// Add heartbeat ticker to keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case evt := <-sub:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			// Send comment heartbeat
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-ctx.Done():
			s.bus.Unsubscribe(sub)
			return
		}
	}
}

// handleModels handles GET and POST /api/models
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		modelConfigs := s.modelsConfig.GetModels()
		models := make([]Model, len(modelConfigs))
		for i, mc := range modelConfigs {
			models[i] = Model{
				ID:            mc.ID,
				Name:          mc.Name,
				Enabled:       mc.Enabled,
				FallbackChain: mc.FallbackChain,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)

	case http.MethodPost:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var newModel Model
		if err := json.NewDecoder(r.Body).Decode(&newModel); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate model
		if newModel.Name == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
			return
		}

		// Generate ID if not provided
		if newModel.ID == "" {
			newModel.ID = fmt.Sprintf("model-%d", time.Now().UnixNano())
		}

		// Convert to models.ModelConfig
		modelConfig := models.ModelConfig{
			ID:            newModel.ID,
			Name:          newModel.Name,
			Enabled:       newModel.Enabled,
			FallbackChain: newModel.FallbackChain,
		}

		if err := s.modelsConfig.AddModel(modelConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(newModel)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleModelDetail handles PUT and DELETE /api/models/{id}
func (s *Server) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	// /api/models/{id}
	id := r.URL.Path[len("/api/models/"):]
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var updatedModel Model
		if err := json.NewDecoder(r.Body).Decode(&updatedModel); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate model
		if updatedModel.Name == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
			return
		}

		// Keep the same ID
		updatedModel.ID = id

		// Convert to models.ModelConfig and update
		modelConfig := models.ModelConfig{
			ID:            updatedModel.ID,
			Name:          updatedModel.Name,
			Enabled:       updatedModel.Enabled,
			FallbackChain: updatedModel.FallbackChain,
		}

		if err := s.modelsConfig.UpdateModel(id, modelConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updatedModel)

	case http.MethodDelete:
		if err := s.modelsConfig.RemoveModel(id); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleValidateModel handles POST /api/models/validate
func (s *Server) handleValidateModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var model Model
	if err := json.NewDecoder(r.Body).Decode(&model); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Validate model name
	if model.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
		return
	}

	// Validate fallback chain - check that all models in the chain exist
	modelConfigs := s.modelsConfig.GetModels()

	existingModels := make(map[string]bool)
	for _, m := range modelConfigs {
		existingModels[m.Name] = true
	}

	// Add the current model being validated to the check
	existingModels[model.Name] = true

	var validationErrors []string
	for _, fallback := range model.FallbackChain {
		if !existingModels[fallback] {
			validationErrors = append(validationErrors, fmt.Sprintf("fallback model '%s' not found", fallback))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if len(validationErrors) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"valid":  false,
			"errors": validationErrors,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid": true,
	})
}
