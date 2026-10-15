package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sameJSON reports whether a and b are the same JSON value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%v: %s", err, a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

// TestRoundTrip decodes every fixture line and encodes it again: the
// result must be the same JSON value, so nothing the package does not
// type is lost and nothing absent is invented.
func TestRoundTrip(t *testing.T) {
	var files []string
	for _, pattern := range []string{"testdata/*.jsonl", "convert/testdata/*/*.jsonl", "convert/testdata/*/*/subagents/*.jsonl"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 8 {
		t.Fatalf("found %d fixture files", len(files))
	}
	for _, f := range files {
		tr, err := ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range tr.Lines {
			got, err := json.Marshal(l)
			if err != nil {
				t.Fatalf("%s:%d: %v", f, l.No, err)
			}
			if !sameJSON(t, l.Raw, got) {
				t.Errorf("%s:%d does not round-trip\nread:  %s\nwrote: %s", f, l.No, l.Raw, got)
			}
		}
	}
}

func TestReadTolerates(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"a"}}`,
		strings.Repeat("\x00", 40),
		"\x00\x00" + `{"type":"mode","mode":"normal"}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"m`,
		`{"type":"ai-title","aiTitle":7}`,
		`{"type":"user","uuid":"u2","parentUuid":"u1","message":{"role":"user","content":"b"}}`,
	}, "\n")
	tr, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if tr.LineCount() != 6 || len(tr.Lines) != 4 || len(tr.Errors) != 2 {
		t.Fatalf("lines %d, read %d, errors %v", tr.LineCount(), len(tr.Lines), tr.Errors)
	}
	if tr.Errors[0].No != 2 || tr.Errors[1].No != 4 {
		t.Errorf("errors at %d and %d; want 2 and 4", tr.Errors[0].No, tr.Errors[1].No)
	}
	if !reflect.DeepEqual(tr.NULLines, []int{2, 3}) {
		t.Errorf("NUL lines = %v", tr.NULLines)
	}
	if l := tr.Lines[1]; l.No != 3 || l.Type != "mode" || l.Err != nil {
		t.Errorf("line after NULs = %+v", l)
	}
	if l := tr.Lines[2]; l.No != 5 || l.Err == nil || l.Type != TypeAITitle {
		t.Errorf("undecodable line = %+v", l)
	}
	if l := tr.Lines[3]; l.No != 6 || l.Message.Content.Text != "b" {
		t.Errorf("last line = %+v", l)
	}
}

func TestReadLongLine(t *testing.T) {
	in := `{"type":"x","pad":"` + strings.Repeat("a", 2<<20) + `"}` + "\n" + `{"type":"mode"}`
	tr, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Lines) != 2 || tr.Lines[1].No != 2 {
		t.Fatalf("a line longer than the buffer: %d lines", len(tr.Lines))
	}
}

func TestOpenFindsAgents(t *testing.T) {
	dir := filepath.Join("convert", "testdata", "subagents")
	paths, err := FindSessions(dir)
	if err != nil || len(paths) != 1 {
		t.Fatalf("FindSessions = %v, %v", paths, err)
	}
	s, err := Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range s.Agents {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, " ") != "a0001 a0002 a0004 a0005 ateam-0003 ateam-0006" {
		t.Errorf("agents = %v", ids)
	}
	if s.Agents[2].Transcript != nil || s.Agents[2].Meta.ToolUseID != "toolu_NONE" {
		t.Errorf("meta-only agent = %+v", s.Agents[2])
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal(err)
	}
}

// TestFindSessions walks trees whose directory names the old finder
// skipped on sight: a fixture directory called "subagents", and a root
// called "memory" or "tool-results". A session's own directory, beside
// its transcript, is still not searched.
func TestFindSessions(t *testing.T) {
	const (
		main  = "11111111-1111-4111-8111-00000000aaaa"
		other = "11111111-1111-4111-8111-00000000bbbb"
	)
	root := t.TempDir()
	files := []string{
		"subagents/" + main + ".jsonl",
		"subagents/" + main + "/subagents/agent-a1.jsonl",
		"subagents/" + main + "/subagents/" + other + ".jsonl",
		"subagents/" + main + "/tool-results/" + other + ".jsonl",
		"memory/" + other + ".jsonl",
		"tool-results/nested/" + other + ".jsonl",
		"notes.jsonl",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		root string
		want []string
	}{
		{"", []string{"memory/" + other + ".jsonl", "subagents/" + main + ".jsonl", "tool-results/nested/" + other + ".jsonl"}},
		{"subagents", []string{"subagents/" + main + ".jsonl"}},
		{"memory", []string{"memory/" + other + ".jsonl"}},
		{"tool-results", []string{"tool-results/nested/" + other + ".jsonl"}},
		// A session's own directory holds no main transcript, even
		// named as the root.
		{"subagents/" + main, nil},
	} {
		paths, err := FindSessions(filepath.Join(root, filepath.FromSlash(tc.root)))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, p := range paths {
			rel, _ := filepath.Rel(root, p)
			got = append(got, filepath.ToSlash(rel))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FindSessions(%q) = %v, want %v", tc.root, got, tc.want)
		}
	}

	// The fixtures: the subagents fixture's main transcript is found
	// from the directory above it, and no agent transcript is.
	paths, err := FindSessions(filepath.Join("convert", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range paths {
		if strings.HasPrefix(filepath.Base(p), "agent-") {
			t.Errorf("an agent transcript was found: %s", p)
		}
		found = found || filepath.Base(filepath.Dir(p)) == "subagents"
	}
	if !found {
		t.Errorf("the subagents fixture's transcript was not found under convert/testdata: %v", paths)
	}
}
