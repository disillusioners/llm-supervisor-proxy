package proxy

// Model + group fallback observability events (wi-4d2c8e17 — LABELS;
// supersedes the wi-7769a812 group-only vocabulary).
//
// Contract (this file is the regression guard):
//
//	MODEL layer — a CHAIN hop inside a member's pipeline emits
//	  model_failed / model_fallback_attempted / model_fallback_advanced
//	  / model_fallback_exhausted, naming MODELS only. It NEVER asserts
//	  group membership (no group_id / member_id): a chain target may be
//	  inactive / not group-exposed by design.
//
//	GROUP layer — the genuine member hop (handler re-entry) emits
//	  group_member_failed / group_fallback_attempted /
//	  group_fallback_advanced / group_fallback_exhausted, with the
//	  group vocabulary. Covered by the handler-level group tests.
//
// The coordinator-level subtests below drive model_* directly; the
// third subtest pins the publisher payload contract (exhausted omits
// to_model).

import (
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// eventsOfType filters a drained history slice by event type. Cross-type
// order comes from ONE drainAllEvents pass over the bus history (the
// package helper — handler_lb_phase5_test.go): Subscribe replays the
// full history in order, so the publish sequence within the advance
// site stays assertable from a post-hoc drain.
func eventsOfType(all []events.Event, eventType string) []events.Event {
	out := make([]events.Event, 0, len(all))
	for _, evt := range all {
		if evt.Type == eventType {
			out = append(out, evt)
		}
	}
	return out
}

// payloadMap type-asserts an event payload with a precise failure line.
func payloadMap(t *testing.T, evt events.Event) map[string]interface{} {
	t.Helper()
	data, ok := evt.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event %s payload not a map: %T", evt.Type, evt.Data)
	}
	return data
}

func TestCoordinator_ModelFallbackEvents(t *testing.T) {
	t.Run("chain advance publishes model_failed, attempted, advanced per hop", func(t *testing.T) {
		// Chain m1 → m2 → m3, all single-credential; the provider
		// quota-fails the first TWO calls (m1, m2) and m3 serves.
		coord, provider, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m1", credCount: 1}, {id: "m2", credCount: 1}, {id: "m3", credCount: 1}},
			2,
			"conv-model-fallback-advance",
		)
		// Group traffic (the picked member is m1) — model_* events do
		// not depend on this, but the production shape carries it.
		coord.groupPick = &groupPickInfo{GroupID: "g-adv", MemberID: "m1", Pool: models.PoolDefault}

		coord.Start()
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("expected a winner on m3")
		}
		if winner.modelID != "m3" {
			t.Errorf("winner model = %q, want m3", winner.modelID)
		}
		if got := provider.callCount(); got != 3 {
			t.Errorf("upstream calls = %d, want 3 (m1 fail + m2 fail + m3 serve)", got)
		}

		all := drainAllEvents(bus)
		modelFailed := eventsOfType(all, "model_failed")
		attempted := eventsOfType(all, "model_fallback_attempted")
		advanced := eventsOfType(all, "model_fallback_advanced")
		exhausted := eventsOfType(all, "model_fallback_exhausted")

		if len(modelFailed) != 2 {
			t.Fatalf("model_failed events = %d, want 2 (one per failed hop m1, m2)", len(modelFailed))
		}
		if len(attempted) != 2 {
			t.Fatalf("model_fallback_attempted events = %d, want 2 (m1→m2, m2→m3)", len(attempted))
		}
		if len(advanced) != 2 {
			t.Fatalf("model_fallback_advanced events = %d, want 2 (m1→m2, m2→m3)", len(advanced))
		}
		if len(exhausted) != 0 {
			t.Fatalf("model_fallback_exhausted events = %d, want 0 (the chain ended on a WINNER)", len(exhausted))
		}

		d := payloadMap(t, modelFailed[0])
		if got := asString(d["model_id"]); got != "m1" {
			t.Errorf("model_failed[0] model_id = %q, want m1", got)
		}
		if got := asString(d["from_model"]); got != "m1" {
			t.Errorf("model_failed[0] from_model = %q, want m1", got)
		}
		if _, present := d["group_id"]; present {
			t.Errorf("model_failed[0] carries group_id — model events must NOT assert group membership")
		}
		if _, present := d["member_id"]; present {
			t.Errorf("model_failed[0] carries member_id — model events must NOT assert group membership")
		}
		if got := asString(d["reason"]); !strings.HasPrefix(got, "reason=upstream_4xx") {
			t.Errorf("model_failed[0] reason = %q, want the classified failureReason line", got)
		}
		if got := asString(d["id"]); got != "layered-test-req" {
			t.Errorf("model_failed[0] id = %q, want layered-test-req", got)
		}

		for i, want := range [][2]string{{"m1", "m2"}, {"m2", "m3"}} {
			ad := payloadMap(t, attempted[i])
			av := payloadMap(t, advanced[i])
			for _, d := range []map[string]interface{}{ad, av} {
				if got := asString(d["from_model"]); got != want[0] {
					t.Errorf("advance event[%d] from_model = %q, want %s", i, got, want[0])
				}
				if got := asString(d["to_model"]); got != want[1] {
					t.Errorf("advance event[%d] to_model = %q, want %s", i, got, want[1])
				}
				if got := asString(d["model_id"]); got != want[0] {
					t.Errorf("advance event[%d] model_id = %q, want %s (model_id tracks the FAILED model)", i, got, want[0])
				}
				if _, present := d["group_id"]; present {
					t.Errorf("advance event[%d] carries group_id — model events must NOT assert group membership", i)
				}
			}
		}

		// fix round #1 / review #5 — chain hops NEVER emit group_* events
		// (membership is asserted only on the genuine member hop).
		for _, groupType := range []string{"group_member_failed", "group_fallback_attempted", "group_fallback_advanced", "group_fallback_exhausted"} {
			if got := len(eventsOfType(all, groupType)); got != 0 {
				t.Errorf("%s events = %d, want 0 on chain hops (group events belong to the handler member-hop sites)", groupType, got)
			}
		}

		// Publish order: model_failed precedes attempted precedes advanced.
		firstFailed, firstAttempted, firstAdvanced := -1, -1, -1
		for i, evt := range all {
			switch evt.Type {
			case "model_failed":
				if firstFailed < 0 {
					firstFailed = i
				}
			case "model_fallback_attempted":
				if firstAttempted < 0 {
					firstAttempted = i
				}
			case "model_fallback_advanced":
				if firstAdvanced < 0 {
					firstAdvanced = i
				}
			}
		}
		if !(firstFailed < firstAttempted && firstAttempted < firstAdvanced) {
			t.Errorf("event order = failed@%d attempted@%d advanced@%d — want failed, then attempted, then advanced", firstFailed, firstAttempted, firstAdvanced)
		}
	})

	t.Run("chain end publishes model_failed and model_fallback_exhausted terminally", func(t *testing.T) {
		// Every model fails: m1 and m2 advance, the chain-end member's
		// failure has no advance target — the exhaust arm publishes
		// model_failed (m3) + model_fallback_exhausted (to_model
		// ABSENT), then the race resolves all-failed.
		coord, provider, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m1", credCount: 1}, {id: "m2", credCount: 1}, {id: "m3", credCount: 1}},
			99,
			"conv-model-fallback-chainend",
		)
		coord.groupPick = &groupPickInfo{GroupID: "g-end", MemberID: "m1", Pool: models.PoolDefault}

		coord.Start()
		if winner := coord.WaitForWinner(); winner != nil {
			t.Fatalf("expected NO winner (every member failed), got model %q", winner.modelID)
		}
		if got := provider.callCount(); got != 3 {
			t.Errorf("upstream calls = %d, want 3 (exactly one model-row per chain entry — chain end schedules NO spawn)", got)
		}

		all := drainAllEvents(bus)
		modelFailed := eventsOfType(all, "model_failed")
		attempted := eventsOfType(all, "model_fallback_attempted")
		advanced := eventsOfType(all, "model_fallback_advanced")
		exhausted := eventsOfType(all, "model_fallback_exhausted")

		if len(modelFailed) != 3 {
			t.Fatalf("model_failed events = %d, want 3 (m1, m2, AND the chain-end m3 — the exhaust arm is live)", len(modelFailed))
		}
		if len(attempted) != 2 || len(advanced) != 2 {
			t.Fatalf("advance events = attempted:%d advanced:%d, want 2/2 (m1→m2, m2→m3 only)", len(attempted), len(advanced))
		}
		if len(exhausted) != 1 {
			t.Fatalf("model_fallback_exhausted events = %d, want exactly 1 (one-shot latch)", len(exhausted))
		}

		if got := asString(payloadMap(t, modelFailed[2])["model_id"]); got != "m3" {
			t.Errorf("model_failed[2] model_id = %q, want m3 (the chain-end model)", got)
		}

		d := payloadMap(t, exhausted[0])
		if got := asString(d["model_id"]); got != "m3" {
			t.Errorf("exhausted model_id = %q, want m3", got)
		}
		if got := asString(d["from_model"]); got != "m3" {
			t.Errorf("exhausted from_model = %q, want m3", got)
		}
		if _, present := d["to_model"]; present {
			t.Errorf("exhausted payload carries to_model = %v — want the key ABSENT (no advance target)", d["to_model"])
		}
		if got := asString(d["reason"]); !strings.HasPrefix(got, "reason=upstream_4xx") {
			t.Errorf("exhausted reason = %q, want the classified failureReason line", got)
		}
	})

	t.Run("exhausted publish contract omits to_model", func(t *testing.T) {
		coord, _, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m-last", credCount: 1}},
			0,
			"conv-model-fallback-contract",
		)
		coord.groupPick = &groupPickInfo{GroupID: "g-contract", MemberID: "m-last", Pool: models.PoolDefault}

		reason := `reason=upstream_4xx status=402 detail="quota-scripted: Token Plan usage limit"`
		coord.publishModelFallback("model_failed", "m-last", "m-last", reason)
		coord.publishModelFallback("model_fallback_exhausted", "m-last", "", reason)

		all := drainAllEvents(bus)
		modelFailed := eventsOfType(all, "model_failed")
		exhausted := eventsOfType(all, "model_fallback_exhausted")
		if len(modelFailed) != 1 || len(exhausted) != 1 {
			t.Fatalf("published events = model_failed:%d exhausted:%d, want 1/1", len(modelFailed), len(exhausted))
		}

		d := payloadMap(t, exhausted[0])
		if got := asString(d["model_id"]); got != "m-last" {
			t.Errorf("exhausted model_id = %q, want m-last", got)
		}
		if got := asString(d["from_model"]); got != "m-last" {
			t.Errorf("exhausted from_model = %q, want m-last", got)
		}
		if _, present := d["to_model"]; present {
			t.Errorf("exhausted payload carries to_model = %v — want the key ABSENT", d["to_model"])
		}
		if got := asString(d["reason"]); got != reason {
			t.Errorf("exhausted reason = %q, want %q", got, reason)
		}
		if got := asString(d["id"]); got != "layered-test-req" {
			t.Errorf("exhausted id = %q, want layered-test-req", got)
		}
		fd := payloadMap(t, modelFailed[0])
		if got := asString(fd["to_model"]); got != "m-last" {
			t.Errorf("model_failed to_model = %q, want m-last (present, unlike exhausted)", got)
		}
	})
}
