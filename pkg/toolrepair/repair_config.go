package toolrepair

import (
	"encoding/json"
)

// Config holds configuration for the tool repair system
type Config struct {
	// Enabled enables or disables tool repair
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Strategies is the ordered list of repair strategies to attempt
	// Available strategies: "extract_json", "library_repair", "remove_reasoning"
	Strategies []string `json:"strategies" yaml:"strategies"`

	// MaxArgumentsSize is the maximum size of tool arguments in bytes (0 = unlimited)
	MaxArgumentsSize int `json:"max_arguments_size" yaml:"max_arguments_size"`

	// MaxToolCallsPerResponse is the maximum number of tool calls per response (0 = unlimited)
	MaxToolCallsPerResponse int `json:"max_tool_calls_per_response" yaml:"max_tool_calls_per_response"`

	// LogOriginal logs the original malformed JSON for debugging
	LogOriginal bool `json:"log_original" yaml:"log_original"`

	// LogRepaired logs the repaired JSON for verification
	LogRepaired bool `json:"log_repaired" yaml:"log_repaired"`

	// FixerModel is the model to use for LLM-based JSON repair (empty = disabled)
	FixerModel string `json:"fixer_model" yaml:"fixer_model"`

	// FixerTimeout is the timeout in seconds for fixer requests
	FixerTimeout int `json:"fixer_timeout" yaml:"fixer_timeout"`
}

// DefaultConfig returns the default configuration for tool repair
func DefaultConfig() *Config {
	return &Config{
		Enabled:                 true,
		Strategies:              []string{"extract_json", "library_repair", "remove_reasoning"},
		MaxArgumentsSize:        10 * 1024, // 10KB
		MaxToolCallsPerResponse: 8,
		LogOriginal:             false,
		LogRepaired:             true,
		FixerModel:              "",
		FixerTimeout:            10, // 10 seconds
	}
}

// DisabledConfig returns a configuration with repair disabled
func DisabledConfig() *Config {
	return &Config{
		Enabled: false,
	}
}

// MarshalJSON customizes JSON marshaling
func (c Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	return json.Marshal(&struct {
		*Alias
	}{})
}

// UnmarshalJSON customizes JSON unmarshaling
func (c *Config) UnmarshalJSON(data []byte) error {
	type Alias Config
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	return nil
}
