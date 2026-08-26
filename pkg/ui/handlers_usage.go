package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// UsageDataRow represents a row in the usage data array
type UsageDataRow struct {
	TokenID          string `json:"token_id"`
	TokenName        string `json:"token_name"`
	HourBucket       string `json:"hour_bucket"`
	RequestCount     int    `json:"request_count"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
}

// UsageTotals represents aggregated totals
type UsageTotals struct {
	RequestCount     int `json:"request_count"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UsageResponse represents the response for GET /fe/api/usage
type UsageResponse struct {
	TokenID string         `json:"token_id,omitempty"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	View    string         `json:"view"`
	Data    []UsageDataRow `json:"data"`
	Totals  UsageTotals    `json:"totals"`
}

// UsageTokenSummary represents a single token's summary
type UsageTokenSummary struct {
	TokenID       string `json:"token_id"`
	Name          string `json:"name"`
	TotalRequests int    `json:"total_requests"`
	TotalTokens   int    `json:"total_tokens"`
}

// UsageTokensResponse represents the response for GET /fe/api/usage/tokens
type UsageTokensResponse struct {
	Tokens []UsageTokenSummary `json:"tokens"`
}

// TokenSummaryDetail represents a single token in the summary
type TokenSummaryDetail struct {
	TokenID               string `json:"token_id"`
	Name                  string `json:"name"`
	TotalRequests         int    `json:"total_requests"`
	TotalPromptTokens     int    `json:"total_prompt_tokens"`
	TotalCompletionTokens int    `json:"total_completion_tokens"`
	TotalTokens           int    `json:"total_tokens"`
}

// GrandTotal represents the grand total with peak hour info
type GrandTotal struct {
	TotalRequests         int    `json:"total_requests"`
	TotalPromptTokens     int    `json:"total_prompt_tokens"`
	TotalCompletionTokens int    `json:"total_completion_tokens"`
	TotalTokens           int    `json:"total_tokens"`
	PeakHour              string `json:"peak_hour,omitempty"`
	PeakHourRequests      int    `json:"peak_hour_requests,omitempty"`
}

// UsageSummaryResponse represents the response for GET /fe/api/usage/summary
type UsageSummaryResponse struct {
	From       string               `json:"from"`
	To         string               `json:"to"`
	Tokens     []TokenSummaryDetail `json:"tokens"`
	GrandTotal GrandTotal           `json:"grand_total"`
}

// ModelUsageDataRow represents a row in the model usage data array
type ModelUsageDataRow struct {
	ModelID          string `json:"model_id"`
	ModelName        string `json:"model_name"`
	HourBucket       string `json:"hour_bucket"`
	RequestCount     int    `json:"request_count"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
}

// ModelSummary represents a model in the models list
type ModelSummary struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`
}

// UsageModelsResponse represents the response for GET /fe/api/usage/models
type UsageModelsResponse struct {
	From   string              `json:"from"`
	To     string              `json:"to"`
	View   string              `json:"view"`
	Data   []ModelUsageDataRow `json:"data"`
	Models []ModelSummary      `json:"models"`
}

// validateDateRange validates the from/to date range and returns an error message if invalid.
// The format should be "2006-01-02T15" or "2006-01-02T15:04" (e.g., "2024-03-15T14" or "2024-03-15T14:30").
func validateDateRange(from, to string) string {
	// Try parsing with hour-only format first
	fromTime, err := time.Parse("2006-01-02T15", from)
	if err != nil {
		// Try parsing with minute format
		fromTime, err = time.Parse("2006-01-02T15:04", from)
		if err != nil {
			return fmt.Sprintf("invalid 'from' date format: %s (expected YYYY-MM-DDTHH or YYYY-MM-DDTHH:MM)", from)
		}
	}
	toTime, err := time.Parse("2006-01-02T15", to)
	if err != nil {
		// Try parsing with minute format
		toTime, err = time.Parse("2006-01-02T15:04", to)
		if err != nil {
			return fmt.Sprintf("invalid 'to' date format: %s (expected YYYY-MM-DDTHH or YYYY-MM-DDTHH:MM)", to)
		}
	}

	// Check for inverted range
	if fromTime.After(toTime) {
		return "'from' date must be before or equal to 'to' date"
	}

	// Check max range (1 year)
	maxDuration := 365 * 24 * time.Hour
	if toTime.Sub(fromTime) > maxDuration {
		return "date range cannot exceed 1 year"
	}

	return ""
}

// handleUsage handles GET /fe/api/usage
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dbStore == nil {
		http.Error(w, "Database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	// Parse query params
	tokenID := r.URL.Query().Get("token_id")
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "hourly"
	}

	// Default time range: last 24 hours
	now := time.Now()
	to := toStr
	if to == "" {
		to = now.Format("2006-01-02T15")
	}
	from := fromStr
	if from == "" {
		from = now.Add(-24 * time.Hour).Format("2006-01-02T15")
	}

	// Validate date range if provided
	if fromStr != "" || toStr != "" {
		if errMsg := validateDateRange(from, to); errMsg != "" {
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
	}

	// Build query with dialect-aware placeholders
	var query string
	var args []interface{}

	dialect := s.dbStore.Dialect

	if view == "daily" {
		// Daily aggregation: group by token_id and day
		if tokenID != "" {
			// Query with token_id filter
			if dialect == database.PostgreSQL {
				query = `SELECT u.token_id, coalesce(t.name, ''), SUBSTR(u.hour_bucket, 1, 10), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = $1 AND u.hour_bucket >= $2 AND u.hour_bucket <= $3
				GROUP BY u.token_id, t.name, SUBSTR(u.hour_bucket, 1, 10)
				ORDER BY SUBSTR(u.hour_bucket, 1, 10)`
			} else {
				query = `SELECT u.token_id, coalesce(t.name, ''), SUBSTR(u.hour_bucket, 1, 10), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = ? AND u.hour_bucket >= ? AND u.hour_bucket <= ?
				GROUP BY u.token_id, t.name, SUBSTR(u.hour_bucket, 1, 10)
				ORDER BY SUBSTR(u.hour_bucket, 1, 10)`
			}
			args = []interface{}{tokenID, from, to}
		} else {
			// Query without token_id filter (all tokens)
			if dialect == database.PostgreSQL {
				query = `SELECT u.token_id, coalesce(t.name, ''), SUBSTR(u.hour_bucket, 1, 10), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= $1 AND u.hour_bucket <= $2
				GROUP BY u.token_id, t.name, SUBSTR(u.hour_bucket, 1, 10)
				ORDER BY SUBSTR(u.hour_bucket, 1, 10)`
			} else {
				query = `SELECT u.token_id, coalesce(t.name, ''), SUBSTR(u.hour_bucket, 1, 10), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= ? AND u.hour_bucket <= ?
				GROUP BY u.token_id, t.name, SUBSTR(u.hour_bucket, 1, 10)
				ORDER BY SUBSTR(u.hour_bucket, 1, 10)`
			}
			args = []interface{}{from, to}
		}
	} else {
		// Hourly view (default): return individual hour buckets
		if tokenID != "" {
			// Query with token_id filter
			if dialect == database.PostgreSQL {
				query = `SELECT u.token_id, coalesce(t.name, ''), u.hour_bucket, u.request_count, u.prompt_tokens, u.completion_tokens, u.total_tokens
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = $1 AND u.hour_bucket >= $2 AND u.hour_bucket <= $3
				ORDER BY u.hour_bucket`
			} else {
				query = `SELECT u.token_id, coalesce(t.name, ''), u.hour_bucket, u.request_count, u.prompt_tokens, u.completion_tokens, u.total_tokens
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = ? AND u.hour_bucket >= ? AND u.hour_bucket <= ?
				ORDER BY u.hour_bucket`
			}
			args = []interface{}{tokenID, from, to}
		} else {
			// Query without token_id filter (all tokens)
			if dialect == database.PostgreSQL {
				query = `SELECT u.token_id, coalesce(t.name, ''), u.hour_bucket, u.request_count, u.prompt_tokens, u.completion_tokens, u.total_tokens
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= $1 AND u.hour_bucket <= $2
				ORDER BY u.hour_bucket`
			} else {
				query = `SELECT u.token_id, coalesce(t.name, ''), u.hour_bucket, u.request_count, u.prompt_tokens, u.completion_tokens, u.total_tokens
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= ? AND u.hour_bucket <= ?
				ORDER BY u.hour_bucket`
			}
			args = []interface{}{from, to}
		}
	}

	rows, err := s.dbStore.DB.QueryContext(ctx, query, args...)
	if err != nil {
		http.Error(w, "Failed to query usage data", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var data []UsageDataRow
	var totals UsageTotals

	for rows.Next() {
		var row UsageDataRow
		if err := rows.Scan(&row.TokenID, &row.TokenName, &row.HourBucket, &row.RequestCount, &row.PromptTokens, &row.CompletionTokens, &row.TotalTokens); err != nil {
			http.Error(w, "Failed to scan usage row", http.StatusInternalServerError)
			return
		}
		data = append(data, row)
		totals.RequestCount += row.RequestCount
		totals.PromptTokens += row.PromptTokens
		totals.CompletionTokens += row.CompletionTokens
		totals.TotalTokens += row.TotalTokens
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Error iterating usage rows", http.StatusInternalServerError)
		return
	}

	// Ensure data is not nil
	if data == nil {
		data = []UsageDataRow{}
	}

	response := UsageResponse{
		TokenID: tokenID,
		From:    from,
		To:      to,
		View:    view,
		Data:    data,
		Totals:  totals,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleUsageTokens handles GET /fe/api/usage/tokens
func (s *Server) handleUsageTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dbStore == nil {
		http.Error(w, "Database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	// This query has no placeholders so it's the same for both dialects
	query := `SELECT u.token_id, coalesce(t.name, ''), SUM(u.request_count), SUM(u.total_tokens)
		FROM token_hourly_usage u
		LEFT JOIN auth_tokens t ON u.token_id = t.id
		GROUP BY u.token_id, t.name
		ORDER BY u.token_id`

	rows, err := s.dbStore.DB.QueryContext(ctx, query)
	if err != nil {
		http.Error(w, "Failed to query token usage", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tokens []UsageTokenSummary

	for rows.Next() {
		var token UsageTokenSummary
		if err := rows.Scan(&token.TokenID, &token.Name, &token.TotalRequests, &token.TotalTokens); err != nil {
			http.Error(w, "Failed to scan token row", http.StatusInternalServerError)
			return
		}
		tokens = append(tokens, token)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Error iterating token rows", http.StatusInternalServerError)
		return
	}

	// Ensure tokens is not nil
	if tokens == nil {
		tokens = []UsageTokenSummary{}
	}

	response := UsageTokensResponse{
		Tokens: tokens,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleUsageSummary handles GET /fe/api/usage/summary
func (s *Server) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dbStore == nil {
		http.Error(w, "Database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	// Parse query params
	tokenID := r.URL.Query().Get("token_id")
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	// Default time range: last 24 hours
	now := time.Now()
	to := toStr
	if to == "" {
		to = now.Format("2006-01-02T15")
	}
	from := fromStr
	if from == "" {
		from = now.Add(-24 * time.Hour).Format("2006-01-02T15")
	}

	// Validate date range if provided
	if fromStr != "" || toStr != "" {
		if errMsg := validateDateRange(from, to); errMsg != "" {
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
	}

	dialect := s.dbStore.Dialect

	// Build query for per-token summary
	var query string
	var args []interface{}

	if tokenID != "" {
		// Query with token_id filter
		if dialect == database.PostgreSQL {
			query = `SELECT u.token_id, coalesce(t.name, ''), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = $1 AND u.hour_bucket >= $2 AND u.hour_bucket <= $3
				GROUP BY u.token_id, t.name
				ORDER BY u.token_id`
		} else {
			query = `SELECT u.token_id, coalesce(t.name, ''), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.token_id = ? AND u.hour_bucket >= ? AND u.hour_bucket <= ?
				GROUP BY u.token_id, t.name
				ORDER BY u.token_id`
		}
		args = []interface{}{tokenID, from, to}
	} else {
		// Query without token_id filter (all tokens)
		if dialect == database.PostgreSQL {
			query = `SELECT u.token_id, coalesce(t.name, ''), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= $1 AND u.hour_bucket <= $2
				GROUP BY u.token_id, t.name
				ORDER BY u.token_id`
		} else {
			query = `SELECT u.token_id, coalesce(t.name, ''), SUM(u.request_count), SUM(u.prompt_tokens), SUM(u.completion_tokens), SUM(u.total_tokens)
				FROM token_hourly_usage u
				LEFT JOIN auth_tokens t ON u.token_id = t.id
				WHERE u.hour_bucket >= ? AND u.hour_bucket <= ?
				GROUP BY u.token_id, t.name
				ORDER BY u.token_id`
		}
		args = []interface{}{from, to}
	}

	rows, err := s.dbStore.DB.QueryContext(ctx, query, args...)
	if err != nil {
		http.Error(w, "Failed to query usage summary", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tokens []TokenSummaryDetail
	var grandTotal GrandTotal

	for rows.Next() {
		var detail TokenSummaryDetail
		if err := rows.Scan(&detail.TokenID, &detail.Name, &detail.TotalRequests, &detail.TotalPromptTokens, &detail.TotalCompletionTokens, &detail.TotalTokens); err != nil {
			http.Error(w, "Failed to scan summary row", http.StatusInternalServerError)
			return
		}
		tokens = append(tokens, detail)
		grandTotal.TotalRequests += detail.TotalRequests
		grandTotal.TotalPromptTokens += detail.TotalPromptTokens
		grandTotal.TotalCompletionTokens += detail.TotalCompletionTokens
		grandTotal.TotalTokens += detail.TotalTokens
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Error iterating summary rows", http.StatusInternalServerError)
		return
	}

	// Query for peak hour
	var peakHourQuery string
	var peakHourArgs []interface{}

	if tokenID != "" {
		if dialect == database.PostgreSQL {
			peakHourQuery = `SELECT hour_bucket, SUM(request_count) as cnt
				FROM token_hourly_usage
				WHERE token_id = $1 AND hour_bucket >= $2 AND hour_bucket <= $3
				GROUP BY hour_bucket
				ORDER BY cnt DESC
				LIMIT 1`
		} else {
			peakHourQuery = `SELECT hour_bucket, SUM(request_count) as cnt
				FROM token_hourly_usage
				WHERE token_id = ? AND hour_bucket >= ? AND hour_bucket <= ?
				GROUP BY hour_bucket
				ORDER BY cnt DESC
				LIMIT 1`
		}
		peakHourArgs = []interface{}{tokenID, from, to}
	} else {
		if dialect == database.PostgreSQL {
			peakHourQuery = `SELECT hour_bucket, SUM(request_count) as cnt
				FROM token_hourly_usage
				WHERE hour_bucket >= $1 AND hour_bucket <= $2
				GROUP BY hour_bucket
				ORDER BY cnt DESC
				LIMIT 1`
		} else {
			peakHourQuery = `SELECT hour_bucket, SUM(request_count) as cnt
				FROM token_hourly_usage
				WHERE hour_bucket >= ? AND hour_bucket <= ?
				GROUP BY hour_bucket
				ORDER BY cnt DESC
				LIMIT 1`
		}
		peakHourArgs = []interface{}{from, to}
	}

	var peakHour string
	var peakHourRequests int

	err = s.dbStore.DB.QueryRowContext(ctx, peakHourQuery, peakHourArgs...).Scan(&peakHour, &peakHourRequests)
	if err == nil {
		grandTotal.PeakHour = peakHour
		grandTotal.PeakHourRequests = peakHourRequests
	}
	// Peak hour is optional - errors are silently ignored

	// Ensure tokens is not nil
	if tokens == nil {
		tokens = []TokenSummaryDetail{}
	}

	response := UsageSummaryResponse{
		From:       from,
		To:         to,
		Tokens:     tokens,
		GrandTotal: grandTotal,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleUsageModels handles GET /fe/api/usage/models
func (s *Server) handleUsageModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dbStore == nil {
		http.Error(w, "Database not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	// Parse query params
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "hourly"
	}

	// Default time range: last 24 hours
	now := time.Now()
	to := toStr
	if to == "" {
		to = now.Format("2006-01-02T15")
	}
	from := fromStr
	if from == "" {
		from = now.Add(-24 * time.Hour).Format("2006-01-02T15")
	}

	// Validate date range if provided
	if fromStr != "" || toStr != "" {
		if errMsg := validateDateRange(from, to); errMsg != "" {
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
	}

	// Build query with dialect-aware placeholders
	var query string
	var args []interface{}

	dialect := s.dbStore.Dialect

	if view == "daily" {
		// Daily aggregation: group by model_id and day
		if dialect == database.PostgreSQL {
			query = `SELECT model_id, SUBSTR(hour_bucket, 1, 10), SUM(request_count), SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens)
			FROM model_hourly_usage
			WHERE hour_bucket >= $1 AND hour_bucket <= $2
			GROUP BY model_id, SUBSTR(hour_bucket, 1, 10)
			ORDER BY model_id, SUBSTR(hour_bucket, 1, 10)`
		} else {
			query = `SELECT model_id, SUBSTR(hour_bucket, 1, 10), SUM(request_count), SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens)
			FROM model_hourly_usage
			WHERE hour_bucket >= ? AND hour_bucket <= ?
			GROUP BY model_id, SUBSTR(hour_bucket, 1, 10)
			ORDER BY model_id, SUBSTR(hour_bucket, 1, 10)`
		}
		args = []interface{}{from, to}
	} else {
		// Hourly view (default): return individual hour buckets
		if dialect == database.PostgreSQL {
			query = `SELECT model_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
			FROM model_hourly_usage
			WHERE hour_bucket >= $1 AND hour_bucket <= $2
			ORDER BY model_id, hour_bucket`
		} else {
			query = `SELECT model_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
			FROM model_hourly_usage
			WHERE hour_bucket >= ? AND hour_bucket <= ?
			ORDER BY model_id, hour_bucket`
		}
		args = []interface{}{from, to}
	}

	rows, err := s.dbStore.DB.QueryContext(ctx, query, args...)
	if err != nil {
		http.Error(w, "Failed to query model usage data", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var data []ModelUsageDataRow
	modelIDs := make(map[string]bool)

	for rows.Next() {
		var row ModelUsageDataRow
		if err := rows.Scan(&row.ModelID, &row.HourBucket, &row.RequestCount, &row.PromptTokens, &row.CompletionTokens, &row.TotalTokens); err != nil {
			http.Error(w, "Failed to scan model usage row", http.StatusInternalServerError)
			return
		}
		// Look up model name from models config
		if modelConfig := s.modelsConfig.GetModel(row.ModelID); modelConfig != nil {
			row.ModelName = modelConfig.Name
		} else {
			row.ModelName = row.ModelID
		}
		data = append(data, row)
		modelIDs[row.ModelID] = true
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Error iterating model usage rows", http.StatusInternalServerError)
		return
	}

	// Build models list
	var models []ModelSummary
	for modelID := range modelIDs {
		modelSummary := ModelSummary{
			ModelID: modelID,
		}
		if modelConfig := s.modelsConfig.GetModel(modelID); modelConfig != nil {
			modelSummary.ModelName = modelConfig.Name
		} else {
			modelSummary.ModelName = modelID
		}
		models = append(models, modelSummary)
	}

	// Ensure data is not nil
	if data == nil {
		data = []ModelUsageDataRow{}
	}
	// Ensure models is not nil
	if models == nil {
		models = []ModelSummary{}
	}

	response := UsageModelsResponse{
		From:   from,
		To:     to,
		View:   view,
		Data:   data,
		Models: models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
