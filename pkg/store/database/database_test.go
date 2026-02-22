package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func TestSQLiteConnection(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create connection
	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	defer store.Close()

	// Run migrations
	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Verify we can query
	isEmpty, err := store.IsEmpty(context.Background())
	if err != nil {
		t.Fatalf("Failed to check if empty: %v", err)
	}
	if isEmpty {
		t.Log("Database is empty (expected after migrations)")
	}
}

func TestConfigManager(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create config manager
	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Test Get
	cfg := cfgMgr.Get()
	if cfg.UpstreamURL == "" {
		t.Error("Expected non-empty upstream URL")
	}

	// Test GetPort
	port := cfgMgr.GetPort()
	if port <= 0 || port > 65535 {
		t.Errorf("Invalid port: %d", port)
	}

	// Test Save
	newCfg := cfg
	newCfg.UpstreamURL = "http://test.example.com:8080"
	result, err := cfgMgr.Save(newCfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
	if result == nil {
		t.Error("Expected non-nil save result")
	}

	// Verify save
	updatedCfg := cfgMgr.Get()
	if updatedCfg.UpstreamURL != "http://test.example.com:8080" {
		t.Errorf("Expected upstream URL to be updated, got: %s", updatedCfg.UpstreamURL)
	}
}

func TestModelsManager(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create models manager
	modelsMgr, err := NewModelsManager(store)
	if err != nil {
		t.Fatalf("Failed to create models manager: %v", err)
	}

	// Test empty models list
	modelList := modelsMgr.GetModels()
	if len(modelList) != 0 {
		t.Errorf("Expected empty models list, got: %d", len(modelList))
	}

	// Test AddModel
	testModel := models.ModelConfig{
		ID:             "test-model",
		Name:           "Test Model",
		Enabled:        true,
		FallbackChain:  []string{"fallback-model"},
		TruncateParams: []string{"max_tokens"},
	}
	if err := modelsMgr.AddModel(testModel); err != nil {
		t.Fatalf("Failed to add model: %v", err)
	}

	// Verify model was added
	modelList = modelsMgr.GetModels()
	if len(modelList) != 1 {
		t.Errorf("Expected 1 model, got: %d", len(modelList))
	}
	if modelList[0].ID != "test-model" {
		t.Errorf("Expected model ID 'test-model', got: %s", modelList[0].ID)
	}

	// Test GetFallbackChain
	chain := modelsMgr.GetFallbackChain("test-model")
	if len(chain) != 2 { // model ID + fallback
		t.Errorf("Expected chain length 2, got: %d", len(chain))
	}
	if chain[0] != "test-model" {
		t.Errorf("Expected first element to be model ID, got: %s", chain[0])
	}

	// Test GetTruncateParams
	params := modelsMgr.GetTruncateParams("test-model")
	if len(params) != 1 || params[0] != "max_tokens" {
		t.Errorf("Expected truncate params ['max_tokens'], got: %v", params)
	}

	// Test UpdateModel
	updatedModel := testModel
	updatedModel.Name = "Updated Test Model"
	updatedModel.Enabled = false
	if err := modelsMgr.UpdateModel("test-model", updatedModel); err != nil {
		t.Fatalf("Failed to update model: %v", err)
	}

	// Verify update
	modelList = modelsMgr.GetModels()
	if modelList[0].Name != "Updated Test Model" {
		t.Errorf("Expected updated name, got: %s", modelList[0].Name)
	}

	// Test RemoveModel
	if err := modelsMgr.RemoveModel("test-model"); err != nil {
		t.Fatalf("Failed to remove model: %v", err)
	}

	// Verify removal
	modelList = modelsMgr.GetModels()
	if len(modelList) != 0 {
		t.Errorf("Expected empty models list after removal, got: %d", len(modelList))
	}
}

func TestJSONMigration(t *testing.T) {
	// Skip this test on macOS because os.UserConfigDir() doesn't respect XDG_CONFIG_HOME
	// On macOS it uses $HOME/Library/Application Support instead
	if _, err := os.Stat("/usr/bin/sw_vers"); err == nil {
		t.Skip("Skipping on macOS - os.UserConfigDir() doesn't respect XDG_CONFIG_HOME")
	}

	// Create temp directories
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a mock config directory
	configDir := filepath.Join(tmpDir, "llm-supervisor-proxy")
	os.MkdirAll(configDir, 0755)

	// Create test config.json
	configJSON := `{
		"version": "1.0",
		"upstream_url": "http://localhost:9999",
		"port": 5555,
		"idle_timeout": "30s",
		"max_generation_time": "60s",
		"max_upstream_error_retries": 2,
		"max_idle_retries": 3,
		"max_generation_retries": 2,
		"loop_detection": {
			"enabled": true,
			"shadow_mode": false,
			"message_window": 5
		},
		"updated_at": "2024-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Create test models.json
	modelsJSON := `{
		"models": [
			{
				"id": "model-1",
				"name": "Model One",
				"enabled": true,
				"fallback_chain": ["model-2"],
				"truncate_params": ["max_tokens"]
			},
			{
				"id": "model-2",
				"name": "Model Two",
				"enabled": true,
				"fallback_chain": [],
				"truncate_params": []
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(configDir, "models.json"), []byte(modelsJSON), 0644); err != nil {
		t.Fatalf("Failed to write test models: %v", err)
	}

	// Override user config dir for test (works on Linux)
	originalConfigDir := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Setenv("XDG_CONFIG_HOME", originalConfigDir)

	// Create database and run migration
	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Run JSON migration
	if err := store.MigrateFromJSON(context.Background()); err != nil {
		t.Fatalf("Failed to run JSON migration: %v", err)
	}

	// Verify config was migrated
	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := cfgMgr.Get()
	if cfg.UpstreamURL != "http://localhost:9999" {
		t.Errorf("Expected upstream URL 'http://localhost:9999', got: %s", cfg.UpstreamURL)
	}
	if cfg.Port != 5555 {
		t.Errorf("Expected port 5555, got: %d", cfg.Port)
	}
	if cfg.LoopDetection.MessageWindow != 5 {
		t.Errorf("Expected message window 5, got: %d", cfg.LoopDetection.MessageWindow)
	}

	// Verify models were migrated
	modelsMgr, err := NewModelsManager(store)
	if err != nil {
		t.Fatalf("Failed to create models manager: %v", err)
	}

	modelList := modelsMgr.GetModels()
	if len(modelList) != 2 {
		t.Errorf("Expected 2 models, got: %d", len(modelList))
	}

	// Verify JSON files were renamed
	if _, err := os.Stat(filepath.Join(configDir, "config.json")); !os.IsNotExist(err) {
		t.Error("Expected config.json to be renamed to .migrated")
	}
	if _, err := os.Stat(filepath.Join(configDir, "config.json.migrated")); os.IsNotExist(err) {
		t.Error("Expected config.json.migrated to exist")
	}
}

func TestInitializeAll(t *testing.T) {
	// Test the main initialization function
	ctx := context.Background()
	bus := events.NewBus()

	// Use temp directory for database
	tmpDir := t.TempDir()
	originalConfigDir := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer os.Setenv("XDG_CONFIG_HOME", originalConfigDir)

	store, cfgMgr, modelsMgr, err := InitializeAll(ctx, bus)
	if err != nil {
		t.Fatalf("InitializeAll failed: %v", err)
	}
	defer store.Close()

	// Verify managers work
	cfg := cfgMgr.Get()
	if cfg.UpstreamURL == "" {
		t.Error("Expected non-empty config")
	}

	modelList := modelsMgr.GetModels()
	_ = modelList // May be empty, which is fine

	// Test that config.Manager interface is satisfied
	var _ config.ManagerInterface = cfgMgr
	var _ models.ModelsConfigInterface = modelsMgr

	t.Log("InitializeAll completed successfully")
}

func TestConfigDurationConversion(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite connection: %v", err)
	}
	defer store.Close()

	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Save config with specific durations
	cfg := cfgMgr.Get()
	cfg.IdleTimeout = config.Duration(45 * time.Second)
	cfg.MaxGenerationTime = config.Duration(120 * time.Second)

	_, err = cfgMgr.Save(cfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Reload and verify
	cfgMgr2, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create second config manager: %v", err)
	}

	loadedCfg := cfgMgr2.Get()
	if loadedCfg.IdleTimeout.Duration() != 45*time.Second {
		t.Errorf("Expected idle timeout 45s, got: %v", loadedCfg.IdleTimeout.Duration())
	}
	if loadedCfg.MaxGenerationTime.Duration() != 120*time.Second {
		t.Errorf("Expected max generation time 120s, got: %v", loadedCfg.MaxGenerationTime.Duration())
	}
}
