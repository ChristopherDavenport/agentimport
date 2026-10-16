package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
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

// TestTimestampCountedOnce checks that a step whose time is repaired is
// counted once, though a reference on its result asks for its time too.
func TestTimestampCountedOnce(t *testing.T) {
	f, err := atif.Parse([]byte(`{"schema_version":"ATIF-v1.7","session_id":"s","steps":[` +
		`{"source":"agent","timestamp":"2026-10-01T00:00:00","message":"","tool_calls":[{"tool_call_id":"c","function_name":"Task","arguments":{}}],` +
		`"observation":{"results":[{"source_call_id":"c","content":"x","subagent_trajectory_ref":[{"trajectory_id":"t1"}]}]}}],` +
		`"subagent_trajectories":[{"trajectory_id":"t1","steps":[{"source":"user","timestamp":"2026-10-01T00:00:01Z","message":"go"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(f, Options{CreatedAt: fixed})
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "atif/timestamp" && !strings.Contains(fd.Detail, "(steps: 1)") {
			t.Errorf("%s", fd.Detail)
		}
	}
	if !slices.Contains(res.Report.Codes(), "atif/timestamp") {
		t.Errorf("no atif/timestamp: %v", res.Report.Codes())
	}
}

// TestSlimFindings checks that what Slim leaves out is reported as left
// out, never as kept, and that a leading system step's extra is
// reported either way.
func TestSlimFindings(t *testing.T) {
	doc := `{"schema_version":"ATIF-v1.7","session_id":"s","steps":[` +
		`{"step_id":1,"source":"system","message":"Be brief.","extra":{"origin":"x"}},` +
		`{"step_id":2,"source":"user","message":"hi"},` +
		`{"step_id":3,"source":"agent","message":"a","metrics":"bad"},` +
		`{"step_id":4,"source":"agent","message":"b","llm_call_count":"many"},` +
		`{"step_id":5,"source":"agent","message":"c","metrics":{"prompt_tokens":"lots","completion_tokens":3,"cost_usd":0.1}},` +
		`{"step_id":6,"source":"agent","llm_call_count":0,"tool_calls":[{"tool_call_id":"d","function_name":"f","arguments":{}}],"metrics":5,` +
		`"observation":{"results":[{"source_call_id":"d","content":"ok"}]}},` +
		`{"step_id":7,"source":"system","message":"note","extra":{"context_management":{"type":"injection","boundary":"append"}},"observation":{"results":[{"content":"fact"}]}}]}`
	for _, slim := range []bool{false, true} {
		f, err := atif.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		res, err := Convert(f, Options{CreatedAt: fixed, Slim: slim})
		if err != nil {
			t.Fatal(err)
		}
		lines := map[string][]int{}
		for _, fd := range res.Report.Findings {
			lines[fd.Code] = append(lines[fd.Code], fd.Line)
			if !slim {
				continue
			}
			if fd.Kind == "kept" && slices.Contains([]string{"atif/mistyped-member", "atif/metrics", "atif/extra", "atif/unknown-member"}, fd.Code) {
				t.Errorf("slim: %s is kept: %s", fd.Code, fd.Detail)
			}
			for _, claim := range []string{"kept in atif:", "are in atif:", "is in atif:"} {
				if strings.Contains(fd.Detail, claim) {
					t.Errorf("slim: %s says %q: %s", fd.Code, claim, fd.Detail)
				}
			}
		}
		if !slices.Contains(lines["atif/extra"], 1) {
			t.Errorf("slim %v: the leading system step's extra is not reported: %v", slim, lines["atif/extra"])
		}
		if !slices.Contains(lines["atif/mistyped-member"], 6) {
			t.Errorf("slim %v: the dispatch step's metrics are not reported: %v", slim, lines["atif/mistyped-member"])
		}
	}
}

// TestStepExtraOnResponse checks that an agent step's extra, which
// describes the response, is on the response entry, not on the output
// after it.
func TestStepExtraOnResponse(t *testing.T) {
	res := convertFixture(t, fixtures(t)["claude-code"], Options{})
	found := false
	for _, e := range reread(t, res.Sessions[0]).Entries() {
		raw := e.Base().Unknown["atif:extra"]
		has := strings.Contains(string(raw), "req_1")
		if _, ok := e.(*agentsession.ResponseEntry); ok && has {
			found = true
		} else if has {
			t.Errorf("the step's extra is on a %T", e)
		}
	}
	if !found {
		t.Error("no response entry holds the step's extra")
	}
}
