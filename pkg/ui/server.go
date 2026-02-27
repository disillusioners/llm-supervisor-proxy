package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// Model represents a model with its fallback chain
type Model struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FallbackChain  []string `json:"fallback_chain"`
	TruncateParams []string `json:"truncate_params,omitempty"`
	// Internal upstream fields
	Internal         bool   `json:"internal"`
	InternalProvider string `json:"internal_provider,omitempty"`
	InternalAPIKey   string `json:"internal_api_key,omitempty"` // Write-only: accepted in POST/PUT, never returned in GET
	InternalBaseURL  string `json:"internal_base_url,omitempty"`
	InternalModel    string `json:"internal_model,omitempty"`
}

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	bus          *events.Bus
	configMgr    config.ManagerInterface      // Config manager
	proxyConfig  *proxy.Config                // Keep for models config access
	modelsConfig models.ModelsConfigInterface // Models config
	store        *store.RequestStore
	bufferStore  *bufferstore.BufferStore
	tokenStore   *auth.TokenStore
	mu           sync.Mutex
}

func NewServer(bus *events.Bus, configMgr config.ManagerInterface, proxyConfig *proxy.Config, modelsConfig models.ModelsConfigInterface, store *store.RequestStore, bufferStore *bufferstore.BufferStore, tokenStore *auth.TokenStore) *Server {
	return &Server{
		bus:          bus,
		configMgr:    configMgr,
		proxyConfig:  proxyConfig,
		modelsConfig: modelsConfig,
		store:        store,
		bufferStore:  bufferStore,
		tokenStore:   tokenStore,
	}
}

// version is set at build time via -ldflags (passed from main package)
var version = "dev"

// SetVersion allows the main package to set the version at startup
func SetVersion(v string) {
	version = v
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))

	// UI at /ui
	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/ui/")
		if path == "" || path == "index.html" {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try static file first
		f, err := staticFS.Open(path)
		if err == nil {
			f.Close()
			http.StripPrefix("/ui/", fileServer).ServeHTTP(w, r)
			return
		}
		// Fallback to index.html for SPA client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	// Redirect root to UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	// API at /fe/api
	mux.HandleFunc("/fe/api/version", s.handleVersion)
	mux.HandleFunc("/fe/api/config", s.handleConfig)
	mux.HandleFunc("/fe/api/models", s.handleModels)
	mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
	mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
	mux.HandleFunc("/fe/api/models/test", s.handleTestModel)
	mux.HandleFunc("/fe/api/events", s.handleEvents)
	mux.HandleFunc("/fe/api/requests", s.handleRequests)
	mux.HandleFunc("/fe/api/requests/", s.handleRequestDetail)
	mux.HandleFunc("/fe/api/buffers/", s.handleBufferContent)
	// Token management
	mux.HandleFunc("/fe/api/tokens", s.handleTokens)
	mux.HandleFunc("/fe/api/tokens/", s.handleTokenDetail)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": version})
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
	// /fe/api/requests/{id}
	id := r.URL.Path[len("/fe/api/requests/"):]
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

func (s *Server) handleBufferContent(w http.ResponseWriter, r *http.Request) {
	// /fe/api/buffers/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Path[len("/fe/api/buffers/"):]
	if id == "" {
		http.Error(w, "Missing buffer ID", http.StatusBadRequest)
		return
	}

	if s.bufferStore == nil {
		http.Error(w, "Buffer storage not configured", http.StatusServiceUnavailable)
		return
	}

	content, err := s.bufferStore.Get(id)
	if err != nil {
		http.Error(w, "Buffer not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
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

// handleModels handles GET and POST /fe/api/models
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		modelConfigs := s.modelsConfig.GetModels()
		models := make([]Model, len(modelConfigs))
		for i, mc := range modelConfigs {
			models[i] = Model{
				ID:               mc.ID,
				Name:             mc.Name,
				Enabled:          mc.Enabled,
				FallbackChain:    mc.FallbackChain,
				TruncateParams:   mc.TruncateParams,
				Internal:         mc.Internal,
				InternalProvider: mc.InternalProvider,
				InternalBaseURL:  mc.InternalBaseURL,
				InternalModel:    mc.InternalModel,
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
			ID:               newModel.ID,
			Name:             newModel.Name,
			Enabled:          newModel.Enabled,
			FallbackChain:    newModel.FallbackChain,
			TruncateParams:   newModel.TruncateParams,
			Internal:         newModel.Internal,
			InternalProvider: newModel.InternalProvider,
			InternalAPIKey:   newModel.InternalAPIKey,
			InternalBaseURL:  newModel.InternalBaseURL,
			InternalModel:    newModel.InternalModel,
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

// handleModelDetail handles PUT and DELETE /fe/api/models/{id}
func (s *Server) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	// /fe/api/models/{id}
	id := r.URL.Path[len("/fe/api/models/"):]
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
			ID:               updatedModel.ID,
			Name:             updatedModel.Name,
			Enabled:          updatedModel.Enabled,
			FallbackChain:    updatedModel.FallbackChain,
			TruncateParams:   updatedModel.TruncateParams,
			Internal:         updatedModel.Internal,
			InternalProvider: updatedModel.InternalProvider,
			InternalAPIKey:   updatedModel.InternalAPIKey,
			InternalBaseURL:  updatedModel.InternalBaseURL,
			InternalModel:    updatedModel.InternalModel,
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

// handleValidateModel handles POST /fe/api/models/validate
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

// TestModelRequest represents the request body for testing a model
type TestModelRequest struct {
	ModelID          string `json:"model_id,omitempty"` // Optional: use saved config from existing model
	InternalProvider string `json:"internal_provider"`
	InternalAPIKey   string `json:"internal_api_key"`
	InternalBaseURL  string `json:"internal_base_url"`
	InternalModel    string `json:"internal_model"`
}

// TestModelResponse represents the response for testing a model
type TestModelResponse struct {
	Success  bool   `json:"success"`
	Response string `json:"response,omitempty"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleTestModel handles POST /fe/api/models/test
func (s *Server) handleTestModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req TestModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid JSON: %v", err),
		})
		return
	}

	// If model_id is provided and api_key is empty, use saved config
	if req.ModelID != "" && req.InternalAPIKey == "" {
		savedModel := s.modelsConfig.GetModel(req.ModelID)
		if savedModel == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(TestModelResponse{
				Success: false,
				Error:   fmt.Sprintf("Model not found: %s", req.ModelID),
			})
			return
		}

		// Use saved values
		if req.InternalProvider == "" {
			req.InternalProvider = savedModel.InternalProvider
		}
		if req.InternalBaseURL == "" {
			req.InternalBaseURL = savedModel.InternalBaseURL
		}
		if req.InternalModel == "" {
			req.InternalModel = savedModel.InternalModel
		}
		req.InternalAPIKey = savedModel.InternalAPIKey // Use saved (decrypted) API key
	}

	// Validate required fields
	if req.InternalProvider == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   "internal_provider is required",
		})
		return
	}

	if req.InternalAPIKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   "internal_api_key is required (or provide model_id to use saved key)",
		})
		return
	}

	// Create provider using the factory
	provider, err := providers.NewProvider(req.InternalProvider, req.InternalAPIKey, req.InternalBaseURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to create provider: %v", err),
		})
		return
	}

	// Create a minimal chat completion request with "hi" message
	model := req.InternalModel
	if model == "" {
		model = "gpt-4o" // default model
	}

	chatReq := &providers.ChatCompletionRequest{
		Model: model,
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "hi",
			},
		},
	}

	// Send the request
	resp, err := provider.ChatCompletion(r.Context(), chatReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Request failed: %v", err),
		})
		return
	}

	// Extract response content
	var responseText string
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		responseText = resp.Choices[0].Message.Content
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TestModelResponse{
		Success:  true,
		Response: responseText,
		Model:    resp.Model,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Token Management Handlers
// ─────────────────────────────────────────────────────────────────────────────

// TokenResponse represents a token in API responses (without sensitive data)
type TokenResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	CreatedAt string  `json:"created_at"`
	CreatedBy string  `json:"created_by"`
}

// CreateTokenRequest represents the request body for creating a token
type CreateTokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"` // ISO 8601 format, optional
}

// CreateTokenResponse includes the plaintext token (shown only once)
type CreateTokenResponse struct {
	TokenResponse
	Token string `json:"token"` // Plaintext token - show only once!
}

// handleTokens handles GET (list) and POST (create) for tokens
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		s.listTokens(ctx, w, r)
	case http.MethodPost:
		s.createToken(ctx, w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTokenDetail handles DELETE for a specific token
func (s *Server) handleTokenDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	id := strings.TrimPrefix(r.URL.Path, "/fe/api/tokens/")
	if id == "" {
		http.Error(w, "Token ID required", http.StatusBadRequest)
		return
	}

	if s.tokenStore == nil {
		http.Error(w, "Token store not configured", http.StatusInternalServerError)
		return
	}

	err := s.tokenStore.DeleteToken(ctx, id)
	if err != nil {
		if err == auth.ErrTokenNotFound {
			http.Error(w, "Token not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to delete token: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTokens(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		// Return empty list if token store not configured
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TokenResponse{})
		return
	}

	tokens, err := s.tokenStore.ListTokens(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list tokens: %v", err), http.StatusInternalServerError)
		return
	}

	response := make([]TokenResponse, len(tokens))
	for i, t := range tokens {
		response[i] = TokenResponse{
			ID:        t.ID,
			Name:      t.Name,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
			CreatedBy: t.CreatedBy,
		}
		if t.ExpiresAt != nil {
			iso := t.ExpiresAt.Format(time.RFC3339)
			response[i].ExpiresAt = &iso
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) createToken(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		http.Error(w, "Token store not configured", http.StatusInternalServerError)
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Parse optional expiry
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			http.Error(w, "Invalid expires_at format (use ISO 8601)", http.StatusBadRequest)
			return
		}
		expiresAt = &t
	}

	// Get creator from request (could be from OAuth header in production)
	createdBy := "api"
	if user := r.Header.Get("X-User"); user != "" {
		createdBy = user
	}

	plaintext, token, err := s.tokenStore.CreateToken(ctx, req.Name, expiresAt, createdBy)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create token: %v", err), http.StatusInternalServerError)
		return
	}

	response := CreateTokenResponse{
		TokenResponse: TokenResponse{
			ID:        token.ID,
			Name:      token.Name,
			CreatedAt: token.CreatedAt.Format(time.RFC3339),
			CreatedBy: token.CreatedBy,
		},
		Token: plaintext, // Show once!
	}
	if token.ExpiresAt != nil {
		iso := token.ExpiresAt.Format(time.RFC3339)
		response.TokenResponse.ExpiresAt = &iso
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}
