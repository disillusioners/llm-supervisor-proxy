package modelcache

// ─────────────────────────────────────────────────────────────────────────────
// scope_seam_test.go — the MINT-SCOPE test seam (wi-4b8d21e7).
//
// The seam lives in a _test.go file on PURPOSE: it must NOT be part of
// the production binary. The production model-scope vocabulary is EMPTY
// (no observed model-scoped body — Phase A audit,
// .agents/shared/planning/quota-mint-scope/phase-a-audit.md), so the
// ScopeModel branch and the model-layer mark path would be unreachable
// by any test. The seam lets the model layer's machinery — the scope
// gate, the knob cap, the [1m,24h] clamp, the never-shorten rule — be
// exercised WITHOUT inventing a production classification.
//
// A _test.go file is compiled ONLY into the test binary: the shipped
// binary carries ZERO trace of the seam (and of the synthetic marker
// vocabulary it installs). Production code (scope.go / policy.go /
// reset.go / modelcache.go) never calls the seam — the production
// vocabulary is only ever READ.
//
// The injected marker is a TEST FIXTURE, never a production class: it
// is removed again by the returned restore func.
//
// NOT PARALLEL-SAFE: the seam mutates the package-level
// modelScopeMarkers slice WITHOUT a lock. Only tests that install the
// seam may run concurrently with each other — a test that reads the
// scope vocabulary while the seam is installed would observe the
// synthetic marker. The modelcache tests are sequential (no
// t.Parallel()), so the seam is safe there; a future parallel test
// must not read the vocabulary while the seam is installed.
// ─────────────────────────────────────────────────────────────────────────────

// InjectModelScopeMarkerForTest appends a model-scope marker to the
// package vocabulary and returns a restore func that removes it again.
// Nil-safe; an empty marker is ignored.
//
// TEST-ONLY (this file is _test.go): the seam never reaches the
// production binary.
func InjectModelScopeMarkerForTest(marker string) (restore func()) {
	if marker == "" {
		return func() {}
	}
	previous := modelScopeMarkers
	modelScopeMarkers = append(append([]string{}, previous...), marker)
	return func() { modelScopeMarkers = previous }
}
