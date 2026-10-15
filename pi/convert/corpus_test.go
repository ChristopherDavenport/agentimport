package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/pi"
)

// TestCorpus converts every session under AGENTIMPORT_CORPUS_PI, such
// as ~/.pi/agent/sessions, where it is: each must convert, verify and
// account for every line. The aggregate report is logged, and written to
// AGENTIMPORT_CORPUS_REPORT when that is set. Real sessions are private,
// so nothing is copied and nothing of their content is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_PI")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_PI is not set")
	}
	paths, err := pi.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	failed := 0
	for _, p := range paths {
		f, err := pi.ReadFile(p)
		if err != nil {
			t.Errorf("%s: read: %v", p, err)
			failed++
			continue
		}
		res, err := ConvertFile(p, Options{})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		if got, want := res.Report.Lines(), f.LineCount(); got != want {
			t.Errorf("%s: report accounts for %d lines, the file holds %d", p, got, want)
		}
		doccheck.Check(t, Source, res.Report)
		// Only the tally and the codes are aggregated: details and line
		// numbers name the corpus's files.
		for d, n := range res.Report.Tally {
			total.Tally[d] += n
		}
		for _, fd := range res.Report.Findings {
			total.Add(agentimport.Finding{Code: fd.Code, Kind: fd.Kind})
		}
	}
	var b strings.Builder
	if err := total.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d sessions, %d failed\n%s", len(paths), failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
