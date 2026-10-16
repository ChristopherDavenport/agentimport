package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// TestCorpus converts every ATIF document under
// AGENTIMPORT_CORPUS_ATIF, such as a Harbor jobs directory, where it is:
// each must convert, verify and account for every unit of the documents
// it reads. The aggregate report is logged, and written to
// AGENTIMPORT_CORPUS_REPORT when that is set. Real trajectories are
// private, so nothing is copied and nothing of their content is
// printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_ATIF")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_ATIF is not set")
	}
	paths, err := atif.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	total.Unit = Unit
	failed := 0
	for _, p := range paths {
		f, err := atif.ReadFile(p)
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
		want := f.Units()
		for _, r := range Related(p) {
			if rf, err := atif.ReadFile(r); err == nil {
				want += rf.Units()
			}
		}
		if got := res.Report.Lines(); got != want {
			t.Errorf("%s: report accounts for %d units, the documents hold %d", p, got, want)
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
	t.Logf("%d documents, %d failed\n%s", len(paths), failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
