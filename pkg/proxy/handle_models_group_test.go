package proxy

// load-balance-groups / P2.5 (plan §3.4 + §9.4):
// /v1/models second-pass shadow-hide: a model whose ID is claimed
// by an ENABLED group must NOT appear in the OpenAI-compatible
// listing (while the claim is active). Groups themselves STAY
// listed. A disabled group's claim is inactive ⇒ the shadowed
// model reappears.
//
// Uses the same stubModelsConfig harness as the imggen filter
// tests (no real manager needed — the second-pass hide is a
// pure-list scan over the enabled listing; an interface-
// constrained scan with no per-row model-kind branching).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func TestHandleModels_ShadowHide_EnabledGroup(t *testing.T) {
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5"},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi"},
		// The group (kind=group, enabled, shadow=coding) — stays
		// listed; the claim HIDES the chat row "coding".
		{ID: "coding-group", Name: "Coding Group", Enabled: true,
			Kind: models.KindGroup, Internal: true,
			ExcludeFromUltimateSwitching: true,
			Members: []models.GroupMember{
				{ModelID: "coding", Weight: 2},
				{ModelID: "coding-alt", Weight: 1},
			},
			ShadowModelID: "coding",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object string                   `json:"object"`
		Data   []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make(map[string]bool)
	for _, m := range body.Data {
		if id, ok := m["id"].(string); ok {
			got[id] = true
		}
	}
	// (a) Group stays listed.
	if !got["coding-group"] {
		t.Errorf("the GROUP itself must be listed; got %v", got)
	}
	// (b) The claimed chat row is HIDDEN.
	if got["coding"] {
		t.Errorf("the claimed 'coding' row must be hidden while the group is enabled; got %v", got)
	}
	// (c) Other chat members remain listed.
	if !got["coding-alt"] {
		t.Errorf("non-claimed member 'coding-alt' must remain listed; got %v", got)
	}
}

func TestHandleModels_ShadowHide_DisabledGroupReveals(t *testing.T) {
	// The stub harness does NOT filter by Enabled (the listing
	// driver under test is the second-pass hide, not the
	// enabled-filter), so the disabled group row IS in the
	// enabled list passed to HandleModels. The shape of the
	// assertion is therefore: a disabled group contributes NO
	// claim (the second-pass scan requires `Enabled && IsGroup`),
	// so the shadowed model reappears.
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5"},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi"},
		// Disabled group: the second-pass scan requires
		// `Enabled && IsGroup` ⇒ no claim contributed ⇒ the
		// shadowed chat row is NOT hidden.
		{ID: "coding-group", Name: "Coding Group", Enabled: false,
			Kind: models.KindGroup, Internal: true,
			ExcludeFromUltimateSwitching: true,
			Members: []models.GroupMember{
				{ModelID: "coding", Weight: 2},
				{ModelID: "coding-alt", Weight: 1},
			},
			ShadowModelID: "coding",
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	got := make(map[string]bool)
	for _, m := range body.Data {
		if id, ok := m["id"].(string); ok {
			got[id] = true
		}
	}
	if !got["coding"] {
		t.Errorf("disabled group's claim must be inactive: the claimed chat row must reappear; got %v", got)
	}
}

func TestHandleModels_ShadowHide_TwoEnabledGroups_NoUniqueness(t *testing.T) {
	// G8 enforces uniqueness at WRITE time (validated), so two
	// enabled groups cannot both claim the same ID. The
	// second-pass hide is therefore guaranteed to see a unique
	// claim per ID; this test pins the absence of a uniqueness
	// panic on the listing path.
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5"},
		{ID: "coding-group", Name: "Coding Group", Enabled: true,
			Kind: models.KindGroup, Internal: true,
			ExcludeFromUltimateSwitching: true,
			Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
			ShadowModelID:                "coding",
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req) // must not panic on the listing path
}
