package supervisor

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// mockSlowReader simulates a reader that blocks for a duration before returning data
type mockSlowReader struct {
	data        []byte
	readDelay   time.Duration
	returnError error
	closed      bool
}

func (m *mockSlowReader) Read(p []byte) (n int, err error) {
	if m.returnError != nil {
		return 0, m.returnError
	}
	if m.closed {
		return 0, errors.New("read on closed reader")
	}
	if m.readDelay > 0 {
		time.Sleep(m.readDelay)
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, m.data)
	m.data = m.data[n:]
	return n, nil
}

func (m *mockSlowReader) Close() error {
	m.closed = true
	return nil
}

func TestMonitoredReader_BasicRead(t *testing.T) {
	data := []byte("hello world")
	reader := &mockSlowReader{data: data}
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	buf := make([]byte, len(data))
	n, err := mr.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != len(data) {
		t.Errorf("Read() n = %d, want %d", n, len(data))
	}
	if string(buf[:n]) != string(data) {
		t.Errorf("Read() data = %q, want %q", buf[:n], data)
	}
}

func TestMonitoredReader_IdleTimeout(t *testing.T) {
	// Reader that blocks longer than the idle timeout
	reader := &mockSlowReader{data: []byte("test"), readDelay: 2 * time.Second}
	mr := NewMonitoredReader(reader, 100*time.Millisecond)
	defer mr.Close()

	buf := make([]byte, 100)
	start := time.Now()
	_, err := mr.Read(buf)
	elapsed := time.Since(start)

	if err != ErrIdleTimeout {
		t.Errorf("Read() error = %v, want %v", err, ErrIdleTimeout)
	}
	// Should timeout around 100ms, not 5+ seconds
	if elapsed > 500*time.Millisecond {
		t.Errorf("Read() took %v, should have timed out around 100ms", elapsed)
	}
}

func TestMonitoredReader_ReadAfterClose(t *testing.T) {
	data := []byte("test")
	reader := &mockSlowReader{data: data}
	mr := NewMonitoredReader(reader, 1*time.Second)

	// Close immediately
	mr.Close()

	buf := make([]byte, 100)
	_, err := mr.Read(buf)
	if err == nil {
		t.Error("Read() after Close() should return error")
	}
}

func TestMonitoredReader_DoubleClose(t *testing.T) {
	data := []byte("test")
	reader := &mockSlowReader{data: data}
	mr := NewMonitoredReader(reader, 1*time.Second)

	// Double close should not panic
	mr.Close()
	mr.Close()
}

func TestMonitoredReader_EOF(t *testing.T) {
	reader := &mockSlowReader{data: []byte{}} // Empty data = EOF
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	buf := make([]byte, 100)
	_, err := mr.Read(buf)
	if err != io.EOF {
		t.Errorf("Read() error = %v, want %v", err, io.EOF)
	}
}

func TestMonitoredReader_ReadError(t *testing.T) {
	testErr := errors.New("test error")
	reader := &mockSlowReader{returnError: testErr}
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	buf := make([]byte, 100)
	_, err := mr.Read(buf)
	if err != testErr {
		t.Errorf("Read() error = %v, want %v", err, testErr)
	}
}

func TestMonitoredReader_ConcurrentClose(t *testing.T) {
	// Test that Close() properly waits for readLoop to finish
	reader := &mockSlowReader{data: make([]byte, 1000), readDelay: 50 * time.Millisecond}
	mr := NewMonitoredReader(reader, 1*time.Second)

	// Start a read that will block
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 100)
		mr.Read(buf)
		close(readDone)
	}()

	// Give read time to start
	time.Sleep(20 * time.Millisecond)

	// Close should wait for readLoop
	start := time.Now()
	mr.Close()
	elapsed := time.Since(start)

	// Close should complete within reasonable time (not hang)
	if elapsed > 2*time.Second {
		t.Errorf("Close() took %v, should complete quickly", elapsed)
	}

	// Wait for read to complete
	select {
	case <-readDone:
	case <-time.After(1 * time.Second):
		t.Error("Read() did not complete after Close()")
	}
}

func TestMonitoredReader_BytesReader(t *testing.T) {
	// Test with a real bytes.Reader
	data := []byte("hello world this is a test")
	reader := io.NopCloser(bytes.NewReader(data))
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	// Read all data using multiple Read calls
	received := make([]byte, 0, len(data))
	buf := make([]byte, 10) // Small buffer to test multiple reads
	for {
		n, err := mr.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		received = append(received, buf[:n]...)
	}

	if string(received) != string(data) {
		t.Errorf("Received %q, want %q", received, data)
	}
}

func TestMonitoredReader_LargeData(t *testing.T) {
	// Test with larger data to ensure buffer handling is correct
	data := make([]byte, 100*1024) // 100KB
	for i := range data {
		data[i] = byte(i % 256)
	}
	reader := io.NopCloser(bytes.NewReader(data))
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	// Read all data using io.ReadAll
	received, err := io.ReadAll(mr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	if len(received) != len(data) {
		t.Errorf("Received %d bytes, want %d", len(received), len(data))
	}
	for i := range data {
		if received[i] != data[i] {
			t.Errorf("Data mismatch at byte %d: got %d, want %d", i, received[i], data[i])
			break
		}
	}
}

func TestMonitoredReader_SmallBuffer(t *testing.T) {
	// Test that small caller buffers work correctly with large read chunks
	data := []byte("this is a test string that is longer than the small buffer")
	reader := io.NopCloser(bytes.NewReader(data))
	mr := NewMonitoredReader(reader, 1*time.Second)
	defer mr.Close()

	// Read with very small buffer (smaller than readLoop's 32KB buffer)
	buf := make([]byte, 5)
	received := make([]byte, 0, len(data))
	for {
		n, err := mr.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		received = append(received, buf[:n]...)
	}

	if string(received) != string(data) {
		t.Errorf("Received %q, want %q", received, data)
	}
}

func TestMonitoredReader_TimeoutThenClose(t *testing.T) {
	// Test that calling Close() after timeout doesn't panic
	reader := &mockSlowReader{data: []byte("test"), readDelay: 2 * time.Second}
	mr := NewMonitoredReader(reader, 100*time.Millisecond)

	buf := make([]byte, 100)
	_, err := mr.Read(buf)
	if err != ErrIdleTimeout {
		t.Errorf("Read() error = %v, want %v", err, ErrIdleTimeout)
	}

	// Close after timeout should not panic
	mr.Close()
}
