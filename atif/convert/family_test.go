package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestConvertNamesItself checks that Convert, given a file read from
// disk and the directory to read, does not convert a reference to that
// file again.
func TestConvertNamesItself(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self.json")
	doc := `{"schema_version":"ATIF-v1.7","session_id":"self","steps":[{"source":"agent","message":"","tool_calls":[{"tool_call_id":"c","function_name":"Task","arguments":{}}],` +
		`"observation":{"results":[{"source_call_id":"c","content":"x","subagent_trajectory_ref":[{"trajectory_path":"self.json"}]}]}}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := atif.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(f, Options{CreatedAt: fixed, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || !slices.Contains(res.Report.Codes(), "atif/ref-repeated") {
		t.Errorf("%d sessions, codes %v", len(res.Sessions), res.Report.Codes())
	}
	if got, want := res.Report.Lines(), f.Units(); got != want {
		t.Errorf("the report accounts for %d units; the document holds %d", got, want)
	}
}

// TestUnreadableRefNamesNoPath checks that a reference to a file that
// cannot be read, a directory, is reported without the machine's path.
func TestUnreadableRefNamesNoPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trajectory.json")
	doc := `{"schema_version":"ATIF-v1.7","session_id":"s","steps":[{"source":"agent","message":"","tool_calls":[{"tool_call_id":"c","function_name":"Task","arguments":{}}],` +
		`"observation":{"results":[{"source_call_id":"c","content":"x","subagent_trajectory_ref":[{"trajectory_path":"sub.json"}]}]}}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ConvertFile(path, Options{CreatedAt: fixed})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fd := range res.Report.Findings {
		if fd.Code != "atif/ref-unresolved" {
			continue
		}
		found = true
		if strings.Contains(fd.Detail, dir) {
			t.Errorf("the finding names the path: %s", fd.Detail)
		}
	}
	if !found {
		t.Errorf("no atif/ref-unresolved: %v", res.Report.Codes())
	}
}
