package credentiallb

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Known vectors (computed independently of the implementation):
//
//	sha256("m1|tok1|hello")                      — raw-string hash-FORMULA pin
//	sha256("m1||hello")                          — anonymous (empty token) degrades to the double-separator form
//	sha256("m1|tok1|[\"hello\"]")                — DERIVATION vector: single user message → canonical one-element run
//	sha256("m1||[\"hello\"]")                    — anonymous derivation vector
//	sha256("m1|tok1|[[mm-canonic]]")             — multimodal single user message → run wraps the canonical content array
//
// The 2026-10-17 leading-user-run change keeps the hash formula
// (modelID|tokenID|X) but X is now the canonical JSON ARRAY of the
// leading contiguous user-message run — hence the array-wrapped
// derivation vectors.
const (
	vecSaltedHex        = "e207c7c3ccaec24e3ef21ae6add6aa7dfbab1d3d9f71316bb641547dcfe01f82"
	vecAnonymousHex     = "b3963fee7aa7d603579566facc1f597cfa9e524249fbb2592119462d77e6a664"
	vecRunHelloHex      = "c1ff9fa58786f4cfc489507213ce1bc1fa82c230228473814e1d2bf03dda6828"
	vecRunHelloAnonHex  = "b642201e25daa05006fa7c93772ee0e5684e161cf7388e4323102af78a3a6cea"
	vecRunMultimodalHex = "bf074caca38beae8f94bda53443a1908ccbbea97482160775ddffc06c88bd0e8"
	multimodalCanonic   = `[{"text":"hello","type":"text"},{"type":"image","url":"x"}]`
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// userMsg builds a {"role": "user", "content": ...} map (test shorthand).
func userMsg(content interface{}) map[string]interface{} {
	return map[string]interface{}{"role": "user", "content": content}
}

// TestKey_KnownVectors pins the exact hash formula byte-for-byte:
// sha256(modelID + "|" + tokenID + "|" + material), 64 hex chars, no
// truncation. The empty-token vector pins the anonymous degradation
// (A-1): the separators remain, only the salt material disappears.
// The material here is a raw string — the formula-level contract; the
// DERIVATION-level vectors (what the handlers actually feed in) are
// pinned in TestKey_DerivationVectors below.
func TestKey_KnownVectors(t *testing.T) {
	if got := ComputeConversationKey("m1", "tok1", "hello"); got != vecSaltedHex {
		t.Fatalf("salted vector: got %s want %s", got, vecSaltedHex)
	}
	if got := ComputeConversationKey("m1", "", "hello"); got != vecAnonymousHex {
		t.Fatalf("anonymous vector: got %s want %s", got, vecAnonymousHex)
	}
	if got := len(ComputeConversationKey("m1", "tok1", "hello")); got != 64 {
		t.Fatalf("key length: got %d want 64", got)
	}
}

// TestKey_DerivationVectors pins the full derivation end-to-end:
// ExtractLeadingUserRun of a single-user-message conversation yields
// the canonical one-element JSON array ["hello"], and hashing it
// byte-exactly matches the independently computed vectors — salted
// and anonymous (A-1). Multimodal: the run wraps the canonical
// content array, so the material is [[...canonic...]].
func TestKey_DerivationVectors(t *testing.T) {
	msgs := []interface{}{userMsg("hello")}
	run := ExtractLeadingUserRun(msgs)
	if run != `["hello"]` {
		t.Fatalf("single-user run: got %s want [\"hello\"]", run)
	}
	if got := ComputeConversationKey("m1", "tok1", run); got != vecRunHelloHex {
		t.Fatalf("salted derivation vector: got %s want %s", got, vecRunHelloHex)
	}
	if got := ComputeConversationKey("m1", "", run); got != vecRunHelloAnonHex {
		t.Fatalf("anonymous derivation vector: got %s want %s", got, vecRunHelloAnonHex)
	}

	mmMsgs := []interface{}{userMsg([]interface{}{
		map[string]interface{}{"type": "text", "text": "hello"},
		map[string]interface{}{"type": "image", "url": "x"},
	})}
	mmRun := ExtractLeadingUserRun(mmMsgs)
	if want := "[" + multimodalCanonic + "]"; mmRun != want {
		t.Fatalf("multimodal run: got %s want %s", mmRun, want)
	}
	if got := ComputeConversationKey("m1", "tok1", mmRun); got != vecRunMultimodalHex {
		t.Fatalf("multimodal derivation vector: got %s want %s", got, vecRunMultimodalHex)
	}
}

// TestKey_AnonymousDegradesToUnsalted — A-1 acceptance: two anonymous
// calls with the same message + model produce the same key, and that
// key equals the unsalted sha256 over the double-separator
// concatenation (byte-exact against an independent computation).
func TestKey_AnonymousDegradesToUnsalted(t *testing.T) {
	a := ComputeConversationKey("m1", "", "hello")
	b := ComputeConversationKey("m1", "", "hello")
	if a != b {
		t.Fatalf("anonymous keys differ: %s vs %s", a, b)
	}
	if want := sha256Hex("m1||hello"); a != want {
		t.Fatalf("anonymous key != unsalted sha256: got %s want %s", a, want)
	}
}

// TestKey_TokenIsolation — A-1: same model + same message, different
// tokens ⇒ different keys.
func TestKey_TokenIsolation(t *testing.T) {
	a := ComputeConversationKey("m1", "tok1", "hello")
	b := ComputeConversationKey("m1", "tok2", "hello")
	if a == b {
		t.Fatalf("token isolation violated: tok1 and tok2 produced the same key")
	}
}

// TestKey_ModelIsolation — same token + same message, different models
// ⇒ different keys.
func TestKey_ModelIsolation(t *testing.T) {
	a := ComputeConversationKey("m1", "tok1", "hello")
	b := ComputeConversationKey("m2", "tok1", "hello")
	if a == b {
		t.Fatalf("model isolation violated: m1 and m2 produced the same key")
	}
}

// TestKey_StabilityAcrossTurns — TURN-STABILITY (spec item e):
// appending later messages (assistant replies, more user turns) to the
// TAIL must not change the key: the key derives from the LEADING
// CONTIGUOUS run of user messages, and tail turns never touch it.
func TestKey_StabilityAcrossTurns(t *testing.T) {
	first := []interface{}{
		map[string]interface{}{"role": "system", "content": "you are terse"},
		userMsg("hello"),
	}
	later := append(append([]interface{}{}, first...),
		map[string]interface{}{"role": "assistant", "content": "hi"},
		userMsg("and now something completely different"),
	)
	a := ExtractLeadingUserRun(first)
	b := ExtractLeadingUserRun(later)
	if a != `["hello"]` || b != `["hello"]` {
		t.Fatalf("leading-run extraction unstable across turns: first=%q later=%q", a, b)
	}
	ka := ComputeConversationKey("m1", "tok1", a)
	kb := ComputeConversationKey("m1", "tok1", b)
	if ka != kb {
		t.Fatalf("key changed across turns: %s vs %s", ka, kb)
	}
}

// TestKey_LeadingRun_MultiUserWithInterleavedSystem — spec item (a):
// a run of 2-3 CONSECUTIVE user messages is collected in full, and a
// system message INTERLEAVED between them neither joins the run nor
// terminates it.
func TestKey_LeadingRun_MultiUserWithInterleavedSystem(t *testing.T) {
	two := []interface{}{userMsg("a"), userMsg("b")}
	if got := ExtractLeadingUserRun(two); got != `["a","b"]` {
		t.Fatalf("two-user run: got %s want [\"a\",\"b\"]", got)
	}

	interleaved := []interface{}{
		userMsg("a"),
		map[string]interface{}{"role": "system", "content": "context insert"},
		userMsg("b"),
	}
	if got := ExtractLeadingUserRun(interleaved); got != `["a","b"]` {
		t.Fatalf("interleaved-system run: got %s want [\"a\",\"b\"] (system must not terminate the walk)", got)
	}

	three := []interface{}{
		userMsg("a"),
		map[string]interface{}{"role": "system", "content": "ctx"},
		userMsg("b"),
		map[string]interface{}{"role": "system", "content": "ctx2"},
		userMsg("c"),
	}
	if got := ExtractLeadingUserRun(three); got != `["a","b","c"]` {
		t.Fatalf("three-user run: got %s want [\"a\",\"b\",\"c\"]", got)
	}

	// The interleaved-system list and the plain consecutive list hash
	// IDENTICALLY — the system message contributes no key material.
	if ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(two)) !=
		ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(interleaved)) {
		t.Fatal("interleaved system message changed the key — it must be transparent to the run")
	}
}

// TestKey_SystemThenUsersThenAssistant — spec item (b): system
// preamble is skipped, the user run is collected, and the FIRST
// assistant terminates the walk — a trailing user message after the
// assistant is NOT part of the leading run.
func TestKey_SystemThenUsersThenAssistant(t *testing.T) {
	msgs := []interface{}{
		map[string]interface{}{"role": "system", "content": "sys"},
		userMsg("a"),
		userMsg("b"),
		map[string]interface{}{"role": "assistant", "content": "reply"},
		userMsg("after-assistant"),
	}
	got := ExtractLeadingUserRun(msgs)
	if got != `["a","b"]` {
		t.Fatalf("terminated run: got %s want [\"a\",\"b\"]", got)
	}
	// Same key as a plain [a, b] conversation…
	if ComputeConversationKey("m1", "tok1", got) != ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun([]interface{}{userMsg("a"), userMsg("b")})) {
		t.Fatal("terminated run key differs from the equivalent leading-run key")
	}
	// …and DIFFERENT from the key the trailing user would produce if
	// the assistant failed to terminate the walk.
	if got == ExtractLeadingUserRun([]interface{}{userMsg("a"), userMsg("b"), userMsg("after-assistant")}) {
		t.Fatal("post-assistant user message leaked into the leading run")
	}
}

// TestKey_EmptyUserSkippedMidRun — spec item (c): a user message with
// empty/unusable content in the MIDDLE of the run is skipped without
// terminating the walk (retired-extractor predicate, unchanged).
func TestKey_EmptyUserSkippedMidRun(t *testing.T) {
	msgs := []interface{}{
		userMsg("a"),
		userMsg(""),
		userMsg([]interface{}{}), // empty multimodal array — degenerate
		userMsg(nil),             // null content
		userMsg("b"),
	}
	run := ExtractLeadingUserRun(msgs)
	if run != `["a","b"]` {
		t.Fatalf("mid-run empties: got %s want [\"a\",\"b\"]", run)
	}
	// Identical to the run with the empties absent.
	if run != ExtractLeadingUserRun([]interface{}{userMsg("a"), userMsg("b")}) {
		t.Fatal("mid-run empty user messages changed the key")
	}
}

// TestKey_JoinCollisionSafety — spec item (d): the run join is the
// canonical JSON array, NOT a "|" concatenation — a message containing
// a literal "|" must not alias into a different grouping.
func TestKey_JoinCollisionSafety(t *testing.T) {
	single := []interface{}{userMsg("a|b")}            // ONE message "a|b"
	split := []interface{}{userMsg("a"), userMsg("b")} // TWO messages "a", "b"

	ra, rs := ExtractLeadingUserRun(single), ExtractLeadingUserRun(split)
	if ra != `["a|b"]` || rs != `["a","b"]` {
		t.Fatalf("run renderings: single=%q split=%q — must differ", ra, rs)
	}
	if ComputeConversationKey("m1", "tok1", ra) == ComputeConversationKey("m1", "tok1", rs) {
		t.Fatal("collision: [\"a|b\"] and [\"a\",\"b\"] hashed equal — the array join is not collision-safe")
	}
	// The bare-"|" concatenation would have collided here; pin that the
	// canonical forms really are different texts.
	if ra == rs {
		t.Fatal("run texts equal — collision guard is vacuous")
	}
}

// TestKey_RunOrderCanonicalized — AMBIGUITY-(i) PIN: the collected
// run's outer array is canonicalized with SORTED element order (the
// A-2 machinery, reused verbatim), so a reordered run hashes EQUAL.
// If the ordering policy ever flips to walk-order-preservation, this
// test is the one that must be inverted.
func TestKey_RunOrderCanonicalized(t *testing.T) {
	ab := []interface{}{userMsg("a"), userMsg("b")}
	ba := []interface{}{userMsg("b"), userMsg("a")}
	if got, want := ExtractLeadingUserRun(ba), ExtractLeadingUserRun(ab); got != want {
		t.Fatalf("reordered run not canonicalized: %q vs %q", got, want)
	}
	if ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(ab)) !=
		ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(ba)) {
		t.Fatal("reordered run produced a different key — outer array must be element-sorted")
	}
}

// TestKey_AssistantBeforeUser_EmptyRun — AMBIGUITY-(ii) PIN: a walk
// that hits the assistant (or any other non-system/non-user role)
// BEFORE any user message yields an empty run ⇒ key "". This is an
// INTENTIONAL semantic change vs the retired extractor, which searched
// for the first user message ANYWHERE in the list (it would have
// returned "hello" here); the leading run stops at the first
// non-system/non-user entry instead.
func TestKey_AssistantBeforeUser_EmptyRun(t *testing.T) {
	assistantFirst := []interface{}{
		map[string]interface{}{"role": "assistant", "content": "prefilled"},
		userMsg("hello"),
	}
	if got := ExtractLeadingUserRun(assistantFirst); got != "" {
		t.Fatalf("assistant-first run: got %q want \"\" (leading run must stop at the assistant)", got)
	}
	// Other roles terminate too — mid-run this time.
	toolMid := []interface{}{
		userMsg("a"),
		map[string]interface{}{"role": "tool", "content": "result"},
		userMsg("b"),
	}
	if got := ExtractLeadingUserRun(toolMid); got != `["a"]` {
		t.Fatalf("tool-terminated run: got %q want [\"a\"]", got)
	}
	// And a non-map entry (no role at all) terminates the walk — the
	// "any OTHER role → STOP" rule covers malformed entries.
	garbage := []interface{}{
		"garbage-string",
		userMsg("ok"),
	}
	if got := ExtractLeadingUserRun(garbage); got != "" {
		t.Fatalf("garbage-terminated run: got %q want \"\"", got)
	}
	// …so these degenerate conversations get a fresh weighted pick
	// (W-2), not affinity derived from a message past the boundary.
	if ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(assistantFirst)) != ComputeConversationKey("m1", "tok1", "") {
		t.Fatal("empty run must hash to the no-affinity key")
	}
}

// TestKey_Multimodal_Determinism — A-2: multimodal content produces a
// stable canonical-JSON hash across runs, byte-stable against a known
// vector (the run wraps the canonical content array).
func TestKey_Multimodal_Determinism(t *testing.T) {
	msgs := []interface{}{
		userMsg([]interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "image", "url": "x"},
		}),
	}
	first := ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(msgs))
	for i := 0; i < 10; i++ {
		if got := ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(msgs)); got != first {
			t.Fatalf("multimodal hash unstable across runs: run %d got %s want %s", i, got, first)
		}
	}
	if want := sha256Hex("m1|tok1|[" + multimodalCanonic + "]"); first != want {
		t.Fatalf("multimodal vector: got %s want %s (canonical=%q)", first, want, "["+multimodalCanonic+"]")
	}
}

// TestKey_Multimodal_ContentOrderInvariance — A-2 acceptance: the same
// multimodal parts in a different order produce the SAME hash
// (canonicalization sorts object keys AND rendered array elements).
func TestKey_Multimodal_ContentOrderInvariance(t *testing.T) {
	a := []interface{}{
		userMsg([]interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "image", "url": "x"},
		}),
	}
	b := []interface{}{
		userMsg([]interface{}{
			map[string]interface{}{"url": "x", "type": "image"}, // keys in different order
			map[string]interface{}{"text": "hello", "type": "text"},
		}),
	}
	ka := ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(a))
	kb := ComputeConversationKey("m1", "tok1", ExtractLeadingUserRun(b))
	if ka != kb {
		t.Fatalf("content-order invariance violated: %s vs %s", ka, kb)
	}
}

// TestKey_Multimodal_GetsAffinity — the multimodal-as-"" fallback is
// REMOVED (A-2): multimodal user messages yield a non-empty run, so
// every request gets affinity.
func TestKey_Multimodal_GetsAffinity(t *testing.T) {
	msgs := []interface{}{
		userMsg([]interface{}{
			map[string]interface{}{"type": "image", "url": "x"},
		}),
	}
	run := ExtractLeadingUserRun(msgs)
	if run == "" {
		t.Fatal("multimodal content extracted as empty — the removed fallback regressed")
	}
	if ComputeConversationKey("m1", "tok1", run) == ComputeConversationKey("m1", "tok1", "") {
		t.Fatal("multimodal key collides with the empty-message key")
	}
}

// TestKey_NoUserRoleOrEmptyContent — nil / system-only / no-user-role
// ⇒ ""; empty-content user messages are skipped in favor of usable
// ones; non-map entries TERMINATE (leading-run semantics — see the
// ambiguity-(ii) pin test above).
func TestKey_NoUserRoleOrEmptyContent(t *testing.T) {
	if got := ExtractLeadingUserRun(nil); got != "" {
		t.Fatalf("nil messages: got %q want \"\"", got)
	}
	if got := ExtractLeadingUserRun([]interface{}{
		map[string]interface{}{"role": "system", "content": "sys"},
		map[string]interface{}{"role": "assistant", "content": "as"},
	}); got != "" {
		t.Fatalf("system/assistant only: got %q want \"\"", got)
	}
	// Empty-content user messages before the usable one are skipped,
	// NOT terminating (run = the usable content only).
	if got := ExtractLeadingUserRun([]interface{}{
		map[string]interface{}{"role": "user"}, // missing content
		map[string]interface{}{"role": "user", "content": nil},
		map[string]interface{}{"role": "user", "content": ""},
		userMsg("real"),
	}); got != `["real"]` {
		t.Fatalf("empty-content skip: got %q want [\"real\"]", got)
	}
	// Multimodal with no user-role message anywhere.
	if got := ExtractLeadingUserRun([]interface{}{
		map[string]interface{}{"role": "system", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "s"},
		}},
	}); got != "" {
		t.Fatalf("multimodal system-only: got %q want \"\"", got)
	}
	// A role-less map (missing "role" key) is an OTHER-role entry →
	// terminates the walk (documented semantic; old code skipped it).
	if got := ExtractLeadingUserRun([]interface{}{
		map[string]interface{}{"content": "roleless"},
		userMsg("ok"),
	}); got != "" {
		t.Fatalf("roleless-map terminator: got %q want \"\"", got)
	}
}
