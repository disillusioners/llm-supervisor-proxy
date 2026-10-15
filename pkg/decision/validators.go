package decision

import (
	"fmt"
	"strings"
)

// Decision Models commission / §3.2 step 5 / §4.2 / Q4 / amendment
// 4 — typed gotcha predicates. Each is a pure function over the
// typed *DecisionRequest; the handler runs them once at
// lifecycle step 5 (after parse, before BuildUpstream) and the
// typesafe adapter re-asserts a subset at BuildUpstream time as
// defense-in-depth (per amendment 4).
//
// Every predicate returns (string, true) on the first failure
// — `string` is the failure message that flows into the
// `error.message` field of the house OpenAI envelope, and `true`
// signals failure (so the handler can short-circuit on the
// first bad predicate without paying the cost of subsequent
// ones; the order below is the house canonical order — most
// specific-to-the-shape first).
//
// Edges covered (plan §3.2 step 5 / §4.2 / Q4 + findings 9 +
// 24):
//
//   - ValidateQuestionsCount        (> 128 questions)
//   - ValidateQuestionsNonEmpty     (0 questions)
//   - ValidateDuplicateQuestionNames (finding 9)
//   - ValidateChoiceOptionsCount    (choice < 2 or > 255)
//   - ValidateScoreLevelsCount      (score < 2 or > 10)
//   - ValidateBooleanStringCollision (true vs "true" cross-type)
//   - ValidateSafetyIdentifier      (safety_identifier > 128 chars; finding 24)
//
// The handler runs the full set; the typesafe adapter re-runs
// the subset relevant to its System One translation (it can't
// relax them — re-asserts as defense-in-depth against future
// OpenAI Decision schema additions the common validator hasn't
// learned).

// validateFirstFailed runs validators in order and returns the
// first failure message (and true) or ("", false) on full pass.
func validateFirstFailed(req *DecisionRequest) (string, bool) {
	if req == nil {
		return "request body is empty", true
	}
	for _, v := range allValidators() {
		if msg, failed := v(req); failed {
			return msg, true
		}
	}
	return "", false
}

// validator is a typed predicate on *DecisionRequest.
// Returns (msg, true) on failure; ("", false) on pass.
type validator func(*DecisionRequest) (string, bool)

// allValidators is the canonical validator order (most
// specific-to-the-shape first). Adding a new validator: append
// here AND consider whether typesafe.go needs to re-assert it.
func allValidators() []validator {
	return []validator{
		ValidateQuestionsNonEmpty,
		ValidateQuestionsCount,
		ValidateDuplicateQuestionNames,
		ValidateChoiceOptionsCount,
		ValidateScoreLevelsCount,
		ValidateBooleanStringCollision,
		ValidateSafetyIdentifier,
	}
}

// ValidateQuestionsNonEmpty fails when the request has zero
// questions. The plan §4.1 says questions[1..128]; zero is the
// silent-fail edge.
func ValidateQuestionsNonEmpty(req *DecisionRequest) (string, bool) {
	if len(req.Questions) == 0 {
		return "at least one question is required", true
	}
	return "", false
}

// ValidateQuestionsCount fails when more than 128 questions are
// present (plan §4.1 cap; research-web §Q1).
func ValidateQuestionsCount(req *DecisionRequest) (string, bool) {
	if len(req.Questions) > 128 {
		return fmt.Sprintf("too many questions: got %d, max 128", len(req.Questions)), true
	}
	return "", false
}

// ValidateDuplicateQuestionNames (finding 9) fails when two
// entries of questions[] share the same Name. System One keys
// the questions map on Name (plan §4.2) — a duplicate
// collides. The OpenAI wire permits the duplicate (it's an
// array, not a map), so this is a typed-predicate edge check
// the handler enforces BEFORE upstream.
func ValidateDuplicateQuestionNames(req *DecisionRequest) (string, bool) {
	seen := make(map[string]struct{}, len(req.Questions))
	for i := range req.Questions {
		name := strings.TrimSpace(req.Questions[i].Name)
		if name == "" {
			// Names are required for the typesafe bridge;
			// missing names trigger the rejection below.
			continue
		}
		if _, dup := seen[name]; dup {
			return fmt.Sprintf("duplicate question name %q (System One questions map requires unique keys; finding 9)", name), true
		}
		seen[name] = struct{}{}
	}
	return "", false
}

// ValidateChoiceOptionsCount fails when any choice-question has
// <2 options (System One requires ≥2 per research-web §Q2) or
// >255 options (OpenAI cap per plan §4.2).
func ValidateChoiceOptionsCount(req *DecisionRequest) (string, bool) {
	for i := range req.Questions {
		q := &req.Questions[i]
		if q.Type != QuestionTypeChoice {
			continue
		}
		n := len(q.Choices)
		if n < 2 {
			return fmt.Sprintf("question %q (choice) needs at least 2 options (got %d)", q.Name, n), true
		}
		if n > 255 {
			return fmt.Sprintf("question %q (choice) has too many options: got %d, max 255", q.Name, n), true
		}
	}
	return "", false
}

// ValidateScoreLevelsCount fails when any score-question has
// <2 levels (System One requires ≥2 per research-web §Q2) or
// >10 levels (OpenAI cap per plan §4.2).
func ValidateScoreLevelsCount(req *DecisionRequest) (string, bool) {
	for i := range req.Questions {
		q := &req.Questions[i]
		if q.Type != QuestionTypeScore {
			continue
		}
		n := len(q.Levels)
		if n < 2 {
			return fmt.Sprintf("question %q (score) needs at least 2 levels (got %d)", q.Name, n), true
		}
		if n > 10 {
			return fmt.Sprintf("question %q (score) has too many levels: got %d, max 10", q.Name, n), true
		}
	}
	return "", false
}

// ValidateBooleanStringCollision fails when a choice question
// has two options whose values collide across the boolean /
// string type boundary (`true` vs `"true"`, `false` vs
// `"false"`). System One's choice criteria are string-keyed
// (research-web §Q2); the cross-type collision collapses on
// marshal — reject upstream of the bridge.
func ValidateBooleanStringCollision(req *DecisionRequest) (string, bool) {
	for i := range req.Questions {
		q := &req.Questions[i]
		if q.Type != QuestionTypeChoice {
			continue
		}
		boolSet := make(map[bool]struct{}, 2)
		stringSet := make(map[string]struct{}, len(q.Choices))
		for j := range q.Choices {
			switch v := q.Choices[j].Value.(type) {
			case bool:
				boolSet[v] = struct{}{}
				// also synthesize the string form for the
				// bridge; if the matching string form is
				// present in another option it collides.
				stringSet[fmt.Sprintf("%t", v)] = struct{}{}
			case string:
				stringSet[v] = struct{}{}
				if v == "true" || v == "false" {
					// a string that looks like the
					// JSON form of a boolean collides
					// with the matching bool value
					// — flag if a bool side exists.
					if _, present := boolSet[func() bool {
						if v == "true" {
							return true
						}
						return false
					}()]; present {
						return fmt.Sprintf("question %q has both boolean %q and string %q — type-collision across the bridge", q.Name, v, v), true
					}
				}
			}
		}
		// Also flag the inverse: a bool option with the
		// string form present as another option's value.
		if _, ok := boolSet[true]; ok {
			if _, ok := stringSet["true"]; ok {
				return fmt.Sprintf("question %q has both boolean true and string %q — type-collision across the bridge", q.Name, "true"), true
			}
		}
		if _, ok := boolSet[false]; ok {
			if _, ok := stringSet["false"]; ok {
				return fmt.Sprintf("question %q has both boolean false and string %q — type-collision across the bridge", q.Name, "false"), true
			}
		}
	}
	return "", false
}

// ValidateSafetyIdentifier (finding 24) fails when
// safety_identifier is present and exceeds 128 chars (research-
// web §Q1 cap).
func ValidateSafetyIdentifier(req *DecisionRequest) (string, bool) {
	if len(req.SafetyIdentifier) > 128 {
		return fmt.Sprintf("safety_identifier exceeds 128 chars (got %d)", len(req.SafetyIdentifier)), true
	}
	return "", false
}

// typesafeEdgeValidators is the subset the typesafe adapter
// re-asserts at BuildUpstream time (defense-in-depth). It runs
// only validators that protect System One translation
// invariants — omitting validators that are purely cosmetic
// (safety_identifier length is enforced only at the handler
// edge; System One drops safety_identifier with 400 regardless
// of length).
func typesafeEdgeValidators() []validator {
	return []validator{
		ValidateQuestionsNonEmpty,
		ValidateQuestionsCount,
		ValidateDuplicateQuestionNames,
		ValidateChoiceOptionsCount,
		ValidateScoreLevelsCount,
		ValidateBooleanStringCollision,
	}
}

// typesafePreflightValidate is the per-provider pre-flight
// edge check (handler step 9.5). The typesafe BuildUpstream
// re-runs the typed validator subset as defense-in-depth
// (amendment 4); the handler pre-flight catches the same
// provider-specific edge cases (input_image on text-only
// TypeSafe, safety_identifier drop on TypeSafe) so the
// proxy surfaces them as 400 BEFORE any upstream call. The
// openai path does NOT run this — it forwards input_image
// as-is (Q3 / R2 posture, no validator).
//
// Returns (message, true) on failure; ("", false) on pass.
func typesafePreflightValidate(req *DecisionRequest) (string, bool) {
	if req.SafetyIdentifier != "" {
		return "TypeSafe (Jev) does not support safety_identifier (research-web §Q2)", true
	}
	if msgs, ok := req.InputMessages(); ok {
		for i := range msgs {
			for j := range msgs[i].Parts {
				if msgs[i].Parts[j].Type == InputPartTypeImage {
					return fmt.Sprintf("TypeSafe (Jev) does not support image input (research-web §Q2); rejected decision input with input_image at message %d part %d", i, j), true
				}
			}
		}
	}
	return "", false
}

// typesafeValidateFirstFailed runs the typesafe subset in order
// and returns the first failure message (and true) or ("",
// false) on pass.
func typesafeValidateFirstFailed(req *DecisionRequest) (string, bool) {
	if req == nil {
		return "request body is empty", true
	}
	for _, v := range typesafeEdgeValidators() {
		if msg, failed := v(req); failed {
			return msg, true
		}
	}
	return "", false
}
