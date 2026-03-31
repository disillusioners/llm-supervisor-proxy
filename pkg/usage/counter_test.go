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
