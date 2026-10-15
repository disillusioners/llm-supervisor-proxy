package decision

// Small helpers used by handler_test.go (Decision Models /
// P2 suite). Kept in a separate file so the test harness
// stays self-contained.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// compileRE thin wrapper for regexp.Compile.
func compileRE(s string) (*regexp.Regexp, error) {
	return regexp.Compile(s)
}

// readPackageDir is a thin wrapper over os.ReadDir to keep
// the import surface tight in the test file.
func readPackageDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}

// readFile is a thin wrapper over os.ReadFile.
func readFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

// getwdOrFatal is the cwd helper used by the transitive-
// closure test.
func getwdOrFatal(t interface {
	Fatalf(format string, args ...interface{})
}) string {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return cwd
}

// joinPath is filepath.Join used by the transitive-closure
// test (module root = cwd/../..).
func joinPath(elem ...string) string {
	return filepath.Join(elem...)
}

// runGoListDeps invokes `go list -deps` for the transitive-
// closure test and returns the bytes (the test parses them
// itself).
func runGoListDeps(t interface {
	Fatalf(format string, args ...interface{})
}, moduleRoot string) []byte {
	cmd := exec.Command("go", "list", "-deps", "./pkg/decision/...")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	return out
}
