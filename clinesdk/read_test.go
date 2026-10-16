package clinesdk

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fixtureSessions lists every session directory of the converter's
// fixtures, by its main file.
func fixtureSessions(t *testing.T) []string {
	t.Helper()
	paths, err := FindSessions("convert/testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 10 {
		t.Fatalf("found %d fixture sessions", len(paths))
	}
	return paths
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

// TestRoundTrip reads every fixture file and writes it again: the same
// JSON value, every message, envelope and manifest.
func TestRoundTrip(t *testing.T) {
	for _, p := range fixtureSessions(t) {
		s, err := ReadSession(p)
		if err != nil {
			t.Fatal(err)
		}
		check := func(name string, v any) {
			data, err := os.ReadFile(filepath.Join(s.Dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) {
				return
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !sameJSON(t, data, out) {
				t.Errorf("%s/%s does not write back as read:\n%s", s.ID, name, out)
			}
		}
		if s.Manifest != nil {
			check(s.ManifestName(), s.Manifest)
		}
		if s.Messages != nil {
			check(s.Messages.Name, s.Messages)
		}
		for _, a := range s.Agents {
			check(a.Name, a)
		}
		if c := s.Compaction; c != nil && c.Err == nil {
			check(c.Name, c)
		}
	}
}

func TestReadSession(t *testing.T) {
	s, err := ReadSession("convert/testdata/s5-malformed/sessions/1759345200000_bad01")
	if err != nil {
		t.Fatal(err)
	}
	if s.HasManifest || s.Messages == nil || s.Compaction == nil || s.Compaction.Err == nil {
		t.Fatalf("read %+v", s)
	}
	if got := s.Others; !reflect.DeepEqual(got, []string{"notes.txt"}) {
		t.Errorf("others %v", got)
	}
	if got := s.Dirs; !reflect.DeepEqual(got, []string{"checkpoints"}) {
		t.Errorf("dirs %v", got)
	}
	ms := s.Messages.Messages
	if ms[0].Err == nil || ms[1].Err == nil || ms[2].Content.Err == nil {
		t.Errorf("malformed elements read as messages: %v %v %v", ms[0].Err, ms[1].Err, ms[2].Content.Err)
	}
	if n, ok := s.Messages.VersionNumber(); !ok || n != 2 {
		t.Errorf("version %d %v", n, ok)
	}
	if _, err := ReadSession(t.TempDir()); err == nil {
		t.Error("an empty directory read as a session")
	}
}

func TestParseCutShort(t *testing.T) {
	f := ParseMessagesFile("x.messages.json", []byte(`{"version":1,"messages":[{"role":"user","content":"a"},{"role":"assistant","conte`))
	if f.Err == nil || len(f.Messages) != 1 || !f.HasMessages {
		t.Errorf("cut file: err %v, %d messages", f.Err, len(f.Messages))
	}
	f = ParseMessagesFile("x.messages.json", []byte(`{"messages":[{"role":"user","content":"a"}],"messages":"x"}`))
	if f.HasMessages || len(f.Messages) != 0 {
		t.Errorf("a repeated messages member: the last stands, got %d messages", len(f.Messages))
	}
	f = ParseMessagesFile("x.messages.json", []byte(`[{"role":"user","content":"a"}]`))
	if !f.Bare || len(f.Messages) != 1 {
		t.Errorf("bare file: %+v", f)
	}
	for _, in := range []string{``, `7`, `nul`, `{"messages":[1,2] } x`} {
		if f := ParseMessagesFile("x", []byte(in)); f.Err == nil {
			t.Errorf("%q read whole", in)
		}
	}
}

func TestFindSessions(t *testing.T) {
	paths := fixtureSessions(t)
	for _, p := range paths {
		if !IsSessionFile(p) {
			t.Errorf("%s is not a session file", p)
		}
	}
	got, err := FindSessions("convert/testdata/s3-subagents/sessions/1759327200000_sub01/agent_7.messages.json")
	if err != nil || len(got) != 1 || !strings.HasSuffix(got[0], "1759327200000_sub01.messages.json") {
		t.Errorf("a child's file finds %v, %v", got, err)
	}
	if IsSessionFile("convert/testdata/s5-malformed/sessions/1759345200000_bad01/notes.txt") {
		t.Error("notes.txt is a session file")
	}
}

func TestSubSessionIDs(t *testing.T) {
	if got := MakeSubSessionID("17_a b", "agent/7"); got != "17_a_b__agent_7" {
		t.Errorf("got %s", got)
	}
	if got := MakeSubSessionID(strings.Repeat("r", 200), "a"); len(got) != 180 {
		t.Errorf("long ID is %d long", len(got))
	}
	if root, agent, ok := ParseSubSessionID("17_a__agent_7"); !ok || root != "17_a" || agent != "agent_7" {
		t.Errorf("got %s %s %v", root, agent, ok)
	}
	if _, _, ok := ParseSubSessionID("17__teamtask__rev__x1"); ok {
		t.Error("a team task's ID read as a sub-agent's")
	}
	tt, ok := ParseTeamTaskSubSessionID("17__teamtask__rev__iewer__x1")
	if !ok || tt.Root != "17" || tt.Agent != "rev__iewer" || tt.Task != "x1" {
		t.Errorf("got %+v %v", tt, ok)
	}
	if got := FileStem("17__teamtask__rev__x1"); got != "rev__x1" {
		t.Errorf("stem %s", got)
	}
}

// The wants were printed by node 25 running Cline's functions,
// transcribed.
func TestUserInput(t *testing.T) {
	for _, tc := range []struct{ in, norm, display string }{
		{`<user_input mode="act">Fix it</user_input>`, "Fix it", "Fix it"},
		{"  plain text  ", "plain text", "plain text"},
		{`<user_input mode="act">a</user_input> and <user_input mode="plan">b</user_input>`, "a and b", "a and b"},
		{`<user_input mode="act">unclosed`, "unclosed", "unclosed"},
		{`<user_command slash="team">Spawn a team of agents for the following task: ship it</user_command>`, "Spawn a team of agents for the following task: ship it", "/team ship it"},
		{`<user_command slash="Team">spawn a team of agents for the following task:</user_command>`, "spawn a team of agents for the following task:", "/team"},
		{`<user_command slash="review">check <user_input mode="act">x</user_input></user_command>`, "check x", "/review check x"},
		{`<USER_COMMAND SLASH="x"> y </USER_COMMAND>`, `<USER_COMMAND SLASH="x"> y </USER_COMMAND>`, `/x <USER_COMMAND SLASH="x"> y </USER_COMMAND>`},
		{"x <mode_notice>n</mode_notice> y <mode_notice>m", "x <mode_notice>n</mode_notice> y <mode_notice>m", "x  y <mode_notice>m"},
		{"\u00a0\ufeff spaced \u2028", "spaced", "spaced"},
		{"", "", ""},
		{"<user_inputs>keep</user_inputs>", "keep</user_inputs>", "keep</user_inputs>"},
	} {
		if got := NormalizeUserInput(tc.in); got != tc.norm {
			t.Errorf("NormalizeUserInput(%q) = %q, want %q", tc.in, got, tc.norm)
		}
		if got := FormatDisplayUserInput(tc.in); got != tc.display {
			t.Errorf("FormatDisplayUserInput(%q) = %q, want %q", tc.in, got, tc.display)
		}
	}
}

// The wants were printed by node 25 running Cline's sourcePrefixHash,
// transcribed, over testdata/hash.messages.json.
func TestSourcePrefixHash(t *testing.T) {
	data, err := os.ReadFile("testdata/hash.messages.json")
	if err != nil {
		t.Fatal(err)
	}
	f := ParseMessagesFile("hash.messages.json", data)
	source := CanonicalSource(f.Messages)
	for n, want := range []string{
		"sha256:574986b05fe9c534acf733180fff5135cc0b044ecbf9724a8fb0549fdeb90ab5",
		"sha256:4079c2c4f88f972d149219768b83c3171df12b78118424cc349118f8d7cce973",
		"sha256:3da7d954f864acb1374ccb1c5039049527f966ea621ba07cc8d13e23db840787",
		"sha256:27fd918e3bfbb7cc93c80c8e860573c3c305dab1b756dae0ff3a26f128cfe098",
		"sha256:e280dbe862d7f9b00563e5213ef51d17d47392f0e01eb505c1d10bbf92f37809",
		"sha256:e2fca97ccf1d72f0279bdff3f176748e91a49235b322fa0ec061a68e6c4b990c",
	} {
		got, err := SourcePrefixHash(source, n)
		if err != nil || got != want {
			t.Errorf("%d messages: got %s, %v; want %s", n, got, err, want)
		}
	}
	if _, err := SourcePrefixHash(source, 6); !errors.Is(err, ErrLoneSurrogate) {
		t.Errorf("a lone surrogate in user text: %v", err)
	}
	if key, err := BoundaryKey(source[3]); err != nil || key != "id:d" {
		t.Errorf("key %s %v", key, err)
	}
}

func TestProject(t *testing.T) {
	s, err := ReadSession("convert/testdata/s2-compaction/sessions/1759316400000_cmp01")
	if err != nil {
		t.Fatal(err)
	}
	p := s.Compaction.Project(s.Messages.Messages)
	if !p.Applies || p.By != "hash" || p.Count != 6 {
		t.Errorf("projection %+v", p)
	}
	s, err = ReadSession("convert/testdata/s2-stale/sessions/1759320000000_cmp02")
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Compaction.Project(s.Messages.Messages); p.Applies || p.Why == "" {
		t.Errorf("stale projection %+v", p)
	}
	c := ParseCompaction("c", []byte(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":1,"source_last_message_key":"id:a","messages":[{"role":"user","content":"s"}]}`))
	if p := c.Project([]*Message{{Role: RoleUser, ID: "a", Elem: json.RawMessage(`{"id":"a","role":"user","content":"x"}`)}}); !p.Applies || p.By != "boundary" {
		t.Errorf("boundary projection %+v", p)
	}
	for _, bad := range []string{
		`{"version":"1","updated_at":"2026-10-01T10:00:00Z","source_message_count":0,"messages":[]}`,
		`{"version":1,"updated_at":"2026-10-01T10:00:00+01:00","source_message_count":0,"messages":[]}`,
		`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":-1,"messages":[]}`,
		`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":0,"messages":[{"role":"tool","content":"x"}]}`,
		`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":0,"conversation_id":null,"messages":[]}`,
	} {
		if err := ParseCompaction("c", []byte(bad)).Valid(); err == nil {
			t.Errorf("%s is valid", bad)
		}
	}
}

func TestFileIndex(t *testing.T) {
	dir := t.TempDir()
	index := `{"version":1,"sessions":{"r":{"sessionId":"r","source":"cli","startedAt":"2026-10-01T10:00:00Z","status":"completed","isSubagent":false},
"r__a":{"sessionId":"r__a","parentSessionId":"r","parentAgentId":"lead","agentId":"a","isSubagent":true,"prompt":"Do"},"x":{"sessionId":"x"},"bad":7}}`
	if err := os.WriteFile(filepath.Join(dir, FileIndexName), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	ix, err := OpenFileIndex(filepath.Join(dir, FileIndexName))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := ix.Family("r")
	if len(rows) != 2 || rows[1].ParentAgentID != "lead" || rows[1].Prompt != "Do" {
		t.Errorf("rows %+v", rows)
	}
}

// heapAfter is the live heap after f, its result kept alive.
func heapAfter(f func() any) (uint64, any) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v := f()
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc < before.HeapAlloc {
		return 0, v
	}
	return after.HeapAlloc - before.HeapAlloc, v
}

// TestTinyElements reads a 9 MB array of a million tiny elements: the
// messages it holds cost memory in proportion to the file, not a few
// hundred bytes each.
func TestTinyElements(t *testing.T) {
	data := []byte("[" + strings.Repeat(`"element",`, 999_999) + `"element"]`)
	for _, wrap := range []bool{false, true} {
		in := data
		if wrap {
			in = []byte(`{"version":1,"messages":` + string(data) + `}`)
		}
		heap, v := heapAfter(func() any { return ParseMessagesFile("x.messages.json", in) })
		f := v.(*MessagesFile)
		if len(f.Messages) != 1_000_000 {
			t.Fatalf("%d messages", len(f.Messages))
		}
		if limit := uint64(8 * len(in)); heap > limit {
			t.Errorf("a %d-byte file of tiny elements holds %d bytes of heap, over %d", len(in), heap, limit)
		}
		runtime.KeepAlive(f)
	}
	// Past MaxMessages the rest is a fault, as a file cut short is.
	f := ParseMessagesFile("x.messages.json", []byte("["+strings.Repeat("0,", MaxMessages)+"0]"))
	if len(f.Messages) != MaxMessages || f.Err == nil {
		t.Errorf("%d messages, err %v", len(f.Messages), f.Err)
	}
}

// TestManyNotices strips many mode notices in time linear in the text.
func TestManyNotices(t *testing.T) {
	text := func(n int) string {
		return strings.Repeat("word <mode_notice>switched</mode_notice> ", n)
	}
	best := func(s string) time.Duration {
		var d time.Duration
		for i := 0; i < 3; i++ {
			start := time.Now()
			if got := StripModeNotices(s); strings.Contains(got, "mode_notice") {
				t.Fatal("a notice is left")
			}
			if e := time.Since(start); i == 0 || e < d {
				d = e
			}
		}
		return d
	}
	a, b := best(text(5_000)), best(text(20_000))
	if b > 8*a {
		t.Errorf("%v for 5000 notices, %v for 20000: not linear", a, b)
	}
}
