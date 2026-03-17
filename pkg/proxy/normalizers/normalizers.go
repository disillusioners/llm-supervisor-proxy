package normalizers

import (
	"sync"
)

var (
	// defaultRegistry is the global registry instance
	defaultRegistry *Registry
	once            sync.Once
)

// init initializes the default registry with all normalizers
func init() {
	once.Do(func() {
		defaultRegistry = NewRegistry()

		// Register all normalizers (both enabled by default)
		defaultRegistry.Register(NewFixEmptyRoleNormalizer())
		defaultRegistry.Register(NewFixMissingToolCallIndexNormalizer())
	})
}

// GetRegistry returns the default registry instance
func GetRegistry() *Registry {
	return defaultRegistry
}

// Normalize applies all enabled normalizers to a line of SSE data
// This is the main entry point for normalizing streaming responses
// Returns (normalizedLine, wasModified)
func Normalize(line []byte, provider, requestID string) ([]byte, bool) {
	ctx := &NormalizeContext{
		Provider:  provider,
		RequestID: requestID,
	}

	// Reset normalizers state for this new request
	defaultRegistry.ResetAll(ctx)

	// Apply normalization
	return defaultRegistry.Normalize(line, ctx)
}

// NormalizeWithContext applies normalization with an existing context
// Use this when you need to maintain state across multiple chunks in a stream
func NormalizeWithContext(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	return defaultRegistry.Normalize(line, ctx)
}

// NormalizeWithContextAndName applies normalization and returns which normalizer made the modification
// Returns (normalizedLine, wasModified, normalizerName)
func NormalizeWithContextAndName(line []byte, ctx *NormalizeContext) ([]byte, bool, string) {
	return defaultRegistry.NormalizeWithName(line, ctx)
}

// NewContext creates a new normalization context for a request
func NewContext(provider, requestID string) *NormalizeContext {
	return &NormalizeContext{
		Provider:  provider,
		RequestID: requestID,
	}
}
