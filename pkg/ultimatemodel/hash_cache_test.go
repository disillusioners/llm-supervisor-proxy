package ultimatemodel

import (
	"sync"
	"testing"
)

func TestHashMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []map[string]interface{}
		wantLen  int // Expected hash length (SHA256 = 64 hex chars)
	}{
		{
			name:     "empty messages",
			messages: []map[string]interface{}{},
			wantLen:  64,
		},
		{
			name: "single message",
			messages: []map[string]interface{}{
				{"role": "user", "content": "Hello"},
			},
			wantLen: 64,
		},
		{
			name: "multiple messages",
			messages: []map[string]interface{}{
				{"role": "user", "content": "Hello"},
				{"role": "assistant", "content": "Hi there!"},
			},
			wantLen: 64,
		},
		{
			name: "multimodal content",
			messages: []map[string]interface{}{
				{"role": "user", "content": []interface{}{
					map[string]interface{}{"type": "text", "text": "What's in this image?"},
					map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/image.png"}},
				}},
			},
			wantLen: 64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := HashMessages(tt.messages)
			if len(hash) != tt.wantLen {
				t.Errorf("HashMessages() hash length = %d, want %d", len(hash), tt.wantLen)
			}
		})
	}
}

func TestHashMessagesConsistency(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
		{"role": "assistant", "content": "Hi there!"},
	}

	hash1 := HashMessages(messages)
	hash2 := HashMessages(messages)

	if hash1 != hash2 {
		t.Errorf("HashMessages() not consistent: %s != %s", hash1, hash2)
	}
}

func TestHashMessagesOrder(t *testing.T) {
	messages1 := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
		{"role": "assistant", "content": "Hi"},
	}
	messages2 := []map[string]interface{}{
		{"role": "assistant", "content": "Hi"},
		{"role": "user", "content": "Hello"},
	}

	hash1 := HashMessages(messages1)
	hash2 := HashMessages(messages2)

	if hash1 == hash2 {
		t.Error("HashMessages() should produce different hashes for different message orders")
	}
}

func TestHashCacheStoreAndCheck(t *testing.T) {
	cache := NewHashCache(3)

	// First store should return false (not duplicate)
	if cache.StoreAndCheck("hash1") {
		t.Error("First StoreAndCheck should return false")
	}

	// Second store of same hash should return true (duplicate)
	if !cache.StoreAndCheck("hash1") {
		t.Error("Second StoreAndCheck of same hash should return true")
	}

	// Different hash should return false
	if cache.StoreAndCheck("hash2") {
		t.Error("StoreAndCheck of different hash should return false")
	}
}

func TestHashCacheCircularBuffer(t *testing.T) {
	cache := NewHashCache(3)

	// Fill cache
	cache.StoreAndCheck("hash1")
	cache.StoreAndCheck("hash2")
	cache.StoreAndCheck("hash3")

	// Add one more - should evict oldest
	cache.StoreAndCheck("hash4")

	// hash1 should be evicted
	if cache.StoreAndCheck("hash1") {
		t.Error("hash1 should have been evicted")
	}

	// hash4 should be present
	if !cache.StoreAndCheck("hash4") {
		t.Error("hash4 should be present")
	}
}

func TestHashCacheRemove(t *testing.T) {
	cache := NewHashCache(10)

	cache.StoreAndCheck("hash1")
	cache.StoreAndCheck("hash2")
	cache.StoreAndCheck("hash3")

	// Remove hash2
	cache.Remove("hash2")

	// hash2 should be gone
	if cache.StoreAndCheck("hash2") {
		t.Error("hash2 should have been removed")
	}

	// hash1 and hash3 should still be present
	if !cache.StoreAndCheck("hash1") {
		t.Error("hash1 should still be present")
	}
	if !cache.StoreAndCheck("hash3") {
		t.Error("hash3 should still be present")
	}
}

func TestHashCacheReset(t *testing.T) {
	cache := NewHashCache(10)

	cache.StoreAndCheck("hash1")
	cache.StoreAndCheck("hash2")

	cache.Reset()

	// All hashes should be gone
	if cache.StoreAndCheck("hash1") {
		t.Error("hash1 should have been reset")
	}
	if cache.StoreAndCheck("hash2") {
		t.Error("hash2 should have been reset")
	}
}

func TestHashCacheConcurrent(t *testing.T) {
	cache := NewHashCache(100)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hash := HashMessages([]map[string]interface{}{
				{"role": "user", "content": string(rune(n))},
			})
			cache.StoreAndCheck(hash)
		}(i)
	}

	wg.Wait()

	// Should not panic and cache should be consistent
	count, _ := cache.GetStats()
	if count > 100 {
		t.Errorf("Cache count should be <= 100, got %d", count)
	}
}

func TestHashCacheGetStats(t *testing.T) {
	cache := NewHashCache(10)

	count, capacity := cache.GetStats()
	if count != 0 {
		t.Errorf("Initial count should be 0, got %d", count)
	}
	if capacity != 10 {
		t.Errorf("Capacity should be 10, got %d", capacity)
	}

	cache.StoreAndCheck("hash1")
	cache.StoreAndCheck("hash2")

	count, _ = cache.GetStats()
	if count != 2 {
		t.Errorf("Count should be 2, got %d", count)
	}
}

func TestHashCache_IncrementAndCheckRetry(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"
	maxRetries := 2

	// First increment: count=1, not exhausted
	newCount, exhausted := cache.IncrementAndCheckRetry(hash, maxRetries)
	if newCount != 1 {
		t.Errorf("Expected count=1, got %d", newCount)
	}
	if exhausted {
		t.Error("Should not be exhausted on first increment")
	}

	// Second increment: count=2, not exhausted (2 <= 2)
	newCount, exhausted = cache.IncrementAndCheckRetry(hash, maxRetries)
	if newCount != 2 {
		t.Errorf("Expected count=2, got %d", newCount)
	}
	if exhausted {
		t.Error("Should not be exhausted on second increment (2 <= 2)")
	}

	// Third increment: count=3, exhausted (3 > 2)
	newCount, exhausted = cache.IncrementAndCheckRetry(hash, maxRetries)
	if newCount != 3 {
		t.Errorf("Expected count=3, got %d", newCount)
	}
	if !exhausted {
		t.Error("Should be exhausted on third increment (3 > 2)")
	}
}

func TestHashCache_GetRetryCount(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"

	// Non-existent hash should return 0
	if count := cache.GetRetryCount(hash); count != 0 {
		t.Errorf("Expected count=0 for non-existent hash, got %d", count)
	}

	// After increment, should return the count
	cache.IncrementAndCheckRetry(hash, 5)
	cache.IncrementAndCheckRetry(hash, 5)
	if count := cache.GetRetryCount(hash); count != 2 {
		t.Errorf("Expected count=2, got %d", count)
	}
}

func TestHashCache_ClearRetryCount(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"

	// Increment a few times
	cache.IncrementAndCheckRetry(hash, 5)
	cache.IncrementAndCheckRetry(hash, 5)

	// Clear the counter
	cache.ClearRetryCount(hash)

	// Should be back to 0
	if count := cache.GetRetryCount(hash); count != 0 {
		t.Errorf("Expected count=0 after clear, got %d", count)
	}
}

func TestHashCache_RemoveClearsRetryCounter(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"

	// Store and increment
	cache.StoreAndCheck(hash)
	cache.IncrementAndCheckRetry(hash, 5)
	cache.IncrementAndCheckRetry(hash, 5)

	// Remove the hash
	cache.Remove(hash)

	// Retry counter should be cleared
	if count := cache.GetRetryCount(hash); count != 0 {
		t.Errorf("Expected count=0 after remove, got %d", count)
	}
}

func TestHashCache_ResetClearsAllRetryCounters(t *testing.T) {
	cache := NewHashCache(100)

	// Store and increment multiple hashes
	hash1 := "hash-1"
	hash2 := "hash-2"
	cache.StoreAndCheck(hash1)
	cache.StoreAndCheck(hash2)
	cache.IncrementAndCheckRetry(hash1, 5)
	cache.IncrementAndCheckRetry(hash2, 5)

	// Reset
	cache.Reset()

	// All counters should be cleared
	if count := cache.GetRetryCount(hash1); count != 0 {
		t.Errorf("Expected hash1 count=0 after reset, got %d", count)
	}
	if count := cache.GetRetryCount(hash2); count != 0 {
		t.Errorf("Expected hash2 count=0 after reset, got %d", count)
	}
}

func TestHashCache_RetryCounterCleanedOnEviction(t *testing.T) {
	// Test that retry counter is cleaned up when hash is evicted from circular buffer
	cache := NewHashCache(3) // Small buffer to force eviction

	// Store hashes and increment retry counters
	hash1 := "hash-1"
	hash2 := "hash-2"
	hash3 := "hash-3"
	hash4 := "hash-4" // This will cause eviction

	cache.StoreAndCheck(hash1)
	cache.IncrementAndCheckRetry(hash1, 10)
	cache.IncrementAndCheckRetry(hash1, 10)

	cache.StoreAndCheck(hash2)
	cache.IncrementAndCheckRetry(hash2, 10)

	cache.StoreAndCheck(hash3)
	cache.IncrementAndCheckRetry(hash3, 10)

	// Verify retry counters exist
	if count := cache.GetRetryCount(hash1); count != 2 {
		t.Errorf("Expected hash1 count=2, got %d", count)
	}

	// Store hash4 - this should evict hash1 and clean up its retry counter
	cache.StoreAndCheck(hash4)

	// hash1's retry counter should be cleaned up
	if count := cache.GetRetryCount(hash1); count != 0 {
		t.Errorf("Expected hash1 count=0 after eviction, got %d", count)
	}

	// hash2, hash3, hash4 should still have their counters
	if count := cache.GetRetryCount(hash2); count != 1 {
		t.Errorf("Expected hash2 count=1, got %d", count)
	}
	if count := cache.GetRetryCount(hash3); count != 1 {
		t.Errorf("Expected hash3 count=1, got %d", count)
	}
}

func TestHashCache_ConcurrentRetryCounter(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"
	maxRetries := 2

	var wg sync.WaitGroup
	results := make([]bool, 10) // exhausted results

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			newCount, exhausted := cache.IncrementAndCheckRetry(hash, maxRetries)
			results[idx] = exhausted
			t.Logf("Request %d: count=%d, exhausted=%v", idx, newCount, exhausted)
		}(i)
	}

	wg.Wait()

	// Verify exactly (10 - maxRetries) requests see exhausted=true
	// With maxRetries=2, first 2 succeed, remaining 8 are exhausted
	exhaustedCount := 0
	for _, ex := range results {
		if ex {
			exhaustedCount++
		}
	}
	if exhaustedCount != 10-maxRetries {
		t.Errorf("Expected %d exhausted, got %d", 10-maxRetries, exhaustedCount)
	}

	// Final count should be 10
	if count := cache.GetRetryCount(hash); count != 10 {
		t.Errorf("Expected final count=10, got %d", count)
	}
}
