package convert

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes/sqlite"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// TestSessionFails converts a database where one session fails: its
// rows are dropped and reported, and its sub-agents are converted on
// their own.
func TestSessionFails(t *testing.T) {
	testHookRun = func(sc *sconv) error {
		if sc.rec.s.ID == "20261005_060000_ee0007" {
			return errors.New("injected")
		}
		return nil
	}
	defer func() { testHookRun = nil }()
	res := convertFixture(t, "subagents", Options{})
	failed := 0
	for _, fd := range res.Report.Findings {
		if fd.Code == "hermes/session-failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d failures reported", failed)
	}
	if len(res.Sessions) != 6 {
		t.Errorf("%d sessions", len(res.Sessions))
	}
	for _, s := range res.Sessions {
		if h := s.Header(); h.ParentSession == "20261005_060000_ee0007" && h.SpawnedBy != "" {
			t.Errorf("session %s keeps spawned_by of a parent that failed", s.ID())
		}
	}
}

// TestLeafContext holds the context a converted session resumes with
// to the rows Hermes resumes with: the live rows, in id order.
func TestLeafContext(t *testing.T) {
	res := convertFixture(t, "inplace", Options{})
	ctx, err := res.Sessions[0].Context()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, it := range ctx.Items {
		texts = append(texts, renderItems(openresponses.Items{it})...)
	}
	want := []string{"user: [CONTEXT COMPACTION — REFERENCE ONLY] Earlier tu", "user: Ship it.", "assistant: Shipped."}
	if len(texts) != len(want) {
		t.Fatalf("context %v", texts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("item %d: %q, want %q", i, texts[i], want[i])
		}
	}
}

// TestConvertFile converts a home directory and refuses what is not a
// database.
func TestConvertFile(t *testing.T) {
	root := build(t, "basic")
	res, err := ConvertFile(root, Options{OpenDatabase: sqlite.OpenStore})
	if err != nil || len(res.Sessions) != 1 {
		t.Fatalf("%v, %v", res, err)
	}
	if _, err := ConvertFile(root, Options{}); err == nil {
		t.Error("converts with no database reader")
	}
	if _, err := ConvertFile(filepath.Join(root, "none"), Options{OpenDatabase: sqlite.OpenStore}); err == nil {
		t.Error("converts a missing path")
	}
	if _, err := ConvertFile(t.TempDir(), Options{OpenDatabase: sqlite.OpenStore}); err == nil {
		t.Error("converts a directory with no database")
	}
	if err := agentimport.Verify(res.Sessions[0]); err != nil {
		t.Error(err)
	}
	var _ *agentsession.Session = res.Sessions[0]
}
