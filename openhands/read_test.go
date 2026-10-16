package openhands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fixtureDirs lists every conversation directory of the converter's
// fixtures, sub-agents included.
func fixtureDirs(t *testing.T) []string {
	t.Helper()
	roots, err := FindSessions("convert/testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) == 0 {
		t.Fatal("no fixture conversations")
	}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		out = append(out, dir)
		if depth > 4 {
			return
		}
		subs, _ := subagentDirs(filepath.Join(dir, DirSubagents))
		for _, s := range subs {
			walk(filepath.Join(dir, DirSubagents, s), depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

// sameJSON reports whether two JSON texts hold the same value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// TestRoundTrip decodes every event of every fixture as its kind, and
// every state and metadata file, and encodes each again: the same JSON
// value.
func TestRoundTrip(t *testing.T) {
	for _, dir := range fixtureDirs(t) {
		c, err := ReadConversation(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		check := func(what string, raw []byte, v any) {
			t.Helper()
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("%s %s: %v", dir, what, err)
			}
			if !sameJSON(t, raw, out) {
				t.Errorf("%s %s does not round-trip:\n%s\n%s", dir, what, raw, out)
			}
		}
		if c.State != nil {
			check(FileBaseState, c.StateRaw, c.State)
		}
		if c.Meta != nil {
			check(FileMeta, c.MetaRaw, c.Meta)
		}
		for _, e := range c.Events {
			if e.Err != nil {
				continue
			}
			var v any
			var err error
			switch e.Kind {
			case KindSystemPrompt:
				v, err = e.SystemPrompt()
			case KindMessage:
				v, err = e.Message()
			case KindAction:
				var a *ActionEvent
				a, err = e.Action()
				v = a
				if act := ParseAction(a.Action); act != nil {
					check(e.File+" action", a.Action, act)
				}
			case KindObservation:
				v, err = e.Observation()
			case KindUserReject:
				v, err = e.UserReject()
			case KindAgentError:
				v, err = e.AgentError()
			case KindCondensation:
				v, err = e.Condensation()
			default:
				continue
			}
			if err != nil {
				t.Errorf("%s %s: %v", dir, e.File, err)
				continue
			}
			check(e.File, e.Raw, v)
		}
	}
}

// TestUnits reads every fixture and checks its units add up: every file
// of the events directory but the sidecars, the state, the metadata and
// the other files.
func TestUnits(t *testing.T) {
	for _, dir := range fixtureDirs(t) {
		c, err := ReadConversation(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		entries, err := os.ReadDir(filepath.Join(dir, DirEvents))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range entries {
			if !e.IsDir() && e.Name()[0] != '.' {
				n++
			}
		}
		if len(c.Files) != n {
			t.Errorf("%s: %d event files read, %d in the directory", dir, len(c.Files), n)
		}
		for i, e := range c.Events {
			if e.Index != i {
				t.Errorf("%s: event %d has index %d", dir, i, e.Index)
			}
		}
	}
}

// TestSequence checks the reader takes events as OpenHands does: from
// index 0 to the first gap, the last file of a repeated index, and a
// malformed file in the sequence as an event that did not read.
func TestSequence(t *testing.T) {
	ev := func(id string) []byte {
		return []byte(`{"kind":"PauseEvent","id":"` + id + `","timestamp":"2026-01-01T00:00:00","source":"user"}`)
	}
	c := Parse("x", map[string][]byte{
		"events/event-00000-aaaaaaaa.json": ev("aaaaaaaa"),
		"events/event-00001-bbbbbbbb.json": ev("bbbbbbbb"),
		"events/event-00001-cccccccc.json": ev("cccccccc"),
		"events/event-00002-dddddddd.json": []byte(""),
		"events/event-00004-eeeeeeee.json": ev("eeeeeeee"),
		"events/notes.txt":                 nil,
	})
	var ids []string
	for _, e := range c.Events {
		ids = append(ids, e.ID)
	}
	if want := []string{"aaaaaaaa", "cccccccc", "dddddddd"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("events %v, want %v", ids, want)
	}
	if c.Events[2].Err == nil {
		t.Error("an empty event file read")
	}
	status := map[string]FileStatus{}
	for _, f := range c.Files {
		status[f.Name] = f.Status
	}
	want := map[string]FileStatus{
		"event-00000-aaaaaaaa.json": StatusEvent,
		"event-00001-bbbbbbbb.json": StatusDuplicate,
		"event-00001-cccccccc.json": StatusEvent,
		"event-00002-dddddddd.json": StatusMalformed,
		"event-00004-eeeeeeee.json": StatusGap,
		"notes.txt":                 StatusBadName,
	}
	if !reflect.DeepEqual(status, want) {
		t.Errorf("statuses %v, want %v", status, want)
	}
	if c.Units() != 6 {
		t.Errorf("%d units, want 6", c.Units())
	}
}

// TestParseTime reads naive timestamps in the zone given and zoned ones
// in their own.
func TestParseTime(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	cases := []struct {
		in    string
		want  time.Time
		naive bool
		ok    bool
	}{
		{"2026-05-01T10:00:00.000001", time.Date(2026, 5, 1, 9, 0, 0, 1000, time.UTC), true, true},
		{"2026-05-01T10:00:00", time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC), true, true},
		{"2026-05-01T10:00:00+00:00", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC), false, true},
		{"2026-05-01T10:00:00.5Z", time.Date(2026, 5, 1, 10, 0, 0, 5e8, time.UTC), false, true},
		{"2026-05-01T10:00:00-0230", time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC), false, true},
		{"yesterday", time.Time{}, false, false},
		{"2026-13-01T10:00:00", time.Time{}, false, false},
	}
	for _, tc := range cases {
		got, naive, ok := ParseTime(tc.in, loc)
		if ok != tc.ok || naive != tc.naive || !got.Equal(tc.want) {
			t.Errorf("ParseTime(%q) = %s %v %v, want %s %v %v", tc.in, got, naive, ok, tc.want, tc.naive, tc.ok)
		}
	}
}

// TestFindSessions finds the fixtures' conversations, not their
// sub-agents, and the conversation of a file in one.
func TestFindSessions(t *testing.T) {
	dirs, err := FindSessions("convert/testdata/subagent-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("found %v, want the parent alone", dirs)
	}
	ev, err := filepath.Glob(filepath.Join(dirs[0], DirEvents, "event-00000-*.json"))
	if err != nil || len(ev) != 1 {
		t.Fatalf("glob: %v %v", ev, err)
	}
	got, err := FindSessions(ev[0])
	if err != nil || len(got) != 1 || got[0] != dirs[0] {
		t.Errorf("FindSessions(%s) = %v, %v", ev[0], got, err)
	}
	if !IsConversationFile(ev[0]) || IsConversationFile(filepath.Join(dirs[0], "notes.txt")) {
		t.Error("IsConversationFile is wrong")
	}
}

// TestSubagentLinkAndDetection lists a sub-agent whose directory is a
// symbolic link, reports a subagents directory that cannot be listed,
// and takes a base_state.json for OpenHands' only when it holds the
// state's members.
func TestSubagentLinkAndDetection(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := `{"id":"x","agent":{"kind":"Agent"},"persistence_dir":"p"}`
	parent := filepath.Join(root, "parent")
	write(filepath.Join(parent, FileBaseState), state)
	other := filepath.Join(root, "elsewhere")
	write(filepath.Join(other, FileBaseState), state)
	if err := os.MkdirAll(filepath.Join(parent, DirSubagents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(parent, DirSubagents, "linked")); err != nil {
		t.Fatal(err)
	}
	c, err := ReadConversation(parent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Subagents, []string{"linked"}) || c.SubagentsErr != nil {
		t.Errorf("sub-agents %v, %v; want the linked one", c.Subagents, c.SubagentsErr)
	}
	if err := os.Chmod(filepath.Join(parent, DirSubagents), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(parent, DirSubagents), 0o755)
	if _, err := os.ReadDir(filepath.Join(parent, DirSubagents)); err == nil {
		t.Skip("the directory is readable without permission (running as root?)")
	}
	if c, err = ReadConversation(parent); err != nil || c.SubagentsErr == nil {
		t.Errorf("an unreadable subagents directory: %v, %v", c.SubagentsErr, err)
	}

	foreign := filepath.Join(root, "foreign", FileBaseState)
	write(foreign, `{"version":2}`)
	if IsConversationFile(foreign) || IsConversationDir(filepath.Dir(foreign)) {
		t.Error("another tool's base_state.json is taken for OpenHands'")
	}
	if !IsConversationFile(filepath.Join(parent, FileBaseState)) {
		t.Error("OpenHands' base_state.json is not recognised")
	}
}
