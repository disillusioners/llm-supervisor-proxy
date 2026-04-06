package proxy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewUpstreamRequest(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

	if req.id != 0 {
		t.Errorf("newUpstreamRequest().id = %d, want 0", req.id)
	}
	if req.modelType != modelTypeMain {
		t.Errorf("newUpstreamRequest().modelType = %v, want %v", req.modelType, modelTypeMain)
	}
	if req.modelID != "gpt-4" {
		t.Errorf("newUpstreamRequest().modelID = %s, want gpt-4", req.modelID)
	}
	if req.status != statusPending {
		t.Errorf("newUpstreamRequest().status = %v, want %v", req.status, statusPending)
	}
	if req.buffer == nil {
		t.Error("newUpstreamRequest().buffer = nil, want non-nil buffer")
	}
	if req.err != nil {
		t.Errorf("newUpstreamRequest().err = %v, want nil", req.err)
	}
}

func TestUpstreamRequestMarkStarted(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

	if req.status != statusPending {
		t.Errorf("initial status = %v, want %v", req.status, statusPending)
	}

	req.MarkStarted()

	if req.status != statusRunning {
		t.Errorf("MarkStarted() status = %v, want %v", req.status, statusRunning)
	}
	if req.startTime.IsZero() {
		t.Error("MarkStarted() startTime is zero")
	}
}

func TestUpstreamRequestMarkStreaming(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	beforeTime := time.Now()

	req.MarkStreaming()

	if req.status != statusStreaming {
		t.Errorf("MarkStreaming() status = %v, want %v", req.status, statusStreaming)
	}
	if req.firstByteTime.IsZero() {
		t.Error("MarkStreaming() firstByteTime is zero")
	}
	if req.firstByteTime.Before(beforeTime) {
		t.Error("MarkStreaming() firstByteTime is before expected time")
	}
	if req.lastActivityTime.IsZero() {
		t.Error("MarkStreaming() lastActivityTime is zero")
	}
}

func TestUpstreamRequestMarkCompleted(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkStreaming()

	// Add some data to buffer
	req.buffer.Add([]byte("test data"))

	req.MarkCompleted()

	if req.status != statusCompleted {
		t.Errorf("MarkCompleted() status = %v, want %v", req.status, statusCompleted)
	}
	if req.completionTime.IsZero() {
		t.Error("MarkCompleted() completionTime is zero")
	}
	if !req.buffer.IsComplete() {
		t.Error("MarkCompleted() buffer should be complete")
	}
}

func TestUpstreamRequestMarkFailed(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkStreaming()
	testErr := errors.New("upstream error")

	req.MarkFailed(testErr)

	if req.status != statusFailed {
		t.Errorf("MarkFailed() status = %v, want %v", req.status, statusFailed)
	}
	if req.err != testErr {
		t.Errorf("MarkFailed() err = %v, want %v", req.err, testErr)
	}
	if req.completionTime.IsZero() {
		t.Error("MarkFailed() completionTime is zero")
	}
	if !req.buffer.IsComplete() {
		t.Error("MarkFailed() buffer should be complete")
	}
	if req.buffer.Err() != testErr {
		t.Errorf("MarkFailed() buffer.Err() = %v, want %v", req.buffer.Err(), testErr)
	}
}

func TestUpstreamRequestSetContext(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	ctx, cancel := context.WithCancel(context.Background())

	req.SetContext(ctx, cancel)

	// Check context is set (can't directly compare contexts, but we can verify fields)
	if req.ctx == nil {
		t.Error("SetContext() ctx = nil, want non-nil context")
	}
	if req.cancel == nil {
		t.Error("SetContext() cancel = nil, want non-nil cancel function")
	}
}

func TestUpstreamRequestGetStatus(t *testing.T) {
	tests := []struct {
		name           string
		action         func(*upstreamRequest)
		expectedStatus upstreamStatus
	}{
		{
			name:           "pending initially",
			action:         func(r *upstreamRequest) {},
			expectedStatus: statusPending,
		},
		{
			name:           "after MarkStarted",
			action:         func(r *upstreamRequest) { r.MarkStarted() },
			expectedStatus: statusRunning,
		},
		{
			name:           "after MarkStreaming",
			action:         func(r *upstreamRequest) { r.MarkStreaming() },
			expectedStatus: statusStreaming,
		},
		{
			name:           "after MarkCompleted",
			action:         func(r *upstreamRequest) { r.MarkCompleted() },
			expectedStatus: statusCompleted,
		},
		{
			name:           "after MarkFailed",
			action:         func(r *upstreamRequest) { r.MarkFailed(errors.New("test")) },
			expectedStatus: statusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.action(req)
			if got := req.GetStatus(); got != tt.expectedStatus {
				t.Errorf("GetStatus() = %v, want %v", got, tt.expectedStatus)
			}
		})
	}
}

func TestUpstreamRequestGetters(t *testing.T) {
	req := newUpstreamRequest(42, modelTypeFallback, "claude-3", 2048)

	if got := req.GetID(); got != 42 {
		t.Errorf("GetID() = %d, want 42", got)
	}
	if got := req.GetModelType(); got != modelTypeFallback {
		t.Errorf("GetModelType() = %v, want %v", got, modelTypeFallback)
	}
	if got := req.GetModelID(); got != "claude-3" {
		t.Errorf("GetModelID() = %s, want claude-3", got)
	}
}

func TestUpstreamRequestGetBuffer(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	buffer := req.GetBuffer()

	if buffer == nil {
		t.Error("GetBuffer() = nil, want non-nil buffer")
	}
	if buffer != req.buffer {
		t.Error("GetBuffer() returned different buffer")
	}
}

func TestUpstreamRequestGetError(t *testing.T) {
	tests := []struct {
		name      string
		action    func(*upstreamRequest)
		wantError bool
	}{
		{
			name:      "no error initially",
			action:    func(r *upstreamRequest) {},
			wantError: false,
		},
		{
			name:      "no error after MarkStarted",
			action:    func(r *upstreamRequest) { r.MarkStarted() },
			wantError: false,
		},
		{
			name:      "no error after MarkStreaming",
			action:    func(r *upstreamRequest) { r.MarkStreaming() },
			wantError: false,
		},
		{
			name:      "no error after MarkCompleted",
			action:    func(r *upstreamRequest) { r.MarkCompleted() },
			wantError: false,
		},
		{
			name:      "has error after MarkFailed",
			action:    func(r *upstreamRequest) { r.MarkFailed(errors.New("test error")) },
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.action(req)
			err := req.GetError()
			if tt.wantError && err == nil {
				t.Error("GetError() = nil, want non-nil error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("GetError() = %v, want nil", err)
			}
		})
	}
}

func TestUpstreamRequestIsDone(t *testing.T) {
	tests := []struct {
		name     string
		action   func(*upstreamRequest)
		wantDone bool
	}{
		{
			name:     "pending is not done",
			action:   func(r *upstreamRequest) {},
			wantDone: false,
		},
		{
			name:     "running is not done",
			action:   func(r *upstreamRequest) { r.MarkStarted() },
			wantDone: false,
		},
		{
			name:     "streaming is not done",
			action:   func(r *upstreamRequest) { r.MarkStreaming() },
			wantDone: false,
		},
		{
			name:     "completed is done",
			action:   func(r *upstreamRequest) { r.MarkCompleted() },
			wantDone: true,
		},
		{
			name:     "failed is done",
			action:   func(r *upstreamRequest) { r.MarkFailed(errors.New("test")) },
			wantDone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.action(req)
			if got := req.IsDone(); got != tt.wantDone {
				t.Errorf("IsDone() = %v, want %v", got, tt.wantDone)
			}
		})
	}
}

func TestUpstreamRequestIsCompleted(t *testing.T) {
	tests := []struct {
		name          string
		action        func(*upstreamRequest)
		wantCompleted bool
	}{
		{
			name:          "pending is not completed",
			action:        func(r *upstreamRequest) {},
			wantCompleted: false,
		},
		{
			name:          "running is not completed",
			action:        func(r *upstreamRequest) { r.MarkStarted() },
			wantCompleted: false,
		},
		{
			name:          "streaming is not completed",
			action:        func(r *upstreamRequest) { r.MarkStreaming() },
			wantCompleted: false,
		},
		{
			name:          "completed is completed",
			action:        func(r *upstreamRequest) { r.MarkCompleted() },
			wantCompleted: true,
		},
		{
			name:          "failed is not completed",
			action:        func(r *upstreamRequest) { r.MarkFailed(errors.New("test")) },
			wantCompleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.action(req)
			if got := req.IsCompleted(); got != tt.wantCompleted {
				t.Errorf("IsCompleted() = %v, want %v", got, tt.wantCompleted)
			}
		})
	}
}

func TestUpstreamRequestIsStreaming(t *testing.T) {
	tests := []struct {
		name       string
		action     func(*upstreamRequest)
		wantStream bool
	}{
		{
			name:       "pending is not streaming",
			action:     func(r *upstreamRequest) {},
			wantStream: false,
		},
		{
			name:       "running is not streaming",
			action:     func(r *upstreamRequest) { r.MarkStarted() },
			wantStream: false,
		},
		{
			name:       "streaming is streaming",
			action:     func(r *upstreamRequest) { r.MarkStreaming() },
			wantStream: true,
		},
		{
			name:       "completed is not streaming",
			action:     func(r *upstreamRequest) { r.MarkCompleted() },
			wantStream: false,
		},
		{
			name:       "failed is not streaming",
			action:     func(r *upstreamRequest) { r.MarkFailed(errors.New("test")) },
			wantStream: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.action(req)
			if got := req.IsStreaming(); got != tt.wantStream {
				t.Errorf("IsStreaming() = %v, want %v", got, tt.wantStream)
			}
		})
	}
}

func TestUpstreamRequestCancel(t *testing.T) {
	t.Run("cancel with cancel function set", func(t *testing.T) {
		req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
		ctx, cancel := context.WithCancel(context.Background())
		req.SetContext(ctx, cancel)

		canceled := false
		go func() {
			<-ctx.Done()
			canceled = true
		}()

		// Give goroutine time to set up listener
		time.Sleep(time.Millisecond)

		req.Cancel()

		// Give cancel time to propagate
		time.Sleep(time.Millisecond)

		if !canceled {
			t.Error("Cancel() did not trigger context cancellation")
		}
	})

	t.Run("cancel with nil cancel function", func(t *testing.T) {
		req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
		// Don't set context/cancel - they should be nil

		// Should not panic
		req.Cancel()
	})
}

func TestUpstreamRequestTrackActivity(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	beforeActivity := time.Now()

	req.TrackActivity()

	if req.lastActivityTime.IsZero() {
		t.Error("TrackActivity() lastActivityTime is zero")
	}
	if req.lastActivityTime.Before(beforeActivity) {
		t.Error("TrackActivity() lastActivityTime is before expected time")
	}
}

func TestUpstreamRequestGetLastActivity(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

	// Initially zero time
	if !req.GetLastActivity().IsZero() {
		t.Error("GetLastActivity() should be zero initially")
	}

	req.TrackActivity()

	if req.GetLastActivity().IsZero() {
		t.Error("GetLastActivity() after TrackActivity() is zero")
	}
}

func TestUpstreamRequestIsIdle(t *testing.T) {
	idleTimeout := 100 * time.Millisecond

	tests := []struct {
		name   string
		setup  func(*upstreamRequest)
		isIdle bool
	}{
		{
			name:   "not streaming returns false",
			setup:  func(r *upstreamRequest) {},
			isIdle: false,
		},
		{
			name:   "running returns false",
			setup:  func(r *upstreamRequest) { r.MarkStarted() },
			isIdle: false,
		},
		{
			name:   "streaming with recent activity returns false",
			setup:  func(r *upstreamRequest) { r.MarkStreaming(); r.TrackActivity() },
			isIdle: false,
		},
		{
			name: "streaming with old activity returns true",
			setup: func(r *upstreamRequest) {
				r.MarkStreaming()
				r.mu.Lock()
				r.lastActivityTime = time.Now().Add(-idleTimeout - time.Millisecond)
				r.mu.Unlock()
			},
			isIdle: true,
		},
		{
			name:   "completed returns false",
			setup:  func(r *upstreamRequest) { r.MarkCompleted() },
			isIdle: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
			tt.setup(req)
			if got := req.IsIdle(idleTimeout); got != tt.isIdle {
				t.Errorf("IsIdle() = %v, want %v", got, tt.isIdle)
			}
		})
	}
}

func TestUpstreamRequestUsage(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

	// Initially nil
	if req.GetUsage() != nil {
		t.Error("GetUsage() should be nil initially")
	}

	usage := &TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	req.SetUsage(usage)
	got := req.GetUsage()

	if got == nil {
		t.Fatal("GetUsage() = nil, want non-nil")
	}
	if got.PromptTokens != 100 {
		t.Errorf("GetUsage().PromptTokens = %d, want 100", got.PromptTokens)
	}
	if got.CompletionTokens != 50 {
		t.Errorf("GetUsage().CompletionTokens = %d, want 50", got.CompletionTokens)
	}
	if got.TotalTokens != 150 {
		t.Errorf("GetUsage().TotalTokens = %d, want 150", got.TotalTokens)
	}
}

func TestUpstreamRequestHTTPStatus(t *testing.T) {
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

	// Initially 0
	if req.GetHTTPStatus() != 0 {
		t.Errorf("GetHTTPStatus() = %d, want 0 initially", req.GetHTTPStatus())
	}

	req.SetHTTPStatus(200)
	if got := req.GetHTTPStatus(); got != 200 {
		t.Errorf("GetHTTPStatus() = %d, want 200", got)
	}

	req.SetHTTPStatus(500)
	if got := req.GetHTTPStatus(); got != 500 {
		t.Errorf("GetHTTPStatus() = %d, want 500", got)
	}
}
