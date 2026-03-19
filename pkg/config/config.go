package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

const (
	AppName        = "llm-supervisor-proxy"
	ConfigFileName = "config.json"
	ConfigVersion  = "1.0"
)

// Duration is a custom type that serializes to human-readable format (e.g., "10s")
// instead of nanoseconds. Required because time.Duration marshals to int64.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

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

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// Config holds all application configuration
type Config struct {
	Version              string              `json:"version"`
	UpstreamURL          string              `json:"upstream_url"`
	UpstreamCredentialID string              `json:"upstream_credential_id,omitempty"`
	Port                 int                 `json:"port"`
	IdleTimeout          Duration            `json:"idle_timeout"`
	StreamDeadline       Duration            `json:"stream_deadline"`        // Time limit before picking best buffer and continuing streaming (default: 110s)
	MaxGenerationTime    Duration            `json:"max_generation_time"`    // Absolute hard timeout for entire request lifecycle (default: 300s)
	MaxStreamBufferSize  int                 `json:"max_stream_buffer_size"` // Max bytes to buffer for streaming retry (0 = unlimited)
	BufferStorageDir     string              `json:"buffer_storage_dir"`     // Directory to store buffer content files
	BufferMaxStorageMB   int                 `json:"buffer_max_storage_mb"`  // Max total storage for buffers in MB (0 = unlimited)
	SSEHeartbeatEnabled  bool                `json:"sse_heartbeat_enabled"`  // Enable SSE heartbeat for streaming responses
	LoopDetection        LoopDetectionConfig `json:"loop_detection"`
	ToolRepair           toolrepair.Config   `json:"tool_repair"`
	UltimateModel        UltimateModelConfig `json:"ultimate_model"`
	UpdatedAt            string              `json:"updated_at"` // ISO8601 string for readability

	// Race Retry (Redesign)
	RaceRetryEnabled   bool `json:"race_retry_enabled"`
	RaceParallelOnIdle bool `json:"race_parallel_on_idle"`
	RaceMaxParallel    int  `json:"race_max_parallel"`
	RaceMaxBufferBytes int  `json:"race_max_buffer_bytes"` // Max bytes per request buffer (5MB default)

	// Tool Call Buffering (for weak streaming clients)
	ToolCallBufferDisabled bool  `json:"tool_call_buffer_disabled"` // When true, tool calls are streamed as-is (for clients that can handle partial JSON)
	ToolCallBufferMaxSize  int64 `json:"tool_call_buffer_max_size"` // Max bytes to buffer per request (default: 1MB)

	// Raw Upstream Response Logging
	LogRawUpstreamResponse bool `json:"log_raw_upstream_response"` // Log successful upstream responses (default: false)
	LogRawUpstreamOnError  bool `json:"log_raw_upstream_on_error"` // Log failed/error upstream responses (default: false)
	LogRawUpstreamMaxKB    int  `json:"log_raw_upstream_max_kb"`   // Max KB per response (default: 1024)
}

// ManagerInterface defines the interface for config management
// Both JSON and database-backed implementations must satisfy this interface
type ManagerInterface interface {
	Get() Config
	GetUpstreamURL() string
	GetPort() int
	GetIdleTimeout() time.Duration
	GetStreamDeadline() time.Duration
	GetMaxGenerationTime() time.Duration
	GetMaxStreamBufferSize() int
	GetBufferStorageDir() string
	GetBufferMaxStorageMB() int
	GetSSEHeartbeatEnabled() bool
	GetLoopDetection() LoopDetectionConfig
	GetUltimateModel() UltimateModelConfig
	GetRaceRetryEnabled() bool
	GetRaceParallelOnIdle() bool
	GetRaceMaxParallel() int
	GetRaceMaxBufferBytes() int
	GetToolCallBufferDisabled() bool
	GetToolCallBufferMaxSize() int64
	GetLogRawUpstreamResponse() bool
	GetLogRawUpstreamOnError() bool
	GetLogRawUpstreamMaxKB() int
	Save(Config) (*SaveResult, error)
	IsReadOnly() bool
}

// LoopDetectionConfig holds configuration for LLM loop detection.
type LoopDetectionConfig struct {
	Enabled              bool    `json:"enabled"`
	ShadowMode           bool    `json:"shadow_mode"`             // true = log only, false = can interrupt
	MessageWindow        int     `json:"message_window"`          // Sliding window size (default: 10)
	ActionWindow         int     `json:"action_window"`           // Action window size (default: 15)
	ExactMatchCount      int     `json:"exact_match_count"`       // Identical messages to trigger (default: 3)
	SimilarityThreshold  float64 `json:"similarity_threshold"`    // SimHash similarity threshold (default: 0.85)
	MinTokensForSimHash  int     `json:"min_tokens_for_simhash"`  // Min tokens before SimHash applies (default: 15)
	ActionRepeatCount    int     `json:"action_repeat_count"`     // Consecutive identical actions to trigger (default: 3)
	OscillationCount     int     `json:"oscillation_count"`       // A→B→A→B cycles to trigger (default: 4)
	MinTokensForAnalysis int     `json:"min_tokens_for_analysis"` // Min tokens before stream analysis (default: 20)

	// Phase 3: Advanced detection
	ThinkingMinTokens         int      `json:"thinking_min_tokens"`         // Min thinking tokens before analysis (default: 100)
	TrigramThreshold          float64  `json:"trigram_threshold"`           // Trigram repetition ratio threshold (default: 0.3)
	MaxCycleLength            int      `json:"max_cycle_length"`            // Max action cycle length to check (default: 5)
	ReasoningModelPatterns    []string `json:"reasoning_model_patterns"`    // Regex patterns for reasoning models
	ReasoningTrigramThreshold float64  `json:"reasoning_trigram_threshold"` // More forgiving threshold for reasoning models (default: 0.15)
}

// UltimateModelConfig holds configuration for the ultimate model feature.
// When a duplicate request is detected, the proxy bypasses all normal logic
// (fallback, retry, buffering) and acts as a raw proxy to this model.
type UltimateModelConfig struct {
	ModelID    string `json:"model_id"`    // Model ID to use for duplicate requests (e.g., "claude-3-opus")
	MaxHash    int    `json:"max_hash"`    // Max hashes in circular buffer (default: 100)
	MaxRetries int    `json:"max_retries"` // Max ultimate model retries per hash (default: 2, 0 = unlimited)
}

// Defaults - used when env not set and file doesn't exist
var Defaults = Config{
	Version:              ConfigVersion,
	UpstreamURL:          "http://localhost:4001",
	UpstreamCredentialID: "",
	Port:                 4321,
	IdleTimeout:          Duration(60 * time.Second),
	StreamDeadline:       Duration(110 * time.Second), // Time limit before picking best buffer and continuing streaming
	MaxGenerationTime:    Duration(300 * time.Second), // Absolute hard timeout for entire request lifecycle
	MaxStreamBufferSize:  10 * 1024 * 1024,            // 10MB default
	BufferStorageDir:     "",                          // Empty means use default data directory
	BufferMaxStorageMB:   100,                         // 100MB default
	SSEHeartbeatEnabled:  false,                       // Disable heartbeat by default
	LoopDetection: LoopDetectionConfig{
		Enabled:                   true,
		ShadowMode:                true,
		MessageWindow:             10,
		ActionWindow:              15,
		ExactMatchCount:           3,
		SimilarityThreshold:       0.85,
		MinTokensForSimHash:       15,
		ActionRepeatCount:         3,
		OscillationCount:          4,
		MinTokensForAnalysis:      20,
		ThinkingMinTokens:         100,
		TrigramThreshold:          0.3,
		MaxCycleLength:            5,
		ReasoningModelPatterns:    []string{"o1", "o3", "deepseek-r1"},
		ReasoningTrigramThreshold: 0.15,
	},
	ToolRepair: toolrepair.Config{
		Enabled:                 true,
		Strategies:              []string{"extract_json", "library_repair", "remove_reasoning"},
		MaxArgumentsSize:        20 * 1024, // 20KB
		MaxToolCallsPerResponse: 10,
		LogOriginal:             false,
		LogRepaired:             true,
		FixerModel:              "",
		FixerTimeout:            25, // 25 seconds
	},
	UltimateModel: UltimateModelConfig{
		ModelID:    "",
		MaxHash:    100,
		MaxRetries: 2, // Default: allow 2 retries
	},
	RaceRetryEnabled:       false,
	RaceParallelOnIdle:     true,
	RaceMaxParallel:        3,
	RaceMaxBufferBytes:     5242880, // 5MB limit
	ToolCallBufferDisabled: false,           // Buffering ENABLED by default
	ToolCallBufferMaxSize:  1024 * 1024,     // 1MB default
	// Raw Upstream Response Logging
	LogRawUpstreamResponse: false, // Disabled by default
	LogRawUpstreamOnError:  false, // Disabled by default
	LogRawUpstreamMaxKB:    1024,  // 1MB default
}

// Validate ensures config values are valid before saving
func (c *Config) Validate() error {
	if c.UpstreamURL == "" {
		return errors.New("upstream_url is required")
	}
	parsedURL, err := url.Parse(c.UpstreamURL)
	if err != nil {
		return fmt.Errorf("upstream_url is not a valid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return errors.New("upstream_url must use http or https scheme")
	}
	if parsedURL.Host == "" {
		return errors.New("upstream_url must have a host")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if c.IdleTimeout < Duration(time.Second) {
		return errors.New("idle_timeout must be at least 1s")
	}
	if c.StreamDeadline < Duration(time.Second) {
		return errors.New("stream_deadline must be at least 1s")
	}
	if c.MaxGenerationTime < Duration(time.Second) {
		return errors.New("max_generation_time must be at least 1s")
	}
	if c.MaxStreamBufferSize < 0 {
		return errors.New("max_stream_buffer_size cannot be negative")
	}
	if c.RaceMaxParallel < 1 {
		return errors.New("race_max_parallel must be at least 1")
	}
	if c.RaceMaxBufferBytes < 0 {
		return errors.New("race_max_buffer_bytes cannot be negative")
	}
	if c.UltimateModel.MaxRetries < 0 {
		return errors.New("ultimate_model.max_retries cannot be negative")
	}
	if c.UltimateModel.MaxRetries > 100 {
		return errors.New("ultimate_model.max_retries cannot exceed 100")
	}
	if (c.LogRawUpstreamResponse || c.LogRawUpstreamOnError) && c.BufferStorageDir == "" {
		// Use default data directory
		userConfigDir, err := os.UserConfigDir()
		if err != nil {
			return errors.New("buffer_storage_dir is required when raw response logging is enabled")
		}
		defaultDir := filepath.Join(userConfigDir, "llm-supervisor-proxy", "buffers")
		c.BufferStorageDir = defaultDir
	}
	return nil
}

// SaveResult contains metadata about a save operation
type SaveResult struct {
	RestartRequired bool     `json:"restart_required"`
	ChangedFields   []string `json:"changed_fields,omitempty"`
}

// Manager handles configuration lifecycle
type Manager struct {
	mu       sync.RWMutex
	config   Config
	filePath string
	readOnly bool        // true if file write fails (permission denied, etc.)
	eventBus *events.Bus // optional: for publishing config updates
}

// NewManager creates a new configuration manager
func NewManager() (*Manager, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}
	filePath := filepath.Join(configDir, AppName, ConfigFileName)

	m := &Manager{filePath: filePath}
	if err := m.Load(); err != nil {
		return nil, err
	}
	return m, nil
}

// NewManagerWithEventBus creates a new configuration manager with event bus integration
func NewManagerWithEventBus(eventBus *events.Bus) (*Manager, error) {
	m, err := NewManager()
	if err != nil {
		return nil, err
	}
	m.eventBus = eventBus
	return m, nil
}

// Load initializes configuration with proper precedence: env > file > defaults
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Step 1: Start with defaults
	cfg := Defaults

	// Step 2: Load from file if exists (file > defaults)
	if data, err := os.ReadFile(m.filePath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			// Corrupted file - backup and use defaults
			m.backupCorruptedFile()
			cfg = Defaults
		}
	}

	// Step 3: Apply env overrides (env > file > defaults)
	cfg = applyEnvOverrides(cfg)

	// Step 4: If no file exists, create one for user convenience
	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		cfg.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := m.saveToFile(cfg); err != nil {
			// Can't write file - continue in read-only mode
			m.readOnly = true
		}
	}

	m.config = cfg
	return nil
}

// ApplyEnvOverrides applies env vars on top of config (env wins always)
// This is exported so database-backed ConfigManager can also use it
func ApplyEnvOverrides(cfg Config) Config {
	return applyEnvOverrides(cfg)
}

// applyEnvOverrides applies env vars on top of config (env wins always)
func applyEnvOverrides(cfg Config) Config {
	v := os.Getenv("APPLY_ENV_OVERRIDES")
	if v == "" {
		return cfg
	}
	// Only apply if env var exists AND is non-empty
	if v := os.Getenv("UPSTREAM_URL"); v != "" {
		cfg.UpstreamURL = v
	}
	if v := os.Getenv("UPSTREAM_CREDENTIAL_ID"); v != "" {
		cfg.UpstreamCredentialID = v
	}
	if v := os.Getenv("PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 && port <= 65535 {
			cfg.Port = port
		}
	}
	if v := os.Getenv("IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.IdleTimeout = Duration(d)
		}
	}
	if v := os.Getenv("STREAM_DEADLINE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.StreamDeadline = Duration(d)
		}
	}
	if v := os.Getenv("MAX_GENERATION_TIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.MaxGenerationTime = Duration(d)
		}
	}
	if v := os.Getenv("LOOP_DETECTION_ENABLED"); v != "" {
		cfg.LoopDetection.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("LOOP_DETECTION_SHADOW_MODE"); v != "" {
		cfg.LoopDetection.ShadowMode = v == "true" || v == "1"
	}
	if v := os.Getenv("SSE_HEARTBEAT_ENABLED"); v != "" {
		cfg.SSEHeartbeatEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("ULTIMATE_MODEL_ID"); v != "" {
		cfg.UltimateModel.ModelID = v
	}
	if v := os.Getenv("ULTIMATE_MODEL_MAX_HASH"); v != "" {
		if r, err := strconv.Atoi(v); err == nil && r > 0 {
			cfg.UltimateModel.MaxHash = r
		}
	}
	if v := os.Getenv("ULTIMATE_MODEL_MAX_RETRIES"); v != "" {
		if r, err := strconv.Atoi(v); err == nil && r >= 0 {
			cfg.UltimateModel.MaxRetries = r
		}
	}
	if v := os.Getenv("RACE_RETRY_ENABLED"); v != "" {
		cfg.RaceRetryEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("RACE_PARALLEL_ON_IDLE"); v != "" {
		cfg.RaceParallelOnIdle = v == "true" || v == "1"
	}
	if v := os.Getenv("RACE_MAX_PARALLEL"); v != "" {
		if r, err := strconv.Atoi(v); err == nil && r > 0 {
			cfg.RaceMaxParallel = r
		}
	}
	if v := os.Getenv("RACE_MAX_BUFFER_BYTES"); v != "" {
		if r, err := strconv.ParseInt(v, 10, 64); err == nil && r >= 0 {
			cfg.RaceMaxBufferBytes = int(r)
		}
	}
	// Tool call buffer configuration
	if v := os.Getenv("TOOL_CALL_BUFFER_DISABLED"); v != "" {
		cfg.ToolCallBufferDisabled = v == "true" || v == "1"
	}
	if v := os.Getenv("TOOL_CALL_BUFFER_MAX_SIZE"); v != "" {
		if r, err := strconv.ParseInt(v, 10, 64); err == nil && r > 0 {
			cfg.ToolCallBufferMaxSize = r
		}
	}
	return cfg
}

// backupCorruptedFile renames corrupted config for recovery
func (m *Manager) backupCorruptedFile() {
	backupPath := m.filePath + ".corrupted." + time.Now().Format("20060102-150405")
	if err := os.Rename(m.filePath, backupPath); err != nil {
		log.Printf("Warning: failed to backup corrupted config file: %v", err)
	}
}

// Save persists configuration to file and updates in-memory state
func (m *Manager) Save(cfg Config) (*SaveResult, error) {
	// Validate before any changes
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readOnly {
		return nil, errors.New("config file is read-only (permission denied)")
	}

	// Detect changes that require restart
	result := &SaveResult{}
	if m.config.Port != cfg.Port {
		result.RestartRequired = true
		result.ChangedFields = append(result.ChangedFields, "port")
	}

	// Set metadata
	cfg.Version = ConfigVersion
	cfg.UpdatedAt = time.Now().Format(time.RFC3339)

	// Backup existing file before overwrite
	if _, err := os.Stat(m.filePath); err == nil {
		backupPath := m.filePath + ".bak"
		if err := os.Rename(m.filePath, backupPath); err != nil {
			return nil, fmt.Errorf("failed to backup config file: %w", err)
		}
	}

	if err := m.saveToFile(cfg); err != nil {
		return nil, err
	}

	// Re-apply env overrides to in-memory config (env always wins)
	m.config = applyEnvOverrides(cfg)

	// Publish config update event if event bus is wired
	if m.eventBus != nil {
		m.eventBus.Publish(events.Event{
			Type:      "config.updated",
			Timestamp: time.Now().Unix(),
			Data:      m.config,
		})
	}

	return result, nil
}

// saveToFile writes config to disk atomically
func (m *Manager) saveToFile(cfg Config) error {
	// Ensure directory exists
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Atomic write using temp file (avoids partial writes)
	tmpFile, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Write and sync to disk
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write config: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync config: %w", err)
	}
	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, m.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// Get returns current configuration (thread-safe)
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// GetUpstreamURL returns the upstream URL
func (m *Manager) GetUpstreamURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.UpstreamURL
}

// GetPort returns the port
func (m *Manager) GetPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Port
}

// GetIdleTimeout returns the idle timeout
func (m *Manager) GetIdleTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.IdleTimeout.Duration()
}

// GetStreamDeadline returns the stream deadline for race retry buffer caching
func (m *Manager) GetStreamDeadline() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.StreamDeadline.Duration()
}

// GetMaxGenerationTime returns the max generation time
func (m *Manager) GetMaxGenerationTime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.MaxGenerationTime.Duration()
}

// GetMaxStreamBufferSize returns the max stream buffer size in bytes
func (m *Manager) GetMaxStreamBufferSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.MaxStreamBufferSize
}

// GetLoopDetection returns the loop detection configuration
func (m *Manager) GetLoopDetection() LoopDetectionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.LoopDetection
}

// GetBufferStorageDir returns the buffer storage directory
func (m *Manager) GetBufferStorageDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.BufferStorageDir
}

// GetBufferMaxStorageMB returns the max buffer storage in MB
func (m *Manager) GetBufferMaxStorageMB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.BufferMaxStorageMB
}

// GetSSEHeartbeatEnabled returns whether SSE heartbeat is enabled for streaming responses
func (m *Manager) GetSSEHeartbeatEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.SSEHeartbeatEnabled
}

// GetUltimateModel returns the ultimate model configuration
func (m *Manager) GetUltimateModel() UltimateModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.UltimateModel
}

// GetRaceRetryEnabled returns whether race retry is enabled
func (m *Manager) GetRaceRetryEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.RaceRetryEnabled
}

// GetRaceParallelOnIdle returns whether to spawn parallel requests on idle timeout
func (m *Manager) GetRaceParallelOnIdle() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.RaceParallelOnIdle
}

// GetRaceMaxParallel returns the max parallel requests
func (m *Manager) GetRaceMaxParallel() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.RaceMaxParallel
}

// GetRaceMaxBufferBytes returns the max bytes per request buffer
func (m *Manager) GetRaceMaxBufferBytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.RaceMaxBufferBytes
}

// GetToolCallBufferDisabled returns whether tool call buffering is disabled
func (m *Manager) GetToolCallBufferDisabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.ToolCallBufferDisabled
}

// GetToolCallBufferMaxSize returns the max bytes to buffer for tool calls per request
func (m *Manager) GetToolCallBufferMaxSize() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.ToolCallBufferMaxSize
}

// GetLogRawUpstreamResponse returns whether to log successful upstream responses
func (m *Manager) GetLogRawUpstreamResponse() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.LogRawUpstreamResponse
}

// GetLogRawUpstreamOnError returns whether to log failed/error upstream responses
func (m *Manager) GetLogRawUpstreamOnError() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.LogRawUpstreamOnError
}

// GetLogRawUpstreamMaxKB returns the max KB per response to log
func (m *Manager) GetLogRawUpstreamMaxKB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.LogRawUpstreamMaxKB
}

// IsReadOnly returns true if the config file cannot be written
func (m *Manager) IsReadOnly() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readOnly
}

// GetFilePath returns the path to the config file
func (m *Manager) GetFilePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filePath
}
