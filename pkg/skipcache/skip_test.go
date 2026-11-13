package skipcache

import (
	"sync"
	"testing"
	"time"
)

// The skip cache is on the proxy hot path and is mutated from
// concurrent request goroutines — every behavior here is also pinned
// under `go test -race ./pkg/skipcache/...` (verification bar #4).

func TestMarkAndSkip(t *testing.T) {
	c := New()
	if c.Marked(ScopeCredential, "cred-1", "gpt-x") {
		t.Fatal("fresh cache must report nothing marked")
	}
	c.Mark(ScopeCredential, "cred-1", "gpt-x", time.Second)
	if !c.Marked(ScopeCredential, "cred-1", "gpt-x") {
		t.Fatal("marked pair must be skipped")
	}
	// Same credential, DIFFERENT model — not marked.
	if c.Marked(ScopeCredential, "cred-1", "other-model") {
		t.Fatal("mark is per (scope, id, model) — another model must not inherit it")
	}
	// Another credential, same model — not marked.
	if c.Marked(ScopeCredential, "cred-2", "gpt-x") {
		t.Fatal("mark is per credential — another credential must not inherit it")
	}
}

func TestProviderScopeIndependent(t *testing.T) {
	c := New()
	c.Mark(ScopeProvider, "openai", "gpt-x", time.Second)
	if !c.Marked(ScopeProvider, "openai", "gpt-x") {
		t.Fatal("provider-scope mark must be readable")
	}
	if c.Marked(ScopeCredential, "openai", "gpt-x") {
		t.Fatal("scopes are disjoint — a provider mark must not read as a credential mark")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := New()
	c.Mark(ScopeCredential, "cred-1", "gpt-x", 20*time.Millisecond)
	if !c.Marked(ScopeCredential, "cred-1", "gpt-x") {
		t.Fatal("mark must be live inside its TTL")
	}
	time.Sleep(40 * time.Millisecond)
	if c.Marked(ScopeCredential, "cred-1", "gpt-x") {
		t.Fatal("expired mark must not skip")
	}
}

func TestMarkZeroTTLNoOp(t *testing.T) {
	c := New()
	c.Mark(ScopeCredential, "cred-1", "gpt-x", 0)
	c.Mark(ScopeCredential, "cred-1", "gpt-x", -time.Second)
	c.Mark(ScopeCredential, "", "gpt-x", time.Second)
	c.Mark(ScopeCredential, "cred-1", "", time.Second)
	if c.Marked(ScopeCredential, "cred-1", "gpt-x") {
		t.Fatal("zero/negative TTL and empty ids must be no-ops")
	}
}

func TestMarkExtensionNeverShortens(t *testing.T) {
	c := New()
	long := time.Now().Add(time.Minute)
	c.marked = map[string]time.Time{key(ScopeCredential, "cred-1", "gpt-x"): long}
	// A short re-mark must not shorten the live window.
	c.Mark(ScopeCredential, "cred-1", "gpt-x", time.Millisecond)
	if got := c.marked[key(ScopeCredential, "cred-1", "gpt-x")]; !got.Equal(long) {
		t.Fatalf("re-mark shortened the window: %v → %v", long, got)
	}
}

func TestAnyMarked(t *testing.T) {
	c := New()
	pairs := []Pair{{ScopeCredential, "a"}, {ScopeCredential, "b"}, {ScopeProvider, "openai"}}
	if c.AnyMarked(pairs, "gpt-x") {
		t.Fatal("unmarked candidates: AnyMarked must be false")
	}
	c.Mark(ScopeProvider, "openai", "gpt-x", time.Second)
	if !c.AnyMarked(pairs, "gpt-x") {
		t.Fatal("a provider-scope mark must satisfy AnyMarked")
	}
	// Provider-wide wall shape: every pair of a provider marked.
	wall := []Pair{{ScopeProvider, "openai"}}
	if !c.AnyMarked(wall, "gpt-x") {
		t.Fatal("provider-wide wall must read as fully marked")
	}
}

func TestSoonestExpiryDegradationAnchor(t *testing.T) {
	c := New()
	if _, ok := c.SoonestExpiry([]Pair{{ScopeCredential, "a"}}, "gpt-x"); ok {
		t.Fatal("nothing marked: SoonestExpiry must report !ok")
	}
	c.Mark(ScopeCredential, "a", "gpt-x", 5*time.Second)
	c.Mark(ScopeCredential, "b", "gpt-x", time.Minute)
	soon, ok := c.SoonestExpiry([]Pair{{ScopeCredential, "a"}, {ScopeCredential, "b"}}, "gpt-x")
	if !ok {
		t.Fatal("marked candidates: SoonestExpiry must report ok")
	}
	if time.Until(soon) > 6*time.Second {
		t.Fatalf("soonest expiry = %v — want the 5s mark, not the 60s one", soon)
	}
}

func TestNilReceiverSafe(t *testing.T) {
	var c *Cache
	c.Mark(ScopeCredential, "a", "gpt-x", time.Second) // must not panic
	if c.Marked(ScopeCredential, "a", "gpt-x") {
		t.Fatal("nil cache must never skip")
	}
	if c.AnyMarked([]Pair{{ScopeCredential, "a"}}, "gpt-x") {
		t.Fatal("nil cache must never report marked")
	}
	if _, ok := c.SoonestExpiry(nil, "gpt-x"); ok {
		t.Fatal("nil cache must never report an expiry")
	}
}

func TestConcurrentMarkReadRace(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := string(rune('a' + n%4))
			for j := 0; j < 200; j++ {
				c.Mark(ScopeCredential, id, "gpt-x", time.Millisecond)
				_ = c.Marked(ScopeCredential, id, "gpt-x")
				_, _ = c.SoonestExpiry([]Pair{{ScopeCredential, id}}, "gpt-x")
			}
		}(i)
	}
	wg.Wait()
}
