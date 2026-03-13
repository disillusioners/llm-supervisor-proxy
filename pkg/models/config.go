package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Duration is a custom type that serializes to human-readable format (e.g., "1m50s")
// instead of nanoseconds. Required because time.Duration marshals to int64.
type Duration int64

// MarshalJSON serializes Duration to a human-readable string format
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON parses Duration from string or number
func (d *Duration) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case string:
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration format: %s", value)
		}
		if parsed < 0 {
			return errors.New("duration cannot be negative")
		}
		*d = Duration(parsed)
	case float64:
		if value < 0 {
			return errors.New("duration cannot be negative")
		}
		*d = Duration(time.Duration(value))
	default:
		return errors.New("invalid duration format")
	}
	return nil
}

// String returns the Duration as a human-readable string
func (d Duration) String() string {
	return time.Duration(d).String()
}

// Duration returns the time.Duration value
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

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

	// Internal upstream configuration (bypass external LiteLLM, call AI provider directly)
	Internal        bool   `json:"internal,omitempty"`
	CredentialID    string `json:"credential_id,omitempty"`     // Reference to credential (required if internal is true)
	InternalBaseURL string `json:"internal_base_url,omitempty"` // Base URL override (optional, uses credential's base_url if empty)
	InternalModel   string `json:"internal_model,omitempty"`    // Actual model name for provider (e.g., GLM-5.0)

	// ReleaseStreamChunkDeadline is the duration after which buffered stream chunks
	// should be flushed to downstream even if the stream hasn't completed.
	// This prevents clients with idle chunk detection from dropping the connection.
	// Example: "1m50s" (110 seconds). Set to 0 or omit to disable this feature.
	ReleaseStreamChunkDeadline Duration `json:"release_stream_chunk_deadline,omitempty"`
}

// GetReleaseStreamChunkDeadline returns the configured deadline duration.
// Returns 0 if not set (feature disabled), otherwise returns the configured duration.
// Note: The comment "Default: 1m50s" refers to the suggested value, but 0 means disabled.
func (m *ModelConfig) GetReleaseStreamChunkDeadline() time.Duration {
	if m.ReleaseStreamChunkDeadline == 0 {
		return 0 // Disabled - no deadline
	}
	return time.Duration(m.ReleaseStreamChunkDeadline)
}

// ModelsConfigInterface defines the interface for models configuration
// Both JSON and database-backed implementations must satisfy this interface
type ModelsConfigInterface interface {
	GetModels() []ModelConfig
	GetEnabledModels() []ModelConfig
	GetModel(modelID string) *ModelConfig
	GetTruncateParams(modelID string) []string
	GetFallbackChain(modelID string) []string
	AddModel(model ModelConfig) error
	UpdateModel(modelID string, model ModelConfig) error
	RemoveModel(modelID string) error
	Save() error
	Validate() error

	// Credential management
	GetCredential(id string) *CredentialConfig
	GetCredentials() []CredentialConfig
	AddCredential(cred CredentialConfig) error
	UpdateCredential(id string, cred CredentialConfig) error
	RemoveCredential(id string) error

	// Internal config resolution
	ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool)
}

// ModelsConfig manages the collection of model configurations.
type ModelsConfig struct {
	mu          sync.RWMutex
	Models      []ModelConfig      `json:"models"`
	Credentials *CredentialsConfig `json:"-"` // Credentials are managed separately
	filePath    string
}

// NewModelsConfig creates a new empty ModelsConfig.
func NewModelsConfig() *ModelsConfig {
	return &ModelsConfig{
		Models:      make([]ModelConfig, 0),
		Credentials: NewCredentialsConfig(),
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

// GetModel returns the model configuration for a given model ID.
// Returns nil if the model is not found.
func (mc *ModelsConfig) GetModel(modelID string) *ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			// Return a copy to avoid mutations
			copy := model
			return &copy
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
		mc.Credentials = NewCredentialsConfig()
		mc.filePath = filePath

		// Ensure directory exists and create empty file
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		// Create empty models.json file
		emptyData := []byte(`{"models":[],"credentials":[]}`)
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
		Models      []ModelConfig      `json:"models"`
		Credentials []CredentialConfig `json:"credentials"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	mc.Models = config.Models
	mc.Credentials = NewCredentialsConfig()
	mc.Credentials.SetCredentials(config.Credentials)
	mc.filePath = filePath

	return nil
}

// Save atomically saves the models configuration to a JSON file.
// It writes to a temporary file first, then renames to the target file.
func (mc *ModelsConfig) Save() error {
	mc.mu.RLock()
	filePath := mc.filePath
	models := mc.Models
	credentials := mc.Credentials
	mc.mu.RUnlock()

	// Validate before saving
	tempConfig := &ModelsConfig{
		Models:      models,
		Credentials: credentials,
	}
	if err := tempConfig.Validate(); err != nil {
		return err
	}

	// Get credentials slice for serialization
	var credsSlice []CredentialConfig
	if credentials != nil {
		credsSlice = credentials.ToSlice()
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(struct {
		Models      []ModelConfig      `json:"models"`
		Credentials []CredentialConfig `json:"credentials"`
	}{Models: models, Credentials: credsSlice}, "", "  ")
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
// - If internal is true, credential_id must reference an existing credential
func (mc *ModelsConfig) Validate() error {
	// Build set of valid model IDs
	modelIDs := make(map[string]bool)
	for _, model := range mc.Models {
		modelIDs[model.ID] = true
	}

	// Build set of valid credential IDs
	credentialIDs := make(map[string]bool)
	if mc.Credentials != nil {
		for _, cred := range mc.Credentials.GetCredentials() {
			credentialIDs[cred.ID] = true
		}
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

		// Validate internal upstream configuration
		if model.Internal {
			if model.CredentialID == "" {
				return fmt.Errorf("model %s: credential_id is required when internal is true", model.ID)
			}
			if !credentialIDs[model.CredentialID] {
				return fmt.Errorf("model %s: credential_id '%s' references non-existent credential", model.ID, model.CredentialID)
			}
			if model.InternalModel == "" {
				return fmt.Errorf("model %s: internal_model is required when internal is true", model.ID)
			}
		}
	}

	// Validate all credentials
	if mc.Credentials != nil {
		for _, cred := range mc.Credentials.GetCredentials() {
			if err := cred.Validate(); err != nil {
				return err
			}
		}
	}

	return nil
}

// IsInternal returns true if the model uses internal upstream
func (m *ModelConfig) IsInternal() bool {
	return m.Internal
}

// GetInternalConfig returns the internal upstream configuration.
// Note: This returns only the model-level config. Use ModelsConfig.ResolveInternalConfig()
// to get the full config including resolved credential.
func (m *ModelConfig) GetInternalConfig() (credentialID, provider, baseURL, model string, ok bool) {
	if !m.Internal {
		return "", "", "", "", false
	}
	return m.CredentialID, "", m.InternalBaseURL, m.InternalModel, true
}

// ResolveInternalConfig resolves the full internal upstream configuration including
// credentials. It returns the provider, apiKey, baseURL, and model name.
// The provider comes from the credential. The baseURL is taken from the model if specified, otherwise from the credential.
func (mc *ModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var modelConfig *ModelConfig
	for _, m := range mc.Models {
		if m.ID == modelID {
			copy := m
			modelConfig = &copy
			break
		}
	}

	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	// Get credential
	if mc.Credentials == nil {
		return "", "", "", "", false
	}

	cred := mc.Credentials.GetCredential(modelConfig.CredentialID)
	if cred == nil {
		return "", "", "", "", false
	}

	// Provider comes from credential only
	provider = cred.Provider

	// Resolve baseURL: model override > credential
	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	return provider, cred.APIKey, baseURL, modelConfig.InternalModel, true
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

// GetCredential returns the credential configuration for a given ID.
// Returns nil if the credential is not found.
func (mc *ModelsConfig) GetCredential(id string) *CredentialConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if mc.Credentials == nil {
		return nil
	}
	return mc.Credentials.GetCredential(id)
}

// GetCredentials returns all credential configurations.
func (mc *ModelsConfig) GetCredentials() []CredentialConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if mc.Credentials == nil {
		return []CredentialConfig{}
	}
	return mc.Credentials.GetCredentials()
}

// AddCredential adds a new credential configuration after validation.
func (mc *ModelsConfig) AddCredential(cred CredentialConfig) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		mc.Credentials = NewCredentialsConfig()
	}
	return mc.Credentials.AddCredential(cred)
}

// UpdateCredential updates an existing credential configuration after validation.
func (mc *ModelsConfig) UpdateCredential(id string, cred CredentialConfig) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		return ErrCredentialNotFound
	}
	return mc.Credentials.UpdateCredential(id, cred)
}

// RemoveCredential removes a credential configuration by ID.
// Returns an error if the credential is in use by any model.
func (mc *ModelsConfig) RemoveCredential(id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		return ErrCredentialNotFound
	}

	// Check if credential is in use
	for _, model := range mc.Models {
		if model.CredentialID == id {
			return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, ErrCredentialInUse)
		}
	}

	return mc.Credentials.RemoveCredential(id)
}
