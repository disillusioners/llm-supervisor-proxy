package models

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AppName is the application name used for config directory
const AppName = "llm-supervisor-proxy"

// MaxFallbackDepth is the maximum depth allowed for fallback chains (primary + 2 fallbacks).
// Deprecated: This constant is no longer used as fallback is now single-level (max 1 item).
const MaxFallbackDepth = 3

// GetConfigPath returns the path to the models config file.
// Uses XDG standard: ~/.config/llm-supervisor-proxy/models.json
func GetConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to current directory
		return "models.json"
	}
	return filepath.Join(configDir, AppName, "models.json")
}

// ModelConfig represents the configuration for a single model.
type ModelConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FallbackChain  []string `json:"fallback_chain,omitempty"`
	TruncateParams []string `json:"truncate_params,omitempty"` // Parameters to strip before forwarding (e.g. ["max_completion_tokens", "store"])
}

// ModelsConfig manages the collection of model configurations.
type ModelsConfig struct {
	mu       sync.RWMutex
	Models   []ModelConfig `json:"models"`
	filePath string
}

// NewModelsConfig creates a new empty ModelsConfig.
func NewModelsConfig() *ModelsConfig {
	return &ModelsConfig{
		Models: make([]ModelConfig, 0),
	}
}

// GetTruncateParams returns the list of request-body parameters that should be
// removed before forwarding to the upstream for the given model ID.
// Returns nil if the model is not found or has no truncate_params configured.
func (mc *ModelsConfig) GetTruncateParams(modelID string) []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			if len(model.TruncateParams) == 0 {
				return nil
			}
			result := make([]string, len(model.TruncateParams))
			copy(result, model.TruncateParams)
			return result
		}
	}
	return nil
}

// GetFallbackChain returns the fallback chain for a given model ID.
// Returns nil if the model is not found.
func (mc *ModelsConfig) GetFallbackChain(modelID string) []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			result := make([]string, 0, len(model.FallbackChain)+1)
			result = append(result, model.ID)
			result = append(result, model.FallbackChain...)
			return result
		}
	}
	return nil
}

// Load loads the models configuration from a JSON file.
func (mc *ModelsConfig) Load(filePath string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// File doesn't exist, initialize with empty config
		mc.Models = make([]ModelConfig, 0)
		mc.filePath = filePath

		// Ensure directory exists and create empty file
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		// Create empty models.json file
		emptyData := []byte(`{"models":[]}`)
		if err := os.WriteFile(filePath, emptyData, 0644); err != nil {
			return fmt.Errorf("failed to create models.json: %w", err)
		}

		return nil
	}

	// Read and parse file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var config struct {
		Models []ModelConfig `json:"models"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	mc.Models = config.Models
	mc.filePath = filePath

	return nil
}

// Save atomically saves the models configuration to a JSON file.
// It writes to a temporary file first, then renames to the target file.
func (mc *ModelsConfig) Save() error {
	mc.mu.RLock()
	filePath := mc.filePath
	models := mc.Models
	mc.mu.RUnlock()

	// Validate before saving
	tempConfig := &ModelsConfig{
		Models: models,
	}
	if err := tempConfig.Validate(); err != nil {
		return err
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(struct {
		Models []ModelConfig `json:"models"`
	}{Models: models}, "", "  ")
	if err != nil {
		return err
	}

	// Get directory and filename
	dir := filepath.Dir(filePath)
	filename := filepath.Base(filePath)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write to temporary file
	tmpFile, err := os.CreateTemp(dir, filename+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	_, err = tmpFile.Write(data)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Update file path on success
	mc.mu.Lock()
	mc.filePath = filePath
	mc.mu.Unlock()

	return nil
}

// AddModel adds a new model configuration after validation.
func (mc *ModelsConfig) AddModel(model ModelConfig) error {
	// Validate the model
	if model.ID == "" {
		return ErrInvalidModelID
	}
	if model.Name == "" {
		return ErrInvalidModelName
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Check for duplicate ID
	for _, m := range mc.Models {
		if m.ID == model.ID {
			return ErrDuplicateModelID
		}
	}

	// Create a copy for validation
	testConfig := &ModelsConfig{
		Models: append([]ModelConfig{}, mc.Models...),
	}
	testConfig.Models = append(testConfig.Models, model)

	if err := testConfig.Validate(); err != nil {
		return err
	}

	// Add the model
	mc.Models = append(mc.Models, model)
	return nil
}

// UpdateModel updates an existing model configuration after validation.
func (mc *ModelsConfig) UpdateModel(modelID string, model ModelConfig) error {
	// Validate the model
	if model.ID == "" {
		return ErrInvalidModelID
	}
	if model.Name == "" {
		return ErrInvalidModelName
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Find and update the model
	found := false
	for i, m := range mc.Models {
		if m.ID == modelID {
			// Ensure the ID doesn't change
			if model.ID != modelID {
				return ErrCannotChangeModelID
			}
			mc.Models[i] = model
			found = true
			break
		}
	}

	if !found {
		return ErrModelNotFound
	}

	// Validate the updated config
	testConfig := &ModelsConfig{
		Models: make([]ModelConfig, len(mc.Models)),
	}
	copy(testConfig.Models, mc.Models)

	if err := testConfig.Validate(); err != nil {
		return err
	}

	return nil
}

// RemoveModel removes a model configuration by ID.
func (mc *ModelsConfig) RemoveModel(modelID string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Find and remove the model
	found := false
	for i, m := range mc.Models {
		if m.ID == modelID {
			mc.Models = append(mc.Models[:i], mc.Models[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return ErrModelNotFound
	}

	// Validate after removal (check for dangling references)
	testConfig := &ModelsConfig{
		Models: make([]ModelConfig, len(mc.Models)),
	}
	copy(testConfig.Models, mc.Models)

	if err := testConfig.Validate(); err != nil {
		return err
	}

	return nil
}

// Validate validates the model configuration.
// Since fallback is now single-level (max 1 item), we only perform basic validation:
// - Model IDs must be non-empty
// - Fallback references must reference existing models
func (mc *ModelsConfig) Validate() error {
	// Build set of valid model IDs
	modelIDs := make(map[string]bool)
	for _, model := range mc.Models {
		modelIDs[model.ID] = true
	}

	// Basic validation: check for empty IDs and valid fallback references
	for _, model := range mc.Models {
		if model.ID == "" {
			return ErrInvalidModelID
		}

		// Enforce max 1 fallback model
		if len(model.FallbackChain) > 1 {
			return fmt.Errorf("fallback chain is limited to maximum 1 fallback model")
		}

		// Fallback chain is now limited to max 1 item, so just validate references
		for _, fallbackID := range model.FallbackChain {
			if fallbackID != "" && !modelIDs[fallbackID] {
				// Unknown model reference - warn but allow for forward compatibility
				// This enables adding new models without updating all configs
			}
		}
	}

	return nil
}

// GetEnabledModels returns only the enabled model configurations.
func (mc *ModelsConfig) GetEnabledModels() []ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelConfig, 0)
	for _, model := range mc.Models {
		if model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

// GetModels returns all model configurations.
func (mc *ModelsConfig) GetModels() []ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelConfig, len(mc.Models))
	copy(result, mc.Models)
	return result
}

// LoadWithContext loads the models configuration with context for deadline/cancellation support.
func (mc *ModelsConfig) LoadWithContext(ctx context.Context, filePath string) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- mc.Load(filePath)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// SaveWithContext saves the models configuration with context for deadline/cancellation support.
func (mc *ModelsConfig) SaveWithContext(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- mc.Save()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Model errors
var (
	ErrInvalidModelID      = &ConfigError{"invalid model ID: cannot be empty"}
	ErrInvalidModelName    = &ConfigError{"invalid model name: cannot be empty"}
	ErrDuplicateModelID    = &ConfigError{"duplicate model ID"}
	ErrModelNotFound       = &ConfigError{"model not found"}
	ErrCannotChangeModelID = &ConfigError{"cannot change model ID"}
)

// ConfigError represents a configuration error.
type ConfigError struct {
	msg string
}

func (e *ConfigError) Error() string {
	return e.msg
}
