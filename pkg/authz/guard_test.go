package authz

// Import guard for pkg/authz — the leaf-package invariant.
//
// pkg/authz is the ONE resolver/predicate every lane consults. It must stay a
// LEAF so a lane can import it without dragging a dependency graph behind it:
// it may import pkg/auth (plus stdlib) and NOTHING else from this repo. In
// particular it must never import pkg/proxy, pkg/decision,
// pkg/store/database or pkg/modelscache — those are the layers that call
// INTO it, and importing them would both cycle and re-introduce the
// site-local coupling this package exists to remove (ADR D7/D8).
//
// House precedent: TestPackage_ImportGuard_* (pkg/decision, pkg/imggen).

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runGoListDeps returns the transitive dependency list of pkg/authz.
func runGoListDeps(moduleRoot string) ([]string, error) {
	cmd := exec.Command("go", "list", "-deps", "./pkg/authz/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var deps []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			deps = append(deps, line)
		}
	}
	return deps, nil
}

// bannedAuthzImports is the closed set of repo packages pkg/authz must not
// reference. Built as one alternation so the same expression serves the real
// source scan and the negative control.
var bannedAuthzImports = regexp.MustCompile(
	`github\.com/disillusioners/llm-supervisor-proxy/pkg/(proxy|decision|modelscache|store/database)(/|")`,
)

// TestPackage_ImportGuard_AuthzStaysLeaf scans every non-test .go file in
// pkg/authz for a banned repo import.
func TestPackage_ImportGuard_AuthzStaysLeaf(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if loc := bannedAuthzImports.FindIndex(src); loc != nil {
			line := 1 + bytes.Count(src[:loc[0]], []byte("\n"))
			t.Errorf("%s:%d imports a banned package — pkg/authz must stay a leaf (pkg/auth + stdlib only)",
				e.Name(), line)
		}
	}
	if scanned == 0 {
		t.Fatal("guard scanned zero files — the production sources are missing, so this test is vacuous")
	}
}

// TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure proves the
// hard-to-see violation: a banned package reaching pkg/authz through a
// dependency's dependency.
//
// SCOPE NOTE (measured, not assumed): `go list -deps ./pkg/authz/...` also
// reports pkg/store/database and pkg/models — those arrive TRANSITIVELY
// through pkg/auth (pkg/auth/store.go imports pkg/store/database). That is a
// pre-existing property of pkg/auth, not a dependency this package creates,
// and it cannot be removed from here. The banned set below is therefore the
// packages pkg/authz could only reach by importing them itself: proxy,
// decision, modelscache. pkg/models is deliberately NOT in this list for the
// same reason — TestPackage_ImportGuard_AuthzImportsOnlyAuth asserts it
// directly at the source level, which is the enforceable form.
func TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Join(cwd, "..", "..")

	deps, err := runGoListDeps(moduleRoot)
	if err != nil {
		t.Skipf("go list unavailable in this environment (%v) — source-level guard still runs", err)
	}
	const prefix = "github.com/disillusioners/llm-supervisor-proxy/pkg/"
	for _, dep := range deps {
		switch {
		case dep == prefix+"proxy" || strings.HasPrefix(dep, prefix+"proxy/"):
			t.Errorf("transitive dep %q is under pkg/proxy — forbidden by the pkg/authz leaf guard", dep)
		case dep == prefix+"decision" || strings.HasPrefix(dep, prefix+"decision/"):
			t.Errorf("transitive dep %q is under pkg/decision — forbidden by the pkg/authz leaf guard", dep)
		case dep == prefix+"modelscache" || strings.HasPrefix(dep, prefix+"modelscache/"):
			t.Errorf("transitive dep %q is under pkg/modelscache — forbidden by the pkg/authz leaf guard", dep)
		}
	}
}

// TestPackage_ImportGuard_NegativeControlFires is the anti-vacuous control:
// the SAME expression the real guard uses must match a synthetic banned
// import. If a future edit loosens the regex (or the anchored group prefix)
// and the real scan silently stops matching, this test fails.
func TestPackage_ImportGuard_NegativeControlFires(t *testing.T) {
	controls := []string{
		"import \"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy\"",
		"import (\n\t\"github.com/disillusioners/llm-supervisor-proxy/pkg/decision\"\n)",
		"import \"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache\"",
		"import \"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database\"",
	}
	for _, c := range controls {
		if !bannedAuthzImports.MatchString(c) {
			t.Errorf("negative control did not fire — the guard regex misses a banned import: %q", c)
		}
	}

	// And the positive control: the ONE allowed repo import must NOT match.
	allowed := "import \"github.com/disillusioners/llm-supervisor-proxy/pkg/auth\""
	if bannedAuthzImports.MatchString(allowed) {
		t.Errorf("guard regex rejects the permitted pkg/auth import: %q", allowed)
	}
}

// TestPackage_ImportGuard_AuthzImportsOnlyAuth is the STRONG form of the leaf
// invariant: for every production file in pkg/authz, the set of REPO imports
// must be exactly {pkg/auth} — or empty. Anything else fails, including
// pkg/models (the seam points the other way: models declares ReachFilter,
// authz implements it) and pkg/store/database.
func TestPackage_ImportGuard_AuthzImportsOnlyAuth(t *testing.T) {
	const repoPrefix = "\"github.com/disillusioners/llm-supervisor-proxy/"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, repoPrefix) {
				continue
			}
			imported := strings.TrimSuffix(strings.TrimPrefix(trimmed, "\""), "\"")
			if imported != "github.com/disillusioners/llm-supervisor-proxy/pkg/auth" {
				t.Errorf("%s imports %q — pkg/authz may import pkg/auth and nothing else from this repo",
					e.Name(), imported)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("guard scanned zero files — vacuous")
	}
}
