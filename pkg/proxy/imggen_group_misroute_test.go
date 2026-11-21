package proxy

// imggen-groups commission / T1.8 + T1.8a — the /v1/models catalog
// exclusion for image-gen-lane GROUPS and the CHAT-side misroute arms
// in BOTH chat routes.
//
// The code strings are per route family (decisions.md D-15):
//   - the CHAT family (both /v1/chat/completions and /v1/messages)
//     says "image_gen_misroute";
//   - the image-gen lane's own envelope says "image_misroute"
//     (pkg/imggen — the packages do not import each other, the import
//     guard forbids it).
// Two distinct machine codes, never one shared string.
//
// Every misroute answer is a 400 with the code string and ZERO
// upstream hits. No smoke expectation is amended to 502 anywhere.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// newImageGenGroupStub builds a models stub carrying an image-gen
// member + an image-gen-lane GROUP (the lane seam answers the group's
// lane).
func newImageGenGroupStub(t *testing.T) *groupLaneStubModels {
	t.Helper()
	stub := &groupLaneStubModels{ModelsConfig: &models.ModelsConfig{}, lanes: map[string]models.GroupLane{}}
	credCfg := models.CredentialConfig{ID: "minimax-cred", Provider: "minimax", APIKey: "k"}
	if err := stub.AddCredential(credCfg); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	cred := []models.CredentialRef{{CredentialID: "minimax-cred", Weight: 1, Position: 0}}
	if err := stub.AddModel(models.ModelConfig{
		ID: "img-1", Name: "Image 1", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
		Credentials: cred,
	}); err != nil {
		t.Fatalf("AddModel image-gen member: %v", err)
	}
	if err := stub.AddModel(models.ModelConfig{
		ID: "img-group", Name: "Image Gen Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "img-1", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel image-gen group: %v", err)
	}
	stub.lanes["img-group"] = models.GroupLaneImageGen
	return stub
}

// listedModelIDs returns the ids in a /v1/models response.
func listedModelIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var parsed struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, string(body))
	}
	out := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if id, ok := m["id"].(string); ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// TestHandleModels_ExcludesImageGenGroup_EmptyIdSetDiff — the
// catalog gate (§3.5 / F-5): with image-gen groups configured, the
// /v1/models id set EQUALS the id set with those rows deleted — the
// DIFF is empty (the group id is absent AND no other id appears or
// disappears).
func TestHandleModels_ExcludesImageGenGroup_EmptyIdSetDiff(t *testing.T) {
	// The WITH-groups store: a chat model + an image-gen member + an
	// image-gen-lane group.
	withGroup := newImageGenGroupStub(t)
	if err := withGroup.AddModel(models.ModelConfig{
		ID: "chat-1", Name: "Chat 1", Enabled: true,
		Kind: models.KindChat, Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{{CredentialID: "minimax-cred", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel chat: %v", err)
	}

	// The WITHOUT-groups store: the same chat model (the image-gen
	// rows are deleted).
	withoutGroup := &groupLaneStubModels{ModelsConfig: &models.ModelsConfig{}, lanes: map[string]models.GroupLane{}}
	if err := withoutGroup.AddCredential(models.CredentialConfig{ID: "minimax-cred", Provider: "minimax", APIKey: "k"}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := withoutGroup.AddModel(models.ModelConfig{
		ID: "chat-1", Name: "Chat 1", Enabled: true,
		Kind: models.KindChat, Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{{CredentialID: "minimax-cred", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel chat: %v", err)
	}

	listIDs := func(stub models.ModelsConfigInterface) []string {
		h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, stub)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		h.HandleModels(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		return listedModelIDs(t, rec.Body.Bytes())
	}

	withIDs := listIDs(withGroup)
	withoutIDs := listIDs(withoutGroup)

	if strings.Contains(strings.Join(withIDs, ","), "img-group") {
		t.Errorf("the image-gen group leaked into /v1/models: %v", withIDs)
	}
	if len(withIDs) != len(withoutIDs) {
		t.Fatalf("the id-set diff is NOT empty: with=%v without=%v", withIDs, withoutIDs)
	}
	for i := range withIDs {
		if withIDs[i] != withoutIDs[i] {
			t.Fatalf("the id-set diff is NOT empty: with=%v without=%v", withIDs, withoutIDs)
		}
	}
}

// TestHandleChatCompletions_RejectsImageGenGroupMisroute — an
// image-gen-lane GROUP posted to /v1/chat/completions ⇒ 400 with the
// chat-family code image_gen_misroute and ZERO upstream hits (the guard
// fires BEFORE the group rewrite).
func TestHandleChatCompletions_RejectsImageGenGroupMisroute(t *testing.T) {
	stub := newImageGenGroupStub(t)

	var upstreamHits atomic.Int64
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	body := `{"model":"img-group","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Errorf("the upstream was called for an image-gen-group misroute; hit count = %d", upstreamHits.Load())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != "image_gen_misroute" {
		t.Errorf("error.code = %q, want %q", code, "image_gen_misroute")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "/v1/image_generation") {
		t.Errorf("error.message = %q, want it to mention /v1/image_generation", msg)
	}
}

// TestHandleChatCompletions_ImageGenStandalone_CodeString — the
// STANDALONE image-gen guard on the chat route keeps the same
// chat-family code string (the two lane strings are distinct:
// image_gen_misroute here, image_misroute on the image-gen lane).
func TestHandleChatCompletions_ImageGenStandalone_CodeString(t *testing.T) {
	stub := newImageGenGroupStub(t)

	var upstreamHits atomic.Int64
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	body := `{"model":"img-1","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Errorf("the upstream was called for an image-gen misroute; hit count = %d", upstreamHits.Load())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, _ := parsed["error"].(map[string]interface{})
	code, _ := errObj["code"].(string)
	if code != imageGenMisrouteCode {
		t.Errorf("error.code = %q, want %q", code, imageGenMisrouteCode)
	}
}

// TestAnthropicBoundary_RejectsImageGenStandaloneMisroute — the
// DOCUMENTED GAP on the Anthropic route is CLOSED: a standalone
// image-gen model posted to /v1/messages ⇒ 400 with the Anthropic
// envelope family and the chat-family code image_gen_misroute, and
// ZERO upstream hits.
func TestAnthropicBoundary_RejectsImageGenStandaloneMisroute(t *testing.T) {
	stub := newImageGenGroupStub(t)

	var upstreamHits atomic.Int64
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"x"}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, anthropicDecisionRequest(t, "img-1"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Errorf("the upstream was called for an image-gen misroute on /v1/messages; hit count = %d", upstreamHits.Load())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	if parsed["type"] != "error" {
		t.Errorf("root 'type' = %v, want \"error\" (Anthropic envelope family)", parsed["type"])
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != imageGenMisrouteCode {
		t.Errorf("error.code = %q, want %q", code, imageGenMisrouteCode)
	}
	innerType, _ := errObj["type"].(string)
	if innerType != "invalid_request_error" {
		t.Errorf("error.type = %q, want \"invalid_request_error\" (route-local Anthropic envelope family)", innerType)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "/v1/image_generation") {
		t.Errorf("error.message = %q, want it to mention /v1/image_generation", msg)
	}
}

// TestAnthropicBoundary_RejectsImageGenGroupMisroute — an image-gen
// lane GROUP posted to /v1/messages ⇒ 400 with the chat-family code
// image_gen_misroute and ZERO upstream hits.
func TestAnthropicBoundary_RejectsImageGenGroupMisroute(t *testing.T) {
	stub := newImageGenGroupStub(t)

	var upstreamHits atomic.Int64
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"x"}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, anthropicDecisionRequest(t, "img-group"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Errorf("the upstream was called for an image-gen-group misroute on /v1/messages; hit count = %d", upstreamHits.Load())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != imageGenMisrouteCode {
		t.Errorf("error.code = %q, want %q", code, imageGenMisrouteCode)
	}
}

// TestMisrouteCodeStrings_AreDistinct — the two lane strings are
// DISTINCT machine codes (decisions.md D-15): the chat family says
// image_gen_misroute, the image-gen lane says image_misroute. A
// refactor that collapses them into one string would break the
// per-family contract.
func TestMisrouteCodeStrings_AreDistinct(t *testing.T) {
	const (
		chatFamilyCode = "image_gen_misroute"
		imggenLaneCode = "image_misroute"
	)
	if chatFamilyCode == imggenLaneCode {
		t.Fatal("the chat-family and image-gen-lane misroute codes must be distinct strings")
	}
	if imageGenMisrouteCode != chatFamilyCode {
		t.Errorf("the chat-family code = %q, want %q", imageGenMisrouteCode, chatFamilyCode)
	}
	if strings.Contains(chatFamilyCode, imggenLaneCode) && chatFamilyCode != imggenLaneCode+"_x" {
		// The image-gen lane's code must never be a PREFIX of the
		// chat-family code in the wrong direction: a client that
		// matches on the lane string must not also match the chat
		// family string.
		t.Errorf("the chat-family code %q contains the lane code %q — the two families must not overlap", chatFamilyCode, imggenLaneCode)
	}
}
