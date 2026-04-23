package ultimatemodel

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// HashCache is a circular buffer of request hashes.
// It stores hashes of message content to detect duplicate requests.
// When a duplicate is detected, the ultimate model is triggered.
// Also tracks retry counts per hash for the ultimate model retry limit feature.
type HashCache struct {
	mu           sync.RWMutex
	hashes       []string        // circular buffer
	size         int             // max capacity
	head         int             // next write position
	count        int             // current count
	retryCounter map[string]int  // hash -> retry count for ultimate model
}

// NewHashCache creates a new hash cache with the given max size.
// If maxSize <= 0, defaults to 100.
func NewHashCache(maxSize int) *HashCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &HashCache{
		hashes:       make([]string, maxSize),
		size:         maxSize,
		head:         0,
		count:        0,
		retryCounter: make(map[string]int),
	}
}

// StoreAndCheck stores the hash and returns whether it was ALREADY present.
// This is an ATOMIC operation that prevents race conditions with concurrent requests.
//
// Returns:
//   - true: hash was already in cache (duplicate detected)
//   - false: hash was not in cache (first time seeing this request)
//
// The hash is always stored after the check, so subsequent calls will return true.
func (c *HashCache) StoreAndCheck(hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if hash already exists
	for i := 0; i < c.count; i++ {
		if c.hashes[i] == hash {
			return true // Duplicate detected
		}
	}

	// If buffer is full, clean up the evicted hash's retry counter
	// This prevents memory leak in retryCounter map
	if c.count >= c.size {
		evictedHash := c.hashes[c.head]
		if evictedHash != "" {
			delete(c.retryCounter, evictedHash)
		}
	}

	// Store hash in circular buffer
	c.hashes[c.head] = hash
	c.head = (c.head + 1) % c.size
	if c.count < c.size {
		c.count++
	}

	return false // First time
}

// Remove removes a hash from the cache.
// This also clears the retry counter for the hash.
// If the hash is not found, this is a no-op.
func (c *HashCache) Remove(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find and remove the hash
	for i := 0; i < c.count; i++ {
		if c.hashes[i] == hash {
			// Shift remaining elements to fill the gap
			copy(c.hashes[i:], c.hashes[i+1:c.count])
			c.count--
			c.hashes[c.count] = "" // Clear the last element
			// head only changes if we removed at head position (i == 0)
			if i == 0 {
				c.head = (c.head - 1 + c.size) % c.size
			}
			break
		}
	}

	// Also clear retry counter
	delete(c.retryCounter, hash)
}

// Contains checks if a hash exists in the cache without storing it.
// Returns true if the hash is present, false otherwise.
func (c *HashCache) Contains(hash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := 0; i < c.count; i++ {
		if c.hashes[i] == hash {
			return true
		}
	}
	return false
}

// Reset clears all hashes from the cache.
// This is called when the ultimate_model_id config changes.
func (c *HashCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.hashes = make([]string, c.size)
	c.head = 0
	c.count = 0
	c.retryCounter = make(map[string]int) // Clear all retry counters
}

// IncrementAndCheckRetry atomically increments and checks if limit exceeded.
// This prevents TOCTOU race condition between check and increment.
// Returns (newCount, exhausted) where exhausted=true if newCount > maxRetries.
func (c *HashCache) IncrementAndCheckRetry(hash string, maxRetries int) (newCount int, exhausted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryCounter[hash]++
	newCount = c.retryCounter[hash]
	return newCount, newCount > maxRetries
}

// GetRetryCount returns the current retry count for a hash.
// Returns 0 if hash not found in retry counter.
func (c *HashCache) GetRetryCount(hash string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.retryCounter[hash]
}

// ClearRetryCount removes the retry counter for a hash.
// Called when ultimate model succeeds or when hash is removed.
func (c *HashCache) ClearRetryCount(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.retryCounter, hash)
}

// HashMessages generates a consistent hash from chat completion messages.
// Only the role and content are hashed - timestamps, metadata, and tool_call_ids are ignored.
//
// FULL SHA256 (64 hex characters) is used. Truncation is NOT permitted.
// Birthday paradox: 16 chars = 2^64 space = collision at ~2^32 hashes.
func HashMessages(messages []map[string]interface{}) string {
	h := sha256.New()
	for _, msg := range messages {
		// Hash role
		if role, ok := msg["role"].(string); ok {
			h.Write([]byte(role))
		}
		h.Write([]byte{'|'})

		// Hash content (can be string or array for multimodal)
		switch content := msg["content"].(type) {
		case string:
			h.Write([]byte(content))
		case []interface{}:
			// Multimodal content - hash each part
			for _, part := range content {
				if partMap, ok := part.(map[string]interface{}); ok {
					if partType, ok := partMap["type"].(string); ok {
						h.Write([]byte(partType))
						h.Write([]byte{':'})
					}
					if text, ok := partMap["text"].(string); ok {
						h.Write([]byte(text))
					}
					if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
						if url, ok := imageURL["url"].(string); ok {
							h.Write([]byte(url))
						}
					}
				}
			}
		}
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// GetStats returns statistics about the hash cache.
// This is useful for debugging and monitoring.
func (c *HashCache) GetStats() (count int, capacity int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.count, c.size
}
