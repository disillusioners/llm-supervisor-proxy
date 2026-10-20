package imggen

// Import guards for pkg/imggen.
//
// The image-gen package sits OUTSIDE pkg/proxy so the chat-
// supervision exclusion boundary is mechanically enforceable. The
// source-level guard (TestPackage_ImportGuard_NoProxyImports) lives
// in handler_test.go; this file adds the two mirrors the decision
// lane already has and pkg/imggen was missing:
//
//   - the source-level guard against pkg/decision (the PARENT → CHILD
//     inversion: pkg/imggen is the template pkg/decision was built
//     from, so the dependency may only ever point that way),
//   - the TRANSITIVE-CLOSURE guard: a banned package can also sneak
//     in through a dependency's dependency, which the source-level
//     grep cannot see.
//
// Both are NEW tests in a NEW file — no existing imggen test is
// touched.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPackage_ImportGuard_NoDecisionImports — pkg/imggen must never
// import pkg/decision (the parent → child inversion: the decision
// adapters were built FROM the image-gen template).
func TestPackage_ImportGuard_NoDecisionImports(t *testing.T) {
	const banned = `github\.com/disillusioners/llm-supervisor-proxy/pkg/decision(/|")`
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
			t.Errorf("%s:%d references the pkg/decision import path — forbidden by the pkg/imggen import guard (parent → child inversion)", e.Name(), line)
		}
	}
}

// TestPackage_ImportGuard_TransitiveClosure — the banned packages
// must not be reachable through a dependency's dependency either
// (the source-level grep cannot see that).
func TestPackage_ImportGuard_TransitiveClosure(t *testing.T) {
	const projectPrefix = "github.com/disillusioners/llm-supervisor-proxy/pkg/"
	const bannedProxy = projectPrefix + "proxy"
	const bannedDecision = projectPrefix + "decision"

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Join(cwd, "..", "..")

	cmd := exec.Command("go", "list", "-deps", "./pkg/imggen/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if dep == "" {
			continue
		}
		if strings.HasPrefix(dep, bannedProxy+"/") || dep == bannedProxy {
			t.Errorf("transitive dep %q is under pkg/proxy — forbidden by the pkg/imggen transitive-closure guard", dep)
		}
		if strings.HasPrefix(dep, bannedDecision+"/") || dep == bannedDecision {
			t.Errorf("transitive dep %q is under pkg/decision — forbidden by the pkg/imggen transitive-closure guard", dep)
		}
	}
}
