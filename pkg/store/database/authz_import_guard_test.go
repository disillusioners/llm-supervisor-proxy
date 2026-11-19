package database

// The AUTH COMMISSION's import guard for the store package: the
// scope-enforcement seam crosses into pkg/store/database as
// models.ReachFilter (pkg/models), NOT as pkg/auth or pkg/authz.
//
// WHY: pkg/auth imports pkg/store/database (the token store), so an
// import back would be a CYCLE. The ReachFilter interface is the
// cycle-breaker — and the guard names the intent so a future edit cannot
// quietly re-introduce the cycle as a "convenient" direct import.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackage_ImportGuard_StoreDatabaseNoAuthImports(t *testing.T) {
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
			if !strings.Contains(trimmed, "llm-supervisor-proxy/pkg/auth") {
				continue
			}
			t.Errorf("%s imports %q — pkg/store/database must stay free of pkg/auth and pkg/authz (the models.ReachFilter seam is the cycle-breaker)", e.Name(), trimmed)
		}
	}
	if scanned == 0 {
		t.Fatal("guard scanned zero files — vacuous")
	}

	// NEGATIVE CONTROL — must FIRE.
	control := `"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"`
	if !strings.Contains(control, "llm-supervisor-proxy/pkg/auth") {
		t.Error("negative control did not fire — the guard misses a direct import")
	}
}

// guardDummy keeps filepath referenced (the walk above uses ReadDir).
var _ = filepath.Join
