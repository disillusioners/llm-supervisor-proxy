package supervisor

import (
	"errors"
	"io"
	"log"
	"sync"
	"time"
)

var ErrIdleTimeout = errors.New("stream idle timeout")

// MonitoredReader wraps an io.ReadCloser with idle timeout detection.
// It uses a single persistent goroutine to avoid goroutine accumulation
// that occurred with the previous per-Read() goroutine spawning approach.
//
// How it works:
//  1. readLoop() goroutine is spawned once in NewMonitoredReader
//  2. readLoop() reads from upstream and sends data via channel
//  3. Read() waits for data with idle timeout
//  4. On timeout or Close(), done channel is closed, readLoop() exits
//  5. sync.WaitGroup ensures readLoop() completes before Close() returns
type MonitoredReader struct {
	reader      io.ReadCloser
	idleTimeout time.Duration
	readCh      chan readResult
	done        chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	closed      bool
	// Buffer for leftover data when caller's buffer is smaller than read data
	leftover []byte
	// pendingErr stores error from readLoop to return after leftover is exhausted
	pendingErr error
}

type readResult struct {
	data []byte
	err  error
}

// NewMonitoredReader creates a new monitored reader with idle timeout detection.
// IMPORTANT: This spawns a single persistent goroutine. Call Close() to stop the goroutine.
func NewMonitoredReader(r io.ReadCloser, timeout time.Duration) *MonitoredReader {
	m := &MonitoredReader{
		reader:      r,
		idleTimeout: timeout,
		readCh:      make(chan readResult, 1),
		done:        make(chan struct{}),
	}
	m.wg.Add(1)
	go m.readLoop()
	return m
}

// readLoop runs in a separate goroutine and continuously reads from upstream.
func (m *MonitoredReader) readLoop() {
	defer m.wg.Done()
	buf := make([]byte, 32*1024) // 32KB buffer

	for {
		n, err := m.reader.Read(buf)

		// CRITICAL FIX: Handle empty reads (n=0, err=nil)
		// Some HTTP implementations may return n=0 without error, which would cause
		// a busy loop where readCh keeps getting empty results and the idle timer
		// never fires. Skip sending empty results and retry after a small backoff.
		if n == 0 && err == nil {
			// Small backoff to prevent CPU spinning while waiting for data
			// This allows the idle timer in Read() to fire correctly
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Copy data to result (must copy since buf is reused)
		result := readResult{
			data: append([]byte(nil), buf[:n]...),
			err:  err,
		}

		select {
		case m.readCh <- result:
			if err != nil {
				return // Exit on error/EOF
			}
		case <-m.done:
			return
		}
	}
}

// Read reads data from the wrapped reader with idle timeout detection.
// Returns the number of bytes read and any error encountered.
func (m *MonitoredReader) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, errors.New("read on closed MonitoredReader")
	}

	// If we have leftover data from previous read, use it first
	if len(m.leftover) > 0 {
		copied := copy(p, m.leftover)
		m.leftover = m.leftover[copied:]
		if len(m.leftover) == 0 {
			m.leftover = nil // Release memory
			// If we had a pending error, return it now that leftover is exhausted
			if m.pendingErr != nil {
				pending := m.pendingErr
				m.pendingErr = nil
				m.mu.Unlock()
				return copied, pending
			}
		}
		m.mu.Unlock()
		return copied, nil
	}

	// If we have a pending error (from previous read with data+error), return it
	if m.pendingErr != nil {
		pending := m.pendingErr
		m.pendingErr = nil
		m.mu.Unlock()
		return 0, pending
	}
	m.mu.Unlock()

	timer := time.NewTimer(m.idleTimeout)
	defer timer.Stop()

	select {
	case res := <-m.readCh:
		// IMPORTANT: Data may come WITH an error (e.g., final chunk + EOF)
		// We must return data first, then return error on next Read()
		if len(res.data) > 0 {
			// Copy data to caller's buffer, store leftover if buffer is too small
			copied := copy(p, res.data)
			if copied < len(res.data) {
				m.mu.Lock()
				m.leftover = res.data[copied:]
				// Store error to return after leftover is exhausted
				if res.err != nil {
					m.pendingErr = res.err
				}
				m.mu.Unlock()
				return copied, nil
			}
			// If we have an error but returned all data, store error for next call
			if res.err != nil {
				m.mu.Lock()
				m.pendingErr = res.err
				m.mu.Unlock()
				return copied, nil
			}
			return copied, nil
		}
		// No data, just return the error
		if res.err != nil {
			return 0, res.err
		}
		return 0, nil
	case <-timer.C:
		// Timeout occurred - signal read loop to stop
		// Close done channel directly (safe because Close() checks m.closed first)
		m.mu.Lock()
		if !m.closed {
			m.closed = true
			close(m.done)
		}
		m.mu.Unlock()
		// Close the underlying reader to unblock the readLoop
		m.reader.Close()
		return 0, ErrIdleTimeout
	case <-m.done:
		// readLoop exited (error/EOF or Close() called)
		return 0, io.EOF
	}
}

// Close closes the underlying reader and stops the read loop goroutine.
func (m *MonitoredReader) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.done)
	m.mu.Unlock()

	// Wait for readLoop to finish with timeout
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	select {
	case <-done:
	case <-timeout.C:
		log.Printf("[MonitoredReader] Timeout waiting for readLoop to finish")
	}

	return m.reader.Close()
}
