package proxy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Helper to create a minimal ConfigSnapshot for testing
func newTestConfigSnapshot(modelID string) *ConfigSnapshot {
	return &ConfigSnapshot{
		ModelID:            modelID,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
		RaceMaxParallel:    3,
		RaceParallelOnIdle: true,
	}
}

// Helper to create a minimal http.Request for testing
func newTestRequest() *http.Request {
	req, _ := http.NewRequest("POST", "http://localhost:4001/v1/chat/completions", nil)
	return req
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewRaceCoordinator(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")
	models := []string{"gpt-4", "claude-3"}
	rawBody := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), rawBody, models)

	if coord == nil {
		t.Fatal("newRaceCoordinator returned nil")
	}

	// Verify initial state
	if coord.baseCtx != ctx {
		t.Error("baseCtx not set correctly")
	}
	if coord.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if coord.req == nil {
		t.Error("req not set correctly")
	}
	if string(coord.rawBody) != string(rawBody) {
		t.Error("rawBody not set correctly")
	}
	if len(coord.models) != 2 {
		t.Errorf("models length = %d, want 2", len(coord.models))
	}
	if coord.winner != nil {
		t.Error("winner should be nil initially")
	}
	if coord.winnerIdx != -1 {
		t.Errorf("winnerIdx = %d, want -1", coord.winnerIdx)
	}
	if coord.failedCount != 0 {
		t.Errorf("failedCount = %d, want 0", coord.failedCount)
	}
	if coord.done == nil {
		t.Error("done channel should be initialized")
	}
	if coord.streamCh == nil {
		t.Error("streamCh channel should be initialized")
	}
	if coord.eventBus != nil {
		t.Error("eventBus should be nil when not provided")
	}
	if coord.requestID != "" {
		t.Error("requestID should be empty when not provided")
	}
}

func TestNewRaceCoordinatorWithEmptyModels(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")
	// Empty models slice
	models := []string{}

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), models)

	// Should default to cfg.ModelID
	if len(coord.models) != 1 {
		t.Errorf("models length = %d, want 1 (defaulted to cfg.ModelID)", len(coord.models))
	}
	if coord.models[0] != "gpt-4" {
		t.Errorf("models[0] = %s, want gpt-4", coord.models[0])
	}
}

func TestNewRaceCoordinatorWithEvents(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")
	models := []string{"gpt-4"}

	coord := newRaceCoordinatorWithEvents(ctx, cfg, newTestRequest(), []byte("{}"), models, nil, "test-request-id")

	if coord.eventBus != nil {
		t.Error("eventBus should be nil")
	}
	if coord.requestID != "test-request-id" {
		t.Errorf("requestID = %s, want test-request-id", coord.requestID)
	}
}

// =============================================================================
// GetWinner Tests
// =============================================================================

func TestGetWinnerInitiallyNil(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	if coord.GetWinner() != nil {
		t.Error("GetWinner() should return nil when no winner")
	}
}

func TestGetWinnerAfterSet(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Manually set a winner via internal state
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	coord.mu.Lock()
	coord.winner = req
	coord.winnerIdx = 0
	coord.mu.Unlock()

	winner := coord.GetWinner()
	if winner == nil {
		t.Fatal("GetWinner() returned nil after setting winner")
	}
	if winner.modelID != "gpt-4" {
		t.Errorf("winner.modelID = %s, want gpt-4", winner.modelID)
	}
	if winner.GetModelType() != modelTypeMain {
		t.Errorf("winner.modelType = %v, want %v", winner.GetModelType(), modelTypeMain)
	}
}

// =============================================================================
// GetStats Tests
// =============================================================================

func TestGetStatsInitiallyEmpty(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	stats := coord.GetStats()

	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}
	if stats.WinnerType != "" {
		t.Error("WinnerType should be empty initially")
	}
	if stats.WinnerModel != "" {
		t.Error("WinnerModel should be empty initially")
	}
	if stats.WinnerIndex != -1 {
		t.Errorf("WinnerIndex = %d, want -1", stats.WinnerIndex)
	}
	if stats.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", stats.FailedCount)
	}
	if stats.Duration == 0 {
		t.Error("Duration should be > 0")
	}
	if len(stats.SpawnTriggers) != 0 {
		t.Errorf("SpawnTriggers length = %d, want 0", len(stats.SpawnTriggers))
	}
}

func TestGetStatsWithWinner(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Set a winner
	req := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req.MarkCompleted()
	coord.mu.Lock()
	coord.winner = req
	coord.winnerIdx = 1
	coord.mu.Unlock()

	stats := coord.GetStats()

	if stats.WinnerType != "fallback" {
		t.Errorf("WinnerType = %s, want fallback", stats.WinnerType)
	}
	if stats.WinnerModel != "claude-3" {
		t.Errorf("WinnerModel = %s, want claude-3", stats.WinnerModel)
	}
	if stats.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1", stats.WinnerIndex)
	}
}

// =============================================================================
// GetRequestStatuses Tests
// =============================================================================

func TestGetRequestStatusesInitiallyAllNotStarted(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	statuses := coord.GetRequestStatuses()

	expected := map[string]string{
		"main":     "not_started",
		"second":   "not_started",
		"fallback": "not_started",
	}

	for key, want := range expected {
		if got := statuses[key]; got != want {
			t.Errorf("statuses[%s] = %s, want %s", key, got, want)
		}
	}
}

func TestGetRequestStatusesWithMainCompleted(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a completed main request
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkCompleted()
	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	statuses := coord.GetRequestStatuses()

	if statuses["main"] != "success" {
		t.Errorf("statuses[main] = %s, want success", statuses["main"])
	}
	if statuses["second"] != "not_started" {
		t.Errorf("statuses[second] = %s, want not_started", statuses["second"])
	}
	if statuses["fallback"] != "not_started" {
		t.Errorf("statuses[fallback] = %s, want not_started", statuses["fallback"])
	}
}

func TestGetRequestStatusesWithFailedRequests(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Add a failed main request
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("upstream error"))
	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	statuses := coord.GetRequestStatuses()

	if statuses["main"] != "failed" {
		t.Errorf("statuses[main] = %s, want failed", statuses["main"])
	}
}

func TestGetRequestStatusesWithMultipleRequestTypes(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Add main (completed), second (running), fallback (not started)
	mainReq := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	mainReq.MarkCompleted()

	secondReq := newUpstreamRequest(1, modelTypeSecond, "gpt-4", 1024)
	secondReq.MarkStarted()

	fallbackReq := newUpstreamRequest(2, modelTypeFallback, "claude-3", 1024)
	// Not started - stays pending

	coord.mu.Lock()
	coord.requests = append(coord.requests, mainReq, secondReq, fallbackReq)
	coord.mu.Unlock()

	statuses := coord.GetRequestStatuses()

	if statuses["main"] != "success" {
		t.Errorf("statuses[main] = %s, want success", statuses["main"])
	}
	if statuses["second"] != "not_started" {
		// Running is treated as not_started for status purposes
		t.Errorf("statuses[second] = %s, want not_started", statuses["second"])
	}
	if statuses["fallback"] != "not_started" {
		t.Errorf("statuses[fallback] = %s, want not_started", statuses["fallback"])
	}
}

// =============================================================================
// GetCommonFailureStatus Tests
// =============================================================================

func TestGetCommonFailureStatusNoRequests(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	status := coord.GetCommonFailureStatus()

	if status != 0 {
		t.Errorf("GetCommonFailureStatus() = %d, want 0 (no requests)", status)
	}
}

func TestGetCommonFailureStatusNoFailures(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a completed request
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkCompleted()
	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != 0 {
		t.Errorf("GetCommonFailureStatus() = %d, want 0 (no failures)", status)
	}
}

func TestGetCommonFailureStatusAllSameHTTPStatus(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Add two failed requests with same HTTP status
	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.MarkFailed(errors.New("rate limited"))
	req1.SetHTTPStatus(http.StatusTooManyRequests)

	req2 := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req2.MarkFailed(errors.New("rate limited"))
	req2.SetHTTPStatus(http.StatusTooManyRequests)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1, req2)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != http.StatusTooManyRequests {
		t.Errorf("GetCommonFailureStatus() = %d, want %d", status, http.StatusTooManyRequests)
	}
}

func TestGetCommonFailureStatusDifferentHTTPStatus(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Add two failed requests with different HTTP statuses
	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.MarkFailed(errors.New("rate limited"))
	req1.SetHTTPStatus(http.StatusTooManyRequests)

	req2 := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req2.MarkFailed(errors.New("server error"))
	req2.SetHTTPStatus(http.StatusInternalServerError)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1, req2)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != 0 {
		t.Errorf("GetCommonFailureStatus() = %d, want 0 (different statuses)", status)
	}
}

func TestGetCommonFailureStatusParsedFromError(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request without HTTP status, but with error containing status code
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("upstream returned error: 429 Too Many Requests"))
	// No SetHTTPStatus call, so httpStatusCode is 0

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != http.StatusTooManyRequests {
		t.Errorf("GetCommonFailureStatus() = %d, want %d", status, http.StatusTooManyRequests)
	}
}

func TestGetCommonFailureStatusTimeoutError(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with timeout error
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("context deadline exceeded: idle timeout"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != http.StatusGatewayTimeout {
		t.Errorf("GetCommonFailureStatus() = %d, want %d", status, http.StatusGatewayTimeout)
	}
}

func TestGetCommonFailureStatusBufferLimitError(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with buffer limit error
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("response exceeds buffer limit"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("GetCommonFailureStatus() = %d, want %d", status, http.StatusRequestEntityTooLarge)
	}
}

func TestGetCommonFailureStatusGenericError(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with generic error (defaults to 502)
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("connection refused"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	status := coord.GetCommonFailureStatus()

	if status != http.StatusBadGateway {
		t.Errorf("GetCommonFailureStatus() = %d, want %d", status, http.StatusBadGateway)
	}
}

// =============================================================================
// GetFinalErrorInfo Tests
// =============================================================================

func TestGetFinalErrorInfoNoRequests(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusBadGateway {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusBadGateway)
	}
	if info.ErrorType != models.ErrorTypeServerError {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeServerError)
	}
	if info.Message != "No upstream requests were made" {
		t.Errorf("Message = %s, want 'No upstream requests were made'", info.Message)
	}
}

func TestGetFinalErrorInfoAllFailedRateLimit(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Add two failed requests with 429 status
	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.MarkFailed(errors.New("rate limited"))
	req1.SetHTTPStatus(http.StatusTooManyRequests)

	req2 := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req2.MarkFailed(errors.New("rate limited"))
	req2.SetHTTPStatus(http.StatusTooManyRequests)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1, req2)
	coord.mu.Unlock()

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusTooManyRequests)
	}
	if info.ErrorType != models.ErrorTypeRateLimit {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeRateLimit)
	}
	if info.ErrorCode != models.ErrorCodeRateLimit {
		t.Errorf("ErrorCode = %s, want %s", info.ErrorCode, models.ErrorCodeRateLimit)
	}
	if info.Message != "All models rate limited" {
		t.Errorf("Message = %s, want 'All models rate limited'", info.Message)
	}
}

func TestGetFinalErrorInfoContextOverflow(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with context overflow error
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("error code: context_length_exceeded - This model's maximum context window is 8192 tokens"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusBadRequest)
	}
	if info.ErrorType != models.ErrorTypeContextOverflow {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeContextOverflow)
	}
	if info.ErrorCode != "" {
		t.Errorf("ErrorCode = %s, want empty (no rate_limit code for context overflow)", info.ErrorCode)
	}
}

func TestGetFinalErrorInfoBadGateway(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with generic error
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("connection refused"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusBadGateway {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusBadGateway)
	}
	if info.ErrorType != models.ErrorTypeUpstreamError {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeUpstreamError)
	}
	if info.ErrorCode != models.ErrorCodeUnavailable {
		t.Errorf("ErrorCode = %s, want %s", info.ErrorCode, models.ErrorCodeUnavailable)
	}
}

func TestGetFinalErrorInfoServiceUnavailable(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with 503 status
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("service unavailable"))
	req.SetHTTPStatus(http.StatusServiceUnavailable)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusServiceUnavailable)
	}
	if info.ErrorType != models.ErrorTypeTooManyRequests {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeTooManyRequests)
	}
	if info.ErrorCode != models.ErrorCodeUnavailable {
		t.Errorf("ErrorCode = %s, want %s", info.ErrorCode, models.ErrorCodeUnavailable)
	}
}

func TestGetFinalErrorInfoGatewayTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Add a failed request with timeout error
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req.MarkFailed(errors.New("context deadline exceeded: idle timeout"))

	coord.mu.Lock()
	coord.requests = append(coord.requests, req)
	coord.mu.Unlock()

	info := coord.GetFinalErrorInfo()

	if info.HTTPStatus != http.StatusGatewayTimeout {
		t.Errorf("HTTPStatus = %d, want %d", info.HTTPStatus, http.StatusGatewayTimeout)
	}
	if info.ErrorType != models.ErrorTypeUpstreamError {
		t.Errorf("ErrorType = %s, want %s", info.ErrorType, models.ErrorTypeUpstreamError)
	}
}

// =============================================================================
// GetStreamDeadlineError Tests
// =============================================================================

func TestGetStreamDeadlineErrorInitiallyNil(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	err := coord.GetStreamDeadlineError()

	if err != nil {
		t.Errorf("GetStreamDeadlineError() = %v, want nil", err)
	}
}

func TestGetStreamDeadlineErrorAfterSet(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Set stream deadline error
	expectedErr := &FinalErrorInfo{
		HTTPStatus: http.StatusGatewayTimeout,
		ErrorType:  models.ErrorTypeUpstreamError,
		ErrorCode:  "",
		Message:    "Request timeout - no response received",
	}

	coord.mu.Lock()
	coord.streamDeadlineError = expectedErr
	coord.mu.Unlock()

	err := coord.GetStreamDeadlineError()

	if err == nil {
		t.Fatal("GetStreamDeadlineError() returned nil after setting")
	}
	if err.HTTPStatus != expectedErr.HTTPStatus {
		t.Errorf("HTTPStatus = %d, want %d", err.HTTPStatus, expectedErr.HTTPStatus)
	}
	if err.Message != expectedErr.Message {
		t.Errorf("Message = %s, want %s", err.Message, expectedErr.Message)
	}
}

// =============================================================================
// CancelAll Tests
// =============================================================================

func TestCancelAllCancelsAllRequests(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Create requests with cancel contexts
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.SetContext(ctx1, cancel1)

	req2 := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req2.SetContext(ctx2, cancel2)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1, req2)
	coord.mu.Unlock()

	// Track context cancellations
	cancelled1 := make(chan bool, 1)
	cancelled2 := make(chan bool, 1)

	go func() {
		<-ctx1.Done()
		cancelled1 <- true
	}()
	go func() {
		<-ctx2.Done()
		cancelled2 <- true
	}()

	// Give goroutines time to set up listeners
	time.Sleep(10 * time.Millisecond)

	// Call cancelAll
	coord.cancelAll()

	// Wait for cancellations with timeout
	select {
	case <-cancelled1:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("Request 1 was not cancelled")
	}

	select {
	case <-cancelled2:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("Request 2 was not cancelled")
	}
}

func TestCancelAllExceptNilCancelsAll(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Create request with cancel context
	ctx1, cancel1 := context.WithCancel(context.Background())

	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.SetContext(ctx1, cancel1)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1)
	coord.mu.Unlock()

	cancelled := make(chan bool, 1)
	go func() {
		<-ctx1.Done()
		cancelled <- true
	}()

	time.Sleep(10 * time.Millisecond)

	// Call cancelAllExcept with nil (should cancel all)
	coord.cancelAllExcept(nil)

	select {
	case <-cancelled:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("Request was not cancelled when cancelAllExcept(nil) called")
	}
}

func TestCancelAllExceptWinnerPreservesWinner(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4", "claude-3"})

	// Create two requests
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	req1 := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	req1.SetContext(ctx1, cancel1)

	req2 := newUpstreamRequest(1, modelTypeFallback, "claude-3", 1024)
	req2.SetContext(ctx2, cancel2)

	coord.mu.Lock()
	coord.requests = append(coord.requests, req1, req2)
	coord.mu.Unlock()

	// Track context cancellations
	cancelled1 := make(chan bool, 1)
	cancelled2 := make(chan bool, 1)

	go func() {
		<-ctx1.Done()
		cancelled1 <- true
	}()
	go func() {
		<-ctx2.Done()
		cancelled2 <- true
	}()

	time.Sleep(10 * time.Millisecond)

	// Call cancelAllExcept with req1 as winner (should only cancel req2)
	coord.cancelAllExcept(req1)

	select {
	case <-cancelled1:
		t.Error("Winner should not have been cancelled")
	case <-time.After(50 * time.Millisecond):
		// OK - winner should NOT be cancelled
	}

	select {
	case <-cancelled2:
		// OK - non-winner should be cancelled
	case <-time.After(100 * time.Millisecond):
		t.Error("Non-winner was not cancelled")
	}
}

// =============================================================================
// WaitForWinner Tests
// =============================================================================

func TestWaitForWinnerWithBaseCtxDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Cancel context immediately
	cancel()

	// Should return immediately with nil (context done)
	result := coord.WaitForWinner()

	if result != nil {
		t.Error("WaitForWinner() should return nil when base context is done")
	}
}

func TestWaitForWinnerWithStreamChClosed(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Set a winner
	req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)
	coord.mu.Lock()
	coord.winner = req
	coord.winnerIdx = 0
	coord.mu.Unlock()

	// Close streamCh to signal winner found
	close(coord.streamCh)

	// Should return the winner
	result := coord.WaitForWinner()

	if result == nil {
		t.Error("WaitForWinner() should return winner when streamCh is closed")
	}
	if result.modelID != "gpt-4" {
		t.Errorf("winner.modelID = %s, want gpt-4", result.modelID)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestCoordinatorWithNilHTTPRequest(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	// Should not panic with nil request
	coord := newRaceCoordinator(ctx, cfg, nil, []byte("{}"), []string{"gpt-4"})

	if coord == nil {
		t.Fatal("Coordinator should be created even with nil request")
	}
}

func TestCoordinatorWithNilRawBody(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	// Should not panic with nil rawBody
	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), nil, []string{"gpt-4"})

	if coord == nil {
		t.Fatal("Coordinator should be created even with nil rawBody")
	}
}

func TestGetStatsWithSpawnTriggers(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfigSnapshot("gpt-4")

	coord := newRaceCoordinator(ctx, cfg, newTestRequest(), []byte("{}"), []string{"gpt-4"})

	// Manually add spawn triggers
	coord.mu.Lock()
	coord.spawnTriggers = append(coord.spawnTriggers, spawnTriggerInfo{
		trigger:       triggerIdleTimeout,
		errorMessage:  "",
		failedRequest: -1,
	})
	coord.spawnTriggers = append(coord.spawnTriggers, spawnTriggerInfo{
		trigger:       triggerMainError,
		errorMessage:  "upstream error",
		failedRequest: 0,
	})
	coord.mu.Unlock()

	stats := coord.GetStats()

	if len(stats.SpawnTriggers) != 2 {
		t.Errorf("SpawnTriggers length = %d, want 2", len(stats.SpawnTriggers))
	}
	if stats.SpawnTriggers[0] != "idle_timeout" {
		t.Errorf("SpawnTriggers[0] = %s, want idle_timeout", stats.SpawnTriggers[0])
	}
	if stats.SpawnTriggers[1] != "main_error" {
		t.Errorf("SpawnTriggers[1] = %s, want main_error", stats.SpawnTriggers[1])
	}
}
