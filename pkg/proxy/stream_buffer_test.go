package proxy

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewStreamBuffer(t *testing.T) {
	tests := []struct {
		name     string
		maxBytes int64
		wantMax  int64
	}{
		{
			name:     "zero maxBytes uses default",
			maxBytes: 0,
			wantMax:  defaultMaxBufferBytes,
		},
		{
			name:     "negative maxBytes uses default",
			maxBytes: -100,
			wantMax:  defaultMaxBufferBytes,
		},
		{
			name:     "positive maxBytes uses provided value",
			maxBytes: 1000,
			wantMax:  1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(tt.maxBytes)
			if sb == nil {
				t.Fatal("newStreamBuffer returned nil")
			}
			if sb.maxBytes != tt.wantMax {
				t.Errorf("maxBytes = %d, want %d", sb.maxBytes, tt.wantMax)
			}
			if sb.completed != 0 {
				t.Errorf("completed = %d, want 0", sb.completed)
			}
			if sb.totalLen != 0 {
				t.Errorf("totalLen = %d, want 0", sb.totalLen)
			}
			if sb.done == nil {
				t.Error("done channel is nil")
			}
			if sb.notifyCh == nil {
				t.Error("notifyCh channel is nil")
			}
		})
	}
}

func TestStreamBufferAdd(t *testing.T) {
	tests := []struct {
		name       string
		maxBytes   int64
		chunk      []byte
		wantResult bool
		wantLen    int64
	}{
		{
			name:       "normal add returns true",
			maxBytes:   1000,
			chunk:      []byte("hello"),
			wantResult: true,
			wantLen:    6, // "hello" + newline
		},
		{
			name:       "empty chunk still adds with newline",
			maxBytes:   1000,
			chunk:      []byte(""),
			wantResult: true,
			wantLen:    1, // just newline
		},
		{
			name:       "overflow returns false",
			maxBytes:   10,
			chunk:      []byte("hello world"),
			wantResult: false,
			wantLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(tt.maxBytes)
			got := sb.Add(tt.chunk)
			if got != tt.wantResult {
				t.Errorf("Add() = %v, want %v", got, tt.wantResult)
			}
			if got && sb.TotalLen() != tt.wantLen {
				t.Errorf("TotalLen() = %d, want %d", sb.TotalLen(), tt.wantLen)
			}
		})
	}
}

func TestStreamBufferAddMultiple(t *testing.T) {
	sb := newStreamBuffer(1000)

	// Add multiple chunks
	chunks := [][]byte{
		[]byte("chunk1"),
		[]byte("chunk2"),
		[]byte("chunk3"),
	}

	for _, chunk := range chunks {
		if !sb.Add(chunk) {
			t.Errorf("Add(%q) returned false, want true", string(chunk))
		}
	}

	// Verify chunks are stored correctly
	result, _ := sb.GetChunksFrom(0)
	if len(result) != 3 {
		t.Errorf("GetChunksFrom(0) returned %d chunks, want 3", len(result))
	}

	// Verify each chunk ends with newline
	for i, chunk := range result {
		if len(chunk) == 0 || chunk[len(chunk)-1] != '\n' {
			t.Errorf("chunk[%d] does not end with newline", i)
		}
	}

	// Verify total length: 6 + 1 + 6 + 1 + 6 + 1 = 21
	expectedLen := int64(0)
	for _, chunk := range chunks {
		expectedLen += int64(len(chunk) + 1) // +1 for newline
	}
	if sb.TotalLen() != expectedLen {
		t.Errorf("TotalLen() = %d, want %d", sb.TotalLen(), expectedLen)
	}
}

func TestStreamBufferAddAfterClose(t *testing.T) {
	sb := newStreamBuffer(1000)
	sb.Close(nil)

	if sb.Add([]byte("after close")) {
		t.Error("Add() returned true after Close(), want false")
	}
}

func TestStreamBufferAddOverflow(t *testing.T) {
	sb := newStreamBuffer(10) // Small buffer

	// First add should succeed
	if !sb.Add([]byte("hi")) {
		t.Error("Add(hello) returned false on first add")
	}

	// Second add should fail due to overflow
	if sb.Add([]byte("hello world this is too long")) {
		t.Error("Add() returned true on overflow, want false")
	}

	// Verify overflow flag is set (atomic read)
	if atomic.LoadUint32(&sb.overflow) == 0 {
		t.Error("overflow flag not set after overflow")
	}
}

func TestStreamBufferClose(t *testing.T) {
	tests := []struct {
		name       string
		numAdds    int
		closeCalls int
		wantDone   bool
	}{
		{
			name:       "single close marks complete",
			numAdds:    5,
			closeCalls: 1,
			wantDone:   true,
		},
		{
			name:       "idempotent close",
			numAdds:    3,
			closeCalls: 2,
			wantDone:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)

			// Add some chunks first
			for i := 0; i < tt.numAdds; i++ {
				sb.Add([]byte("data"))
			}

			// Close multiple times
			for i := 0; i < tt.closeCalls; i++ {
				sb.Close(nil)
			}

			if !sb.IsComplete() {
				t.Error("IsComplete() = false, want true after Close()")
			}

			// Verify done channel is closed
			select {
			case <-sb.Done():
				// Channel is closed (expected)
			default:
				t.Error("Done() channel is not closed")
			}
		})
	}
}

func TestStreamBufferGetChunksFrom(t *testing.T) {
	tests := []struct {
		name        string
		setupChunks [][]byte
		fromIndex   int
		wantNil     bool
		wantCount   int
		wantNextIdx int
	}{
		{
			name:        "no chunks returns nil",
			setupChunks: nil,
			fromIndex:   0,
			wantNil:     true,
			wantCount:   0,
			wantNextIdx: 0,
		},
		{
			name:        "from index beyond length returns nil",
			setupChunks: [][]byte{[]byte("a"), []byte("b")},
			fromIndex:   10,
			wantNil:     true,
			wantCount:   0,
			wantNextIdx: 10,
		},
		{
			name:        "returns chunks from index",
			setupChunks: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
			fromIndex:   1,
			wantNil:     false,
			wantCount:   2,
			wantNextIdx: 3,
		},
		{
			name:        "returns all chunks from zero",
			setupChunks: [][]byte{[]byte("a"), []byte("b")},
			fromIndex:   0,
			wantNil:     false,
			wantCount:   2,
			wantNextIdx: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)
			for _, chunk := range tt.setupChunks {
				sb.Add(chunk)
			}

			result, nextIdx := sb.GetChunksFrom(tt.fromIndex)

			if tt.wantNil && result != nil {
				t.Errorf("GetChunksFrom() returned non-nil, want nil")
			}
			if !tt.wantNil && result == nil {
				t.Errorf("GetChunksFrom() returned nil, want non-nil")
			}
			if len(result) != tt.wantCount {
				t.Errorf("GetChunksFrom() returned %d chunks, want %d", len(result), tt.wantCount)
			}
			if nextIdx != tt.wantNextIdx {
				t.Errorf("nextIdx = %d, want %d", nextIdx, tt.wantNextIdx)
			}
		})
	}
}

func TestStreamBufferGetChunksFromFiltersNil(t *testing.T) {
	sb := newStreamBuffer(1000)
	sb.Add([]byte("a"))
	sb.Add([]byte("b"))
	sb.Add([]byte("c"))

	// Prune to set first chunk to nil
	sb.Prune(1)

	// Get chunks - should filter out nil
	result, _ := sb.GetChunksFrom(0)
	if len(result) != 2 {
		t.Errorf("GetChunksFrom() returned %d chunks, want 2 (nil filtered)", len(result))
	}
}

func TestStreamBufferPrune(t *testing.T) {
	sb := newStreamBuffer(1000)

	// Add chunks
	for i := 0; i < 5; i++ {
		sb.Add([]byte("data"))
	}

	// Prune first 3 chunks
	sb.Prune(3)

	// Verify first 3 are nil, rest are not
	for i := 0; i < 5; i++ {
		sb.mu.RLock()
		chunk := sb.chunks[i]
		sb.mu.RUnlock()
		if i < 3 && chunk != nil {
			t.Errorf("chunks[%d] is not nil after Prune(3)", i)
		}
		if i >= 3 && chunk == nil {
			t.Errorf("chunks[%d] is nil before Prune", i)
		}
	}
}

func TestStreamBufferPruneBeyondLength(t *testing.T) {
	sb := newStreamBuffer(1000)

	// Add only 2 chunks
	sb.Add([]byte("a"))
	sb.Add([]byte("b"))

	// Try to prune more than exists
	sb.Prune(10) // Should not panic

	// All chunks are now nil due to prune, so GetChunksFrom returns nil
	result, _ := sb.GetChunksFrom(0)
	if result != nil {
		t.Errorf("GetChunksFrom() should return nil when all chunks are pruned, got %d chunks", len(result))
	}
}

func TestStreamBufferTotalLen(t *testing.T) {
	tests := []struct {
		name      string
		chunks    [][]byte
		wantTotal int64
	}{
		{
			name:      "empty buffer",
			chunks:    nil,
			wantTotal: 0,
		},
		{
			name:      "single chunk",
			chunks:    [][]byte{[]byte("hello")},
			wantTotal: 6, // "hello" + newline
		},
		{
			name: "multiple chunks",
			chunks: [][]byte{
				[]byte("hi"),
				[]byte("there"),
				[]byte("friend"),
			},
			wantTotal: 16, // "hi\n"=3 + "there\n"=6 + "friend\n"=7
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)
			for _, chunk := range tt.chunks {
				sb.Add(chunk)
			}
			if sb.TotalLen() != tt.wantTotal {
				t.Errorf("TotalLen() = %d, want %d", sb.TotalLen(), tt.wantTotal)
			}
		})
	}
}

func TestStreamBufferIsComplete(t *testing.T) {
	sb := newStreamBuffer(1000)

	// Before close
	if sb.IsComplete() {
		t.Error("IsComplete() = true before Close(), want false")
	}

	// After close
	sb.Close(nil)
	if !sb.IsComplete() {
		t.Error("IsComplete() = false after Close(), want true")
	}
}

func TestStreamBufferNotifyCh(t *testing.T) {
	sb := newStreamBuffer(1000)

	// Get notification channel
	notifyCh := sb.NotifyCh()
	if notifyCh == nil {
		t.Error("NotifyCh() returned nil")
	}

	// Non-blocking receive should work
	select {
	case <-notifyCh:
		// Channel may or may not have pending signal
	default:
		// No pending signal (expected initially)
	}
}

func TestStreamBufferDone(t *testing.T) {
	tests := []struct {
		name         string
		preCloseWait bool
		postClose    bool
	}{
		{
			name:         "before close channel is open",
			preCloseWait: true,
			postClose:    false,
		},
		{
			name:         "after close channel is closed",
			preCloseWait: false,
			postClose:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)
			doneCh := sb.Done()

			if tt.preCloseWait {
				// Verify channel is not closed
				select {
				case <-doneCh:
					t.Error("Done() channel closed before Close()")
				case <-time.After(10 * time.Millisecond):
					// Timeout means channel is not closed (expected)
				}
			}

			sb.Close(nil)

			// Verify channel is closed
			select {
			case <-doneCh:
				// Channel is closed (expected)
			default:
				t.Error("Done() channel not closed after Close()")
			}
		})
	}
}

func TestStreamBufferErr(t *testing.T) {
	tests := []struct {
		name     string
		closeErr error
		wantErr  bool
		wantMsg  string
	}{
		{
			name:     "nil error before close",
			closeErr: nil,
			wantErr:  false,
		},
		{
			name:     "nil error after close",
			closeErr: nil,
			wantErr:  false,
		},
		{
			name:     "error after close",
			closeErr: errors.New("stream error"),
			wantErr:  true,
			wantMsg:  "stream error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)

			// Check before close
			if sb.Err() != nil {
				t.Error("Err() != nil before Close()")
			}

			// Close with error
			sb.Close(tt.closeErr)

			// Check after close
			gotErr := sb.Err()
			if tt.wantErr {
				if gotErr == nil {
					t.Error("Err() = nil, want non-nil")
				} else if gotErr.Error() != tt.wantMsg {
					t.Errorf("Err() = %q, want %q", gotErr.Error(), tt.wantMsg)
				}
			} else {
				if gotErr != nil {
					t.Errorf("Err() = %v, want nil", gotErr)
				}
			}
		})
	}
}

func TestStreamBufferGetAllRawBytes(t *testing.T) {
	tests := []struct {
		name      string
		chunks    [][]byte
		wantEmpty bool
		wantLen   int
	}{
		{
			name:      "no chunks returns empty",
			chunks:    nil,
			wantEmpty: true,
			wantLen:   0,
		},
		{
			name:      "empty chunks returns empty",
			chunks:    [][]byte{},
			wantEmpty: true,
			wantLen:   0,
		},
		{
			name: "single chunk",
			chunks: [][]byte{
				[]byte("hello"),
			},
			wantEmpty: false,
			wantLen:   6, // "hello" + newline
		},
		{
			name: "multiple chunks combined",
			chunks: [][]byte{
				[]byte("hi"),
				[]byte("there"),
			},
			wantEmpty: false,
			wantLen:   9, // "hi\n"=3 + "there\n"=6
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := newStreamBuffer(1000)
			for _, chunk := range tt.chunks {
				sb.Add(chunk)
			}

			result := sb.GetAllRawBytes()
			if tt.wantEmpty {
				if len(result) != 0 {
					t.Errorf("GetAllRawBytes() returned %d bytes, want 0", len(result))
				}
			} else {
				if len(result) != tt.wantLen {
					t.Errorf("GetAllRawBytes() returned %d bytes, want %d", len(result), tt.wantLen)
				}
				// Verify result contains expected content
				expected := []byte("hi\nthere\n")
				if tt.name == "single chunk" {
					expected = []byte("hello\n")
				}
				if string(result) != string(expected) {
					t.Errorf("GetAllRawBytes() = %q, want %q", string(result), string(expected))
				}
			}
		})
	}
}

func TestStreamBufferGetAllRawBytesWithPrune(t *testing.T) {
	sb := newStreamBuffer(1000)
	sb.Add([]byte("a"))
	sb.Add([]byte("b"))
	sb.Add([]byte("c"))

	// Prune first chunk
	sb.Prune(1)

	// GetAllRawBytes should still return all non-nil chunks
	result := sb.GetAllRawBytes()
	// "b\n" + "c\n" = 4 bytes
	if len(result) != 4 {
		t.Errorf("GetAllRawBytes() = %d bytes after prune, want 4", len(result))
	}
}

func TestChunkPool(t *testing.T) {
	pool := newChunkPool()

	// Test Get with small size
	chunk := pool.Get(100)
	if cap(chunk) < 100 {
		t.Errorf("Get(100) returned chunk with capacity %d, want >= 100", cap(chunk))
	}
	if len(chunk) != 100 {
		t.Errorf("Get(100) returned chunk with length %d, want 100", len(chunk))
	}
	pool.Put(chunk)

	// Test Get with larger size
	chunk2 := pool.Get(4000)
	if cap(chunk2) < 4000 {
		t.Errorf("Get(4000) returned chunk with capacity %d, want >= 4000", cap(chunk2))
	}
	pool.Put(chunk2)

	// Test Get with very large size (shouldn't pool)
	chunk3 := pool.Get(100 * 1024)
	if cap(chunk3) < 100*1024 {
		t.Errorf("Get(100*1024) returned chunk with capacity %d, want >= 100KB", cap(chunk3))
	}
	// Very large chunks should not be returned to pool (we don't call Put for them)

	// Test that reused chunks have correct data
	chunk4 := pool.Get(50)
	for i := range chunk4 {
		chunk4[i] = byte(i % 256)
	}
	pool.Put(chunk4)

	chunk5 := pool.Get(50)
	// Verify chunk is reused (may or may not have old data depending on pool implementation)
	if cap(chunk5) < 50 {
		t.Errorf("Get(50) after put returned chunk with capacity %d, want >= 50", cap(chunk5))
	}
}

func TestChunkPoolSizes(t *testing.T) {
	tests := []struct {
		name    string
		minSize int
	}{
		{"tiny", 100},
		{"small", 1000},
		{"medium", 4000},
		{"large", 8000},
		{"xlarge", 64 * 1024},
	}

	pool := newChunkPool()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := pool.Get(tt.minSize)
			if len(chunk) != tt.minSize {
				t.Errorf("Get(%d) len = %d, want %d", tt.minSize, len(chunk), tt.minSize)
			}
			if cap(chunk) < tt.minSize {
				t.Errorf("Get(%d) cap = %d, want >= %d", tt.minSize, cap(chunk), tt.minSize)
			}
			pool.Put(chunk)
		})
	}
}

func TestChunkPoolVeryLargeChunks(t *testing.T) {
	pool := newChunkPool()

	// Very large chunks (>64KB) should be allocated but not pooled
	// They should still work correctly
	chunk := pool.Get(128 * 1024)
	if len(chunk) != 128*1024 {
		t.Errorf("Get(128*1024) len = %d, want %d", len(chunk), 128*1024)
	}

	// Write some data
	for i := 0; i < len(chunk); i++ {
		chunk[i] = byte(i % 256)
	}

	// Verify data
	for i := 0; i < len(chunk); i++ {
		if chunk[i] != byte(i%256) {
			t.Errorf("chunk[%d] = %d, want %d", i, chunk[i], byte(i%256))
			break
		}
	}
}

func BenchmarkChunkPoolGet(b *testing.B) {
	pool := newChunkPool()
	for i := 0; i < b.N; i++ {
		chunk := pool.Get(1000)
		pool.Put(chunk)
	}
}

func BenchmarkChunkAllocation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		chunk := make([]byte, 1000)
		_ = chunk
	}
}

// === Memory Optimization Tests ===

func TestStreamBufferGetAllRawBytesOnce(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add some data
	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	// First call should build cache
	result1 := sb.GetAllRawBytesOnce()

	// Second call should return cached result
	result2 := sb.GetAllRawBytesOnce()

	if &result1[0] != &result2[0] {
		t.Error("GetAllRawBytesOnce should return cached result")
	}
}

func TestStreamBufferCacheInvalidation(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	sb.Add([]byte("chunk1"))
	result1 := sb.GetAllRawBytesOnce()

	sb.Add([]byte("chunk2"))
	result2 := sb.GetAllRawBytesOnce()

	if &result1[0] == &result2[0] {
		t.Error("Cache should be invalidated after Add")
	}
}

func TestStreamBufferGetChunksFromFastPath(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	chunks1, nextIndex := sb.GetChunksFrom(0)
	chunks2, _ := sb.GetChunksFrom(0)

	// Fast path returns defensive copy, so slices should have same content but different backing arrays
	if len(chunks1) != len(chunks2) {
		t.Error("Fast path chunks should have same length")
	}
	if &chunks1[0] == &chunks2[0] {
		t.Error("Fast path should return defensive copy with different backing array")
	}

	// Next index should be 2 (len of chunks)
	if nextIndex != 2 {
		t.Errorf("Expected nextIndex=2, got %d", nextIndex)
	}
}

func TestStreamBufferShouldPrune(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add 4 chunks
	for i := 0; i < 4; i++ {
		sb.Add([]byte("chunk"))
	}

	// readIndex=0 should NOT trigger prune
	if sb.ShouldPrune(0) {
		t.Error("ShouldPrune(0) should be false")
	}

	// readIndex=1 should NOT trigger prune (1 <= 4/2)
	if sb.ShouldPrune(1) {
		t.Error("ShouldPrune(1) should be false")
	}

	// readIndex=2 should NOT trigger prune (2 == 4/2)
	if sb.ShouldPrune(2) {
		t.Error("ShouldPrune(2) should be false")
	}

	// readIndex=3 should trigger prune (3 > 4/2)
	if !sb.ShouldPrune(3) {
		t.Error("ShouldPrune(3) should be true")
	}
}

func TestStreamBufferShouldPruneEmptyBuffer(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Empty buffer - no pruning needed
	if sb.ShouldPrune(0) {
		t.Error("ShouldPrune(0) on empty buffer should be false")
	}
	if sb.ShouldPrune(1) {
		t.Error("ShouldPrune(1) on empty buffer should be false")
	}
}

func TestStreamBufferGetChunksFromWithPrunedChunks(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add 4 chunks
	for i := 0; i < 4; i++ {
		sb.Add([]byte("chunk"))
	}

	// Prune first 2 chunks
	sb.Prune(2)

	// GetChunksFrom(0) should skip nil chunks
	chunks, nextIndex := sb.GetChunksFrom(0)

	// Should have 2 remaining chunks
	if len(chunks) != 2 {
		t.Errorf("Expected 2 chunks after pruning, got %d", len(chunks))
	}

	// Next index should be 4 (original length)
	if nextIndex != 4 {
		t.Errorf("Expected nextIndex=4, got %d", nextIndex)
	}
}

func TestStreamBufferGetAllRawBytesOnceConcurrency(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add some data
	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	var wg sync.WaitGroup
	results := make([][]byte, 10)

	// Launch 10 concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = sb.GetAllRawBytesOnce()
		}(i)
	}

	wg.Wait()

	// All results should have the same memory address (cached)
	for i := 1; i < 10; i++ {
		if &results[0][0] != &results[i][0] {
			t.Errorf("Result %d has different address than result 0", i)
		}
	}
}

func TestStreamBufferInvalidateCacheOnPrune(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add data
	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	// First call builds cache
	result1 := sb.GetAllRawBytesOnce()

	// Prune should invalidate cache
	sb.Prune(1)

	// Next call should rebuild cache
	result2 := sb.GetAllRawBytesOnce()

	// result2 should have fewer bytes since we pruned
	if len(result2) >= len(result1) {
		t.Error("Cache should be invalidated after prune, result should be smaller")
	}
}

// Test 1: GetChunksFrom fast path disabled after Prune
func TestStreamBufferFastPathDisabledAfterPrune(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add data
	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	// First call uses fast path (before pruning)
	chunks1, _ := sb.GetChunksFrom(0)
	_ = chunks1 // Verify fast path works

	// Prune - this sets chunks to nil
	sb.Prune(1)

	// After pruning, fast path should be disabled (slow path handles nil chunks)
	// GetChunksFrom should use slow path and filter out nil chunks
	chunks2, _ := sb.GetChunksFrom(0)

	// Should have 1 remaining chunk (nil chunk was filtered)
	if len(chunks2) != 1 {
		t.Errorf("Expected 1 chunk after pruning, got %d", len(chunks2))
	}
}

// Test 2: GetAllRawBytesOnce after Close
func TestStreamBufferGetAllRawBytesOnceAfterClose(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add data
	sb.Add([]byte("chunk1"))
	sb.Add([]byte("chunk2"))

	// Cache the result
	result1 := sb.GetAllRawBytesOnce()

	// Close the buffer
	sb.Close(nil)

	// GetAllRawBytesOnce should still work (cache was invalidated, rebuilt from chunks)
	result2 := sb.GetAllRawBytesOnce()

	// Should return valid data
	if len(result2) == 0 {
		t.Error("GetAllRawBytesOnce after Close should return remaining chunks")
	}

	// result2 should be same as result1 (content unchanged after close)
	if string(result1) != string(result2) {
		t.Errorf("GetAllRawBytesOnce content changed after Close: got %q, want %q", string(result2), string(result1))
	}
}

// Test 3: ShouldPrune boundary when readIndex == len(chunks)
func TestStreamBufferShouldPruneBoundaryAtEnd(t *testing.T) {
	sb := newStreamBuffer(1024 * 1024)

	// Add 4 chunks
	for i := 0; i < 4; i++ {
		sb.Add([]byte("chunk"))
	}

	// readIndex == len(chunks) (all consumed) should NOT trigger prune
	if sb.ShouldPrune(4) {
		t.Error("ShouldPrune(4) should be false when len(chunks)=4 (all consumed)")
	}

	// readIndex == len(chunks) - 1 (one remaining) SHOULD trigger prune
	// (past halfway point: 3 > 4/2 = 2)
	if !sb.ShouldPrune(3) {
		t.Error("ShouldPrune(3) should be true (past halfway, one chunk remaining)")
	}
}
