package database

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)

func TestNewMockConfigManager(t *testing.T) {
	mock := NewMockConfigManager()

	// Verify defaults are applied
	cfg := mock.Get()
	if cfg.UpstreamURL != config.Defaults.UpstreamURL {
		t.Errorf("Expected default UpstreamURL %s, got %s", config.Defaults.UpstreamURL, cfg.UpstreamURL)
	}
	if cfg.Port != config.Defaults.Port {
		t.Errorf("Expected default Port %d, got %d", config.Defaults.Port, cfg.Port)
	}
}

func TestNewMockConfigManagerWithConfig(t *testing.T) {
	customCfg := config.Config{
		Version:       "1.0",
		UpstreamURL:   "http://custom:9999",
		Port:          8888,
		IdleTimeout:   config.Duration(30 * time.Second),
		StreamDeadline: config.Duration(60 * time.Second),
		MaxGenerationTime: config.Duration(120 * time.Second),
	}

	mock := NewMockConfigManagerWithConfig(customCfg)

	cfg := mock.Get()
	if cfg.UpstreamURL != "http://custom:9999" {
		t.Errorf("Expected UpstreamURL 'http://custom:9999', got %s", cfg.UpstreamURL)
	}
	if cfg.Port != 8888 {
		t.Errorf("Expected Port 8888, got %d", cfg.Port)
	}
}

func TestMockConfigManager_Get(t *testing.T) {
	mock := NewMockConfigManager()

	cfg := mock.Get()
	if cfg.UpstreamURL == "" {
		t.Error("Expected non-empty UpstreamURL")
	}
}

func TestMockConfigManager_GetUpstreamURL(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		UpstreamURL: "http://test:4001",
	})

	if mock.GetUpstreamURL() != "http://test:4001" {
		t.Errorf("Expected 'http://test:4001', got %s", mock.GetUpstreamURL())
	}
}

func TestMockConfigManager_GetPort(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		Port: 5555,
	})

	if mock.GetPort() != 5555 {
		t.Errorf("Expected 5555, got %d", mock.GetPort())
	}
}

func TestMockConfigManager_GetIdleTimeout(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		IdleTimeout: config.Duration(45 * time.Second),
	})

	timeout := mock.GetIdleTimeout()
	if timeout != 45*time.Second {
		t.Errorf("Expected 45s, got %v", timeout)
	}
}

func TestMockConfigManager_GetStreamDeadline(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		StreamDeadline: config.Duration(90 * time.Second),
	})

	deadline := mock.GetStreamDeadline()
	if deadline != 90*time.Second {
		t.Errorf("Expected 90s, got %v", deadline)
	}
}

func TestMockConfigManager_GetMaxGenerationTime(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		MaxGenerationTime: config.Duration(200 * time.Second),
	})

	maxTime := mock.GetMaxGenerationTime()
	if maxTime != 200*time.Second {
		t.Errorf("Expected 200s, got %v", maxTime)
	}
}

func TestMockConfigManager_GetMaxStreamBufferSize(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		MaxStreamBufferSize: 20 * 1024 * 1024,
	})

	size := mock.GetMaxStreamBufferSize()
	if size != 20*1024*1024 {
		t.Errorf("Expected 20MB, got %d", size)
	}
}

func TestMockConfigManager_GetBufferStorageDir(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		BufferStorageDir: "/tmp/test",
	})

	dir := mock.GetBufferStorageDir()
	if dir != "/tmp/test" {
		t.Errorf("Expected '/tmp/test', got %s", dir)
	}
}

func TestMockConfigManager_GetBufferMaxStorageMB(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		BufferMaxStorageMB: 500,
	})

	mb := mock.GetBufferMaxStorageMB()
	if mb != 500 {
		t.Errorf("Expected 500, got %d", mb)
	}
}

func TestMockConfigManager_GetLoopDetection(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		LoopDetection: config.LoopDetectionConfig{
			Enabled:         true,
			ShadowMode:      false,
			MessageWindow:   20,
			ActionWindow:    25,
			ExactMatchCount: 4,
		},
	})

	ld := mock.GetLoopDetection()
	if ld.MessageWindow != 20 {
		t.Errorf("Expected MessageWindow 20, got %d", ld.MessageWindow)
	}
	if ld.ActionWindow != 25 {
		t.Errorf("Expected ActionWindow 25, got %d", ld.ActionWindow)
	}
}

func TestMockConfigManager_GetUltimateModel(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		UltimateModel: config.UltimateModelConfig{
			ModelID:    "claude-3-opus",
			MaxHash:    200,
			MaxRetries: 5,
		},
	})

	um := mock.GetUltimateModel()
	if um.ModelID != "claude-3-opus" {
		t.Errorf("Expected ModelID 'claude-3-opus', got %s", um.ModelID)
	}
	if um.MaxHash != 200 {
		t.Errorf("Expected MaxHash 200, got %d", um.MaxHash)
	}
}

func TestMockConfigManager_GetRaceRetryEnabled(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		RaceRetryEnabled: true,
	})

	if !mock.GetRaceRetryEnabled() {
		t.Error("Expected RaceRetryEnabled to be true")
	}
}

func TestMockConfigManager_GetRaceParallelOnIdle(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		RaceParallelOnIdle: false,
	})

	if mock.GetRaceParallelOnIdle() {
		t.Error("Expected RaceParallelOnIdle to be false")
	}
}

func TestMockConfigManager_GetRaceMaxParallel(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		RaceMaxParallel: 7,
	})

	if mock.GetRaceMaxParallel() != 7 {
		t.Errorf("Expected 7, got %d", mock.GetRaceMaxParallel())
	}
}

func TestMockConfigManager_GetRaceMaxBufferBytes(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		RaceMaxBufferBytes: 10 * 1024 * 1024,
	})

	if mock.GetRaceMaxBufferBytes() != 10*1024*1024 {
		t.Errorf("Expected 10MB, got %d", mock.GetRaceMaxBufferBytes())
	}
}

func TestMockConfigManager_GetToolCallBufferDisabled(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		ToolCallBufferDisabled: true,
	})

	if !mock.GetToolCallBufferDisabled() {
		t.Error("Expected ToolCallBufferDisabled to be true")
	}
}

func TestMockConfigManager_GetToolCallBufferMaxSize(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		ToolCallBufferMaxSize: 2 * 1024 * 1024,
	})

	if mock.GetToolCallBufferMaxSize() != 2*1024*1024 {
		t.Errorf("Expected 2MB, got %d", mock.GetToolCallBufferMaxSize())
	}
}

func TestMockConfigManager_GetLogRawUpstreamResponse(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		LogRawUpstreamResponse: true,
	})

	if !mock.GetLogRawUpstreamResponse() {
		t.Error("Expected LogRawUpstreamResponse to be true")
	}
}

func TestMockConfigManager_GetLogRawUpstreamOnError(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		LogRawUpstreamOnError: true,
	})

	if !mock.GetLogRawUpstreamOnError() {
		t.Error("Expected LogRawUpstreamOnError to be true")
	}
}

func TestMockConfigManager_GetLogRawUpstreamMaxKB(t *testing.T) {
	mock := NewMockConfigManager()
	mock.SetConfig(config.Config{
		LogRawUpstreamMaxKB: 2048,
	})

	if mock.GetLogRawUpstreamMaxKB() != 2048 {
		t.Errorf("Expected 2048, got %d", mock.GetLogRawUpstreamMaxKB())
	}
}

func TestMockConfigManager_Save(t *testing.T) {
	mock := NewMockConfigManager()

	newCfg := config.Config{
		UpstreamURL:       "http://saved:4001",
		Port:              6666,
		IdleTimeout:       config.Duration(50 * time.Second),
		StreamDeadline:    config.Duration(100 * time.Second),
		MaxGenerationTime: config.Duration(150 * time.Second),
	}

	result, err := mock.Save(newCfg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Verify SaveCalled flag
	if !mock.WasSaveCalled() {
		t.Error("Expected SaveCalled to be true")
	}

	// Verify SavedConfig
	savedCfg := mock.GetSavedConfig()
	if savedCfg == nil {
		t.Fatal("Expected SavedConfig to be non-nil")
	}
	if savedCfg.UpstreamURL != "http://saved:4001" {
		t.Errorf("Expected UpstreamURL 'http://saved:4001', got %s", savedCfg.UpstreamURL)
	}

	// Verify current config was updated
	if mock.Get().UpstreamURL != "http://saved:4001" {
		t.Errorf("Expected current config to be updated")
	}
}

func TestMockConfigManager_SaveError(t *testing.T) {
	mock := NewMockConfigManager()

	expectedErr := errors.New("save failed")
	mock.SetSaveError(expectedErr)

	_, err := mock.Save(config.Config{})
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}

	// SaveCalled should still be false since we returned early
	if mock.WasSaveCalled() {
		t.Error("Expected SaveCalled to be false when error occurs")
	}
}

func TestMockConfigManager_Load(t *testing.T) {
	mock := NewMockConfigManager()

	err := mock.Load()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestMockConfigManager_LoadError(t *testing.T) {
	mock := NewMockConfigManager()

	expectedErr := errors.New("load failed")
	mock.SetLoadError(expectedErr)

	err := mock.Load()
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestMockConfigManager_IsReadOnly(t *testing.T) {
	mock := NewMockConfigManager()

	// Default is false
	if mock.IsReadOnly() {
		t.Error("Expected IsReadOnly to be false by default")
	}

	mock.SetReadOnly(true)

	if !mock.IsReadOnly() {
		t.Error("Expected IsReadOnly to be true")
	}
}

func TestMockConfigManager_Reset(t *testing.T) {
	mock := NewMockConfigManager()

	// Modify the mock
	mock.SetConfig(config.Config{
		UpstreamURL: "http://modified:4001",
		Port:        9999,
	})
	mock.SetReadOnly(true)
	mock.SetSaveError(errors.New("test error"))
	_, _ = mock.Save(config.Config{UpstreamURL: "http://saved:4001"})

	// Reset
	mock.Reset()

	// Verify all state is reset
	cfg := mock.Get()
	if cfg.UpstreamURL != config.Defaults.UpstreamURL {
		t.Errorf("Expected default UpstreamURL after reset, got %s", cfg.UpstreamURL)
	}
	if mock.IsReadOnly() {
		t.Error("Expected IsReadOnly to be false after reset")
	}
	if mock.WasSaveCalled() {
		t.Error("Expected SaveCalled to be false after reset")
	}
	if mock.GetSavedConfig() != nil {
		t.Error("Expected SavedConfig to be nil after reset")
	}
}

func TestMockConfigManager_ConcurrentAccess(t *testing.T) {
	mock := NewMockConfigManager()

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mock.Get()
			_ = mock.GetUpstreamURL()
			_ = mock.GetPort()
			_ = mock.GetIdleTimeout()
		}()
	}

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := config.Config{
				Port:        1000 + i,
				UpstreamURL: "http://test:4001",
			}
			mock.SetConfig(cfg)
		}(i)
	}

	// Concurrent Save calls
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := config.Config{
				Port:        2000 + i,
				UpstreamURL: "http://saved:4001",
			}
			_, _ = mock.Save(cfg)
		}(i)
	}

	wg.Wait()

	// If we get here without race conditions or panics, the test passes
	t.Log("Concurrent access test completed successfully")
}

func TestMockConfigManager_ImplementsInterface(t *testing.T) {
	// This test verifies at runtime that MockConfigManager implements config.ManagerInterface
	mock := NewMockConfigManager()

	// The assignment will fail at compile time if the interface is not satisfied
	var _ config.ManagerInterface = mock

	t.Log("MockConfigManager correctly implements config.ManagerInterface")
}
