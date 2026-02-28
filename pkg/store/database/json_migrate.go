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

	// First run: no existing user configs. We can still try to seed defaults later.

	qb := NewQueryBuilder(s.Dialect)

	// Migrate config.json independently
	if configExists {
		// Check if config in DB is still at defaults
		needsMigration, err := s.configNeedsMigration(ctx)
		if err != nil {
			log.Printf("Warning: failed to check config migration status: %v", err)
		} else if needsMigration {
			data, err := os.ReadFile(configPath)
			if err == nil {
				if err := s.migrateConfigJSON(ctx, data, qb); err != nil {
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
	} else {
		// Fresh start. Seed default config using embedded JSON template.
		needsMigration, err := s.configNeedsMigration(ctx)
		if err == nil && needsMigration {
			if err := s.migrateConfigJSON(ctx, []byte(defaultConfigJSON), qb); err != nil {
				log.Printf("Warning: failed to seed default config: %v", err)
			} else {
				log.Printf("Seeded default config from embedded template")
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
			data, err := os.ReadFile(modelsPath)
			if err == nil {
				if err := s.migrateModelsJSON(ctx, data, qb); err != nil {
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
	} else {
		// Fresh start without previous models.json. Try seeding from embedded models JSON template.
		hasModels, err := s.HasModels(ctx)
		if err == nil && !hasModels {
			if err := s.migrateModelsJSON(ctx, []byte(defaultModelsJSON), qb); err != nil {
				log.Printf("Warning: failed to seed default models: %v", err)
			} else {
				log.Printf("Seeded default models from embedded template")
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

func (s *Store) migrateConfigJSON(ctx context.Context, data []byte, qb *QueryBuilder) error {
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

func (s *Store) migrateModelsJSON(ctx context.Context, data []byte, qb *QueryBuilder) error {
	var file struct {
		Models      []models.ModelConfig      `json:"models"`
		Credentials []models.CredentialConfig `json:"credentials"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse models file: %w", err)
	}

	// Migrate credentials first (models reference them)
	for _, cred := range file.Credentials {
		encryptedAPIKey := cred.APIKey
		if encryptedAPIKey != "" {
			// Note: API keys in JSON are assumed to be already encrypted or plaintext
			// For migration, we store them as-is (will be encrypted on next save via UI)
		}

		var query string
		if s.Dialect == PostgreSQL {
			query = `INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW()) ON CONFLICT (id) DO NOTHING`
		} else {
			query = `INSERT OR IGNORE INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`
		}

		_, err := s.DB.ExecContext(ctx, query, cred.ID, cred.Provider, encryptedAPIKey, cred.BaseURL)
		if err != nil {
			log.Printf("Warning: failed to migrate credential %s: %v", cred.ID, err)
		}
	}

	// Migrate models
	for _, model := range file.Models {
		fallbackJSON, _ := json.Marshal(model.FallbackChain)
		truncateJSON, _ := json.Marshal(model.TruncateParams)

		query := qb.InsertModel()
		_, err := s.DB.ExecContext(ctx, query,
			model.ID,
			model.Name,
			qb.BooleanLiteral(model.Enabled),
			string(fallbackJSON),
			string(truncateJSON),
			qb.BooleanLiteral(model.Internal),
			model.CredentialID,
			model.InternalBaseURL,
			model.InternalModel,
		)
		if err != nil {
			log.Printf("Warning: failed to migrate model %s: %v", model.ID, err)
		}
	}

	return nil
}
