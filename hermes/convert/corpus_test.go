package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentimport/hermes/sqlite"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// TestCorpus converts every Hermes database under
// AGENTIMPORT_CORPUS_HERMES, such as ~/.hermes, where it is: each must
// convert, verify and account for every row it holds. A database is
// copied and read, never written. The aggregate report is logged, and
// written to AGENTIMPORT_CORPUS_REPORT when that is set. Real sessions
// are private, so nothing is copied out and nothing of their content
// is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_HERMES")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_HERMES is not set")
	}
	paths, err := hermes.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	total.Unit = Unit
	failed, sessions := 0, 0
	for _, p := range paths {
		opts := Options{OpenDatabase: sqlite.OpenStore}
		db, err := OpenDatabase(p, opts)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		res, err := Convert(opts, db)
		if err != nil {
			db.Close()
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		sessions += len(res.Sessions)
		// The rows are counted on the copy the conversion read, so a
		// running Hermes cannot change them between the two.
		want, err := db.(*sqlite.DB).Units()
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Report.Lines(); got != want {
			t.Errorf("%s: report accounts for %d rows, the database holds %d", p, got, want)
		}
		db.Close()
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
	t.Logf("%d databases, %d sessions, %d failed\n%s", len(paths), sessions, failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
