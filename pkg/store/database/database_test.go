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
