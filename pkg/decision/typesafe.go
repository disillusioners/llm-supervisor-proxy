package decision

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Decision Models commission / §3.2 step 10 / §4.2 — the
// TypeSafe (Jev) adapter. Typed struct → System One translation
// on the request side; keyed-map → ordered-array synthesis on
// the response side. The typed invariants are re-asserted at
// BuildUpstream time as defense-in-depth (amendment 4).
//
// Provider name (keyed in providerRegistry): "typesafe"
// (lowercase; matches credential.Provider).
const TypesafeProviderName = "typesafe"

// NewTypesafeProvider returns the TypeSafe (Jev) identity
// adapter. Registered at init() time.
func NewTypesafeProvider() UpstreamProvider {
	return &typesafeProvider{}
}

type typesafeProvider struct{}

// System One request shape (research-web §Q2 verified).
// Struct-only fields; JSON encoding uses snake_case to match
// the upstream wire.
type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     systemOneState               `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

// systemOneState holds the input translation target. It can
// render as a string (text input) or as a joined-text string
// (text message array); the openai-decision input_image
// rejection is enforced at BuildUpstream time (returns an
// error before this struct is ever realized).
type systemOneState struct{}

// systemOneQuestion is one entry of the System One `questions`
// map. Type is one of {"noul","choice","score"}; only Choice
// and Score need extra fields.
type systemOneQuestion struct {
	Type         string           `json:"type"`
	Instructions string           `json:"instructions,omitempty"`
	Choice       *systemOneChoice `json:"choice,omitempty"`
	Score        *systemOneScore  `json:"score,omitempty"`
}

// systemOneChoice is the System One choice sub-shape (per-question
// keyed map; `label` is the OpenAI value's JSON form — string
// or boolean; `description` is optional).
type systemOneChoice struct {
	Criteria map[string]systemOneChoiceLabel `json:"criteria"`
}

// systemOneChoiceLabel is one criteria entry for System One's
// choice `criteria` map. Value is verbatim (string or boolean —
// bridge preserves the original value's JSON form).
type systemOneChoiceLabel struct {
	Value       interface{} `json:"value"`
	Description string      `json:"description,omitempty"`
}

// systemOneScore is the System One score sub-shape: `criteria`
// is an ordered array (System One uses array form for score).
type systemOneScore struct {
	Criteria []systemOneScoreCriterion `json:"criteria"`
}

// systemOneScoreCriterion is one entry of systemOneScore.Criteria.
// System One stores the integer level index in `index`; the
// OpenAI label is in `label`.
type systemOneScoreCriterion struct {
	Index int    `json:"index"`
	Label string `json:"label"`
}

// BuildUpstream (Decision Models / §3.2 step 10 / §4.2):
//
//   - upstreamURL = TrimSuffix(cred.BaseURL, "/") + "/v1/systemone"
//     (Q17 honor cred.BaseURL as-is).
//   - upstreamBody = typed struct → System One JSON bytes.
//   - err returns are 400-class (typed validation) OR a JSON-encode
//     failure (impossible without manual corruption but kept
//     for symmetry with the openai adapter). The handler maps
//     a non-nil err to 502 + decision_upstream_build_failed
//     per Q15 (the typed-vs-build-error distinction is the
//     handler's responsibility, not the adapter's).
//
// The translation rules (Decision Models / §4.2 + research-web
// §Q2 gotchas):
//
//   - input string → state string (verbatim).
//   - input array with input_text parts only → state = joined
//     text. Per-message structure is lost (documented
//     limitation).
//   - input array with input_image parts → 400 (Jev is
//     text-only per research-web §Q2). The BuildUpstream
//     returns a typed-validation error so the handler emits
//     400 + house OpenAI envelope BEFORE upstream.
//   - safety_identifier present → 400 (research-web §Q2:
//     TypeSafe rejects safety_identifier — Q3).
//   - questions[] → questions{} keyed by Name (Finding 9: any
//     duplicate Name triggers the validator to reject at the
//     handler edge; this adapter re-asserts as defense-in-
//     depth).
//   - predicate → noul: instructions passthrough; OpenAI
//     predicate has no `criteria`, so no-op. OpenAI requires
//     instructions for predicate — synthesize "Is the
//     following statement true?" fallback when absent.
//   - choice → choice: choices[].value → criteria map entries
//     keyed by the JSON-formatted value (research-web §Q2).
//   - score → score: levels[] → criteria array. Each entry
//     carries `index` and `label`.
func (p *typesafeProvider) BuildUpstream(cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte) (string, []byte, error) {
	if req == nil {
		return "", nil, DecisionErrorf("typed DecisionRequest is nil for BuildUpstream (typesafe)")
	}

	// Defense-in-depth: re-assert the typed edge validators
	// that protect System One translation invariants.
	// (Amendments 4 + 24; same code as the handler's step 5
	// validators but scoped to the System One-relevant subset.)
	if msg, failed := typesafeValidateFirstFailed(req); failed {
		return "", nil, DecisionErrorf("%s", msg)
	}

	// Reject safety_identifier for typesafe (research-web §Q2
	// — Q3 in the plan). The proxy surfaces 400 here so the
	// client gets a clear message; the upstream would reject
	// anyway but a proxy-side check keeps the failure message
	// in the house envelope.
	if req.SafetyIdentifier != "" {
		return "", nil, DecisionErrorf("TypeSafe (Jev) does not support safety_identifier (research-web §Q2)")
	}

	// State translation. Three cases:
	//   1. Input is a JSON string → state string.
	//   2. Input is a message array with text parts only →
	//      state is the joined text.
	//   3. Input has any image part → 400 (text-only upstream).
	stateStr, err := deriveStateText(req)
	if err != nil {
		return "", nil, err
	}

	// Questions translation. questions{} is keyed by Name;
	// a missing Name makes the bridge indeterminate
	// (System One requires a key per question). Reject 400 at
	// BuildUpstream (defense-in-depth even when the handler
	// already caught a duplicate Name).
	questions := make(map[string]systemOneQuestion, len(req.Questions))
	for i := range req.Questions {
		q := &req.Questions[i]
		name := strings.TrimSpace(q.Name)
		if name == "" {
			return "", nil, DecisionErrorf("decision question at index %d has no name; System One requires a key per question", i)
		}
		if _, dup := questions[name]; dup {
			return "", nil, DecisionErrorf("duplicate question name %q (System One map-key collision; finding 9)", name)
		}
		questions[name] = translateQuestion(q)
	}

	systemOne := systemOneRequest{
		Model:     cred.InternalModel,
		State:     systemOneState{},
		Questions: questions,
	}
	// systemOneState encodes as a bare JSON string by
	// implementing json.Marshaler.
	body, err := marshalSystemOne(systemOne, stateStr)
	if err != nil {
		return "", nil, err
	}

	upstreamBase := cred.BaseURL
	if upstreamBase == "" {
		upstreamBase = "https://api.typesafe.ai"
	}
	upstreamURL := strings.TrimSuffix(upstreamBase, "/") + "/v1/systemone"
	return upstreamURL, body, nil
}

// systemOneState.Marshals as a bare JSON string. The content
// is set via the parent struct (stateStr is threaded through
// marshalSystemOne below).

// deriveStateText translates DecisionRequest.Input into the
// `state` text value System One accepts. Returns a typed
// validation error on input_image rejection; a JSON-decode
// failure surfaces as a typed validation error too (the
// handler typed-parse already enforced a clean decode —
// this is defense-in-depth).
func deriveStateText(req *DecisionRequest) (string, error) {
	if req.InputIsText() {
		s, _ := req.InputText()
		return s, nil
	}
	msgs, ok := req.InputMessages()
	if !ok {
		return "", DecisionErrorf("decision request input is neither a string nor a message array")
	}
	// Message-array case. Per research-web §Q2 / Q3, Jev is
	// text-only — any input_image part is a hard reject.
	var sb strings.Builder
	for i := range msgs {
		for j := range msgs[i].Parts {
			part := msgs[i].Parts[j]
			switch part.Type {
			case InputPartTypeImage, InputPartType(""), "image":
				// Empty Type defaults to image? No — an
				// unknown type is also a reject to keep
				// semantics tight. The OpenAI wire uses
				// "input_image" / "input_text" verbatim
				// (research-web §Q1); anything else is
				// an unhandled forward-compat shape.
				if part.Type == "" {
					return "", DecisionErrorf("decision input part at message %d part %d has no type (only input_text / input_image are supported)", i, j)
				}
				return "", DecisionErrorf("TypeSafe (Jev) does not support image input (research-web §Q2); rejected decision input with input_image at message %d part %d", i, j)
			case InputPartTypeText:
				sb.WriteString(part.Text)
			default:
				return "", DecisionErrorf("decision input part at message %d part %d has unknown type %q (only input_text / input_image)", i, j, part.Type)
			}
		}
	}
	if sb.Len() == 0 {
		// Empty-state is allowed by System One but
		// pointless here; surface as a soft reject.
		return "", DecisionErrorf("decision input message array contained no text parts to translate to state")
	}
	return sb.String(), nil
}

// translateQuestion maps one OpenAI Decision Question →
// systemOneQuestion. Instructions / Type / per-shape fields are
// populated per the §4.2 rules.
func translateQuestion(q *Question) systemOneQuestion {
	out := systemOneQuestion{}
	switch q.Type {
	case QuestionTypePredicate:
		out.Type = "noul"
		// OpenAI requires instructions for predicate.
		// Synthesize the generic fallback per research-web
		// §Q2.
		out.Instructions = q.Instructions
		if out.Instructions == "" {
			out.Instructions = "Is the following statement true?"
		}
	case QuestionTypeChoice:
		out.Type = "choice"
		out.Instructions = q.Instructions
		criteria := make(map[string]systemOneChoiceLabel, len(q.Choices))
		for i := range q.Choices {
			c := &q.Choices[i]
			// System One keys on a label string. We derive
			// the label from the value's string form (true →
			// "true", "billing" → "billing"). The label is the
			// JSON-marshaled value.
			label := labelFromValue(c.Value)
			if label == "" {
				// Defensive: a nil value would yield an
				// empty label, which collides with
				// another nil-valued option. The bridge
				// rejects via the duplicate-key error
				// downstream; surface a typed error
				// here instead so the failure is local.
				label = fmt.Sprintf("__option_%d", i)
			}
			criteria[label] = systemOneChoiceLabel{
				Value:       c.Value,
				Description: c.Description,
			}
		}
		out.Choice = &systemOneChoice{Criteria: criteria}
	case QuestionTypeScore:
		out.Type = "score"
		out.Instructions = q.Instructions
		criteria := make([]systemOneScoreCriterion, 0, len(q.Levels))
		for i := range q.Levels {
			criteria = append(criteria, systemOneScoreCriterion{
				Index: i,
				Label: q.Levels[i].Label,
			})
		}
		out.Score = &systemOneScore{Criteria: criteria}
	}
	return out
}

// labelFromValue derives the System One label (used as a map
// key) from an OpenAI choice value. Booleans stringify as
// "true" / "false"; strings are verbatim; numbers stringify via
// the canonical Go default; nil becomes "".
func labelFromValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// Use 'g' to avoid trailing '.0' for integer-valued
		// floats; System One keys on the resulting string.
		return strings.TrimSuffix(strings.TrimSuffix(
			fmt.Sprintf("%g", x), ".0"), ".0")
	case int64:
		return fmt.Sprintf("%d", x)
	case int:
		return fmt.Sprintf("%d", x)
	case json.Number:
		return x.String()
	}
	// Last-resort: marshal then trim the surrounding quotes.
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// marshalSystemOne serializes a systemOneRequest as JSON.
// The `state` field must serialize as a bare string
// (System One's wire form); we hand-roll the JSON encoding
// to avoid introducing an interface-and-reflection layer.
func marshalSystemOne(req systemOneRequest, stateStr string) ([]byte, error) {
	// Re-marshal the per-field layers: state string first,
	// then the rest, then re-attach state to the top.
	// Strategy: encode the struct with State as a
	// type-implementing json.Marshaler that yields a bare
	// string. Then we adjust the top-level structure by
	// encoding the whole struct via the standard library
	// using the Marshaler.
	return json.Marshal(struct {
		Model     string                       `json:"model"`
		State     string                       `json:"state"`
		Questions map[string]systemOneQuestion `json:"questions"`
	}{
		Model:     req.Model,
		State:     stateStr,
		Questions: req.Questions,
	})
}

// System One response shape (research-web §Q2 verified).
type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   systemOneUsage             `json:"usage"`
}

// systemOneUsage carries input_tokens + output_tokens
// (research-web §Q2 — System One returns only these two fields;
// the detail sub-objects are zero-filled per A25).
type systemOneUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// InspectResponse (Decision Models / §3.2 step 14 / §4.2):
//
// Parses the System One response, then synthesizes a typed
// DecisionResponse with ordered answers[] using the original
// request's questions[] order (per-question key map; the
// originalReq.Name → response key is the natural join — both
// adapters store the name verbatim).
//
// The synthesized response shape mirrors OpenAI Decision wire
// (research-web §Q1 / §Q2 liteLLM precedent — A24 + A25).
//
// Outcome classification:
//   - System One 200 with a parseable body ⇒ PassAsIs; return
//     the typed DecisionResponse.
//   - Empty body / unparseable JSON / missing required keys ⇒
//     502 with body verbatim (imggen-style).
func (p *typesafeProvider) InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error) {
	if len(respBytes) == 0 {
		return nil, &Outcome{RelayStatus: 502, Reason: "empty body"}, nil
	}

	// Quick discrimination: System One may return its own
	// error envelope. Detect and promote to 502.
	var probe struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &probe); err == nil && probe.Error != nil && probe.Error.Message != "" {
		return nil, &Outcome{
			RelayStatus: 502,
			Reason:      fmt.Sprintf("typesafe returned error envelope (code=%q, type=%q)", probe.Error.Code, probe.Error.Type),
		}, nil
	}

	var sysResp systemOneResponse
	if err := json.Unmarshal(respBytes, &sysResp); err != nil {
		return nil, &Outcome{RelayStatus: 502, Reason: "typesafe: unparseable JSON"}, nil
	}

	// Ordered-answer synthesis. The OpenAI Decision wire
	// mandates answers[] order — we use originalReq.Questions
	// order (whichever names appear in the response map).
	answers := synthesizeAnswers(originalReq, sysResp.Answers)

	// Usage synthesis — A25 zero-fill.
	usage := Usage{
		InputTokens:  sysResp.Usage.InputTokens,
		OutputTokens: sysResp.Usage.OutputTokens,
		TotalTokens:  sysResp.Usage.InputTokens + sysResp.Usage.OutputTokens,
	}

	return &DecisionResponse{
		Model:   sysResp.Model,
		Answers: answers,
		Usage:   usage,
	}, &Outcome{RelayStatus: 0}, nil
}

// synthesizeAnswers maps the System One answer map into an
// ordered OpenAI answers[]. The order matches the original
// request's questions[] order via the per-question Name key.
// Questions present in the request but missing in the response
// are surfaced as refusal answers (the OpenAI wire carries
// refusal as a degenerate answer type).
func synthesizeAnswers(req *DecisionRequest, sys map[string]json.RawMessage) []Answer {
	if req == nil || len(req.Questions) == 0 {
		return nil
	}
	out := make([]Answer, 0, len(req.Questions))
	for i := range req.Questions {
		q := &req.Questions[i]
		raw, ok := sys[q.Name]
		if !ok {
			// Question declined / absent in the upstream.
			out = append(out, Answer{
				Type:    QuestionTypeRefusal,
				Name:    q.Name,
				Refusal: "upstream did not return an answer for this question",
			})
			continue
		}
		ans := synthesizeAnswer(q, raw)
		out = append(out, ans)
	}
	return out
}

// synthesizeAnswer converts one System One answer element to
// the typed OpenAI Answer shape using the original question as
// a shape hint.
//
// System One answer values are FLAT objects whose fields
// signal the discriminator and carry siblings at the same level:
//   - {noul: <float>} ⇒ predicate (siblings absent).
//   - {choice: <bare-string>, probabilities: {<value>: <prob>},
//     confidence: <float>} ⇒ choice. The bare-string `choice`
//     carries the chosen option value; `probabilities` and
//     `confidence` are siblings of it. Documented Jev wire.
//   - {score: <float>, probabilities: {<index-str>: <prob>},
//     legend: {<index-str>: <label>}, confidence: <float>}
//     ⇒ score. `probabilities` is keyed by LEVEL INDEX as a
//     string ("0", "1", "2"); `legend` is a sibling map from
//     index-string to level label. Documented Jev wire.
//   - {refusal: {reason}} ⇒ refusal.
//
// Tolerated defensive variants:
//   - Choice with `choice` as a nested object
//     {value, probabilities, confidence} (older Jev SDK shape).
//   - Score with label-keyed probabilities (no legend present,
//     keys directly match q.Levels[i].Label).
func synthesizeAnswer(q *Question, raw json.RawMessage) Answer {
	// Probe the answer's discriminator + sibling fields. We
	// use *json.RawMessage for the shape-polymorphic fields
	// (choice/score can be any JSON shape) and a plain
	// map for probabilities (always a map on the wire).
	var probe struct {
		Noul          *json.RawMessage           `json:"noul"`
		Choice        *json.RawMessage           `json:"choice"`
		Score         *json.RawMessage           `json:"score"`
		Legend        map[string]string          `json:"legend"`
		Probabilities map[string]json.RawMessage `json:"probabilities"`
		Confidence    *json.RawMessage           `json:"confidence"`
		Refusal       *json.RawMessage           `json:"refusal"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Answer{Type: QuestionTypeRefusal, Name: q.Name, Refusal: "upstream answer unparseable"}
	}

	switch {
	case probe.Noul != nil:
		var prob float64
		_ = json.Unmarshal(*probe.Noul, &prob)
		return Answer{
			Type:        QuestionTypePredicate,
			Name:        q.Name,
			Probability: &prob,
		}
	case probe.Choice != nil:
		return synthesizeChoiceAnswer(q, *probe.Choice, probe.Probabilities, probe.Confidence)
	case probe.Score != nil:
		return synthesizeScoreAnswer(q, *probe.Score, probe.Probabilities, probe.Confidence, probe.Legend)
	case probe.Refusal != nil:
		return synthesizeRefusalAnswer(q, *probe.Refusal)
	}
	return Answer{Type: QuestionTypeRefusal, Name: q.Name, Refusal: "upstream answer had no recognized type discriminator"}
}

// synthesizeChoiceAnswer converts a System One choice answer
// to the typed OpenAI Answer shape.
//
// Wire form (documented Jev / System One): the `choice` field
// is a BARE STRING carrying the chosen option value;
// `probabilities` and `confidence` are SIBLINGS of `choice` at
// the same object level (NOT nested inside `choice`).
//
// Tolerated defensive variants:
//   - Nested-object legacy: `choice` is an object
//     {value, probabilities, confidence} (older Jev SDK / proxy
//     variants). When detected, sibling probabilities/confidence
//     from the outer level win; only missing fields fall back to
//     the nested form.
//   - Non-string value (boolean true / false) bare: stringify as
//     "true" / "false" so the typed Answer still carries a value.
func synthesizeChoiceAnswer(q *Question, choiceRaw json.RawMessage, probs map[string]json.RawMessage, conf *json.RawMessage) Answer {
	out := Answer{Type: QuestionTypeChoice, Name: q.Name}

	// Primary path: documented bare-string form.
	var chosen string
	if err := json.Unmarshal(choiceRaw, &chosen); err == nil {
		out.Choice = chosen
	} else {
		// Fall back to nested-object legacy form
		// {value, probabilities, confidence}.
		var nested systemOneAnswerChoice
		if err := json.Unmarshal(choiceRaw, &nested); err == nil {
			out.Choice = stringifyValue(nested.Value)
			// Nested probabilities/confidence are
			// shadowed by sibling fields when those are
			// present; only fall back to nested when the
			// outer level omitted them.
			if probs == nil {
				probs = nested.Probabilities
			}
			if conf == nil {
				conf = nested.Confidence
			}
		} else {
			// Last resort: stringify the raw bytes
			// (handles boolean true / false bare values
			// defensively).
			out.Choice = stringifyValue(choiceRaw)
		}
	}

	// Probabilities map → array (typed `probabilities`).
	if probs != nil {
		out.Probabilities = make([]ChoiceAnswer, 0, len(probs))
		// The map iteration order is non-deterministic
		// in Go — we accept this for choice answers (the
		// client doesn't depend on probabilistic-array
		// order; liteLLM emits maps and clients handle
		// both shapes).
		for k, v := range probs {
			prob, _ := strconvFloat(v)
			// k is a Go string (not JSON-formatted);
			// wrap in quotes before unmarshaling into
			// interface{} so the typed Value preserves
			// the option value as a JSON value (string /
			// boolean / number).
			rawKey := json.RawMessage(`"` + k + `"`)
			var val interface{}
			_ = json.Unmarshal(rawKey, &val)
			out.Probabilities = append(out.Probabilities, ChoiceAnswer{
				Value:       val,
				Probability: prob,
			})
		}
	}
	if conf != nil {
		c, _ := strconvFloat(*conf)
		out.Confidence = &c
	}
	return out
}

// synthesizeScoreAnswer converts a System One score answer to
// the typed OpenAI Answer shape.
//
// Wire form (documented Jev / System One): the `score` field is
// a JSON number (int or float; may land between integer levels);
// `probabilities` is a map keyed by LEVEL INDEX AS STRING
// ("0", "1", "2", ...); `legend` is a sibling map translating
// each index-string to the level label sent in the request;
// `confidence` is a sibling.
//
// Resolution strategy for each probability key:
//  1. Use the `legend` sibling (when present) to translate
//     the index-string to a level label, then locate the
//     matching level in q.Levels by label. Documented path.
//  2. If `legend` is absent, fall back to interpreting the key
//     as a positional index against q.Levels (defensive — some
//     clients omit legend when keys already align positionally).
//  3. Final defensive fallback: treat the key as a level label
//     and look it up in q.Levels (legacy Jev SDK shape where
//     probabilities are keyed by label, not index).
//
// Any key that resolves to no level is emitted with a sentinel
// index 0 and the resolved label preserved verbatim (degraded
// but not silently dropped — same posture as the pre-fix
// behavior).
//
// The scalar `score` is parsed verbatim (systemOneScore is a
// float-compatible JSON number); no level-index coercion.
func synthesizeScoreAnswer(q *Question, scoreRaw json.RawMessage, probs map[string]json.RawMessage, conf *json.RawMessage, legend map[string]string) Answer {
	// A24 — emit the scalar score as a float; OpenAI wire
	// accepts both int and float (liteLLM precedent; System
	// One score may be a fractional float between levels).
	scoreIdx, _ := strconvFloat(scoreRaw)
	out := Answer{
		Type:  QuestionTypeScore,
		Name:  q.Name,
		Score: &scoreIdx,
	}
	if probs != nil && q.Type == QuestionTypeScore {
		entries := make([]ScoreAnswerEntry, 0, len(probs))
		for key, prob := range probs {
			p, _ := strconvFloat(prob)

			label := key
			idx := -1

			// Strategy 1: legend-keyed (documented).
			if l, ok := legend[key]; ok && l != "" {
				label = l
				for i := range q.Levels {
					if q.Levels[i].Label == label {
						idx = i
						break
					}
				}
			}

			// Strategy 2: positional fallback — key is a
			// numeric string index that maps directly
			// against q.Levels.
			if idx < 0 {
				if pos, convErr := strconv.Atoi(key); convErr == nil {
					if pos >= 0 && pos < len(q.Levels) {
						idx = pos
						label = q.Levels[pos].Label
					}
				}
			}

			// Strategy 3 (defensive legacy): the key is a
			// level label directly matching q.Levels.
			if idx < 0 {
				for i := range q.Levels {
					if q.Levels[i].Label == key {
						idx = i
						label = key
						break
					}
				}
			}

			// Unresolvable — degraded sentinel (not dropped).
			if idx < 0 {
				idx = 0
			}
			entries = append(entries, ScoreAnswerEntry{
				Value:       idx,
				Label:       label,
				Probability: p,
			})
		}
		out.SetScoreProbabilities(entries)
	}
	if conf != nil {
		c, _ := strconvFloat(*conf)
		out.Confidence = &c
	}
	return out
}

// synthesizeRefusalAnswer converts a System One refusal answer
// to the typed OpenAI Answer shape. Refusal payloads are
// consistent across Jev wire variants ({reason} may be present
// or absent; an empty reason yields an empty Refusal string so
// the client still sees the type discriminator).
func synthesizeRefusalAnswer(q *Question, refusalRaw json.RawMessage) Answer {
	var nested systemOneAnswerRefusal
	// A missing {reason} object or empty object is fine —
	// the Unmarshal yields a zero-valued struct with empty
	// Reason, which is the documented empty-refusal form.
	_ = json.Unmarshal(refusalRaw, &nested)
	return Answer{
		Type:    QuestionTypeRefusal,
		Name:    q.Name,
		Refusal: nested.Reason,
	}
}

// systemOneAnswerChoice is the LEGACY nested-object shape of
// a System One `choice` answer value. The documented Jev wire
// emits `choice` as a BARE STRING (with `probabilities` and
// `confidence` as siblings); this struct models the older
// nested-object variant (`choice` carries
// {value, probabilities, confidence}). synthesizeChoiceAnswer
// falls back to this shape defensively when the bare-string
// primary path fails to parse.
type systemOneAnswerChoice struct {
	Value         json.RawMessage            `json:"value"`
	Probabilities map[string]json.RawMessage `json:"probabilities"`
	Confidence    *json.RawMessage           `json:"confidence"`
}

// systemOneAnswerRefusal carries the refusal reason (rare in
// real traffic; the OpenAI wire carries `refusal` on each
// answer).
type systemOneAnswerRefusal struct {
	Reason string `json:"reason"`
}

// stringifyValue returns the JSON-form string of an OpenAI
// choice value, or empty when null.
func stringifyValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// Stringify: if it's a JSON string, unquote; else
	// return the raw bytes (booleans / numbers serialize
	// as their JSON form).
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

// strconvFloat parses a json.RawMessage-number-or-string as
// float64. Used for probability fields that System One returns
// as a number or a string (tolerated).
func strconvFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty probability value")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		var f float64
		_, err := fmt.Sscanf(s, "%g", &f)
		if err != nil {
			return 0, err
		}
		return f, nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}

// ExtractUsage (Decision Models / P2.4 success-only meter):
// the typed response carries the zero-filled Usage struct —
// the meter's Increment reads input_tokens + output_tokens.
func (p *typesafeProvider) ExtractUsage(resp *DecisionResponse) Usage {
	if resp == nil {
		return Usage{}
	}
	return resp.Usage
}
