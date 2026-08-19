package translator

// P3-3 drift-trap regression gate (architecture §5.6, H10): all
// reasoning_details mutation funnels through the translator module.
//
// This test walks the AST of every wiring file that calls into the
// translator on the four request/response paths (race-external,
// race-internal, ultimate-internal, ultimate-external). If any of those
// files contains a `translator.ReasoningDetail{...}` (or
// `&translator.ReasoningDetail{...}`) composite literal, the test
// fails — inline field mutation outside the translator module is the
// failure mode §5.6 calls out.
//
// Scope is intentionally NARROW:
//   - `*ast.CompositeLit` nodes ONLY (literal expressions, not
//     declarations, function params, range loops, or field types).
//   - Type must resolve to `translator.ReasoningDetail` (the named
//     composite type defined in this package).
//   - Files outside `pkg/proxy/translator/` (the four call sites).
//
// Pure Go test, no subprocess, no git dependency — deterministic and
// CI-portable per H10.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wiringFiles are the four call-site files whose ASTs must NOT contain
// a `translator.ReasoningDetail{...}` composite literal. Paths are
// resolved relative to the test package directory via repoRoot().
var wiringFiles = []string{
	"pkg/proxy/race_executor.go",
	"pkg/proxy/internal_handler.go",
	"pkg/ultimatemodel/handler_internal.go",
	"pkg/ultimatemodel/handler_external.go",
}

// translatorSourceFiles is the allow-list — the only files in which
// `ReasoningDetail{...}` and `&translator.ReasoningDetail{...}`
// composite literals are expected to live. The list is short on
// purpose: if a new translator source file appears that needs to use
// the type, this list must be updated deliberately (a code-review
// step, not silent drift).
var translatorSourceFiles = map[string]bool{
	"minimax.go":        true,
	"minimax_stream.go": true,
}

// repoRoot walks upward from the test package directory until it finds
// a go.mod file. The four wiring files are repo-relative paths and are
// stable as long as the repo layout doesn't move; the locator exists so
// the test passes regardless of where `go test` is invoked from.
func repoRoot(t *testing.T) string {
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
	t.Fatalf("could not locate repo root (go.mod) from %s", wd)
	return ""
}

// TestDriftTrap_ReasoningDetailCompositeLitsOutsideTranslator is the
// regression gate. It parses each wiring file, walks the AST, and
// asserts that no `*ast.CompositeLit` resolves to the
// translator.ReasoningDetail named type.
//
// If this test fails, someone introduced inline field mutation outside
// the translator module — the architecture §5.6 invariant has been
// broken. Revert the change and route the mutation through the
// translator package's exported API instead.
func TestDriftTrap_ReasoningDetailCompositeLitsOutsideTranslator(t *testing.T) {
	root := repoRoot(t)

	// Pre-flight: every allow-listed translator source file must
	// exist; this catches typos in the allow-list before we walk
	// anything.
	for name := range translatorSourceFiles {
		p := filepath.Join(root, "pkg/proxy/translator", name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("translator source file listed in allow-list is missing: %s (%v)", p, err)
		}
	}

	// Walk each wiring file.
	var violations []string
	for _, rel := range wiringFiles {
		abs := filepath.Join(root, rel)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("wiring file missing: %s (%v)", abs, err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, abs, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", abs, err)
		}
		violations = append(violations, walkForTranslatorReasoningDetail(fset, file, abs)...)
	}

	if len(violations) > 0 {
		t.Fatalf("drift-trap regression: %d composite literal(s) of translator.ReasoningDetail found outside the translator module:\n  %s\n\nMove the mutation into pkg/proxy/translator/ — see architecture §5.6.",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// walkForTranslatorReasoningDetail inspects a single parsed file for
// `translator.ReasoningDetail{...}` and `&translator.ReasoningDetail{...}`
// composite literals. Returns human-readable violation descriptions
// (file:line — snippet).
func walkForTranslatorReasoningDetail(fset *token.FileSet, file *ast.File, absPath string) []string {
	var out []string
	relPath := absPath
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(cwd, absPath); err == nil {
			relPath = r
		}
	}
	for _, decl := range file.Decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !typeIsTranslatorReasoningDetail(lit.Type) {
				return true
			}
			pos := fset.Position(lit.Pos())
			out = append(out, fmt.Sprintf("%s:%d  %s", relPath, pos.Line, snippetFor(lit)))
			return true
		})
	}
	return out
}

// typeIsTranslatorReasoningDetail returns true when the AST expression
// resolves to the named type `translator.ReasoningDetail`. Accepts
// both the value form (`translator.ReasoningDetail{...}`) and the
// pointer form (`&translator.ReasoningDetail{...}`).
func typeIsTranslatorReasoningDetail(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		// translator.ReasoningDetail → Sel.Name == "ReasoningDetail"
		// AND X is the package identifier `translator`.
		if e.Sel == nil || e.Sel.Name != "ReasoningDetail" {
			return false
		}
		x, ok := e.X.(*ast.Ident)
		return ok && x.Name == "translator"
	case *ast.UnaryExpr:
		// &translator.ReasoningDetail{...} → unary & wraps a
		// SelectorExpr matching the case above.
		if e.Op != token.AND {
			return false
		}
		return typeIsTranslatorReasoningDetail(e.X)
	}
	return false
}

// snippetFor produces a short, single-line snippet of an AST node for
// failure messages. It avoids reflecting the entire sub-tree and keeps
// the violation log readable.
func snippetFor(n ast.Node) string {
	switch v := n.(type) {
	case *ast.CompositeLit:
		// Print just enough to identify the literal — the type name
		// is the discriminator we care about.
		if v.Type != nil {
			return typeString(v.Type) + "{...}"
		}
		return "<anonymous>{...}"
	}
	return fmt.Sprintf("%T", n)
}

// typeString renders an AST type expression as it would appear in
// source — used only for failure messages.
func typeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
		return "<selector>"
	case *ast.UnaryExpr:
		return "&" + typeString(e.X)
	case *ast.Ident:
		return e.Name
	}
	return fmt.Sprintf("%T", expr)
}

// TestDriftTrap_AllowListContainsExpectedFiles is a sanity check: the
// allow-list must list the two translator source files that today
// construct the type. If a future refactor moves the constructor
// (e.g. a builder factory) and this list is updated, the change must
// be visible in code review.
//
// This is intentionally NOT a "negative" test against the allow-list
// (the allow-list is allowed to grow), but it does lock in the
// baseline so a silent deletion is caught.
func TestDriftTrap_AllowListContainsExpectedFiles(t *testing.T) {
	expected := []string{"minimax.go", "minimax_stream.go"}
	for _, name := range expected {
		if !translatorSourceFiles[name] {
			t.Errorf("translator source file allow-list missing %q (the type's constructors live here)", name)
		}
	}
}

// TestDriftTrap_AllowListFilesParseClean is a sanity check that every
// allow-listed translator source file actually parses without syntax
// errors. If the type moves between files, this catches the move
// before the main gate fails mysteriously.
func TestDriftTrap_AllowListFilesParseClean(t *testing.T) {
	root := repoRoot(t)
	for name := range translatorSourceFiles {
		abs := filepath.Join(root, "pkg/proxy/translator", name)
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, abs, nil, parser.AllErrors); err != nil {
			t.Errorf("allow-listed file %s failed to parse: %v", name, err)
		}
	}
}
