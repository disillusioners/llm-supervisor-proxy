package proxy

import (
	"errors"
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

	// Verify overflow flag is set
	if !sb.overflow {
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
