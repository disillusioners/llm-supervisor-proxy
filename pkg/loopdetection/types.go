package loopdetection

import "time"

// Severity represents the severity level of a detected loop.
type Severity int

const (
	SeverityNone     Severity = iota
	SeverityWarning           // Suspicious pattern, worth monitoring
	SeverityCritical          // High confidence loop, intervention recommended
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return "none"
	}
}

// DetectionResult contains the outcome of a loop detection analysis.
type DetectionResult struct {
	LoopDetected bool     `json:"loop_detected"`
	Severity     Severity `json:"severity"`
	Strategy     string   `json:"strategy"`     // Which strategy detected it
	Evidence     string   `json:"evidence"`     // Human-readable explanation
	Confidence   float64  `json:"confidence"`   // 0.0 - 1.0
	Pattern      []string `json:"pattern"`      // The repeated sequence
	RepeatCount  int      `json:"repeat_count"` // How many times the pattern repeated
}

// MessageContext represents tracked metadata for a single message or chunk.
type MessageContext struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Role        string    `json:"role"`         // "user", "assistant", "system"
	ContentType string    `json:"content_type"` // "text", "tool_call", "thinking"

	// For similarity detection
	Content     string `json:"-"`            // Raw content (not serialized)
	ContentHash uint64 `json:"content_hash"` // SimHash fingerprint
	TokenCount  int    `json:"token_count"`

	// For action tracking
	Actions []Action `json:"actions,omitempty"`
}

// Action represents a tool call or file operation performed by the LLM.
type Action struct {
	Type   string `json:"type"`   // "read", "write", "grep", "execute", etc.
	Target string `json:"target"` // File path, command, search query
}

// ActionKey returns a unique string key for this action (type:target).
func (a Action) ActionKey() string {
	return a.Type + ":" + a.Target
}

// Strategy is the interface that all loop detection strategies must implement.
type Strategy interface {
	// Name returns the strategy identifier.
	Name() string

	// Analyze checks the message window for loop patterns.
	// It receives the full sliding window of tracked messages.
	Analyze(window []MessageContext) *DetectionResult
}
