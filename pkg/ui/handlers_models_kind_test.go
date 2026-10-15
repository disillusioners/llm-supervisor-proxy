package ui

// ImgGen Models commission / T1.2.2 + T1.7.3 — /fe/api/models
// ?kind= filter contract tests (BE-D4):
//   - absent: all models (back-compat — today's behavior)
//   - kind=image-gen: only IsImageGen() models
//   - kind=chat: only non-image-gen models
//   - unknown value: treat as chat (server-log warning per the
//     approver advisory)

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func newImgGenTestServer() *modelTestServer {
	ts := newModelTestServer()
	// Add a minimax credential so image-gen model validation
	// (the kind-rule provider check) passes.
	ts.mockModels.AddCredential(models.CredentialConfig{
		ID: "minimax-cred", Provider: "minimax", APIKey: "k",
	})
	return ts
}

func imgGenTestCreds() []models.CredentialRef {
	return []models.CredentialRef{{CredentialID: "minimax-cred", Weight: 1, Position: 0}}
}

func TestHandleModels_GET_KindFilter_AbsentReturnsAll(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})

	resp, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("absent ?kind= should return all models, got %d", len(got))
	}
}

func TestHandleModels_GET_KindFilter_ImageGen(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})

	u := server.URL + "/fe/api/models?kind=image-gen"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "img-1" {
		t.Errorf("kind=image-gen should return only image-gen models, got %+v", got)
	}
}

func TestHandleModels_GET_KindFilter_Chat(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})

	u := server.URL + "/fe/api/models?kind=chat"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "chat-1" {
		t.Errorf("kind=chat should return only non-image-gen models, got %+v", got)
	}
}

func TestHandleModels_GET_KindFilter_UnknownValue_Chat(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})

	u := server.URL + "/fe/api/models?kind=unknown-value"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "chat-1" {
		t.Errorf("unknown kind value should treat as chat, got %+v", got)
	}
}

func TestHandleModels_POST_RejectsImageGen_InvalidModel(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	// Image-gen without internal_model — should be 400.
	body := `{
		"id": "img-bad",
		"name": "Image Bad",
		"enabled": true,
		"kind": "image-gen",
		"internal": true,
		"credentials": [{"credential_id": "minimax-cred", "weight": 1, "position": 0}]
	}`
	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing internal_model)", resp.StatusCode)
	}
}

func TestHandleModels_POST_RejectsImageGen_NonMiniMaxProvider(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "img-openai",
		"name": "Image OpenAI",
		"enabled": true,
		"kind": "image-gen",
		"internal": true,
		"internal_model": "image-01",
		"credentials": [{"credential_id": "test-cred", "weight": 1, "position": 0}]
	}`
	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (non-minimax primary provider)", resp.StatusCode)
	}
}

func TestHandleModels_POST_AcceptsValidImageGen(t *testing.T) {
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "img-ok",
		"name": "Image OK",
		"enabled": true,
		"kind": "image-gen",
		"internal": true,
		"internal_model": "image-01",
		"credentials": [{"credential_id": "minimax-cred", "weight": 1, "position": 0}]
	}`
	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201 (valid image-gen model); body=%s", resp.StatusCode, readBody(resp))
	}
}

// readBody is a small helper for error messages.
func readBody(resp *http.Response) string {
	var sb strings.Builder
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

func TestHandleModels_GET_KindFilter_DefaultArm_UnknownKind_LogsWarning_ThreeKinds(t *testing.T) {
	// Decision Models commission / §9.2 (R1.2 / approver polish
	// item 4) — the default-arm warning wording must mention
	// THREE kinds (chat + image-gen + decision) now that
	// KindDecision exists. The unknown-kind branch still falls
	// back to a chat filter (back-compat with operators who
	// typo'd a URL).
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})
	ts.mockModels.AddModel(models.ModelConfig{ID: "dec-1", Name: "Decision 1", Enabled: true, Internal: true, Kind: models.KindDecision, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("test-cred"), ExcludeFromUltimateSwitching: true})

	u := server.URL + "/fe/api/models?kind=unknown-value"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Default-arm fallback: chat filter = !IsImageGen && !IsDecision.
	// chat-1 passes; img-1 + dec-1 are excluded.
	if len(got) != 1 || got[0].ID != "chat-1" {
		t.Errorf("unknown-kind fallback should treat as chat (excluding image-gen + decision); got %+v", got)
	}
}

func TestHandleModels_GET_KindFilter_ChatExcludesImageGenAndDecision(t *testing.T) {
	// Decision Models commission / §3.1 site 7 — the chat arm
	// was EXTENDED in P1.4 to `!m.IsImageGen() && !m.IsDecision()`.
	// Before the fix, decision models leaked into `?kind=chat`
	// for any non-UI direct API consumer (Review Round 1 finding 1).
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})
	ts.mockModels.AddModel(models.ModelConfig{ID: "dec-1", Name: "Decision 1", Enabled: true, Internal: true, Kind: models.KindDecision, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("test-cred"), ExcludeFromUltimateSwitching: true})

	u := server.URL + "/fe/api/models?kind=chat"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "chat-1" {
		t.Errorf("?kind=chat must return chat-1 only (no image-gen, no decision); got %+v", got)
	}
	for _, m := range got {
		if m.ID == "img-1" {
			t.Error("?kind=chat returned image-gen model (would leak to chat pickers)")
		}
		if m.ID == "dec-1" {
			t.Error("?kind=chat returned decision model (P1.4 fix regression — Review Round 1 finding 1 re-emerged)")
		}
	}
}

func TestHandleModels_GET_KindFilter_ImageGenExcludesDecision(t *testing.T) {
	// Decision Models / §9.2 — `?kind=image-gen` must return
	// ONLY image-gen models. Decision models on this arm would
	// leak into the image-gen picker.
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})
	ts.mockModels.AddModel(models.ModelConfig{ID: "dec-1", Name: "Decision 1", Enabled: true, Internal: true, Kind: models.KindDecision, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("test-cred"), ExcludeFromUltimateSwitching: true})

	u := server.URL + "/fe/api/models?kind=image-gen"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "img-1" {
		t.Errorf("?kind=image-gen must return image-gen models only; got %+v", got)
	}
}

func TestHandleModels_GET_KindFilter_Decision(t *testing.T) {
	// Decision Models / §3.1 site 7 — `?kind=decision` returns
	// ONLY decision models. P1.4 acceptance text.
	ts := newImgGenTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x", Credentials: models.TestRefs("test-cred")})
	ts.mockModels.AddModel(models.ModelConfig{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01", Credentials: imgGenTestCreds()})
	ts.mockModels.AddModel(models.ModelConfig{ID: "dec-1", Name: "Decision 1", Enabled: true, Internal: true, Kind: models.KindDecision, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("test-cred"), ExcludeFromUltimateSwitching: true})

	u := server.URL + "/fe/api/models?kind=decision"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "dec-1" {
		t.Errorf("?kind=decision must return decision models only; got %+v", got)
	}
}
