package proxy

// credential_groups_ast_pin_test.go — T-29 structural pin for the
// credential-groups feature.
//
// Invariant: credential SELECTION never sees group objects. Groups are
// flattened away before selection; selection only ever sees plain
// []models.CredentialRef. See plan phase1-plan.md §g.
//
// This test is a NON-VACUOUS scan: it walks the AST of every non-test
// .go file in the target packages and asserts:
//
//  1. No identifier with the "CredentialGroup" prefix (i.e. no
//     models.CredentialGroup* type or value) appears in:
//     pkg/credentiallb, pkg/stickyorder, pkg/skipcache, pkg/proxy.
//  2. The CredentialRef.GroupID field is READ only in pkg/models,
//     pkg/store/database, pkg/ui, pkg/modelscache. (modelscache is a
//     flatten site per A-4 — it flattens cache-side from the decorator
//     group mirror; it is a cache layer, not selection code, so it is
//     legitimately allowed to read GroupID.)
//     In pkg/proxy, every .GroupID selector must be on a load-balance
//     group type (groupPickInfo / GroupRewriteResult), never on a
//     models.CredentialRef — see the documented exemption below.
//  3. Import guard: pkg/credentiallb, pkg/stickyorder, pkg/skipcache do
//     NOT import pkg/store/database.
//
// Type-scoping note (the load-bearing trap): pkg/proxy non-test files
// contain PRE-EXISTING .GroupID selectors on groupPickInfo (the
// LOAD-BALANCE-GROUP type — pkg/grouplb member model ids, NOT
// credential-group ids). A bare name match would false-positive. We
// therefore allow, in pkg/proxy only, .GroupID selectors whose base
// expression is a known load-balance-group expression (groupPick, pick,
// gp, res, or a *.groupPick field access). Any other base (e.g. a
// CredentialRef variable) is a violation. The exemption is documented
// here and is the only place groupPickInfo.GroupID is permitted.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// credGroupTargetPkgs are the selection packages that must never see a
// CredentialGroup type or value.
var credGroupTargetPkgs = []string{
	"pkg/credentiallb",
	"pkg/stickyorder",
	"pkg/skipcache",
	"pkg/proxy",
}

// groupIDReadAllowedPkgs are the packages permitted to READ
// CredentialRef.GroupID (definition / flatten / API / cache-mirror).
var groupIDReadAllowedPkgs = map[string]bool{
	"pkg/models":         true,
	"pkg/store/database": true,
	"pkg/ui":             true,
	"pkg/modelscache":    true,
}

// groupIDProxyAllowedBase reports whether a .GroupID selector's base
// expression is a known load-balance-group expression (the documented
// exemption for pkg/proxy). Anything else is treated as a potential
// CredentialRef read and is a violation.
func groupIDProxyAllowedBase(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		switch e.Name {
		case "groupPick", "pick", "gp", "res":
			return true
		}
		return false
	case *ast.SelectorExpr:
		// arc.groupPick, rc.groupPick, etc.
		if e.Sel != nil && e.Sel.Name == "groupPick" {
			return true
		}
		return false
	}
	return false
}

func credGroupRepoRoot(t *testing.T) string {
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
	t.Fatalf("could not locate repo root (go.mod)")
	return ""
}

// collectGoFiles returns non-test .go files under repoRoot/relDir.
// Fails the test loudly if the directory is missing.
func collectGoFiles(t *testing.T, root, relDir string) []string {
	t.Helper()
	abs := filepath.Join(root, relDir)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		t.Fatalf("target package missing: %s (%v)", abs, err)
	}
	var out []string
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", abs, err)
	}
	return out
}

// TestCredentialGroups_Structural_NoCredentialGroupTypesInSelectionPkgs
// asserts that no non-test file in the selection packages references any
// identifier with the "CredentialGroup" prefix (no type or value).
func TestCredentialGroups_Structural_NoCredentialGroupTypesInSelectionPkgs(t *testing.T) {
	root := credGroupRepoRoot(t)
	var violations []string
	for _, rel := range credGroupTargetPkgs {
		for _, path := range collectGoFiles(t, root, rel) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				// Composite literal type: models.CredentialGroupConfig{...}
				if lit, ok := n.(*ast.CompositeLit); ok {
					if identHasCredGroupPrefix(lit.Type) {
						pos := fset.Position(lit.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d composite literal of CredentialGroup* type", relPath(root, path), pos.Line))
					}
				}
				// Any identifier (selector or bare) with the prefix.
				if id, ok := n.(*ast.Ident); ok {
					if strings.HasPrefix(id.Name, "CredentialGroup") {
						pos := fset.Position(id.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d identifier %q", relPath(root, path), pos.Line, id.Name))
					}
				}
				// Selector expression: models.CredentialGroupConfig or pkg.CredentialGroup...
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if strings.HasPrefix(sel.Sel.Name, "CredentialGroup") {
						pos := fset.Position(sel.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d selector .%s", relPath(root, path), pos.Line, sel.Sel.Name))
					}
				}
				return true
			})
		}
	}
	if len(violations) > 0 {
		t.Fatalf("structural pin: CredentialGroup* type/value found in selection packages:\n  %s", strings.Join(violations, "\n  "))
	}
}

func identHasCredGroupPrefix(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return strings.HasPrefix(e.Name, "CredentialGroup")
	case *ast.SelectorExpr:
		return strings.HasPrefix(e.Sel.Name, "CredentialGroup")
	}
	return false
}

// TestCredentialGroups_Structural_GroupIDReadRestricted asserts that the
// CredentialRef.GroupID field is read only in the allowed packages, with
// the documented pkg/proxy load-balance-group exemption.
func TestCredentialGroups_Structural_GroupIDReadRestricted(t *testing.T) {
	root := credGroupRepoRoot(t)
	// Collect all non-test .go files under pkg/.
	pkgRoot := filepath.Join(root, "pkg")
	var allFiles []string
	err := filepath.WalkDir(pkgRoot, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		allFiles = append(allFiles, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg: %v", err)
	}

	var violations []string
	for _, path := range allFiles {
		rel, _ := filepath.Rel(root, path)
		// normalise to package path (pkg/foo)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		pkgPath := "pkg"
		if len(parts) >= 2 {
			pkgPath = parts[0] + "/" + parts[1]
		}
		// deeper packages like pkg/store/database -> pkg/store/database
		if len(parts) >= 3 && parts[1] == "store" {
			pkgPath = "pkg/store/" + parts[2]
		}
		if len(parts) >= 3 && parts[1] == "ui" {
			// pkg/ui is a single package
			pkgPath = "pkg/ui"
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "GroupID" {
				return true
			}
			if groupIDReadAllowedPkgs[pkgPath] {
				return true
			}
			// pkg/proxy exemption: load-balance-group bases only.
			if pkgPath == "pkg/proxy" && groupIDProxyAllowedBase(sel.X) {
				return true
			}
			pos := fset.Position(sel.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d .GroupID read in package %s", rel, pos.Line, pkgPath))
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("structural pin: CredentialRef.GroupID read outside allowed packages:\n  %s", strings.Join(violations, "\n  "))
	}
}

// TestCredentialGroups_Structural_NoStoreDBImportInSelectionPkgs asserts
// that pkg/credentiallb, pkg/stickyorder, pkg/skipcache do not import
// pkg/store/database.
func TestCredentialGroups_Structural_NoStoreDBImportInSelectionPkgs(t *testing.T) {
	root := credGroupRepoRoot(t)
	forbidden := "github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	for _, rel := range []string{"pkg/credentiallb", "pkg/stickyorder", "pkg/skipcache"} {
		for _, path := range collectGoFiles(t, root, rel) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, spec := range file.Imports {
				imp, uerr := strconv.Unquote(spec.Path.Value)
				if uerr != nil {
					continue
				}
				if imp == forbidden {
					relp := relPath(root, path)
					t.Errorf("%s imports %q — selection packages must not import store/database", relp, imp)
				}
			}
		}
	}
}

func relPath(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
