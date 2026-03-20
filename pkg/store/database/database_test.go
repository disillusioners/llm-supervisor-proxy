// PostgreSQL integration tests require a running PostgreSQL instance.
// Run with: TEST_DATABASE_URL="postgres://user:pass@host:5432/db?sslmode=disable" go test -v -run TestPostgreSQL

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

// skipIfNoPostgreSQL returns a Store connected to PostgreSQL or skips the test
func skipIfNoPostgreSQL(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("Skipping: TEST_DATABASE_URL not set. Set it to run PostgreSQL tests.")
	}
	store, err := newPostgreSQLConnection(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}

	// Run migrations
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	return store
}

func TestPostgreSQLConnection(t *testing.T) {
	store := skipIfNoPostgreSQL(t)
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		t.Errorf("Failed to ping: %v", err)
	}

	// Verify dialect is PostgreSQL
	if store.Dialect != PostgreSQL {
		t.Errorf("Expected dialect PostgreSQL, got: %s", store.Dialect)
	}
}

func TestPostgreSQLConfigManager(t *testing.T) {
	store := skipIfNoPostgreSQL(t)
	defer store.Close()

	// Create config manager
	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Test Get - should return defaults
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

	// Clean up - reset to default
	defaultCfg := config.Defaults
	defaultCfg.UpstreamURL = "http://localhost:4001"
	_, _ = cfgMgr.Save(defaultCfg)

	// Verify cleanup
	cleanedCfg := cfgMgr.Get()
	if cleanedCfg.UpstreamURL != "http://localhost:4001" {
		t.Errorf("Expected upstream URL to be reset, got: %s", cleanedCfg.UpstreamURL)
	}
}

func TestPostgreSQLModelsManager(t *testing.T) {
	store := skipIfNoPostgreSQL(t)
	defer store.Close()

	// Create models manager
	modelsMgr, err := NewModelsManager(store)
	if err != nil {
		t.Fatalf("Failed to create models manager: %v", err)
	}

	// Clean up any existing test data first
	_ = modelsMgr.RemoveModel("test-model-pg")
	_ = modelsMgr.RemoveModel("test-model-pg-2")

	// Set up cleanup to run after test
	t.Cleanup(func() {
		_ = modelsMgr.RemoveModel("test-model-pg")
		_ = modelsMgr.RemoveModel("test-model-pg-2")
	})

	// Test empty models list
	modelList := modelsMgr.GetModels()
	_ = modelList // May have data from previous tests

	// Test AddModel
	testModel := models.ModelConfig{
		ID:             "test-model-pg",
		Name:           "Test Model PG",
		Enabled:        true,
		FallbackChain:  []string{"fallback-model"},
		TruncateParams: []string{"max_tokens"},
	}
	if err := modelsMgr.AddModel(testModel); err != nil {
		t.Fatalf("Failed to add model: %v", err)
	}

	// Verify model was added
	modelList = modelsMgr.GetModels()
	found := false
	for _, m := range modelList {
		if m.ID == "test-model-pg" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected model 'test-model-pg' to be found")
	}

	// Test GetFallbackChain
	chain := modelsMgr.GetFallbackChain("test-model-pg")
	if len(chain) != 2 { // model ID + fallback
		t.Errorf("Expected chain length 2, got: %d", len(chain))
	}
	if chain[0] != "test-model-pg" {
		t.Errorf("Expected first element to be model ID, got: %s", chain[0])
	}

	// Test GetTruncateParams
	params := modelsMgr.GetTruncateParams("test-model-pg")
	if len(params) != 1 || params[0] != "max_tokens" {
		t.Errorf("Expected truncate params ['max_tokens'], got: %v", params)
	}

	// Test UpdateModel
	updatedModel := testModel
	updatedModel.Name = "Updated Test Model PG"
	updatedModel.Enabled = false
	if err := modelsMgr.UpdateModel("test-model-pg", updatedModel); err != nil {
		t.Fatalf("Failed to update model: %v", err)
	}

	// Verify update
	modelList = modelsMgr.GetModels()
	for _, m := range modelList {
		if m.ID == "test-model-pg" {
			if m.Name != "Updated Test Model PG" {
				t.Errorf("Expected updated name, got: %s", m.Name)
			}
			if m.Enabled != false {
				t.Errorf("Expected enabled to be false, got: %v", m.Enabled)
			}
			break
		}
	}

	// Test RemoveModel
	if err := modelsMgr.RemoveModel("test-model-pg"); err != nil {
		t.Fatalf("Failed to remove model: %v", err)
	}

	// Verify removal
	modelList = modelsMgr.GetModels()
	for _, m := range modelList {
		if m.ID == "test-model-pg" {
			t.Error("Expected model to be removed")
			break
		}
	}
}

func TestConfigManager_EnvOverrides(t *testing.T) {
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

	// Save a config to the database
	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := cfgMgr.Get()
	cfg.UpstreamURL = "http://database-value:4001"
	cfg.Port = 5000
	cfg.RaceRetryEnabled = false
	if _, err := cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Set environment variables to override database values
	os.Setenv("APPLY_ENV_OVERRIDES", "true")
	os.Setenv("UPSTREAM_URL", "http://env-override:9999")
	os.Setenv("PORT", "8888")
	os.Setenv("RACE_RETRY_ENABLED", "true")
	defer func() {
		os.Unsetenv("APPLY_ENV_OVERRIDES")
		os.Unsetenv("UPSTREAM_URL")
		os.Unsetenv("PORT")
		os.Unsetenv("RACE_RETRY_ENABLED")
	}()

	// Create a new config manager to test loading with env overrides
	cfgMgr2, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create second config manager: %v", err)
	}

	// Verify env overrides are applied
	loadedCfg := cfgMgr2.Get()
	if loadedCfg.UpstreamURL != "http://env-override:9999" {
		t.Errorf("Expected UpstreamURL to be overridden by env to 'http://env-override:9999', got: %s", loadedCfg.UpstreamURL)
	}
	if loadedCfg.Port != 8888 {
		t.Errorf("Expected Port to be overridden by env to 8888, got: %d", loadedCfg.Port)
	}
	if !loadedCfg.RaceRetryEnabled {
		t.Errorf("Expected RaceRetryEnabled to be overridden by env to true, got: %v", loadedCfg.RaceRetryEnabled)
	}
}

func TestPostgreSQLBooleanHandling(t *testing.T) {
	store := skipIfNoPostgreSQL(t)
	defer store.Close()

	// Create models manager
	modelsMgr, err := NewModelsManager(store)
	if err != nil {
		t.Fatalf("Failed to create models manager: %v", err)
	}

	// Clean up any existing test data
	_ = modelsMgr.RemoveModel("bool-test-model-true")
	_ = modelsMgr.RemoveModel("bool-test-model-false")

	// Set up cleanup
	t.Cleanup(func() {
		_ = modelsMgr.RemoveModel("bool-test-model-true")
		_ = modelsMgr.RemoveModel("bool-test-model-false")
	})

	// Test model with enabled = true
	trueModel := models.ModelConfig{
		ID:      "bool-test-model-true",
		Name:    "Boolean True Model",
		Enabled: true,
	}
	if err := modelsMgr.AddModel(trueModel); err != nil {
		t.Fatalf("Failed to add true model: %v", err)
	}

	// Test model with enabled = false
	falseModel := models.ModelConfig{
		ID:      "bool-test-model-false",
		Name:    "Boolean False Model",
		Enabled: false,
	}
	if err := modelsMgr.AddModel(falseModel); err != nil {
		t.Fatalf("Failed to add false model: %v", err)
	}

	// Retrieve and verify enabled = true
	modelsList := modelsMgr.GetModels()
	var trueModelResult, falseModelResult *models.ModelConfig
	for i := range modelsList {
		if modelsList[i].ID == "bool-test-model-true" {
			m := modelsList[i]
			trueModelResult = &m
		}
		if modelsList[i].ID == "bool-test-model-false" {
			m := modelsList[i]
			falseModelResult = &m
		}
	}

	if trueModelResult == nil {
		t.Fatal("Could not find true model")
	}
	if falseModelResult == nil {
		t.Fatal("Could not find false model")
	}

	// Verify boolean values are correctly handled
	if trueModelResult.Enabled != true {
		t.Errorf("Expected enabled=true, got: %v", trueModelResult.Enabled)
	}
	if falseModelResult.Enabled != false {
		t.Errorf("Expected enabled=false, got: %v", falseModelResult.Enabled)
	}

	// Test GetEnabledModels - should only return enabled models
	enabledModels := modelsMgr.GetEnabledModels()
	foundEnabled := false
	for _, m := range enabledModels {
		if m.ID == "bool-test-model-true" {
			foundEnabled = true
			break
		}
	}
	if !foundEnabled {
		t.Error("Expected enabled model to be in GetEnabledModels result")
	}

	// Verify false model is not in enabled list
	for _, m := range enabledModels {
		if m.ID == "bool-test-model-false" {
			t.Error("Disabled model should not be in GetEnabledModels")
		}
	}
}

func TestRaceRetryPersistence(t *testing.T) {
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

	// Get initial config
	cfg := cfgMgr.Get()
	initialMaxParallel := cfg.RaceMaxParallel

	// Save with custom race retry settings
	cfg.RaceRetryEnabled = true
	cfg.RaceParallelOnIdle = true
	cfg.RaceMaxParallel = 7
	cfg.RaceMaxBufferBytes = 2000000

	_, err = cfgMgr.Save(cfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Verify in same session
	cfg2 := cfgMgr.Get()
	if cfg2.RaceMaxParallel != 7 {
		t.Errorf("Same session: RaceMaxParallel = %d, want 7", cfg2.RaceMaxParallel)
	}

	// Now simulate restart - create NEW config manager
	store2, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second SQLite connection: %v", err)
	}
	defer store2.Close()

	cfgMgr2, err := NewConfigManager(store2, bus)
	if err != nil {
		t.Fatalf("Failed to create second config manager: %v", err)
	}

	// Load from database (simulating restart)
	if err := cfgMgr2.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg3 := cfgMgr2.Get()

	// Check all race retry fields persisted
	if cfg3.RaceRetryEnabled != true {
		t.Errorf("After restart: RaceRetryEnabled = %v, want true", cfg3.RaceRetryEnabled)
	}
	if cfg3.RaceParallelOnIdle != true {
		t.Errorf("After restart: RaceParallelOnIdle = %v, want true", cfg3.RaceParallelOnIdle)
	}
	if cfg3.RaceMaxParallel != 7 {
		t.Errorf("After restart: RaceMaxParallel = %d, want 7", cfg3.RaceMaxParallel)
	}
	if cfg3.RaceMaxBufferBytes != 2000000 {
		t.Errorf("After restart: RaceMaxBufferBytes = %d, want 2000000", cfg3.RaceMaxBufferBytes)
	}

	// Verify we didn't break defaults (restore original)
	t.Logf("Initial max_parallel was %d, now is %d (should be 7)", initialMaxParallel, cfg3.RaceMaxParallel)
}

func TestLogRawUpstreamPersistence(t *testing.T) {
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

	// Get initial config
	cfg := cfgMgr.Get()

	// Save with custom log_raw_upstream settings
	cfg.LogRawUpstreamResponse = true
	cfg.LogRawUpstreamOnError = true
	cfg.LogRawUpstreamMaxKB = 2048

	_, err = cfgMgr.Save(cfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Verify in same session
	cfg2 := cfgMgr.Get()
	if cfg2.LogRawUpstreamResponse != true {
		t.Errorf("Same session: LogRawUpstreamResponse = %v, want true", cfg2.LogRawUpstreamResponse)
	}
	if cfg2.LogRawUpstreamOnError != true {
		t.Errorf("Same session: LogRawUpstreamOnError = %v, want true", cfg2.LogRawUpstreamOnError)
	}

	// Now simulate restart - create NEW config manager
	store2, err := newSQLiteConnectionAtPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second SQLite connection: %v", err)
	}
	defer store2.Close()

	cfgMgr2, err := NewConfigManager(store2, bus)
	if err != nil {
		t.Fatalf("Failed to create second config manager: %v", err)
	}

	// Load from database (simulating restart)
	if err := cfgMgr2.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg3 := cfgMgr2.Get()

	// Check all log_raw_upstream fields persisted
	if cfg3.LogRawUpstreamResponse != true {
		t.Errorf("After restart: LogRawUpstreamResponse = %v, want true", cfg3.LogRawUpstreamResponse)
	}
	if cfg3.LogRawUpstreamOnError != true {
		t.Errorf("After restart: LogRawUpstreamOnError = %v, want true", cfg3.LogRawUpstreamOnError)
	}
	if cfg3.LogRawUpstreamMaxKB != 2048 {
		t.Errorf("After restart: LogRawUpstreamMaxKB = %d, want 2048", cfg3.LogRawUpstreamMaxKB)
	}

	t.Logf("Log raw upstream settings persisted correctly: response=%v, on_error=%v, max_kb=%d",
		cfg3.LogRawUpstreamResponse, cfg3.LogRawUpstreamOnError, cfg3.LogRawUpstreamMaxKB)
}

// TestConfigManager_EmptyDatabase tests Load() with empty database
// Should return default config with ENV overrides applied
func TestConfigManager_EmptyDatabase(t *testing.T) {
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

	// Should return defaults
	cfg := cfgMgr.Get()
	if cfg.UpstreamURL != config.Defaults.UpstreamURL {
		t.Errorf("Expected default UpstreamURL %s, got %s", config.Defaults.UpstreamURL, cfg.UpstreamURL)
	}
	if cfg.Port != config.Defaults.Port {
		t.Errorf("Expected default Port %d, got %d", config.Defaults.Port, cfg.Port)
	}
	if cfg.IdleTimeout != config.Defaults.IdleTimeout {
		t.Errorf("Expected default IdleTimeout %v, got %v", config.Defaults.IdleTimeout, cfg.IdleTimeout)
	}
}

// TestConfigManager_ValidJSON tests Load() with valid JSON in database
// Should unmarshal correctly and apply ENV overrides on top
func TestConfigManager_ValidJSON(t *testing.T) {
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

	// Insert valid JSON directly into database (UPDATE because migrations already insert a row)
	validJSON := `{
		"version": "1.0",
		"upstream_url": "http://json-test:4001",
		"port": 5555,
		"idle_timeout": "45s",
		"stream_deadline": "110s",
		"max_generation_time": "200s",
		"race_retry_enabled": true,
		"race_parallel_on_idle": false,
		"race_max_parallel": 5,
		"race_max_buffer_bytes": 3000000,
		"loop_detection": {
			"enabled": true,
			"shadow_mode": false,
			"message_window": 20
		}
	}`

	_, err = store.DB.ExecContext(context.Background(),
		"UPDATE configs SET config_json = ?, updated_at = datetime('now') WHERE id = 1",
		validJSON)
	if err != nil {
		t.Fatalf("Failed to update test JSON: %v", err)
	}

	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := cfgMgr.Get()
	if cfg.UpstreamURL != "http://json-test:4001" {
		t.Errorf("Expected UpstreamURL 'http://json-test:4001', got %s", cfg.UpstreamURL)
	}
	if cfg.Port != 5555 {
		t.Errorf("Expected Port 5555, got %d", cfg.Port)
	}
	if cfg.IdleTimeout != config.Duration(45*time.Second) {
		t.Errorf("Expected IdleTimeout 45s, got %v", cfg.IdleTimeout)
	}
	if !cfg.RaceRetryEnabled {
		t.Errorf("Expected RaceRetryEnabled true, got %v", cfg.RaceRetryEnabled)
	}
	if cfg.RaceMaxParallel != 5 {
		t.Errorf("Expected RaceMaxParallel 5, got %d", cfg.RaceMaxParallel)
	}
	if cfg.LoopDetection.MessageWindow != 20 {
		t.Errorf("Expected LoopDetection.MessageWindow 20, got %d", cfg.LoopDetection.MessageWindow)
	}
}

// TestConfigManager_InvalidJSON tests Load() with invalid JSON in database
// Should fall back to defaults (corruption recovery)
func TestConfigManager_InvalidJSON(t *testing.T) {
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

	// Insert invalid JSON directly into database (UPDATE because migrations already insert a row)
	invalidJSON := `{this is not valid json`

	_, err = store.DB.ExecContext(context.Background(),
		"UPDATE configs SET config_json = ?, updated_at = datetime('now') WHERE id = 1",
		invalidJSON)
	if err != nil {
		t.Fatalf("Failed to update invalid JSON: %v", err)
	}

	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Should fall back to defaults
	cfg := cfgMgr.Get()
	if cfg.UpstreamURL != config.Defaults.UpstreamURL {
		t.Errorf("Expected default UpstreamURL after corruption, got %s", cfg.UpstreamURL)
	}
	if cfg.Port != config.Defaults.Port {
		t.Errorf("Expected default Port after corruption, got %d", cfg.Port)
	}
}

// TestConfigManager_PartialUpdate tests Save() with partial config
// Should merge with existing config and not overwrite unspecified fields
func TestConfigManager_PartialUpdate(t *testing.T) {
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

	// First, save a complete config with non-default values
	fullCfg := cfgMgr.Get()
	fullCfg.UpstreamURL = "http://initial:4001"
	fullCfg.Port = 7777
	fullCfg.RaceRetryEnabled = true
	fullCfg.RaceMaxParallel = 10
	fullCfg.LoopDetection.MessageWindow = 25
	fullCfg.LoopDetection.ShadowMode = false

	_, err = cfgMgr.Save(fullCfg)
	if err != nil {
		t.Fatalf("Failed to save initial config: %v", err)
	}

	// Now save a partial config (only changing UpstreamURL)
	// IMPORTANT: We only set UpstreamURL and required duration fields.
	// We do NOT set RaceMaxParallel because that would trigger isRaceRetryProvided()
	// which would then copy all race retry fields (including RaceRetryEnabled=false).
	// The merge logic only merges race retry if it detects race retry was "provided".
	partialCfg := config.Config{
		UpstreamURL: "http://updated:4001",
	}
	// Set only the required duration fields for validation (these are always sent by frontend)
	partialCfg.IdleTimeout = fullCfg.IdleTimeout
	partialCfg.StreamDeadline = fullCfg.StreamDeadline
	partialCfg.MaxGenerationTime = fullCfg.MaxGenerationTime

	_, err = cfgMgr.Save(partialCfg)
	if err != nil {
		t.Fatalf("Failed to save partial config: %v", err)
	}

	// Verify merge behavior
	loadedCfg := cfgMgr.Get()

	// UpstreamURL should be updated
	if loadedCfg.UpstreamURL != "http://updated:4001" {
		t.Errorf("Expected UpstreamURL 'http://updated:4001', got %s", loadedCfg.UpstreamURL)
	}

	// Port should be preserved from previous config
	if loadedCfg.Port != 7777 {
		t.Errorf("Expected Port 7777 (preserved), got %d", loadedCfg.Port)
	}

	// RaceRetryEnabled should be preserved (not overwritten to false)
	// This works because we didn't set RaceMaxParallel in the partial config
	if !loadedCfg.RaceRetryEnabled {
		t.Errorf("Expected RaceRetryEnabled true (preserved), got %v", loadedCfg.RaceRetryEnabled)
	}

	// RaceMaxParallel should be preserved
	if loadedCfg.RaceMaxParallel != 10 {
		t.Errorf("Expected RaceMaxParallel 10 (preserved), got %d", loadedCfg.RaceMaxParallel)
	}

	// LoopDetection.MessageWindow should be preserved
	if loadedCfg.LoopDetection.MessageWindow != 25 {
		t.Errorf("Expected LoopDetection.MessageWindow 25 (preserved), got %d", loadedCfg.LoopDetection.MessageWindow)
	}

	// LoopDetection.ShadowMode should be preserved
	if loadedCfg.LoopDetection.ShadowMode {
		t.Errorf("Expected LoopDetection.ShadowMode false (preserved), got %v", loadedCfg.LoopDetection.ShadowMode)
	}
}

// TestConfigManager_EnvOverridePrecedence tests that ENV variables override database values
func TestConfigManager_EnvOverridePrecedence(t *testing.T) {
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

	// Update config with specific values in database (migrations already insert a row)
	dbJSON := `{
		"version": "1.0",
		"upstream_url": "http://database:4001",
		"port": 6000,
		"idle_timeout": "30s",
		"stream_deadline": "110s",
		"max_generation_time": "200s",
		"race_retry_enabled": false,
		"race_max_parallel": 3
	}`

	_, err = store.DB.ExecContext(context.Background(),
		"UPDATE configs SET config_json = ?, updated_at = datetime('now') WHERE id = 1",
		dbJSON)
	if err != nil {
		t.Fatalf("Failed to update test JSON: %v", err)
	}

	// Set ENV overrides
	os.Setenv("APPLY_ENV_OVERRIDES", "true")
	os.Setenv("UPSTREAM_URL", "http://env-override:9999")
	os.Setenv("PORT", "8888")
	os.Setenv("RACE_RETRY_ENABLED", "true")
	os.Setenv("RACE_MAX_PARALLEL", "7")
	defer func() {
		os.Unsetenv("APPLY_ENV_OVERRIDES")
		os.Unsetenv("UPSTREAM_URL")
		os.Unsetenv("PORT")
		os.Unsetenv("RACE_RETRY_ENABLED")
		os.Unsetenv("RACE_MAX_PARALLEL")
	}()

	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := cfgMgr.Get()

	// ENV should override database values
	if cfg.UpstreamURL != "http://env-override:9999" {
		t.Errorf("Expected UpstreamURL from ENV 'http://env-override:9999', got %s", cfg.UpstreamURL)
	}
	if cfg.Port != 8888 {
		t.Errorf("Expected Port from ENV 8888, got %d", cfg.Port)
	}
	if !cfg.RaceRetryEnabled {
		t.Errorf("Expected RaceRetryEnabled from ENV true, got %v", cfg.RaceRetryEnabled)
	}
	if cfg.RaceMaxParallel != 7 {
		t.Errorf("Expected RaceMaxParallel from ENV 7, got %d", cfg.RaceMaxParallel)
	}

	// idle_timeout should come from database (no ENV override for this specific test)
	if cfg.IdleTimeout != config.Duration(30*time.Second) {
		t.Errorf("Expected IdleTimeout from database 30s, got %v", cfg.IdleTimeout)
	}
}

// TestConfigManager_JSONRoundtrip tests that all fields serialize/deserialize correctly
// This test verifies JSON marshaling/unmarshaling by directly inserting JSON into the database
// and checking that all fields are correctly loaded.
func TestConfigManager_JSONRoundtrip(t *testing.T) {
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

	// Create a comprehensive JSON config with all fields set to non-default values
	// This tests the JSON unmarshaling path directly
	configJSON := `{
		"version": "1.0",
		"upstream_url": "http://roundtrip:4001",
		"upstream_credential_id": "cred-123",
		"port": 9999,
		"idle_timeout": "45s",
		"stream_deadline": "120s",
		"max_generation_time": "250s",
		"max_stream_buffer_size": 20971520,
		"buffer_storage_dir": "/tmp/test-buffers",
		"buffer_max_storage_mb": 200,
		"sse_heartbeat_enabled": true,
		"loop_detection": {
			"enabled": false,
			"shadow_mode": false,
			"message_window": 15,
			"action_window": 20,
			"exact_match_count": 4,
			"similarity_threshold": 0.9,
			"min_tokens_for_simhash": 20,
			"action_repeat_count": 5,
			"oscillation_count": 6,
			"min_tokens_for_analysis": 30,
			"thinking_min_tokens": 150,
			"trigram_threshold": 0.4,
			"max_cycle_length": 6,
			"reasoning_model_patterns": ["o1", "o3"],
			"reasoning_trigram_threshold": 0.2
		},
		"race_retry_enabled": true,
		"race_parallel_on_idle": false,
		"race_max_parallel": 8,
		"race_max_buffer_bytes": 4000000,
		"tool_call_buffer_disabled": true,
		"tool_call_buffer_max_size": 2097152,
		"log_raw_upstream_response": false,
		"log_raw_upstream_on_error": true,
		"log_raw_upstream_max_kb": 2048,
		"ultimate_model": {
			"model_id": "ultimate-model",
			"max_hash": 200,
			"max_retries": 5
		}
	}`

	// Update the database with our test JSON
	_, err = store.DB.ExecContext(context.Background(),
		"UPDATE configs SET config_json = ?, updated_at = datetime('now') WHERE id = 1",
		configJSON)
	if err != nil {
		t.Fatalf("Failed to update test JSON: %v", err)
	}

	bus := events.NewBus()
	cfgMgr, err := NewConfigManager(store, bus)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	loadedCfg := cfgMgr.Get()

	// Verify core fields
	if loadedCfg.UpstreamURL != "http://roundtrip:4001" {
		t.Errorf("UpstreamURL mismatch: got %s, want http://roundtrip:4001", loadedCfg.UpstreamURL)
	}
	if loadedCfg.UpstreamCredentialID != "cred-123" {
		t.Errorf("UpstreamCredentialID mismatch: got %s, want cred-123", loadedCfg.UpstreamCredentialID)
	}
	if loadedCfg.Port != 9999 {
		t.Errorf("Port mismatch: got %d, want 9999", loadedCfg.Port)
	}
	if loadedCfg.IdleTimeout != config.Duration(45*time.Second) {
		t.Errorf("IdleTimeout mismatch: got %v, want 45s", loadedCfg.IdleTimeout)
	}
	if loadedCfg.StreamDeadline != config.Duration(120*time.Second) {
		t.Errorf("StreamDeadline mismatch: got %v, want 120s", loadedCfg.StreamDeadline)
	}
	if loadedCfg.MaxGenerationTime != config.Duration(250*time.Second) {
		t.Errorf("MaxGenerationTime mismatch: got %v, want 250s", loadedCfg.MaxGenerationTime)
	}
	if loadedCfg.MaxStreamBufferSize != 20971520 {
		t.Errorf("MaxStreamBufferSize mismatch: got %d, want 20971520", loadedCfg.MaxStreamBufferSize)
	}
	if loadedCfg.BufferStorageDir != "/tmp/test-buffers" {
		t.Errorf("BufferStorageDir mismatch: got %s, want /tmp/test-buffers", loadedCfg.BufferStorageDir)
	}
	if loadedCfg.BufferMaxStorageMB != 200 {
		t.Errorf("BufferMaxStorageMB mismatch: got %d, want 200", loadedCfg.BufferMaxStorageMB)
	}
	if !loadedCfg.SSEHeartbeatEnabled {
		t.Errorf("SSEHeartbeatEnabled mismatch: got %v, want true", loadedCfg.SSEHeartbeatEnabled)
	}

	// Verify race retry fields
	if !loadedCfg.RaceRetryEnabled {
		t.Errorf("RaceRetryEnabled mismatch: got %v, want true", loadedCfg.RaceRetryEnabled)
	}
	if loadedCfg.RaceParallelOnIdle {
		t.Errorf("RaceParallelOnIdle mismatch: got %v, want false", loadedCfg.RaceParallelOnIdle)
	}
	if loadedCfg.RaceMaxParallel != 8 {
		t.Errorf("RaceMaxParallel mismatch: got %d, want 8", loadedCfg.RaceMaxParallel)
	}
	if loadedCfg.RaceMaxBufferBytes != 4000000 {
		t.Errorf("RaceMaxBufferBytes mismatch: got %d, want 4000000", loadedCfg.RaceMaxBufferBytes)
	}

	// Verify tool call buffer fields
	if !loadedCfg.ToolCallBufferDisabled {
		t.Errorf("ToolCallBufferDisabled mismatch: got %v, want true", loadedCfg.ToolCallBufferDisabled)
	}
	if loadedCfg.ToolCallBufferMaxSize != 2097152 {
		t.Errorf("ToolCallBufferMaxSize mismatch: got %d, want 2097152", loadedCfg.ToolCallBufferMaxSize)
	}

	// Verify log raw upstream fields
	if loadedCfg.LogRawUpstreamResponse {
		t.Errorf("LogRawUpstreamResponse mismatch: got %v, want false", loadedCfg.LogRawUpstreamResponse)
	}
	if !loadedCfg.LogRawUpstreamOnError {
		t.Errorf("LogRawUpstreamOnError mismatch: got %v, want true", loadedCfg.LogRawUpstreamOnError)
	}
	if loadedCfg.LogRawUpstreamMaxKB != 2048 {
		t.Errorf("LogRawUpstreamMaxKB mismatch: got %d, want 2048", loadedCfg.LogRawUpstreamMaxKB)
	}

	// Verify loop detection fields
	if loadedCfg.LoopDetection.Enabled {
		t.Errorf("LoopDetection.Enabled mismatch: got %v, want false", loadedCfg.LoopDetection.Enabled)
	}
	if loadedCfg.LoopDetection.ShadowMode {
		t.Errorf("LoopDetection.ShadowMode mismatch: got %v, want false", loadedCfg.LoopDetection.ShadowMode)
	}
	if loadedCfg.LoopDetection.MessageWindow != 15 {
		t.Errorf("LoopDetection.MessageWindow mismatch: got %d, want 15", loadedCfg.LoopDetection.MessageWindow)
	}
	if loadedCfg.LoopDetection.ActionWindow != 20 {
		t.Errorf("LoopDetection.ActionWindow mismatch: got %d, want 20", loadedCfg.LoopDetection.ActionWindow)
	}
	if loadedCfg.LoopDetection.SimilarityThreshold != 0.9 {
		t.Errorf("LoopDetection.SimilarityThreshold mismatch: got %v, want 0.9", loadedCfg.LoopDetection.SimilarityThreshold)
	}
	if loadedCfg.LoopDetection.ThinkingMinTokens != 150 {
		t.Errorf("LoopDetection.ThinkingMinTokens mismatch: got %d, want 150", loadedCfg.LoopDetection.ThinkingMinTokens)
	}
	if len(loadedCfg.LoopDetection.ReasoningModelPatterns) != 2 {
		t.Errorf("LoopDetection.ReasoningModelPatterns mismatch: got %v, want [o1 o3]", loadedCfg.LoopDetection.ReasoningModelPatterns)
	}

	// Verify ultimate model fields
	if loadedCfg.UltimateModel.ModelID != "ultimate-model" {
		t.Errorf("UltimateModel.ModelID mismatch: got %s, want ultimate-model", loadedCfg.UltimateModel.ModelID)
	}
	if loadedCfg.UltimateModel.MaxHash != 200 {
		t.Errorf("UltimateModel.MaxHash mismatch: got %d, want 200", loadedCfg.UltimateModel.MaxHash)
	}
	if loadedCfg.UltimateModel.MaxRetries != 5 {
		t.Errorf("UltimateModel.MaxRetries mismatch: got %d, want 5", loadedCfg.UltimateModel.MaxRetries)
	}
}
