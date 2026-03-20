package database

import (
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)

// MockConfigManager implements config.ManagerInterface for testing purposes.
// It provides thread-safe access to configuration and allows controlling
// what config is returned, verifying Save was called, and simulating errors.
type MockConfigManager struct {
	mu sync.RWMutex

	// Config is the current configuration returned by Get()
	Config config.Config

	// SaveErr if non-nil, Save() returns this error
	SaveErr error

	// LoadErr if non-nil, Load() returns this error
	LoadErr error

	// SaveCalled is set to true when Save() is invoked
	SaveCalled bool

	// SavedConfig is the config passed to the last Save() call
	SavedConfig *config.Config

	// readOnly controls IsReadOnly() return value
	readOnly bool
}

// NewMockConfigManager creates a new MockConfigManager with default configuration.
func NewMockConfigManager() *MockConfigManager {
	return &MockConfigManager{
		Config: config.Defaults,
	}
}

// NewMockConfigManagerWithConfig creates a new MockConfigManager with a specific configuration.
func NewMockConfigManagerWithConfig(cfg config.Config) *MockConfigManager {
	return &MockConfigManager{
		Config: cfg,
	}
}

// Load initializes or reloads configuration. Returns LoadErr if set.
func (m *MockConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.LoadErr != nil {
		return m.LoadErr
	}
	return nil
}

// Get returns the current configuration.
func (m *MockConfigManager) Get() config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config
}

// GetUpstreamURL returns the upstream URL from the current config.
func (m *MockConfigManager) GetUpstreamURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.UpstreamURL
}

// GetPort returns the port from the current config.
func (m *MockConfigManager) GetPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.Port
}

// GetIdleTimeout returns the idle timeout from the current config.
func (m *MockConfigManager) GetIdleTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.Config.IdleTimeout)
}

// GetStreamDeadline returns the stream deadline from the current config.
func (m *MockConfigManager) GetStreamDeadline() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.Config.StreamDeadline)
}

// GetMaxGenerationTime returns the max generation time from the current config.
func (m *MockConfigManager) GetMaxGenerationTime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.Config.MaxGenerationTime)
}

// GetMaxStreamBufferSize returns the max stream buffer size from the current config.
func (m *MockConfigManager) GetMaxStreamBufferSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.MaxStreamBufferSize
}

// GetBufferStorageDir returns the buffer storage directory from the current config.
func (m *MockConfigManager) GetBufferStorageDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.BufferStorageDir
}

// GetBufferMaxStorageMB returns the max buffer storage in MB from the current config.
func (m *MockConfigManager) GetBufferMaxStorageMB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.BufferMaxStorageMB
}

// GetSSEHeartbeatEnabled returns whether SSE heartbeat is enabled.
func (m *MockConfigManager) GetSSEHeartbeatEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.SSEHeartbeatEnabled
}

// GetLoopDetection returns the loop detection configuration.
func (m *MockConfigManager) GetLoopDetection() config.LoopDetectionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.LoopDetection
}

// GetUltimateModel returns the ultimate model configuration.
func (m *MockConfigManager) GetUltimateModel() config.UltimateModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.UltimateModel
}

// GetRaceRetryEnabled returns whether race retry is enabled.
func (m *MockConfigManager) GetRaceRetryEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.RaceRetryEnabled
}

// GetRaceParallelOnIdle returns whether to spawn parallel requests on idle timeout.
func (m *MockConfigManager) GetRaceParallelOnIdle() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.RaceParallelOnIdle
}

// GetRaceMaxParallel returns the max parallel requests.
func (m *MockConfigManager) GetRaceMaxParallel() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.RaceMaxParallel
}

// GetRaceMaxBufferBytes returns the max bytes per request buffer.
func (m *MockConfigManager) GetRaceMaxBufferBytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.RaceMaxBufferBytes
}

// GetToolCallBufferDisabled returns whether tool call buffering is disabled.
func (m *MockConfigManager) GetToolCallBufferDisabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.ToolCallBufferDisabled
}

// GetToolCallBufferMaxSize returns the max size for tool call buffer.
func (m *MockConfigManager) GetToolCallBufferMaxSize() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.ToolCallBufferMaxSize
}

// GetLogRawUpstreamResponse returns whether to log successful upstream responses.
func (m *MockConfigManager) GetLogRawUpstreamResponse() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.LogRawUpstreamResponse
}

// GetLogRawUpstreamOnError returns whether to log failed/error upstream responses.
func (m *MockConfigManager) GetLogRawUpstreamOnError() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.LogRawUpstreamOnError
}

// GetLogRawUpstreamMaxKB returns the max KB per response to log.
func (m *MockConfigManager) GetLogRawUpstreamMaxKB() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Config.LogRawUpstreamMaxKB
}

// Save stores the configuration and updates the mock's state.
// Returns SaveErr if set, otherwise updates Config and records the call.
func (m *MockConfigManager) Save(cfg config.Config) (*config.SaveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SaveErr != nil {
		return nil, m.SaveErr
	}

	m.SaveCalled = true
	m.SavedConfig = &cfg
	m.Config = cfg

	// Return a default SaveResult (no restart required)
	return &config.SaveResult{
		RestartRequired: false,
		ChangedFields:   nil,
	}, nil
}

// IsReadOnly returns whether the config is read-only.
func (m *MockConfigManager) IsReadOnly() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readOnly
}

// SetReadOnly sets the read-only state for testing.
func (m *MockConfigManager) SetReadOnly(readOnly bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readOnly = readOnly
}

// SetConfig sets the current configuration for testing.
func (m *MockConfigManager) SetConfig(cfg config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Config = cfg
}

// SetSaveError sets the error to return on Save() for testing.
func (m *MockConfigManager) SetSaveError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SaveErr = err
}

// SetLoadError sets the error to return on Load() for testing.
func (m *MockConfigManager) SetLoadError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LoadErr = err
}

// WasSaveCalled returns true if Save() was called.
func (m *MockConfigManager) WasSaveCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SaveCalled
}

// GetSavedConfig returns the config passed to the last Save() call.
// Returns nil if Save() was never called.
func (m *MockConfigManager) GetSavedConfig() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SavedConfig
}

// Reset resets all mock state to initial values.
func (m *MockConfigManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Config = config.Defaults
	m.SaveErr = nil
	m.LoadErr = nil
	m.SaveCalled = false
	m.SavedConfig = nil
	m.readOnly = false
}

// Verify MockConfigManager implements config.ManagerInterface at compile time.
var _ config.ManagerInterface = (*MockConfigManager)(nil)
