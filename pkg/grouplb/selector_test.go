package grouplb

import (
	"testing"
	"time"
)

func TestSelector_PrefixSumIntervals(t *testing.T) {
	s := newWeightedSelector([]MemberRef{
		{ModelID: "m-a", Weight: 3},
		{ModelID: "m-b", Weight: 1},
	})
	if s == nil {
		t.Fatal("selector nil for valid refs")
	}
	if s.totalWeight != 4 {
		t.Fatalf("totalWeight = %v, want 4", s.totalWeight)
	}
	// prefixSum[i] = sum(weights[0..i])
	if s.prefixSum[0] != 3 || s.prefixSum[1] != 4 {
		t.Fatalf("prefixSum = %v, want [3 4]", s.prefixSum)
	}
	// Interval mapping: r∈[0,3) → m-a; r∈[3,4) → m-b (O(log k) pick).
	cases := []struct {
		r    float64
		want string
	}{
		{0, "m-a"}, {2.99, "m-a"},
		{3, "m-b"}, {3.999, "m-b"},
	}
	for _, c := range cases {
		if got := s.pick(c.r); got != c.want {
			t.Errorf("pick(%v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestSelector_NilOnEmptyOrNonPositiveWeight(t *testing.T) {
	if newWeightedSelector(nil) != nil {
		t.Error("empty refs must yield nil selector")
	}
	if newWeightedSelector([]MemberRef{}) != nil {
		t.Error("zero-length refs must yield nil selector")
	}
	if newWeightedSelector([]MemberRef{{ModelID: "m", Weight: 0}}) != nil {
		t.Error("weight=0 must yield nil selector")
	}
	if newWeightedSelector([]MemberRef{{ModelID: "m", Weight: -1}}) != nil {
		t.Error("weight<0 must yield nil selector")
	}
	if newWeightedSelector([]MemberRef{{ModelID: "a", Weight: 1}, {ModelID: "b", Weight: -0.5}}) != nil {
		t.Error("any non-positive weight must yield nil selector (belt-and-braces)")
	}
}

func TestSelector_FloatWeights(t *testing.T) {
	s := newWeightedSelector([]MemberRef{
		{ModelID: "m-a", Weight: 0.5},
		{ModelID: "m-b", Weight: 1.5},
	})
	if s == nil {
		t.Fatal("float weights must build a selector")
	}
	if s.totalWeight != 2.0 {
		t.Fatalf("totalWeight = %v, want 2.0", s.totalWeight)
	}
	if got := s.pick(0.49); got != "m-a" {
		t.Errorf("pick(0.49) = %q, want m-a", got)
	}
	if got := s.pick(0.5); got != "m-b" {
		t.Errorf("pick(0.5) = %q, want m-b", got)
	}
}

func TestSelector_SingleMemberFastPath(t *testing.T) {
	// The E-3 engine fast path is exercised end-to-end in
	// engine_test.go; here pin the selector primitive: a single-ref
	// selector maps the whole interval to that member.
	s := newWeightedSelector([]MemberRef{{ModelID: "solo", Weight: 7}})
	for _, r := range []float64{0, 3.5, 6.999} {
		if got := s.pick(r); got != "solo" {
			t.Errorf("pick(%v) = %q, want solo", r, got)
		}
	}
}

func TestSelector_WeightDistribution(t *testing.T) {
	// Statistical pin (seeded engine → deterministic RNG): 3:1 refs
	// over N picks distribute ~3:1 within tolerance.
	e := NewEngine(time.Hour, time.Hour, 99, time.Minute)
	defer e.Stop()
	st := newGroupState("dist", 99)
	st.members = []MemberRef{{ModelID: "m-a", Weight: 3}, {ModelID: "m-b", Weight: 1}}
	st.selector = newWeightedSelector(st.members)
	counts := map[string]int{}
	now := time.Now()
	for i := 0; i < 4000; i++ {
		m := st.pickHealthyLocked(now)
		counts[m]++
	}
	if counts["m-a"] < 2600 || counts["m-a"] > 3400 {
		t.Errorf("3:1 weight distribution off: %+v", counts)
	}
}
