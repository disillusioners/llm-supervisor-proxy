package models

// Test fixture helpers for the Phase 1 multi-credential sweep.
//
// House pattern: `pkg/proxy/test_helpers.go` and
// `pkg/proxy/handler_helpers_test.go` carry package-level test
// helpers alongside the test files in the same package. `pkg/models`
// follows the same convention here (Round-2 reviewer punch-list #13:
//
// "route all test-file CredentialID: literal substitutions through
// a TestRefs fixture factory — a small test helper that constructs
// []models.CredentialRef slices for tests").
//
// The factory minimizes diff noise across the 122+ sweep sites by
// expressing the new credential-list shape in one place; tests that
// only need a single credential write `TestRefs("cred-X")` (weight=1,
// position=0 — the legacy single-credential view) and tests that
// exercise weighted or multi-credential paths pass full
// `models.CredentialRef` literals to `TestRefsWeighted(...)`.
//
// NOTE: this file is a non-test file (no `_test.go` suffix) on
// purpose — these helpers are imported by tests in other packages
// (`pkg/proxy`, `pkg/store/database`, `test/...`) and so must be
// visible to those packages. Go's test framework would otherwise
// refuse to link `Test*`-prefixed helpers that don't accept
// `*testing.T`, and `_test.go` files are package-private.

// TestRefs builds a single-ref-per-id []CredentialRef slice (weight=1,
// position=slice-index). Empty/zero ids produce an empty slice
// (NOT nil — matches the migration's NOT NULL DEFAULT '[]' invariant
// and `parseCredentialsJSON` round-trip semantics). Used by the
// mechanical sweep that replaces `CredentialID: "x"` literals with
// `Credentials: TestRefs("x")` across the test corpus.
//
// Example:
//
//	ModelConfig{Internal: true, Credentials: TestRefs("cred-A"), InternalModel: "..."}
func TestRefs(ids ...string) []CredentialRef {
	if len(ids) == 0 {
		return []CredentialRef{}
	}
	refs := make([]CredentialRef, len(ids))
	for i, id := range ids {
		refs[i] = CredentialRef{CredentialID: id, Weight: 1, Position: i}
	}
	return refs
}

// TestRefsWeighted builds a []CredentialRef slice from full
// CredentialRef literals. Position is the slice index (0-based).
// Used by tests that exercise the multi-credential / weighted
// validation matrix (Task 6).
//
// Example:
//
//	ModelConfig{
//	    Internal: true,
//	    Credentials: TestRefsWeighted(
//	        CredentialRef{CredentialID: "cred-A", Weight: 2, Position: 0},
//	        CredentialRef{CredentialID: "cred-B", Weight: 1, Position: 1},
//	    ),
//	}
func TestRefsWeighted(refs ...CredentialRef) []CredentialRef {
	if len(refs) == 0 {
		return []CredentialRef{}
	}
	out := make([]CredentialRef, len(refs))
	for i, r := range refs {
		// Always re-stamp Position to the slice index — callers may
		// pass literal positions for readability, but the engine and
		// the validation matrix both rely on slice-index ordering.
		r.Position = i
		if r.Weight <= 0 {
			// Defensive: a test helper should not silently let
			// through values that the validation layer rejects.
			r.Weight = 1
		}
		out[i] = r
	}
	return out
}
