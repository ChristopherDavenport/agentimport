// Package doccheck holds the converters to their documents: every
// finding code a conversion reports must be listed in the source's
// docs/formats document, every gap code in docs/incompatibilities.md as
// well, and every identity member a conversion reports in its
// document's Identity section.
package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// identityRow matches a backquoted member path at the start of a table
// row.
var identityRow = regexp.MustCompile("(?m)^\\|\\s*`([^`]+)`")

// IdentityPaths returns the member paths a document's Identity section
// lists, as the first cell of a row of its table.
func IdentityPaths(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	start := strings.Index(text, "\n## Identity\n")
	if start < 0 {
		return map[string]bool{}, nil
	}
	section := text[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	paths := map[string]bool{}
	for _, m := range identityRow.FindAllStringSubmatch(section, -1) {
		paths[m[1]] = true
	}
	return paths, nil
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
	identity, err := IdentityPaths(filepath.Join(docs, "formats", source+".md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if f.Code == agentimport.IdentityCode && !identity[f.Path] {
			t.Errorf("identity member %s is not listed in the Identity section of docs/formats/%s.md", f.Path, source)
		}
		if !own[f.Code] && !shared[f.Code] {
			t.Errorf("finding code %s is not documented in docs/formats/%s.md", f.Code, source)
		}
		if f.Kind == agentimport.Gap && !gaps[f.Code] {
			t.Errorf("gap code %s is not listed in docs/incompatibilities.md", f.Code)
		}
	}
}
