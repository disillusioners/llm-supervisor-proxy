package normalizers

import (
	"sync"
)

// Registry manages all stream normalizers with thread-safe operations
type Registry struct {
	mu          sync.RWMutex
	normalizers map[string]StreamNormalizer
	enabled     map[string]bool
}

// NewRegistry creates a new normalizer registry
func NewRegistry() *Registry {
	return &Registry{
		normalizers: make(map[string]StreamNormalizer),
		enabled:     make(map[string]bool),
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

// Normalize applies all enabled normalizers to a line of SSE data
// Returns the (potentially modified) line and whether any normalizer modified it
func (r *Registry) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	// Get the enabled set
	r.mu.RLock()
	enabled := make(map[string]bool)
	for name, isEnabled := range r.enabled {
		enabled[name] = isEnabled
	}
	normalizers := make(map[string]StreamNormalizer)
	for name, n := range r.normalizers {
		normalizers[name] = n
	}
	r.mu.RUnlock()

	modified := false
	result := line

	// Apply each enabled normalizer in sequence
	for name, isEnabled := range enabled {
		if !isEnabled {
			continue
		}

		normalizer := normalizers[name]
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

// NormalizeWithName applies all enabled normalizers and returns which one modified the output
// Returns (normalizedLine, wasModified, normalizerName)
func (r *Registry) NormalizeWithName(line []byte, ctx *NormalizeContext) ([]byte, bool, string) {
	// Get the enabled set
	r.mu.RLock()
	enabled := make(map[string]bool)
	for name, isEnabled := range r.enabled {
		enabled[name] = isEnabled
	}
	normalizers := make(map[string]StreamNormalizer)
	for name, n := range r.normalizers {
		normalizers[name] = n
	}
	r.mu.RUnlock()

	modified := false
	result := line
	modifierName := ""

	// Apply each enabled normalizer in sequence
	for name, isEnabled := range enabled {
		if !isEnabled {
			continue
		}

		normalizer := normalizers[name]
		if normalizer == nil {
			continue
		}

		newResult, wasModified := normalizer.Normalize(result, ctx)
		if wasModified && !modified {
			// Only record the first normalizer that modifies the output
			modified = true
			modifierName = name
		}
		result = newResult
	}

	return result, modified, modifierName
}
