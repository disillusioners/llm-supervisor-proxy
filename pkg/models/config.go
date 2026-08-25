package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// MaxCredentialRefs is the maximum number of credential references
// allowed per model. Multi-credential load balancing lives in
// Phase 2 (pkg/credentiallb); Phase 1 enforces the upper bound so
// the JSON column and the engine inputs stay aligned.
const MaxCredentialRefs = 16

// CredentialRef is a single (credential, weight, position) entry in a
// model's ordered, weighted credential list.
//
//   - CredentialID: references credentials.id (app-level FK).
//   - Weight: positive integer; the higher, the more often picked.
//     Default 1. Validation: must be > 0.
//   - Position: 0-based deterministic ordering. Used to break weight ties
//     (lower position wins) and to define the "primary" credential
//     (Credentials[0]).
type CredentialRef struct {
	CredentialID string `json:"credential_id"`
	Weight       int    `json:"weight"`
	Position     int    `json:"position"`
}

// ModelConfig represents the configuration for a single model.
type ModelConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FallbackChain  []string `json:"fallback_chain,omitempty"`
	TruncateParams []string `json:"truncate_params,omitempty"` // Parameters to strip before forwarding (e.g. ["max_completion_tokens", "store"])

	// Internal upstream configuration (bypass external LiteLLM, call AI provider directly)
	Internal        bool           `json:"internal,omitempty"`
	InternalBaseURL string         `json:"internal_base_url,omitempty"` // Base URL override (optional, uses credential's base_url if empty)
	InternalModel   string         `json:"internal_model,omitempty"`    // Actual model name for provider (e.g., GLM-5.0)

	// Credentials is the ordered, weighted list of credential refs. Empty for
	// external / non-internal models. Validation: if non-empty, every entry's
	// CredentialID must exist in the credentials table; every entry's weight
	// must be > 0; every entry's provider must match Credentials[0]'s provider
	// (provider-match invariant). Use PrimaryCredentialID() to read the
	// "primary" (back-compat single-credential view) — it returns
	// Credentials[0].CredentialID.
	Credentials []CredentialRef `json:"credentials,omitempty"`

	// ReleaseStreamChunkDeadline is the duration after which buffered stream chunks
	// should be flushed to downstream even if the stream hasn't completed.
	// This prevents clients with idle chunk detection from dropping the connection.
	// Example: "1m50s" (110 seconds). Set to 0 or omit to disable this feature.
	ReleaseStreamChunkDeadline Duration `json:"release_stream_chunk_deadline,omitempty"`

	// PeakHourConfig controls automatic model switching during peak hours.
	PeakHourEnabled  bool   `json:"peak_hour_enabled,omitempty"`
	PeakHourStart    string `json:"peak_hour_start,omitempty"`    // HH:MM format (local time)
	PeakHourEnd      string `json:"peak_hour_end,omitempty"`      // HH:MM format (local time)
	PeakHourTimezone string `json:"peak_hour_timezone,omitempty"` // UTC offset like +7, -5, +5.5
	PeakHourModel    string `json:"peak_hour_model,omitempty"`    // Upstream model name during peak hours

	// Secondary upstream model for retry logic (only valid for internal=true)
	SecondaryUpstreamModel string `json:"secondary_upstream_model,omitempty"`

	// ExcludeFromUltimateSwitching prevents this model from being used in ultimate model switching
	ExcludeFromUltimateSwitching bool `json:"exclude_from_ultimate_switching,omitempty"`
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

// PrimaryCredentialID returns the legacy single-credential view of the
// model's credentials list: the first ref's CredentialID, or "" if
// the model has no credentials configured.
//
// This is the single-credential fast path used by every existing
// call site (race-internal, ultimate-internal, ultimate-external's
// D3 provider probe) until the Phase 2 LB engine (pkg/credentiallb)
// wires the affinity-aware variant. Behavior is byte-identical to
// the pre-Phase-1 m.CredentialID reads when the model has exactly
// one credential; it returns "" for external models with empty
// Credentials (same as the old default string value).
func (m *ModelConfig) PrimaryCredentialID() string {
	if len(m.Credentials) == 0 {
		return ""
	}
	return m.Credentials[0].CredentialID
}

// ModelsConfigInterface defines the interface for models configuration
// Both JSON and database-backed implementations must satisfy this interface
type ModelsConfigInterface interface {
	GetModels() []ModelConfig
	GetEnabledModels() []ModelConfig
	GetModel(modelID string) *ModelConfig
	GetModelByName(modelName string) *ModelConfig
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

// GetModelByName returns the model configuration for a given model name.
// Returns nil if the model is not found.
func (mc *ModelsConfig) GetModelByName(modelName string) *ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.Name == modelName {
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
		Models:      append([]ModelConfig{}, mc.Models...),
		Credentials: mc.Credentials,
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
			// Phase 1: internal models must carry at least one credential ref.
			// Empty Credentials ⇒ keep the legacy error text verbatim so
			// downstream callers that grep the message keep working.
			if len(model.Credentials) == 0 {
				return fmt.Errorf("model %s: credential_id is required when internal is true", model.ID)
			}

			// Per-ref validation (Task 6 validation matrix).
			seen := make(map[string]bool, len(model.Credentials))
			var primaryCred *CredentialConfig
			for idx, ref := range model.Credentials {
				if ref.CredentialID == "" {
					return fmt.Errorf("model %s: credentials[%d].credential_id is empty", model.ID, idx)
				}
				if !credentialIDs[ref.CredentialID] {
					return fmt.Errorf("model %s: credential_id '%s' references non-existent credential", model.ID, ref.CredentialID)
				}
				if ref.Weight <= 0 {
					return fmt.Errorf("model %s: credentials[%d] (credential_id=%q): weight must be > 0, got %d", model.ID, idx, ref.CredentialID, ref.Weight)
				}
				if seen[ref.CredentialID] {
					return fmt.Errorf("model %s: duplicate credential_id '%s' in credentials list", model.ID, ref.CredentialID)
				}
				seen[ref.CredentialID] = true
			}
			if len(model.Credentials) > MaxCredentialRefs {
				return fmt.Errorf("model %s: credentials list exceeds max of %d refs", model.ID, MaxCredentialRefs)
			}

			// Provider-match invariant: every ref's credential must share
			// Credentials[0]'s provider (case-insensitive). Looks up via
			// mc.Credentials when available, else falls back to the
			// already-built credentialIDs set (best-effort: matches when
			// the credential row's Provider is the same — the database
			// Validate mirror builds the same lookup).
			if mc.Credentials != nil {
				primaryCred = mc.Credentials.GetCredential(model.Credentials[0].CredentialID)
			}
			if primaryCred != nil {
				primaryProvider := strings.ToLower(primaryCred.Provider)
				for idx, ref := range model.Credentials[1:] {
					refCred := mc.Credentials.GetCredential(ref.CredentialID)
					if refCred == nil {
						continue // already caught by the existence check above
					}
					if strings.ToLower(refCred.Provider) != primaryProvider {
						return fmt.Errorf("model %s: credentials[%d] (credential_id=%q) provider %q does not match primary provider %q",
							model.ID, idx+1, ref.CredentialID, refCred.Provider, primaryCred.Provider)
					}
				}
			}

			if model.InternalModel == "" {
				return fmt.Errorf("model %s: internal_model is required when internal is true", model.ID)
			}
		}
		// Note: non-internal models MAY carry Credentials — the
		// ultimate-external D3 provider probe reads Credentials[0] to
		// classify the upstream provider without injecting credentials.
		// (See ModelsConfig.Validate comment for the rationale on the
		// plan's "non-internal with creds ⇒ reject" rule deviation.)

		// Validate secondary upstream model
		if model.SecondaryUpstreamModel != "" {
			if !model.Internal {
				return fmt.Errorf("model %s: secondary_upstream_model requires internal to be true", model.ID)
			}
		}

		// Validate peak hour configuration
		if model.PeakHourEnabled {
			// Peak hours require internal upstream
			if !model.Internal {
				return fmt.Errorf("model %s: peak_hour_enabled requires internal to be true", model.ID)
			}

			// All peak hour fields must be provided
			if model.PeakHourStart == "" {
				return fmt.Errorf("model %s: peak_hour_start is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourEnd == "" {
				return fmt.Errorf("model %s: peak_hour_end is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourTimezone == "" {
				return fmt.Errorf("model %s: peak_hour_timezone is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourModel == "" {
				return fmt.Errorf("model %s: peak_hour_model is required when peak_hour_enabled is true", model.ID)
			}

			// Validate HH:MM format for start and end
			if err := ValidateTimeFormat(model.PeakHourStart); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_start: %w", model.ID, err)
			}
			if err := ValidateTimeFormat(model.PeakHourEnd); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_end: %w", model.ID, err)
			}

			// Validate UTC offset
			if err := ValidateUTCOffset(model.PeakHourTimezone); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_timezone: %w", model.ID, err)
			}

			// Reject same start and end times (would create empty or full-day window)
			if model.PeakHourStart == model.PeakHourEnd {
				return fmt.Errorf("model %s: peak_hour_start and peak_hour_end cannot be the same", model.ID)
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
	return m.PrimaryCredentialID(), "", m.InternalBaseURL, m.InternalModel, true
}

// ResolveInternalConfig resolves the full internal upstream configuration including
// credentials. It returns the provider, apiKey, baseURL, and model name.
// The provider comes from the credential. The baseURL is taken from the model if specified, otherwise from the credential.
//
// Legacy single-credential resolution — always uses Credentials[0].
// The Phase 2 LB engine (pkg/credentiallb) introduces
// ResolveInternalConfigWithAffinity for multi-credential models with
// conversation-sticky affinity; for Phase 1 this stays as the
// single-credential fast path (byte-identical to pre-change behavior).
func (mc *ModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	log.Printf("[PEAK-DBG] ResolveInternalConfig ENTRY: modelID=%q", modelID)

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
		log.Printf("[PEAK-DBG] ResolveInternalConfig: modelConfig=%v, Internal=%v", modelConfig != nil, modelConfig != nil && modelConfig.Internal)
		return "", "", "", "", false
	}

	log.Printf("[PEAK-DBG] ResolveInternalConfig: found modelConfig, ID=%q, PeakHourEnabled=%v, InternalModel=%q, PeakHourModel=%q",
		modelConfig.ID, modelConfig.PeakHourEnabled, modelConfig.InternalModel, modelConfig.PeakHourModel)

	// Get credential via the primary (single-credential fast path).
	primaryCredentialID := modelConfig.PrimaryCredentialID()
	if primaryCredentialID == "" {
		log.Printf("[PEAK-DBG] ResolveInternalConfig: modelConfig has no primary credential")
		return "", "", "", "", false
	}

	// Get credential
	if mc.Credentials == nil {
		log.Printf("[PEAK-DBG] ResolveInternalConfig: Credentials is nil")
		return "", "", "", "", false
	}

	cred := mc.Credentials.GetCredential(primaryCredentialID)
	if cred == nil {
		log.Printf("[PEAK-DBG] ResolveInternalConfig: credential %q not found", primaryCredentialID)
		return "", "", "", "", false
	}

	// Provider comes from credential only
	provider = cred.Provider

	// Resolve baseURL: model override > credential
	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	// Determine actual model: check peak hour first
	actualModel := modelConfig.InternalModel
	log.Printf("[PEAK-DBG] ResolveInternalConfig: before peak check, actualModel=%q", actualModel)

	if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s",
			modelConfig.ID, peakModel, modelConfig.InternalModel)
		log.Printf("[PEAK-DBG] ResolveInternalConfig: peak hour SUBSTITUTED %q -> %q", actualModel, peakModel)
		actualModel = peakModel
	} else {
		log.Printf("[PEAK-DBG] ResolveInternalConfig: no peak hour active, using internalModel=%q", actualModel)
	}

	log.Printf("[PEAK-DBG] ResolveInternalConfig EXIT: returning model=%q", actualModel)
	return provider, cred.APIKey, baseURL, actualModel, true
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
//
// In-use guard scans every model's Credentials slice (the new
// ordered, weighted list). When a single-credential model with the
// legacy CredentialID field would have been caught by `model.CredentialID
// == id`, the new guard catches it on `Credentials[i].CredentialID == id`
// (the primary ref). When the field-drop is in place, only the slice
// matters; both shapes remain covered for the deprecation window.
func (mc *ModelsConfig) RemoveCredential(id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		return ErrCredentialNotFound
	}

	// Check if credential is in use by any model
	for _, model := range mc.Models {
		for _, ref := range model.Credentials {
			if ref.CredentialID == id {
				return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, ErrCredentialInUse)
			}
		}
	}

	return mc.Credentials.RemoveCredential(id)
}
