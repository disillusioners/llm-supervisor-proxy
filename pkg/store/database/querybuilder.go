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

// UpsertConfig returns a query to insert or replace the config JSON
func (q *QueryBuilder) UpsertConfig() string {
	if q.dialect == PostgreSQL {
		return `INSERT INTO configs (id, config_json) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET config_json = $1, updated_at = NOW()`
	}
	return `INSERT OR REPLACE INTO configs (id, config_json, updated_at) VALUES (1, ?, datetime('now'))`
}

// SelectConfig returns a query to select the config JSON
func (q *QueryBuilder) SelectConfig() string {
	return `SELECT config_json FROM configs WHERE id = 1`
}

// InsertModel returns the appropriate INSERT query for a model
func (q *QueryBuilder) InsertModel() string {
	if q.dialect == PostgreSQL {
		return `INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
			internal, credential_id, internal_base_url, internal_model, release_stream_chunk_deadline,
			peak_hour_enabled, peak_hour_start, peak_hour_end, peak_hour_timezone, peak_hour_model)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				enabled = EXCLUDED.enabled,
				fallback_chain_json = EXCLUDED.fallback_chain_json,
				truncate_params_json = EXCLUDED.truncate_params_json,
				internal = EXCLUDED.internal,
				credential_id = EXCLUDED.credential_id,
				internal_base_url = EXCLUDED.internal_base_url,
				internal_model = EXCLUDED.internal_model,
				release_stream_chunk_deadline = EXCLUDED.release_stream_chunk_deadline,
				peak_hour_enabled = EXCLUDED.peak_hour_enabled,
				peak_hour_start = EXCLUDED.peak_hour_start,
				peak_hour_end = EXCLUDED.peak_hour_end,
				peak_hour_timezone = EXCLUDED.peak_hour_timezone,
				peak_hour_model = EXCLUDED.peak_hour_model,
				updated_at = NOW()`
	}
	return `INSERT OR REPLACE INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
		internal, credential_id, internal_base_url, internal_model, release_stream_chunk_deadline,
		peak_hour_enabled, peak_hour_start, peak_hour_end, peak_hour_timezone, peak_hour_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
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
			release_stream_chunk_deadline = $9,
			peak_hour_enabled = $10,
			peak_hour_start = $11,
			peak_hour_end = $12,
			peak_hour_timezone = $13,
			peak_hour_model = $14,
			updated_at = NOW()
		WHERE id = $15`
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
			release_stream_chunk_deadline = ?,
			peak_hour_enabled = ?,
			peak_hour_start = ?,
			peak_hour_end = ?,
			peak_hour_timezone = ?,
			peak_hour_model = ?,
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
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at, 
			coalesce(release_stream_chunk_deadline, 0), 
			coalesce(internal, false), coalesce(credential_id, ''),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
		FROM models WHERE id = $1`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at, 
		coalesce(release_stream_chunk_deadline, 0),
		coalesce(internal, 0), coalesce(credential_id, ''),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
	FROM models WHERE id = ?`
}

// GetAllModels returns the appropriate SELECT query for all models
func (q *QueryBuilder) GetAllModels() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
            coalesce(release_stream_chunk_deadline, 0),
            coalesce(internal, false), coalesce(credential_id, ''),
            coalesce(internal_base_url, ''), coalesce(internal_model, ''),
            peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
            coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
        FROM models ORDER BY name`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
        coalesce(release_stream_chunk_deadline, 0),
        coalesce(internal, 0), coalesce(credential_id, ''),
        coalesce(internal_base_url, ''), coalesce(internal_model, ''),
        peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
        coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
    FROM models ORDER BY name`
}

// GetEnabledModels returns the appropriate SELECT query for enabled models
func (q *QueryBuilder) GetEnabledModels() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0),
			coalesce(internal, false), coalesce(credential_id, ''),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
		FROM models WHERE enabled = true ORDER BY name`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0),
		coalesce(internal, 0), coalesce(credential_id, ''),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, '')
	FROM models WHERE enabled = 1 ORDER BY name`
}
