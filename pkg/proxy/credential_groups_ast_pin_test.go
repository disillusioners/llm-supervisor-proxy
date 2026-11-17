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

// ── T-29 hardening: the NEGATIVE binding check ─────────────────────────────
//
// The pkg/proxy exemption above (groupIDProxyAllowedBase) is a BASE-NAME
// allowlist: {"groupPick", "pick", "gp", "res"} plus a "*.groupPick"
// field access. A base-name allowlist cannot see what the name is BOUND
// TO. A future `pick := someCredentialRef` in pkg/proxy would make
// `pick.GroupID` a models.CredentialRef.GroupID read that the
// exemption silently waves through — a narrow but real hole in the
// feature's central structural invariant.
//
// The check below closes it with go/ast only (no new dependency): for
// every scanned pkg/proxy file it collects the LOCAL BINDINGS of the
// exempted base identifiers — var declarations, := / = assignments,
// range bindings, function parameters and struct fields — and fails the
// pin when an exempted identifier is bound to a models.CredentialRef (or
// to a pointer to one), or to a type whose field set includes a
// CredentialRef GroupID field.
//
// The genuine load-balance-group bindings are unaffected: pick / gp /
// res / groupPick are groupPickInfo (or *groupPickInfo, or
// models.GroupRewriteResult), and their GroupID field is a STRING —
// not a models.CredentialRef.

// exemptGroupIDBaseNames are the base identifiers the pkg/proxy .GroupID
// exemption accepts (the Ident case of groupIDProxyAllowedBase). The
// negative binding check polices exactly this set.
var exemptGroupIDBaseNames = map[string]bool{
	"groupPick": true,
	"pick":      true,
	"gp":        true,
	"res":       true,
}

// modelsImportPath is the package the CredentialRef type is declared in.
const modelsImportPath = "github.com/disillusioners/llm-supervisor-proxy/pkg/models"

// scannedPkgFile is one scanned file with the package path the .GroupID
// read pin derives for it (the same normalisation that pin uses).
type scannedPkgFile struct {
	rel     string
	pkgPath string
	file    *ast.File
	fset    *token.FileSet
}

// modelsQualifierForFile returns the identifiers that may qualify the
// models package in this file (its import name or alias) and whether the
// package is dot-imported.
func modelsQualifierForFile(file *ast.File) (map[string]bool, bool) {
	qual := map[string]bool{}
	dotImported := false
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != modelsImportPath {
			continue
		}
		if imp.Name == nil {
			qual["models"] = true
			continue
		}
		if imp.Name.Name == "." {
			dotImported = true
			continue
		}
		qual[imp.Name.Name] = true
	}
	return qual, dotImported
}

// isCredentialRefTypeExpr reports whether a type expression denotes
// models.CredentialRef (or a pointer to it).
func isCredentialRefTypeExpr(expr ast.Expr, qual map[string]bool, dotImported bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return dotImported && e.Name == "CredentialRef"
	case *ast.SelectorExpr:
		if e.Sel == nil || e.Sel.Name != "CredentialRef" {
			return false
		}
		x, ok := e.X.(*ast.Ident)
		return ok && qual[x.Name]
	case *ast.StarExpr:
		return isCredentialRefTypeExpr(e.X, qual, dotImported)
	}
	return false
}

// exprBoundType extracts the type expression an assignment binds, when
// it is syntactically evident: a composite literal, an address-of a
// composite literal, or a single-value call to a package-local function
// with a declared result type.
func exprBoundType(rhs ast.Expr, localFuncs map[string]ast.Expr) ast.Expr {
	switch e := rhs.(type) {
	case *ast.CompositeLit:
		return e.Type
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return exprBoundType(e.X, localFuncs)
		}
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok {
			return localFuncs[id.Name]
		}
	}
	return nil
}

// exemptedCredentialRefBindings returns a violation string for every
// local binding of an exempted base identifier whose declared type is a
// models.CredentialRef (or a pointer to one), or a type whose field set
// includes a CredentialRef GroupID field.
//
// The type lookup is SYNTACTIC: a binding whose type is only inferable
// (e.g. `pick := someFunc()` with a multi-value call) is NOT resolved
// and therefore not reported. That is deliberate — a structural pin must
// never false-positive on the genuine load-balance-group bindings.
func exemptedCredentialRefBindings(sf scannedPkgFile, qual map[string]bool, dotImported bool,
	localFuncs map[string]ast.Expr, structGroupID map[string]ast.Expr) []string {
	var out []string
	report := func(name string, typ ast.Expr, pos token.Pos) {
		if name == "" || !exemptGroupIDBaseNames[name] || typ == nil {
			return
		}
		line := sf.fset.Position(pos).Line
		if isCredentialRefTypeExpr(typ, qual, dotImported) {
			out = append(out, fmt.Sprintf("%s:%d %q is bound to a models.CredentialRef — the .GroupID base-name exemption would wave through a credential-group read",
				sf.rel, line, name))
			return
		}
		// A type whose field set includes a CredentialRef GroupID field.
		var typeName string
		switch t := typ.(type) {
		case *ast.Ident:
			typeName = t.Name
		case *ast.SelectorExpr:
			if t.Sel != nil {
				typeName = t.Sel.Name
			}
		}
		if typeName == "" {
			return
		}
		if fieldType, ok := structGroupID[typeName]; ok &&
			isCredentialRefTypeExpr(fieldType, qual, dotImported) {
			out = append(out, fmt.Sprintf("%s:%d %q is bound to %s, whose GroupID field is a models.CredentialRef — the .GroupID base-name exemption would wave through a credential-group read",
				sf.rel, line, name, typeName))
		}
	}

	ast.Inspect(sf.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			// var pick models.CredentialRef
			for _, name := range node.Names {
				report(name.Name, node.Type, name.Pos())
			}
		case *ast.AssignStmt:
			// pick := models.CredentialRef{...} / pick = models.CredentialRef{...}
			// A multi-value assignment (one RHS, several LHS) binds
			// types that are NOT syntactically evident — skip it.
			if len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, lhs := range node.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				report(id.Name, exprBoundType(node.Rhs[i], localFuncs), id.Pos())
			}
		case *ast.RangeStmt:
			// for pick := range xs
			if node.Tok == token.DEFINE && node.Key != nil {
				if id, ok := node.Key.(*ast.Ident); ok {
					report(id.Name, nil, id.Pos())
				}
			}
		case *ast.Field:
			// function parameters and struct fields
			for _, name := range node.Names {
				report(name.Name, node.Type, name.Pos())
			}
		}
		return true
	})
	return out
}

// scanPkgFiles walks pkg/ exactly like the .GroupID read pin does and
// returns the parsed non-test files with their derived package path.
func scanPkgFiles(t *testing.T, root string) []scannedPkgFile {
	t.Helper()
	pkgRoot := filepath.Join(root, "pkg")
	var out []scannedPkgFile
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
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		pkgPath := "pkg"
		if len(parts) >= 2 {
			pkgPath = parts[0] + "/" + parts[1]
		}
		if len(parts) >= 3 && parts[1] == "store" {
			pkgPath = "pkg/store/" + parts[2]
		}
		if len(parts) >= 3 && parts[1] == "ui" {
			pkgPath = "pkg/ui"
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out = append(out, scannedPkgFile{rel: rel, pkgPath: pkgPath, file: file, fset: fset})
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg: %v", err)
	}
	return out
}

// collectTypeAndFuncIndex indexes the package-local struct types (by
// their GroupID field type) and the package-local functions (by their
// first result type) for the binding-type resolution.
func collectTypeAndFuncIndex(sf scannedPkgFile, structGroupID map[string]ast.Expr, localFuncs map[string]ast.Expr) {
	for _, decl := range sf.file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil || d.Type.Results == nil || len(d.Type.Results.List) == 0 {
				continue
			}
			localFuncs[d.Name.Name] = d.Type.Results.List[0].Type
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, fld := range st.Fields.List {
					for _, fname := range fld.Names {
						if fname.Name == "GroupID" {
							structGroupID[ts.Name.Name] = fld.Type
						}
					}
				}
			}
		}
	}
}

// TestCredentialGroups_Structural_ExemptedGroupIDBasesAreNotCredentialRefs
// closes the one hole in the pkg/proxy .GroupID exemption: the
// exemption is a BASE-NAME allowlist, so it cannot distinguish a
// load-balance-group variable from a models.CredentialRef variable.
//
// It collects the local bindings of the exempted base identifiers and
// fails the pin when one of them is bound to a models.CredentialRef (or
// to a type whose field set includes a CredentialRef GroupID field).
func TestCredentialGroups_Structural_ExemptedGroupIDBasesAreNotCredentialRefs(t *testing.T) {
	root := credGroupRepoRoot(t)
	scanned := scanPkgFiles(t, root)

	// Index the local struct types + functions of the packages that can
	// appear in a binding's declared type.
	structGroupID := map[string]ast.Expr{}
	localFuncs := map[string]ast.Expr{}
	for _, sf := range scanned {
		if sf.pkgPath == "pkg/proxy" || sf.pkgPath == "pkg/models" {
			collectTypeAndFuncIndex(sf, structGroupID, localFuncs)
		}
	}

	var violations []string
	for _, sf := range scanned {
		if sf.pkgPath != "pkg/proxy" {
			continue
		}
		qual, dotImported := modelsQualifierForFile(sf.file)
		violations = append(violations,
			exemptedCredentialRefBindings(sf, qual, dotImported, localFuncs, structGroupID)...)
	}
	if len(violations) > 0 {
		t.Fatalf("structural pin: an exempted .GroupID base identifier is bound to a models.CredentialRef:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestCredentialGroups_Structural_ExemptedBindingCheck_FlagsViolation
// proves the negative binding check is NON-VACUOUS: a source that binds
// an exempted base identifier to a models.CredentialRef MUST be flagged,
// while the genuine load-balance-group shape MUST NOT be.
func TestCredentialGroups_Structural_ExemptedBindingCheck_FlagsViolation(t *testing.T) {
	const violatingSrc = `package proxy

import "github.com/disillusioners/llm-supervisor-proxy/pkg/models"

func violator(pick models.CredentialRef) string {
	return pick.GroupID
}
`
	const violatingVarSrc = `package proxy

import "github.com/disillusioners/llm-supervisor-proxy/pkg/models"

func violatorVar() string {
	res := models.CredentialRef{GroupID: "g1"}
	return res.GroupID
}
`
	const legitimateSrc = `package proxy

type groupPickInfo struct {
	GroupID  string
	MemberID string
}

func legit(pick groupPickInfo, res groupPickInfo, groupPick *groupPickInfo) string {
	return pick.GroupID + res.GroupID + groupPick.GroupID
}
`
	structGroupID := map[string]ast.Expr{}
	localFuncs := map[string]ast.Expr{}

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"parameter binding", violatingSrc},
		{"var binding", violatingVarSrc},
	} {
		sf := parseSyntheticPkgFile(t, tc.name+".go", tc.src)
		qual, dotImported := modelsQualifierForFile(sf.file)
		violations := exemptedCredentialRefBindings(sf, qual, dotImported, localFuncs, structGroupID)
		if len(violations) == 0 {
			t.Fatalf("the negative binding check did NOT flag a models.CredentialRef bound to an exempted base identifier (%s) — the check is vacuous", tc.name)
		}
	}

	sf := parseSyntheticPkgFile(t, "legitimate.go", legitimateSrc)
	qual, dotImported := modelsQualifierForFile(sf.file)
	if violations := exemptedCredentialRefBindings(sf, qual, dotImported, localFuncs, structGroupID); len(violations) != 0 {
		t.Fatalf("the negative binding check flagged the legitimate load-balance-group bindings: %v", violations)
	}
}

// parseSyntheticPkgFile parses an in-memory source as a pkg/proxy file.
func parseSyntheticPkgFile(t *testing.T, name, src string) scannedPkgFile {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse synthetic %s: %v", name, err)
	}
	return scannedPkgFile{rel: name, pkgPath: "pkg/proxy", file: file, fset: fset}
}
