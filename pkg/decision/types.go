package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Decision Models commission / §4.1 (OpenAI Decisions client-
// facing wire) + §4.2 (per-provider translation seam) +
// amendment 4 (typed structs replacing the previous
// map[string]interface{} seam). Every field on every struct here
// is bound to a verified wire element; unknown future fields on
// the OpenAI client wire are NOT modeled here — the openai-
// decision adapter's byte-faithful pass-through (Q18 / A26)
// preserves them verbatim on the upstream side without going
// through this typed seam.
//
// Cycle-bearing import posture: pkg/decision must not depend on
// pkg/proxy or pkg/imggen; types.go is a leaf file (no imports
// beyond stdlib).

// QuestionType discriminates the three sealed question shapes
// (Decision Models / §4.1). The OpenAI Decision wire is a sealed
// union: each entry of `questions[]` is exactly one of {Predicate,
// Choice, Score}; Refusal is an answer-only type that never
// appears on the request side.
type QuestionType string

const (
	QuestionTypePredicate QuestionType = "predicate"
	QuestionTypeChoice    QuestionType = "choice"
	QuestionTypeScore     QuestionType = "score"
	// QuestionTypeRefusal appears only on the answer side (a
	// question that the upstream declined). The request schema
	// never carries a refusal question.
	QuestionTypeRefusal QuestionType = "refusal"
)

// InputPartType discriminates the two verified OpenAI Decision
// input part shapes (Decision Models / §4.1).
type InputPartType string

const (
	InputPartTypeText  InputPartType = "input_text"
	InputPartTypeImage InputPartType = "input_image"
)

// Question is the sealed union over the three OpenAI Decision
// question shapes (Decision Models / §4.1). All three shapes
// share `Type`, `Name`, `Instructions`, and the validator-only
// `InstructionsRequired` policy marker. The shape-specific
// fields (Choices / Levels) are populated only when the
// discriminator permits.
//
// Name is REQUIRED by System One (the typesafe adapter keys
// `questions` on it; the validators enforce uniqueness — see
// validators.go ValidateDuplicateQuestionNames). OpenAI allows
// name to be optional in theory, but System One ↔ OpenAI
// translation requires a stable key.
type Question struct {
	Type         QuestionType `json:"type"`
	Name         string       `json:"name"`
	Instructions string       `json:"instructions,omitempty"`

	// Choice-specific (Choice / type=="choice").
	Choices []ChoiceOption `json:"choices,omitempty"`

	// Score-specific (Score / type=="score"). Each entry is
	// at minimum a label; description is optional.
	Levels []ScoreLevel `json:"levels,omitempty"`
}

// ChoiceOption is one entry in a Choice question's `choices[]`.
// The OpenAI wire accepts string or boolean values; the typed
// Value field uses interface{} so boolean/string collapses
// cleanly. The validators (validators.go
// ValidateBooleanStringCollision) reject the case where two
// choices across the bridge collide on `true` vs `"true"`
// (research-web §Q2 gotcha).
type ChoiceOption struct {
	Value       interface{} `json:"value"`
	Description string      `json:"description,omitempty"`
}

// ScoreLevel is one entry in a Score question's `levels[]`. The
// label is required (System One's `legend` keys); description
// is optional.
type ScoreLevel struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// DecisionInputMessage is one entry in the array form of
// `input` (Decision Models / §4.1). Each message carries an
// ordered list of parts; input_image parts are verbatim on the
// OpenAI path, but the typesafe adapter's BuildUpstream rejects
// any input_image with 400 (Jev is text-only per
// research-web §Q2).
type DecisionInputMessage struct {
	Role  string      `json:"role"`
	Parts []InputPart `json:"parts"`
}

// InputPart is one element of a DecisionInputMessage.Parts
// array. The two shapes share the same Type discriminator
// (input_text / input_image); the value-bag is type-specific.
type InputPart struct {
	Type InputPartType `json:"type"`
	// Text content (input_text).
	Text string `json:"text,omitempty"`
	// Image content (input_image). On the OpenAI path the
	// image_url is forwarded verbatim (base64 data URL or
	// hosted URL — Q3 / R2 posture, no validator).
	ImageURL string `json:"image_url,omitempty"`
}

// DecisionRequest is the parsed OpenAI Decision client wire
// (Decision Models / §4.1). It is the shared typed value that
// flows through validation, BuildUpstream, and InspectResponse —
// the handler parses the request bytes ONCE into this struct
// (one decode) and reuses the same value for both providers.
//
// Field tags use the OpenAI Decision wire names verbatim so a
// future json.Marshal of this struct round-trips the wire.
// BuildUpstream for the openai-decision adapter does NOT marshal
// through this struct — that path is byte-faithful (Q18 / A26).
// BuildUpstream for the typesafe adapter DOES marshal a derived
// System One struct.
type DecisionRequest struct {
	Model            string          `json:"model"`
	Input            json.RawMessage `json:"input"` // string | DecisionInputMessage[]
	Questions        []Question      `json:"questions"`
	SafetyIdentifier string          `json:"safety_identifier,omitempty"`
}

// DecisionResponse is the typed response surface the handler
// returns to clients (Decision Models / §4.1 verified shape).
// Both adapters synthesize this struct from their per-provider
// wire; the handler relays it as JSON to the client.
//
// Model echoes the upstream's resolved versioned ID (e.g.
// "gpt-6-luna" or "jev-1.13.0"). Per research-web §Q1 + §Q2
// there is no id / object / created field — the proxy does not
// synthesize any.
type DecisionResponse struct {
	Model   string   `json:"model"`
	Answers []Answer `json:"answers"`
	Usage   Usage    `json:"usage"`
}

// Answer is one entry of DecisionResponse.Answers (Decision
// Models / §4.1). The Type discriminator carries four shapes:
// predicate / choice / score / refusal. Per-type fields are
// populated in line with Type; the validators at the request
// edge cover the request-side shape constraints; the response
// side is whatever the upstream returned (typesafe synthesizes
// OpenAI-shape answers from System One's keyed map).
//
// The Choice-vs-Score probability arrays share a JSON key
// (`probabilities`) — wire-shape mirrors OpenAI's
// per-discriminator union. A custom MarshalJSON collapses the
// two probability arrays into one JSON output key. The unmarshal
// is a no-op split; the union-typed shape means the consumer
// reads Probabilities (for choice) or ScoreProbabilities (for
// score) depending on Type.
type Answer struct {
	Type QuestionType `json:"type"`
	Name string       `json:"name,omitempty"`

	// Predicate-specific.
	Probability *float64 `json:"probability,omitempty"`

	// Choice-specific. `Choice` is the chosen option's value
	// (string or boolean — typed as interface{}). Probabilities
	// is the full distribution.
	Choice        string         `json:"choice,omitempty"`
	Probabilities []ChoiceAnswer `json:"probabilities,omitempty"`
	Confidence    *float64       `json:"confidence,omitempty"`

	// Score-specific. Score is the integer level index (liteLLM
	// semantics — A24). ScoreProbabilities is the level-indexed
	// distribution. NOTE: re-mapped to `probabilities` in the
	// wire JSON via MarshalJSON below — same key the choice
	// discriminator uses.
	Score              *float64           `json:"score,omitempty"`
	scoreProbabilities []ScoreAnswerEntry `json:"-"`

	// Refusal-specific carries an optional refusal message.
	Refusal string `json:"refusal,omitempty"`
}

// MarshalJSON collapses the score/choice probability arrays
// into the wire's single `probabilities` key. The output JSON
// shape is dictated by Type — choice answers emit the
// `probabilities` array of `{value, probability}`; score
// answers emit the `probabilities` array of
// `{value, label, probability}` per the OpenAI Decision
// spec.
func (a Answer) MarshalJSON() ([]byte, error) {
	// Hand-roll a wrapper to dodge the recursion that a
	// struct-with-typed-field can't express.
	type wireAnswer struct {
		Type          QuestionType `json:"type"`
		Name          string       `json:"name,omitempty"`
		Probability   *float64     `json:"probability,omitempty"`
		Choice        string       `json:"choice,omitempty"`
		Probabilities interface{}  `json:"probabilities,omitempty"`
		Confidence    *float64     `json:"confidence,omitempty"`
		Score         *float64     `json:"score,omitempty"`
		Refusal       string       `json:"refusal,omitempty"`
	}
	w := wireAnswer{
		Type: a.Type, Name: a.Name, Probability: a.Probability,
		Choice: a.Choice, Confidence: a.Confidence,
		Score: a.Score, Refusal: a.Refusal,
	}
	switch a.Type {
	case QuestionTypeChoice:
		if len(a.Probabilities) > 0 {
			w.Probabilities = a.Probabilities
		}
	case QuestionTypeScore:
		if len(a.scoreProbabilities) > 0 {
			w.Probabilities = a.scoreProbabilities
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON splits the wire `probabilities` array into the
// choice vs. score receiver field based on Type. The typesafe
// adapter builds Answer values directly (no Unmarshal); only
// the openai-decision adapter's InspectResponse reads the wire
// (and OpenAI's shape is fixed). This function is the inverse
// MarshalJSON for completeness.
func (a *Answer) UnmarshalJSON(data []byte) error {
	// Probe the type first; the rest of the fields follow the
	// discriminator.
	var probe struct {
		Type          QuestionType      `json:"type"`
		Name          string            `json:"name"`
		Probability   *float64          `json:"probability"`
		Choice        string            `json:"choice"`
		Probabilities []json.RawMessage `json:"probabilities"`
		Confidence    *float64          `json:"confidence"`
		Score         *float64          `json:"score"`
		Refusal       string            `json:"refusal"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	a.Type = probe.Type
	a.Name = probe.Name
	a.Probability = probe.Probability
	a.Choice = probe.Choice
	a.Confidence = probe.Confidence
	a.Score = probe.Score
	a.Refusal = probe.Refusal
	switch probe.Type {
	case QuestionTypeChoice:
		for i := range probe.Probabilities {
			var c ChoiceAnswer
			if err := json.Unmarshal(probe.Probabilities[i], &c); err != nil {
				return err
			}
			a.Probabilities = append(a.Probabilities, c)
		}
	case QuestionTypeScore:
		for i := range probe.Probabilities {
			var s ScoreAnswerEntry
			if err := json.Unmarshal(probe.Probabilities[i], &s); err != nil {
				return err
			}
			a.scoreProbabilities = append(a.scoreProbabilities, s)
		}
	}
	return nil
}

// SetScoreProbabilities is the small wire-shaping accessor the
// typesafe adapter uses to populate score answers without
// touching the unexported field directly (kept inline so the
// adapter path stays in this package).
func (a *Answer) SetScoreProbabilities(entries []ScoreAnswerEntry) {
	a.scoreProbabilities = entries
}

// GetScoreProbabilities is the inverse accessor — the meter
// path reads score answers via this accessor. (Unused at P2
// but exported for symmetry.)
func (a *Answer) GetScoreProbabilities() []ScoreAnswerEntry {
	return a.scoreProbabilities
}

// ChoiceAnswer is one entry of Answer.Probabilities on a choice
// answer (Decision Models / §4.1).
type ChoiceAnswer struct {
	Value       interface{} `json:"value"`
	Probability float64     `json:"probability"`
}

// ScoreAnswerEntry is one entry of Answer.Probabilities on a
// score answer (Decision Models / §4.1). Value is the integer
// level index; Label is the level's text label (System One's
// `legend` — synthesized on the typesafe path).
type ScoreAnswerEntry struct {
	Value       int     `json:"value"`
	Label       string  `json:"label"`
	Probability float64 `json:"probability"`
}

// Usage is the typed OpenAI Decision usage block (Decision
// Models / §4.1). All token counts are int64; the per-detail
// fields (CachedTokens / CacheWriteTokens / ReasoningTokens) are
// zero-filled on the typesafe path per A25 — System One returns
// only `{input_tokens, output_tokens}`.
//
// Both providers DO return token usage (research-web §Q1, §Q2).
// Success-only metering is per amendment 5 — failures NEVER
// write a usage row; failure telemetry flows through
// DecisionEvent only.
type Usage struct {
	InputTokens        int64              `json:"input_tokens"`
	OutputTokens       int64              `json:"output_tokens"`
	TotalTokens        int64              `json:"total_tokens"`
	InputTokensDetail  InputTokensDetail  `json:"input_tokens_details"`
	OutputTokensDetail OutputTokensDetail `json:"output_tokens_details"`
}

// InputTokensDetail is the typed zero-fill shape for OpenAI's
// `usage.input_tokens_details` object. The fields are
// per OpenAI Decision wire; the proxy zero-fills them on the
// typesafe path (System One doesn't break down input tokens).
type InputTokensDetail struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// OutputTokensDetail is the typed zero-fill shape for OpenAI's
// `usage.output_tokens_details` object. ReasoningTokens is
// zero-filled on both paths (decision models don't reason the
// way chat models do — the field is present in the wire shape
// for forward-compatibility but unused at v1).
type OutputTokensDetail struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// InputText extracts the joined text content of a
// DecisionRequest.Input that is known to be a string. Returns
// ("", false) on any non-string RawMessage — call this only
// after probing InputIsText. Used by the typesafe adapter to
// translate input string form to System One `state`.
func (r *DecisionRequest) InputText() (string, bool) {
	if len(r.Input) == 0 {
		return "", false
	}
	// Trim surrounding whitespace defensively; trailing \n from
	// HTTP body framing is common.
	trimmed := bytes.TrimSpace(r.Input)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return "", false
	}
	return s, true
}

// InputIsText reports whether r.Input's JSON shape is a string
// (vs. a DecisionInputMessage array).
func (r *DecisionRequest) InputIsText() bool {
	if len(r.Input) == 0 {
		return false
	}
	trimmed := bytes.TrimSpace(r.Input)
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '"'
}

// InputMessages parses r.Input as a DecisionInputMessage array.
// Returns (nil, false) on a non-array RawMessage. Used by the
// typesafe adapter — typesafe's input image rejection
// (research-web §Q2) requires per-message structure to detect.
func (r *DecisionRequest) InputMessages() ([]DecisionInputMessage, bool) {
	if len(r.Input) == 0 {
		return nil, false
	}
	trimmed := bytes.TrimSpace(r.Input)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var msgs []DecisionInputMessage
	if err := json.Unmarshal(trimmed, &msgs); err != nil {
		return nil, false
	}
	return msgs, true
}

// DecisionErrorf is a small convenience for adapter-internal
// error returns; never wired into the public envelope directly
// — BuildUpstream wraps it, the handler decides the status
// (400 vs 502 by step).
func DecisionErrorf(format string, args ...interface{}) error {
	return fmt.Errorf("decision: "+format, args...)
}
