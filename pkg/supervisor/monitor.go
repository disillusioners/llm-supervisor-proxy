package supervisor

import (
	"errors"
	"io"
	"time"
)

var ErrIdleTimeout = errors.New("stream idle timeout")

type MonitoredReader struct {
	reader      io.ReadCloser
	idleTimeout time.Duration
}

func NewMonitoredReader(r io.ReadCloser, timeout time.Duration) *MonitoredReader {
	return &MonitoredReader{
		reader:      r,
		idleTimeout: timeout,
	}
}

func (m *MonitoredReader) Read(p []byte) (n int, err error) {
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)

	go func() {
		n, err := m.reader.Read(p)
		// Use non-blocking send to prevent goroutine leak if caller abandons
		select {
		case ch <- readResult{n, err}:
			// Sent successfully
		default:
			// Caller abandoned, exit gracefully
		}
	}()

	timer := time.NewTimer(m.idleTimeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.n, res.err
	case <-timer.C:
		// Timeout occurred. We close the underlying reader to ensure the
		// read goroutine unblocks (it returns an error, usually "use of closed network connection" or similar).
		m.reader.Close()
		return 0, ErrIdleTimeout
	}
}

func (m *MonitoredReader) Close() error {
	return m.reader.Close()
}
