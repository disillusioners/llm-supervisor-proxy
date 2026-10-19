package proxy

// group-backup-members / D5 — the FAILOVER event's `pool` field.
//
// Why the pool rides on the failover event: the re-entry path
// (tryGroupReentry) calls ExcludeGroupMember FIRST, and that call
// creates the conversation's binding — so the rebuild that follows
// (RewriteGroupEntries) returns MemberNewlyBound=false. The
// model_member_selected emission is gated on NewlyBound
// (race_executor.go), so it never fires on the failover path and
// its `pool` field cannot carry the signal. The failover event
// (model_member_failover) is the only event the re-entry emits, so
// the pool rides there.
//
// Contract (M3): the field is emitted ONLY for a BACKUP-pool serve
// ("backup"); a DEFAULT-pool serve leaves the field ABSENT — a
// "default" value is never emitted.
//
// Both legs drive the real failover path: the group's member 5xx's
// at the upstream, the Am24 re-entry fires exactly once, and the bus
// is inspected for the emitted payload. The stub fails the FIRST
// call and serves every later one — deterministic regardless of
// which member the weighted sticky pick lands on (the same idiom the
// Anthropic failover test uses).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// failoverStub is the shared stub state: the call counter and the
// wire model of the attempt it failed (so a test can assert the
// re-entry did NOT re-serve the failed member).
type failoverStub struct {
	calls  atomic.Int32
	failed atomic.Value // string — the wire model of the failed attempt
}

// newFailoverStubUpstream returns a stub OpenAI upstream that 502s
// the FIRST call and 200s every subsequent one, recording the failed
// attempt's wire model in the stub state.
func newFailoverStubUpstream(st *failoverStub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		if st.calls.Add(1) == 1 {
			st.failed.Store(model)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub fail"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody(model, "hi")))
	}
}

// seedFailoverPoolGroup seeds a group with the given members
// (member id + backup flag) plus their chat rows. The rows carry
// InternalModel == the row ID so the upstream wire model is
// addressable per member.
func seedFailoverPoolGroup(t *testing.T, h *groupHarness, groupID string, members []models.GroupMember) {
	t.Helper()
	for _, m := range members {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: m.ModelID, Name: m.ModelID, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: m.ModelID,
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ModelID, err)
		}
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: groupID, Name: groupID, Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      members,
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", groupID, err)
	}
}

// subscribeFailover subscribes to the bus BEFORE the request is
// issued and returns the channel of failover payload maps (the
// subscription idiom used by the Anthropic failover test — no
// reliance on the bus's history replay).
func subscribeFailover(t *testing.T, h *groupHarness) (chan map[string]interface{}, func()) {
	t.Helper()
	seen := make(chan map[string]interface{}, 16)
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
				if e.Type != grouplb.EventMemberFailover {
					continue
				}
				d, _ := e.Data.(map[string]interface{})
				select {
				case seen <- d:
				default:
				}
			}
		}
	}()
	return seen, func() { close(stop) }
}

// awaitFailover reads the first failover payload from the channel.
func awaitFailover(t *testing.T, seen chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case d := <-seen:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("expected a model_member_failover event on the bus after the re-entry; saw none")
		return nil
	}
}

// TestHandlerGroups_FailoverEvent_PoolBackupOnBackupServe — the
// group's ONLY default fails terminally; the Am24 re-entry cascades
// to the BACKUP member (C1) and the emitted model_member_failover
// event carries pool == "backup".
func TestHandlerGroups_FailoverEvent_PoolBackupOnBackupServe(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedFailoverPoolGroup(t, h, "g-fail-pool", []models.GroupMember{
		{ModelID: "f-default", Weight: 1},
		{ModelID: "f-backup", Weight: 1, BackupOnly: true},
	})

	// Swap the upstream so the FIRST attempt 502s (the group has a
	// single default ⇒ it is the first pick) and the backup's
	// attempt 200s.
	stub := &failoverStub{}
	h.up.Close()
	h.up = httptest.NewServer(newFailoverStubUpstream(stub))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-fail-pool",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("backup failover: status=%d body=%s (the re-entry did not reach the backup)", rec.Code, rec.Body.String())
	}

	data := awaitFailover(t, seen)
	poolVal, present := data["pool"]
	if !present {
		t.Fatal("D5: the failover event must carry pool=backup on a backup-pool serve (the key is absent)")
	}
	if got := getFailoverString(poolVal); got != models.PoolBackup {
		t.Fatalf("D5: failover event pool = %q, want %q", got, models.PoolBackup)
	}
	// The bounded/redacted rules: ids only, no free text.
	if got := getFailoverString(data["group_id"]); got != "g-fail-pool" {
		t.Errorf("failover event group_id = %q, want g-fail-pool", got)
	}
	if got := getFailoverString(data["member_id"]); got != "f-backup" {
		t.Errorf("failover event member_id = %q, want f-backup (the backup served)", got)
	}
}

// TestHandlerGroups_FailoverEvent_NoPoolKeyOnDefaultServe — the
// absent⇒default contract: when the re-entry serves the OTHER
// DEFAULT (no backup involved), the failover event carries NO `pool`
// key at all — a "default" value is never emitted.
func TestHandlerGroups_FailoverEvent_NoPoolKeyOnDefaultServe(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedFailoverPoolGroup(t, h, "g-fail-def", []models.GroupMember{
		{ModelID: "f-default", Weight: 1},
		{ModelID: "f-default-2", Weight: 1},
	})

	// Swap the upstream so the FIRST attempt 502s (whichever default
	// the sticky pick lands on) and the re-entry's attempt 200s.
	stub := &failoverStub{}
	h.up.Close()
	h.up = httptest.NewServer(newFailoverStubUpstream(stub))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-fail-def",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default failover: status=%d body=%s", rec.Code, rec.Body.String())
	}

	data := awaitFailover(t, seen)
	if _, present := data["pool"]; present {
		t.Fatalf("D5/M3: the failover event must carry NO pool key on a default-pool serve; got %v", data["pool"])
	}
	// The re-entry must not re-serve the member that just failed —
	// it lands on the OTHER default. (Which default failed is
	// weighted-random, so the stub records it.)
	failedModel, _ := stub.failed.Load().(string)
	served := getFailoverString(data["member_id"])
	if served == failedModel {
		t.Errorf("failover event member_id = %q — that is the member that just failed", served)
	}
	if served != "f-default" && served != "f-default-2" {
		t.Errorf("failover event member_id = %q, want one of the group's defaults", served)
	}
}
