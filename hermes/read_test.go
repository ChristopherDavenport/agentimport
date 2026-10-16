package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestToolCallsRoundTrip decodes tool calls and writes them back as the
// same JSON value, members the type does not declare and members of
// another JSON type included.
func TestToolCallsRoundTrip(t *testing.T) {
	for _, in := range []string{
		`[{"id":"call_1","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"ls\"}"}}]`,
		`[{"id":"c|rs_1","call_id":"c","response_item_id":"fc_1","type":"function","function":{"name":"x","arguments":{"a":1},"strict":true},"extra_content":{"google":{"thought_signature":"sig"}}}]`,
		`[{"id":7,"type":["odd"],"function":"not an object"}]`,
	} {
		calls, bad, err := DecodeToolCalls([]byte(in))
		if err != nil || len(bad) != 0 {
			t.Fatalf("%s: %v, bad %v", in, err, bad)
		}
		out, err := json.Marshal(calls)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(in), &a)
		json.Unmarshal(out, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("round trip:\n in  %s\n out %s", in, out)
		}
	}
	calls, bad, err := DecodeToolCalls([]byte(`[null, 7, {"id":"a"}]`))
	if err != nil || len(calls) != 1 || len(bad) != 2 {
		t.Errorf("calls %v bad %v err %v", calls, bad, err)
	}
	if _, _, err := DecodeToolCalls([]byte(`{oops`)); err == nil {
		t.Error("a column that is not JSON decodes")
	}
}

func TestPairingID(t *testing.T) {
	for _, tc := range []struct {
		call ToolCall
		want string
	}{
		{ToolCall{ID: "call_1"}, "call_1"},
		{ToolCall{ID: "x", CallID: "c"}, "c"},
		{ToolCall{ID: "c|rs_1"}, "c"},
		{ToolCall{ID: " "}, ""},
	} {
		if got := tc.call.PairingID(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.call, got, tc.want)
		}
	}
}

func TestDecodeContent(t *testing.T) {
	for _, tc := range []struct {
		in         any
		text       string
		structured bool
		ok         bool
	}{
		{"plain", "plain", false, true},
		{ContentPrefix + `[{"type":"text","text":"a"}]`, "a", true, true},
		{ContentPrefix + `"\u0000json:literal"`, "\x00json:literal", false, true},
		{ContentPrefix + `{"type":"text","text":"d"}`, "d", true, true},
		{ContentPrefix + `[{broken`, ContentPrefix + `[{broken`, false, false},
		{nil, "", false, true},
		{int64(7), "7", false, true},
	} {
		c, ok := DecodeContent(tc.in, true)
		if ok != tc.ok || c.Structured != tc.structured || c.String() != tc.text {
			t.Errorf("%q: %+v, %v", tc.in, c, ok)
		}
	}
}

func TestDisplayMetadata(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{`{"a":1}`, `{"a":1}`, true},
		{`"{\"a\":1}"`, `{"a":1}`, true},
		{`garbage`, ``, false},
		{`[1]`, ``, false},
	} {
		got, ok := displayMetadata(tc.in)
		if ok != tc.ok || string(got) != tc.want {
			t.Errorf("%s: %s, %v", tc.in, got, ok)
		}
	}
}

func TestClassify(t *testing.T) {
	sess := func(id, parent, source, cfg, key string, started float64) *Session {
		cols := Columns{{"id", id}, {"source", source}, {"started_at", started}}
		if parent != "" {
			cols = append(cols, Column{"parent_session_id", parent})
		}
		if cfg != "" {
			cols = append(cols, Column{"model_config", cfg})
		}
		if key != "" {
			cols = append(cols, Column{"session_key", key})
		}
		return DecodeSessionRow("u", cols)
	}
	ended := func(s *Session, reason string, at float64) *Session {
		s.EndReason = reason
		s.Ended, _ = Epoch(at)
		return s
	}
	p := sess("p", "", "cli", "", "k", 1e9)
	for _, tc := range []struct {
		name   string
		s, p   *Session
		rel    Relation
		marked bool
	}{
		{"root", p, nil, Root, true},
		{"tool", sess("c", "p", "tool", "", "", 1e9), p, Tool, true},
		{"branch marker", sess("c", "p", "cli", `{"_branched_from":"p"}`, "", 1e9), p, Branch, true},
		{"delegate marker", sess("c", "p", "cli", `{"_delegate_from":"p"}`, "", 1e9), p, Delegate, true},
		{"reset marker", sess("c", "p", "cli", `{"_reset_from":"p"}`, "", 1e9), p, Reset, true},
		{"inherited marker", sess("c", "p", "cli", `{"_branched_from":"q"}`, "", 1e9), ended(sess("p", "", "cli", "", "", 1e9), "compression", 1e9), Continuation, true},
		{"compression", sess("c", "p", "cli", "", "", 1e9), ended(sess("p", "", "cli", "", "", 1e9), "compression", 1e9), Continuation, true},
		{"old sub-agent of a compressed parent", sess("c", "p", "subagent", "", "", 1e9), ended(sess("p", "", "cli", "", "", 1e9), "compression", 1e9), Delegate, true},
		{"sub-agent's continuation", sess("c", "p", "subagent", "", "", 1e9), ended(sess("p", "", "subagent", "", "", 1e9), "compression", 1e9), Continuation, true},
		{"legacy branch", sess("c", "p", "cli", "", "", 1e9+10), ended(sess("p", "", "cli", "", "", 1e9), "branched", 1e9+5), Branch, false},
		{"legacy reset", sess("c", "p", "telegram", "", "k", 1e9+10), ended(sess("p", "", "telegram", "", "k", 1e9), "idle", 1e9+5), Reset, false},
		{"reset on another key", sess("c", "p", "telegram", "", "j", 1e9+10), ended(sess("p", "", "telegram", "", "k", 1e9), "idle", 1e9+5), Delegate, false},
		{"parent missing", sess("c", "gone", "cli", "", "", 1e9), nil, Delegate, false},
	} {
		rel, marked := Classify(tc.s, tc.p)
		if rel != tc.rel || marked != tc.marked {
			t.Errorf("%s: %v %v, want %v %v", tc.name, rel, marked, tc.rel, tc.marked)
		}
	}
}

func TestFindSessions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".hermes")
	for _, p := range []string{
		filepath.Join(home, DatabaseName),
		filepath.Join(home, DirProfiles, "work", DatabaseName),
		filepath.Join(home, DirProfiles, "empty", "notes.txt"),
		filepath.Join(root, "elsewhere", DatabaseName),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, DatabaseName), filepath.Join(home, DirProfiles, "work", DatabaseName), filepath.Join(root, "elsewhere", DatabaseName)}
	if len(got) != len(want) {
		t.Fatalf("found %v", got)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("did not find %s in %v", w, got)
		}
	}
	if db, ok := StoreOf(home); !ok || db != filepath.Join(home, DatabaseName) {
		t.Errorf("StoreOf(home) = %s, %v", db, ok)
	}
	if _, ok := StoreOf(filepath.Join(home, DirProfiles)); ok {
		t.Error("the profiles directory is a store")
	}
}

func TestEpoch(t *testing.T) {
	if _, ok := Epoch(-1); ok {
		t.Error("a negative time is in the window")
	}
	if _, ok := Epoch(5e9); ok {
		t.Error("a time past 4.2e9 is in the window")
	}
	if ts, ok := Epoch(1759312801.25); !ok || ts.Nanosecond() != 250000000 {
		t.Errorf("%v %v", ts, ok)
	}
}
