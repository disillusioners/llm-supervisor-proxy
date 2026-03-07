package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
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
	UpstreamCredentialID    string
	Port                    int64
	IdleTimeoutMs           int64
	MaxGenerationTimeMs     int64
	MaxUpstreamErrorRetries int64
	MaxIdleRetries          int64
	MaxGenerationRetries    int64
	MaxStreamBufferSize     int64
	LoopDetectionJSON       string
	ToolRepairJSON          string
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
		&dbCfg.UpstreamCredentialID,
		&dbCfg.Port,
		&dbCfg.IdleTimeoutMs,
		&dbCfg.MaxGenerationTimeMs,
		&dbCfg.MaxUpstreamErrorRetries,
		&dbCfg.MaxIdleRetries,
		&dbCfg.MaxGenerationRetries,
		&dbCfg.MaxStreamBufferSize,
		&dbCfg.LoopDetectionJSON,
		&dbCfg.ToolRepairJSON,
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
	cfg.UpstreamCredentialID = dbCfg.UpstreamCredentialID
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

	// Parse tool repair JSON
	if dbCfg.ToolRepairJSON != "" && dbCfg.ToolRepairJSON != "{}" {
		if err := json.Unmarshal([]byte(dbCfg.ToolRepairJSON), &cfg.ToolRepair); err != nil {
			cfg.ToolRepair = *toolrepair.DefaultConfig()
		}
	}

	m.cfg = cfg
	return nil
}

// Save persists configuration to database
func (m *ConfigManager) Save(cfg config.Config) (*config.SaveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readOnly {
		return nil, fmt.Errorf("config is read-only")
	}

	// Merge incoming config with existing config to preserve fields not sent by frontend
	merged := mergeConfig(m.cfg, cfg)

	// Validate the merged config, not the partial incoming config
	if err := merged.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	result := &config.SaveResult{}
	if m.cfg.Port != merged.Port {
		result.RestartRequired = true
		result.ChangedFields = append(result.ChangedFields, "port")
	}

	merged.Version = config.ConfigVersion
	merged.UpdatedAt = time.Now().Format(time.RFC3339)

	// Serialize loop detection
	loopDetectionJSON, err := json.Marshal(merged.LoopDetection)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize loop detection: %w", err)
	}

	// Serialize tool repair
	toolRepairJSON, err := json.Marshal(merged.ToolRepair)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize tool repair: %w", err)
	}

	// Update database using dialect-aware query
	query := m.qb.UpdateConfig()
	_, err = m.store.DB.ExecContext(context.Background(), query,
		merged.Version,
		merged.UpstreamURL,
		merged.UpstreamCredentialID,
		merged.Port,
		time.Duration(merged.IdleTimeout).Milliseconds(),
		time.Duration(merged.MaxGenerationTime).Milliseconds(),
		merged.MaxUpstreamErrorRetries,
		merged.MaxIdleRetries,
		merged.MaxGenerationRetries,
		merged.MaxStreamBufferSize,
		string(loopDetectionJSON),
		string(toolRepairJSON),
		merged.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save config to database: %w", err)
	}

	m.cfg = merged

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

// mergeConfig merges incoming config with existing config, preserving values
// for fields that weren't sent by the frontend. The frontend sends either:
// - Proxy settings only (no loop_detection)
// - Loop detection settings only (no proxy settings)
// We detect which case by checking for non-zero values.
func mergeConfig(existing, incoming config.Config) config.Config {
	result := existing // Start with existing config

	// Proxy settings: update if incoming has non-zero values
	// UpstreamURL is required, empty string means not sent
	if incoming.UpstreamURL != "" {
		result.UpstreamURL = incoming.UpstreamURL
	}
	// UpstreamCredentialID: empty string means not sent
	if incoming.UpstreamCredentialID != "" {
		result.UpstreamCredentialID = incoming.UpstreamCredentialID
	}
	// Port: 0 means not sent (0 is invalid anyway)
	if incoming.Port != 0 {
		result.Port = incoming.Port
	}
	// IdleTimeout: 0 means not sent (0 is invalid per validation)
	if incoming.IdleTimeout != 0 {
		result.IdleTimeout = incoming.IdleTimeout
	}
	// MaxGenerationTime: 0 means not sent
	if incoming.MaxGenerationTime != 0 {
		result.MaxGenerationTime = incoming.MaxGenerationTime
	}
	// For retry counts, 0 could be valid, so we check if any retry field is set
	// If any retry field is non-zero, update all retry fields (frontend sends all or none)
	if incoming.MaxUpstreamErrorRetries != 0 || incoming.MaxIdleRetries != 0 || incoming.MaxGenerationRetries != 0 {
		result.MaxUpstreamErrorRetries = incoming.MaxUpstreamErrorRetries
		result.MaxIdleRetries = incoming.MaxIdleRetries
		result.MaxGenerationRetries = incoming.MaxGenerationRetries
	}
	// MaxStreamBufferSize: update if incoming differs from existing (it's always sent with proxy settings)
	if incoming.MaxStreamBufferSize != 0 {
		result.MaxStreamBufferSize = incoming.MaxStreamBufferSize
	}

	// Loop detection: check if any loop detection field was set
	// We check multiple fields to detect if loop_detection was intentionally sent
	if isLoopDetectionProvided(incoming.LoopDetection) {
		result.LoopDetection = mergeLoopDetectionConfig(existing.LoopDetection, incoming.LoopDetection)
	}

	// Tool repair: check if any tool repair field was set
	if isToolRepairProvided(incoming.ToolRepair) {
		result.ToolRepair = mergeToolRepairConfig(existing.ToolRepair, incoming.ToolRepair)
	}

	return result
}

// isLoopDetectionProvided checks if loop detection config was explicitly provided
// by looking for any non-zero field values (excluding booleans which default to false)
func isLoopDetectionProvided(ld config.LoopDetectionConfig) bool {
	// Check if any non-boolean field has a non-zero value
	// We don't check Enabled/ShadowMode because false is a valid value but also the zero value
	return ld.MessageWindow != 0 ||
		ld.ActionWindow != 0 ||
		ld.ExactMatchCount != 0 ||
		ld.SimilarityThreshold != 0 ||
		ld.MinTokensForSimHash != 0 ||
		ld.ActionRepeatCount != 0 ||
		ld.OscillationCount != 0 ||
		ld.MinTokensForAnalysis != 0 ||
		ld.ThinkingMinTokens != 0 ||
		ld.TrigramThreshold != 0 ||
		ld.MaxCycleLength != 0 ||
		ld.ReasoningTrigramThreshold != 0 ||
		len(ld.ReasoningModelPatterns) > 0
}

// mergeLoopDetectionConfig merges loop detection settings
// All fields from incoming are copied (frontend sends complete loop_detection object)
func mergeLoopDetectionConfig(existing, incoming config.LoopDetectionConfig) config.LoopDetectionConfig {
	return incoming
}

// isToolRepairProvided checks if tool repair config was explicitly provided
// by looking for any non-zero field values (excluding booleans which default to false)
func isToolRepairProvided(tr toolrepair.Config) bool {
	// Check if any non-boolean field has a non-zero value
	// We don't check Enabled/LogOriginal/LogRepaired because false is valid but also zero value
	return len(tr.Strategies) > 0 ||
		tr.MaxArgumentsSize != 0 ||
		tr.MaxToolCallsPerResponse != 0 ||
		tr.FixerModel != "" ||
		tr.FixerTimeout != 0
}

// mergeToolRepairConfig merges tool repair settings
// All fields from incoming are copied (frontend sends complete tool_repair object)
func mergeToolRepairConfig(existing, incoming toolrepair.Config) toolrepair.Config {
	return incoming
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

// GetBufferStorageDir returns the buffer storage directory
func (m *ConfigManager) GetBufferStorageDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.BufferStorageDir
}

// GetBufferMaxStorageMB returns the max buffer storage in MB
func (m *ConfigManager) GetBufferMaxStorageMB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.BufferMaxStorageMB
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
	// Internal upstream fields
	Internal        interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
	CredentialID    string      // Reference to credential
	InternalBaseURL string      // Base URL override (optional)
	InternalModel   string
}

// dbCredentialRow represents a row from the credentials table
type dbCredentialRow struct {
	ID        string
	Provider  string
	APIKey    string
	BaseURL   string
	CreatedAt string
	UpdatedAt string
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

// isInternal converts the Internal field to bool
func (r *dbModelRow) isInternal() bool {
	switch v := r.Internal.(type) {
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
			&dbModel.Internal,
			&dbModel.CredentialID,
			&dbModel.InternalBaseURL,
			&dbModel.InternalModel,
		)
		if err != nil {
			return nil, err
		}

		model := models.ModelConfig{
			ID:              dbModel.ID,
			Name:            dbModel.Name,
			Enabled:         dbModel.isEnabled(),
			Internal:        dbModel.isInternal(),
			CredentialID:    dbModel.CredentialID,
			InternalBaseURL: dbModel.InternalBaseURL,
			InternalModel:   dbModel.InternalModel,
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

// GetModel returns a single model configuration by ID, including internal fields
func (m *ModelsManager) GetModel(modelID string) *models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(internal, 0), coalesce(credential_id, ''),
		coalesce(internal_base_url, ''), coalesce(internal_model, '')
		FROM models WHERE id = ?`

	if m.store.Dialect == "postgres" {
		query = `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(internal, false), coalesce(credential_id, ''),
			coalesce(internal_base_url, ''), coalesce(internal_model, '')
			FROM models WHERE id = $1`
	}

	var dbModel dbModelRow
	err := m.store.DB.QueryRowContext(context.Background(), query, modelID).Scan(
		&dbModel.ID,
		&dbModel.Name,
		&dbModel.Enabled,
		&dbModel.FallbackChainJSON,
		&dbModel.TruncateParamsJSON,
		&dbModel.CreatedAt,
		&dbModel.UpdatedAt,
		&dbModel.Internal,
		&dbModel.CredentialID,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
	)
	if err != nil {
		return nil
	}

	model := &models.ModelConfig{
		ID:              dbModel.ID,
		Name:            dbModel.Name,
		Enabled:         dbModel.isEnabled(),
		Internal:        dbModel.isInternal(),
		CredentialID:    dbModel.CredentialID,
		InternalBaseURL: dbModel.InternalBaseURL,
		InternalModel:   dbModel.InternalModel,
	}

	// Parse fallback chain
	if dbModel.FallbackChainJSON != "" {
		json.Unmarshal([]byte(dbModel.FallbackChainJSON), &model.FallbackChain)
	}

	// Parse truncate params
	if dbModel.TruncateParamsJSON != "" {
		json.Unmarshal([]byte(dbModel.TruncateParamsJSON), &model.TruncateParams)
	}

	return model
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
		m.qb.BooleanLiteral(model.Internal),
		model.CredentialID,
		model.InternalBaseURL,
		model.InternalModel,
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
		m.qb.BooleanLiteral(model.Internal),
		model.CredentialID,
		model.InternalBaseURL,
		model.InternalModel,
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

	// Build set of valid credential IDs
	credentialIDs := make(map[string]bool)
	for _, cred := range m.GetCredentials() {
		credentialIDs[cred.ID] = true
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
	for _, cred := range m.GetCredentials() {
		if err := cred.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Credential management methods

// GetCredential returns the credential configuration for a given ID
func (m *ModelsManager) GetCredential(id string) *models.CredentialConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials WHERE id = ?`
	if m.store.Dialect == PostgreSQL {
		query = `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials WHERE id = $1`
	}

	var dbCred dbCredentialRow
	err := m.store.DB.QueryRowContext(context.Background(), query, id).Scan(
		&dbCred.ID,
		&dbCred.Provider,
		&dbCred.APIKey,
		&dbCred.BaseURL,
		&dbCred.CreatedAt,
		&dbCred.UpdatedAt,
	)
	if err != nil {
		return nil
	}

	cred := &models.CredentialConfig{
		ID:       dbCred.ID,
		Provider: dbCred.Provider,
		APIKey:   dbCred.APIKey,
		BaseURL:  dbCred.BaseURL,
	}

	// Decrypt API key
	if dbCred.APIKey != "" {
		decrypted, err := crypto.Decrypt(dbCred.APIKey)
		if err != nil {
			log.Printf("Warning: failed to decrypt API key for credential %s: %v", dbCred.ID, err)
		} else {
			cred.APIKey = decrypted
		}
	}

	return cred
}

// GetCredentials returns all credential configurations
func (m *ModelsManager) GetCredentials() []models.CredentialConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials ORDER BY id`
	rows, err := m.store.DB.QueryContext(context.Background(), query)
	if err != nil {
		return []models.CredentialConfig{}
	}
	defer rows.Close()

	var result []models.CredentialConfig
	for rows.Next() {
		var dbCred dbCredentialRow
		if err := rows.Scan(&dbCred.ID, &dbCred.Provider, &dbCred.APIKey, &dbCred.BaseURL, &dbCred.CreatedAt, &dbCred.UpdatedAt); err != nil {
			continue
		}

		cred := models.CredentialConfig{
			ID:       dbCred.ID,
			Provider: dbCred.Provider,
			APIKey:   dbCred.APIKey,
			BaseURL:  dbCred.BaseURL,
		}

		// Decrypt API key
		if cred.APIKey != "" {
			decrypted, err := crypto.Decrypt(cred.APIKey)
			if err != nil {
				log.Printf("Warning: failed to decrypt API key for credential %s: %v", cred.ID, err)
			} else {
				cred.APIKey = decrypted
			}
		}

		result = append(result, cred)
	}
	return result
}

// AddCredential adds a new credential configuration
func (m *ModelsManager) AddCredential(cred models.CredentialConfig) error {
	if err := cred.Validate(); err != nil {
		return err
	}

	// Encrypt API key before storing
	encryptedAPIKey := cred.APIKey
	if encryptedAPIKey != "" {
		encrypted, err := crypto.Encrypt(cred.APIKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt API key: %w", err)
		}
		encryptedAPIKey = encrypted
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW())`
	} else {
		query = `INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`
	}

	_, err := m.store.DB.ExecContext(context.Background(), query, cred.ID, cred.Provider, encryptedAPIKey, cred.BaseURL)
	return err
}

// UpdateCredential updates an existing credential configuration
func (m *ModelsManager) UpdateCredential(id string, cred models.CredentialConfig) error {
	if err := cred.Validate(); err != nil {
		return err
	}
	if cred.ID != id {
		return models.ErrCannotChangeCredentialID
	}

	// Encrypt API key before storing
	encryptedAPIKey := cred.APIKey
	if encryptedAPIKey != "" {
		encrypted, err := crypto.Encrypt(cred.APIKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt API key: %w", err)
		}
		encryptedAPIKey = encrypted
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `UPDATE credentials SET provider = $1, api_key = $2, base_url = $3, updated_at = NOW() WHERE id = $4`
	} else {
		query = `UPDATE credentials SET provider = ?, api_key = ?, base_url = ?, updated_at = datetime('now') WHERE id = ?`
	}

	result, err := m.store.DB.ExecContext(context.Background(), query, cred.Provider, encryptedAPIKey, cred.BaseURL, id)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return models.ErrCredentialNotFound
	}
	return nil
}

// RemoveCredential removes a credential configuration
func (m *ModelsManager) RemoveCredential(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if credential is in use
	modelList, _ := m.scanModels(m.qb.GetAllModels())
	for _, model := range modelList {
		if model.CredentialID == id {
			return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, models.ErrCredentialInUse)
		}
	}

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `DELETE FROM credentials WHERE id = $1`
	} else {
		query = `DELETE FROM credentials WHERE id = ?`
	}

	result, err := m.store.DB.ExecContext(context.Background(), query, id)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return models.ErrCredentialNotFound
	}
	return nil
}

// ResolveInternalConfig resolves the full internal upstream configuration
func (m *ModelsManager) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	modelConfig := m.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	if modelConfig.CredentialID == "" {
		return "", "", "", "", false
	}

	cred := m.GetCredential(modelConfig.CredentialID)
	if cred == nil {
		return "", "", "", "", false
	}

	// Resolve provider: use credential provider
	provider = cred.Provider

	// Resolve baseURL: model override > credential
	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	return provider, cred.APIKey, baseURL, modelConfig.InternalModel, true
}
