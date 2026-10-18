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

func TestHashCacheRecordAttempt(t *testing.T) {
	cache := NewHashCache(3)

	// First attempt: insert-on-first-sight, count=1
	if count := cache.RecordAttempt("hash1"); count != 1 {
		t.Errorf("First RecordAttempt should return 1, got %d", count)
	}

	// Second attempt of same hash: increment, count=2
	if count := cache.RecordAttempt("hash1"); count != 2 {
		t.Errorf("Second RecordAttempt should return 2, got %d", count)
	}

	// Different hash: fresh insert, count=1
	if count := cache.RecordAttempt("hash2"); count != 1 {
		t.Errorf("RecordAttempt of different hash should return 1, got %d", count)
	}
}

func TestHashCacheStoreIfAbsent(t *testing.T) {
	cache := NewHashCache(3)

	// Insert without incrementing: counter starts at 1
	cache.StoreIfAbsent("hash1")
	if !cache.Contains("hash1") {
		t.Error("hash1 should be present after StoreIfAbsent")
	}

	// The next normal RecordAttempt increments from 1 -> 2
	// (the StoreIfAbsent insertion counts as attempt 1)
	if count := cache.RecordAttempt("hash1"); count != 2 {
		t.Errorf("RecordAttempt after StoreIfAbsent should return 2, got %d", count)
	}
}

// TestHashCache_RecordAttemptAfterStoreIfAbsent pins the §4.1 semantics:
// RecordAttempt(hash) called after StoreIfAbsent(hash) for the same hash
// returns 2 (the force-seen insertion is counter-only: counter → 1, hash
// present, no increment; the next RecordAttempt increments 1 → 2).
func TestHashCache_RecordAttemptAfterStoreIfAbsent(t *testing.T) {
	cache := NewHashCache(100)
	hash := "force-seen-hash"

	cache.StoreIfAbsent(hash)
	count := cache.RecordAttempt(hash)
	if count != 2 {
		t.Errorf("RecordAttempt after StoreIfAbsent should return 2, got %d", count)
	}
}

func TestHashCacheStoreIfAbsentIdempotent(t *testing.T) {
	cache := NewHashCache(3)

	// Second StoreIfAbsent on a present hash is a no-op (still no increment)
	cache.StoreIfAbsent("hash1")
	cache.StoreIfAbsent("hash1")
	if count := cache.RecordAttempt("hash1"); count != 2 {
		t.Errorf("RecordAttempt after double StoreIfAbsent should return 2, got %d", count)
	}
}

func TestHashCacheCircularBuffer(t *testing.T) {
	cache := NewHashCache(3)

	// Fill cache
	cache.StoreIfAbsent("hash1")
	cache.StoreIfAbsent("hash2")
	cache.StoreIfAbsent("hash3")

	// Add one more - should evict oldest
	cache.StoreIfAbsent("hash4")

	// hash1 should be evicted
	if cache.Contains("hash1") {
		t.Error("hash1 should have been evicted")
	}

	// hash4 should be present
	if !cache.Contains("hash4") {
		t.Error("hash4 should be present")
	}
}

func TestHashCacheRemove(t *testing.T) {
	cache := NewHashCache(10)

	cache.StoreIfAbsent("hash1")
	cache.StoreIfAbsent("hash2")
	cache.StoreIfAbsent("hash3")

	// Remove hash2
	cache.Remove("hash2")

	// hash2 should be gone
	if cache.Contains("hash2") {
		t.Error("hash2 should have been removed")
	}

	// hash1 and hash3 should still be present
	if !cache.Contains("hash1") {
		t.Error("hash1 should still be present")
	}
	if !cache.Contains("hash3") {
		t.Error("hash3 should still be present")
	}
}

// TestHashCacheRemoveClearsAttemptCounter verifies that Remove resets the
// attempt schedule for the hash (next RecordAttempt restarts at 1).
func TestHashCacheRemoveClearsAttemptCounter(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"

	// Climb the counter
	cache.RecordAttempt(hash)
	cache.RecordAttempt(hash)
	cache.RecordAttempt(hash)

	// Remove the hash (ultimate success path — schedule re-arms)
	cache.Remove(hash)

	// Next attempt restarts at 1
	if count := cache.RecordAttempt(hash); count != 1 {
		t.Errorf("Expected count=1 after remove, got %d", count)
	}
}

func TestHashCacheReset(t *testing.T) {
	cache := NewHashCache(10)

	cache.StoreIfAbsent("hash1")
	cache.StoreIfAbsent("hash2")

	cache.Reset()

	// All hashes should be gone
	if cache.Contains("hash1") {
		t.Error("hash1 should have been reset")
	}
	if cache.Contains("hash2") {
		t.Error("hash2 should have been reset")
	}
}

// TestHashCacheResetClearsAllAttemptCounters verifies that Reset clears
// every attempt counter (next attempts restart at 1).
func TestHashCacheResetClearsAllAttemptCounters(t *testing.T) {
	cache := NewHashCache(100)

	hash1 := "hash-1"
	hash2 := "hash-2"
	cache.RecordAttempt(hash1)
	cache.RecordAttempt(hash1)
	cache.RecordAttempt(hash2)

	cache.Reset()

	if count := cache.RecordAttempt(hash1); count != 1 {
		t.Errorf("Expected hash1 count=1 after reset, got %d", count)
	}
	if count := cache.RecordAttempt(hash2); count != 1 {
		t.Errorf("Expected hash2 count=1 after reset, got %d", count)
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
			cache.StoreIfAbsent(hash)
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

	cache.StoreIfAbsent("hash1")
	cache.StoreIfAbsent("hash2")

	count, _ = cache.GetStats()
	if count != 2 {
		t.Errorf("Count should be 2, got %d", count)
	}
}

// TestHashCache_AttemptCounterCleanedOnEviction verifies the counter map is
// cleaned up when a hash is evicted from the circular buffer (counting
// restarts at 1 after eviction).
func TestHashCache_AttemptCounterCleanedOnEviction(t *testing.T) {
	cache := NewHashCache(3) // Small buffer to force eviction

	// Climb hash1's counter to 3
	for i := 0; i < 3; i++ {
		cache.RecordAttempt("hash-1")
	}

	// Fill the buffer with other hashes, evicting hash-1
	cache.StoreIfAbsent("hash-2")
	cache.StoreIfAbsent("hash-3")
	cache.StoreIfAbsent("hash-4") // This evicts hash-1

	if cache.Contains("hash-1") {
		t.Error("hash-1 should have been evicted from the circular buffer")
	}

	// The same hash restarts at 1 (counter entry was deleted on eviction)
	if count := cache.RecordAttempt("hash-1"); count != 1 {
		t.Errorf("Expected hash-1 count=1 after eviction, got %d", count)
	}
}

// TestHashCache_ConcurrentRecordAttempt asserts that N goroutines calling
// RecordAttempt on the same hash receive exactly the counts {1..N} (set
// equality, not just mutual exclusion).
func TestHashCache_ConcurrentRecordAttempt(t *testing.T) {
	cache := NewHashCache(100)
	hash := "test-hash-123"
	const n = 50

	counts := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			counts[idx] = cache.RecordAttempt(hash)
		}(i)
	}
	wg.Wait()

	// Multiset of returned counts must be exactly {1..n}
	seen := make(map[int]int)
	for _, c := range counts {
		seen[c]++
	}
	for want := 1; want <= n; want++ {
		if seen[want] != 1 {
			t.Errorf("Expected exactly one goroutine to see count=%d, saw %d", want, seen[want])
		}
	}
}

func TestHashCache_Contains(t *testing.T) {
	cache := NewHashCache(10)

	// Empty cache should not contain any hash
	if cache.Contains("hash1") {
		t.Error("Empty cache should not contain hash1")
	}

	// Store a hash
	cache.RecordAttempt("hash1")

	// Now it should contain the hash
	if !cache.Contains("hash1") {
		t.Error("Cache should contain hash1 after RecordAttempt")
	}

	// Different hash should not be found
	if cache.Contains("hash2") {
		t.Error("Cache should not contain hash2")
	}

	// Store another hash
	cache.RecordAttempt("hash2")

	// Both should be found
	if !cache.Contains("hash1") {
		t.Error("Cache should contain hash1")
	}
	if !cache.Contains("hash2") {
		t.Error("Cache should contain hash2")
	}

	// Remove hash1
	cache.Remove("hash1")

	// hash1 should not be found after removal
	if cache.Contains("hash1") {
		t.Error("Cache should not contain hash1 after removal")
	}
	if !cache.Contains("hash2") {
		t.Error("Cache should still contain hash2")
	}
}
