package decision

// P1 import guard tests (Decision Models commission / §3.5 / §10
// P1.8). These are the ONLY handler_test.go content at P1 — full
// suite (29+ tests from §9.1) lands in P2.7.
//
// Two layers per the plan's 3-layer guard matrix:
//   - Layer 1 (Makefile): grep on `pkg/decision/` for the
//     banned import paths (Makefile test target above).
//   - Layer 2 (in-package): TestPackage_ImportGuard_NoProxyImports
//     mirrors pkg/imggen/handler_test.go:689-713 verbatim.
//   - Layer 3 (NEW transitive-closure test — improvement over
//     pkg/imggen): TestPackage_ImportGuard_TransitiveClosure runs
//     `go list -deps ./pkg/decision/...` and asserts none of the
//     deps are under `pkg/proxy` or `pkg/imggen` (the chat-imggen
//     interaction surface). This catches any transitive leak that
//     the grep gate would miss.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPackage_ImportGuard_NoProxyImports — direct-source guard.
// Mirrors pkg/imggen/handler_test.go:689-713. Fails the test if
// ANY .go file under pkg/decision/ directly references the
// pkg/proxy import path string.
func TestPackage_ImportGuard_NoProxyImports(t *testing.T) {
	const banned = `github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")`
	re, err := regexp.Compile(banned)
	if err != nil {
		t.Fatalf("bad guard regex: %v", err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if loc := re.FindIndex(src); loc != nil {
			line := 1 + bytes.Count(src[:loc[0]], []byte("\n"))
			t.Errorf("%s:%d references the pkg/proxy import path — forbidden by the pkg/decision import guard (Decision Models §3.5)", e.Name(), line)
		}
	}
}

// TestPackage_ImportGuard_NoImggenImports — direct-source guard
// for the sibling package. Decision's stricter import policy: not
// only must pkg/proxy stay out, pkg/imggen (the chat-imggen
// interaction surface) must also stay out. Decision does NOT
// share code with imggen at the import level — it mirrors
// behavior at the API surface only.
func TestPackage_ImportGuard_NoImggenImports(t *testing.T) {
	const banned = `github\.com/disillusioners/llm-supervisor-proxy/pkg/imggen(/|")`
	re, err := regexp.Compile(banned)
	if err != nil {
		t.Fatalf("bad guard regex: %v", err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if loc := re.FindIndex(src); loc != nil {
			line := 1 + bytes.Count(src[:loc[0]], []byte("\n"))
			t.Errorf("%s:%d references the pkg/imggen import path — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", e.Name(), line)
		}
	}
}

// TestPackage_ImportGuard_TransitiveClosure — TRANSITIVE closure
// guard (NEW per Decision Models commission / §3.5 / R1.5 / §10
// P1.8; an improvement over pkg/imggen which only guards direct
// imports). Runs `go list -deps ./pkg/decision/...` and asserts
// NONE of the deps are under pkg/proxy or pkg/imggen.
//
// This catches leaks through transitive imports — e.g. an
// accidental `pkg/foo` import that itself depends on pkg/proxy
// would silently leak the chat supervision surface into
// pkg/decision. The grep gate (Layer 1) catches direct text
// references but misses transitive Go-module deps.
func TestPackage_ImportGuard_TransitiveClosure(t *testing.T) {
	// Find the module root from the package directory (the test
	// runs in pkg/decision/ so module root is two levels up
	// from cwd).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Join(cwd, "..", "..")

	cmd := exec.Command("go", "list", "-deps", "./pkg/decision/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}

	// The package's own path is permitted; everything else under
	// pkg/proxy or pkg/imggen is forbidden.
	const projectPrefix = "github.com/disillusioners/llm-supervisor-proxy/pkg/"
	const bannedProxy = projectPrefix + "proxy"
	const bannedImggen = projectPrefix + "imggen"

	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		if dep == "" {
			continue
		}
		// Match the prefix + slash OR prefix + quote so we catch
		// "pkg/proxy/..." and "pkg/proxy" but not e.g.
		// "pkg/proxyheader" (different package).
		if strings.HasPrefix(dep, bannedProxy+"/") || dep == bannedProxy {
			t.Errorf("transitive dep %q is under pkg/proxy — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", dep)
		}
		if strings.HasPrefix(dep, bannedImggen+"/") || dep == bannedImggen {
			t.Errorf("transitive dep %q is under pkg/imggen — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", dep)
		}
	}
}
