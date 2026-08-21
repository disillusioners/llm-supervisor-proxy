// Scenarios A–D for the FE reasoning-observability closure gate.
//
//	A (CLOSURE GATE) — glm-5.3 non-stream response reasoning_content must
//	    appear as messages[N].thinking in the FE API detail payload.
//	B — request-side reasoning_content (DeepSeek-replay shape) must be
//	    captured as thinking on the request-side assistant message.
//	C — negative cleanliness: no reasoning anywhere ⇒ ZERO occurrences of
//	    the substring "thinking" in the FE detail payload (omitempty).
//	D — payload-shape static check: the 🧠 block in the FE renders
//	    `message.thinking`, matching the FE API field.
package e2e_fe_reasoning_observability

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// glmReasoning is the fixed multi-sentence reasoning constant the mock
// upstream returns — glm-5.3 style chain-of-thought, ≥2 sentences.
const glmReasoning = "Okay, the user is asking about the capital of France. Let me break this down step by step. The capital of France has been Paris for centuries, and it is a well-established fact. I should answer directly and concisely."

// feDetail decodes the FE detail payload into a RequestLog-shaped struct.
type feDetail struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Model    string          `json:"model"`
	Messages []store.Message `json:"messages"`
	IsStream bool            `json:"is_stream"`
}

// ═════════════════════════════════════════════════════════════════════════════
// Scenario A — CLOSURE GATE: glm-5.3 non-stream → FE API thinking
// ═════════════════════════════════════════════════════════════════════════════

// TestScenarioA_GLMNonStream_ReasoningVisibleInFEAPI reproduces the ORIGINAL
// bug scenario end-to-end and proves it is gone: the FE API detail payload
// must contain the assistant message's thinking, byte-exact.
func TestScenarioA_GLMNonStream_ReasoningVisibleInFEAPI(t *testing.T) {
	env := setupTestEnv(t, reasoningNonStreamHandler(glmReasoning))

	// Client sends a NON-STREAMING request (no "stream" field at all).
	rr := env.run(chatRequest{
		model:    glmModel,
		token:    env.plainToken,
		messages: []map[string]interface{}{{"role": "user", "content": "What is the capital of France?"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("client status=%d body=%s", rr.Code, rr.Body.String())
	}

	// (2) Obtain the request id via the FE list endpoint (newest-first).
	id := env.latestRequestID(t)
	t.Logf("EVIDENCE A: request id used = %s", id)

	// (3) GET the detail over real HTTP.
	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("FE detail status=%d body=%s", status, string(raw))
	}

	// (5) Raw-HTTP-level symptom check: the ORIGINAL bug was ZERO
	// occurrences of "thinking" in this payload.
	occurrences := strings.Count(string(raw), `"thinking"`)
	if occurrences == 0 {
		t.Fatalf("A: FE detail payload contains ZERO occurrences of \"thinking\" — ORIGINAL BUG REPRODUCED. Payload:\n%s", string(raw))
	}
	t.Logf("EVIDENCE A: FE detail payload contains %d occurrence(s) of \"thinking\"", occurrences)

	// (4) Assert the assistant message thinking byte-exact.
	var detail feDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("A: FE detail not JSON: %v — %s", err, string(raw))
	}
	msgs := detail.Messages
	if len(msgs) < 2 {
		t.Fatalf("A: expected ≥2 messages (user + assistant), got %d — %s", len(msgs), string(raw))
	}
	assistant := msgs[len(msgs)-1]
	if assistant.Role != "assistant" {
		t.Fatalf("A: last message role=%q, want assistant — %s", assistant.Role, string(raw))
	}
	if assistant.Thinking != glmReasoning {
		t.Errorf("A: assistant.thinking=%q\n  want (byte-exact)=%q\n  full payload:\n%s",
			assistant.Thinking, glmReasoning, string(raw))
	} else {
		t.Logf("EVIDENCE A: FE detail messages[%d].thinking == expected glm reasoning (%d bytes, byte-exact)",
			len(msgs)-1, len(glmReasoning))
	}

	// (6) Belt-and-braces: same value via direct store read.
	stored := env.reqStore.Get(id)
	if stored == nil {
		t.Fatalf("A: reqStore.Get(%s) returned nil", id)
	}
	if len(stored.Messages) < 2 {
		t.Fatalf("A: store has %d messages, want ≥2 — %s", len(stored.Messages), mustJSON(stored))
	}
	storedAssistant := stored.Messages[len(stored.Messages)-1]
	if storedAssistant.Thinking != glmReasoning {
		t.Errorf("A: reqStore.Get(%s).Messages[last].Thinking=%q, want %q", id, storedAssistant.Thinking, glmReasoning)
	} else {
		t.Logf("EVIDENCE A: reqStore.Get(%s).Messages[last].Thinking matches (belt-and-braces)", id)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Scenario B — request-side reasoning_content (DeepSeek-replay shape)
// ═════════════════════════════════════════════════════════════════════════════

// TestScenarioB_RequestSideReasoningCaptured sends messages = [user,
// assistant WITH reasoning_content, user] against a plain upstream and
// asserts the request-side assistant message (messages[1]) carries thinking
// in the FE API, while the response assistant message (last) has NO thinking
// field (omitempty ⇒ absent in JSON).
func TestScenarioB_RequestSideReasoningCaptured(t *testing.T) {
	const sentReasoning = "Previous turn reasoning: I evaluated the options and picked the simplest approach before answering."
	env := setupTestEnv(t, plainNonStreamHandler())

	rr := env.run(chatRequest{
		model: glmModel,
		token: env.plainToken,
		messages: []map[string]interface{}{
			{"role": "user", "content": "What is 2+2?"},
			{"role": "assistant", "content": "The answer is 4.", "reasoning_content": sentReasoning},
			{"role": "user", "content": "Are you sure?"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("client status=%d body=%s", rr.Code, rr.Body.String())
	}

	id := env.latestRequestID(t)
	t.Logf("EVIDENCE B: request id used = %s", id)

	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("B: FE detail status=%d body=%s", status, string(raw))
	}

	var detail feDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("B: FE detail not JSON: %v — %s", err, string(raw))
	}

	// Request-side assistant message is messages[1] (user, assistant, user,
	// then response assistant appended). Find it by position: with the
	// response appended there are 4 messages; the request-side assistant
	// is index 1.
	msgs := detail.Messages
	if len(msgs) != 4 {
		t.Fatalf("B: expected 4 messages [user, assistant(req), user, assistant(resp)], got %d — %s", len(msgs), string(raw))
	}
	if got := msgs[1].Thinking; got != sentReasoning {
		t.Errorf("B: request-side messages[1].thinking=%q\n  want (byte-exact)=%q\n  payload:\n%s", got, sentReasoning, string(raw))
	} else {
		t.Logf("EVIDENCE B: messages[1].thinking == sent reasoning_content (%d bytes, byte-exact)", len(sentReasoning))
	}

	// Response assistant message (last): thinking must be ABSENT —
	// omitempty means the key is not present at all. Re-check on the raw
	// JSON: decode last message generically.
	var generic struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("B: re-decode: %v", err)
	}
	last := generic.Messages[len(generic.Messages)-1]
	if _, present := last["thinking"]; present {
		t.Errorf("B: response assistant message carries thinking=%q but upstream returned NO reasoning (omitempty must omit) — payload:\n%s", last["thinking"], string(raw))
	} else {
		t.Logf("EVIDENCE B: response-side messages[%d] has NO thinking field (omitempty clean)", len(generic.Messages)-1)
	}
	if msgs[3].Thinking != "" {
		t.Errorf("B: response-side messages[3].Thinking=%q, want empty string", msgs[3].Thinking)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Scenario C — negative cleanliness: zero "thinking" occurrences
// ═════════════════════════════════════════════════════════════════════════════

// TestScenarioC_NoReasoning_ZeroThinkingOccurrences asserts that when
// neither request nor response carries reasoning, the FE detail payload
// contains ZERO occurrences of the substring "thinking" — omitempty keeps
// the payload clean.
func TestScenarioC_NoReasoning_ZeroThinkingOccurrences(t *testing.T) {
	env := setupTestEnv(t, plainNonStreamHandler())

	rr := env.run(chatRequest{
		model:    glmModel,
		token:    env.plainToken,
		messages: []map[string]interface{}{{"role": "user", "content": "Say ok"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("client status=%d body=%s", rr.Code, rr.Body.String())
	}

	id := env.latestRequestID(t)
	t.Logf("EVIDENCE C: request id used = %s", id)

	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("C: FE detail status=%d body=%s", status, string(raw))
	}

	if n := strings.Count(string(raw), "thinking"); n != 0 {
		t.Errorf("C: FE detail payload contains %d occurrence(s) of substring \"thinking\" but NO reasoning was sent or returned — payload:\n%s", n, string(raw))
	} else {
		t.Logf("EVIDENCE C: FE detail payload contains ZERO occurrences of \"thinking\" (omitempty clean)")
	}

	var generic struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("C: decode: %v — %s", err, string(raw))
	}
	if len(generic.Messages) < 2 {
		t.Fatalf("C: expected ≥2 messages, got %d — %s", len(generic.Messages), string(raw))
	}
	for i, m := range generic.Messages {
		if _, present := m["thinking"]; present {
			t.Errorf("C: messages[%d] carries thinking=%q despite no reasoning anywhere", i, m["thinking"])
		}
	}
	assistant := generic.Messages[len(generic.Messages)-1]
	if assistant["role"] != "assistant" {
		t.Errorf("C: last message role=%v, want assistant", assistant["role"])
	}
	t.Logf("EVIDENCE C: assistant message decodes cleanly with no thinking field")
}

// ═════════════════════════════════════════════════════════════════════════════
// Scenario D — payload-shape static check (FE 🧠 block reads message.thinking)
// ═════════════════════════════════════════════════════════════════════════════

// TestScenarioD_FEBlockReadsThinkingField statically verifies that the
// React component which renders the 🧠 Thinking block reads exactly the
// field the FE API returns: `message.thinking` (pkg/store/memory.go:23
// `json:"thinking,omitempty"`). The component lives at
// pkg/ui/frontend/src/components/RequestDetail.tsx (~line 697).
//
// If the FE were reading a DIFFERENT field name than `thinking`, the 🧠
// block would never render even with a correct API — that is a FAIL and is
// reported loudly.
func TestScenarioD_FEBlockReadsThinkingField(t *testing.T) {
	// A test binary's working directory is its PACKAGE directory
	// (test/e2e_fe_reasoning_observability/), not the repo root — walk up
	// two levels to reach the repo root.
	const componentRel = "../../pkg/ui/frontend/src/components/RequestDetail.tsx"

	src, err := os.ReadFile(componentRel)
	if err != nil {
		t.Fatalf("D: cannot read %s: %v", componentRel, err)
	}
	text := string(src)

	// The 🧠 block guard: `{message.thinking && (` — the exact field the
	// block renders. Look for the three hard requirements:
	//   1. guard on `message.thinking`
	//   2. renders `message.thinking` as the 🧠 text
	//   3. a 🧠 marker on the thinking block (it is the Thinking summary)
	hasGuard := strings.Contains(text, "message.thinking &&")
	hasRender := strings.Count(text, "message.thinking") >= 2
	hasBrain := strings.Contains(text, "🧠")

	if !hasGuard {
		t.Errorf("D: 🧠 Thinking block does NOT guard on `message.thinking` — FE reads a different/unrecognizable trigger. Inspect %s", componentRel)
	}
	if !hasRender {
		t.Errorf("D: component references `message.thinking` fewer than 2 times — the block may not render the API field")
	}
	if !hasBrain {
		t.Errorf("D: no 🧠 marker found in %s — could not locate the Thinking block", componentRel)
	}

	if hasGuard && hasRender && hasBrain {
		t.Logf("EVIDENCE D: %s guards on `message.thinking &&` and renders it inside the 🧠 Thinking block — matches FE API field `messages[i].thinking` (store.Message.Thinking, json:\"thinking,omitempty\")", componentRel)
	} else {
		t.Logf("D: FAIL detail — guard=%v renderRefs=%v brain=%v", hasGuard, hasRender, hasBrain)
	}
}
