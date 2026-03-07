package toolrepair

import (
	"encoding/json"
	"time"
)

// Config holds configuration for the tool repair system
type Config struct {
	// Enabled enables or disables tool repair
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Strategies is the ordered list of repair strategies to attempt
	// Available strategies: "extract_json", "library_repair", "escape_quotes", "remove_reasoning", "close_brackets"
	Strategies []string `json:"strategies" yaml:"strategies"`

	// MaxArgumentsSize is the maximum size of tool arguments in bytes (0 = unlimited)
	MaxArgumentsSize int `json:"max_arguments_size" yaml:"max_arguments_size"`

	// MaxToolCallsPerResponse is the maximum number of tool calls per response (0 = unlimited)
	MaxToolCallsPerResponse int `json:"max_tool_calls_per_response" yaml:"max_tool_calls_per_response"`

	// LogOriginal logs the original malformed JSON for debugging
	LogOriginal bool `json:"log_original" yaml:"log_original"`

	// LogRepaired logs the repaired JSON for verification
	LogRepaired bool `json:"log_repaired" yaml:"log_repaired"`

	// RetryEnabled enables retry with prompt injection when repair fails
	RetryEnabled bool `json:"retry_enabled" yaml:"retry_enabled"`

	// MaxRetries is the maximum number of retries (0 = no retry)
	MaxRetries int `json:"max_retries" yaml:"max_retries"`

	// RetryPrompt is the prompt template for retry attempts
	RetryPrompt string `json:"retry_prompt" yaml:"retry_prompt"`

	// MaxRepairDuration is the maximum time to spend on repair (0 = unlimited)
	MaxRepairDuration time.Duration `json:"max_repair_duration" yaml:"max_repair_duration"`
}

// DefaultConfig returns the default configuration for tool repair
func DefaultConfig() *Config {
	return &Config{
		Enabled:                 true,
		Strategies:              []string{"extract_json", "library_repair", "escape_quotes", "remove_reasoning"},
		MaxArgumentsSize:        10 * 1024, // 10KB
		MaxToolCallsPerResponse: 8,
		LogOriginal:             false,
		LogRepaired:             true,
		RetryEnabled:            true,
		MaxRetries:              1,
		RetryPrompt:             "The previous tool call arguments were invalid JSON. Return only valid JSON matching the tool schema.",
		MaxRepairDuration:       500 * time.Millisecond,
	}
}

// DisabledConfig returns a configuration with repair disabled
func DisabledConfig() *Config {
	return &Config{
		Enabled: false,
	}
}

// MarshalJSON customizes JSON marshaling to convert time.Duration to milliseconds
func (c Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	return json.Marshal(&struct {
		MaxRepairDuration int64 `json:"max_repair_duration"` // in milliseconds
		*Alias
	}{
		MaxRepairDuration: c.MaxRepairDuration.Milliseconds(),
		Alias:             (*Alias)(&c),
	})
}

// UnmarshalJSON customizes JSON unmarshaling to convert milliseconds to time.Duration
func (c *Config) UnmarshalJSON(data []byte) error {
	type Alias Config
	aux := &struct {
		MaxRepairDuration int64 `json:"max_repair_duration"` // in milliseconds
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	c.MaxRepairDuration = time.Duration(aux.MaxRepairDuration) * time.Millisecond
	return nil
}
