package proxy

// wi-mscttl-pins — model-layer skip-cache regression pins.
//
// Two contracts:
//
//  1. A MODEL-layer suppression is a synthetic, zero-cost failure. It
//     must NOT re-mark or extend the live model mark (the mark is
//     refreshed only by an observed model-attributable wall).
//  2. v194 doctrine: suppress an ATTEMPT, never a PICK or a CHAIN. A
//     marked model stays in the coordinator's model list (the pick is
//     untouched) and the request still ends with a winner (chain
//     served), not an abort.

import (
	"net/http"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
)

// TestModelSkipCache_SuppressedAttemptDoesNotExtendMark: a model mark
// captured before a suppression run must be byte-identical (within 1s)
// afterwards — the suppression neither extends nor clears it.
func TestModelSkipCache_SuppressedAttemptDoesNotExtendMark(t *testing.T) {
	up := skipcache.New()
	mc := modelcache.New()
	provider := &sepStatusProvider{statuses: map[string]int{"m1": http.StatusOK, "m2": http.StatusOK}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-nomark-model",
		func(cfg *ConfigSnapshot) {
			cfg.UpstreamSkipCache = up
			cfg.ModelSkipCache = mc
		})

	until := time.Now().Add(10 * time.Minute)
	mc.MarkUntil("m1", until)

	untilBefore, ok := mc.Until("m1")
	if !ok {
		t.Fatal("mark not present before run")
	}

	coord.Start()
	if winner := coord.WaitForWinner(); winner == nil {
		t.Fatal("no winner (m2 should serve)")
	}

	untilAfter, ok2 := mc.Until("m1")
	if !ok2 {
		t.Fatal("mark cleared by suppression run")
	}
	diff := untilAfter.Sub(untilBefore)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Fatalf("mark changed by suppression: before=%v after=%v diff=%v", untilBefore, untilAfter, diff)
	}
}

// TestModelSkipCache_MarkedModelStillPicked_ChainServes: v194 doctrine
// pin — with m1 marked, the coordinator's model list still contains m1
// (pick untouched) and the request still ends with a winner (chain
// served), not an abort.
func TestModelSkipCache_MarkedModelStillPicked_ChainServes(t *testing.T) {
	mc := modelcache.New()
	provider := &sepStatusProvider{statuses: map[string]int{"m1": http.StatusOK, "m2": http.StatusOK}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-marked-pick",
		func(cfg *ConfigSnapshot) {
			cfg.ModelSkipCache = mc
		})
	mc.Mark("m1", 10*time.Minute)

	found := false
	for _, m := range coord.models {
		if m == "m1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("coordinator model list does not contain m1: %v", coord.models)
	}

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("request aborted: no winner (chain must serve)")
	}
	if winner.modelID != "m2" {
		t.Fatalf("winner = %s, want m2 (chain served)", winner.modelID)
	}
}
