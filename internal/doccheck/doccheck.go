// Package doccheck holds the converters to their documents: every
// finding code a conversion reports must be listed in the source's
// docs/formats document, and every gap code in docs/incompatibilities.md
// as well.
package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
)

// code matches a backquoted finding code at the start of a table row.
var code = regexp.MustCompile("(?m)^\\|\\s*`([a-z0-9_-]+/[a-z0-9_.-]+)`")

// Codes returns the finding codes a document's tables list, as the
// first cell of a row.
func Codes(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	codes := map[string]bool{}
	for _, m := range code.FindAllSubmatch(data, -1) {
		codes[string(m[1])] = true
	}
	return codes, nil
}

// Root is the repository root.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// Check fails t for every code in r that docs/formats/<source>.md does
// not list, and every gap code docs/incompatibilities.md does not.
// Codes in the agentimport/ namespace, which the shared builder
// reports, are documented in docs/formats/README.md.
func Check(t testing.TB, source string, r *agentimport.Report) {
	t.Helper()
	docs := filepath.Join(Root(), "docs")
	own, err := Codes(filepath.Join(docs, "formats", source+".md"))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := Codes(filepath.Join(docs, "formats", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := Codes(filepath.Join(docs, "incompatibilities.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if !own[f.Code] && !shared[f.Code] {
			t.Errorf("finding code %s is not documented in docs/formats/%s.md", f.Code, source)
		}
		if f.Kind == agentimport.Gap && !gaps[f.Code] {
			t.Errorf("gap code %s is not listed in docs/incompatibilities.md", f.Code)
		}
	}
}
