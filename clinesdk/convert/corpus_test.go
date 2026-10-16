package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/clinesdk/sqlite"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// TestCorpus converts every Cline SDK session under
// AGENTIMPORT_CORPUS_CLINESDK, such as ~/.cline/data, where it is: each
// must convert, verify and account for every unit it holds. The index
// is copied and read, never written. The aggregate report is logged,
// and written to AGENTIMPORT_CORPUS_REPORT when that is set. Real
// sessions are private, so nothing is copied out and nothing of their
// content is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_CLINESDK")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_CLINESDK is not set")
	}
	paths, err := clinesdk.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	total.Unit = Unit
	failed, sessions := 0, 0
	for _, p := range paths {
		s, err := clinesdk.ReadSession(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		res, err := ConvertFile(p, Options{OpenIndex: sqlite.OpenIndex})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		sessions += len(res.Sessions)
		if got, want := res.Report.Lines(), s.Units(); got != want {
			t.Errorf("%s: report accounts for %d units, the session holds %d", p, got, want)
		}
		doccheck.Check(t, Source, res.Report)
		// Only the tally and the codes are aggregated: details name the
		// corpus's sessions.
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
	t.Logf("%d session directories, %d sessions, %d failed\n%s", len(paths), sessions, failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
