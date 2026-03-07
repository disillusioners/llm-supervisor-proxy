package toolrepair

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const fixerSystemPrompt = "You are a JSON repair tool. Fix malformed JSON and return ONLY the corrected JSON. No explanations, no markdown code blocks, just valid JSON."

// FixerFunc is the function signature for calling an LLM to fix JSON
// Returns the fixed JSON string or an error
type FixerFunc func(ctx context.Context, model string, prompt string) (string, error)

// Fixer uses an LLM to repair malformed JSON
type Fixer struct {
	fixerFunc FixerFunc
	config    *Config
}

// NewFixer creates a new Fixer
func NewFixer(fixerFunc FixerFunc, config *Config) *Fixer {
	return &Fixer{fixerFunc: fixerFunc, config: config}
}

// Fix attempts to repair malformed JSON using an LLM
func (f *Fixer) Fix(ctx context.Context, malformedJSON string) (string, error) {
	// Size check
	if f.config.MaxArgumentsSize > 0 && len(malformedJSON) > f.config.MaxArgumentsSize {
		return "", fmt.Errorf("JSON too large: %d bytes", len(malformedJSON))
	}

	// Timeout context
	timeout := time.Duration(f.config.FixerTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build prompt
	prompt := buildFixerUserPrompt(malformedJSON)

	// Call fixer
	fixed, err := f.fixerFunc(ctx, f.config.FixerModel, prompt)
	if err != nil {
		return "", fmt.Errorf("fixer request failed: %w", err)
	}

	// Validate
	fixed = strings.TrimSpace(fixed)
	if !isValidJSON(fixed) {
		return "", fmt.Errorf("fixer returned invalid JSON")
	}

	return fixed, nil
}

func buildFixerUserPrompt(malformedJSON string) string {
	return "Fix this JSON. Return ONLY valid JSON:\n\n" + malformedJSON
}
