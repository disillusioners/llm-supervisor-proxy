package decision

// Coverage pass for the TypeSafe adapter levers (Decision
// Models / §10 P4.1 — package coverage ≥ 85%). Unit tests for
// the small pure helpers that the P2 handler-level suite only
// touches on their happy paths: labelFromValue, strconvFloat,
// stringifyValue, deriveStateText, synthesizeAnswer(s), and
// the BuildUpstream / InspectResponse error branches.
// Same-package white-box, stdlib-only, house style.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// ---------- labelFromValue ----------

// TestLabelFromValue_AllTypes — every branch of the label
// derivation: nil, string, bool (both), float64 ('g' form),
// int64, int, json.Number, JSON-marshal fallback, and the
// marshal-failure fallback ("").
func TestLabelFromValue_AllTypes(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "string", in: "billing", want: "billing"},
		{name: "bool_true", in: true, want: "true"},
		{name: "bool_false", in: false, want: "false"},
		{name: "float_int_valued", in: float64(2), want: "2"},
		{name: "float_fractional", in: float64(2.5), want: "2.5"},
		{name: "int64", in: int64(7), want: "7"},
		{name: "int", in: 9, want: "9"},
		{name: "json_number", in: json.Number("42"), want: "42"},
		{name: "marshal_fallback", in: []string{"a"}, want: `["a"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := labelFromValue(tc.in)
			if got != tc.want {
				t.Errorf("labelFromValue(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// Marshal-failure fallback: channels are not JSON-marshalable
	// → empty label.
	if got := labelFromValue(make(chan int)); got != "" {
		t.Errorf("labelFromValue(channel) = %q, want empty", got)
	}
}

// ---------- strconvFloat ----------

// TestStrconvFloat_ParseForms — numeric raw, quoted-numeric
// string, and the three error shapes (empty, non-numeric
// string, non-number raw).
func TestStrconvFloat_ParseForms(t *testing.T) {
	cases := []struct {
		name    string
		raw     json.RawMessage
		want    float64
		wantErr bool
	}{
		{name: "empty", raw: nil, wantErr: true},
		{name: "raw_number", raw: json.RawMessage(`0.25`), want: 0.25},
		{name: "raw_int", raw: json.RawMessage(`3`), want: 3},
		{name: "quoted_numeric", raw: json.RawMessage(`"0.7"`), want: 0.7},
		{name: "quoted_non_numeric", raw: json.RawMessage(`"abc"`), wantErr: true},
		{name: "quoted_malformed", raw: json.RawMessage(`"\q"`), wantErr: true},
		{name: "raw_not_a_number", raw: json.RawMessage(`[1,2]`), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := strconvFloat(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("strconvFloat(%s) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("strconvFloat(%s): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("strconvFloat(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// ---------- stringifyValue ----------

// TestStringifyValue_Forms — empty/null → "", strings unquote,
// non-string raw passes through as JSON text, malformed quoted
// string falls back to the raw bytes.
func TestStringifyValue_Forms(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "empty", raw: nil, want: ""},
		{name: "null", raw: json.RawMessage(`null`), want: ""},
		{name: "string", raw: json.RawMessage(`"hello"`), want: "hello"},
		{name: "bool", raw: json.RawMessage(`true`), want: "true"},
		{name: "number", raw: json.RawMessage(`42`), want: "42"},
		{name: "malformed_quoted", raw: json.RawMessage(`"unterminated`), want: `"unterminated`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stringifyValue(tc.raw); got != tc.want {
				t.Errorf("stringifyValue(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// ---------- deriveStateText ----------

// TestDeriveStateText_Rejections — every reject branch:
// neither-string-nor-array, empty part type, input_image,
// literal "image" type alias, unknown part type, and a text
// array with no text content.
func TestDeriveStateText_Rejections(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantSub  string
	}{
		{name: "neither_string_nor_array", input: `42`, wantSub: "neither a string nor a message array"},
		{name: "empty_part_type", input: `[{"role":"user","parts":[{"type":"","text":"x"}]}]`, wantSub: "has no type"},
		{name: "input_image", input: `[{"role":"user","parts":[{"type":"input_image","image_url":"data:image/png;base64,AAA"}]}]`, wantSub: "does not support image input"},
		{name: "image_alias", input: `[{"role":"user","parts":[{"type":"image","image_url":"https://x/y.png"}]}]`, wantSub: "does not support image input"},
		{name: "unknown_type", input: `[{"role":"user","parts":[{"type":"input_audio","text":"x"}]}]`, wantSub: `unknown type "input_audio"`},
		{name: "no_text_parts", input: `[{"role":"user","parts":[]}]`, wantSub: "no text parts to translate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &DecisionRequest{Input: json.RawMessage(tc.input)}
			_, err := deriveStateText(req)
			if err == nil {
				t.Fatalf("deriveStateText(%s) = nil error, want %q", tc.input, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("deriveStateText(%s) error = %q, want contains %q", tc.input, err.Error(), tc.wantSub)
			}
		})
	}
}

// TestDeriveStateText_JoinedText — a text-only message array
// joins part text across messages in order (documented
// per-message-structure loss).
func TestDeriveStateText_JoinedText(t *testing.T) {
	req := &DecisionRequest{Input: json.RawMessage(`[
		{"role":"user","parts":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]},
		{"role":"user","parts":[{"type":"input_text","text":"c"}]}
	]`)}
	got, err := deriveStateText(req)
	if err != nil {
		t.Fatalf("deriveStateText: %v", err)
	}
	if got != "abc" {
		t.Errorf("state = %q, want %q", got, "abc")
	}
}

// ---------- typesafe BuildUpstream error branches ----------

func typesafeCred() models.ResolvedCredential {
	return models.ResolvedCredential{
		Provider: "typesafe", InternalModel: "jev-1.13.0",
		APIKey: "k", BaseURL: "https://api.typesafe.ai",
	}
}

// TestTypesafeBuildUpstream_NilReq — nil typed request → typed
// validation error mentioning the adapter.
func TestTypesafeBuildUpstream_NilReq(t *testing.T) {
	p := NewTypesafeProvider()
	_, _, err := p.BuildUpstream(typesafeCred(), nil, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("BuildUpstream(nil) err = %v, want nil-request error", err)
	}
}

// TestTypesafeBuildUpstream_FirstFailedValidator — the
// defense-in-depth re-assert fires on a shape validator the
// handler edge would also catch (choice with 1 option).
func TestTypesafeBuildUpstream_FirstFailedValidator(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Input: json.RawMessage(`"x"`),
		Questions: []Question{
			{Type: QuestionTypeChoice, Name: "dept", Choices: []ChoiceOption{{Value: "only"}}},
		},
	}
	_, _, err := p.BuildUpstream(typesafeCred(), req, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "needs at least 2 options") {
		t.Errorf("BuildUpstream err = %v, want first-failed choice-count error", err)
	}
}

// TestTypesafeBuildUpstream_SafetyIdentifier — short
// (validator-passing) safety_identifier is still dropped by
// the adapter itself.
func TestTypesafeBuildUpstream_SafetyIdentifier(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x", Input: json.RawMessage(`"x"`),
		Questions:        []Question{{Type: QuestionTypePredicate, Name: "a"}},
		SafetyIdentifier: "abc",
	}
	_, _, err := p.BuildUpstream(typesafeCred(), req, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "does not support safety_identifier") {
		t.Errorf("BuildUpstream err = %v, want safety_identifier rejection", err)
	}
}

// TestTypesafeBuildUpstream_DeriveErrorPropagates — a
// non-string non-array Input surfaces deriveStateText's typed
// error verbatim (not wrapped).
func TestTypesafeBuildUpstream_DeriveErrorPropagates(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x", Input: json.RawMessage(`42`),
		Questions: []Question{{Type: QuestionTypePredicate, Name: "a"}},
	}
	_, _, err := p.BuildUpstream(typesafeCred(), req, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "neither a string nor a message array") {
		t.Errorf("BuildUpstream err = %v, want deriveStateText error", err)
	}
}

// TestTypesafeBuildUpstream_QuestionNameEdges — System One is
// keyed by Name: empty name and duplicate names are rejected
// at BuildUpstream (defense-in-depth).
func TestTypesafeBuildUpstream_QuestionNameEdges(t *testing.T) {
	p := NewTypesafeProvider()
	_, _, err := p.BuildUpstream(typesafeCred(), &DecisionRequest{
		Input:     json.RawMessage(`"x"`),
		Questions: []Question{{Type: QuestionTypePredicate, Name: "   "}},
	}, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("empty-name err = %v, want no-name rejection", err)
	}
	_, _, err = p.BuildUpstream(typesafeCred(), &DecisionRequest{
		Input: json.RawMessage(`"x"`),
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "dup"},
			{Type: QuestionTypePredicate, Name: "dup"},
		},
	}, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), `duplicate question name "dup"`) {
		t.Errorf("dup-name err = %v, want duplicate-key rejection", err)
	}
}

// ---------- typesafe InspectResponse error branches ----------

// TestTypesafeInspectResponse_ErrorBranches — empty body,
// System One error envelope, and unparseable JSON all promote
// to 502 with distinct reasons.
func TestTypesafeInspectResponse_ErrorBranches(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{Questions: []Question{{Type: QuestionTypePredicate, Name: "a"}}}

	_, outcome, err := p.InspectResponse(nil, req)
	if err != nil || outcome == nil || outcome.RelayStatus != 502 || outcome.Reason != "empty body" {
		t.Errorf("empty body: outcome=%+v err=%v, want 502/empty body", outcome, err)
	}

	_, outcome, err = p.InspectResponse([]byte(`{"error":{"message":"quota exceeded","code":"q7","type":"limits"}}`), req)
	if err != nil || outcome == nil || outcome.RelayStatus != 502 {
		t.Fatalf("error envelope: outcome=%+v err=%v, want 502", outcome, err)
	}
	if !strings.Contains(outcome.Reason, `code="q7"`) || !strings.Contains(outcome.Reason, `type="limits"`) {
		t.Errorf("error envelope reason = %q, want code+type echoed", outcome.Reason)
	}

	_, outcome, err = p.InspectResponse([]byte(`not-json{`), req)
	if err != nil || outcome == nil || outcome.RelayStatus != 502 || outcome.Reason != "typesafe: unparseable JSON" {
		t.Errorf("unparseable: outcome=%+v err=%v, want 502/unparseable", outcome, err)
	}
}

// ---------- synthesizeAnswers / synthesizeAnswer ----------

// TestSynthesizeAnswers_NilAndEmptyQuestions — a nil request
// or a request with zero questions synthesizes no answers.
func TestSynthesizeAnswers_NilAndEmptyQuestions(t *testing.T) {
	if got := synthesizeAnswers(nil, map[string]json.RawMessage{"a": json.RawMessage(`{}`)}); got != nil {
		t.Errorf("synthesizeAnswers(nil) = %+v, want nil", got)
	}
	if got := synthesizeAnswers(&DecisionRequest{}, nil); got != nil {
		t.Errorf("synthesizeAnswers(empty) = %+v, want nil", got)
	}
}

// TestSynthesizeAnswers_MissingKey_Refusal — a question absent
// from the upstream map becomes a refusal answer carrying the
// question name.
func TestSynthesizeAnswers_MissingKey_Refusal(t *testing.T) {
	req := &DecisionRequest{Questions: []Question{{Type: QuestionTypePredicate, Name: "ghost"}}}
	got := synthesizeAnswers(req, map[string]json.RawMessage{})
	if len(got) != 1 {
		t.Fatalf("answers = %+v, want 1 refusal", got)
	}
	a := got[0]
	if a.Type != QuestionTypeRefusal || a.Name != "ghost" || a.Refusal != "upstream did not return an answer for this question" {
		t.Errorf("refusal answer = %+v, want refusal/ghost/declined message", a)
	}
}

// TestSynthesizeAnswer_RefusalAndFallbacks — refusal with and
// without a reason, unparseable raw, and a raw with no
// recognized discriminator.
func TestSynthesizeAnswer_RefusalAndFallbacks(t *testing.T) {
	q := &Question{Type: QuestionTypePredicate, Name: "a"}

	withReason := synthesizeAnswer(q, json.RawMessage(`{"refusal":{"reason":"policy"}}`))
	if withReason.Type != QuestionTypeRefusal || withReason.Refusal != "policy" {
		t.Errorf("refusal-with-reason = %+v, want refusal/policy", withReason)
	}
	noReason := synthesizeAnswer(q, json.RawMessage(`{"refusal":{}}`))
	if noReason.Type != QuestionTypeRefusal || noReason.Refusal != "" {
		t.Errorf("refusal-no-reason = %+v, want refusal with empty reason", noReason)
	}
	unparseable := synthesizeAnswer(q, json.RawMessage(`{bad`))
	if unparseable.Refusal != "upstream answer unparseable" {
		t.Errorf("unparseable = %+v, want unparseable refusal", unparseable)
	}
	noDiscriminator := synthesizeAnswer(q, json.RawMessage(`{}`))
	if noDiscriminator.Refusal != "upstream answer had no recognized type discriminator" {
		t.Errorf("no-discriminator = %+v, want fallback refusal", noDiscriminator)
	}
}

// TestSynthesizeAnswer_ScoreUnknownLabel_SentinelIndex — a
// probability label absent from the request's levels[] still
// emits an entry with the sentinel index 0 (degraded, not
// dropped).
func TestSynthesizeAnswer_ScoreUnknownLabel_SentinelIndex(t *testing.T) {
	q := &Question{Type: QuestionTypeScore, Name: "s", Levels: []ScoreLevel{{Label: "calm"}}}
	a := synthesizeAnswer(q, json.RawMessage(`{"score":1,"probabilities":{"mystery":0.5}}`))
	if a.Score == nil || *a.Score != 1 {
		t.Errorf("score = %v, want 1", a.Score)
	}
	entries := a.GetScoreProbabilities()
	if len(entries) != 1 {
		t.Fatalf("score probabilities = %+v, want 1 entry", entries)
	}
	if entries[0].Value != 0 || entries[0].Label != "mystery" || entries[0].Probability != 0.5 {
		t.Errorf("entry = %+v, want {0 mystery 0.5}", entries[0])
	}
}

// TestSynthesizeAnswer_ScoreShapeOnNonScoreQuestion — a
// score-shaped raw answer for a non-score question emits the
// score value but NO synthesized probabilities (the
// q.Type == score guard).
func TestSynthesizeAnswer_ScoreShapeOnNonScoreQuestion(t *testing.T) {
	q := &Question{Type: QuestionTypePredicate, Name: "p"}
	a := synthesizeAnswer(q, json.RawMessage(`{"score":0,"probabilities":{"calm":0.9}}`))
	if a.Type != QuestionTypeScore || a.Score == nil || *a.Score != 0 {
		t.Errorf("answer = %+v, want score answer with score 0", a)
	}
	if entries := a.GetScoreProbabilities(); entries != nil {
		t.Errorf("score probabilities = %+v, want nil (non-score question)", entries)
	}
}

// ---------- ExtractUsage / translateQuestion ----------

// TestTypesafeExtractUsage_NilResponse — nil typed response →
// zero Usage.
func TestTypesafeExtractUsage_NilResponse(t *testing.T) {
	if got := (NewTypesafeProvider()).ExtractUsage(nil); got != (Usage{}) {
		t.Errorf("ExtractUsage(nil) = %+v, want zero Usage", got)
	}
}

// TestTranslateQuestion_Fallbacks — predicate without
// instructions synthesizes the generic fallback; a choice
// option with nil value gets the __option_N sentinel label.
func TestTranslateQuestion_Fallbacks(t *testing.T) {
	pred := translateQuestion(&Question{Type: QuestionTypePredicate, Name: "a"})
	if pred.Type != "noul" {
		t.Errorf("predicate type = %q, want noul", pred.Type)
	}
	if pred.Instructions != "Is the following statement true?" {
		t.Errorf("predicate instructions = %q, want synthesized fallback", pred.Instructions)
	}

	choice := translateQuestion(&Question{Type: QuestionTypeChoice, Name: "c", Choices: []ChoiceOption{{Value: nil}}})
	if choice.Choice == nil || choice.Choice.Criteria == nil {
		t.Fatalf("choice translation missing criteria: %+v", choice)
	}
	if _, ok := choice.Choice.Criteria["__option_0"]; !ok {
		t.Errorf("criteria = %+v, want __option_0 sentinel key", choice.Choice.Criteria)
	}
}
