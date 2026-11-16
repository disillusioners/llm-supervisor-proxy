package decision

// decision-groups commission — the decision PROVIDER VOCABULARY pins.
//
// The vocabulary is {openai, typesafe, openrouter} and it must not
// drift: a provider that is accepted at write time but has no adapter
// (or the other way round) is a "saveable-but-502" hole. The pins
// below hold the FIVE surfaces together:
//
//  1. the FE constant (the operator's picker),
//  2. the canonical gate (pkg/models — the reference rule),
//  3. the POST mirror (pkg/ui),
//  4. the PUT mirror (pkg/ui),
//  5. the registry init (this package — the adapters that exist).
//
// The credential vocabulary is a SEPARATE map (a credential may be
// "anthropic" while a decision MODEL may not) — the two maps are
// nested, never crossed.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// decisionVocabulary is the decision provider set (the single
// expectation every surface must agree with).
var decisionVocabulary = []string{"openai", "typesafe", "openrouter"}

// repoFile reads a repo-relative source file (the pins are
// SOURCE-LITERAL pins: the surfaces are literal sets in the source).
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces — the five
// vocabulary surfaces cannot drift independently: each carries the same
// {openai, typesafe, openrouter} set.
func TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces(t *testing.T) {
	surfaces := []struct {
		name string
		rel  string
		// needles are the literal tokens each surface must carry.
		needles []string
	}{
		{
			name:    "fe_constant",
			rel:     "pkg/ui/frontend/src/components/config/DecisionModelForm.tsx",
			needles: []string{"'openai'", "'typesafe'", "'openrouter'"},
		},
		{
			name:    "canonical_gate",
			rel:     "pkg/models/config.go",
			needles: []string{`case "openai", "typesafe", "openrouter":`},
		},
		{
			name:    "post_mirror",
			rel:     "pkg/ui/server.go",
			needles: []string{`primaryProvider != "openai" && primaryProvider != "typesafe" && primaryProvider != "openrouter"`},
		},
		{
			name:    "put_mirror",
			rel:     "pkg/ui/server.go",
			needles: []string{`primaryProvider != "openai" && primaryProvider != "typesafe" && primaryProvider != "openrouter"`},
		},
		{
			name:    "registry_init",
			rel:     "pkg/decision/registry.go",
			needles: []string{"OpenAIDecisionProviderName", "TypesafeProviderName", "OpenRouterDecisionProviderName"},
		},
	}
	for _, surface := range surfaces {
		src := repoFile(t, surface.rel)
		for _, needle := range surface.needles {
			if !strings.Contains(src, needle) {
				t.Errorf("the %s surface (%s) does not carry the decision vocabulary literal %q — the five surfaces must not drift independently", surface.name, surface.rel, needle)
			}
		}
	}
}

// TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList — the
// registry's key set EQUALS the canonical gate's allow-list: the
// anti-saveable-but-502 pin.
func TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList(t *testing.T) {
	// The registry's keys (the adapters that exist).
	registryKeys := map[string]bool{}
	for _, entry := range providerRegistry {
		_ = entry
	}
	for name := range providerRegistry {
		registryKeys[name] = true
	}

	// The canonical gate's allow-list (read from the source literal).
	gateSrc := repoFile(t, "pkg/models/config.go")
	re := regexp.MustCompile(`case "([a-z]+)", "([a-z]+)", "([a-z]+)":`)
	match := re.FindStringSubmatch(gateSrc)
	if match == nil {
		t.Fatalf("the canonical gate's allow-list literal was not found in pkg/models/config.go")
	}
	gateSet := map[string]bool{match[1]: true, match[2]: true, match[3]: true}

	if len(registryKeys) != len(gateSet) {
		t.Errorf("the registry key set %v and the gate allow-list %v differ in SIZE — a provider that saves but 502s (or the reverse) is a hole", registryKeys, gateSet)
	}
	for name := range gateSet {
		if !registryKeys[name] {
			t.Errorf("the gate allows %q but the registry has no adapter for it (saveable-but-502)", name)
		}
	}
	for name := range registryKeys {
		if !gateSet[name] {
			t.Errorf("the registry has an adapter for %q but the gate does not allow it (the two vocabularies must not drift)", name)
		}
	}
}
