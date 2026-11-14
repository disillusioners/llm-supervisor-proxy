package proxy

// Group-fallback observability events (wi-7769a812 part 1 — ultimate
// retirement replacement; part 2 flipped the exhaust arm). The four
// event types are published from the race-path group advance site
// (race_coordinator.go Case-1) and are gated on group traffic ONLY
// (c.groupPick != nil — plain model chains keep the race_*
// vocabulary untouched).
//
// Coverage contract (this file is the ONLY regression guard):
//
//	(a) a group ADVANCE publishes group_member_failed +
//	    group_fallback_attempted + group_fallback_advanced, once per
//	    failed member, carrying the payload keys group_id / member_id /
//	    from_member / to_member / reason (+ id) — end-to-end through a
//	    live coordinator;
//	(b) a chain END resolves terminally AND — since part 2's
//	    exhaustive fallback flipped the admission gate
//	    (chainEndPending) — PUBLISHES member_failed for the chain-end
//	    member + group_fallback_exhausted, with the publish contract
//	    (member naming, group_id, classified reason, and the ABSENT
//	    to_member key) asserted end-to-end;
//	(c) the exhausted payload contract is pinned at the publisher
//	    seam as well (the to_member-absent contrast with
//	    member_failed).
//
// Part-2 reachability (supersedes the part-1 note): the exhaust arm
// is LIVE. A chain-end failure that no spawn branch consumes reaches
// Case-1 once via the chainEndPending gate disjunct, publishes the
// member_failed + exhausted pair, and latches (chainEndPublished) so
// the terminal resolves exactly once.

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

func TestCoordinator_GroupFallbackEvents(t *testing.T) {
	t.Run("advance publishes member_failed, attempted, advanced per failed member", func(t *testing.T) {
		// Chain m1 → m2 → m3, all single-credential; the provider
		// quota-fails the first TWO calls (m1, m2) and m3 serves.
		coord, provider, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m1", credCount: 1}, {id: "m2", credCount: 1}, {id: "m3", credCount: 1}},
			2,
			"conv-group-fallback-advance",
		)
		// Group-traffic gate: stamp the pick BEFORE Start — the same
		// shape the handler's applyGroupRewrite produces for a group
		// whose member list the coordinator received as modelIDs.
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
		memberFailed := eventsOfType(all, "group_member_failed")
		attempted := eventsOfType(all, "group_fallback_attempted")
		advanced := eventsOfType(all, "group_fallback_advanced")
		exhausted := eventsOfType(all, "group_fallback_exhausted")

		if len(memberFailed) != 2 {
			t.Fatalf("group_member_failed events = %d, want 2 (one per failed member m1, m2)", len(memberFailed))
		}
		if len(attempted) != 2 {
			t.Fatalf("group_fallback_attempted events = %d, want 2 (m1→m2, m2→m3)", len(attempted))
		}
		if len(advanced) != 2 {
			t.Fatalf("group_fallback_advanced events = %d, want 2 (m1→m2, m2→m3)", len(advanced))
		}
		if len(exhausted) != 0 {
			t.Fatalf("group_fallback_exhausted events = %d, want 0 (the chain ended on a WINNER, not exhaustion)", len(exhausted))
		}

		// First member failure (m1): member_failed names the member in
		// member_id / from_member — and to_member repeats it (the site
		// passes the same model id for both — the FAILED member is the
		// "from" of every event at this site).
		d := payloadMap(t, memberFailed[0])
		if got := asString(d["group_id"]); got != "g-adv" {
			t.Errorf("member_failed[0] group_id = %q, want g-adv", got)
		}
		if got := asString(d["member_id"]); got != "m1" {
			t.Errorf("member_failed[0] member_id = %q, want m1", got)
		}
		if got := asString(d["from_member"]); got != "m1" {
			t.Errorf("member_failed[0] from_member = %q, want m1", got)
		}
		if got := asString(d["to_member"]); got != "m1" {
			t.Errorf("member_failed[0] to_member = %q, want m1 (site passes the failed member as both from and to)", got)
		}
		if got := asString(d["reason"]); !strings.HasPrefix(got, "reason=upstream_4xx") {
			t.Errorf("member_failed[0] reason = %q, want the classified failureReason line (reason=upstream_4xx … — 402 is upstream_4xx, and the raw upstream body must stay out of the payload shape)", got)
		}
		if got := asString(d["id"]); got != "layered-test-req" {
			t.Errorf("member_failed[0] id = %q, want layered-test-req (the coordinator request id rides the payload)", got)
		}
		if got := asString(payloadMap(t, memberFailed[1])["member_id"]); got != "m2" {
			t.Errorf("member_failed[1] member_id = %q, want m2", got)
		}

		// The advance pair events: from = the failed member, to = the
		// member the chain advanced TO. attempted and advanced fire
		// TOGETHER per advance with identical routing payloads.
		for i, want := range [][2]string{{"m1", "m2"}, {"m2", "m3"}} {
			ad := payloadMap(t, attempted[i])
			av := payloadMap(t, advanced[i])
			for _, d := range []map[string]interface{}{ad, av} {
				if got := asString(d["from_member"]); got != want[0] {
					t.Errorf("advance event[%d] from_member = %q, want %s", i, got, want[0])
				}
				if got := asString(d["to_member"]); got != want[1] {
					t.Errorf("advance event[%d] to_member = %q, want %s", i, got, want[1])
				}
				if got := asString(d["group_id"]); got != "g-adv" {
					t.Errorf("advance event[%d] group_id = %q, want g-adv", i, got)
				}
				if got := asString(d["member_id"]); got != want[0] {
					t.Errorf("advance event[%d] member_id = %q, want %s (member_id tracks the FAILED member)", i, got, want[0])
				}
				if got := asString(d["reason"]); !strings.HasPrefix(got, "reason=upstream_4xx") {
					t.Errorf("advance event[%d] reason = %q, want the classified failureReason line (reason=upstream_4xx …)", i, got)
				}
			}
		}

		// Publish order within the site: member_failed precedes the
		// attempted/advanced pair for the same member.
		firstFailed, firstAttempted, firstAdvanced := -1, -1, -1
		for i, evt := range all {
			switch evt.Type {
			case "group_member_failed":
				if firstFailed < 0 {
					firstFailed = i
				}
			case "group_fallback_attempted":
				if firstAttempted < 0 {
					firstAttempted = i
				}
			case "group_fallback_advanced":
				if firstAdvanced < 0 {
					firstAdvanced = i
				}
			}
		}
		if !(firstFailed < firstAttempted && firstAttempted < firstAdvanced) {
			t.Errorf("event order = member_failed@%d, attempted@%d, advanced@%d — want member_failed first, then attempted, then advanced", firstFailed, firstAttempted, firstAdvanced)
		}
	})

	t.Run("chain end publishes member_failed and exhausted terminally", func(t *testing.T) {
		// Every member fails: m1 and m2 advance (their failures pass
		// the hoisted admission gate), but the chain-end member's
		// failure has no advance target — wi-7769a812 part 2 flipped
		// the gate (chainEndPending), so that failure now reaches the
		// Case-1 site ONCE and publishes member_failed (m3) +
		// group_fallback_exhausted (payload contract: to_member
		// ABSENT), then the race resolves all-failed.
		coord, provider, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m1", credCount: 1}, {id: "m2", credCount: 1}, {id: "m3", credCount: 1}},
			99, // every upstream call fails
			"conv-group-fallback-chainend",
		)
		coord.groupPick = &groupPickInfo{GroupID: "g-end", MemberID: "m1", Pool: models.PoolDefault}

		coord.Start()
		if winner := coord.WaitForWinner(); winner != nil {
			t.Fatalf("expected NO winner (every member failed), got model %q", winner.modelID)
		}
		if got := provider.callCount(); got != 3 {
			t.Errorf("upstream calls = %d, want 3 (exactly one model-row per member — chain end schedules NO spawn)", got)
		}

		all := drainAllEvents(bus)
		memberFailed := eventsOfType(all, "group_member_failed")
		attempted := eventsOfType(all, "group_fallback_attempted")
		advanced := eventsOfType(all, "group_fallback_advanced")
		exhausted := eventsOfType(all, "group_fallback_exhausted")

		if len(memberFailed) != 3 {
			t.Fatalf("group_member_failed events = %d, want 3 (m1, m2, AND the chain-end member m3 — part 2 made the exhaust arm live)", len(memberFailed))
		}
		if len(attempted) != 2 || len(advanced) != 2 {
			t.Fatalf("advance events = attempted:%d advanced:%d, want 2/2 (m1→m2, m2→m3 only — the chain-end member advances NOWHERE)", len(attempted), len(advanced))
		}
		if len(exhausted) != 1 {
			t.Fatalf("group_fallback_exhausted events = %d, want exactly 1 (the chainEndPending one-shot — a second publish would mean the latch leaked)", len(exhausted))
		}

		// The chain-end member_failed names m3.
		if got := asString(payloadMap(t, memberFailed[2])["member_id"]); got != "m3" {
			t.Errorf("member_failed[2] member_id = %q, want m3 (the chain-end member)", got)
		}

		// The exhausted event: names the failed chain-end member and
		// the group, carries the classified reason — and NO to_member
		// key (nothing to advance to; the key is omitted, not empty).
		d := payloadMap(t, exhausted[0])
		if got := asString(d["group_id"]); got != "g-end" {
			t.Errorf("exhausted group_id = %q, want g-end", got)
		}
		if got := asString(d["member_id"]); got != "m3" {
			t.Errorf("exhausted member_id = %q, want m3", got)
		}
		if got := asString(d["from_member"]); got != "m3" {
			t.Errorf("exhausted from_member = %q, want m3", got)
		}
		if _, present := d["to_member"]; present {
			t.Errorf("exhausted payload carries to_member = %v — want the key ABSENT (no advance target)", d["to_member"])
		}
		if got := asString(d["reason"]); !strings.HasPrefix(got, "reason=upstream_4xx") {
			t.Errorf("exhausted reason = %q, want the classified failureReason line (reason=upstream_4xx …)", got)
		}

		// The advance payloads still name the group and carry the
		// classified reason for the two real advances.
		for i, wantTo := range []string{"m2", "m3"} {
			ad := payloadMap(t, attempted[i])
			if got := asString(ad["group_id"]); got != "g-end" {
				t.Errorf("attempted[%d] group_id = %q, want g-end", i, got)
			}
			if got := asString(ad["to_member"]); got != wantTo {
				t.Errorf("attempted[%d] to_member = %q, want %s", i, got, wantTo)
			}
		}
	})

	t.Run("exhausted publish contract omits to_member", func(t *testing.T) {
		// Pins the exhausted payload shape at the publisher seam (the
		// end-to-end arm is part-2-live; see the file-header note).
		// publishGroupFallback is the single publish path for all four
		// event types — asserting it here covers the one payload
		// difference the live advance subtests cannot reach: the
		// exhausted event carries NO to_member key.
		coord, _, bus, _ := newLayeredChainEnv(
			t,
			[]layeredChainModel{{id: "m-last", credCount: 1}},
			0,
			"conv-group-fallback-contract",
		)
		coord.groupPick = &groupPickInfo{GroupID: "g-contract", MemberID: "m-last", Pool: models.PoolDefault}

		reason := `reason=upstream_4xx status=402 detail="quota-scripted: Token Plan usage limit"`
		coord.publishGroupFallback("group_member_failed", "m-last", "m-last", "", reason)
		coord.publishGroupFallback("group_fallback_exhausted", "m-last", "", "", reason)

		all := drainAllEvents(bus)
		memberFailed := eventsOfType(all, "group_member_failed")
		exhausted := eventsOfType(all, "group_fallback_exhausted")
		if len(memberFailed) != 1 || len(exhausted) != 1 {
			t.Fatalf("published events = member_failed:%d exhausted:%d, want 1/1", len(memberFailed), len(exhausted))
		}

		// exhausted: names the failed member and the group, carries the
		// classified reason verbatim — and NO to_member key (nothing to
		// advance to; the key is omitted, not empty).
		d := payloadMap(t, exhausted[0])
		if got := asString(d["group_id"]); got != "g-contract" {
			t.Errorf("exhausted group_id = %q, want g-contract", got)
		}
		if got := asString(d["member_id"]); got != "m-last" {
			t.Errorf("exhausted member_id = %q, want m-last", got)
		}
		if got := asString(d["from_member"]); got != "m-last" {
			t.Errorf("exhausted from_member = %q, want m-last", got)
		}
		if _, present := d["to_member"]; present {
			t.Errorf("exhausted payload carries to_member = %v — want the key ABSENT (no advance target)", d["to_member"])
		}
		if got := asString(d["reason"]); got != reason {
			t.Errorf("exhausted reason = %q, want the classified failureReason line passed through verbatim (%q)", got, reason)
		}
		if got := asString(d["id"]); got != "layered-test-req" {
			t.Errorf("exhausted id = %q, want layered-test-req", got)
		}
		// member_failed at the same seam keeps to_member (the failed
		// member itself) — the contrast IS the contract.
		fd := payloadMap(t, memberFailed[0])
		if got := asString(fd["to_member"]); got != "m-last" {
			t.Errorf("member_failed to_member = %q, want m-last (present, unlike exhausted)", got)
		}
	})
}
