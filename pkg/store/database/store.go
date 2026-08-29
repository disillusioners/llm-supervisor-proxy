package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
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

// Load initializes configuration from database
func (m *ConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Start with defaults
	cfg := config.Defaults

	// Try to load from database using the new config_json column
	var configJSON string
	err := m.store.DB.QueryRowContext(context.Background(), m.qb.SelectConfig()).Scan(&configJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No config in DB, use defaults with ENV overrides
			cfg = config.ApplyEnvOverrides(cfg)
			m.cfg = cfg
			return nil
		}
		return fmt.Errorf("failed to query config: %w", err)
	}

	// Unmarshal JSON into config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		// Corrupted JSON - use defaults
		log.Printf("Warning: failed to unmarshal config JSON: %v, using defaults", err)
		cfg = config.Defaults
	}

	// Apply environment variable overrides (env > database > defaults)
	cfg = config.ApplyEnvOverrides(cfg)

	m.cfg = cfg
	return nil
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
	// StreamDeadline: 0 means not sent
	if incoming.StreamDeadline != 0 {
		result.StreamDeadline = incoming.StreamDeadline
	}
	// MaxGenerationTime: 0 means not sent
	if incoming.MaxGenerationTime != 0 {
		result.MaxGenerationTime = incoming.MaxGenerationTime
	}

	// MaxStreamBufferSize: update if incoming differs from existing (it's always sent with proxy settings)
	if incoming.MaxStreamBufferSize != 0 {
		result.MaxStreamBufferSize = incoming.MaxStreamBufferSize
	}

	// Race retry
	// Check if any race retry field was provided (not just RaceMaxParallel)
	// We check multiple fields to detect if race_retry was intentionally sent
	if isRaceRetryProvided(incoming) {
		result.RaceRetryEnabled = incoming.RaceRetryEnabled
		result.RaceParallelOnIdle = incoming.RaceParallelOnIdle
		result.RaceMaxParallel = incoming.RaceMaxParallel
		result.RaceMaxBufferBytes = incoming.RaceMaxBufferBytes
	}

	// Idle termination: check if idle termination config was provided
	if isIdleTerminationProvided(incoming) {
		result.IdleTerminationEnabled = incoming.IdleTerminationEnabled
		if incoming.IdleTerminationTimeout != 0 {
			result.IdleTerminationTimeout = incoming.IdleTerminationTimeout
		}
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

	// Ultimate model: check if ultimate model config was provided
	if isUltimateModelProvided(incoming.UltimateModel) {
		result.UltimateModel = incoming.UltimateModel
	}

	// Raw upstream response logging: check if any field was set
	if incoming.LogRawUpstreamResponse || incoming.LogRawUpstreamOnError || incoming.LogRawUpstreamMaxKB != 0 {
		result.LogRawUpstreamResponse = incoming.LogRawUpstreamResponse
		result.LogRawUpstreamOnError = incoming.LogRawUpstreamOnError
		if incoming.LogRawUpstreamMaxKB != 0 {
			result.LogRawUpstreamMaxKB = incoming.LogRawUpstreamMaxKB
		}
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

// isRaceRetryProvided checks if race retry config was explicitly provided
// by looking for any non-zero field values (excluding booleans which default to false)
// This is similar to isLoopDetectionProvided and isToolRepairProvided patterns
func isRaceRetryProvided(cfg config.Config) bool {
	return cfg.RaceMaxParallel != 0 ||
		cfg.RaceMaxBufferBytes != 0 ||
		cfg.RaceRetryEnabled || // booleans are also checked since true is a valid intentional value
		cfg.RaceParallelOnIdle
}

// isIdleTerminationProvided checks if idle termination config was explicitly provided
func isIdleTerminationProvided(cfg config.Config) bool {
	return cfg.IdleTerminationEnabled || cfg.IdleTerminationTimeout != 0
}

// isUltimateModelProvided checks if ultimate model config was explicitly provided
// by checking if ModelID is non-empty (MaxHash defaults to 100, so 0 means not set)
func isUltimateModelProvided(um config.UltimateModelConfig) bool {
	// ModelID is the primary indicator - empty string means feature is disabled
	// We also check MaxHash to detect if the config was sent at all
	return um.ModelID != "" || um.MaxHash != 0
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

// GetStreamDeadline returns the stream deadline for race retry buffer caching
func (m *ConfigManager) GetStreamDeadline() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.cfg.StreamDeadline)
}

// GetMaxGenerationTime returns the max generation time
func (m *ConfigManager) GetMaxGenerationTime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.cfg.MaxGenerationTime)
}

// GetMaxStreamBufferSize returns the max stream buffer size in bytes
func (m *ConfigManager) GetMaxStreamBufferSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.MaxStreamBufferSize
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

// GetLoopDetection returns the loop detection configuration
func (m *ConfigManager) GetLoopDetection() config.LoopDetectionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.LoopDetection
}

// GetUltimateModel returns the ultimate model configuration
func (m *ConfigManager) GetUltimateModel() config.UltimateModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.UltimateModel
}

// GetRaceRetryEnabled returns whether race retry is enabled
func (m *ConfigManager) GetRaceRetryEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.RaceRetryEnabled
}

// GetRaceParallelOnIdle returns whether to spawn parallel requests on idle timeout
func (m *ConfigManager) GetRaceParallelOnIdle() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.RaceParallelOnIdle
}

// GetRaceMaxParallel returns the max parallel requests
func (m *ConfigManager) GetRaceMaxParallel() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.RaceMaxParallel
}

// GetRaceMaxBufferBytes returns the max bytes per request buffer
func (m *ConfigManager) GetRaceMaxBufferBytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.RaceMaxBufferBytes
}

// GetToolCallBufferDisabled returns whether tool call buffering is disabled
func (m *ConfigManager) GetToolCallBufferDisabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ToolCallBufferDisabled
}

// GetToolCallBufferMaxSize returns the max size for tool call buffer
func (m *ConfigManager) GetToolCallBufferMaxSize() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.ToolCallBufferMaxSize
}

// GetLogRawUpstreamResponse returns whether to log successful upstream responses
func (m *ConfigManager) GetLogRawUpstreamResponse() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.LogRawUpstreamResponse
}

// GetLogRawUpstreamOnError returns whether to log failed/error upstream responses
func (m *ConfigManager) GetLogRawUpstreamOnError() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.LogRawUpstreamOnError
}

// GetLogRawUpstreamMaxKB returns the max KB per response to log
func (m *ConfigManager) GetLogRawUpstreamMaxKB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.LogRawUpstreamMaxKB
}

// Save persists configuration to database and updates in-memory state
func (m *ConfigManager) Save(cfg config.Config) (*config.SaveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readOnly {
		return nil, errors.New("config is read-only")
	}

	// Merge incoming config with existing config to preserve fields not sent by frontend
	merged := mergeConfig(m.cfg, cfg)

	// Validate the merged config, not the partial incoming config
	if err := merged.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Detect changes that require restart
	result := &config.SaveResult{}
	if m.cfg.Port != merged.Port {
		result.RestartRequired = true
		result.ChangedFields = append(result.ChangedFields, "port")
	}

	// Set metadata
	merged.Version = config.ConfigVersion
	merged.UpdatedAt = time.Now().Format(time.RFC3339)

	// Marshal entire config to JSON
	configJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	// Upsert config to database using the new config_json column
	_, err = m.store.DB.ExecContext(context.Background(), m.qb.UpsertConfig(), string(configJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to save config to database: %w", err)
	}

	// Apply environment variable overrides (env always wins)
	merged = config.ApplyEnvOverrides(merged)

	// Update in-memory config
	m.cfg = merged

	// Publish config update event if event bus is wired
	if m.eventBus != nil {
		m.eventBus.Publish(events.Event{
			Type:      "config.updated",
			Timestamp: time.Now().Unix(),
			Data:      merged,
		})
	}

	return result, nil
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
	ID                         string
	Name                       string
	Enabled                    interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
	FallbackChainJSON          string
	TruncateParamsJSON         string
	CreatedAt                  string
	UpdatedAt                  string
	ReleaseStreamChunkDeadline int64 // Deadline in milliseconds for releasing stream chunks
	// Internal upstream fields
	Internal        interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
	CredentialsJSON string      // JSON array of CredentialRef; column = credentials_json (M-1)
	InternalBaseURL string      // Base URL override (optional)
	InternalModel   string
	// Peak hour configuration
	PeakHourEnabled  interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
	PeakHourStart    string
	PeakHourEnd      string
	PeakHourTimezone string
	PeakHourModel    string
	// Secondary upstream model for retry logic
	SecondaryUpstreamModel string
	// Exclude from ultimate model switching
	ExcludeFromUltimateSwitching interface{} // Can be int64 (SQLite) or bool (PostgreSQL)
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

// isPeakHourEnabled converts the PeakHourEnabled field to bool
func (r *dbModelRow) isPeakHourEnabled() bool {
	switch v := r.PeakHourEnabled.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	default:
		return false
	}
}

// isExcludeFromUltimateSwitching converts the ExcludeFromUltimateSwitching field to bool
func (r *dbModelRow) isExcludeFromUltimateSwitching() bool {
	switch v := r.ExcludeFromUltimateSwitching.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	default:
		return false
	}
}

// parseCredentialsJSON parses the credentials_json column into a
// []models.CredentialRef. House precedent (FallbackChainJSON /
// TruncateParamsJSON at 607-615 in the pre-Phase-1 file):
// unparseable / empty input yields an empty slice with no error
// — the column is app-level, not DB-enforced.
//
// The shadow `credential_id` column is NOT consulted here. Per the
// Phase 1 M-1 contract (technical-analysis.md §API Contract
// store-layer read-path), the application reads `credentials_json`
// only — the shadow exists for legacy binaries and external tooling.
func parseCredentialsJSON(raw string) []models.CredentialRef {
	if raw == "" || raw == "[]" {
		return []models.CredentialRef{}
	}
	var refs []models.CredentialRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		// House precedent: treat unparseable as empty (no error).
		return []models.CredentialRef{}
	}
	if refs == nil {
		return []models.CredentialRef{}
	}
	return refs
}

// marshalCredentialsJSON serializes []models.CredentialRef into the
// JSON text stored in models.credentials_json. nil/empty slice
// yields `'[]'` (the column default; never NULL), matching the
// migration's NOT NULL DEFAULT '[]' invariant. Marshal failure
// falls back to `'[]'` so AddModel does not surface a JSON error
// to the caller — pre-Phase-1 models would write empty strings
// via the legacy field, which is the same effective shape.
func marshalCredentialsJSON(refs []models.CredentialRef) string {
	if len(refs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(refs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ModelsManager implements models.ModelsConfigInterface using database storage
//
// M-1 shadow-write contract (per technical-analysis.md §API Contract
// store-layer write-path, lines 905-934, and Round-2 reviewer punch-list #12):
//
// Every write that updates `credentials_json` MUST also write
// `credential_id = model.Credentials[0].CredentialID` (Go-evaluated
// shadow) in the same statement. The shadow is computed in Go, NOT
// extracted from JSON in SQL — the application binds
// `model.Credentials[0].CredentialID` to the `credential_id`
// placeholder, then writes both columns in one INSERT/UPDATE.
// The `json_extract(credentials_json, '$[0].credential_id')` form
// (PG: `credentials_json::JSONB -> 0 ->> 'credential_id'`) appears
// ONLY in the down-migration (`028_add_model_credentials.down.sql`)
// when re-deriving the shadow during rollback.
//
// The shadow is read ONLY by legacy binaries and external tooling;
// this application reads `credentials_json` only.
//
// When migration 029 lands (DROP INDEX + DROP COLUMN `credential_id`),
// the shadow write is removed in the same commit, the engine field
// thread, and the comments above (this contract and the Task 13
// test are deleted together).
//
// Phase 2 wiring: ModelsManager OWNS the credentiallb.Engine —
// constructed in NewModelsManager (24h TTL / 5m sweep / time-seeded
// RNG / 60s default cooldown), seeded via RebindFromStore for every
// persisted model at startup, invalidated after every successful
// credentials write, and exposed to Phase 3 via the typed Engine()
// accessor (for the proxy.NewHandler 7th constructor arg). The
// engine itself never imports pkg/events: ModelsManager subscribes
// to model.credentials.changed ON BEHALF of the engine (P2-3) and
// forwards events into engine method calls by re-reading the model.
type ModelsManager struct {
	store     *Store
	qb        *QueryBuilder
	mu        sync.RWMutex
	engine    *credentiallb.Engine
	eventBus  *events.Bus
	subCh     chan events.Event // nil when constructed without a bus
	closeOnce sync.Once
}

// Engine returns the underlying *credentiallb.Engine. It is non-nil
// for every ModelsManager built via NewModelsManager; it returns nil
// only for hand-constructed instances (legacy tests). Callers must
// still handle the nil case — the engine's own methods are nil-safe
// no-ops, and ResolveInternalConfigWithAffinity delegates to the
// legacy single-credential resolution when the engine is nil.
func (m *ModelsManager) Engine() *credentiallb.Engine {
	return m.engine
}

// NewModelsManager creates a new database-backed models manager with
// an owned credential-LB engine (Phase 2).
//
// The engine is constructed with the production defaults (24h
// sliding-idle TTL, 5m janitor sweep, time-based RNG seed, 60s
// default cooldown) and seeded from the persisted models via
// RebindFromStore (startup rebind — last call wins, no bindings
// exist yet).
//
// eventBus may be nil: the engine then runs with direct-call
// invalidation only (AddModel/UpdateModel/RemoveModel/
// RemoveCredential still notify it synchronously) and a WARN is
// logged — the defensive still-in-config lookup check inside
// GetOrSelect bounds the damage of any missed invalidation to one
// request (P2-8). When non-nil, ModelsManager subscribes to
// model.credentials.changed on the engine's behalf; the drain
// goroutine exits when Close unsubscribes (the bus closes the
// channel) — call Close to release it.
func NewModelsManager(store *Store, eventBus *events.Bus) (*ModelsManager, error) {
	m := &ModelsManager{
		store:    store,
		qb:       NewQueryBuilder(store.Dialect),
		eventBus: eventBus,
		engine:   credentiallb.NewEngine(credentiallb.DefaultBindingTTL, credentiallb.DefaultSweepInterval, credentiallb.DefaultCredentialSeed, credentiallb.DefaultCooldown),
	}

	// Startup rebind: seed engine state for every persisted model so
	// the first request after boot resolves without a cold miss.
	for _, mc := range m.GetModels() {
		m.engine.RebindFromStore(mc.ID, mc.Credentials)
	}

	if eventBus == nil {
		log.Printf("[WARN] [credentiallb] ModelsManager constructed without an event bus — engine invalidation limited to direct write-path calls")
		return m, nil
	}

	ch, err := eventBus.Subscribe()
	if err != nil {
		// Non-fatal: subscription is belt-and-braces on top of the
		// synchronous write-path invalidation.
		log.Printf("[WARN] [credentiallb] credentials-changed subscription failed (%v) — engine invalidation limited to direct write-path calls", err)
		return m, nil
	}
	m.subCh = ch
	go m.drainCredentialsChanged(ch)
	return m, nil
}

// drainCredentialsChanged is the on-behalf-of-the-engine subscription
// loop (P2-3). The engine must not import pkg/events, so this loop
// lives here: it receives model.credentials.changed events published
// by AddModel/UpdateModel (and any future same-process writer) and
// refreshes the engine state by RE-READING the model — the DB is the
// source of truth, so a replayed or racing event converges to the
// committed state. OnModelChanged is idempotent (filter-survivors),
// making the direct write-path call plus this bus refresh a safe
// double-apply.
//
// The loop exits when the channel closes (Close → bus.Unsubscribe
// closes it). It must never call back into ModelsManager write
// paths: GetModel takes m.mu.RLock only, and Bus.Publish sends are
// non-blocking, so a publisher holding m.mu cannot deadlock with a
// drain iteration waiting on that RLock.
func (m *ModelsManager) drainCredentialsChanged(ch chan events.Event) {
	for evt := range ch {
		if evt.Type != credentiallb.EventCredentialsChanged {
			continue
		}
		data, ok := evt.Data.(map[string]interface{})
		if !ok {
			// Malformed payload (nil or non-map Data) must not panic
			// this goroutine: a dead drain loop would silently kill
			// bus-driven invalidation for the process lifetime.
			log.Printf("[WARN] [credentiallb] credentials-changed event with malformed Data (type %T) — skipping engine refresh", evt.Data)
			continue
		}
		modelID, _ := data["model_id"].(string)
		if modelID == "" {
			continue
		}
		mc := m.GetModel(modelID)
		if mc == nil {
			// Model removed between publish and drain (or removed by
			// another writer): drop the engine state entirely.
			m.engine.OnModelChanged(modelID, nil)
			continue
		}
		m.engine.OnModelChanged(modelID, mc.Credentials)
	}
}

// Close releases the ModelsManager's engine resources: it
// unsubscribes the credentials-changed listener (the bus closes the
// channel, terminating the drain goroutine — P2-3) and stops the
// engine janitor. Idempotent. Engine reads (GetOrSelect) keep
// working after Close (lazy expiry only). Skipping Close leaks only
// the janitor + drain goroutines for the remaining process lifetime.
func (m *ModelsManager) Close() {
	m.closeOnce.Do(func() {
		if m.eventBus != nil && m.subCh != nil {
			m.eventBus.Unsubscribe(m.subCh) // closes subCh → drain loop exits
		}
		m.engine.Stop() // nil-safe
	})
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
	return m.scanModelsContext(context.Background(), query, args...)
}

// scanModelsContext is the ctx-aware core of scanModels (Phase 1A —
// consumed by the strict list reads so the cache decorator can bound
// every strict fill with a 5s timeout; the legacy GetModels/
// GetEnabledModels keep their byte-identical behavior by delegating
// here with context.Background()).
func (m *ModelsManager) scanModelsContext(ctx context.Context, query string, args ...interface{}) ([]models.ModelConfig, error) {
	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return []models.ModelConfig{}, err
	}
	defer rows.Close()

	result := make([]models.ModelConfig, 0)
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
			&dbModel.ReleaseStreamChunkDeadline,
			&dbModel.Internal,
			&dbModel.CredentialsJSON,
			&dbModel.InternalBaseURL,
			&dbModel.InternalModel,
			&dbModel.PeakHourEnabled,
			&dbModel.PeakHourStart,
			&dbModel.PeakHourEnd,
			&dbModel.PeakHourTimezone,
			&dbModel.PeakHourModel,
			&dbModel.SecondaryUpstreamModel,
			&dbModel.ExcludeFromUltimateSwitching,
		)
		if err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		model := models.ModelConfig{
			ID:                           dbModel.ID,
			Name:                         dbModel.Name,
			Enabled:                      dbModel.isEnabled(),
			ReleaseStreamChunkDeadline:   models.Duration(time.Duration(dbModel.ReleaseStreamChunkDeadline) * time.Millisecond),
			Internal:                     dbModel.isInternal(),
			Credentials:                  parseCredentialsJSON(dbModel.CredentialsJSON),
			InternalBaseURL:              dbModel.InternalBaseURL,
			InternalModel:                dbModel.InternalModel,
			PeakHourEnabled:              dbModel.isPeakHourEnabled(),
			PeakHourStart:                dbModel.PeakHourStart,
			PeakHourEnd:                  dbModel.PeakHourEnd,
			PeakHourTimezone:             dbModel.PeakHourTimezone,
			PeakHourModel:                dbModel.PeakHourModel,
			SecondaryUpstreamModel:       dbModel.SecondaryUpstreamModel,
			ExcludeFromUltimateSwitching: dbModel.isExcludeFromUltimateSwitching(),
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return result, nil
}

// GetModel returns a single model configuration by ID, including internal fields
func (m *ModelsManager) GetModel(modelID string) *models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0), coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, peak_hour_start, peak_hour_end,
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, 0)
		FROM models WHERE id = ?`

	if m.store.Dialect == "postgres" {
		query = `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0), coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, peak_hour_start, peak_hour_end,
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, false)
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
		&dbModel.ReleaseStreamChunkDeadline,
		&dbModel.Internal,
		&dbModel.CredentialsJSON,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
		&dbModel.PeakHourEnabled,
		&dbModel.PeakHourStart,
		&dbModel.PeakHourEnd,
		&dbModel.PeakHourTimezone,
		&dbModel.PeakHourModel,
		&dbModel.SecondaryUpstreamModel,
		&dbModel.ExcludeFromUltimateSwitching,
	)
	if err != nil {
		return nil
	}

	model := &models.ModelConfig{
		ID:                           dbModel.ID,
		Name:                         dbModel.Name,
		Enabled:                      dbModel.isEnabled(),
		ReleaseStreamChunkDeadline:   models.Duration(time.Duration(dbModel.ReleaseStreamChunkDeadline) * time.Millisecond),
		Internal:                     dbModel.isInternal(),
		Credentials:                  parseCredentialsJSON(dbModel.CredentialsJSON),
		InternalBaseURL:              dbModel.InternalBaseURL,
		InternalModel:                dbModel.InternalModel,
		PeakHourEnabled:              dbModel.isPeakHourEnabled(),
		PeakHourStart:                dbModel.PeakHourStart,
		PeakHourEnd:                  dbModel.PeakHourEnd,
		PeakHourTimezone:             dbModel.PeakHourTimezone,
		PeakHourModel:                dbModel.PeakHourModel,
		SecondaryUpstreamModel:       dbModel.SecondaryUpstreamModel,
		ExcludeFromUltimateSwitching: dbModel.isExcludeFromUltimateSwitching(),
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

// GetModelByName returns a single model configuration by name, including internal fields
func (m *ModelsManager) GetModelByName(modelName string) *models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0), coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, peak_hour_start, peak_hour_end,
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, 0)
		FROM models WHERE name = ?`

	if m.store.Dialect == "postgres" {
		query = `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0), coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, peak_hour_start, peak_hour_end,
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, false)
			FROM models WHERE name = $1`
	}

	var dbModel dbModelRow
	err := m.store.DB.QueryRowContext(context.Background(), query, modelName).Scan(
		&dbModel.ID,
		&dbModel.Name,
		&dbModel.Enabled,
		&dbModel.FallbackChainJSON,
		&dbModel.TruncateParamsJSON,
		&dbModel.CreatedAt,
		&dbModel.UpdatedAt,
		&dbModel.ReleaseStreamChunkDeadline,
		&dbModel.Internal,
		&dbModel.CredentialsJSON,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
		&dbModel.PeakHourEnabled,
		&dbModel.PeakHourStart,
		&dbModel.PeakHourEnd,
		&dbModel.PeakHourTimezone,
		&dbModel.PeakHourModel,
		&dbModel.SecondaryUpstreamModel,
		&dbModel.ExcludeFromUltimateSwitching,
	)
	if err != nil {
		return nil
	}

	model := &models.ModelConfig{
		ID:                           dbModel.ID,
		Name:                         dbModel.Name,
		Enabled:                      dbModel.isEnabled(),
		ReleaseStreamChunkDeadline:   models.Duration(time.Duration(dbModel.ReleaseStreamChunkDeadline) * time.Millisecond),
		Internal:                     dbModel.isInternal(),
		Credentials:                  parseCredentialsJSON(dbModel.CredentialsJSON),
		InternalBaseURL:              dbModel.InternalBaseURL,
		InternalModel:                dbModel.InternalModel,
		PeakHourEnabled:              dbModel.isPeakHourEnabled(),
		PeakHourStart:                dbModel.PeakHourStart,
		PeakHourEnd:                  dbModel.PeakHourEnd,
		PeakHourTimezone:             dbModel.PeakHourTimezone,
		PeakHourModel:                dbModel.PeakHourModel,
		SecondaryUpstreamModel:       dbModel.SecondaryUpstreamModel,
		ExcludeFromUltimateSwitching: dbModel.isExcludeFromUltimateSwitching(),
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
		return []models.ModelConfig{}
	}
	return result
}

// GetEnabledModels returns only enabled model configurations
func (m *ModelsManager) GetEnabledModels() []models.ModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result, err := m.scanModels(m.qb.GetEnabledModels())
	if err != nil {
		return []models.ModelConfig{}
	}
	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 1A (db-cache-layer) — strict, error-propagating read methods.
//
// These are ADDITIVE ONLY (planner ruling C1): every legacy signature
// above is byte-identical; the strict twins exist so the pkg/modelscache
// decorator can distinguish row-not-found from infrastructure errors
// and never mistake a DB outage for an empty configuration (the
// 2026-08-27 conflated-nil misroute class). The decorator is the sole
// consumer; no production caller reaches the legacy silent-nil paths
// once the decorator is wired at cmd/main.go.
// ─────────────────────────────────────────────────────────────────────────────

// Strict-read sentinel errors (Phase 1A). ErrModelNotFound and
// ErrCredentialNotFound WRAP sql.ErrNoRows so consumers can use either
// errors.Is(err, ErrModelNotFound) or errors.Is(err, sql.ErrNoRows).
var (
	// ErrModelNotFound — the queried model row does not exist. Wraps
	// sql.ErrNoRows (satisfies errors.Is(err, sql.ErrNoRows)).
	ErrModelNotFound = fmt.Errorf("model not found: %w", sql.ErrNoRows)
	// ErrCredentialNotFound — the queried credential row does not exist.
	// Wraps sql.ErrNoRows.
	ErrCredentialNotFound = fmt.Errorf("credential not found: %w", sql.ErrNoRows)
	// ErrDecryptionFailed — the stored credential API key could not be
	// decrypted (GetCredentialStrict hardening: the legacy GetCredential
	// WARNs and serves ciphertext as the APIKey; the strict twin NEVER
	// serves ciphertext — arch §5 / matrix row 5).
	ErrDecryptionFailed = errors.New("credential decryption failed")
	// ErrDecryptionFailureInScan — a bulk credential scan hit a row whose
	// stored API key could not be decrypted; the whole scan aborts
	// (planner ruling J: a partial swap would mask configuration
	// corruption). The partial slice returned alongside this error must
	// be treated as unusable by callers (boot priming / reconciler
	// abort on ANY non-nil error).
	ErrDecryptionFailureInScan = errors.New("credential scan aborted: decryption failure")
)

// modelSelectColumnsPG and modelSelectColumnsSQLite are the shared
// column lists of the single-model strict SELECTs, one per dialect
// (PostgreSQL spells the boolean coalesce literals false/true; SQLite
// uses 0/1). The embedded continuation-line indentation is
// load-bearing: modelSelectQuery composes these with the FROM/WHERE
// tail so the emitted query text stays byte-identical to the former
// per-site literals (same columns, same order, same placeholder
// semantics).
const modelSelectColumnsPG = `id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0), coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, peak_hour_start, peak_hour_end,
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, false)`

const modelSelectColumnsSQLite = `id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0), coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, peak_hour_start, peak_hour_end,
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''), coalesce(exclude_from_ultimate_switching, 0)`

// modelSelectQuery returns the dialect-appropriate single-model
// SELECT used by the strict reads (same shape the legacy GetModel /
// GetModelByName build inline). where is the column the caller keys
// on ("id" or "name").
func (m *ModelsManager) modelSelectQuery(where string) string {
	if m.store.Dialect == PostgreSQL {
		return "SELECT " + modelSelectColumnsPG + "\n\t\t\tFROM models WHERE " + where + " = $1"
	}
	return "SELECT " + modelSelectColumnsSQLite + "\n\t\tFROM models WHERE " + where + " = ?"
}

// dbModelRowToConfig converts a scanned dbModelRow into the public
// models.ModelConfig shape (shared by the strict single-row reads).
func dbModelRowToConfig(dbModel *dbModelRow) *models.ModelConfig {
	model := &models.ModelConfig{
		ID:                           dbModel.ID,
		Name:                         dbModel.Name,
		Enabled:                      dbModel.isEnabled(),
		ReleaseStreamChunkDeadline:   models.Duration(time.Duration(dbModel.ReleaseStreamChunkDeadline) * time.Millisecond),
		Internal:                     dbModel.isInternal(),
		Credentials:                  parseCredentialsJSON(dbModel.CredentialsJSON),
		InternalBaseURL:              dbModel.InternalBaseURL,
		InternalModel:                dbModel.InternalModel,
		PeakHourEnabled:              dbModel.isPeakHourEnabled(),
		PeakHourStart:                dbModel.PeakHourStart,
		PeakHourEnd:                  dbModel.PeakHourEnd,
		PeakHourTimezone:             dbModel.PeakHourTimezone,
		PeakHourModel:                dbModel.PeakHourModel,
		SecondaryUpstreamModel:       dbModel.SecondaryUpstreamModel,
		ExcludeFromUltimateSwitching: dbModel.isExcludeFromUltimateSwitching(),
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

// getModelStrict is the shared core of GetModelStrict /
// GetModelByNameStrict: run the single-row query, discriminate
// not-found (→ ErrModelNotFound, which wraps sql.ErrNoRows) from
// infrastructure errors (→ propagate unchanged).
func (m *ModelsManager) getModelStrict(ctx context.Context, query, arg string) (*models.ModelConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dbModel dbModelRow
	err := m.store.DB.QueryRowContext(ctx, query, arg).Scan(
		&dbModel.ID,
		&dbModel.Name,
		&dbModel.Enabled,
		&dbModel.FallbackChainJSON,
		&dbModel.TruncateParamsJSON,
		&dbModel.CreatedAt,
		&dbModel.UpdatedAt,
		&dbModel.ReleaseStreamChunkDeadline,
		&dbModel.Internal,
		&dbModel.CredentialsJSON,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
		&dbModel.PeakHourEnabled,
		&dbModel.PeakHourStart,
		&dbModel.PeakHourEnd,
		&dbModel.PeakHourTimezone,
		&dbModel.PeakHourModel,
		&dbModel.SecondaryUpstreamModel,
		&dbModel.ExcludeFromUltimateSwitching,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrModelNotFound
		}
		return nil, err
	}
	return dbModelRowToConfig(&dbModel), nil
}

// GetModelStrict is the error-propagating twin of GetModel: returns
// (nil, ErrModelNotFound) when the row does not exist — an error that
// satisfies errors.Is(err, sql.ErrNoRows) — and (nil, err) with the
// driver's error unchanged on infrastructure failure. Never conflates
// the two (the legacy GetModel returns nil for both — the 2026-08-27
// incident crux).
func (m *ModelsManager) GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error) {
	return m.getModelStrict(ctx, m.modelSelectQuery("id"), modelID)
}

// GetModelByNameStrict is the error-propagating twin of GetModelByName
// with the same error discrimination as GetModelStrict.
func (m *ModelsManager) GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error) {
	return m.getModelStrict(ctx, m.modelSelectQuery("name"), modelName)
}

// GetModelsStrict is the error-propagating twin of GetModels: a
// transient infrastructure error returns (nil, err); a legitimate
// empty table returns ([], nil). Consumers (boot priming / reconciler)
// treat ANY non-nil error as "snapshot unusable — do not swap".
func (m *ModelsManager) GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result, err := m.scanModelsContext(ctx, m.qb.GetAllModels())
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetEnabledModelsStrict is the error-propagating twin of
// GetEnabledModels (same contract as GetModelsStrict).
func (m *ModelsManager) GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result, err := m.scanModelsContext(ctx, m.qb.GetEnabledModels())
	if err != nil {
		return nil, err
	}
	return result, nil
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
		&dbModel.ReleaseStreamChunkDeadline,
		&dbModel.Internal,
		&dbModel.CredentialsJSON,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
		&dbModel.PeakHourEnabled,
		&dbModel.PeakHourStart,
		&dbModel.PeakHourEnd,
		&dbModel.PeakHourTimezone,
		&dbModel.PeakHourModel,
		&dbModel.SecondaryUpstreamModel,
		&dbModel.ExcludeFromUltimateSwitching,
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
		&dbModel.ReleaseStreamChunkDeadline,
		&dbModel.Internal,
		&dbModel.CredentialsJSON,
		&dbModel.InternalBaseURL,
		&dbModel.InternalModel,
		&dbModel.PeakHourEnabled,
		&dbModel.PeakHourStart,
		&dbModel.PeakHourEnd,
		&dbModel.PeakHourTimezone,
		&dbModel.PeakHourModel,
		&dbModel.SecondaryUpstreamModel,
		&dbModel.ExcludeFromUltimateSwitching,
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

// AddModel adds a new model configuration.
//
// M-1 shadow-write contract (see ModelsManager doc):
// Every write that updates `credentials_json` MUST also write
// `credential_id = model.Credentials[0].CredentialID` (Go-evaluated
// shadow) in the same INSERT. The shadow is computed in Go, NOT
// extracted from JSON in SQL — see InsertModel() doc for the full
// shape. The `json_extract` form lives ONLY in the down-migration.
//
// Phase-1 W4 hardening: per-model validation (weight>0, dup IDs,
// 16-cap, provider-match, internal+creds consistency) runs BEFORE
// the write lock — see validateBeforeWrite. This brings the DB
// write path to parity with ModelsConfig.Validate() so any model
// saved via the store manager is enforcement-equivalent to one
// saved via the JSON-backed manager. Pre-Phase-1 store tests that
// relied on AddModel accepting an invalid shape (e.g.
// TestModelsManager_AddModel_SecondaryWithNonInternal) are updated
// to assert the new reject behavior; the rule is preserved verbatim.
func (m *ModelsManager) AddModel(model models.ModelConfig) error {
	if model.ID == "" {
		return models.ErrInvalidModelID
	}
	if model.Name == "" {
		return models.ErrInvalidModelName
	}

	// Phase-1 W4 hardening — validate before write (cheap guards
	// above already passed; per-ref rules + secondary + peak-hour
	// rules live in validateBeforeWrite → validateModelAgainstCredentials).
	if err := m.validateBeforeWrite(model); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, model.ID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err == nil {
		return models.ErrDuplicateModelID
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	// M-1 (Go-computed shadow): marshal Credentials → credentials_json,
	// and derive the credential_id shadow from Credentials[0] in Go.
	// The QueryBuilder writes BOTH columns in the same INSERT.
	credentialsJSON := marshalCredentialsJSON(model.Credentials)
	shadowCredentialID := model.PrimaryCredentialID() // "" when no refs

	// Convert Duration (nanoseconds) to milliseconds for storage
	releaseStreamChunkDeadlineMs := int64(time.Duration(model.ReleaseStreamChunkDeadline).Milliseconds())

	insertQuery := m.qb.InsertModel()
	_, err = m.store.DB.ExecContext(context.Background(), insertQuery,
		model.ID,
		model.Name,
		m.qb.BooleanLiteral(model.Enabled),
		string(fallbackJSON),
		string(truncateJSON),
		m.qb.BooleanLiteral(model.Internal),
		credentialsJSON,    // credentials_json (JSON text)
		shadowCredentialID, // credential_id (Go-computed shadow, see ModelsManager doc)
		model.InternalBaseURL,
		model.InternalModel,
		releaseStreamChunkDeadlineMs,
		m.qb.BooleanLiteral(model.PeakHourEnabled),
		model.PeakHourStart,
		model.PeakHourEnd,
		model.PeakHourTimezone,
		model.PeakHourModel,
		model.SecondaryUpstreamModel,
		m.qb.BooleanLiteral(model.ExcludeFromUltimateSwitching),
	)
	if err != nil {
		return err // no engine invalidation on write failure (P2-4)
	}

	// Phase 2: engine invalidation strictly AFTER the successful DB
	// write (E-2 filter-survivors semantics — not clear-all), plus the
	// bus broadcast for the on-behalf-of-the-engine drain loop.
	// Engine methods are nil-safe, so a hand-constructed manager
	// without an engine is fine here.
	m.engine.OnModelChanged(model.ID, model.Credentials)
	m.publishCredentialsChanged(model.ID)
	return nil
}

// UpdateModel updates an existing model configuration.
//
// M-1 shadow-write contract (see ModelsManager doc): every UPDATE
// that touches `credentials_json` MUST also rewrite
// `credential_id = model.Credentials[0].CredentialID` (Go-evaluated
// shadow) in the same statement. Both columns are bound in the
// UPDATE; the shadow is NOT SQL-extracted.
//
// Phase-1 W4 hardening: per-model validation runs BEFORE the write
// lock (see validateBeforeWrite). The cheap guards (empty ID /
// Name, ID-changed, not-found) stay inline and run first.
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

	// Phase-1 W4 hardening — validate before write.
	if err := m.validateBeforeWrite(model); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check model exists
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err != nil {
		return models.ErrModelNotFound
	}

	fallbackJSON, _ := json.Marshal(model.FallbackChain)
	truncateJSON, _ := json.Marshal(model.TruncateParams)

	// M-1 (Go-computed shadow): marshal Credentials → credentials_json,
	// derive the credential_id shadow from Credentials[0] in Go.
	credentialsJSON := marshalCredentialsJSON(model.Credentials)
	shadowCredentialID := model.PrimaryCredentialID() // "" when no refs

	// Convert Duration (nanoseconds) to milliseconds for storage
	releaseStreamChunkDeadlineMs := int64(time.Duration(model.ReleaseStreamChunkDeadline).Milliseconds())

	updateQuery := m.qb.UpdateModel()
	_, err = m.store.DB.ExecContext(context.Background(), updateQuery,
		model.Name,
		m.qb.BooleanLiteral(model.Enabled),
		string(fallbackJSON),
		string(truncateJSON),
		m.qb.BooleanLiteral(model.Internal),
		credentialsJSON,    // credentials_json (JSON text)
		shadowCredentialID, // credential_id (Go-computed shadow, see ModelsManager doc)
		model.InternalBaseURL,
		model.InternalModel,
		releaseStreamChunkDeadlineMs,
		m.qb.BooleanLiteral(model.PeakHourEnabled),
		model.PeakHourStart,
		model.PeakHourEnd,
		model.PeakHourTimezone,
		model.PeakHourModel,
		model.SecondaryUpstreamModel,
		m.qb.BooleanLiteral(model.ExcludeFromUltimateSwitching),
		modelID,
	)
	if err != nil {
		return err // no engine invalidation on write failure (P2-4)
	}

	// Phase 2: engine invalidation strictly AFTER the successful DB
	// write (E-2 filter-survivors) + bus broadcast. Holding m.mu here
	// is safe: Bus.Publish sends are non-blocking and the drain loop
	// only takes m.mu.RLock (no publish→drain cycle).
	m.engine.OnModelChanged(model.ID, model.Credentials)
	m.publishCredentialsChanged(model.ID)
	return nil
}

// RemoveModel removes a model configuration
func (m *ModelsManager) RemoveModel(modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check model exists
	query := m.qb.GetModelByID()
	row := m.store.DB.QueryRowContext(context.Background(), query, modelID)
	var dummy string
	err := row.Scan(&dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy, &dummy)
	if err != nil {
		return models.ErrModelNotFound
	}

	deleteQuery := m.qb.DeleteModel()
	_, err = m.store.DB.ExecContext(context.Background(), deleteQuery, modelID)
	if err != nil {
		return err // no engine invalidation on delete failure (P2-4)
	}

	// Phase 2: engine invalidation strictly AFTER the successful DB
	// delete (Task 8) — nil refs drop the model's engine state
	// (bindings, cooldowns, the e.models entry) so nothing lingers
	// past removal. A failed delete above leaves engine state intact.
	m.engine.OnModelChanged(modelID, nil)
	return nil
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

		// Phase-1 W4 hardening: per-model rules are shared with the
		// single-write AddModel/UpdateModel path via
		// validateModelAgainstCredentials (see below).
		if err := m.validateModelAgainstCredentials(model, credentialIDs); err != nil {
			return err
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

// validateModelAgainstCredentials runs the per-model rules used by
// both the bulk ModelsManager.Validate() and the single-write paths
// AddModel/UpdateModel (Phase-1 W4 hardening — wire Validate() into
// the DB write path so per-ref rules — weight>0, dup IDs, 16-cap,
// provider-match, internal+creds consistency — are enforced on every
// store write, not only at Validate() time).
//
// `credentialIDs` is a precomputed set built by the caller (snapshot
// from GetCredentials before the write lock; under read-lock during
// Validate). The provider-match invariant resolves per-ref via
// m.GetCredential — the canonical lookup against the persisted
// credential row.
//
// Cheap guards (empty ID / Name, duplicate / not-found) are NOT
// re-run here — those live in AddModel / UpdateModel (write path) and
// Validate (bulk path iterates `modelList` from the same source).
//
// Returns the same error texts the JSON-backed ModelsConfig.Validate
// path emits, so downstream callers (pkg/ui/server.go surfaces, the
// ModelsConfigInterface.Save callers, the existing 9-case validation
// matrix test) keep matching.
func (m *ModelsManager) validateModelAgainstCredentials(model models.ModelConfig, credentialIDs map[string]bool) error {
	if model.Internal {
		// Phase 1: internal models must carry at least one credential ref.
		// Empty Credentials ⇒ keep the legacy error text verbatim so
		// downstream callers that grep the message keep working.
		if len(model.Credentials) == 0 {
			return fmt.Errorf("model %s: credential_id is required when internal is true", model.ID)
		}

		// Per-ref validation (Task 6 validation matrix).
		seen := make(map[string]bool, len(model.Credentials))
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
		if len(model.Credentials) > models.MaxCredentialRefs {
			return fmt.Errorf("model %s: credentials list exceeds max of %d refs", model.ID, models.MaxCredentialRefs)
		}

		// Provider-match invariant: every ref's credential must share
		// Credentials[0]'s provider (case-insensitive). Looks up via
		// GetCredential so the comparison reflects the actual stored
		// credential row.
		primaryCred := m.GetCredential(model.Credentials[0].CredentialID)
		if primaryCred != nil {
			primaryProvider := strings.ToLower(primaryCred.Provider)
			for idx, ref := range model.Credentials[1:] {
				refCred := m.GetCredential(ref.CredentialID)
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
	// Rejecting non-internal-with-creds would break the D3 path.
	// The plan's Task 6 validation rule "non-internal with
	// non-empty Credentials ⇒ reject" was an oversight vs. the
	// existing D3-probe call site; preserving the existing
	// behavior is the contract-correct call.

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
		if err := models.ValidateTimeFormat(model.PeakHourStart); err != nil {
			return fmt.Errorf("model %s: invalid peak_hour_start: %w", model.ID, err)
		}
		if err := models.ValidateTimeFormat(model.PeakHourEnd); err != nil {
			return fmt.Errorf("model %s: invalid peak_hour_end: %w", model.ID, err)
		}

		// Validate UTC offset
		if err := models.ValidateUTCOffset(model.PeakHourTimezone); err != nil {
			return fmt.Errorf("model %s: invalid peak_hour_timezone: %w", model.ID, err)
		}

		// Reject same start and end times (would create empty or full-day window)
		if model.PeakHourStart == model.PeakHourEnd {
			return fmt.Errorf("model %s: peak_hour_start and peak_hour_end cannot be the same", model.ID)
		}
	}

	return nil
}

// validateBeforeWrite runs the per-model validation rules from
// validateModelAgainstCredentials against a fresh credential-IDs
// snapshot, BEFORE acquiring the write lock. The snapshot is built
// outside the write lock (GetCredentials takes RLock; a write Lock
// would deadlock on the re-entrant read).
//
// Phase-1 W4 hardening — AddModel / UpdateModel call this so the
// store write path enforces the same per-ref rules Validate() does.
// The cheap guards (empty ID / Name, duplicate / not-found) stay in
// AddModel / UpdateModel and run before this helper.
func (m *ModelsManager) validateBeforeWrite(model models.ModelConfig) error {
	creds := m.GetCredentials() // RLock — OK, no write Lock held yet
	credentialIDs := make(map[string]bool, len(creds))
	for _, c := range creds {
		credentialIDs[c.ID] = true
	}
	return m.validateModelAgainstCredentials(model, credentialIDs)
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

// GetCredentialStrict is the error-propagating twin of GetCredential:
// returns (nil, ErrCredentialNotFound) — wrapping sql.ErrNoRows — when
// the row does not exist, (nil, err) unchanged on infrastructure
// failure, and CRITICALLY (nil, ErrDecryptionFailed) when the stored
// API key cannot be decrypted. The legacy GetCredential WARNs and
// serves the raw ciphertext as the APIKey (the legacy GetCredential's
// ciphertext-serving hazard);
// the strict twin NEVER serves ciphertext (arch §5, matrix row 5).
func (m *ModelsManager) GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials WHERE id = ?`
	if m.store.Dialect == PostgreSQL {
		query = `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials WHERE id = $1`
	}

	var dbCred dbCredentialRow
	err := m.store.DB.QueryRowContext(ctx, query, id).Scan(
		&dbCred.ID,
		&dbCred.Provider,
		&dbCred.APIKey,
		&dbCred.BaseURL,
		&dbCred.CreatedAt,
		&dbCred.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}

	cred := &models.CredentialConfig{
		ID:       dbCred.ID,
		Provider: dbCred.Provider,
		APIKey:   dbCred.APIKey,
		BaseURL:  dbCred.BaseURL,
	}

	// Decrypt API key — decrypt failure is a HARD error here (never
	// serve ciphertext).
	if dbCred.APIKey != "" {
		decrypted, err := crypto.Decrypt(dbCred.APIKey)
		if err != nil {
			return nil, ErrDecryptionFailed
		}
		cred.APIKey = decrypted
	}

	return cred, nil
}

// GetCredentialsStrict is the error-propagating twin of GetCredentials.
// Per planner ruling J: a per-row crypto.Decrypt failure ABORTS the
// whole scan and returns (partial, ErrDecryptionFailureInScan) — the
// row with the raw ciphertext MUST NOT appear in the partial slice,
// and consumers must treat any non-nil error as "do not swap" (a
// partial swap would risk masking configuration corruption such as a
// wrong encryption key or manual SQL edit). A legitimate empty
// credential table returns ([], nil); an infrastructure error returns
// (nil, err).
func (m *ModelsManager) GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	query := `SELECT id, provider, api_key, coalesce(base_url, ''), created_at, updated_at FROM credentials ORDER BY id`
	rows, err := m.store.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.CredentialConfig
	for rows.Next() {
		var dbCred dbCredentialRow
		if err := rows.Scan(&dbCred.ID, &dbCred.Provider, &dbCred.APIKey, &dbCred.BaseURL, &dbCred.CreatedAt, &dbCred.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		cred := models.CredentialConfig{
			ID:       dbCred.ID,
			Provider: dbCred.Provider,
			APIKey:   dbCred.APIKey,
			BaseURL:  dbCred.BaseURL,
		}

		// Decrypt API key — per-row failure aborts the entire scan
		// ([PLANNER-J]); the offending row is never appended.
		if cred.APIKey != "" {
			decrypted, err := crypto.Decrypt(cred.APIKey)
			if err != nil {
				return result, ErrDecryptionFailureInScan
			}
			cred.APIKey = decrypted
		}

		result = append(result, cred)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("rows error: %w", err)
	}
	return result, nil
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
//
// In-use guard scans every model's Credentials slice (the new
// ordered, weighted list). When a single-credential model with the
// legacy CredentialID field would have been caught by `model.CredentialID
// == id`, the new guard catches it on `Credentials[i].CredentialID == id`
// (any ref). Phase 2's engine invalidation hook follows the same
// pattern.
func (m *ModelsManager) RemoveCredential(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if credential is in use
	modelList, _ := m.scanModels(m.qb.GetAllModels())
	for _, model := range modelList {
		for _, ref := range model.Credentials {
			if ref.CredentialID == id {
				return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, models.ErrCredentialInUse)
			}
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

	// Phase 2: clear engine bindings AND cooldowns for the deleted
	// credential across all models (S6 combined pass). The in-use
	// guard above makes in-process deletion of a referenced
	// credential impossible; this hook is the defensive path for
	// direct external SQL deletes and mid-flight races. Nil-safe.
	m.engine.OnCredentialDeleted(id)
	return nil
}

// publishCredentialsChanged broadcasts model.credentials.changed on
// the bus (nil-bus tolerant). The on-behalf-of-the-engine drain loop
// refreshes engine state from these events; the synchronous
// OnModelChanged call in the write path remains the primary
// invalidation.
func (m *ModelsManager) publishCredentialsChanged(modelID string) {
	if m.eventBus == nil {
		return
	}
	m.eventBus.Publish(events.Event{
		Type:      credentiallb.EventCredentialsChanged,
		Timestamp: time.Now().Unix(),
		Data:      map[string]interface{}{"model_id": modelID},
	})
}

// ResolveInternalConfig resolves the full internal upstream configuration.
//
// Legacy single-credential resolution — always uses Credentials[0].
// The Phase 2 LB engine (pkg/credentiallb) introduces
// ResolveInternalConfigWithAffinity for multi-credential models with
// conversation-sticky affinity; this stays as the single-credential
// fast path (byte-identical to pre-change behavior) and as the
// nil-engine delegation target.
func (m *ModelsManager) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	modelConfig := m.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	primaryCredentialID := modelConfig.PrimaryCredentialID()
	if primaryCredentialID == "" {
		return "", "", "", "", false
	}

	cred := m.GetCredential(primaryCredentialID)
	if cred == nil {
		return "", "", "", "", false
	}

	provider, apiKey, baseURL, model = resolveWithCredential(modelConfig, cred)
	return provider, apiKey, baseURL, model, true
}

// resolveWithCredential resolves every resolution field that depends
// only on (modelConfig, credential): provider from the credential,
// baseURL with the model's InternalBaseURL override taking
// precedence, and InternalModel with peak-hour substitution
// (ResolvePeakHourModel + the [PEAK-HOUR] log line).
//
// SHARED by the legacy 5-tuple path and the affinity struct path
// (P2-5) so peak-hour substitution cannot drift between them.
func resolveWithCredential(modelConfig *models.ModelConfig, cred *models.CredentialConfig) (provider, apiKey, baseURL, actualModel string) {
	// Resolve provider: use credential provider
	provider = cred.Provider

	// Resolve baseURL: model override > credential
	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	// Determine actual model: check peak hour first
	actualModel = modelConfig.InternalModel
	if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s",
			modelConfig.ID, peakModel, modelConfig.InternalModel)
		actualModel = peakModel
	}

	return provider, cred.APIKey, baseURL, actualModel
}

// ResolvedCredential is the return shape of ResolveInternalConfigWithAffinity
// (reviewer pass #3, leader-ruled struct form). Field mapping vs the
// legacy 5-tuple: Provider/APIKey/BaseURL/InternalModel ↔ the legacy
// provider/apiKey/baseURL/model; the trailing ok bool of the method ↔
// the legacy ok. NEW fields: CredentialID (which credential in the
// model's list was selected — for #16f prompt-cache observability and
// the phase3 model_credential_selected event payload) and NewlyBound
// (W-1 signal: true ⇔ this call stored a new engine binding — the
// only way newlyBound flows from the engine's binding-store side
// effect through to the proxy/ultimatemodel call sites).
//
// Phase 3: this is now an alias of models.ResolvedCredential. The
// canonical declaration lives in pkg/models so the ModelsConfigInterface
// method signature can refer to it without an import cycle. The alias
// here is the SOLE assignment / read target — the dbStore
// implementation populates models.ResolvedCredential fields directly.
type ResolvedCredential = models.ResolvedCredential

// ResolveInternalConfigWithAffinity resolves a credential for modelID,
// applying the LB engine when the model has 2+ credentials.
//
// Branch table (contract Task 9):
//   - m.engine == nil            → mirror the legacy single-credential
//     resolution (struct form); ok=false on failure.
//   - model nil / non-internal   → (ResolvedCredential{}, false).
//   - len(Credentials) == 0      → legacy-equivalent: ok=false.
//   - len(Credentials) == 1      → E-3 fast path: resolve
//     Credentials[0] directly with NO engine call and NO map writes —
//     byte-identical to today, zero engine overhead. NewlyBound=false.
//   - len(Credentials) >= 2      → engine.GetOrSelect(modelID,
//     conversationKey); ErrNoCredentials → ok=false; the picked
//     credential's row missing (deleted mid-flight, e.g. direct SQL
//     delete) → defensive engine invalidation for that credential
//     (bindings + cooldowns dropped so the NEXT call re-selects a
//     live credential) and THIS call returns ok=false.
//
// W-2: conversationKey == "" ⇒ the engine performs a fresh weighted
// pick per call with NO binding stored; per C2 the returned
// NewlyBound is false for empty-key picks (newlyBound ⇔ a binding was
// stored on this call). DEBUG-log observability for empty-key fresh
// picks is phase3's responsibility (handler-side) — this layer stays
// silent for them.
func (m *ModelsManager) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (ResolvedCredential, bool) {
	modelConfig := m.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return ResolvedCredential{}, false
	}

	// Nil engine (hand-constructed instances) and empty credential
	// lists both degrade to the legacy single-credential shape:
	// PrimaryCredentialID → GetCredential → shared resolver. With no
	// primary configured this is ok=false, identical to today.
	if m.engine == nil || len(modelConfig.Credentials) == 0 {
		primaryID := modelConfig.PrimaryCredentialID()
		if primaryID == "" {
			return ResolvedCredential{}, false
		}
		cred := m.GetCredential(primaryID)
		if cred == nil {
			return ResolvedCredential{}, false
		}
		provider, apiKey, baseURL, actualModel := resolveWithCredential(modelConfig, cred)
		return ResolvedCredential{
			Provider:      provider,
			APIKey:        apiKey,
			BaseURL:       baseURL,
			InternalModel: actualModel,
			CredentialID:  primaryID,
			NewlyBound:    false,
		}, true
	}

	// E-3 single-credential fast path: NO engine call, NO map writes.
	if len(modelConfig.Credentials) == 1 {
		fastID := modelConfig.Credentials[0].CredentialID
		cred := m.GetCredential(fastID)
		if cred == nil {
			return ResolvedCredential{}, false
		}
		provider, apiKey, baseURL, actualModel := resolveWithCredential(modelConfig, cred)
		return ResolvedCredential{
			Provider:      provider,
			APIKey:        apiKey,
			BaseURL:       baseURL,
			InternalModel: actualModel,
			CredentialID:  fastID,
			NewlyBound:    false, // fast path never stores a binding
		}, true
	}

	// 2+ credentials: engine path (affinity for non-empty keys, fresh
	// weighted pick without binding for empty keys).
	credID, newlyBound, err := m.engine.GetOrSelect(modelID, conversationKey)
	if err != nil || credID == "" {
		return ResolvedCredential{}, false
	}
	cred := m.GetCredential(credID)
	if cred == nil {
		// Deleted mid-flight (direct SQL delete raced the resolution —
		// the credentials_json still references the dead row).
		// Defensive healing: (1) OnCredentialDeleted clears every
		// binding + cooldown row for the dead credential (S6 hygiene —
		// it can never be selected again); (2) ExcludeAndReselect
		// seeds a default-length cooldown on it and rebinds THIS
		// conversation to a healthy credential, so the NEXT call
		// deterministically re-selects a live credential instead of
		// re-picking the dead one until the dangling ref is cleaned
		// up. Per the contract THIS call still fails with ok=false.
		//
		// DELIBERATE RE-SEED ASYMMETRY (Item 6a): OnCredentialDeleted
		// above clears st.cooldowns[credID] to zero; the very next
		// ExcludeAndReselect call re-seeds a fresh defaultCooldown
		// row on the SAME credential. The asymmetry is intentional:
		// if we left the cooldown slot empty after the S6 hygiene
		// clear, a subsequent GetOrSelect could pick the dead
		// credential before the dangling credentials_json ref is
		// cleaned up by an external writer — re-seeding the
		// cooldown deterministically blocks every future selection
		// of credID until the row expires (or OnCredentialDeleted
		// re-fires after another failed resolution). The Cooldowns
		// GAUGE is therefore >= 1 for the lifetime of the dangling
		// ref + the cooldown TTL — that is the heal's visible
		// side-effect, pinned by the Failovers==1 && Cooldowns==1
		// assertions in TestResolveInternalConfigWithAffinity_BranchTable.
		log.Printf("[WARN] [credentiallb] credential %s vanished mid-flight for model %s — clearing engine state, resolution fails this call", credID, modelID)
		m.engine.OnCredentialDeleted(credID)
		m.engine.ExcludeAndReselect(modelID, conversationKey, credID, 0)
		return ResolvedCredential{}, false
	}

	provider, apiKey, baseURL, actualModel := resolveWithCredential(modelConfig, cred)
	return ResolvedCredential{
		Provider:      provider,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: actualModel,
		CredentialID:  credID,
		NewlyBound:    newlyBound,
	}, true
}

// ResolveInternalConfigWithAffinityCached is the resolver variant the
// pkg/modelscache decorator consumes on its hot path (planner
// correction 3 / council D3): identical branching to
// ResolveInternalConfigWithAffinity, but (a) it skips the m.GetModel
// re-read at the top — the caller supplies the CACHED model config —
// and (b) every m.GetCredential(...) call is replaced by the supplied
// credLookup closure, which the decorator backs with its in-memory
// credential map. The 2+-credentials engine path (GetOrSelect) and the
// dangling-reference defensive heal (OnCredentialDeleted +
// ExcludeAndReselect) are preserved verbatim, so multi-credential
// affinity and credFailover behave identically to the DB-backed
// resolver — with ZERO database reads on a warm cache.
//
// Callers hold the decorator's cache lock; this method takes no lock
// of its own — it touches only the supplied closures and the engine's
// own mutex (on the 2+-credentials path). It performs no I/O.
//
// CONFIRMED INTENT (leader ruling, 2026-08-28): the supplied
// credLookup does NOT strict-fill credentials that are missing from
// the cache — a credential added to the DB after boot becomes
// visible to resolution only via the next reconciler sweep (≤60s at
// the default interval). This is the accepted consistency window;
// do not "fix" it by strict-filling inside the resolver.
func (m *ModelsManager) ResolveInternalConfigWithAffinityCached(cached *models.ModelConfig, conversationKey string, credLookup func(credentialID string) (*models.CredentialConfig, bool)) (ResolvedCredential, bool) {
	if cached == nil || !cached.Internal {
		return ResolvedCredential{}, false
	}
	modelConfig := cached

	lookupCredential := func(id string) *models.CredentialConfig {
		if credLookup == nil {
			return nil
		}
		cred, ok := credLookup(id)
		if !ok {
			return nil
		}
		return cred
	}

	// resolveSingle is the shared tail of the two single-credential
	// paths below (the legacy PrimaryCredentialID shape and the E-3
	// fast path): resolve via exactly one credential ID or fail.
	// NewlyBound is always false — no engine call, no binding write
	// on either path.
	resolveSingle := func(credID string) (ResolvedCredential, bool) {
		cred := lookupCredential(credID)
		if cred == nil {
			return ResolvedCredential{}, false
		}
		provider, apiKey, baseURL, actualModel := resolveWithCredential(modelConfig, cred)
		return ResolvedCredential{
			Provider:      provider,
			APIKey:        apiKey,
			BaseURL:       baseURL,
			InternalModel: actualModel,
			CredentialID:  credID,
			NewlyBound:    false,
		}, true
	}

	// Nil engine (hand-constructed instances) and empty credential
	// lists both degrade to the legacy single-credential shape —
	// identical to ResolveInternalConfigWithAffinity.
	if m.engine == nil || len(modelConfig.Credentials) == 0 {
		primaryID := modelConfig.PrimaryCredentialID()
		if primaryID == "" {
			return ResolvedCredential{}, false
		}
		return resolveSingle(primaryID)
	}

	// E-3 single-credential fast path: NO engine call, NO map writes.
	if len(modelConfig.Credentials) == 1 {
		return resolveSingle(modelConfig.Credentials[0].CredentialID)
	}

	// 2+ credentials: engine path (affinity for non-empty keys, fresh
	// weighted pick without binding for empty keys). The engine is
	// already zero-DB-per-request (snapshot refreshed via
	// RebindFromStore / OnModelChanged), so this stays DB-free.
	credID, newlyBound, err := m.engine.GetOrSelect(modelConfig.ID, conversationKey)
	if err != nil || credID == "" {
		return ResolvedCredential{}, false
	}
	cred := lookupCredential(credID)
	if cred == nil {
		// Dangling reference (credential deleted while the model's
		// credentials_json still references it) — same defensive heal
		// as ResolveInternalConfigWithAffinity's dangling-reference
		// heal:
		// clear the engine state for the dead credential and rebind
		// this conversation to a live one; THIS call still fails.
		log.Printf("[WARN] [credentiallb] credential %s missing from cache for model %s — clearing engine state, resolution fails this call", credID, modelConfig.ID)
		m.engine.OnCredentialDeleted(credID)
		m.engine.ExcludeAndReselect(modelConfig.ID, conversationKey, credID, 0)
		return ResolvedCredential{}, false
	}

	provider, apiKey, baseURL, actualModel := resolveWithCredential(modelConfig, cred)
	return ResolvedCredential{
		Provider:      provider,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: actualModel,
		CredentialID:  credID,
		NewlyBound:    newlyBound,
	}, true
}

// isDbBoolTrue converts a database value (bool or int64) to boolean
func isDbBoolTrue(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int64:
		return val != 0
	case int:
		return val != 0
	default:
		return false
	}
}
