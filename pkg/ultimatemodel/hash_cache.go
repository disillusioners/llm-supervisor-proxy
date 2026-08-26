package ultimatemodel

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// HashCache is a circular buffer of request hashes.
// It stores hashes of message content to detect duplicate requests.
// It also tracks the total attempt count per hash for the hardcoded
// ultimate-model trigger schedule (5/10/20/30/40, cap 40).
type HashCache struct {
	mu             sync.RWMutex
	hashes         []string       // circular buffer
	size           int            // max capacity
	head           int            // next write position
	count          int            // current count
	attemptCounter map[string]int // hash -> total attempt count
}

// NewHashCache creates a new hash cache with the given max size.
// If maxSize <= 0, defaults to 100.
func NewHashCache(maxSize int) *HashCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &HashCache{
		hashes:         make([]string, maxSize),
		size:           maxSize,
		head:           0,
		count:          0,
		attemptCounter: make(map[string]int),
	}
}

// RecordAttempt records the hash (inserting on first sight, with circular-
// buffer eviction cleanup of the counter map) and increments the attempt
// counter on EVERY call. Returns the total attempt count for this hash.
//
// First sight: the hash is inserted into the circular buffer (evicting the
// oldest hash and deleting its counter entry when full) AND the counter is
// set to 1. Subsequent calls: counter increment only.
func (c *HashCache) RecordAttempt(hash string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.containsLocked(hash) {
		c.attemptCounter[hash]++
		return c.attemptCounter[hash]
	}

	c.insertLocked(hash)
	c.attemptCounter[hash] = 1
	return 1
}

// StoreIfAbsent inserts the hash if not already present WITHOUT
// incrementing the attempt counter. Used only by the force-trigger
// branch: the insertion itself counts as attempt 1 (counter → 1), so the
// next normal RecordAttempt call returns 2.
//
// Insert only, no increment — with the same first-sight eviction semantics
// as RecordAttempt (evicting the oldest hash + counter entry when full).
// If the hash is already present, this is a no-op.
func (c *HashCache) StoreIfAbsent(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.containsLocked(hash) {
		return
	}

	c.insertLocked(hash)
	c.attemptCounter[hash] = 1
}

// containsLocked reports whether the hash is present in the circular
// buffer. Callers must hold c.mu.
func (c *HashCache) containsLocked(hash string) bool {
	for i := 0; i < c.count; i++ {
		if c.hashes[i] == hash {
			return true
		}
	}
	return false
}

// insertLocked stores the hash in the circular buffer, evicting the oldest
// hash and deleting its counter entry when the buffer is full (this
// prevents a memory leak in the attemptCounter map). Callers must hold
// c.mu and must have checked the hash is not already present.
func (c *HashCache) insertLocked(hash string) {
	if c.count >= c.size {
		evictedHash := c.hashes[c.head]
		if evictedHash != "" {
			delete(c.attemptCounter, evictedHash)
		}
	}

	c.hashes[c.head] = hash
	c.head = (c.head + 1) % c.size
	if c.count < c.size {
		c.count++
	}
}

// Remove removes a hash from the cache.
// This also clears the attempt counter for the hash (schedule re-arms).
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

	// Also clear attempt counter
	delete(c.attemptCounter, hash)
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
	c.attemptCounter = make(map[string]int) // Clear all attempt counters
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
