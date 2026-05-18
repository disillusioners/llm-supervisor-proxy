package usage

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// setupTestDB creates an in-memory SQLite database with the token_hourly_usage table
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Create the table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS token_hourly_usage (
		token_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (token_id, hour_bucket)
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCounter_Increment(t *testing.T) {
	db := setupTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	t.Run("increment creates new row", func(t *testing.T) {
		err := counter.Increment(ctx, "token1", "2024-01-01T10:00", 1, 100, 50, 150)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		// Verify the row was created
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-01T10:00", "2024-01-01T10:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 1 {
			t.Errorf("RequestCount = %d, want 1", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 100 {
			t.Errorf("PromptTokens = %d, want 100", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 50 {
			t.Errorf("CompletionTokens = %d, want 50", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 150 {
			t.Errorf("TotalTokens = %d, want 150", rows[0].TotalTokens)
		}
	})

	t.Run("increment with existing row increments counts", func(t *testing.T) {
		// Increment again for the same token and hour
		err := counter.Increment(ctx, "token1", "2024-01-01T10:00", 1, 200, 100, 300)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		// Verify the counts were accumulated
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-01T10:00", "2024-01-01T10:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 2 {
			t.Errorf("RequestCount = %d, want 2", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 300 {
			t.Errorf("PromptTokens = %d, want 300 (100+200)", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 150 {
			t.Errorf("CompletionTokens = %d, want 150 (50+100)", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 450 {
			t.Errorf("TotalTokens = %d, want 450 (150+300)", rows[0].TotalTokens)
		}
	})

	t.Run("multiple increments accumulate correctly", func(t *testing.T) {
		tokenID := "token2"
		hourBucket := "2024-01-01T11:00"

		// First increment
		err := counter.Increment(ctx, tokenID, hourBucket, 1, 50, 25, 75)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		// Second increment
		err = counter.Increment(ctx, tokenID, hourBucket, 1, 75, 30, 105)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		// Third increment
		err = counter.Increment(ctx, tokenID, hourBucket, 1, 100, 40, 140)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		// Verify accumulated counts
		rows, err := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 3 {
			t.Errorf("RequestCount = %d, want 3", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 225 {
			t.Errorf("PromptTokens = %d, want 225 (50+75+100)", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 95 {
			t.Errorf("CompletionTokens = %d, want 95 (25+30+40)", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 320 {
			t.Errorf("TotalTokens = %d, want 320 (75+105+140)", rows[0].TotalTokens)
		}
	})

	t.Run("increment with zero tokens (just request count)", func(t *testing.T) {
		tokenID := "token3"
		hourBucket := "2024-01-01T12:00"

		err := counter.Increment(ctx, tokenID, hourBucket, 1, 0, 0, 0)
		if err != nil {
			t.Fatalf("Increment() error = %v", err)
		}

		rows, err := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 1 {
			t.Errorf("RequestCount = %d, want 1", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 0 {
			t.Errorf("PromptTokens = %d, want 0", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 0 {
			t.Errorf("CompletionTokens = %d, want 0", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 0 {
			t.Errorf("TotalTokens = %d, want 0", rows[0].TotalTokens)
		}
	})
}

// setupModelUsageTestDB creates an in-memory SQLite database with the model_hourly_usage table
func setupModelUsageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Create the table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS model_hourly_usage (
		model_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (model_id, hour_bucket)
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// =============================================================================
// Part A: IncrementModelUsage UPSERT Tests
// =============================================================================

func TestCounter_IncrementModelUsage(t *testing.T) {
	db := setupModelUsageTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	t.Run("first insert creates row with correct counts", func(t *testing.T) {
		err := counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T10:00", 1, 100, 50, 150)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].ModelID != "gpt-4" {
			t.Errorf("ModelID = %s, want gpt-4", rows[0].ModelID)
		}
		if rows[0].RequestCount != 1 {
			t.Errorf("RequestCount = %d, want 1", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 100 {
			t.Errorf("PromptTokens = %d, want 100", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 50 {
			t.Errorf("CompletionTokens = %d, want 50", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 150 {
			t.Errorf("TotalTokens = %d, want 150", rows[0].TotalTokens)
		}
	})

	t.Run("second insert with same model_id and hour_bucket increments counts", func(t *testing.T) {
		err := counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T10:00", 1, 200, 100, 300)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 2 {
			t.Errorf("RequestCount = %d, want 2", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 300 {
			t.Errorf("PromptTokens = %d, want 300 (100+200)", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 150 {
			t.Errorf("CompletionTokens = %d, want 150 (50+100)", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 450 {
			t.Errorf("TotalTokens = %d, want 450 (150+300)", rows[0].TotalTokens)
		}
	})

	t.Run("different model_id creates separate rows", func(t *testing.T) {
		err := counter.IncrementModelUsage(ctx, "gpt-3.5", "2024-01-01T10:00", 1, 50, 25, 75)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}

		// Find gpt-4
		var gpt4Row *ModelHourlyUsageRow
		var gpt35Row *ModelHourlyUsageRow
		for i := range rows {
			if rows[i].ModelID == "gpt-4" {
				gpt4Row = &rows[i]
			} else if rows[i].ModelID == "gpt-3.5" {
				gpt35Row = &rows[i]
			}
		}
		if gpt4Row == nil {
			t.Fatal("gpt-4 row not found")
		}
		if gpt35Row == nil {
			t.Fatal("gpt-3.5 row not found")
		}

		// gpt-4 should have its previous counts (2 requests)
		if gpt4Row.RequestCount != 2 {
			t.Errorf("gpt-4 RequestCount = %d, want 2", gpt4Row.RequestCount)
		}
		// gpt-3.5 should have new counts
		if gpt35Row.RequestCount != 1 {
			t.Errorf("gpt-3.5 RequestCount = %d, want 1", gpt35Row.RequestCount)
		}
	})

	t.Run("different hour_bucket creates separate rows", func(t *testing.T) {
		err := counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T11:00", 1, 100, 50, 150)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T12:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}

		// Find gpt-4 at 10:00 and 11:00
		var hour10Row *ModelHourlyUsageRow
		var hour11Row *ModelHourlyUsageRow
		for i := range rows {
			if rows[i].ModelID == "gpt-4" && rows[i].HourBucket == "2024-01-01T10:00" {
				hour10Row = &rows[i]
			} else if rows[i].ModelID == "gpt-4" && rows[i].HourBucket == "2024-01-01T11:00" {
				hour11Row = &rows[i]
			}
		}
		if hour10Row == nil {
			t.Fatal("gpt-4 hour10 row not found")
		}
		if hour11Row == nil {
			t.Fatal("gpt-4 hour11 row not found")
		}

		if hour10Row.RequestCount != 2 {
			t.Errorf("gpt-4 hour10 RequestCount = %d, want 2", hour10Row.RequestCount)
		}
		if hour11Row.RequestCount != 1 {
			t.Errorf("gpt-4 hour11 RequestCount = %d, want 1", hour11Row.RequestCount)
		}
	})

	t.Run("zero token values are handled correctly", func(t *testing.T) {
		err := counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T12:00", 1, 0, 0, 0)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, "2024-01-01T12:00", "2024-01-01T12:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 1 {
			t.Errorf("RequestCount = %d, want 1", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 0 {
			t.Errorf("PromptTokens = %d, want 0", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 0 {
			t.Errorf("CompletionTokens = %d, want 0", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 0 {
			t.Errorf("TotalTokens = %d, want 0", rows[0].TotalTokens)
		}
	})

	t.Run("multiple sequential increments accumulate correctly", func(t *testing.T) {
		modelID := "claude-3"
		hourBucket := "2024-01-01T13:00"

		err := counter.IncrementModelUsage(ctx, modelID, hourBucket, 1, 50, 25, 75)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}
		err = counter.IncrementModelUsage(ctx, modelID, hourBucket, 1, 75, 30, 105)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}
		err = counter.IncrementModelUsage(ctx, modelID, hourBucket, 1, 100, 40, 140)
		if err != nil {
			t.Fatalf("IncrementModelUsage() error = %v", err)
		}

		rows, err := counter.GetModelUsage(ctx, hourBucket, hourBucket)
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].RequestCount != 3 {
			t.Errorf("RequestCount = %d, want 3", rows[0].RequestCount)
		}
		if rows[0].PromptTokens != 225 {
			t.Errorf("PromptTokens = %d, want 225 (50+75+100)", rows[0].PromptTokens)
		}
		if rows[0].CompletionTokens != 95 {
			t.Errorf("CompletionTokens = %d, want 95 (25+30+40)", rows[0].CompletionTokens)
		}
		if rows[0].TotalTokens != 320 {
			t.Errorf("TotalTokens = %d, want 320 (75+105+140)", rows[0].TotalTokens)
		}
	})
}

// =============================================================================
// Part B: GetModelUsage Query Tests
// =============================================================================

func TestCounter_GetModelUsage(t *testing.T) {
	db := setupModelUsageTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	// Setup test data
	testData := []struct {
		modelID     string
		hourBucket  string
		reqCount    int
		promptTok   int
		compTok     int
		totalTok    int
	}{
		{"gpt-4", "2024-01-01T10:00", 1, 100, 50, 150},
		{"gpt-4", "2024-01-01T11:00", 2, 200, 100, 300},
		{"gpt-4", "2024-01-01T12:00", 3, 300, 150, 450},
		{"gpt-3.5", "2024-01-01T10:00", 1, 50, 25, 75},
		{"gpt-3.5", "2024-01-01T11:00", 1, 60, 30, 90},
		{"claude-3", "2024-01-02T10:00", 5, 500, 250, 750},
	}

	for _, td := range testData {
		err := counter.IncrementModelUsage(ctx, td.modelID, td.hourBucket, td.reqCount, td.promptTok, td.compTok, td.totalTok)
		if err != nil {
			t.Fatalf("Setup: IncrementModelUsage() error = %v", err)
		}
	}

	t.Run("returns data within date range", func(t *testing.T) {
		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T12:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		// Should return gpt-4 (3 hours) + gpt-3.5 (2 hours) = 5 rows
		if len(rows) != 5 {
			t.Errorf("expected 5 rows, got %d", len(rows))
		}
	})

	t.Run("filters out data outside date range", func(t *testing.T) {
		// Only get data from hour 10:00 to 11:00
		rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T11:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		// Should return gpt-4 (2 hours) + gpt-3.5 (2 hours) = 4 rows
		if len(rows) != 4 {
			t.Errorf("expected 4 rows, got %d", len(rows))
		}

		// Verify the hour buckets
		for _, row := range rows {
			if row.HourBucket == "2024-01-01T12:00" {
				t.Errorf("should not include data from 12:00 hour")
			}
		}
	})

	t.Run("returns empty slice for date range with no data", func(t *testing.T) {
		rows, err := counter.GetModelUsage(ctx, "2024-01-03T00:00", "2024-01-03T23:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected 0 rows, got %d", len(rows))
		}
	})

	t.Run("returns data sorted by model_id then hour_bucket", func(t *testing.T) {
		rows, err := counter.GetModelUsage(ctx, "2024-01-01T09:00", "2024-01-01T13:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 5 {
			t.Fatalf("expected 5 rows, got %d", len(rows))
		}

		// Verify ordering: gpt-3.5 comes before gpt-4 alphabetically
		if rows[0].ModelID != "gpt-3.5" {
			t.Errorf("rows[0].ModelID = %s, want gpt-3.5", rows[0].ModelID)
		}
		if rows[1].ModelID != "gpt-3.5" {
			t.Errorf("rows[1].ModelID = %s, want gpt-3.5", rows[1].ModelID)
		}
		if rows[2].ModelID != "gpt-4" {
			t.Errorf("rows[2].ModelID = %s, want gpt-4", rows[2].ModelID)
		}

		// Within same model, hours should be ordered
		if rows[0].HourBucket != "2024-01-01T10:00" {
			t.Errorf("rows[0].HourBucket = %s, want 2024-01-01T10:00", rows[0].HourBucket)
		}
		if rows[1].HourBucket != "2024-01-01T11:00" {
			t.Errorf("rows[1].HourBucket = %s, want 2024-01-01T11:00", rows[1].HourBucket)
		}
	})

	t.Run("single hour range returns single row per model", func(t *testing.T) {
		rows, err := counter.GetModelUsage(ctx, "2024-01-01T11:00", "2024-01-01T11:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		if len(rows) != 2 {
			t.Errorf("expected 2 rows (gpt-4 and gpt-3.5), got %d", len(rows))
		}

		for _, row := range rows {
			if row.HourBucket != "2024-01-01T11:00" {
				t.Errorf("HourBucket = %s, want 2024-01-01T11:00", row.HourBucket)
			}
		}
	})

	t.Run("multiple models returned correctly", func(t *testing.T) {
		rows, err := counter.GetModelUsage(ctx, "2024-01-01T00:00", "2024-01-03T23:00")
		if err != nil {
			t.Fatalf("GetModelUsage() error = %v", err)
		}
		// Should return 6 rows total (5 from Jan 1 + 1 from Jan 2)
		if len(rows) != 6 {
			t.Errorf("expected 6 rows, got %d", len(rows))
		}

		// Count unique models
		modelSet := make(map[string]bool)
		for _, row := range rows {
			modelSet[row.ModelID] = true
		}
		if len(modelSet) != 3 {
			t.Errorf("expected 3 unique models, got %d", len(modelSet))
		}
	})
}

// =============================================================================
// Edge Case Tests for Model Usage
// =============================================================================

func TestCounter_IncrementModelUsage_LargeTokenCounts(t *testing.T) {
	db := setupModelUsageTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	// Test with very large token counts (no overflow)
	largePromptTokens := 1_000_000_000  // 1 billion
	largeCompletionTokens := 500_000_000 // 500 million
	largeTotalTokens := 1_500_000_000   // 1.5 billion

	err := counter.IncrementModelUsage(ctx, "large-model", "2024-01-01T10:00", 100, largePromptTokens, largeCompletionTokens, largeTotalTokens)
	if err != nil {
		t.Fatalf("IncrementModelUsage() error = %v", err)
	}

	rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
	if err != nil {
		t.Fatalf("GetModelUsage() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].PromptTokens != largePromptTokens {
		t.Errorf("PromptTokens = %d, want %d", rows[0].PromptTokens, largePromptTokens)
	}
	if rows[0].CompletionTokens != largeCompletionTokens {
		t.Errorf("CompletionTokens = %d, want %d", rows[0].CompletionTokens, largeCompletionTokens)
	}
	if rows[0].TotalTokens != largeTotalTokens {
		t.Errorf("TotalTokens = %d, want %d", rows[0].TotalTokens, largeTotalTokens)
	}

	// Accumulate more large values
	err = counter.IncrementModelUsage(ctx, "large-model", "2024-01-01T10:00", 100, largePromptTokens, largeCompletionTokens, largeTotalTokens)
	if err != nil {
		t.Fatalf("IncrementModelUsage() error = %v", err)
	}

	rows, err = counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
	if err != nil {
		t.Fatalf("GetModelUsage() error = %v", err)
	}
	// Should have accumulated
	if rows[0].PromptTokens != 2*largePromptTokens {
		t.Errorf("PromptTokens = %d, want %d", rows[0].PromptTokens, 2*largePromptTokens)
	}
}

func TestCounter_GetModelUsage_EmptyResultReturnsEmptySlice(t *testing.T) {
	db := setupModelUsageTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	// No data inserted
	rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T12:00")
	if err != nil {
		t.Fatalf("GetModelUsage() error = %v", err)
	}
	// The function returns nil when no rows found - this is Go's default behavior
	// We verify it's either nil or empty slice
	if rows != nil && len(rows) != 0 {
		t.Errorf("expected nil or empty slice, got %d rows", len(rows))
	}
}

func TestCounter_IncrementModelUsage_DifferentModelsIndependent(t *testing.T) {
	db := setupModelUsageTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	// Increment gpt-4 twice at same hour
	err := counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T10:00", 1, 100, 50, 150)
	if err != nil {
		t.Fatalf("IncrementModelUsage() error = %v", err)
	}
	err = counter.IncrementModelUsage(ctx, "gpt-4", "2024-01-01T10:00", 1, 200, 100, 300)
	if err != nil {
		t.Fatalf("IncrementModelUsage() error = %v", err)
	}

	// Increment claude-3 once at same hour
	err = counter.IncrementModelUsage(ctx, "claude-3", "2024-01-01T10:00", 1, 75, 40, 115)
	if err != nil {
		t.Fatalf("IncrementModelUsage() error = %v", err)
	}

	rows, err := counter.GetModelUsage(ctx, "2024-01-01T10:00", "2024-01-01T10:00")
	if err != nil {
		t.Fatalf("GetModelUsage() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	for _, row := range rows {
		if row.ModelID == "gpt-4" {
			if row.RequestCount != 2 {
				t.Errorf("gpt-4 RequestCount = %d, want 2", row.RequestCount)
			}
			if row.PromptTokens != 300 {
				t.Errorf("gpt-4 PromptTokens = %d, want 300", row.PromptTokens)
			}
		} else if row.ModelID == "claude-3" {
			if row.RequestCount != 1 {
				t.Errorf("claude-3 RequestCount = %d, want 1", row.RequestCount)
			}
			if row.PromptTokens != 75 {
				t.Errorf("claude-3 PromptTokens = %d, want 75", row.PromptTokens)
			}
		}
	}
}

func TestCounter_GetTokenUsage(t *testing.T) {
	db := setupTestDB(t)
	counter := NewCounter(db, database.SQLite)
	ctx := context.Background()

	// Setup test data
	testData := []struct {
		tokenID    string
		hourBucket string
		reqCount   int
		promptTok  int
		compTok    int
		totalTok   int
	}{
		{"token1", "2024-01-01T10:00", 1, 100, 50, 150},
		{"token1", "2024-01-01T11:00", 2, 200, 100, 300},
		{"token1", "2024-01-01T12:00", 3, 300, 150, 450},
		{"token2", "2024-01-01T10:00", 1, 50, 25, 75},
		{"token2", "2024-01-01T11:00", 1, 60, 30, 90},
	}

	for _, td := range testData {
		err := counter.Increment(ctx, td.tokenID, td.hourBucket, td.reqCount, td.promptTok, td.compTok, td.totalTok)
		if err != nil {
			t.Fatalf("Setup: Increment() error = %v", err)
		}
	}

	t.Run("returns correct data for token", func(t *testing.T) {
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-01T10:00", "2024-01-01T12:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("expected 3 rows, got %d", len(rows))
		}
	})

	t.Run("returns correct data with date range filtering", func(t *testing.T) {
		// Only get data from hour 10:00 to 11:00
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-01T10:00", "2024-01-01T11:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 2 {
			t.Errorf("expected 2 rows, got %d", len(rows))
		}

		// Verify the hour buckets
		if rows[0].HourBucket != "2024-01-01T10:00" {
			t.Errorf("rows[0].HourBucket = %s, want 2024-01-01T10:00", rows[0].HourBucket)
		}
		if rows[1].HourBucket != "2024-01-01T11:00" {
			t.Errorf("rows[1].HourBucket = %s, want 2024-01-01T11:00", rows[1].HourBucket)
		}
	})

	t.Run("returns empty slice for non-existent token", func(t *testing.T) {
		rows, err := counter.GetTokenUsage(ctx, "non-existent", "2024-01-01T10:00", "2024-01-01T12:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected 0 rows, got %d", len(rows))
		}
	})

	t.Run("returns empty slice for date range with no data", func(t *testing.T) {
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-02T00:00", "2024-01-02T23:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected 0 rows, got %d", len(rows))
		}
	})

	t.Run("returns data sorted by hour_bucket", func(t *testing.T) {
		rows, err := counter.GetTokenUsage(ctx, "token2", "2024-01-01T09:00", "2024-01-01T13:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}

		// Verify ordering
		if rows[0].HourBucket != "2024-01-01T10:00" {
			t.Errorf("rows[0].HourBucket = %s, want 2024-01-01T10:00", rows[0].HourBucket)
		}
		if rows[1].HourBucket != "2024-01-01T11:00" {
			t.Errorf("rows[1].HourBucket = %s, want 2024-01-01T11:00", rows[1].HourBucket)
		}

		// Verify values
		if rows[0].PromptTokens != 50 {
			t.Errorf("rows[0].PromptTokens = %d, want 50", rows[0].PromptTokens)
		}
		if rows[1].PromptTokens != 60 {
			t.Errorf("rows[1].PromptTokens = %d, want 60", rows[1].PromptTokens)
		}
	})

	t.Run("single hour range returns single row", func(t *testing.T) {
		rows, err := counter.GetTokenUsage(ctx, "token1", "2024-01-01T11:00", "2024-01-01T11:00")
		if err != nil {
			t.Fatalf("GetTokenUsage() error = %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("expected 1 row, got %d", len(rows))
		}
		if rows[0].HourBucket != "2024-01-01T11:00" {
			t.Errorf("rows[0].HourBucket = %s, want 2024-01-01T11:00", rows[0].HourBucket)
		}
		if rows[0].RequestCount != 2 {
			t.Errorf("rows[0].RequestCount = %d, want 2", rows[0].RequestCount)
		}
	})
}
