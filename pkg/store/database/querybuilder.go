package database

import (
	"fmt"
	"strings"
)

// QueryBuilder provides dialect-aware SQL query building
type QueryBuilder struct {
	dialect Dialect
}

// NewQueryBuilder creates a new query builder for the given dialect
func NewQueryBuilder(dialect Dialect) *QueryBuilder {
	return &QueryBuilder{dialect: dialect}
}

// Placeholder returns the appropriate placeholder for the current dialect
// SQLite uses ?, PostgreSQL uses $1, $2, etc.
func (q *QueryBuilder) Placeholder(index int) string {
	if q.dialect == PostgreSQL {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

// Placeholders returns a comma-separated list of placeholders
func (q *QueryBuilder) Placeholders(count int) string {
	placeholders := make([]string, count)
	for i := 0; i < count; i++ {
		placeholders[i] = q.Placeholder(i + 1)
	}
	return strings.Join(placeholders, ", ")
}

// Now returns the appropriate function to get current timestamp
func (q *QueryBuilder) Now() string {
	if q.dialect == PostgreSQL {
		return "NOW()"
	}
	return "datetime('now')"
}

// BooleanLiteral converts a bool to the appropriate database representation
func (q *QueryBuilder) BooleanLiteral(b bool) interface{} {
	if q.dialect == PostgreSQL {
		return b
	}
	if b {
		return 1
	}
	return 0
}

// BooleanToInt converts bool to int for SQLite compatibility
func BooleanToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// IntToBoolean converts int to bool for reading from database
func IntToBoolean(v int64) bool {
	return v != 0
}

// UpsertConfig returns the appropriate upsert syntax for inserting/updating config
func (q *QueryBuilder) UpsertConfig() string {
	if q.dialect == PostgreSQL {
		return `INSERT INTO configs (id, version, upstream_url, port, idle_timeout_ms, max_generation_time_ms, 
			max_upstream_error_retries, max_idle_retries, max_generation_retries, loop_detection_json, updated_at)
			VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE SET
				version = EXCLUDED.version,
				upstream_url = EXCLUDED.upstream_url,
				port = EXCLUDED.port,
				idle_timeout_ms = EXCLUDED.idle_timeout_ms,
				max_generation_time_ms = EXCLUDED.max_generation_time_ms,
				max_upstream_error_retries = EXCLUDED.max_upstream_error_retries,
				max_idle_retries = EXCLUDED.max_idle_retries,
				max_generation_retries = EXCLUDED.max_generation_retries,
				loop_detection_json = EXCLUDED.loop_detection_json,
				updated_at = EXCLUDED.updated_at`
	}
	return `INSERT OR REPLACE INTO configs (id, version, upstream_url, port, idle_timeout_ms, max_generation_time_ms,
		max_upstream_error_retries, max_idle_retries, max_generation_retries, loop_detection_json, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

// UpdateConfig returns the appropriate UPDATE query for config
func (q *QueryBuilder) UpdateConfig() string {
	if q.dialect == PostgreSQL {
		return `UPDATE configs SET
			version = $1,
			upstream_url = $2,
			port = $3,
			idle_timeout_ms = $4,
			max_generation_time_ms = $5,
			max_upstream_error_retries = $6,
			max_idle_retries = $7,
			max_generation_retries = $8,
			max_stream_buffer_size = $9,
			loop_detection_json = $10,
			updated_at = $11
		WHERE id = 1`
	}
	return `UPDATE configs SET
			version = ?,
			upstream_url = ?,
			port = ?,
			idle_timeout_ms = ?,
			max_generation_time_ms = ?,
			max_upstream_error_retries = ?,
			max_idle_retries = ?,
			max_generation_retries = ?,
			max_stream_buffer_size = ?,
			loop_detection_json = ?,
			updated_at = ?
		WHERE id = 1`
}

// InsertModel returns the appropriate INSERT query for a model
func (q *QueryBuilder) InsertModel() string {
	if q.dialect == PostgreSQL {
		return `INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
			internal, credential_id, internal_base_url, internal_model)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				enabled = EXCLUDED.enabled,
				fallback_chain_json = EXCLUDED.fallback_chain_json,
				truncate_params_json = EXCLUDED.truncate_params_json,
				internal = EXCLUDED.internal,
				credential_id = EXCLUDED.credential_id,
				internal_base_url = EXCLUDED.internal_base_url,
				internal_model = EXCLUDED.internal_model,
				updated_at = NOW()`
	}
	return `INSERT OR REPLACE INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
		internal, credential_id, internal_base_url, internal_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

// UpdateModel returns the appropriate UPDATE query for a model
func (q *QueryBuilder) UpdateModel() string {
	if q.dialect == PostgreSQL {
		return `UPDATE models SET
			name = $1,
			enabled = $2,
			fallback_chain_json = $3,
			truncate_params_json = $4,
			internal = $5,
			credential_id = $6,
			internal_base_url = $7,
			internal_model = $8,
			updated_at = NOW()
		WHERE id = $9`
	}
	return `UPDATE models SET
			name = ?,
			enabled = ?,
			fallback_chain_json = ?,
			truncate_params_json = ?,
			internal = ?,
			credential_id = ?,
			internal_base_url = ?,
			internal_model = ?,
			updated_at = datetime('now')
		WHERE id = ?`
}

// DeleteModel returns the appropriate DELETE query for a model
func (q *QueryBuilder) DeleteModel() string {
	if q.dialect == PostgreSQL {
		return `DELETE FROM models WHERE id = $1`
	}
	return `DELETE FROM models WHERE id = ?`
}

// GetModelByID returns the appropriate SELECT query for a model
func (q *QueryBuilder) GetModelByID() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at 
			FROM models WHERE id = $1`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at 
		FROM models WHERE id = ?`
}

// GetAllModels returns the appropriate SELECT query for all models
func (q *QueryBuilder) GetAllModels() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
            coalesce(internal, false), coalesce(credential_id, ''),
            coalesce(internal_base_url, ''), coalesce(internal_model, '')
        FROM models ORDER BY name`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
        coalesce(internal, 0), coalesce(credential_id, ''),
        coalesce(internal_base_url, ''), coalesce(internal_model, '')
    FROM models ORDER BY name`
}

// GetEnabledModels returns the appropriate SELECT query for enabled models
func (q *QueryBuilder) GetEnabledModels() string {
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at 
		FROM models WHERE enabled = 1 ORDER BY name`
}

// GetConfig returns the appropriate SELECT query for config
func (q *QueryBuilder) GetConfig() string {
	return `SELECT version, upstream_url, port, idle_timeout_ms, max_generation_time_ms,
		max_upstream_error_retries, max_idle_retries, max_generation_retries, max_stream_buffer_size, loop_detection_json, updated_at
		FROM configs WHERE id = 1`
}
