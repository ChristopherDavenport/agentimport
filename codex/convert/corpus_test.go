package convert

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
)

// TestCorpus converts every rollout under AGENTIMPORT_CORPUS_CODEX, such
// as ~/.codex/sessions, where it is: each must convert, verify and
// account for every line. A compressed rollout is counted and skipped.
// The aggregate report is logged, and written to
// AGENTIMPORT_CORPUS_REPORT when that is set. Real sessions are private,
// so nothing is copied and nothing of their content is printed.
func TestCorpus(t *testing.T) {
	root := os.Getenv("AGENTIMPORT_CORPUS_CODEX")
	if root == "" {
		t.Skip("AGENTIMPORT_CORPUS_CODEX is not set")
	}
	paths, err := codex.FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	total := agentimport.NewReport(Source)
	failed, compressed := 0, 0
	for _, p := range paths {
		f, err := codex.ReadFile(p)
		if errors.Is(err, codex.ErrCompressed) {
			compressed++
			continue
		}
		if err != nil {
			t.Errorf("%s: read: %v", p, err)
			failed++
			continue
		}
		res, err := Convert(f, Options{})
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
	t.Logf("%d rollouts, %d failed, %d compressed and skipped\n%s", len(paths), failed, compressed, b.String())
	if out := os.Getenv("AGENTIMPORT_CORPUS_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
