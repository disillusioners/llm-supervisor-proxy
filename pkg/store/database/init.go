package database

import (
	"context"
	"log"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// Initialize sets up the database and performs migrations
// This should be called at application startup
func Initialize(ctx context.Context) (*Store, error) {
	// Create database connection
	store, err := NewConnection(ctx)
	if err != nil {
		return nil, err
	}

	// Run migrations
	if err := store.RunMigrations(ctx); err != nil {
		store.Close()
		return nil, err
	}

	// Run JSON migration if needed
	if err := store.MigrateFromJSON(ctx); err != nil {
		log.Printf("Warning: JSON migration had issues: %v", err)
		// Don't fail - database is still usable
	}

	return store, nil
}

// InitializeManagers creates both config and models managers from a store
func InitializeManagers(store *Store, eventBus *events.Bus) (*ConfigManager, *ModelsManager, error) {
	configMgr, err := NewConfigManager(store, eventBus)
	if err != nil {
		return nil, nil, err
	}

	modelsMgr, err := NewModelsManager(store)
	if err != nil {
		return nil, nil, err
	}

	return configMgr, modelsMgr, nil
}

// InitializeAll creates store and managers in one call
func InitializeAll(ctx context.Context, eventBus *events.Bus) (*Store, *ConfigManager, *ModelsManager, error) {
	store, err := Initialize(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	configMgr, modelsMgr, err := InitializeManagers(store, eventBus)
	if err != nil {
		store.Close()
		return nil, nil, nil, err
	}

	return store, configMgr, modelsMgr, nil
}
