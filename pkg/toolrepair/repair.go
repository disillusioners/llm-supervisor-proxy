package toolrepair

import (
	"encoding/json"
	"fmt"
	"time"
)

// RepairResult contains the result of a repair operation
type RepairResult struct {
	Original   string        `json:"original"`
	Repaired   string        `json:"repaired"`
	Success    bool          `json:"success"`
	Strategies []string      `json:"strategies"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
	ToolName   string        `json:"tool_name"`
}

// Repairer handles tool call JSON repair operations
type Repairer struct {
	config *Config
}

// NewRepairer creates a new Repairer with the given configuration
func NewRepairer(config *Config) *Repairer {
	if config == nil {
		config = DefaultConfig()
	}
	return &Repairer{config: config}
}

// ToolCallData represents a simplified tool call for repair
type ToolCallData struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// RepairToolCallsData repairs a slice of tool call data
// Returns the repaired data and statistics
func (r *Repairer) RepairToolCallsData(toolCalls []ToolCallData) ([]ToolCallData, *RepairStats) {
	if !r.config.Enabled {
		return toolCalls, &RepairStats{}
	}

	stats := &RepairStats{
		StartTime:      time.Now(),
		StrategiesUsed: make(map[string]int),
	}

	// Check max tool calls limit
	if r.config.MaxToolCallsPerResponse > 0 && len(toolCalls) > r.config.MaxToolCallsPerResponse {
		stats.ExceededLimit = true
		stats.EndTime = time.Now()
		return toolCalls, stats
	}

	repairedCalls := make([]ToolCallData, len(toolCalls))
	for i, tc := range toolCalls {
		repairedCalls[i] = tc
		stats.TotalToolCalls++

		// Check size limit
		if r.config.MaxArgumentsSize > 0 && len(tc.Arguments) > r.config.MaxArgumentsSize {
			stats.TooLarge++
			continue
		}

		// Check if already valid
		if isValidJSON(tc.Arguments) {
			stats.ValidJSON++
			continue
		}

		// Attempt repair
		stats.InvalidJSON++
		result := r.RepairArguments(tc.Arguments, tc.Name)

		if result.Success {
			repairedCalls[i].Arguments = result.Repaired
			stats.Repaired++
		} else {
			stats.Failed++
		}

		// Update strategy stats
		for _, strategy := range result.Strategies {
			stats.StrategiesUsed[strategy]++
		}
	}

	stats.EndTime = time.Now()
	stats.Duration = stats.EndTime.Sub(stats.StartTime)

	return repairedCalls, stats
}

// RepairArguments attempts to repair malformed JSON arguments
func (r *Repairer) RepairArguments(arguments, toolName string) *RepairResult {
	start := time.Now()
	result := &RepairResult{
		Original:   arguments,
		ToolName:   toolName,
		Strategies: []string{},
	}

	// Check size limit
	if r.config.MaxArgumentsSize > 0 && len(arguments) > r.config.MaxArgumentsSize {
		result.Error = "arguments exceed size limit"
		result.Duration = time.Since(start)
		return result
	}

	// Try each strategy in order
	for _, strategyName := range r.config.Strategies {
		strategy := getStrategy(strategyName)
		if strategy == nil {
			continue
		}

		repaired, err := strategy(arguments)
		result.Strategies = append(result.Strategies, strategyName)

		if err == nil && isValidJSON(repaired) {
			result.Repaired = repaired
			result.Success = true
			result.Duration = time.Since(start)
			return result
		}

		// Update arguments for next strategy
		if err == nil {
			arguments = repaired
		}
	}

	// All strategies failed
	result.Error = "all repair strategies failed"
	result.Duration = time.Since(start)
	return result
}

// isValidJSON checks if a string is valid JSON
func isValidJSON(s string) bool {
	var js interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// RepairStats contains statistics about repair operations
type RepairStats struct {
	StartTime      time.Time      `json:"start_time"`
	EndTime        time.Time      `json:"end_time"`
	Duration       time.Duration  `json:"duration"`
	TotalToolCalls int            `json:"total_tool_calls"`
	ValidJSON      int            `json:"valid_json"`
	InvalidJSON    int            `json:"invalid_json"`
	Repaired       int            `json:"repaired"`
	Failed         int            `json:"failed"`
	TooLarge       int            `json:"too_large"`
	ExceededLimit  bool           `json:"exceeded_limit"`
	StrategiesUsed map[string]int `json:"strategies_used"`
	Retries        int            `json:"retries"`
	RetrySuccesses int            `json:"retry_successes"`
}

// NewRepairStats creates a new RepairStats instance
func NewRepairStats() *RepairStats {
	return &RepairStats{
		StrategiesUsed: make(map[string]int),
	}
}

// Summary returns a human-readable summary of the stats
func (s *RepairStats) Summary() string {
	return fmt.Sprintf(
		"total=%d valid=%d invalid=%d repaired=%d failed=%d duration=%v",
		s.TotalToolCalls,
		s.ValidJSON,
		s.InvalidJSON,
		s.Repaired,
		s.Failed,
		s.Duration,
	)
}
