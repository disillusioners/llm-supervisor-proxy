package credentiallb

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Known vectors (computed independently of the implementation):
//
//	sha256("m1|tok1|hello")
//	sha256("m1||hello")                      — anonymous (empty token) degrades to the double-separator form
//	sha256("m1|tok1|<canonical multimodal>") — canonical JSON with sorted keys,
//	                                           no whitespace, array elements sorted by rendered form
const (
	vecSaltedHex      = "e207c7c3ccaec24e3ef21ae6add6aa7dfbab1d3d9f71316bb641547dcfe01f82"
	vecAnonymousHex   = "b3963fee7aa7d603579566facc1f597cfa9e524249fbb2592119462d77e6a664"
	vecMultimodalHex  = "7d051e35dab5157256225a5dc8082a15df4647e06715e3e538ff0cdd3c2d3592"
	multimodalCanonic = `[{"text":"hello","type":"text"},{"type":"image","url":"x"}]`
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestKey_KnownVectors pins the exact hash formula byte-for-byte:
// sha256(modelID + "|" + tokenID + "|" + firstUserMessage), 64 hex
// chars, no truncation. The empty-token vector pins the anonymous
// degradation (A-1): the separators remain, only the salt material
// disappears.
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

// TestKey_StabilityAcrossTurns — appending later messages (assistant
// replies, more user turns) must not change the key: the key derives
// from the FIRST user message only.
func TestKey_StabilityAcrossTurns(t *testing.T) {
	first := []interface{}{
		map[string]interface{}{"role": "system", "content": "you are terse"},
		map[string]interface{}{"role": "user", "content": "hello"},
	}
	later := append(append([]interface{}{}, first...),
		map[string]interface{}{"role": "assistant", "content": "hi"},
		map[string]interface{}{"role": "user", "content": "and now something completely different"},
	)
	a := ExtractFirstUserMessage(first)
	b := ExtractFirstUserMessage(later)
	if a != "hello" || b != "hello" {
		t.Fatalf("first-user extraction unstable: first=%q later=%q", a, b)
	}
	ka := ComputeConversationKey("m1", "tok1", a)
	kb := ComputeConversationKey("m1", "tok1", b)
	if ka != kb {
		t.Fatalf("key changed across turns: %s vs %s", ka, kb)
	}
}

// TestKey_Multimodal_Determinism — A-2: multimodal content produces a
// stable canonical-JSON hash across runs, byte-stable against a known
// vector.
func TestKey_Multimodal_Determinism(t *testing.T) {
	msgs := []interface{}{
		map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "image", "url": "x"},
		}},
	}
	first := ComputeConversationKey("m1", "tok1", ExtractFirstUserMessage(msgs))
	for i := 0; i < 10; i++ {
		if got := ComputeConversationKey("m1", "tok1", ExtractFirstUserMessage(msgs)); got != first {
			t.Fatalf("multimodal hash unstable across runs: run %d got %s want %s", i, got, first)
		}
	}
	if want := sha256Hex("m1|tok1|" + multimodalCanonic); first != want {
		t.Fatalf("multimodal vector: got %s want %s (canonical=%q)", first, want, multimodalCanonic)
	}
}

// TestKey_Multimodal_ContentOrderInvariance — A-2 acceptance: the same
// multimodal parts in a different order produce the SAME hash
// (canonicalization sorts object keys AND rendered array elements).
func TestKey_Multimodal_ContentOrderInvariance(t *testing.T) {
	a := []interface{}{
		map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello"},
			map[string]interface{}{"type": "image", "url": "x"},
		}},
	}
	b := []interface{}{
		map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"url": "x", "type": "image"}, // keys in different order
			map[string]interface{}{"text": "hello", "type": "text"},
		}},
	}
	ka := ComputeConversationKey("m1", "tok1", ExtractFirstUserMessage(a))
	kb := ComputeConversationKey("m1", "tok1", ExtractFirstUserMessage(b))
	if ka != kb {
		t.Fatalf("content-order invariance violated: %s vs %s", ka, kb)
	}
}

// TestKey_Multimodal_GetsAffinity — the multimodal-as-"" fallback is
// REMOVED (A-2): multimodal first messages yield a non-empty key, so
// every request gets affinity.
func TestKey_Multimodal_GetsAffinity(t *testing.T) {
	msgs := []interface{}{
		map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "image", "url": "x"},
		}},
	}
	fum := ExtractFirstUserMessage(msgs)
	if fum == "" {
		t.Fatal("multimodal content extracted as empty — the removed fallback regressed")
	}
	if ComputeConversationKey("m1", "tok1", fum) == ComputeConversationKey("m1", "tok1", "") {
		t.Fatal("multimodal key collides with the empty-message key")
	}
}

// TestKey_NoUserRoleOrEmptyContent — empty/system-only/no-user-role ⇒
// ""; a user message with empty string content is skipped in favor of
// the NEXT user message.
func TestKey_NoUserRoleOrEmptyContent(t *testing.T) {
	if got := ExtractFirstUserMessage(nil); got != "" {
		t.Fatalf("nil messages: got %q want \"\"", got)
	}
	if got := ExtractFirstUserMessage([]interface{}{
		map[string]interface{}{"role": "system", "content": "sys"},
		map[string]interface{}{"role": "assistant", "content": "as"},
	}); got != "" {
		t.Fatalf("system/assistant only: got %q want \"\"", got)
	}
	if got := ExtractFirstUserMessage([]interface{}{
		map[string]interface{}{"role": "user"}, // missing content
		map[string]interface{}{"role": "user", "content": nil},
		map[string]interface{}{"role": "user", "content": ""},
		map[string]interface{}{"role": "user", "content": "real"},
	}); got != "real" {
		t.Fatalf("empty-content skip: got %q want \"real\"", got)
	}
	// Multimodal with no user-role message anywhere.
	if got := ExtractFirstUserMessage([]interface{}{
		map[string]interface{}{"role": "system", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": "s"},
		}},
	}); got != "" {
		t.Fatalf("multimodal system-only: got %q want \"\"", got)
	}
	// Non-map entries are skipped defensively.
	if got := ExtractFirstUserMessage([]interface{}{
		"garbage-string",
		42,
		map[string]interface{}{"role": "user", "content": "ok"},
	}); got != "ok" {
		t.Fatalf("non-map skip: got %q want \"ok\"", got)
	}
}
