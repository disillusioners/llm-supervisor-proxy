package normalizers

import (
	"sync"
)

// Registry manages all stream normalizers with thread-safe operations
type Registry struct {
	mu          sync.RWMutex
	normalizers map[string]StreamNormalizer
	enabled     map[string]bool

	// providerOverrides stores per-provider enabled/disabled normalizers
	// Key: provider name (e.g., "zhipu"), Value: map of normalizer name to enabled state
	providerOverrides map[string]map[string]bool
}

// NewRegistry creates a new normalizer registry
func NewRegistry() *Registry {
	return &Registry{
		normalizers:      make(map[string]StreamNormalizer),
		enabled:          make(map[string]bool),
		providerOverrides: make(map[string]map[string]bool),
	}
}

// Register adds a normalizer to the registry
func (r *Registry) Register(n StreamNormalizer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.normalizers[n.Name()] = n
	// Enable by default if the normalizer says so
	r.enabled[n.Name()] = n.EnabledByDefault()
}

// Enable turns on a normalizer by name
func (r *Registry) Enable(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.normalizers[name]; exists {
		r.enabled[name] = true
	}
}

// Disable turns off a normalizer by name
func (r *Registry) Disable(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.enabled[name] = false
}

// IsEnabled returns true if a normalizer is enabled
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.enabled[name]
}

// SetProviderOverrides sets enabled/disabled state for normalizers for a specific provider
// This allows per-provider configuration
func (r *Registry) SetProviderOverrides(provider string, enabled map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providerOverrides[provider] = enabled
}

// GetEnabledForProvider returns the set of enabled normalizers for a given provider
// Returns the default enabled set if no override is configured
func (r *Registry) GetEnabledForProvider(provider string) map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Check for provider-specific override
	if override, exists := r.providerOverrides[provider]; exists {
		result := make(map[string]bool)
		for name := range r.normalizers {
			if enabled, ok := override[name]; ok {
				result[name] = enabled
			} else {
				// Use default if not specified in override
				result[name] = r.enabled[name]
			}
		}
		return result
	}

	// Return default enabled set
	result := make(map[string]bool)
	for name, enabled := range r.enabled {
		result[name] = enabled
	}
	return result
}

// Normalize applies all enabled normalizers to a line of SSE data
// Returns the (potentially modified) line and whether any normalizer modified it
func (r *Registry) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	// Fast path: if no normalizers are enabled, return as-is
	r.mu.RLock()
	hasAnyEnabled := false
	for _, enabled := range r.enabled {
		if enabled {
			hasAnyEnabled = true
			break
		}
	}
	if !hasAnyEnabled {
		r.mu.RUnlock()
		return line, false
	}

	// Get enabled set for this provider
	enabledForProvider := r.GetEnabledForProvider(ctx.Provider)
	r.mu.RUnlock()

	// No normalizers enabled for this provider
	if len(enabledForProvider) == 0 {
		return line, false
	}

	modified := false
	result := line

	// Apply each enabled normalizer in sequence
	for name, isEnabled := range enabledForProvider {
		if !isEnabled {
			continue
		}

		normalizer := r.getNormalizer(name)
		if normalizer == nil {
			continue
		}

		newResult, wasModified := normalizer.Normalize(result, ctx)
		if wasModified {
			modified = true
			result = newResult
		}
	}

	return result, modified
}

// ResetAll calls Reset on all registered normalizers
func (r *Registry) ResetAll(ctx *NormalizeContext) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, n := range r.normalizers {
		n.Reset(ctx)
	}
}

// getNormalizer returns a normalizer by name (internal, must hold lock)
func (r *Registry) getNormalizer(name string) StreamNormalizer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.normalizers[name]
}

// ListNormalizers returns all registered normalizer names
func (r *Registry) ListNormalizers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.normalizers))
	for name := range r.normalizers {
		names = append(names, name)
	}
	return names
}
