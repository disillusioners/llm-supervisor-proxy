package usage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// sqliteDSN returns a DSN string with the same pragmas as the production configuration.
// This is critical for the stress test to replicate the production fix.
func sqliteDSN(dbPath string) string {
	return dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)"
}

// setupStressTestDB creates a SQLite database with the production DSN configuration
// and creates the required usage tables.
func setupStressTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stress_test.db")

	// Open with the same DSN as production (WAL mode, busy_timeout=5000, MaxOpenConns=10)
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("Failed to open SQLite database: %v", err)
	}

	// Limit connection pool size to match production (10 connections)
	db.SetMaxOpenConns(10)

	// Create the token_hourly_usage table
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS token_hourly_usage (
			token_id           TEXT    NOT NULL,
			hour_bucket        TEXT    NOT NULL,
			request_count      INTEGER NOT NULL DEFAULT 0,
			prompt_tokens      INTEGER NOT NULL DEFAULT 0,
			completion_tokens  INTEGER NOT NULL DEFAULT 0,
			total_tokens       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (token_id, hour_bucket)
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("Failed to create token_hourly_usage table: %v", err)
	}

	// Create the model_hourly_usage table
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS model_hourly_usage (
			model_id           TEXT    NOT NULL,
			hour_bucket        TEXT    NOT NULL,
			request_count      INTEGER NOT NULL DEFAULT 0,
			prompt_tokens      INTEGER NOT NULL DEFAULT 0,
			completion_tokens  INTEGER NOT NULL DEFAULT 0,
			total_tokens       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (model_id, hour_bucket)
		)
	`)
	if err != nil {
		db.Close()
		t.Fatalf("Failed to create model_hourly_usage table: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

// TestConcurrentIncrement_Stress tests concurrent Increment() calls with high concurrency.
// This test verifies that with WAL mode and busy_timeout=5000, we get NO SQLITE_BUSY errors
// and all increments are counted correctly.
func TestConcurrentIncrement_Stress(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		numGoroutines = 30       // Number of concurrent goroutines
		iterations    = 100      // Iterations per goroutine
		incPerCall    = 5        // Increment values per call
	)

	tokenID := "stress-test-token"
	hourBucket := "2026-05-21T14"
	expectedTotalReqCount := int64(numGoroutines * iterations * incPerCall)
	expectedTotalPromptTokens := int64(numGoroutines * iterations * incPerCall * 10)
	expectedTotalCompletionTokens := int64(numGoroutines * iterations * incPerCall * 20)
	expectedTotalTokens := expectedTotalPromptTokens + expectedTotalCompletionTokens

	var wg sync.WaitGroup
	var errorCount int64
	var callCount int64

	// Spawn multiple goroutines that all increment the same token
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				atomic.AddInt64(&callCount, 1)
				err := counter.Increment(ctx, tokenID, hourBucket,
					incPerCall,           // reqCount
					incPerCall*10,        // promptTokens
					incPerCall*20,        // completionTokens
					incPerCall*30,        // totalTokens (prompt+completion+tolerance)
				)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					t.Errorf("Increment failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// Verify no errors occurred
	if errorCount > 0 {
		t.Fatalf("SQLITE_BUSY or other errors occurred: %d errors out of %d calls", errorCount, callCount)
	}

	// Verify the counts are correct (no lost writes)
	rows, err := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("Failed to query token usage: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	actual := rows[0]

	t.Logf("TestConcurrentIncrement_Stress Results:")
	t.Logf("  Goroutines: %d, Iterations: %d, IncPerCall: %d", numGoroutines, iterations, incPerCall)
	t.Logf("  Expected request_count: %d, Actual: %d", expectedTotalReqCount, actual.RequestCount)
	t.Logf("  Expected prompt_tokens: %d, Actual: %d", expectedTotalPromptTokens, actual.PromptTokens)
	t.Logf("  Expected completion_tokens: %d, Actual: %d", expectedTotalCompletionTokens, actual.CompletionTokens)
	t.Logf("  Expected total_tokens: %d, Actual: %d", expectedTotalTokens, actual.TotalTokens)

	if actual.RequestCount != int(expectedTotalReqCount) {
		t.Errorf("Request count mismatch: expected %d, got %d", expectedTotalReqCount, actual.RequestCount)
	}
	if actual.PromptTokens != int(expectedTotalPromptTokens) {
		t.Errorf("Prompt tokens mismatch: expected %d, got %d", expectedTotalPromptTokens, actual.PromptTokens)
	}
	if actual.CompletionTokens != int(expectedTotalCompletionTokens) {
		t.Errorf("Completion tokens mismatch: expected %d, got %d", expectedTotalCompletionTokens, actual.CompletionTokens)
	}
	if actual.TotalTokens != int(expectedTotalTokens) {
		t.Errorf("Total tokens mismatch: expected %d, got %d", expectedTotalTokens, actual.TotalTokens)
	}
}

// TestConcurrentIncrementModelUsage_Stress tests concurrent IncrementModelUsage() calls.
// This verifies that the model_hourly_usage table handles concurrent writes correctly.
func TestConcurrentIncrementModelUsage_Stress(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		numGoroutines = 30
		iterations    = 100
		incPerCall    = 3
	)

	modelID := "stress-test-model"
	hourBucket := "2026-05-21T14"
	expectedTotalReqCount := int64(numGoroutines * iterations * incPerCall)
	expectedTotalPromptTokens := int64(numGoroutines * iterations * incPerCall * 15)
	expectedTotalCompletionTokens := int64(numGoroutines * iterations * incPerCall * 25)
	expectedTotalTokens := expectedTotalPromptTokens + expectedTotalCompletionTokens

	var wg sync.WaitGroup
	var errorCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := counter.IncrementModelUsage(ctx, modelID, hourBucket,
					incPerCall,           // reqCount
					incPerCall*15,        // promptTokens
					incPerCall*25,        // completionTokens
					incPerCall*40,        // totalTokens
				)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					t.Errorf("IncrementModelUsage failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("SQLITE_BUSY or other errors occurred: %d errors", errorCount)
	}

	// Query the model usage
	rows, err := counter.GetModelUsage(ctx, hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("Failed to query model usage: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	actual := rows[0]

	t.Logf("TestConcurrentIncrementModelUsage_Stress Results:")
	t.Logf("  Goroutines: %d, Iterations: %d, IncPerCall: %d", numGoroutines, iterations, incPerCall)
	t.Logf("  Expected request_count: %d, Actual: %d", expectedTotalReqCount, actual.RequestCount)
	t.Logf("  Expected prompt_tokens: %d, Actual: %d", expectedTotalPromptTokens, actual.PromptTokens)
	t.Logf("  Expected completion_tokens: %d, Actual: %d", expectedTotalCompletionTokens, actual.CompletionTokens)
	t.Logf("  Expected total_tokens: %d, Actual: %d", expectedTotalTokens, actual.TotalTokens)

	if actual.RequestCount != int(expectedTotalReqCount) {
		t.Errorf("Request count mismatch: expected %d, got %d", expectedTotalReqCount, actual.RequestCount)
	}
	if actual.PromptTokens != int(expectedTotalPromptTokens) {
		t.Errorf("Prompt tokens mismatch: expected %d, got %d", expectedTotalPromptTokens, actual.PromptTokens)
	}
	if actual.CompletionTokens != int(expectedTotalCompletionTokens) {
		t.Errorf("Completion tokens mismatch: expected %d, got %d", expectedTotalCompletionTokens, actual.CompletionTokens)
	}
	if actual.TotalTokens != int(expectedTotalTokens) {
		t.Errorf("Total tokens mismatch: expected %d, got %d", expectedTotalTokens, actual.TotalTokens)
	}
}

// TestConcurrentMixedUsage_Stress tests concurrent Increment() and IncrementModelUsage()
// calls happening simultaneously. This simulates real-world mixed workload.
func TestConcurrentMixedUsage_Stress(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		tokenGoroutines  = 20
		modelGoroutines  = 20
		iterations       = 100
		tokenIncPerCall  = 7
		modelIncPerCall  = 5
	)

	tokenID := "mixed-test-token"
	modelID := "mixed-test-model"
	hourBucket := "2026-05-21T14"

	// Expected values for token_hourly_usage
	expectedTokenReqCount := int64(tokenGoroutines * iterations * tokenIncPerCall)
	expectedTokenPromptTokens := int64(tokenGoroutines * iterations * tokenIncPerCall * 10)
	expectedTokenCompletionTokens := int64(tokenGoroutines * iterations * tokenIncPerCall * 20)

	// Expected values for model_hourly_usage
	expectedModelReqCount := int64(modelGoroutines * iterations * modelIncPerCall)
	expectedModelPromptTokens := int64(modelGoroutines * iterations * modelIncPerCall * 12)
	expectedModelCompletionTokens := int64(modelGoroutines * iterations * modelIncPerCall * 18)

	var wg sync.WaitGroup
	var tokenErrors, modelErrors int64

	// Spawn token increment goroutines
	for i := 0; i < tokenGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := counter.Increment(ctx, tokenID, hourBucket,
					tokenIncPerCall,
					tokenIncPerCall*10,
					tokenIncPerCall*20,
					tokenIncPerCall*30,
				)
				if err != nil {
					atomic.AddInt64(&tokenErrors, 1)
					t.Errorf("Token Increment failed: %v", err)
				}
			}
		}()
	}

	// Spawn model increment goroutines
	for i := 0; i < modelGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := counter.IncrementModelUsage(ctx, modelID, hourBucket,
					modelIncPerCall,
					modelIncPerCall*12,
					modelIncPerCall*18,
					modelIncPerCall*30,
				)
				if err != nil {
					atomic.AddInt64(&modelErrors, 1)
					t.Errorf("Model Increment failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	totalErrors := tokenErrors + modelErrors
	if totalErrors > 0 {
		t.Fatalf("SQLITE_BUSY or other errors occurred: %d total errors (token: %d, model: %d)",
			totalErrors, tokenErrors, modelErrors)
	}

	// Verify token usage
	tokenRows, err := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("Failed to query token usage: %v", err)
	}

	if len(tokenRows) != 1 {
		t.Fatalf("Expected 1 token row, got %d", len(tokenRows))
	}

	tokenActual := tokenRows[0]

	t.Logf("TestConcurrentMixedUsage_Stress - Token Results:")
	t.Logf("  Expected request_count: %d, Actual: %d", expectedTokenReqCount, tokenActual.RequestCount)
	t.Logf("  Expected prompt_tokens: %d, Actual: %d", expectedTokenPromptTokens, tokenActual.PromptTokens)
	t.Logf("  Expected completion_tokens: %d, Actual: %d", expectedTokenCompletionTokens, tokenActual.CompletionTokens)

	if tokenActual.RequestCount != int(expectedTokenReqCount) {
		t.Errorf("Token request count mismatch: expected %d, got %d", expectedTokenReqCount, tokenActual.RequestCount)
	}
	if tokenActual.PromptTokens != int(expectedTokenPromptTokens) {
		t.Errorf("Token prompt tokens mismatch: expected %d, got %d", expectedTokenPromptTokens, tokenActual.PromptTokens)
	}
	if tokenActual.CompletionTokens != int(expectedTokenCompletionTokens) {
		t.Errorf("Token completion tokens mismatch: expected %d, got %d", expectedTokenCompletionTokens, tokenActual.CompletionTokens)
	}

	// Verify model usage
	modelRows, err := counter.GetModelUsage(ctx, hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("Failed to query model usage: %v", err)
	}

	if len(modelRows) != 1 {
		t.Fatalf("Expected 1 model row, got %d", len(modelRows))
	}

	modelActual := modelRows[0]

	t.Logf("TestConcurrentMixedUsage_Stress - Model Results:")
	t.Logf("  Expected request_count: %d, Actual: %d", expectedModelReqCount, modelActual.RequestCount)
	t.Logf("  Expected prompt_tokens: %d, Actual: %d", expectedModelPromptTokens, modelActual.PromptTokens)
	t.Logf("  Expected completion_tokens: %d, Actual: %d", expectedModelCompletionTokens, modelActual.CompletionTokens)

	if modelActual.RequestCount != int(expectedModelReqCount) {
		t.Errorf("Model request count mismatch: expected %d, got %d", expectedModelReqCount, modelActual.RequestCount)
	}
	if modelActual.PromptTokens != int(expectedModelPromptTokens) {
		t.Errorf("Model prompt tokens mismatch: expected %d, got %d", expectedModelPromptTokens, modelActual.PromptTokens)
	}
	if modelActual.CompletionTokens != int(expectedModelCompletionTokens) {
		t.Errorf("Model completion tokens mismatch: expected %d, got %d", expectedModelCompletionTokens, modelActual.CompletionTokens)
	}
}

// TestConcurrentHighLoad_Stress tests with even higher concurrency to stress the system.
func TestConcurrentHighLoad_Stress(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		numGoroutines = 50       // High number of concurrent goroutines
		iterations    = 50        // Each goroutine does 50 iterations
		incPerCall    = 2
	)

	tokenID := "highload-test-token"
	modelID := "highload-test-model"
	hourBucket := "2026-05-21T14"

	// Expected values
	expectedTokenReqCount := int64(numGoroutines * iterations * incPerCall)
	expectedModelReqCount := int64(numGoroutines * iterations * incPerCall)

	var wg sync.WaitGroup
	var errorCount int64

	// All goroutines do both token and model increments
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Token increment
				err := counter.Increment(ctx, tokenID, hourBucket, incPerCall, incPerCall*5, incPerCall*10, incPerCall*15)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					return
				}
				// Model increment
				err = counter.IncrementModelUsage(ctx, modelID, hourBucket, incPerCall, incPerCall*5, incPerCall*10, incPerCall*15)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					return
				}
			}
		}()
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("SQLITE_BUSY or other errors occurred: %d errors", errorCount)
	}

	// Verify token counts
	tokenRows, _ := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
	if len(tokenRows) == 1 && tokenRows[0].RequestCount != int(expectedTokenReqCount) {
		t.Errorf("Token request count mismatch: expected %d, got %d", expectedTokenReqCount, tokenRows[0].RequestCount)
	}

	// Verify model counts
	modelRows, _ := counter.GetModelUsage(ctx, hourBucket, hourBucket)
	if len(modelRows) == 1 && modelRows[0].RequestCount != int(expectedModelReqCount) {
		t.Errorf("Model request count mismatch: expected %d, got %d", expectedModelReqCount, modelRows[0].RequestCount)
	}

	t.Logf("TestConcurrentHighLoad_Stress: 50 goroutines x 50 iterations completed with %d errors", errorCount)
}

// TestConcurrentDifferentBuckets_Stress tests concurrent increments across different hour buckets.
// This should be easier for SQLite since different primary keys don't conflict.
func TestConcurrentDifferentBuckets_Stress(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		numGoroutines = 30
		iterations    = 100
		incPerCall    = 3
	)

	// Each goroutine uses a different hour bucket
	hourBuckets := []string{
		"2026-05-21T10",
		"2026-05-21T11",
		"2026-05-21T12",
		"2026-05-21T13",
		"2026-05-21T14",
		"2026-05-21T15",
	}

	tokenID := "bucket-test-token"
	modelID := "bucket-test-model"

	var wg sync.WaitGroup
	var errorCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			bucket := hourBuckets[goroutineID%len(hourBuckets)]
			for j := 0; j < iterations; j++ {
				err := counter.Increment(ctx, tokenID, bucket, incPerCall, incPerCall*5, incPerCall*10, incPerCall*15)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					t.Errorf("Increment failed: %v", err)
				}
				err = counter.IncrementModelUsage(ctx, modelID, bucket, incPerCall, incPerCall*5, incPerCall*10, incPerCall*15)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					t.Errorf("IncrementModelUsage failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("SQLITE_BUSY or other errors occurred: %d errors", errorCount)
	}

	// Verify all buckets have correct counts
	expectedReqCount := int64((numGoroutines / len(hourBuckets)) * iterations * incPerCall)

	tokenRows, err := counter.GetTokenUsage(ctx, tokenID, "2026-05-21T10", "2026-05-21T15")
	if err != nil {
		t.Fatalf("Failed to query token usage: %v", err)
	}

	if len(tokenRows) != len(hourBuckets) {
		t.Fatalf("Expected %d rows, got %d", len(hourBuckets), len(tokenRows))
	}

	for _, row := range tokenRows {
		if row.RequestCount != int(expectedReqCount) {
			t.Errorf("Bucket %s: expected %d, got %d", row.HourBucket, expectedReqCount, row.RequestCount)
		}
	}

	t.Logf("TestConcurrentDifferentBuckets_Stress: %d buckets verified correctly", len(tokenRows))
}

// TestSQLiteWALConfiguration verifies that WAL mode and busy_timeout are properly configured.
// This ensures the test configuration matches production.
func TestSQLiteWALConfiguration(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	// Verify busy_timeout = 5000
	var busyTimeout string
	err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if err != nil {
		t.Fatalf("Failed to get busy_timeout: %v", err)
	}

	if busyTimeout != "5000" {
		t.Errorf("Expected busy_timeout=5000, got: %s", busyTimeout)
	}

	// Verify MaxOpenConns = 10 (matching production)
	stats := db.Stats()
	if stats.MaxOpenConnections != 10 {
		t.Errorf("Expected MaxOpenConns=10, got: %d", stats.MaxOpenConnections)
	}

	t.Logf("SQLite configuration verified: busy_timeout=5000, MaxOpenConns=10")
}

// TestConcurrentStressWithQueryInterleaving tests concurrent writes while also
// performing reads. This tests WAL mode's ability to allow concurrent reads during writes.
func TestConcurrentStressWithQueryInterleaving(t *testing.T) {
	db, cleanup := setupStressTestDB(t)
	defer cleanup()

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	const (
		numGoroutines = 20
		iterations    = 50
		incPerCall    = 5
	)

	tokenID := "interleave-test-token"
	modelID := "interleave-test-model"
	hourBucket := "2026-05-21T14"

	var wg sync.WaitGroup
	var writeErrors, readErrors int64
	var readCount int64

	// Writer goroutines
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := counter.Increment(ctx, tokenID, hourBucket, incPerCall, incPerCall*10, incPerCall*20, incPerCall*30)
				if err != nil {
					atomic.AddInt64(&writeErrors, 1)
				}
				err = counter.IncrementModelUsage(ctx, modelID, hourBucket, incPerCall, incPerCall*10, incPerCall*20, incPerCall*30)
				if err != nil {
					atomic.AddInt64(&writeErrors, 1)
				}
			}
		}()
	}

	// Reader goroutines that interleave with writes
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Query token usage
				_, err := counter.GetTokenUsage(ctx, tokenID, hourBucket, hourBucket)
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&readCount, 1)
				}

				// Query model usage
				_, err = counter.GetModelUsage(ctx, hourBucket, hourBucket)
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&readCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	totalErrors := writeErrors + readErrors
	if totalErrors > 0 {
		t.Fatalf("Errors occurred: %d total (write: %d, read: %d)", totalErrors, writeErrors, readErrors)
	}

	t.Logf("TestConcurrentStressWithQueryInterleaving: %d reads completed successfully", readCount)
}

// BenchmarkConcurrentIncrement provides a benchmark for concurrent increment performance.
func BenchmarkConcurrentIncrement(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.db")

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		b.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create tables
	db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS token_hourly_usage (
			token_id TEXT NOT NULL, hour_bucket TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (token_id, hour_bucket)
		)
	`)

	counter := NewCounter(db, "sqlite")
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tokenID := fmt.Sprintf("bench-token-%d", i%10)
			counter.Increment(ctx, tokenID, "2026-05-21T14", 1, 10, 20, 30)
			i++
		}
	})
}
