package usage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// HourlyUsageRow represents a row from the token_hourly_usage table
type HourlyUsageRow struct {
	TokenID          string
	HourBucket       string
	RequestCount     int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Counter tracks token usage statistics per hour bucket
type Counter struct {
	db      *sql.DB
	dialect database.Dialect
	qb      *database.QueryBuilder
}

// NewCounter creates a new usage counter for the given database
func NewCounter(db *sql.DB, dialect database.Dialect) *Counter {
	return &Counter{
		db:      db,
		dialect: dialect,
		qb:      database.NewQueryBuilder(dialect),
	}
}

// Increment increments usage counters for a token within an hour bucket
// Uses UPSERT to atomically add counts to the existing values
func (c *Counter) Increment(ctx context.Context, tokenID, hourBucket string, reqCount, promptTok, completionTok, totalTok int) error {
	var query string
	if c.dialect == database.PostgreSQL {
		query = `INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (token_id, hour_bucket) DO UPDATE SET
				request_count = token_hourly_usage.request_count + EXCLUDED.request_count,
				prompt_tokens = token_hourly_usage.prompt_tokens + EXCLUDED.prompt_tokens,
				completion_tokens = token_hourly_usage.completion_tokens + EXCLUDED.completion_tokens,
				total_tokens = token_hourly_usage.total_tokens + EXCLUDED.total_tokens`
	} else {
		query = `INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (token_id, hour_bucket) DO UPDATE SET
				request_count = request_count + excluded.request_count,
				prompt_tokens = prompt_tokens + excluded.prompt_tokens,
				completion_tokens = completion_tokens + excluded.completion_tokens,
				total_tokens = total_tokens + excluded.total_tokens`
	}

	args := []interface{}{tokenID, hourBucket, reqCount, promptTok, completionTok, totalTok}

	_, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to increment usage: %w", err)
	}
	return nil
}

// GetTokenUsage retrieves usage statistics for a token within a time range
func (c *Counter) GetTokenUsage(ctx context.Context, tokenID, fromHour, toHour string) ([]HourlyUsageRow, error) {
	var query string
	if c.dialect == database.PostgreSQL {
		query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
			FROM token_hourly_usage
			WHERE token_id = $1 AND hour_bucket >= $2 AND hour_bucket <= $3
			ORDER BY hour_bucket`
	} else {
		query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
			FROM token_hourly_usage
			WHERE token_id = ? AND hour_bucket >= ? AND hour_bucket <= ?
			ORDER BY hour_bucket`
	}

	args := []interface{}{tokenID, fromHour, toHour}

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query token usage: %w", err)
	}
	defer rows.Close()

	var result []HourlyUsageRow
	for rows.Next() {
		var row HourlyUsageRow
		if err := rows.Scan(&row.TokenID, &row.HourBucket, &row.RequestCount, &row.PromptTokens, &row.CompletionTokens, &row.TotalTokens); err != nil {
			return nil, fmt.Errorf("failed to scan usage row: %w", err)
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating usage rows: %w", err)
	}

	return result, nil
}
