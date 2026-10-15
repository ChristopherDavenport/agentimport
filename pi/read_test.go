package pi

import (
	"encoding/json"
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
	for _, pattern := range []string{"convert/testdata/*/*.jsonl", "convert/testdata/*/*/*.jsonl"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 10 {
		t.Fatalf("found %d fixture files", len(files))
	}
	for _, f := range files {
		file, err := ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range file.Lines {
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

func TestSectionsKeepOrder(t *testing.T) {
	in := `{"role":"system","content":"","sections":{"z":"last","a":null,"m":"middle"}}`
	var m Message
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	want := Sections{{Name: "z", Text: "last"}, {Name: "a", Remove: true}, {Name: "m", Text: "middle"}}
	if !reflect.DeepEqual(m.Sections, want) {
		t.Errorf("sections = %+v", m.Sections)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"sections":{"z":"last","a":null,"m":"middle"}`) {
		t.Errorf("sections written out of order: %s", out)
	}
}

func TestReadTolerates(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/w"}`,
		`{"type":"message","id":"a","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"hi"}}`,
		strings.Repeat("\x00", 10),
		"",
		`{"type":"message","id":"b","parentId":"a","message":{"role":"assis`,
		`{"type":"message","id":"c","parentId":"a","timestamp":"2026-01-01T00:00:02Z","message":7}`,
		"\x00" + `{"type":"label","id":"d","parentId":"c","targetId":"a"}`,
	}, "\n")
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if f.LineCount() != 6 || len(f.Lines) != 4 || len(f.Errors) != 2 {
		t.Fatalf("lines %d, read %d, errors %v", f.LineCount(), len(f.Lines), f.Errors)
	}
	if f.Errors[0].No != 3 || f.Errors[1].No != 5 {
		t.Errorf("errors at %d and %d; want 3 and 5", f.Errors[0].No, f.Errors[1].No)
	}
	if !reflect.DeepEqual(f.NULLines, []int{3, 7}) {
		t.Errorf("NUL lines = %v", f.NULLines)
	}
	// A line that does not decode keeps its envelope, so it stays in
	// the tree.
	if l := f.Lines[2]; l.Err == nil || l.ID != "c" || l.ParentID != "a" {
		t.Errorf("undecodable line = %+v", l)
	}
	if f.Header().ID != "s" || f.Version() != 3 || len(f.Entries()) != 3 {
		t.Errorf("header %+v, version %d, entries %d", f.Header(), f.Version(), len(f.Entries()))
	}
}

func TestMigrate(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"session","id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/w"}`,
		`{"type":"message","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"one"}}`,
		`{"type":"message","timestamp":"2026-01-01T00:00:02Z","message":{"role":"hookMessage","customType":"h","content":"x","display":true}}`,
		`{"type":"compaction","timestamp":"2026-01-01T00:00:03Z","summary":"s","firstKeptEntryIndex":2,"tokensBefore":1}`,
	}, "\n")
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	m := Migrate(f)
	if m != (Migration{From: 1, To: 3, IDs: 3, FirstKept: 1, HookMessages: 1}) {
		t.Errorf("migration = %+v", m)
	}
	e := f.Entries()
	if e[0].ID != "v1-2" || e[0].ParentID != "" || e[1].ParentID != "v1-2" || !e[1].Synthetic {
		t.Errorf("ids: %+v %+v", e[0], e[1])
	}
	if e[1].Message.Role != RoleCustom || e[2].FirstKeptEntryID != "v1-3" {
		t.Errorf("role %q, first kept %q", e[1].Message.Role, e[2].FirstKeptEntryID)
	}
}

func TestBuildTree(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/w"}`,
		`{"type":"message","id":"b","parentId":"a","message":{"role":"user","content":"child first"}}`,
		`{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"root"}}`,
		`{"type":"message","id":"c","parentId":"missing","message":{"role":"user","content":"dangling"}}`,
		`{"type":"message","id":"d","parentId":"e","message":{"role":"user","content":"cycle"}}`,
		`{"type":"message","id":"e","parentId":"d","message":{"role":"user","content":"cycle"}}`,
		`{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"repeat"}}`,
		`{"type":"label","id":"f","parentId":"a","targetId":"b"}`,
	}, "\n")
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildTree(f)
	var roots []int
	for _, r := range tr.Roots {
		roots = append(roots, r.Line.No)
	}
	// The child names the later "a" (line 7), as pi indexes by id.
	if !reflect.DeepEqual(roots, []int{3, 4, 6, 7}) {
		t.Errorf("roots at lines %v", roots)
	}
	if tr.Node("a").Line.No != 7 || len(tr.Node("a").Children) != 2 {
		t.Errorf("a = line %d with %d children", tr.Node("a").Line.No, len(tr.Node("a").Children))
	}
	if len(tr.Dangling) != 1 || len(tr.Cycles) != 1 || len(tr.Duplicates) != 1 {
		t.Errorf("dangling %d, cycles %d, duplicates %d", len(tr.Dangling), len(tr.Cycles), len(tr.Duplicates))
	}
	if tr.Leaf.Line.ID != "f" {
		t.Errorf("leaf = %s", tr.Leaf.Line.ID)
	}
}

func TestIDFromFileName(t *testing.T) {
	for in, want := range map[string]string{
		"/x/2026-09-01T10-00-00-000Z_01a0beef-0000-7000-8000-00000000cafe.jsonl": "01a0beef-0000-7000-8000-00000000cafe",
		"session.jsonl": "",
		"a_b.json":      "",
	} {
		if got := IDFromFileName(in); got != want {
			t.Errorf("IDFromFileName(%q) = %q", in, got)
		}
	}
}
