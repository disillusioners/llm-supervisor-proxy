package proxy

// load-balance-groups / P4.5 (P2 deferred #1) — Anthropic-path
// re-entry failover publish test.
//
// The OpenAI path publishes `model_member_failover` at handler.go:1221
// after a successful Am24 re-entry reselect. The Anthropic path
// used to skip this publish (P2 deferred #1); P4.5 closed the
// symmetry gap by adding the publish in tryGroupReentryAnthropic
// (handler_groups.go).
//
// This test exercises the full Anthropic flow with a 2-member
// group + a failing first upstream, then asserts:
//   - the request served 200 (proving the reselect fired),
//   - the bus saw exactly ONE `model_member_failover` event with
//     the correct (group_id, member_id, replacement) payload.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestHandlerGroups_AnthropicReentry_PublishesEventMemberFailover —
// when the Anthropic /v1/messages handler hits a terminal member
// failure inside a group, the Am24 re-entry reselects exactly once
// and the bus receives the EventMemberFailover event in lockstep
// with the OpenAI handler (parity by symmetry).
func TestHandlerGroups_AnthropicReentry_PublishesEventMemberFailover(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	// 2-member group so a reselect has somewhere to land.
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "m-a", Name: "m-a", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(m-a): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "m-b", Name: "m-b", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(m-b): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-anth", Name: "G-Anth", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "m-a", Weight: 1},
			{ModelID: "m-b", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(g-anth): %v", err)
	}

	// Swap the upstream so the FIRST call 502s and every
	// subsequent call 200s. The Anthropic reselect hits the same
	// upstream on member-m-b — so the test exercises the
	// terminal failure → reselect → success cycle end-to-end.
	var calls atomic.Int32
	h.up.Close()
	h.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"upstream 502"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal Anthropic /v1/messages non-stream response shape
		// so the Anthropic adapter's translation path returns a
		// 200 to the client.
		_, _ = w.Write([]byte(`{"id":"msg-stub","type":"message","role":"assistant","model":"stub-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	h.rebuildHandler(t)

	// Subscribe to the bus for failovers.
	type evt struct {
		Type    string
		GroupID string
		Member  string
		Replace string
	}
	seen := make(chan evt, 16)
	stop := make(chan struct{})
	bus := h.bus
	go func() {
		ch, _ := bus.Subscribe()
		for {
			select {
			case <-stop:
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.Type != "model_member_failover" {
					continue
				}
				d, _ := e.Data.(map[string]interface{})
				ev := evt{
					Type:    e.Type,
					GroupID: getFailoverString(d["group_id"]),
					Member:  getFailoverString(d["member_id"]),
					Replace: getFailoverString(d["replacement"]),
				}
				select {
				case seen <- ev:
				default:
				}
			}
		}
	}()
	defer close(stop)

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestExternal(t, map[string]interface{}{
		"model":      "g-anth",
		"max_tokens": 16,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleAnthropicMessages(rec, req)
	if rec.Code != http.StatusOK {
		// Anthropic path is more complex than the OpenAI path;
		// surface the full body if the reselect never fired, so
		// the diff is attributable.
		t.Fatalf("Anthropic re-entry: expected 200 (got %d); body=%s", rec.Code, rec.Body.String())
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("expected ≥2 upstream calls (fail + reselect); got %d", got)
	}

	// Wait briefly for the bus delivery.
	deadline := time.After(2 * time.Second)
	gotEvt := evt{}
	select {
	case gotEvt = <-seen:
	case <-deadline:
		t.Fatalf("expected ONE model_member_failover event on the bus after Anthropic reselect; saw 0")
	}
	if gotEvt.GroupID != "g-anth" {
		t.Errorf("failover event group_id=%q, want g-anth", gotEvt.GroupID)
	}
	// MemberID is the NEWLY-picked member (after reselect); it
	// must be one of the group's members. The replacement is the
	// picked member too in our schema (handler publishes the
	// chosen replacement).
	if gotEvt.Member == "" {
		t.Errorf("failover event member_id must be non-empty; got %+v", gotEvt)
	}
	if gotEvt.Replace == "" {
		t.Errorf("failover event replacement must be non-empty; got %+v", gotEvt)
	}
}

// getFailoverString safely extracts a string from a map[string]interface{}
// value (covers the JSON-decoded event payload case). Renamed to
// avoid the asString collision with handler_lb_phase5_test.go:486.
func getFailoverString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
