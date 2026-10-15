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
// read a transcript pays for nothing else.
var formatPackages = []string{"claudecode", "pi", "codex"}

// TestImportBoundary enforces the module's dependency rule on the
// format packages, which make deps cannot see: it checks the module as
// a whole.
func TestImportBoundary(t *testing.T) {
	for _, dir := range formatPackages {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		pkg, err := build.Default.ImportDir(filepath.Join(".", dir), 0)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, imp := range pkg.Imports {
			if !isStandard(imp) {
				t.Errorf("%s imports %q; a format package imports the standard library alone", dir, imp)
			}
		}
	}
}

func isStandard(imp string) bool {
	first, _, _ := strings.Cut(imp, "/")
	return !strings.Contains(first, ".")
}
