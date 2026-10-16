package agentimport

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// formatPackages are the packages that read a native format. They
// import the standard library alone, so a program that only wants to
// read a transcript pays for nothing else. A package of this module's
// internal tree may stand in for the standard library when it imports
// the standard library alone, at any depth.
var formatPackages = []string{"claudecode", "pi", "codex", "atif", "cline", "openhands", "opencode", "clinesdk"}

// TestImportBoundary enforces the module's dependency rule on the
// format packages, which make deps cannot see: it checks the module as
// a whole.
func TestImportBoundary(t *testing.T) {
	for _, dir := range formatPackages {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		for _, bad := range nonStandard(t, dir, map[string]bool{}) {
			t.Errorf("%s imports %s; a format package imports the standard library alone", dir, bad)
		}
	}
}

// TestImportBoundaryCatches checks the walk finds what it must:
// internal/doccheck imports this module's root, which is not the
// standard library.
func TestImportBoundaryCatches(t *testing.T) {
	if len(nonStandard(t, "internal/doccheck", map[string]bool{})) == 0 {
		t.Error("internal/doccheck passes the boundary; the check is broken")
	}
	if bad := nonStandard(t, "internal/jsonline", map[string]bool{}); len(bad) > 0 {
		t.Errorf("internal/jsonline imports %v", bad)
	}
}

// nonStandard lists the imports of the package in dir, and of the
// internal packages it imports, that are not the standard library.
func nonStandard(t *testing.T, dir string, seen map[string]bool) []string {
	t.Helper()
	if seen[dir] {
		return nil
	}
	seen[dir] = true
	pkg, err := build.Default.ImportDir(filepath.Join(".", dir), 0)
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	var out []string
	for _, imp := range pkg.Imports {
		switch {
		case isStandard(imp):
		case strings.HasPrefix(imp, Module+"/internal/"):
			for _, bad := range nonStandard(t, strings.TrimPrefix(imp, Module+"/"), seen) {
				out = append(out, bad+" (through "+imp+")")
			}
		default:
			out = append(out, imp)
		}
	}
	return out
}

func isStandard(imp string) bool {
	first, _, _ := strings.Cut(imp, "/")
	return !strings.Contains(first, ".")
}
