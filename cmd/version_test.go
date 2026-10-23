package main

import (
	"os"
	"testing"
)

// TestVersionDefaultIsDev pins the no-ldflags default of the ldflags
// target var. A plain `go build` must yield "dev" — if anyone
// re-hardcodes a number into this var (the stale committed-VERSION
// pattern this feature replaced), the default changes and this test
// fails.
func TestVersionDefaultIsDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("default Version = %q, want %q (a hardcoded build number must never return)", Version, "dev")
	}
}

// TestResolvedVersionFallback pins the startup guard: an empty or
// whitespace-only injected version falls back to "dev" so the UI badge
// can never render a bare "v".
func TestResolvedVersionFallback(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty injection falls back to dev", "", "dev"},
		{"whitespace injection falls back to dev", "  ", "dev"},
		{"ci run number passes through", "424242", "424242"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			Version = tc.in
			if got := resolvedVersion(); got != tc.want {
				t.Fatalf("resolvedVersion() with Version=%q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNoVersionFileAtRepoRoot guards re-introduction of the stale
// committed-VERSION pattern: the file was deleted because a mutable
// version file and CI-injected versions cannot coexist (the file would
// win and silently freeze the badge). `go test` runs with CWD = the
// package dir, so the repo root is "..".
func TestNoVersionFileAtRepoRoot(t *testing.T) {
	if _, err := os.Stat("../VERSION"); err == nil {
		t.Fatal("repo-root VERSION file exists; the committed-VERSION pattern must stay deleted (the version is CI-injected via github.run_number)")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error stat-ing ../VERSION: %v", err)
	}
}
