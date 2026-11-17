package providers

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Provider defines the interface for AI providers
type Provider interface {
	// Name returns the provider name (e.g., "openai", "anthropic")
	Name() string

	// ChatCompletion sends a non-streaming chat completion request
	ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)

	// StreamChatCompletion sends a streaming chat completion request
	// Returns a channel of normalized StreamEvent
	StreamChatCompletion(ctx context.Context, req *ChatCompletionRequest) (<-chan StreamEvent, error)

	// IsRetryable returns true if the error should trigger a retry
	IsRetryable(err error) bool
}

// ChatCompletionRequest represents a chat completion request (OpenAI-compatible format)
type ChatCompletionRequest struct {
	Model            string                 `json:"model"`
	Messages         []ChatMessage          `json:"messages"`
	MaxTokens        *int                   `json:"max_tokens,omitempty"`
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"top_p,omitempty"`
	N                *int                   `json:"n,omitempty"`
	Stream           bool                   `json:"stream"`
	Stop             interface{}            `json:"stop,omitempty"` // string or []string
	PresencePenalty  *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]float64     `json:"logit_bias,omitempty"`
	User             string                 `json:"user,omitempty"`
	Tools            []Tool                 `json:"tools,omitempty"`
	ToolChoice       interface{}            `json:"tool_choice,omitempty"` // "none", "auto", "required", or specific tool
	Extra            map[string]interface{} `json:"-"`                     // Provider-specific extra fields

	// ReasoningSplit is the typed transport for the MiniMax top-level
	// reasoning_split flag (D1). Pointer type so absent-vs-false stays
	// distinguishable; omitempty drops the wire key when nil.
	ReasoningSplit *bool `json:"reasoning_split,omitempty"`
}

// ReasoningDetailEntry mirrors the wire shape of a single MiniMax
// reasoning_details entry on internal request paths. It is defined here
// (not imported from pkg/proxy/translator) because R3 forbids pkg/providers
// from depending on pkg/proxy/translator types — the translator→providers
// direction is the allowed one. The boundary code (convertRequest,
// extractReasoningContent) is responsible for translating between this
// local type and the translator's ReasoningDetail when needed.
type ReasoningDetailEntry struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Format string `json:"format,omitempty"`
	Index  int    `json:"index,omitempty"`
	Text   string `json:"text,omitempty"`
}

// ChatMessage represents a single message in a chat
type ChatMessage struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"` // string or []ContentPart for multimodal
	Name             string      `json:"name,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`      // Required for tool role messages
	ReasoningContent string      `json:"reasoning_content,omitempty"` // For DeepSeek R1-style thinking models

	// ReasoningDetails carries MiniMax-format per-message reasoning_details
	// on request-side map→struct hydration (D1). It is intentionally NOT
	// exported on StreamEvent (D2: openai.go's stream parser emits one
	// thinking StreamEvent per entry directly, making the field dead surface
	// on the stream type). omitempty drops the key when nil.
	ReasoningDetails []ReasoningDetailEntry `json:"reasoning_details,omitempty"`
}

// Tool represents a tool definition
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a function definition
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	Index    int              `json:"index,omitempty"` // Used in streaming deltas
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function details in a tool call
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ContentPart represents a content part in multimodal messages
type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in multimodal content
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// ChatCompletionResponse represents a chat completion response (OpenAI-compatible format)
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason"`
}

// Usage represents token usage statistics
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent represents a normalized streaming event
type StreamEvent struct {
	Type             string                  // "content", "tool_call", "thinking", "done", "error"
	Content          string                  // Text content delta
	ReasoningContent string                  // Reasoning content delta (DeepSeek-style)
	ToolCalls        []ToolCall              // Tool call deltas for "tool_call" event
	FinishReason     string                  // Finish reason if type is "done"
	Error            error                   // Error if type is "error"
	Response         *ChatCompletionResponse // Full response for "done" event
}

// ProviderError wraps provider-specific errors with retry information.
//
// Round 3 (R3-1) ADDITIVE extension — RetryAfter + ErrorType + ErrorCode
// (Round 3c — W2). No existing field is renamed, removed, or reordered;
// the new fields are appended at the tail so existing error-comparison
// code paths and tests keep compiling.
//
// RetryAfter is captured from the Retry-After response header on the
// 429 path (openai.go:handleError wire-up) so ExcludeAndReselect can
// seed the per-credential cooldown. Zero when the header is absent
// or unparseable; the F.2 caller treats RetryAfter=0 as the "caller
// applies default cooldown (60s)" signal — NEVER "no cooldown".
//
// ErrorType + ErrorCode mirror the unmarshaled anonymous-local
// {Error:{Message,Type,Code}} body shape (openai.go:handleError wire-up).
// They make the Round-3b classifier matrix row 11 (503 + rate-limit
// body) implementable and the W1 /v1/messages anthropic-passthrough
// classifier readable without re-parsing raw response bodies on the
// hook site.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	Retryable  bool
	BufferID   string // Optional: ID of saved request buffer for debugging

	// Round 3 — R3-1 (additive). See struct doc-comment above.
	RetryAfter time.Duration
	ErrorType  string
	ErrorCode  string
}

func (e *ProviderError) Error() string {
	return e.Provider + ": " + e.Message
}

// IsRetryable implements Provider interface check
func (e *ProviderError) IsRetryable() bool {
	return e.Retryable
}

// IsRateLimitError (Round 3 — R3-1) classifies an error as a 429-style
// rate-limit condition.
//
// True when the error wraps a *ProviderError whose:
//   - HTTP status is 429, OR
//   - error-body code matches the rate-limit vocabulary
//     (equality on {rate_limit, rate_limit_error, rate_limit_exceeded}),
//     OR whose:
//   - error-body type contains the substring "rate_limit"
//     (case-insensitive; covers `rate_limit`, `rate_limit_error`,
//     `rate_limit_exceeded` and any `-`/no-underscore variant the
//     upstream brand emits).
//
// Vocabulary (Round 3b pinned; Round 3c — S1 adds rate_limit_exceeded):
//   - type-substring match on "rate_limit" (the ONLY vocabulary
//     check on the type field)
//   - code-equality set = {rate_limit, rate_limit_error, rate_limit_exceeded}
//   - status == 429
//
// Classifier returns true when ANY of the three match. The error
// body's `code` and `type` fields are read from the extended
// ProviderError.ErrorCode / ErrorType (Round 3c — W2), not from a raw
// body re-parse — the API path already decoded them in handleError.
//
// Returns false for nil errors and for non-*ProviderError errors.
// Safe to call from any goroutine.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	// errors.As (not a direct type assertion): callers up the stack may
	// wrap the *ProviderError with fmt.Errorf("%w", ...) — the Task 18
	// coordinator reads latestReq.GetError() which can be wrapped; the
	// classifier must see through the wrapping (Task 21 uses the same
	// errors.As form for the ultimate guard).
	var pe *ProviderError
	if !errors.As(err, &pe) || pe == nil {
		return false
	}
	if pe.StatusCode == 429 {
		return true
	}
	if strings.Contains(strings.ToLower(pe.ErrorType), "rate_limit") {
		return true
	}
	switch pe.ErrorCode {
	case "rate_limit", "rate_limit_error", "rate_limit_exceeded":
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// THE CANONICAL QUOTA-CLASS VOCABULARY (wi-4b8d21e7, review S-1 / S-4).
//
// ONE canonical vocabulary, three consumers:
//
//   - the CHAT lane's IsQuotaError (the *ProviderError classifier);
//   - the ANTHROPIC lane's decode-first classifier (it calls
//     ClassifyQuotaBody — no local list);
//   - the MINT-SCOPE layer's subset invariant (pkg/modelcache's scope
//     vocabulary must cover every token here).
//
// WHY THE CLASS LIST IS TIGHT (the asymmetry that matters):
//
//	over-breadth in the SCOPE list resolves to the SAFE narrow scope
//	(a model mark is simply not minted);
//	over-breadth in the CLASS list benches a HEALTHY credential for
//	15m instead of 60s — the "cooldown trap" this commission exists to
//	close (a walled account that returns to the pool after a minute).
//
// Every token below is EVIDENCE-BACKED: a body in the corpus (a live
// upstream body or a test pin of one) carries it. Tokens without an
// observed instance were DROPPED (the shipped v197 list carried a few;
// the Anthropic lane's local list carried three more, one of them
// INVENTED — "insufficient_parameters" is a CLIENT error, not a quota).
//
// PROVENANCE per token (the observed instance):
//
//	"insufficient_credit"  — providers/interface_quota_test.go (type)
//	"insufficient_quota"   — providers/interface_quota_test.go (type/code);
//	                         pkg/proxy/sticky_scenario_test.go (live-shaped body)
//	"quota"                — providers/interface_quota_test.go (the
//	                         "quota_exceeded" type is matched by this token);
//	                         the 9router live body
//	"payment_required"     — the review's drift probe (code=payment_required
//	                         classified QUOTA in the race lane, NOT in the
//	                         Anthropic lane — the drift this list ends);
//	                         BOTH spellings are kept on purpose
//	"resource_exhausted"   — pkg/imggen/ninerouter.go:290 (the 9router
//	                         nested body: "status":"RESOURCE_EXHAUSTED")
//	"insufficient credit"  — providers/interface_quota_test.go (message)
//	"quota exceeded"       — providers/interface_quota_test.go (message)
//	"quota exhausted"      — pkg/proxy/race_reason_test.go (message)
//	"token plan usage limit" — the z.ai live bodies (codes 2056 / 2067)
//	"token plan"           — the z.ai live bodies (codes 2056 / 2067)
//	"payment required"     — providers/interface_quota_test.go (message)
//	"your quota will reset" — the 9router live body
//	"your limit will reset" — the z.ai 5-hour plan-window body
//	"exhausted your capacity" — the 9router live body
//
// DROPPED (no observed instance — the class list is the narrow, safe
// side; the SCOPE vocabulary in pkg/modelcache keeps the account-subject
// knowledge so the mint scope stays correct):
//
//	"insufficient_quota" / "insufficient_balance" (message spellings),
//	"insufficient_credits", "exceeded your quota", "credit exhausted",
//	"credits exhausted", "out of credit", "billing",
//	"credit_exhausted", "insufficient_balance" (type spelling),
//	"insufficient_parameters" (INVENTED — a client-side parameter
//	complaint, never a quota), "credit_balance_too_low" and
//	"billing_error" (Anthropic-lane-only, no corpus instance).
//
// Observed live vocabulary (job 8d852270): HTTP 402 / code 2067 /
// "Token Plan usage limit" — the code arrives as a NUMBER on the wire
// (unparseable into the string-typed ErrorCode field), but the message
// text survives into ProviderError.Message in BOTH wire shapes:
// OpenAI-shaped bodies decode Message directly; non-OpenAI-shaped bodies
// fall back to msg = raw body (openai.go:handleError), which still
// contains the message text. The message vocabulary is therefore the
// workhorse for numeric-code upstreams.
var (
	quotaTypeVocabulary = []string{
		"insufficient_credit",
		"insufficient_quota",
		"quota",
		"payment_required",
		"billing",
		"resource_exhausted",
	}
	quotaCodeVocabulary    = quotaTypeVocabulary // structured fields share the set
	quotaMessageVocabulary = []string{
		"insufficient credit",
		"quota exceeded",
		"quota exhausted",
		// The specific observed live shape replaces the former bare
		// "usage limit" phrase — a PLAIN rate-limit body uses "usage
		// limit" for per-minute throttling ("you have reached your
		// usage limit for this minute"), which must classify rate_limit
		// (60s/Retry-After), NOT quota.
		"token plan usage limit",
		// "token plan" stays its own status-agnostic phrase BY DESIGN: a
		// token-plan body means the plan is exhausted at ANY status.
		"token plan",
		"payment required",
		// The account-subject shapes the SCOPE layer already knows
		// (review S-1): the z.ai plan window and the 9router account
		// quota are ACCOUNT walls — they must seed the QUOTA cooldown
		// (15m), not the flat rate-limit one (60s), or the dry account
		// re-enters the pool after a minute.
		"your quota will reset",
		"your limit will reset",
		"exhausted your capacity",
	}
)

// QuotaBodyKind — WHICH part of an upstream error body carried the
// quota classification. The Anthropic lane needs the distinction: its
// MESSAGE-driven path is gated on a credential-attributable status
// (review S-3), while its type/code-driven path is pre-existing
// behavior and is not re-gated.
type QuotaBodyKind int

const (
	// QuotaBodyNone — no quota vocabulary in any field.
	QuotaBodyNone QuotaBodyKind = iota
	// QuotaBodyStructured — the error type or code carried the quota
	// vocabulary (the precise, structured signal).
	QuotaBodyStructured
	// QuotaBodyMessage — only the free-text message carried it.
	QuotaBodyMessage
)

// String renders the kind for the operator-facing log lines.
func (k QuotaBodyKind) String() string {
	switch k {
	case QuotaBodyStructured:
		return "structured"
	case QuotaBodyMessage:
		return "message"
	default:
		return "none"
	}
}

// ClassifyQuotaBody is the CANONICAL quota-body classifier: it reads
// the three fields of an upstream error body and reports WHICH of them
// carried the quota vocabulary.
//
// The structured fields (type / code) are matched against the shared
// structured vocabulary (substring-safe, low noise); the free-text
// message is matched against the bounded message vocabulary. The
// structured signal wins the tie: a body whose type says quota is
// classified structured even when its message also matches.
//
// This is the ONE classifier both protocol lanes use — the chat lane
// through IsQuotaError, the Anthropic lane through
// ClassifyQuotaBody (it no longer carries a local list).
func ClassifyQuotaBody(errorType, errorCode, message string) QuotaBodyKind {
	if containsAny(strings.ToLower(errorType), quotaTypeVocabulary) ||
		containsAny(strings.ToLower(errorCode), quotaCodeVocabulary) {
		return QuotaBodyStructured
	}
	if IsQuotaMessage(message) {
		return QuotaBodyMessage
	}
	return QuotaBodyNone
}

// IsQuotaBody is the boolean form of ClassifyQuotaBody: true when any
// field of the upstream error body carries the quota vocabulary.
//
// The canonical entry point for a caller that holds the body's fields
// separately (the Anthropic lane); a caller that holds a raw body uses
// IsQuotaMessage.
func IsQuotaBody(errorType, errorCode, message string) bool {
	return ClassifyQuotaBody(errorType, errorCode, message) != QuotaBodyNone
}

// QuotaVocabularyTokens returns a COPY of the canonical quota-class
// tokens (the structured set + the message set).
//
// It exists for the MINT-SCOPE subset invariant: the class vocabulary
// decides the cooldown policy, the scope vocabulary decides whether a
// model mark is minted — and every class token must be covered by the
// scope vocabulary, or a body the class layer treats as an account
// quota could be scoped as model-scoped and mint a model mark.
//
// Read-only: the returned slice is a copy, so a caller cannot mutate
// the canonical vocabulary.
func QuotaVocabularyTokens() []string {
	out := make([]string, 0, len(quotaTypeVocabulary)+len(quotaMessageVocabulary))
	out = append(out, quotaTypeVocabulary...)
	out = append(out, quotaMessageVocabulary...)
	return out
}

// containsAny reports whether the lowercased haystack contains any of the
// (already-lowercase) vocabulary entries.
func containsAny(lowered string, vocabulary []string) bool {
	for _, v := range vocabulary {
		if strings.Contains(lowered, v) {
			return true
		}
	}
	return false
}

// IsQuotaMessage reports whether a free-text body carries the ACCOUNT
// quota vocabulary (the message half of quotaMessageVocabulary).
//
// It is the exported twin of the message check IsQuotaError performs, so
// a caller that holds a RAW body (no *ProviderError to inspect — the
// Anthropic passthrough lane, which reads the upstream body directly)
// can classify a quota rejection with the SAME vocabulary the chat lane
// uses, instead of a second, drifting copy.
//
// Case-insensitive; an empty string is false.
func IsQuotaMessage(text string) bool {
	if text == "" {
		return false
	}
	return containsAny(strings.ToLower(text), quotaMessageVocabulary)
}

// IsQuotaError (layered credential fallback — Fix 1) classifies an error
// as a QUOTA-class condition: the credential's credit / plan / balance is
// exhausted. This is the second credential-failover class, alongside
// IsRateLimitError (429-style "slow down").
//
// True when the error wraps a *ProviderError whose:
//   - HTTP status is 402 (Payment Required), OR
//   - error-body type contains the quota vocabulary substring
//     (case-insensitive), OR
//   - error-body code matches the quota vocabulary substring
//     (case-insensitive), OR
//   - error message carries one of the bounded quota phrases
//     (case-insensitive) — "insufficient credit", "insufficient
//     balance", "quota exceeded", "token plan usage limit", "token
//     plan", "payment required", …
//
// Why a message vocabulary at all: quota rejections arrive in
// non-OpenAI body shapes from gateway upstreams, and the quota code is
// often numeric (code 2067) — invisible to the string-typed ErrorCode
// field. The message (or the raw-body fallback that lands in Message)
// is the only carrier that survives every shape (see
// quotaMessageVocabulary above).
//
// Precedence note for callers: a 429 whose BODY carries quota
// vocabulary classifies as QUOTA, not rate-limit — the account is out
// of credit, and the recovery is a TOP-UP (hours/days), not a 60s
// pause. This quota-FIRST precedence is INTENTIONAL: callers resolve
// the overlap by consulting this classifier BEFORE IsRateLimitError
// (pkg/proxy/classifyCredFailure does exactly that — pinned at
// interface_quota_test.go). A PLAIN 429 — one whose body carries no
// quota vocabulary — still classifies as rate-limit and keeps the
// 60s/Retry-After policy.
//
// Returns false for nil errors and for non-*ProviderError errors.
// 401/auth-class errors carry none of the vocabulary — the classifier
// is silent on them (auth stays terminal; eligibility is never widened
// to it). Safe to call from any goroutine.
func IsQuotaError(err error) bool {
	if err == nil {
		return false
	}
	// errors.As (not a direct type assertion): callers up the stack may
	// wrap the *ProviderError — the classifier must see through the
	// wrapping (same discipline as IsRateLimitError above).
	var pe *ProviderError
	if !errors.As(err, &pe) || pe == nil {
		return false
	}
	if pe.StatusCode == 402 {
		return true
	}
	// The CANONICAL classifier (review S-4): the structured fields win
	// the tie over the free-text message — one vocabulary, one
	// decision, shared with the Anthropic lane.
	return ClassifyQuotaBody(pe.ErrorType, pe.ErrorCode, pe.Message) != QuotaBodyNone
}
