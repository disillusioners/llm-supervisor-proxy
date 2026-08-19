package proxy

import (
	"context"
	"sync"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// UpstreamModelType represents the type of upstream model request
type UpstreamModelType upstreamModelType

const (
	// ModelTypeMain is the main model request
	ModelTypeMain UpstreamModelType = UpstreamModelType(modelTypeMain)
	// ModelTypeSecond is the second/parallel model request
	ModelTypeSecond UpstreamModelType = UpstreamModelType(modelTypeSecond)
	// ModelTypeFallback is the fallback model request
	ModelTypeFallback UpstreamModelType = UpstreamModelType(modelTypeFallback)
)

// NewUpstreamRequest creates a new upstream request for testing
func NewUpstreamRequest(id int, modelType UpstreamModelType, modelID string, maxBuffer int) *upstreamRequest {
	return newUpstreamRequest(id, upstreamModelType(modelType), modelID, maxBuffer)
}

// testProvider is a mock provider for testing that captures requests
type testProvider struct {
	name               string
	capturedReq        *providers.ChatCompletionRequest
	chatCompletionResp *providers.ChatCompletionResponse
	chatCompletionErr  error
	streamEvents       []providers.StreamEvent
	streamErr          error
	mu                 sync.Mutex
}

func newTestProvider() *testProvider {
	return &testProvider{name: "test"}
}

func (m *testProvider) Name() string {
	return m.name
}

func (m *testProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	m.mu.Lock()
	m.capturedReq = req
	m.mu.Unlock()

	if m.chatCompletionErr != nil {
		return nil, m.chatCompletionErr
	}
	return m.chatCompletionResp, nil
}

func (m *testProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	m.mu.Lock()
	m.capturedReq = req
	m.mu.Unlock()

	ch := make(chan providers.StreamEvent, len(m.streamEvents))
	events := make([]providers.StreamEvent, len(m.streamEvents))
	copy(events, m.streamEvents)
	go func() {
		defer close(ch)
		for _, e := range events {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (m *testProvider) IsRetryable(err error) bool {
	return false
}

func (m *testProvider) setChatCompletionResponse(resp *providers.ChatCompletionResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCompletionResp = resp
}

func (m *testProvider) SetStreamEvents(events []providers.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamEvents = events
}

func (m *testProvider) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCompletionErr = err
}

// GetCapturedRequest returns the captured request from the last call
func (m *testProvider) GetCapturedRequest() *providers.ChatCompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturedReq
}

// SetChatCompletionResponse sets the response for ChatCompletion calls
func (m *testProvider) SetChatCompletionResponse(resp *providers.ChatCompletionResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCompletionResp = resp
}

// TestHelpers provides exported test helpers for external test packages

// NewTestProvider creates a new test provider for E2E testing
func NewTestProvider() *testProvider {
	return newTestProvider()
}

// ExecuteInternalRequestWithProvider executes internal request with a custom provider
// This allows external tests to verify the full serialization chain
func ExecuteInternalRequestWithProvider(
	ctx context.Context,
	cfg *ConfigSnapshot,
	rawBody []byte,
	req *upstreamRequest,
	provider providers.Provider,
) error {
	// Save original provider factory
	originalNewProvider := newProviderClient

	// Replace with our test provider
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return provider, nil
	}

	// Execute the request (interleaved flag off by default — tests do
	// not exercise the MiniMax reasoning_details split-mode path; the
	// 5th-site race-ext body-map mutation is exercised separately
	// via P1-9 byte-identical negative tests).
	err := executeInternalRequest(ctx, cfg, rawBody, req, false)

	// Restore original
	newProviderClient = originalNewProvider

	return err
}
