package convert

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// rebuildDir is agentsession's testdata/rebuild, the documents RFC 0001's
// ATIF read-back rule is held to (format 0.18), in the module this one
// requires.
func rebuildDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/ChristopherDavenport/agentsession").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		t.Skipf("agentsession's module directory: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "testdata", "rebuild")
}

// TestRebuildConformance converts agentsession's rebuild fixtures and
// holds the converter to what the reference rebuild says of each: a
// redacted session's document as exported is restored as redacted, every
// entry to its id; one edited in a step's visible text, one with a step
// appended, or one without its leaf's entry is not, and says why; a
// redacted child embedded in a parent's document is restored as
// redacted; and a document whose media WriteDocument moved to files
// verifies once they are put back inline.
func TestRebuildConformance(t *testing.T) {
	dir := rebuildDir(t)
	for _, tc := range []struct {
		name     string
		redacted bool
		// session is the session whose header is checked: the main one,
		// or the embedded child.
		child bool
		code  string
	}{
		{"verified", true, false, ""},
		{"visible-edit", false, false, "atif/entry-not-rebuilt"},
		{"appended-step", false, false, "atif/step-not-rebuilt"},
		{"removed-leaf", false, false, "atif/redaction-not-restored"},
		{"embedded-child", true, true, ""},
		{"media", false, false, ""},
	} {
		res, err := ConvertFile(filepath.Join(dir, tc.name+".json"), Options{CreatedAt: fixed})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		doccheck.Check(t, Source, res.Report)
		s := res.Sessions[0]
		if tc.child {
			if len(res.Sessions) < 2 {
				t.Fatalf("%s: %d sessions", tc.name, len(res.Sessions))
			}
			s = res.Sessions[len(res.Sessions)-1]
		}
		if got := reread(t, s).Header().Redacted; got != tc.redacted {
			t.Errorf("%s: redacted %v, want %v: %v", tc.name, got, tc.redacted, res.Report.Codes())
		}
		codes := res.Report.Codes()
		if tc.code != "" && !slices.Contains(codes, tc.code) {
			t.Errorf("%s: no %s: %v", tc.name, tc.code, codes)
		}
		if tc.code == "" && (slices.Contains(codes, "atif/entry-not-rebuilt") || slices.Contains(codes, "atif/not-rebuilt")) {
			t.Errorf("%s: %v", tc.name, codes)
		}
	}
}
