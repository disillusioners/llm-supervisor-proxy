package normalizers

// NormalizeContext carries state across chunks within a single request.
// This allows stateful normalizers to track state across streaming chunks.
type NormalizeContext struct {
	// ToolCallIndex tracks the current tool call index for stateful normalizers
	ToolCallIndex int
	// Provider is the provider identifier for per-provider rules (e.g., "zhipu", "anthropic")
	Provider string
	// RequestID is for logging and traceability
	RequestID string

	// SeenToolCallIDs tracks tool call IDs seen in this stream to assign indices
	// This is stored in context instead of the normalizer to avoid race conditions
	// when multiple streams are processed concurrently
	SeenToolCallIDs map[string]int
}

// StreamNormalizer interface - implemented by each normalizer rule
type StreamNormalizer interface {
	// Name returns the normalizer's identifier
	Name() string

	// Normalize fixes a single SSE data line
	// Returns (modifiedLine, wasModified) where wasModified indicates if the line was changed
	// This allows skipping unnecessary marshal if unchanged
	Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool)

	// EnabledByDefault returns true if this normalizer should be enabled
	// when no explicit configuration is provided
	EnabledByDefault() bool

	// Reset clears any state for a new request stream
	// Called at the start of each new request
	Reset(ctx *NormalizeContext)
}
