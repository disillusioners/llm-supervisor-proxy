package proxy

// THE AUTH COMMISSION's structural guards (phase2-plan.md §5.3, T2.13,
// T2.14, and the approver's "New Lane Contract" gate §4c).
//
// These are the DURABLE half of the enforcement: a documented grep is
// only as good as the last person who read the doc, while a test runs on
// every `go test`. Every guard carries a NEGATIVE CONTROL that must
// FIRE — a guard whose regex silently stopped matching is worse than no
// guard at all.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// authzRepoRoot walks up to the module root (the go.mod directory).
func authzRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate the module root (go.mod)")
	return ""
}

// authzNonTestGoFiles returns every non-test .go file under the given
// repo-relative directories.
func authzNonTestGoFiles(t *testing.T, root string, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(path string, e os.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if e.IsDir() {
				return nil
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", d, err)
		}
	}
	return out
}

// stripGoComments removes // line comments and /* */ block comments so a
// prose mention of a banned symbol (the rationale comments name them)
// is not mistaken for executable code.
func stripGoComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// authzRelPath renders a path relative to the repo root (slash form). It
// is a distinct name because pkg/proxy's credential-groups pin already
// declares relPath.
func authzRelPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// ─────────────────────────────────────────────────────────────────────────────
// T2.13 / arch PD-12 — no per-axis len(AllowedModels) guard outside auth.
// ─────────────────────────────────────────────────────────────────────────────

// perAxisLenGuard matches the WILDCARD-GUARD SHAPE the commission
// removed: a length COMPARISON over the model grant axis (`>0`, `==0`,
// …). It is the shape that silently changes from "is this token
// restricted?" to "does this token have a list?" the moment an empty
// list stops meaning wildcard.
//
// A bare len() used as a CAPACITY HINT (a map size, a slice copy) is
// NOT the guard shape and is deliberately not matched — such uses carry
// no authorization decision.
var perAxisLenGuard = regexp.MustCompile(`len\([^)]*AllowedModels\)\s*[<>=!]`)

// TestPackage_NoPerAxisLenGuardOutsideAuth pins the ONE-predicate rule
// (ADR D8): no lane may re-derive the wildcard state from a per-axis
// length test.
//
// SCOPE OF THE ALLOWANCE (stated, not hidden): `pkg/auth` owns the token
// type and its deprecated shim; `pkg/authz` is the ONE resolver, which
// legitimately SIZES its reach map from the grant slices (`len(...)`
// used as a capacity hint, never as a decision). Every OTHER non-test
// file must be free of the shape.
func TestPackage_NoPerAxisLenGuardOutsideAuth(t *testing.T) {
	root := authzRepoRoot(t)
	files := authzNonTestGoFiles(t, root, "pkg", "cmd")
	scanned := 0
	for _, path := range files {
		rel := authzRelPath(root, path)
		if strings.HasPrefix(rel, "pkg/auth/") {
			// pkg/auth owns the token type and the demoted helper (which
			// keeps its own unit test) — the ONLY legitimate home of the
			// model-axis length test.
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		scanned++
		code := stripGoComments(string(src))
		if loc := perAxisLenGuard.FindStringIndex(code); loc != nil {
			line := 1 + strings.Count(code[:loc[0]], "\n")
			t.Errorf("%s:%d contains a per-axis len(AllowedModels) guard — the wildcard state is computed by authz.Resolve ONLY (ADR D8); this shape is a live leak the moment an empty list stops meaning wildcard", rel, line)
		}
	}
	if scanned < 20 {
		t.Fatalf("guard scanned only %d files — the walk is broken, so this pin is vacuous", scanned)
	}

	// NEGATIVE CONTROL — must FIRE on the removed shape.
	controls := []string{
		`if len(t.AllowedModels) > 0 {`,
		`} else if len(authToken.AllowedModels) > 0 {`,
		`restricted := len(tok.AllowedModels) == 0`,
	}
	for _, c := range controls {
		if !perAxisLenGuard.MatchString(c) {
			t.Errorf("negative control did not fire — the guard regex misses the removed shape: %q", c)
		}
	}
	// POSITIVE CONTROL: the legitimate non-guard shapes must NOT match —
	// a capacity hint and a slice copy carry no authorization decision.
	for _, allowed := range []string{
		`ids := make(map[string]struct{}, len(t.AllowedModels)+len(t.AllowedGroups))`,
		`cp.AllowedModels = make([]string, len(t.AllowedModels))`,
	} {
		if perAxisLenGuard.MatchString(allowed) {
			t.Errorf("control broken: %q is a capacity hint / slice copy, not a wildcard guard, and must not be flagged", allowed)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// T2.2 / T2.13 — no IsModelAllowed outside pkg/auth.
// ─────────────────────────────────────────────────────────────────────────────

// TestPackage_ImportGuard_NoIsModelAllowedInLanes pins the collapsed
// entry gate (ADR D8): the four lanes were four hand-written variants of
// one decision, and they were NOT equivalent (the Anthropic arm had no
// length pre-guard). After the scope work every lane calls
// authz.Resolve's predicate; the demoted helper stays in pkg/auth for its
// own unit test alone.
func TestPackage_ImportGuard_NoIsModelAllowedInLanes(t *testing.T) {
	root := authzRepoRoot(t)
	files := authzNonTestGoFiles(t, root, "pkg", "cmd")
	scanned := 0
	for _, path := range files {
		rel := authzRelPath(root, path)
		if strings.HasPrefix(rel, "pkg/auth/") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		scanned++
		if strings.Contains(stripGoComments(string(src)), "IsModelAllowed(") {
			t.Errorf("%s calls IsModelAllowed( — the four entry sites collapse into authz.Resolve's predicate; no non-test file outside pkg/auth may call the model-axis helper", rel)
		}
	}
	if scanned < 20 {
		t.Fatalf("guard scanned only %d files — vacuous", scanned)
	}

	// NEGATIVE CONTROL — the removed call shape must be detected.
	if !strings.Contains("if !authToken.IsModelAllowed(modelID) {", "IsModelAllowed(") {
		t.Error("negative control did not fire — the guard misses the removed call shape")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// T2.14 — no residual ultimate-escalation hook.
// ─────────────────────────────────────────────────────────────────────────────

// ultimateEscalation matches the retired subsystem's identifiers.
var ultimateEscalation = regexp.MustCompile(`UltimateModel|ExcludeFromUltimateSwitching`)

// TestPackage_NoUltimateEscalation is the T2.14 audit as a TEST.
//
// The whole security argument of the commission assumes no other path can
// escalate a model choice around Reach(T). `pkg/ultimatemodel` was
// retired with the external-upstream removal; this pin keeps it retired:
// no non-test file under pkg/ or cmd/ may name the identifiers in
// EXECUTABLE code.
//
// COMMENTS ARE EXEMPT BY DESIGN: several files explain WHY the path is
// absent (the rationale is valuable history). The pin strips comments
// before matching, so a comment cannot re-introduce a hook — and cannot
// trip the pin either.
func TestPackage_NoUltimateEscalation(t *testing.T) {
	root := authzRepoRoot(t)
	if entries, err := os.ReadDir(filepath.Join(root, "pkg")); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.Contains(strings.ToLower(e.Name()), "ultimatе") {
				t.Errorf("pkg/%s exists — the ultimate-model subsystem must stay retired", e.Name())
			}
		}
	}
	for _, dir := range []string{"pkg", "cmd"} {
		if entries, err := os.ReadDir(filepath.Join(root, dir)); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.EqualFold(e.Name(), "ultimatemodel") {
					t.Errorf("%s/%s exists — the ultimate-model subsystem must stay retired", dir, e.Name())
				}
			}
		}
	}

	files := authzNonTestGoFiles(t, root, "pkg", "cmd")
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if loc := ultimateEscalation.FindStringIndex(stripGoComments(string(src))); loc != nil {
			rel := authzRelPath(root, path)
			line := 1 + strings.Count(stripGoComments(string(src))[:loc[0]], "\n")
			t.Errorf("%s:%d names an ultimate-escalation identifier in executable code — a live escalation hook would invalidate the commission's core premise", rel, line)
		}
	}

	// NEGATIVE CONTROL — must FIRE on the executable shape.
	for _, c := range []string{
		"m.UltimateModel",
		"if row.ExcludeFromUltimateSwitching {",
	} {
		if !ultimateEscalation.MatchString(c) {
			t.Errorf("negative control did not fire — the guard misses %q", c)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// T2.14 / §4c — no scope predicate inside the hot loop.
// ─────────────────────────────────────────────────────────────────────────────

// TestPackage_ImportGuard_NoScopeInHotLoop pins the structural property
// that makes the exclusion FREE on the request path: the scope is
// resolved ONCE at the entry and applied to the CANDIDATE LISTS, so the
// race coordinator / race executor (the per-attempt hot loop) never
// consult it. An attempt-time check would leak the existence of
// out-of-scope models (a 403-vs-404 oracle) and force the token into the
// inner loops (ADR D9).
func TestPackage_ImportGuard_NoScopeInHotLoop(t *testing.T) {
	root := authzRepoRoot(t)
	hotLoopFiles := []string{
		"pkg/proxy/race_coordinator.go",
		"pkg/proxy/race_executor.go",
	}
	for _, rel := range hotLoopFiles {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		code := stripGoComments(string(src))
		if strings.Contains(code, "pkg/authz") {
			t.Errorf("%s imports pkg/authz — the hot loop must never consult the scope (the list is pre-filtered at construction)", rel)
		}
		if strings.Contains(code, ".Allows(") {
			t.Errorf("%s calls .Allows( — no attempt-time scope check is permitted (ADR D9: exclusion is by REMOVAL from the candidate list)", rel)
		}
	}

	// The decision lane's attempt loop: the exclusion happens on the
	// member COPY before buildGroupPolicy, never inside the loop.
	groupSrc, err := os.ReadFile(filepath.Join(root, "pkg/decision", "group.go"))
	if err != nil {
		t.Fatalf("read pkg/decision/group.go: %v", err)
	}
	groupCode := stripGoComments(string(groupSrc))
	// The attempt loop opens with the ordered-candidate walk; locate it
	// and assert no .Allows( call appears after it.
	loopIdx := strings.Index(groupCode, "for idx := range order {")
	if loopIdx < 0 {
		t.Fatal("could not locate the decision attempt loop (`for idx := range order {`) — the pin would be vacuous")
	}
	if tail := groupCode[loopIdx:]; strings.Contains(tail, ".Allows(") {
		t.Error("pkg/decision/group.go calls .Allows( inside the attempt loop — the member filter must run on the COPY before buildGroupPolicy, never at attempt time")
	}

	// The image-gen lane's attempt loop: the SAME law. The member filter
	// runs on the COPY before buildGroupPolicy, never inside the loop
	// (imggen-groups commission / phase2-plan §4.3 / S5).
	imggenGroupSrc, err := os.ReadFile(filepath.Join(root, "pkg", "imggen", "group.go"))
	if err != nil {
		t.Fatalf("read pkg/imggen/group.go: %v", err)
	}
	imggenGroupCode := stripGoComments(string(imggenGroupSrc))
	imggenLoopIdx := strings.Index(imggenGroupCode, "for idx := range order {")
	if imggenLoopIdx < 0 {
		t.Fatal("could not locate the imggen attempt loop (`for idx := range order {`) — the pin would be vacuous")
	}
	if tail := imggenGroupCode[imggenLoopIdx:]; strings.Contains(tail, ".Allows(") {
		t.Error("pkg/imggen/group.go calls .Allows( inside the attempt loop — the member filter must run on the COPY before buildGroupPolicy, never at attempt time")
	}

	// NEGATIVE CONTROL — must FIRE on the banned shapes.
	if !strings.Contains("if !scope.Allows(id) {", ".Allows(") {
		t.Error("negative control did not fire — the guard misses an attempt-time scope check")
	}
	if !strings.Contains(`"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"`, "pkg/authz") {
		t.Error("negative control did not fire — the guard misses the authz import")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// §4c — the New Lane Contract: reach resolution ONLY via authz.Resolve.
// ─────────────────────────────────────────────────────────────────────────────

// TestLaneContract_ReachResolutionOnlyViaAuthz pins the one-resolver
// rule structurally (ADR D7/D8, §7.1/§7.2 of the ADR):
//
//	(i)  no lane reads a raw grant AXIS (the token's AllowedModels /
//	     AllowedGroups fields) — the wildcard rule is NOT re-derivable;
//	(ii) the lanes' reach decisions go through authz.Resolve.
//
// "The lanes" = pkg/proxy, pkg/decision, pkg/imggen (the four entry
// sites' packages). pkg/auth owns the type; pkg/authz is the resolver.
func TestLaneContract_ReachResolutionOnlyViaAuthz(t *testing.T) {
	root := authzRepoRoot(t)
	lanes := []string{"pkg/proxy", "pkg/decision", "pkg/imggen"}

	axisRead := regexp.MustCompile(`\.(AllowedModels|AllowedGroups)\b`)
	totalResolveCalls := 0

	for _, lane := range lanes {
		files := authzNonTestGoFiles(t, root, lane)
		if len(files) == 0 {
			t.Fatalf("%s: no non-test files — the lane walk is broken", lane)
		}
		for _, path := range files {
			rel := authzRelPath(root, path)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			code := stripGoComments(string(src))
			if loc := axisRead.FindStringIndex(code); loc != nil {
				line := 1 + strings.Count(code[:loc[0]], "\n")
				t.Errorf("%s:%d reads a raw grant axis — a lane may not re-derive the wildcard/reach rule; resolve the scope through authz.Resolve and ask Scope.Allows", rel, line)
			}
			totalResolveCalls += strings.Count(code, "authz.Resolve(")
		}
	}

	// The lanes must actually USE the resolver (a guard that passes
	// because nobody resolves anything would be vacuous).
	//
	// W-1 (leader ruling): the floor is the FINAL post-fold count — 5
	// verified census sites (pkg/proxy/handler.go, pkg/proxy/
	// handler_anthropic.go, pkg/decision/group.go, pkg/decision/handler.go,
	// pkg/imggen/handler.go) + EXACTLY ONE added by the imggen group arm
	// = 6. The old `>= 3` passed with 5 and would not notice the arm's
	// removal; `>= 6` is non-vacuous.
	if totalResolveCalls < 6 {
		t.Fatalf("found only %d authz.Resolve( call(s) across the lanes — expected >= 6 (5 census sites + the imggen group arm)", totalResolveCalls)
	}

	// W-1 (leader ruling): STRING-PIN the imggen group arm's OWN resolve.
	// The entry gate (pkg/imggen/handler.go) also calls authz.Resolve for
	// PLAIN models — so a bare string pin of `authz.Resolve(` would be
	// satisfied by the entry gate even with the arm deleted. Bound the
	// search to the ARM block (its opening marker) so a removed arm fails
	// the guard BY NAME, not by a count drift.
	imggenGroupRaw, err := os.ReadFile(filepath.Join(root, "pkg", "imggen", "group.go"))
	if err != nil {
		t.Fatalf("read pkg/imggen/group.go: %v", err)
	}
	const armOpenMarker = "ARM#1 (bypass-closed): the GROUP itself must be in reach."
	armIdx := strings.Index(string(imggenGroupRaw), armOpenMarker)
	if armIdx < 0 {
		t.Fatalf("pkg/imggen/group.go is missing the group-arm scope marker %q — the arm's own resolve cannot be pinned (W-1)", armOpenMarker)
	}
	armBlock := string(imggenGroupRaw)[armIdx:]
	if !strings.Contains(armBlock, "scope := authz.Resolve(authToken, h.membersOf())") {
		t.Error("pkg/imggen/group.go ARM#1 does not resolve its OWN scope — the entry gate's resolve cannot satisfy the group arm's bypass-closed obligation (W-1)")
	}

	// NEGATIVE CONTROL — must FIRE on a raw-axis read.
	for _, c := range []string{
		"if len(token.AllowedModels) > 0 {",
		"groups := t.AllowedGroups",
	} {
		if !axisRead.MatchString(c) {
			t.Errorf("negative control did not fire — the guard misses a raw grant-axis read: %q", c)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// T2.11 — the scoped seam must be forwarded by the production decorator.
// ─────────────────────────────────────────────────────────────────────────────

// TestGroupRewriterScoped_ProductionDecoratorForward pins the
// modelscache decorator's scoped forwards at COMPILE time through the
// package's own assertion, and at RUNTIME through a structural check that
// the assertion exists (a removed assertion would silently re-open the
// decorator-forward trap in production while the raw-manager tests stay
// green).
func TestGroupRewriterScoped_ProductionDecoratorForward(t *testing.T) {
	root := authzRepoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "pkg", "modelscache", "models.go"))
	if err != nil {
		t.Fatalf("read pkg/modelscache/models.go: %v", err)
	}
	code := string(src)
	for _, want := range []string{
		`_ models.GroupRewriterScoped`,
		`_ models.GroupMembershipReader`,
		`= (*CachedModelsConfig)(nil)`,
		`func (c *CachedModelsConfig) RewriteGroupEntriesScoped(`,
		`func (c *CachedModelsConfig) ExcludeGroupMemberScoped(`,
		`func (c *CachedModelsConfig) GroupMembers(`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("pkg/modelscache/models.go is missing %q — production runs through the decorator (CACHE_LAYER_ENABLED defaults ON), so a missing forward is a SILENT no-op in production (the decorator-forward trap)", want)
		}
	}
}
