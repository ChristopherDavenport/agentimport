package atif

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// documents lists the fixture documents, which the convert package's
// tests convert.
func documents(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("convert/testdata/*/*.json")
	if err != nil || len(matches) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return matches
}

// sameJSON reports whether a and b are the same JSON value, numbers
// compared as written.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	decode := func(data []byte) any {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}

// TestRoundTrip checks that every fixture document decodes and
// re-encodes to the same JSON value: undeclared members, members of
// another type, nulls, number spellings and blocks that are not
// objects included.
func TestRoundTrip(t *testing.T) {
	for _, p := range documents(t) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if f.Cut != nil {
			continue
		}
		out, err := json.Marshal(f.Doc)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !sameJSON(t, data, out) {
			t.Errorf("%s does not re-encode to the same value:\n%s", p, out)
		}
	}
}

func TestBlocks(t *testing.T) {
	f, err := ReadFile("convert/testdata/malformed/trajectory.json")
	if err != nil {
		t.Fatal(err)
	}
	s := f.Doc.Steps
	if s[0].Err == nil || s[1] != nil || s[2].Err != nil {
		t.Errorf("steps 1-3: %v %v %v", s[0].Err, s[1], s[2].Err)
	}
	if parts := s[2].Message.Parts; len(parts) != 3 || parts[0].Err == nil || parts[1].Text != "Hello" || parts[2].Extra["url"] == nil {
		t.Errorf("parts = %+v", parts)
	}
	if s[3].Message.Raw == nil || s[3].ToolCalls[0] != nil || s[3].Observation.Results[0].Err == nil {
		t.Errorf("step 4 = %+v", s[3])
	}
	if s[4].Observation.Err == nil || s[4].Metrics.Err == nil {
		t.Errorf("step 5 observation %v metrics %v", s[4].Observation.Err, s[4].Metrics.Err)
	}
	if got := f.Units(); got != 1+5+1+1+2 {
		t.Errorf("units = %d", got)
	}

	f, err = ReadFile("convert/testdata/violations/trajectory.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Doc.Extra["unexpected_root"] == nil || f.Doc.Agent.Extra["flavour"] == nil {
		t.Errorf("undeclared members are not kept")
	}
	refs := f.Doc.Steps[1].Observation.Results[0].Refs
	if !refs.Object || len(refs.List) != 1 || refs.List[0].SessionID != "lost" {
		t.Errorf("object ref = %+v", refs)
	}
	if !slices.Contains(f.Doc.Steps[1].ToolCalls[1].Mistyped(), "arguments") && string(f.Doc.Steps[1].ToolCalls[1].Arguments) != `"{\"y\":2}"` {
		t.Errorf("string arguments = %s", f.Doc.Steps[1].ToolCalls[1].Arguments)
	}
}

func TestTruncated(t *testing.T) {
	f, err := ReadFile("convert/testdata/truncated/trajectory.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Cut == nil || !f.CutStep || len(f.Doc.Steps) != 2 || !slices.Equal(f.CutMembers, []string{"steps"}) {
		t.Errorf("cut %v step %v steps %d members %v", f.Cut, f.CutStep, len(f.Doc.Steps), f.CutMembers)
	}
	if f.Doc.SessionID != "cut-run" || f.Doc.Agent == nil || f.Doc.Agent.Name != "terminus-2" {
		t.Errorf("root = %+v", f.Doc)
	}
	if f.Units() != 4 {
		t.Errorf("units = %d", f.Units())
	}
	// Cut after the steps: every step reads, none is cut.
	data, err := os.ReadFile("convert/testdata/minimal/trajectory.json")
	if err != nil {
		t.Fatal(err)
	}
	cut := bytes.TrimRight(data, "\n}")
	cut = append(cut, []byte(`, "final_metrics": {"total_st`)...)
	f, err = Parse(cut)
	if err != nil {
		t.Fatal(err)
	}
	if f.Cut == nil || f.CutStep || len(f.Doc.Steps) != 3 || !slices.Equal(f.CutMembers, []string{"final_metrics"}) {
		t.Errorf("cut %v step %v steps %d members %v", f.Cut, f.CutStep, len(f.Doc.Steps), f.CutMembers)
	}
	// Cut right after a comma between steps: no step was cut.
	i := bytes.Index(data, []byte(`{"step_id": 3`))
	f, err = Parse(data[:i])
	if err != nil {
		t.Fatal(err)
	}
	if f.CutStep || len(f.Doc.Steps) != 2 {
		t.Errorf("step %v steps %d", f.CutStep, len(f.Doc.Steps))
	}
}

func TestNotDocument(t *testing.T) {
	for _, in := range []string{"", "  ", "null", "[1]", "x", `"s"`, "12", "[{"} {
		if _, err := Parse([]byte(in)); err != ErrNotDocument {
			t.Errorf("%q: %v", in, err)
		}
	}
	if f, err := Parse([]byte(`{`)); err != nil || f.Cut == nil || len(f.Doc.Steps) != 0 {
		t.Errorf("{: %v %+v", err, f)
	}
}

func TestProbe(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"schema_version": "ATIF-v1.8", "steps": []}`, true},
		{"{\n  \"agent\": {\"name\": \"x\"},\n  \"steps\": [{\"step_id\": 1}],\n  \"schema_version\": \"ATIF-v1.2\"\n}", true},
		{`{"schema_version": "ATIF-v1.8", "steps": [{"step_id": 1`, true},
		{`{"schema_version": "2.0"}`, false},
		{`{"type":"session","format":"agentsession/0.12","id":"x"}`, false},
		{`[{"schema_version": "ATIF-v1.8"}]`, false},
		{`not json`, false},
	} {
		if _, ok := Probe(write("t.json", tc.data)); ok != tc.ok {
			t.Errorf("%s: probe %v, want %v", tc.data, ok, tc.ok)
		}
	}
	if _, ok := Probe(filepath.Join(dir, "missing.json")); ok {
		t.Error("a missing file probes")
	}
}

func TestFindSessions(t *testing.T) {
	paths, err := FindSessions("convert/testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".json") {
			t.Errorf("found %s", p)
		}
	}
	if len(paths) != len(documents(t)) {
		t.Errorf("found %d documents, want %d", len(paths), len(documents(t)))
	}
	if got, err := FindSessions("convert/testdata/minimal/trajectory.json"); err != nil || len(got) != 1 {
		t.Errorf("a file: %v %v", got, err)
	}
	// A directory that cannot be read is skipped.
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile("convert/testdata/minimal/trajectory.json")
	if err := os.WriteFile(filepath.Join(dir, "trajectory.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	if got, err := FindSessions(dir); err != nil || len(got) != 1 {
		t.Errorf("with an unreadable directory: %v %v", got, err)
	}
}

func TestNames(t *testing.T) {
	f, err := ReadFile("convert/testdata/cycle/trajectory.json")
	if err != nil {
		t.Fatal(err)
	}
	got := Names(f.Doc, "d")
	want := []string{filepath.Join("d", "next.json"), filepath.Join("d", "child.json"), filepath.Join("d", "child.json"), filepath.Join("d", "missing.json")}
	if !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestInt(t *testing.T) {
	for _, tc := range []struct {
		in        string
		n         int64
		plain, ok bool
	}{
		{"12", 12, true, true}, {"12.0", 12, false, true}, {`"12"`, 12, false, true}, {"1e3", 1000, false, true},
		{"12.5", 0, false, false}, {"null", 0, false, false}, {"", 0, false, false}, {`"x"`, 0, false, false},
		{"-3", -3, true, true}, {"1e300", 0, false, false},
	} {
		n, plain, ok := Int(json.RawMessage(tc.in))
		if n != tc.n || plain != tc.plain || ok != tc.ok {
			t.Errorf("Int(%s) = %d %v %v", tc.in, n, plain, ok)
		}
	}
}
