package proxy

// credential-groups / T-23d — A-2 "raw-list gate" census.
//
// Every gate in this file used to read model.Credentials RAW. For a
// GROUP-primary model (one raw ref) that silently disabled the
// headline behaviour while every path still returned 200:
//
//   1. race_coordinator.go — credential-failover ELIGIBILITY
//   2. race_coordinator.go — the failover BUDGET
//   3. race_executor.go   — credBelongs membership scan
//   4. race_executor.go   — the skip-cache REORDER gate + candidate scan
//   5. race_executor.go   — raceIntProviderIsMiniMax (the flat primary)
//   6. internal_handler.go — the /v1/messages rotation gate
//   7. handler_anthropic.go — the Anthropic-passthrough rotation gate
//   8. pkg/ui/server.go   — the six 9router-primary guards
//   9. race_coordinator.go — modelBudget() (LOG-ONLY)
//
// The proxy-side rows are pinned here (rows 1-7, 9); row 8 is pinned
// in pkg/ui (the UI owns its own guards).
//
// pkg/proxy NEVER imports pkg/store/database — the flattening seam is
// reached through models.ModelsConfigInterface only.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
)

// groupAwareResolver is a models.ModelsConfigInterface whose model
// carries a CREDENTIAL GROUP reference and whose flattening seam
// expands that group into its members.
//
// It mirrors the store's authoritative flatten (pkg/store/database
// FlattenCredentialRefs): the group expands IN PLACE, each member
// inherits the group ref's weight, and the result is deduped
// keep-first. The affinity seam resolves through the FLATTENED list
// too (the store-side resolver does the same — C2).
type groupAwareResolver struct {
	*mockModelsConfig
	groups map[string][]string
}

func (g *groupAwareResolver) FlattenCredentialRefs(model *models.ModelConfig) []models.CredentialRef {
	if model == nil || len(model.Credentials) == 0 {
		return nil
	}
	out := make([]models.CredentialRef, 0, len(model.Credentials))
	seen := make(map[string]bool, len(model.Credentials))
	appendRef := func(ref models.CredentialRef) {
		if ref.CredentialID == "" || seen[ref.CredentialID] {
			return
		}
		seen[ref.CredentialID] = true
		out = append(out, ref)
	}
	for _, ref := range model.Credentials {
		if ref.GroupID == "" {
			appendRef(ref)
			continue
		}
		for _, member := range g.groups[ref.GroupID] {
			appendRef(models.CredentialRef{CredentialID: member, Weight: ref.Weight, Position: ref.Position})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (g *groupAwareResolver) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	mc := g.GetModel(modelID)
	if mc == nil || !mc.Internal {
		return models.ResolvedCredential{}, false
	}
	flat := g.FlattenCredentialRefs(mc)
	if len(flat) == 0 {
		return models.ResolvedCredential{}, false
	}
	cred := g.GetCredential(flat[0].CredentialID)
	if cred == nil {
		return models.ResolvedCredential{}, false
	}
	baseURL := mc.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}
	return models.ResolvedCredential{
		Provider:      cred.Provider,
		APIKey:        cred.APIKey,
		BaseURL:       baseURL,
		InternalModel: mc.InternalModel,
		CredentialID:  flat[0].CredentialID,
	}, true
}

// newGroupModelResolver builds a resolver over ONE group-primary
// model ("m1") whose group "g1" holds the given member credential ids.
func newGroupModelResolver(t *testing.T, memberIDs ...string) *groupAwareResolver {
	t.Helper()
	mock := &mockModelsConfig{}
	for i, id := range memberIDs {
		mock.AddCredential(models.CredentialConfig{
			ID:       id,
			Provider: "openai",
			APIKey:   "key-" + id,
			BaseURL:  "http://upstream-" + id,
		})
		_ = i
	}
	mock.AddModel(models.ModelConfig{
		ID:            "m1",
		Name:          "m1",
		Enabled:       true,
		Internal:      true,
		InternalModel: "upstream-m1",
		// ONE raw ref — the group reference. Every gate below used to
		// read this list raw and therefore saw a single credential.
		Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
	})
	return &groupAwareResolver{
		mockModelsConfig: mock,
		groups:           map[string][]string{"g1": memberIDs},
	}
}

// TestA2_Gate_GroupPrimaryModelIsFailoverEligible (rows 1 + 2):
// a group-primary model with TWO members must be failover-ELIGIBLE and
// its budget must be members−1 (not 0).
//
// Before the fix the raw list had length 1 ⇒ eligibility false ⇒ the
// upstream 429 bubbled out of the model instead of rotating to the
// group's second member.
func TestA2_Gate_GroupPrimaryModelIsFailoverEligible(t *testing.T) {
	resolver := newGroupModelResolver(t, "cred-A", "cred-B")

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs("cred-A", "cred-B"))

	provider := &scriptedProvider{failN: 1}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		provider.mu.Lock()
		provider.apiKeys = append(provider.apiKeys, apiKey)
		provider.mu.Unlock()
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()
	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, []string{"m1"}, bus, "test-req", false, engine, "conv-group", false, nil)

	// Row 9 (LOG-ONLY budget): the reported budget is the FLATTENED
	// count − 1 (2 members ⇒ 1), not the raw count − 1 (1 − 1 = 0).
	if got := modelBudget(coord, "m1"); got != 1 {
		t.Fatalf("modelBudget = %d, want 1 (flat members−1; the raw read reported 0 and the log line lied)", got)
	}

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatalf("no winner: the group-primary model was NOT failover-eligible (the 429 was terminal)")
	}

	// Row 1 + 2: exactly ONE credential failover (budget = members−1).
	failoverRows := 0
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			failoverRows++
		}
	}
	if failoverRows != 1 {
		t.Fatalf("credFailover rows = %d, want 1 (a two-member group has a rotation budget of members−1)", failoverRows)
	}
	if got := coord.failoverAttempts; got != 1 {
		t.Errorf("failoverAttempts = %d, want 1", got)
	}
	if provider.callCount() != 2 {
		t.Errorf("provider calls = %d, want 2 (attempt + 1 rotation)", provider.callCount())
	}
	// The rotation visited a DIFFERENT credential (the group's second
	// member) — not the same account twice.
	keys := provider.apiKeys
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Errorf("served api keys = %v, want two distinct group members", keys)
	}
}

// TestA2_Gate_CredBelongsAcceptsGroupMember (row 3): a reselect of a
// GROUP MEMBER must pass the credBelongs membership scan — it used to
// be refused as a "wrong-model spawn" because the raw list held the
// group id, not the member id.
func TestA2_Gate_CredBelongsAcceptsGroupMember(t *testing.T) {
	resolver := newGroupModelResolver(t, "cred-A", "cred-B")
	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver

	got, ok := resolveSpecificInternalCredential(cfg, "m1", "cred-B")
	if !ok {
		t.Fatalf("resolveSpecificInternalCredential(cred-B) refused — a GROUP MEMBER must belong to the model")
	}
	if got.CredentialID != "cred-B" {
		t.Fatalf("resolved credential = %q, want cred-B", got.CredentialID)
	}
	if got.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai", got.Provider)
	}

	// A credential that is in NEITHER the group nor the raw list is
	// still refused (the guard keeps its force).
	if _, ok := resolveSpecificInternalCredential(cfg, "m1", "cred-X"); ok {
		t.Fatalf("resolveSpecificInternalCredential(cred-X) accepted — a foreign credential must still be refused")
	}
}

// TestA2_Gate_ReorderRunsForGroupPrimaryModel (row 4): the
// skip-cache reorder must RUN for a group-primary model (raw len 1 ⇒
// the "< 2" gate switched the reorder off) and the group members must
// be reorder candidates.
func TestA2_Gate_ReorderRunsForGroupPrimaryModel(t *testing.T) {
	resolver := newGroupModelResolver(t, "cred-A", "cred-B")
	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	cfg.UpstreamSkipCache = skipcache.New()

	cur := models.ResolvedCredential{CredentialID: "cred-A", Provider: "openai", InternalModel: "m1"}
	cfg.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-A", "m1", 10*time.Minute)

	got := preferUnmarkedUpstreamCredential(cfg, nil, "m1", cur)
	if got.CredentialID != "cred-B" {
		t.Fatalf("reordered credential = %q, want cred-B (the group's second member must be a reorder candidate)", got.CredentialID)
	}
}

// TestA2_Gate_MiniMaxSplitGateSeesFlatPrimary (row 5): the
// MiniMax reasoning-split gate reads the FLATTENED primary. Before
// the fix the raw PrimaryCredentialID() was EMPTY for a group-primary
// model, so the gate answered "not MiniMax" and the split was
// silently skipped.
func TestA2_Gate_MiniMaxSplitGateSeesFlatPrimary(t *testing.T) {
	mock := &mockModelsConfig{}
	mock.AddCredential(models.CredentialConfig{ID: "mm-cred", Provider: "minimax", APIKey: "k", BaseURL: "http://mm"})
	mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "upstream-m1",
		Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
	})
	resolver := &groupAwareResolver{
		mockModelsConfig: mock,
		groups:           map[string][]string{"g1": {"mm-cred"}},
	}
	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver

	if !raceIntProviderIsMiniMax(cfg, "m1") {
		t.Fatal("raceIntProviderIsMiniMax = false — the gate must see the FLAT primary (the group's first member)")
	}

	// Control: a group whose member is NOT minimax answers false.
	mock2 := &mockModelsConfig{}
	mock2.AddCredential(models.CredentialConfig{ID: "oai-cred", Provider: "openai", APIKey: "k", BaseURL: "http://oai"})
	mock2.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "upstream-m1",
		Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
	})
	resolver2 := &groupAwareResolver{
		mockModelsConfig: mock2,
		groups:           map[string][]string{"g1": {"oai-cred"}},
	}
	cfg2 := newTestConfigSnapshot("m1")
	cfg2.ModelsConfig = resolver2
	if raceIntProviderIsMiniMax(cfg2, "m1") {
		t.Fatal("raceIntProviderIsMiniMax = true for a non-minimax group member — false positive")
	}
}

// TestA2_Gate_RotationGatesReadFlatCount (rows 6 + 7): both
// rotation gates must read the FLATTENED credential count, so a
// group-primary model is NOT switched off.
//
// The behavioural half: the count expression both sites use answers
// "> 1" for a group-primary model with two members. The structural
// half (a source pin): both sites MUST call the flat accessor — a
// silent regression to the raw read would otherwise keep the
// behaviour green for plain models only.
func TestA2_Gate_RotationGatesReadFlatCount(t *testing.T) {
	resolver := newGroupModelResolver(t, "cred-A", "cred-B")
	mc := resolver.GetModel("m1")
	if mc == nil {
		t.Fatal("model m1 not found in the resolver")
	}
	if got := flatCredentialCount(resolver, mc); got != 2 {
		t.Fatalf("flatCredentialCount = %d, want 2 (the group's two members)", got)
	}
	if flatCredentialCount(resolver, mc) <= 1 {
		t.Fatal("the rotation gate would be CLOSED for a group-primary model (flat count <= 1)")
	}

	// Control: a plain single-credential model keeps the gate closed.
	plain := &models.ModelConfig{ID: "m2", Credentials: models.TestRefs("cred-A")}
	if got := flatCredentialCount(resolver, plain); got != 1 {
		t.Fatalf("flatCredentialCount(plain) = %d, want 1", got)
	}

	for _, tc := range []struct{ file, needle string }{
		{"internal_handler.go", "flatCredentialCount(h.resolver, h.config) <= 1"},
		{"handler_anthropic.go", "flatCredentialCount(arc.conf.ModelsConfig, modelConfig) > 1"},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(string(src), tc.needle) {
			t.Errorf("%s does not read the FLATTENED credential count (%q missing) — the rotation gate must not read the raw list", tc.file, tc.needle)
		}
	}

	// The raw reads must be GONE from both files.
	for _, tc := range []struct{ file, forbidden string }{
		{"internal_handler.go", "len(h.config.Credentials) <= 1"},
		{"handler_anthropic.go", "len(modelConfig.Credentials) > 1"},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if strings.Contains(string(src), tc.forbidden) {
			t.Errorf("%s still reads the RAW credential list (%q) — a group-primary model would be switched off", tc.file, tc.forbidden)
		}
	}
}

// TestA2_Gate_FlatAccessorIsInterfaceOnly: the proxy reaches the
// flattening seam through models.ModelsConfigInterface only — no
// pkg/store/database import (the §g import guard).
func TestA2_Gate_FlatAccessorIsInterfaceOnly(t *testing.T) {
	for _, file := range []string{"race_executor.go", "race_coordinator.go", "internal_handler.go", "handler_anthropic.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := string(src)
		if idx := strings.Index(body, "llm-supervisor-proxy/pkg/store/database"); idx != -1 {
			// A COMMENT may mention the guard; an IMPORT may not.
			lineStart := strings.LastIndex(body[:idx], "\n")
			line := body[lineStart+1 : idx]
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s imports pkg/store/database — the §g import guard forbids it in the selection packages", file)
			}
		}
	}
}
