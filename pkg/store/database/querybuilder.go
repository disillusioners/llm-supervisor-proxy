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

// InsertModel returns the appropriate INSERT query for a model.
//
// M-1 shadow write (per technical-analysis.md §API Contract
// store-layer write-path, lines 905-934, and Round-2 reviewer punch-list
// #12): the statement writes BOTH `credentials_json` (the new
// ordered, weighted JSON list) AND `credential_id` (the derived
// shadow, bound from `model.Credentials[0].CredentialID` evaluated
// in Go) in the SAME INSERT. The `json_extract` form lives ONLY in
// the down-migration. Until migration 029 drops the shadow column,
// every write to `credentials_json` MUST also write `credential_id`;
// this statement enforces that contractually.
func (q *QueryBuilder) InsertModel() string {
	if q.dialect == PostgreSQL {
		return `INSERT INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
			internal, credentials_json, credential_id, internal_base_url, internal_model, release_stream_chunk_deadline,
			peak_hour_enabled, peak_hour_start, peak_hour_end, peak_hour_timezone, peak_hour_model,
			secondary_upstream_model, exclude_from_ultimate_switching)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				enabled = EXCLUDED.enabled,
				fallback_chain_json = EXCLUDED.fallback_chain_json,
				truncate_params_json = EXCLUDED.truncate_params_json,
				internal = EXCLUDED.internal,
				credentials_json = EXCLUDED.credentials_json,
				credential_id = EXCLUDED.credential_id,
				internal_base_url = EXCLUDED.internal_base_url,
				internal_model = EXCLUDED.internal_model,
				release_stream_chunk_deadline = EXCLUDED.release_stream_chunk_deadline,
				peak_hour_enabled = EXCLUDED.peak_hour_enabled,
				peak_hour_start = EXCLUDED.peak_hour_start,
				peak_hour_end = EXCLUDED.peak_hour_end,
				peak_hour_timezone = EXCLUDED.peak_hour_timezone,
				peak_hour_model = EXCLUDED.peak_hour_model,
				secondary_upstream_model = EXCLUDED.secondary_upstream_model,
				exclude_from_ultimate_switching = EXCLUDED.exclude_from_ultimate_switching,
				updated_at = NOW()`
	}
	return `INSERT OR REPLACE INTO models (id, name, enabled, fallback_chain_json, truncate_params_json,
		internal, credentials_json, credential_id, internal_base_url, internal_model, release_stream_chunk_deadline,
		peak_hour_enabled, peak_hour_start, peak_hour_end, peak_hour_timezone, peak_hour_model,
		secondary_upstream_model, exclude_from_ultimate_switching)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

// UpdateModel returns the appropriate UPDATE query for a model.
//
// M-1 shadow write (see InsertModel doc): writes BOTH
// `credentials_json` AND `credential_id` in the same UPDATE
// statement. `credential_id` is bound from `model.Credentials[0].CredentialID`
// computed in Go (NOT SQL-side via json_extract).
func (q *QueryBuilder) UpdateModel() string {
	if q.dialect == PostgreSQL {
		return `UPDATE models SET
			name = $1,
			enabled = $2,
			fallback_chain_json = $3,
			truncate_params_json = $4,
			internal = $5,
			credentials_json = $6,
			credential_id = $7,
			internal_base_url = $8,
			internal_model = $9,
			release_stream_chunk_deadline = $10,
			peak_hour_enabled = $11,
			peak_hour_start = $12,
			peak_hour_end = $13,
			peak_hour_timezone = $14,
			peak_hour_model = $15,
			secondary_upstream_model = $16,
			exclude_from_ultimate_switching = $17,
			updated_at = NOW()
		WHERE id = $18`
	}
	return `UPDATE models SET
			name = ?,
			enabled = ?,
			fallback_chain_json = ?,
			truncate_params_json = ?,
			internal = ?,
			credentials_json = ?,
			credential_id = ?,
			internal_base_url = ?,
			internal_model = ?,
			release_stream_chunk_deadline = ?,
			peak_hour_enabled = ?,
			peak_hour_start = ?,
			peak_hour_end = ?,
			peak_hour_timezone = ?,
			peak_hour_model = ?,
			secondary_upstream_model = ?,
			exclude_from_ultimate_switching = ?,
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

// GetModelByID returns the appropriate SELECT query for a model by ID
func (q *QueryBuilder) GetModelByID() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0),
			coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''),
			coalesce(exclude_from_ultimate_switching, false)
		FROM models WHERE id = $1`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0),
		coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''),
		coalesce(exclude_from_ultimate_switching, 0)
	FROM models WHERE id = ?`
}

// GetModelByName returns the appropriate SELECT query for a model by name
func (q *QueryBuilder) GetModelByName() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0),
			coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''),
			coalesce(exclude_from_ultimate_switching, false)
		FROM models WHERE name = $1`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0),
		coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''),
		coalesce(exclude_from_ultimate_switching, 0)
	FROM models WHERE name = ?`
}

// GetAllModels returns the appropriate SELECT query for all models
func (q *QueryBuilder) GetAllModels() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
            coalesce(release_stream_chunk_deadline, 0),
            coalesce(internal, false), coalesce(credentials_json, '[]'),
            coalesce(internal_base_url, ''), coalesce(internal_model, ''),
            peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
            coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
            coalesce(secondary_upstream_model, ''),
            coalesce(exclude_from_ultimate_switching, false)
        FROM models ORDER BY name`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
        coalesce(release_stream_chunk_deadline, 0),
        coalesce(internal, 0), coalesce(credentials_json, '[]'),
        coalesce(internal_base_url, ''), coalesce(internal_model, ''),
        peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
        coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
        coalesce(secondary_upstream_model, ''),
        coalesce(exclude_from_ultimate_switching, 0)
    FROM models ORDER BY name`
}

// GetEnabledModels returns the appropriate SELECT query for enabled models
func (q *QueryBuilder) GetEnabledModels() string {
	if q.dialect == PostgreSQL {
		return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
			coalesce(release_stream_chunk_deadline, 0),
			coalesce(internal, false), coalesce(credentials_json, '[]'),
			coalesce(internal_base_url, ''), coalesce(internal_model, ''),
			peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
			coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
			coalesce(secondary_upstream_model, ''),
			coalesce(exclude_from_ultimate_switching, false)
		FROM models WHERE enabled = true ORDER BY name`
	}
	return `SELECT id, name, enabled, fallback_chain_json, truncate_params_json, created_at, updated_at,
		coalesce(release_stream_chunk_deadline, 0),
		coalesce(internal, 0), coalesce(credentials_json, '[]'),
		coalesce(internal_base_url, ''), coalesce(internal_model, ''),
		peak_hour_enabled, coalesce(peak_hour_start, ''), coalesce(peak_hour_end, ''),
		coalesce(peak_hour_timezone, ''), coalesce(peak_hour_model, ''),
		coalesce(secondary_upstream_model, ''),
		coalesce(exclude_from_ultimate_switching, 0)
	FROM models WHERE enabled = 1 ORDER BY name`
}
