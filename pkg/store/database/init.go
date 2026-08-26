package database

import (
	"context"

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

	return store, nil
}

// InitializeManagers creates both config and models managers from a store
func InitializeManagers(store *Store, eventBus *events.Bus) (*ConfigManager, *ModelsManager, error) {
	configMgr, err := NewConfigManager(store, eventBus)
	if err != nil {
		return nil, nil, err
	}

	// Phase 2: the event bus flows into ModelsManager so it can
	// subscribe to model.credentials.changed on the engine's behalf
	// (nil-bus safe — the engine then runs on direct-call
	// invalidation only).
	modelsMgr, err := NewModelsManager(store, eventBus)
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
