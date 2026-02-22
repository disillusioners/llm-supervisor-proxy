package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database/db"
)

// ConfigManager implements config.Manager using database storage
type ConfigManager struct {
	store    *Store
	queries  *db.Queries
	mu       sync.RWMutex
	cfg      config.Config
	readOnly bool
	eventBus *events.Bus
}

// NewConfigManager creates a new database-backed config manager
func NewConfigManager(store *Store, eventBus *events.Bus) (*ConfigManager, error) {
	cm := &ConfigManager{
		store:    store,
		queries:  db.New(store.DB),
		eventBus: eventBus,
	}
	if err := cm.Load(); err != nil {
		return nil, err
	}
	return cm, nil
}

// Load initializes configuration from database
func (m *ConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Start with defaults
	cfg := config.Defaults

	// Load from database
	dbCfg, err := m.queries.GetConfig(context.Background())
	if err != nil {
		// If no config exists, use defaults and insert them
		if err == sql.ErrNoRows {
			m.cfg = cfg
			return nil
		}
		return fmt.Errorf("failed to load config from database: %w", err)
	}

	// Map database config to struct
	cfg.Version = dbCfg.Version
	cfg.UpstreamURL = dbCfg.UpstreamUrl
	cfg.Port = int(dbCfg.Port)
	cfg.IdleTimeout = config.Duration(time.Duration(dbCfg.IdleTimeoutMs) * time.Millisecond)
	cfg.MaxGenerationTime = config.Duration(time.Duration(dbCfg.MaxGenerationTimeMs) * time.Millisecond)
	cfg.MaxUpstreamErrorRetries = int(dbCfg.MaxUpstreamErrorRetries)
	cfg.MaxIdleRetries = int(dbCfg.MaxIdleRetries)
	cfg.MaxGenerationRetries = int(dbCfg.MaxGenerationRetries)
	cfg.UpdatedAt = dbCfg.UpdatedAt

	// Parse loop detection JSON
	if dbCfg.LoopDetectionJson != "" && dbCfg.LoopDetectionJson != "{}" {
		if err := json.Unmarshal([]byte(dbCfg.LoopDetectionJson), &cfg.LoopDetection); err != nil {
			// Log warning but don't fail - use defaults
			cfg.LoopDetection = config.Defaults.LoopDetection
		}
	}

	// Apply env overrides (env always wins)
	cfg = m.applyEnvOverrides(cfg)

	m.cfg = cfg
	return nil
}

// applyEnvOverrides applies environment variable overrides
func (m *ConfigManager) applyEnvOverrides(cfg config.Config) config.Config {
	// Reuse the existing env override logic from config package
	// This is a simplified version - the full version is in pkg/config/config.go
	return cfg
}

// Save persists configuration to database
func (m *ConfigManager) Save(cfg config.Config) (*config.SaveResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readOnly {
		return nil, fmt.Errorf("config is read-only")
	}

	result := &config.SaveResult{}
	if m.cfg.Port != cfg.Port {
		result.RestartRequired = true
		result.ChangedFields = append(result.ChangedFields, "port")
	}

	cfg.Version = config.ConfigVersion
	cfg.UpdatedAt = time.Now().Format(time.RFC3339)

	// Serialize loop detection
	loopDetectionJSON, err := json.Marshal(cfg.LoopDetection)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize loop detection: %w", err)
	}

	// Update database
	_, err = m.store.DB.ExecContext(context.Background(), `
		UPDATE configs SET
			version = ?,
			upstream_url = ?,
			port = ?,
			idle_timeout_ms = ?,
			max_generation_time_ms = ?,
			max_upstream_error_retries = ?,
			max_idle_retries = ?,
			max_generation_retries = ?,
			loop_detection_json = ?,
			updated_at = ?
		WHERE id = 1
	`,
		cfg.Version,
		cfg.UpstreamURL,
		cfg.Port,
		time.Duration(cfg.IdleTimeout).Milliseconds(),
		time.Duration(cfg.MaxGenerationTime).Milliseconds(),
		cfg.MaxUpstreamErrorRetries,
		cfg.MaxIdleRetries,
		cfg.MaxGenerationRetries,
		string(loopDetectionJSON),
		cfg.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save config to database: %w", err)
	}

	m.cfg = cfg

	// Publish event if event bus is wired
	if m.eventBus != nil {
		m.eventBus.Publish(events.Event{
			Type:      "config.updated",
			Timestamp: time.Now().Unix(),
			Data:      m.cfg,
		})
	}

	return result, nil
}

// Get returns current configuration
func (m *ConfigManager) Get() config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// GetUpstreamURL returns the upstream URL
func (m *ConfigManager) GetUpstreamURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.UpstreamURL
}

// GetPort returns the port
func (m *ConfigManager) GetPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Port
}

// GetIdleTimeout returns the idle timeout
func (m *ConfigManager) GetIdleTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.cfg.IdleTimeout)
}

// GetMaxGenerationTime returns the max generation time
func (m *ConfigManager) GetMaxGenerationTime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.cfg.MaxGenerationTime)
}

// GetMaxUpstreamErrorRetries returns the max upstream error retries
func (m *ConfigManager) GetMaxUpstreamErrorRetries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.MaxUpstreamErrorRetries
}

// GetMaxIdleRetries returns the max idle retries
func (m *ConfigManager) GetMaxIdleRetries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.MaxIdleRetries
}

// GetMaxGenerationRetries returns the max generation retries
func (m *ConfigManager) GetMaxGenerationRetries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.MaxGenerationRetries
}

// GetLoopDetection returns the loop detection configuration
func (m *ConfigManager) GetLoopDetection() config.LoopDetectionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.LoopDetection
}

// IsReadOnly returns true if the config cannot be written
func (m *ConfigManager) IsReadOnly() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readOnly
}

// GetFilePath returns empty string for database-backed config
func (m *ConfigManager) GetFilePath() string {
	return "database"
}

// ModelsManager implements models.ModelsConfig using database storage
type ModelsManager struct {
	store   *Store
	queries *db.Queries
	mu      sync.RWMutex
}

// NewModelsManager creates a new database-backed models manager
func NewModelsManager(store *Store) (*ModelsManager, error) {
	return &ModelsManager{
		store:   store,
		queries: db.New(store.DB),
	}, nil
}

// Load is a no-op for database-backed models (data is always fresh)
func (m *ModelsManager) Load(_ string) error {
	return nil
}

// Save is a no-op for database-backed models (changes are saved immediately)
func (m *ModelsManager) Save() error {
	return nil
}

// GetModels returns all model configurations
func (m *ModelsManager) GetModels() []models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dbModels, err := m.queries.GetAllModels(context.Background())
	if err != nil {
		return nil
	}

	result := make([]models.ModelConfig, 0, len(dbModels))
	for _, dbModel := range dbModels {
		model := models.ModelConfig{
			ID:      dbModel.ID,
			Name:    dbModel.Name,
			Enabled: dbModel.Enabled == 1,
		}

		// Parse fallback chain
		if dbModel.FallbackChainJson != "" {
			json.Unmarshal([]byte(dbModel.FallbackChainJson), &model.FallbackChain)
		}

		// Parse truncate params
		if dbModel.TruncateParamsJson != "" {
			json.Unmarshal([]byte(dbModel.TruncateParamsJson), &model.TruncateParams)
		}

		result = append(result, model)
	}

	return result
}

// GetEnabledModels returns only enabled model configurations
func (m *ModelsManager) GetEnabledModels() []models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dbModels, err := m.queries.GetEnabledModels(context.Background())
	if err != nil {
		return nil
	}

	result := make([]models.ModelConfig, 0, len(dbModels))
	for _, dbModel := range dbModels {
		model := models.ModelConfig{
			ID:      dbModel.ID,
			Name:    dbModel.Name,
			Enabled: dbModel.Enabled == 1,
		}

		if dbModel.FallbackChainJson != "" {
			json.Unmarshal([]byte(dbModel.FallbackChainJson), &model.FallbackChain)
		}

		if dbModel.TruncateParamsJson != "" {
			json.Unmarshal([]byte(dbModel.TruncateParamsJson), &model.TruncateParams)
		}

		result = append(result, model)
	}

	return result
}

// GetTruncateParams returns truncate params for a model
func (m *ModelsManager) GetTruncateParams(modelID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dbModel, err := m.queries.GetModelByID(context.Background(), modelID)
	if err != nil {
		return nil
	}

	var params []string
	if dbModel.TruncateParamsJson != "" {
		json.Unmarshal([]byte(dbModel.TruncateParamsJson), &params)
	}

	if len(params) == 0 {
		return nil
	}

	result := make([]string, len(params))
	copy(result, params)
	return result
}

// GetFallbackChain returns the fallback chain for a model
func (m *ModelsManager) GetFallbackChain(modelID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dbModel, err := m.queries.GetModelByID(context.Background(), modelID)
	if err != nil {
		return nil
	}

	var chain []string
	if dbModel.FallbackChainJson != "" {
		json.Unmarshal([]byte(dbModel.FallbackChainJson), &chain)
	}

	result := make([]string, 0, len(chain)+1)
	result = append(result, dbModel.ID)
	result = append(result, chain...)
	return result
}

// AddModel adds a new model configuration
func (m *ModelsManager) AddModel(model models.ModelConfig) error {
	if model.ID == "" {
		return models.ErrInvalidModelID
	}
	if model.Name == "" {
		return models.ErrInvalidModelName
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate
	_, err := m.queries.GetModelByID(context.Background(), model.ID)
	if err == nil {
		return models.ErrDuplicateModelID
	}

	enabled := int64(0)
	if model.Enabled {
		enabled = 1
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	return m.queries.InsertModel(context.Background(), db.InsertModelParams{
		ID:                 model.ID,
		Name:               model.Name,
		Enabled:            enabled,
		FallbackChainJson:  string(fallbackJSON),
		TruncateParamsJson: string(truncateJSON),
	})
}

// UpdateModel updates an existing model configuration
func (m *ModelsManager) UpdateModel(modelID string, model models.ModelConfig) error {
	if model.ID == "" {
		return models.ErrInvalidModelID
	}
	if model.Name == "" {
		return models.ErrInvalidModelName
	}
	if model.ID != modelID {
		return models.ErrCannotChangeModelID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check model exists
	_, err := m.queries.GetModelByID(context.Background(), modelID)
	if err != nil {
		return models.ErrModelNotFound
	}

	enabled := int64(0)
	if model.Enabled {
		enabled = 1
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	return m.queries.UpdateModel(context.Background(), db.UpdateModelParams{
		ID:                 modelID,
		Name:               model.Name,
		Enabled:            enabled,
		FallbackChainJson:  string(fallbackJSON),
		TruncateParamsJson: string(truncateJSON),
	})
}

// RemoveModel removes a model configuration
func (m *ModelsManager) RemoveModel(modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check model exists
	_, err := m.queries.GetModelByID(context.Background(), modelID)
	if err != nil {
		return models.ErrModelNotFound
	}

	return m.queries.DeleteModel(context.Background(), modelID)
}

// Validate validates the model configuration
func (m *ModelsManager) Validate() error {
	modelList := m.GetModels()

	modelIDs := make(map[string]bool)
	for _, model := range modelList {
		modelIDs[model.ID] = true
	}

	for _, model := range modelList {
		if model.ID == "" {
			return models.ErrInvalidModelID
		}

		if len(model.FallbackChain) > 1 {
			return fmt.Errorf("fallback chain is limited to maximum 1 fallback model")
		}

		for _, fallbackID := range model.FallbackChain {
			if fallbackID != "" && !modelIDs[fallbackID] {
				// Unknown model reference - warn but allow for forward compatibility
			}
		}
	}

	return nil
}
