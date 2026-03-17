package normalizers

import (
	"log"
	"os"
	"sync"
)

var (
	// defaultRegistry is the global registry instance
	defaultRegistry *Registry
	once             sync.Once

	// globalEnabled controls whether normalization is globally enabled
	// When disabled, Normalize() returns line unchanged (fast path)
	globalEnabled bool
)

// init initializes the default registry with all normalizers
func init() {
	once.Do(func() {
		defaultRegistry = NewRegistry()

		// Register all normalizers
		defaultRegistry.Register(NewFixEmptyRoleNormalizer())
		defaultRegistry.Register(NewFixMissingToolCallIndexNormalizer())

		// Load configuration from environment
		cfg := LoadNormalizerConfig()
		globalEnabled = cfg.Enabled

		// Apply provider-specific overrides
		for provider, providerCfg := range cfg.ProviderNormalizers {
			if providerCfg.Enabled {
				enabledMap := make(map[string]bool)
				for _, n := range providerCfg.Normalizers {
					enabledMap[n] = true
				}
				defaultRegistry.SetProviderOverrides(provider, enabledMap)
			}
		}

		// Log registered normalizers at startup
		if logEnabled := os.Getenv("STREAM_NORMALIZER_DEBUG"); logEnabled == "true" {
			log.Printf("[STREAM NORMALIZER] Registered normalizers: %v", defaultRegistry.ListNormalizers())
			log.Printf("[STREAM NORMALIZER] Global enabled: %v", globalEnabled)
		}
	})
}

// SetEnabled globally enables or disables the normalizer
func SetEnabled(enabled bool) {
	globalEnabled = enabled
}

// IsEnabled returns whether the normalizer is globally enabled
func IsEnabled() bool {
	return globalEnabled
}

// GetRegistry returns the default registry instance
func GetRegistry() *Registry {
	return defaultRegistry
}

// Normalize applies all enabled normalizers to a line of SSE data
// This is the main entry point for normalizing streaming responses
// Returns (normalizedLine, wasModified)
func Normalize(line []byte, provider, requestID string) ([]byte, bool) {
	// Fast path: if global normalizer is disabled, skip processing
	if !globalEnabled {
		return line, false
	}

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
	// Fast path: if global normalizer is disabled, skip processing
	if !globalEnabled {
		return line, false
	}

	return defaultRegistry.Normalize(line, ctx)
}

// NewContext creates a new normalization context for a request
func NewContext(provider, requestID string) *NormalizeContext {
	return &NormalizeContext{
		Provider:  provider,
		RequestID: requestID,
	}
}
