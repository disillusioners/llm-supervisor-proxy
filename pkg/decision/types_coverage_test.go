package decision

// Coverage pass for the typed-seam accessors (Decision
// Models / §10 P4.1 — package coverage ≥ 85%): the Answer
// union's MarshalJSON / UnmarshalJSON discriminator branches
// and the DecisionRequest input-accessor edge cases.
// Same-package white-box, stdlib-only, house style.

import (
	"encoding/json"
	"testing"
)

// ---------- Answer.MarshalJSON ----------

// TestAnswerMarshalJSON_ScoreProbabilitiesOnWire — a score
// answer's level-indexed distribution re-emerges on the wire
// under the shared `probabilities` key with value/label/
// probability fields.
func TestAnswerMarshalJSON_ScoreProbabilitiesOnWire(t *testing.T) {
	score := 1.0
	a := Answer{Type: QuestionTypeScore, Name: "s", Score: &score}
	a.SetScoreProbabilities([]ScoreAnswerEntry{{Value: 1, Label: "angry", Probability: 0.3}})
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Type          string `json:"type"`
		Score         *float64 `json:"score"`
		Probabilities []ScoreAnswerEntry `json:"probabilities"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v; body=%s", err, b)
	}
	if wire.Type != "score" || wire.Score == nil || *wire.Score != 1 {
		t.Errorf("wire = %+v, want type score + score 1", wire)
	}
	if len(wire.Probabilities) != 1 || wire.Probabilities[0] != (ScoreAnswerEntry{Value: 1, Label: "angry", Probability: 0.3}) {
		t.Errorf("wire probabilities = %+v, want the score entry", wire.Probabilities)
	}
}

// TestAnswerMarshalJSON_ChoiceProbabilitiesOnWire — a choice
// answer's distribution goes out under `probabilities` with
// the raw value preserved (boolean values stay booleans).
func TestAnswerMarshalJSON_ChoiceProbabilitiesOnWire(t *testing.T) {
	a := Answer{
		Type: QuestionTypeChoice, Name: "c", Choice: "billing",
		Probabilities: []ChoiceAnswer{
			{Value: "billing", Probability: 0.6},
			{Value: true, Probability: 0.4},
		},
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Choice        string         `json:"choice"`
		Probabilities []ChoiceAnswer `json:"probabilities"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v; body=%s", err, b)
	}
	if wire.Choice != "billing" {
		t.Errorf("wire choice = %q, want billing", wire.Choice)
	}
	if len(wire.Probabilities) != 2 || wire.Probabilities[1].Value != true {
		t.Errorf("wire probabilities = %+v, want boolean value preserved", wire.Probabilities)
	}
}

// ---------- Answer.UnmarshalJSON ----------

// TestAnswerUnmarshalJSON_ScoreRoundTrip — a score wire shape
// parses into the union-typed receiver: scoreProbabilities is
// populated (readable via the accessor) while the choice field
// stays empty.
func TestAnswerUnmarshalJSON_ScoreRoundTrip(t *testing.T) {
	var a Answer
	data := []byte(`{"type":"score","name":"s","score":1,"confidence":0.9,
		"probabilities":[{"value":1,"label":"angry","probability":0.3}]}`)
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Type != QuestionTypeScore || a.Score == nil || *a.Score != 1 {
		t.Errorf("answer = %+v, want type score + score 1", a)
	}
	if a.Confidence == nil || *a.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", a.Confidence)
	}
	entries := a.GetScoreProbabilities()
	if len(entries) != 1 || entries[0] != (ScoreAnswerEntry{Value: 1, Label: "angry", Probability: 0.3}) {
		t.Errorf("score probabilities = %+v, want the single entry", entries)
	}
	if a.Probabilities != nil {
		t.Errorf("choice probabilities = %+v, want nil on a score answer", a.Probabilities)
	}
}

// TestAnswerUnmarshalJSON_ChoiceRoundTrip — a choice wire
// shape parses into Answer.Probabilities with typed values
// (string and boolean values both preserved).
func TestAnswerUnmarshalJSON_ChoiceRoundTrip(t *testing.T) {
	var a Answer
	data := []byte(`{"type":"choice","name":"c","choice":"billing",
		"probabilities":[{"value":"billing","probability":0.6},{"value":true,"probability":0.4}]}`)
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Type != QuestionTypeChoice || a.Choice != "billing" {
		t.Errorf("answer = %+v, want type choice + choice billing", a)
	}
	if len(a.Probabilities) != 2 {
		t.Fatalf("probabilities = %+v, want 2 entries", a.Probabilities)
	}
	if a.Probabilities[0].Value != "billing" || a.Probabilities[1].Value != true {
		t.Errorf("probabilities values = %+v, want billing then true", a.Probabilities)
	}
}

// TestAnswerUnmarshalJSON_Errors — invalid JSON, and
// per-entry probability shapes that don't match the
// discriminator's entry type, all surface errors.
func TestAnswerUnmarshalJSON_Errors(t *testing.T) {
	var a Answer
	if err := json.Unmarshal([]byte(`{`), &a); err == nil {
		t.Errorf("invalid JSON: err = nil, want error")
	}
	if err := json.Unmarshal([]byte(`{"type":"choice","probabilities":[5]}`), &a); err == nil {
		t.Errorf("choice entry = number: err = nil, want error")
	}
	if err := json.Unmarshal([]byte(`{"type":"score","probabilities":["x"]}`), &a); err == nil {
		t.Errorf("score entry = string: err = nil, want error")
	}
}

// ---------- DecisionRequest input accessors ----------

// TestDecisionInputAccessors_EdgeCases — the three input
// accessors agree on every input shape: empty, whitespace
// only, string, whitespace-padded string, unterminated string
// (IsText's first-byte probe says true, InputText's decode
// says false), non-string scalar, valid array, and malformed
// array.
func TestDecisionInputAccessors_EdgeCases(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantIsText    bool
		wantText      string
		wantTextOK    bool
		wantMsgsOK    bool
		wantMsgsCount int
	}{
		{name: "empty", input: ``, wantIsText: false, wantText: "", wantTextOK: false, wantMsgsOK: false},
		{name: "whitespace_only", input: "   \n\t ", wantIsText: false, wantText: "", wantTextOK: false, wantMsgsOK: false},
		{name: "string", input: `"hi"`, wantIsText: true, wantText: "hi", wantTextOK: true, wantMsgsOK: false},
		{name: "padded_string", input: `  "hi"  `, wantIsText: true, wantText: "hi", wantTextOK: true, wantMsgsOK: false},
		{name: "unterminated_string", input: `"abc`, wantIsText: true, wantText: "", wantTextOK: false, wantMsgsOK: false},
		{name: "number_scalar", input: `42`, wantIsText: false, wantText: "", wantTextOK: false, wantMsgsOK: false},
		{
			name: "valid_array", input: `[{"role":"user","parts":[{"type":"input_text","text":"a"}]}]`,
			wantIsText: false, wantText: "", wantTextOK: false, wantMsgsOK: true, wantMsgsCount: 1,
		},
		{name: "malformed_array", input: `[{"role":}`, wantIsText: false, wantText: "", wantTextOK: false, wantMsgsOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &DecisionRequest{Input: json.RawMessage(tc.input)}
			if got := req.InputIsText(); got != tc.wantIsText {
				t.Errorf("InputIsText = %v, want %v", got, tc.wantIsText)
			}
			text, ok := req.InputText()
			if ok != tc.wantTextOK || text != tc.wantText {
				t.Errorf("InputText = (%q, %v), want (%q, %v)", text, ok, tc.wantText, tc.wantTextOK)
			}
			msgs, ok := req.InputMessages()
			if ok != tc.wantMsgsOK || len(msgs) != tc.wantMsgsCount {
				t.Errorf("InputMessages = (%d msgs, %v), want (%d msgs, %v)", len(msgs), ok, tc.wantMsgsCount, tc.wantMsgsOK)
			}
		})
	}
}
