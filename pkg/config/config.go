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
	Version           string   `json:"version"`
	UpstreamURL       string   `json:"upstream_url"`
	Port              int      `json:"port"`
	IdleTimeout       Duration `json:"idle_timeout"`
	MaxGenerationTime Duration `json:"max_generation_time"`
	MaxRetries        int      `json:"max_retries"`
	UpdatedAt         string   `json:"updated_at"` // ISO8601 string for readability
}

// Defaults - used when env not set and file doesn't exist
var Defaults = Config{
	Version:           ConfigVersion,
	UpstreamURL:       "http://localhost:4001",
	Port:              8089,
	IdleTimeout:       Duration(10 * time.Second),
	MaxGenerationTime: Duration(180 * time.Second),
	MaxRetries:        1,
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
	if c.MaxGenerationTime < Duration(time.Second) {
		return errors.New("max_generation_time must be at least 1s")
	}
	if c.MaxRetries < 0 {
		return errors.New("max_retries cannot be negative")
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
	cfg = m.applyEnvOverrides(cfg)

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

// applyEnvOverrides applies env vars on top of config (env wins always)
func (m *Manager) applyEnvOverrides(cfg Config) Config {
	// Only apply if env var exists AND is non-empty
	if v := os.Getenv("UPSTREAM_URL"); v != "" {
		cfg.UpstreamURL = v
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
	if v := os.Getenv("MAX_GENERATION_TIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.MaxGenerationTime = Duration(d)
		}
	}
	if v := os.Getenv("MAX_RETRIES"); v != "" {
		if r, err := strconv.Atoi(v); err == nil && r >= 0 {
			cfg.MaxRetries = r
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
	m.config = m.applyEnvOverrides(cfg)

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

// GetMaxGenerationTime returns the max generation time
func (m *Manager) GetMaxGenerationTime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.MaxGenerationTime.Duration()
}

// GetMaxRetries returns the max retries
func (m *Manager) GetMaxRetries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.MaxRetries
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
