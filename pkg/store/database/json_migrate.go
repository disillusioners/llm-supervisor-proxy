package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// MigrateFromJSON performs a one-time migration from JSON files to the database
// It checks if the DB is empty and JSON files exist, then migrates the data
func (s *Store) MigrateFromJSON(ctx context.Context) error {
	// Check if we already have data in the DB
	hasModels, err := s.HasModels(ctx)
	if err != nil {
		return fmt.Errorf("failed to check models: %w", err)
	}

	// If we already have models, skip migration
	if hasModels {
		log.Println("Database already contains data, skipping JSON migration")
		return nil
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	appDir := filepath.Join(cfgDir, "llm-supervisor-proxy")

	// Migrate config.json
	configPath := filepath.Join(appDir, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		if err := s.migrateConfigJSON(ctx, configPath); err != nil {
			log.Printf("Warning: failed to migrate config.json: %v", err)
		} else {
			// Rename migrated file
			backupPath := configPath + ".migrated"
			if err := os.Rename(configPath, backupPath); err != nil {
				log.Printf("Warning: failed to rename migrated config.json: %v", err)
			} else {
				log.Printf("Migrated config.json to database, backup at %s", backupPath)
			}
		}
	}

	// Migrate models.json
	modelsPath := filepath.Join(appDir, "models.json")
	if _, err := os.Stat(modelsPath); err == nil {
		if err := s.migrateModelsJSON(ctx, modelsPath); err != nil {
			log.Printf("Warning: failed to migrate models.json: %v", err)
		} else {
			// Rename migrated file
			backupPath := modelsPath + ".migrated"
			if err := os.Rename(modelsPath, backupPath); err != nil {
				log.Printf("Warning: failed to rename migrated models.json: %v", err)
			} else {
				log.Printf("Migrated models.json to database, backup at %s", backupPath)
			}
		}
	}

	return nil
}

func (s *Store) migrateConfigJSON(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Serialize loop detection to JSON
	loopDetectionJSON, err := json.Marshal(cfg.LoopDetection)
	if err != nil {
		return fmt.Errorf("failed to serialize loop detection: %w", err)
	}

	// Update the config in database
	_, err = s.DB.ExecContext(ctx, `
		UPDATE configs SET
			version = ?,
			upstream_url = ?,
			port = ?,
			idle_timeout_ms = ?,
			max_generation_time_ms = ?,
			max_upstream_error_retries = ?,
			max_idle_retries = ?,
			max_generation_retries = ?,
			loop_detection_json = ?,
			updated_at = ?
		WHERE id = 1
	`,
		cfg.Version,
		cfg.UpstreamURL,
		cfg.Port,
		int64(time.Duration(cfg.IdleTimeout).Milliseconds()),
		int64(time.Duration(cfg.MaxGenerationTime).Milliseconds()),
		cfg.MaxUpstreamErrorRetries,
		cfg.MaxIdleRetries,
		cfg.MaxGenerationRetries,
		string(loopDetectionJSON),
		time.Now().Format(time.RFC3339),
	)

	return err
}

func (s *Store) migrateModelsJSON(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read models file: %w", err)
	}

	var file struct {
		Models []models.ModelConfig `json:"models"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse models file: %w", err)
	}

	for _, model := range file.Models {
		fallbackJSON, _ := json.Marshal(model.FallbackChain)
		truncateJSON, _ := json.Marshal(model.TruncateParams)

		enabled := 0
		if model.Enabled {
			enabled = 1
		}

		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json)
			VALUES (?, ?, ?, ?, ?)
		`,
			model.ID,
			model.Name,
			enabled,
			string(fallbackJSON),
			string(truncateJSON),
		)
		if err != nil {
			log.Printf("Warning: failed to migrate model %s: %v", model.ID, err)
		}
	}

	return nil
}
