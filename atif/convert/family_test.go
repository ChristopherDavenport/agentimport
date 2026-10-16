package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
)

// TestFailedChildDropsItsFiles checks that a sub-agent document that
// fails after converting a file it names takes that file with it: the
// file is not recorded as held, and its units are among those dropped.
func TestFailedChildDropsItsFiles(t *testing.T) {
	dir := t.TempDir()
	grand := `{"schema_version":"ATIF-v1.7","steps":[{"source":"user","message":"g"},{"source":"agent","message":"done"}]}`
	if err := os.WriteFile(filepath.Join(dir, "grand.json"), []byte(grand), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := atif.Parse([]byte(`{"schema_version":"ATIF-v1.7","steps":[` +
		`{"source":"agent","message":"","tool_calls":[{"tool_call_id":"c","function_name":"Task","arguments":{}}],` +
		`"observation":{"results":[{"source_call_id":"c","content":"x","subagent_trajectory_ref":[{"trajectory_path":"grand.json"}]}]}},` +
		`{"source":"user","message":"next"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	// An extra no encoder takes: the step after the reference fails the
	// document, as nothing a reader returns can.
	f.Doc.Steps[1].Ext = json.RawMessage(`{`)
	fam := newFamily(Options{CreatedAt: fixed, Dir: dir})
	_, _, err = fam.child(docInput{t: f.Doc, file: f, name: "child.json", dir: dir}, target{id: "child"}, fam.report, "")
	if err == nil {
		t.Fatal("the child converted")
	}
	g, err := atif.ReadFile(filepath.Join(dir, "grand.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fam.report.Lines(), f.Units()+g.Units(); got != want {
		t.Errorf("the report accounts for %d units; the child and the file it named hold %d", got, want)
	}
	if len(fam.held) != 0 {
		t.Errorf("held = %v, want nothing", fam.held)
	}
	if !slices.Contains(fam.report.Codes(), "atif/subagent-failed") {
		t.Errorf("codes %v", fam.report.Codes())
	}
}
