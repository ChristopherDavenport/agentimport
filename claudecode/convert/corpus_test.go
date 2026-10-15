package convert

import (
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// TestCorpus converts every session under AGENTIMPORT_CORPUS_CLAUDECODE,
// such as ~/.claude/projects, where it is: each must convert, verify and
// account for every line. The aggregate report is logged, and written to
// AGENTIMPORT_CORPUS_REPORT when that is set. Real sessions are private,
// so nothing is copied and nothing of their content is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_CLAUDECODE")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_CLAUDECODE is not set")
	}
	paths, err := claudecode.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	failed := 0
	for _, p := range paths {
		s, err := claudecode.Open(p)
		if err != nil {
			t.Errorf("%s: open: %v", p, err)
			failed++
			continue
		}
		res, err := Convert(s, Options{})
		if err != nil {
			t.Errorf("%s: %v", p, err)
			failed++
			continue
		}
		want := s.Main.LineCount()
		for _, a := range s.Agents {
			if a.Transcript != nil {
				want += a.Transcript.LineCount()
			}
		}
		if got := res.Report.Lines(); got != want {
			t.Errorf("%s: report accounts for %d lines, the files hold %d", p, got, want)
		}
		doccheck.Check(t, Source, res.Report)
		// Only the tally and the codes are aggregated: details and line
		// numbers name the corpus's files.
		for d, n := range res.Report.Tally {
			total.Tally[d] += n
		}
		for _, f := range res.Report.Findings {
			total.Add(agentimport.Finding{Code: f.Code, Kind: f.Kind})
		}
	}
	var b strings.Builder
	b.WriteString("sessions: ")
	b.WriteString(strings.TrimSpace(strings.Repeat(" ", 0)))
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
