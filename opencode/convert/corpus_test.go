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
		res, err := ConvertFile(p, Options{OpenDatabase: sqlite.OpenStore})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		sessions += len(res.Sessions)
		if got, want := res.Report.Lines(), storeUnits(t, p); got != want {
			t.Errorf("%s: report accounts for %d records, the store holds %d", p, got, want)
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
	t.Logf("%d stores, %d sessions, %d failed\n%s", len(paths), sessions, failed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// storeUnits counts the records of the store a path names: a data
// directory's storage files and database rows, or a database's rows.
func storeUnits(t testing.TB, path string) int {
	t.Helper()
	root, ok := opencode.StoreOf(path)
	if !ok {
		t.Fatalf("%s is no store", path)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		return units(t, root)
	}
	db, err := sqlite.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n, err := db.Units()
	if err != nil {
		t.Fatal(err)
	}
	return n
}
