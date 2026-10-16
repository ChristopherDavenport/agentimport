package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentimport/opencode/sqlite"
)

// TestCorpus converts every OpenCode store under
// AGENTIMPORT_CORPUS_OPENCODE, such as ~/.local/share/opencode, where
// it is: each must convert, verify and account for every record it
// holds. A database is copied and read, never written. The aggregate
// report is logged, and written to AGENTIMPORT_CORPUS_REPORT when that
// is set. Real sessions are private, so nothing is copied out and
// nothing of their content is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_OPENCODE")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_OPENCODE is not set")
	}
	paths, err := opencode.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	total.Unit = Unit
	failed, sessions := 0, 0
	for _, p := range paths {
		opts := Options{OpenDatabase: sqlite.OpenStore}
		st, err := OpenStores(p, opts)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		res, err := Convert(opts, st.Stores...)
		if err != nil {
			st.Close()
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		st.notes(res.Report)
		sessions += len(res.Sessions)
		// The records are counted on the copy the conversion read, so
		// a running OpenCode cannot change them between the two.
		if got, want := res.Report.Lines(), units(t, st); got != want {
			t.Errorf("%s: report accounts for %d records, the store holds %d", p, got, want)
		}
		st.Close()
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
	t.Logf("%d stores, %d sessions, %d failed\n%s", len(paths), sessions, failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
