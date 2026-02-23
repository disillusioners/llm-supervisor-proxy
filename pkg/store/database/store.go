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
)

// ConfigManager implements config.ManagerInterface using database storage
type ConfigManager struct {
	store    *Store
	qb       *QueryBuilder
	mu       sync.RWMutex
	cfg      config.Config
	readOnly bool
	eventBus *events.Bus
}

// NewConfigManager creates a new database-backed config manager
func NewConfigManager(store *Store, eventBus *events.Bus) (*ConfigManager, error) {
	cm := &ConfigManager{
		store:    store,
		qb:       NewQueryBuilder(store.Dialect),
		eventBus: eventBus,
	}
	if err := cm.Load(); err != nil {
		return nil, err
	}
	return cm, nil
}

// dbConfigRow represents a row from the configs table
type dbConfigRow struct {
	Version                 string
	UpstreamURL             string
	Port                    int64
	IdleTimeoutMs           int64
	MaxGenerationTimeMs     int64
	MaxUpstreamErrorRetries int64
	MaxIdleRetries          int64
	MaxGenerationRetries    int64
	MaxStreamBufferSize     int64
	LoopDetectionJSON       string
	UpdatedAt               string
}

// Load initializes configuration from database
func (m *ConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Start with defaults
	cfg := config.Defaults

	// Load from database using dialect-aware query
	query := m.qb.GetConfig()
	row := m.store.DB.QueryRowContext(context.Background(), query)

	var dbCfg dbConfigRow
	err := row.Scan(
		&dbCfg.Version,
		&dbCfg.UpstreamURL,
		&dbCfg.Port,
		&dbCfg.IdleTimeoutMs,
		&dbCfg.MaxGenerationTimeMs,
		&dbCfg.MaxUpstreamErrorRetries,
		&dbCfg.MaxIdleRetries,
		&dbCfg.MaxGenerationRetries,
		&dbCfg.MaxStreamBufferSize,
		&dbCfg.LoopDetectionJSON,
		&dbCfg.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			m.cfg = cfg
			return nil
		}
		return fmt.Errorf("failed to load config from database: %w", err)
	}

	// Map database config to struct
	cfg.Version = dbCfg.Version
	cfg.UpstreamURL = dbCfg.UpstreamURL
	cfg.Port = int(dbCfg.Port)
	cfg.IdleTimeout = config.Duration(time.Duration(dbCfg.IdleTimeoutMs) * time.Millisecond)
	cfg.MaxGenerationTime = config.Duration(time.Duration(dbCfg.MaxGenerationTimeMs) * time.Millisecond)
	cfg.MaxUpstreamErrorRetries = int(dbCfg.MaxUpstreamErrorRetries)
	cfg.MaxIdleRetries = int(dbCfg.MaxIdleRetries)
	cfg.MaxGenerationRetries = int(dbCfg.MaxGenerationRetries)
	cfg.MaxStreamBufferSize = int(dbCfg.MaxStreamBufferSize)
	cfg.UpdatedAt = dbCfg.UpdatedAt

	// Parse loop detection JSON
	if dbCfg.LoopDetectionJSON != "" && dbCfg.LoopDetectionJSON != "{}" {
		if err := json.Unmarshal([]byte(dbCfg.LoopDetectionJSON), &cfg.LoopDetection); err != nil {
			cfg.LoopDetection = config.Defaults.LoopDetection
		}
	}

	m.cfg = cfg
	return nil
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

	// Update database using dialect-aware query
	query := m.qb.UpdateConfig()
	_, err = m.store.DB.ExecContext(context.Background(), query,
		cfg.Version,
		cfg.UpstreamURL,
		cfg.Port,
		time.Duration(cfg.IdleTimeout).Milliseconds(),
		time.Duration(cfg.MaxGenerationTime).Milliseconds(),
		cfg.MaxUpstreamErrorRetries,
		cfg.MaxIdleRetries,
		cfg.MaxGenerationRetries,
		cfg.MaxStreamBufferSize,
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

// GetMaxStreamBufferSize returns the max stream buffer size in bytes
func (m *ConfigManager) GetMaxStreamBufferSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.MaxStreamBufferSize
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

// GetFilePath returns a description of the database connection
func (m *ConfigManager) GetFilePath() string {
	if m.store.Dialect == PostgreSQL {
		return "postgresql://[credentials-hidden]"
	}
	return "sqlite://" + m.store.dbPath
}

// dbModelRow represents a row from the models table
type dbModelRow struct {
	ID                 string
	Name               string
	Enabled            interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
	FallbackChainJSON  string
	TruncateParamsJSON string
	CreatedAt          string
	UpdatedAt          string
}

// isEnabled converts the Enabled field to bool (handles both SQLite int64 and PostgreSQL bool)
func (r *dbModelRow) isEnabled() bool {
	switch v := r.Enabled.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	default:
		return false
	}
}

// ModelsManager implements models.ModelsConfigInterface using database storage
type ModelsManager struct {
	store *Store
	qb    *QueryBuilder
	mu    sync.RWMutex
}

// NewModelsManager creates a new database-backed models manager
func NewModelsManager(store *Store) (*ModelsManager, error) {
	return &ModelsManager{
		store: store,
		qb:    NewQueryBuilder(store.Dialect),
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

// scanModels executes a query and scans the results into model configs
func (m *ModelsManager) scanModels(query string, args ...interface{}) ([]models.ModelConfig, error) {
	rows, err := m.store.DB.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.ModelConfig
	for rows.Next() {
		var dbModel dbModelRow
		err := rows.Scan(
			&dbModel.ID,
			&dbModel.Name,
			&dbModel.Enabled,
			&dbModel.FallbackChainJSON,
			&dbModel.TruncateParamsJSON,
			&dbModel.CreatedAt,
			&dbModel.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		model := models.ModelConfig{
			ID:      dbModel.ID,
			Name:    dbModel.Name,
			Enabled: dbModel.isEnabled(),
		}

		// Parse fallback chain
		if dbModel.FallbackChainJSON != "" {
			json.Unmarshal([]byte(dbModel.FallbackChainJSON), &model.FallbackChain)
		}

		// Parse truncate params
		if dbModel.TruncateParamsJSON != "" {
			json.Unmarshal([]byte(dbModel.TruncateParamsJSON), &model.TruncateParams)
		}

		result = append(result, model)
	}

	return result, rows.Err()
}

// GetModels returns all model configurations
func (m *ModelsManager) GetModels() []models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result, err := m.scanModels(m.qb.GetAllModels())
	if err != nil {
		return nil
	}
	return result
}

// GetEnabledModels returns only enabled model configurations
func (m *ModelsManager) GetEnabledModels() []models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result, err := m.scanModels(m.qb.GetEnabledModels())
	if err != nil {
		return nil
	}
	return result
}

// GetTruncateParams returns truncate params for a model
func (m *ModelsManager) GetTruncateParams(modelID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)

	var dbModel dbModelRow
	err := row.Scan(
		&dbModel.ID,
		&dbModel.Name,
		&dbModel.Enabled,
		&dbModel.FallbackChainJSON,
		&dbModel.TruncateParamsJSON,
		&dbModel.CreatedAt,
		&dbModel.UpdatedAt,
	)
	if err != nil {
		return nil
	}

	var params []string
	if dbModel.TruncateParamsJSON != "" {
		json.Unmarshal([]byte(dbModel.TruncateParamsJSON), &params)
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

	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)

	var dbModel dbModelRow
	err := row.Scan(
		&dbModel.ID,
		&dbModel.Name,
		&dbModel.Enabled,
		&dbModel.FallbackChainJSON,
		&dbModel.TruncateParamsJSON,
		&dbModel.CreatedAt,
		&dbModel.UpdatedAt,
	)
	if err != nil {
		return nil
	}

	var chain []string
	if dbModel.FallbackChainJSON != "" {
		json.Unmarshal([]byte(dbModel.FallbackChainJSON), &chain)
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
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, model.ID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err == nil {
		return models.ErrDuplicateModelID
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	insertQuery := m.qb.InsertModel()
	_, err = m.store.DB.ExecContext(context.Background(), insertQuery,
		model.ID,
		model.Name,
		m.qb.BooleanLiteral(model.Enabled),
		string(fallbackJSON),
		string(truncateJSON),
	)
	return err
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
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err != nil {
		return models.ErrModelNotFound
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	updateQuery := m.qb.UpdateModel()
	_, err = m.store.DB.ExecContext(context.Background(), updateQuery,
		model.Name,
		m.qb.BooleanLiteral(model.Enabled),
		string(fallbackJSON),
		string(truncateJSON),
		modelID,
	)
	return err
}

// RemoveModel removes a model configuration
func (m *ModelsManager) RemoveModel(modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check model exists
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err != nil {
		return models.ErrModelNotFound
	}

	deleteQuery := m.qb.DeleteModel()
	_, err = m.store.DB.ExecContext(context.Background(), deleteQuery, modelID)
	return err
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
