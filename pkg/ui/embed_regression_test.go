package ui

// load-balance-groups / P4.5 (P3 #7 carry-forward) — embed regression
// test.
//
// P3.8 establishes the embed discipline: pkg/ui/static/* is embedded
// via go:embed, vite builds under pkg/ui/static/assets/<hash>* with
// the index.html referencing the hashed files. A future regression
// in either the vite build (wrong hash output paths) or the embed
// boundary (missing files) would silently break the dashboard.
// This test parses the embedded static/index.html and asserts
// every /ui/assets/... reference resolves from embed.FS, both for
// the head <link>/<script> blocks (the typical ones to break) and
// for inline sources (e.g., favicons or inline <link rel>
// references).
//
// The embed boundary is the same one pkg/ui/server.go:90 registers:
// `//go:embed static/*`. Anything outside `static/` is irrelevant.

import (
	"embed"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// pkgStaticDirForEmbedTest is a fresh embed.FS rooted at the
// package's static/ directory — exactly the same shape as
// pkg/ui/server.go:90's //go:embed static/* directive. A per-test
// var would not compile (go:embed only applies at package level),
// so this is the package-level canonical boundary used only by
// the embed regression tests below. The production server keeps
// its own staticFiles variable for handler use.
//
//go:embed static
var pkgStaticDirForEmbedTest embed.FS

// pkgStaticIndexForEmbedTest is the embedded pkg/ui/static/index.html
// file content, kept as a package-level byte slice so the
// regression tests can parse it. Mirrors the same //go:embed
// directive path — the file is embedded by the build at compile
// time.
//
//go:embed static/index.html
var pkgStaticIndexForEmbedTest []byte

// matchAssetRef returns every /ui/assets/<...> reference in the
// document. The pattern is intentionally permissive: we accept any
// extension and any number of slashes / hash fragments after the
// asset path. This catches:
//   - <script type="module" src="/ui/assets/index-abc.js">
//   - <link rel="stylesheet" href="/ui/assets/index-def.css">
//   - href="/ui/assets/foo-abc.svg#fragment"
// It does NOT match anything outside `/ui/assets/` (those are NOT
// expected to be embedded; they go through the Go HTTP handlers).
var assetRefRe = regexp.MustCompile(`/ui/assets/[A-Za-z0-9_.\-]+(?:#[^"' ]*)?"?`)

// TestEmbedStatic_AllAssetReferencesResolve pins the embed
// discipline. Reads the embedded pkg/ui/static/index.html, parses
// out every /ui/assets/... reference, and asserts each one
// resolves from the package embed boundary.
//
// If the test suite ever ships a stale index.html (referencing a
// hashed asset that didn't make it into the bundle), the test
// fails with a clear, attributable error — not a 404 at runtime.
func TestEmbedStatic_AllAssetReferencesResolve(t *testing.T) {
	sub, err := fs.Sub(pkgStaticDirForEmbedTest, "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}

	rawRefs := assetRefRe.FindAllString(string(pkgStaticIndexForEmbedTest), -1)
	if len(rawRefs) == 0 {
		t.Fatalf("index.html contains zero /ui/assets/... references; embed boundary changed?")
	}
	refs := make([]string, 0, len(rawRefs))
	seen := map[string]bool{}
	for _, raw := range rawRefs {
		// Trim trailing quote/closing-angle from the pattern's
		// lazy optional.
		ref := strings.TrimRight(raw, "\">")
		if seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		t.Fatalf("no deduplicated references after trim; pattern mismatch")
	}

	// Strip the leading "/ui/assets/" so we can resolve against
	// the embed FS (which is rooted at static/, NOT at /).
	// If the URL has a query string or fragment, drop it
	// (embed.FS is path-only).
	for _, r := range refs {
		rel := strings.TrimPrefix(r, "/ui/assets/")
		if i := strings.IndexAny(rel, "?#"); i >= 0 {
			rel = rel[:i]
		}
		if rel == "" {
			t.Errorf("ref %q has an empty path component", r)
			continue
		}
		full := "assets/" + rel
		if _, err := fs.Stat(sub, full); err != nil {
			t.Errorf("embedded asset %q (ref %q) does NOT exist in static/: %v", rel, r, err)
		}
	}
}

// TestEmbedStatic_IndexHTMLNotEmpty is a smaller companion that
// fails fast when the embed is empty (e.g. //go:embed was removed
// or the path was wrong). The companion reduces the cost of
// diagnosing a broken embed vs an asset-resolution mismatch.
func TestEmbedStatic_IndexHTMLNotEmpty(t *testing.T) {
	if len(pkgStaticIndexForEmbedTest) == 0 {
		t.Fatal("embedded index.html is empty — embed boundary broken")
	}
	if !strings.Contains(string(pkgStaticIndexForEmbedTest), "<html") {
		head := string(pkgStaticIndexForEmbedTest)
		if len(head) > 80 {
			head = head[:80]
		}
		t.Errorf("embedded index.html missing <html tag; first %d bytes: %q", len(head), head)
	}
}
