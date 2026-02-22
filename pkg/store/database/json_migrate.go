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
	// Check both config and models independently
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	appDir := filepath.Join(cfgDir, "llm-supervisor-proxy")

	configPath := filepath.Join(appDir, "config.json")
	modelsPath := filepath.Join(appDir, "models.json")

	configExists := fileExists(configPath)
	modelsExists := fileExists(modelsPath)

	// If neither file exists, nothing to migrate
	if !configExists && !modelsExists {
		return nil
	}

	qb := NewQueryBuilder(s.Dialect)

	// Migrate config.json independently
	if configExists {
		// Check if config in DB is still at defaults
		needsMigration, err := s.configNeedsMigration(ctx)
		if err != nil {
			log.Printf("Warning: failed to check config migration status: %v", err)
		} else if needsMigration {
			if err := s.migrateConfigJSON(ctx, configPath, qb); err != nil {
				log.Printf("Warning: failed to migrate config.json: %v", err)
			} else {
				backupPath := configPath + ".migrated"
				if err := os.Rename(configPath, backupPath); err != nil {
					log.Printf("Warning: failed to rename migrated config.json: %v", err)
				} else {
					log.Printf("Migrated config.json to database, backup at %s", backupPath)
				}
			}
		}
	}

	// Migrate models.json independently
	if modelsExists {
		// Check if we already have models in the DB
		hasModels, err := s.HasModels(ctx)
		if err != nil {
			log.Printf("Warning: failed to check models: %v", err)
		} else if !hasModels {
			if err := s.migrateModelsJSON(ctx, modelsPath, qb); err != nil {
				log.Printf("Warning: failed to migrate models.json: %v", err)
			} else {
				backupPath := modelsPath + ".migrated"
				if err := os.Rename(modelsPath, backupPath); err != nil {
					log.Printf("Warning: failed to rename migrated models.json: %v", err)
				} else {
					log.Printf("Migrated models.json to database, backup at %s", backupPath)
				}
			}
		}
	}

	return nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// configNeedsMigration checks if the config in the database is still at defaults
func (s *Store) configNeedsMigration(ctx context.Context) (bool, error) {
	query := `SELECT upstream_url, port FROM configs WHERE id = 1`
	row := s.DB.QueryRowContext(ctx, query)

	var upstreamURL string
	var port int64
	err := row.Scan(&upstreamURL, &port)
	if err != nil {
		return false, err
	}

	// If config is at defaults, it needs migration
	defaults := config.Defaults
	return upstreamURL == defaults.UpstreamURL && port == int64(defaults.Port), nil
}

func (s *Store) migrateConfigJSON(ctx context.Context, path string, qb *QueryBuilder) error {
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

	// Update the config in database using dialect-aware query
	query := qb.UpdateConfig()
	_, err = s.DB.ExecContext(ctx, query,
		cfg.Version,
		cfg.UpstreamURL,
		cfg.Port,
		time.Duration(cfg.IdleTimeout).Milliseconds(),
		time.Duration(cfg.MaxGenerationTime).Milliseconds(),
		cfg.MaxUpstreamErrorRetries,
		cfg.MaxIdleRetries,
		cfg.MaxGenerationRetries,
		string(loopDetectionJSON),
		time.Now().Format(time.RFC3339),
	)

	return err
}

func (s *Store) migrateModelsJSON(ctx context.Context, path string, qb *QueryBuilder) error {
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

		query := qb.InsertModel()
		_, err := s.DB.ExecContext(ctx, query,
			model.ID,
			model.Name,
			BooleanToInt(model.Enabled),
			string(fallbackJSON),
			string(truncateJSON),
		)
		if err != nil {
			log.Printf("Warning: failed to migrate model %s: %v", model.ID, err)
		}
	}

	return nil
}
