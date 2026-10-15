package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	famParent = "01990000-0000-7000-8000-0000000000f0"
	famPlain  = "01990000-0000-7000-8000-0000000000f1"
	famFork   = "01990000-0000-7000-8000-0000000000f2"
	famGrand  = "01990000-0000-7000-8000-0000000000f3"
)

// TestIndex indexes the family fixture: a parent, a child spawned
// plainly that spawned one of its own, and a forked child.
func TestIndex(t *testing.T) {
	x, err := IndexDir(filepath.Join("convert", "testdata", "family", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	for parent, want := range map[string][]string{
		famParent: {famPlain, famFork},
		famPlain:  {famGrand},
		famGrand:  nil,
	} {
		if got := x.Children(parent); !reflect.DeepEqual(got, want) {
			t.Errorf("Children(%s) = %v, want %v", parent, got, want)
		}
	}
	rs := x.Rollouts(famFork)
	if len(rs) != 1 || rs[0].Parent != famParent || !strings.HasSuffix(rs[0].Path, famFork+".jsonl") {
		t.Errorf("Rollouts(fork) = %+v", rs)
	}
	var nilIndex *Index
	if nilIndex.Children(famParent) != nil || nilIndex.Rollouts(famParent) != nil {
		t.Error("a nil index holds threads")
	}
}

func TestSessionsRoot(t *testing.T) {
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	for _, tc := range []struct {
		in, root string
		tree     bool
	}{
		{"home/.codex/sessions/2026/10/02/rollout-x.jsonl", "home/.codex/sessions", true},
		{"home/.codex/sessions/rollout-x.jsonl", "home/.codex/sessions", true},
		{"fixture/sessions/a/10/02/rollout-x.jsonl", "fixture/sessions/a/10/02", false},
		{"Downloads/rollout-x.jsonl", "Downloads", false},
	} {
		root, tree, err := SessionsRoot(filepath.Join(dir, filepath.FromSlash(tc.in)))
		if err != nil || root != filepath.Join(dir, filepath.FromSlash(tc.root)) || tree != tc.tree {
			t.Errorf("SessionsRoot(%s) = %s, %v, %v", tc.in, root, tree, err)
		}
	}
	// A sessions tree reached through a symbolic link is walked.
	real := filepath.Join(dir, "real", "sessions", "2026", "10", "02")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("convert", "testdata", "family", "sessions", "2026", "10", "02", "rollout-2026-10-02T10-00-00-"+famParent+".jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, filepath.Base(src)), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real", "sessions"), filepath.Join(dir, "sessions")); err != nil {
		t.Skip(err)
	}
	x, err := IndexFor(filepath.Join(dir, "sessions", "2026", "10", "02", filepath.Base(src)))
	if err != nil || len(x.Rollouts(famParent)) != 1 {
		t.Errorf("through a link: %v, %v", x, err)
	}
}

// TestIndexSkips checks an unreadable directory and a rollout whose
// first line is no session meta are listed, not fatal, and that a
// fork's copied meta does not stand in for a damaged first line.
func TestIndexSkips(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "sessions", "2026", "10", "02")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	copied := `{"timestamp":"t","type":"session_meta","payload":{"id":"` + famParent + `"}}`
	if err := os.WriteFile(filepath.Join(day, "rollout-a.jsonl"), []byte("{damaged\n"+copied+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "sessions", "2026", "10", "03")
	if err := os.MkdirAll(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	x, err := IndexDir(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Rollouts(famParent)) != 0 {
		t.Error("a fork's copied meta was indexed for a damaged first line")
	}
	if len(x.Skipped) < 1 {
		t.Errorf("skipped = %v", x.Skipped)
	}
}

// TestAncestors walks up, and stops at a self-parent and a cycle.
func TestAncestors(t *testing.T) {
	x := &Index{threads: map[string][]Thread{
		"g": {{ID: "g", Parent: "c"}}, "c": {{ID: "c", Parent: "p"}}, "p": {{ID: "p"}},
		"s": {{ID: "s", Parent: "s"}}, "a": {{ID: "a", Parent: "b"}}, "b": {{ID: "b", Parent: "a"}},
	}}
	for id, want := range map[string][]string{"g": {"c", "p"}, "p": nil, "s": nil, "a": {"b"}} {
		if got := x.Ancestors(id); !reflect.DeepEqual(got, want) {
			t.Errorf("Ancestors(%s) = %v, want %v", id, got, want)
		}
	}
}

func TestReadMeta(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	m, err := ReadMeta(write("a.jsonl", `{"timestamp":"t","type":"session_meta","payload":{"id":"x","source":{"subagent":{"thread_spawn":{"parent_thread_id":"p","depth":1,"agent_path":"/root/x"}}}}}`+"\n"+`not read`))
	if err != nil || m == nil || m.ID != "x" || m.ParentThread() != "p" || m.SpawnPath() != "/root/x" {
		t.Errorf("ReadMeta = %+v, %v", m, err)
	}
	// The bare layout of 2025: the meta is the first line, bare.
	if m, err := ReadMeta(write("bare.jsonl", `{"id":"y","timestamp":"t"}`+"\n")); err != nil || m == nil || m.ID != "y" {
		t.Errorf("ReadMeta of the bare layout = %+v, %v", m, err)
	}
	// Only the first line is read: a meta after a damaged one is a
	// fork's copy of an ancestor's.
	if m, err := ReadMeta(write("late.jsonl", "not json\n"+`{"timestamp":"t","type":"session_meta","payload":{"id":"z"}}`+"\n")); m != nil || err != nil {
		t.Errorf("ReadMeta past line 1 = %+v, %v", m, err)
	}
	big := `{"timestamp":"t","type":"session_meta","payload":{"id":"x","base_instructions":{"text":"` + strings.Repeat("a", MaxMetaSize) + `"}}}`
	if _, err := ReadMeta(write("big.jsonl", big)); err != ErrMetaTooLong {
		t.Errorf("ReadMeta of an oversize meta = %v", err)
	}
	if m, err := ReadMeta(write("b.jsonl", `{"timestamp":"t","type":"response_item","payload":{"type":"message"}}`+"\n")); m != nil || err != nil {
		t.Errorf("ReadMeta with no meta = %+v, %v", m, err)
	}
	if _, err := ReadMeta(write("c.jsonl", "\x28\xb5\x2f\xfd....")); err != ErrCompressed {
		t.Errorf("ReadMeta of zstd = %v", err)
	}
}

// TestSpawn reads the thread a spawn started from each record Codex
// writes for one.
func TestSpawn(t *testing.T) {
	read := func(line string) *Line {
		f, err := Read(strings.NewReader(line + "\n"))
		if err != nil || len(f.Lines) != 1 {
			t.Fatalf("read %s: %v", line, err)
		}
		return f.Lines[0]
	}
	for name, tc := range map[string]struct {
		line         string
		call, thread string
	}{
		"v2 legacy event":          {`{"timestamp":"t","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"c1","agent_thread_id":"th","agent_path":"/root/a","kind":"started"}}`, "c1", "th"},
		"v2 paginated item":        {`{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"SubAgentActivity","id":"c2","kind":"started","agent_thread_id":"th","agent_path":"/root/a"}}}`, "c2", "th"},
		"v1 paginated item":        {`{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CollabAgentToolCall","id":"c3","tool":"spawn_agent","status":"completed","sender_thread_id":"p","receiver_thread_ids":["th"]}}}`, "c3", "th"},
		"activity of another kind": {`{"timestamp":"t","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"c4","agent_thread_id":"th","kind":"interacted"}}`, "", ""},
		"0.130 extended spawn end": {`{"timestamp":"t","type":"event_msg","payload":{"type":"collab_agent_spawn_end","call_id":"c6","sender_thread_id":"p","new_thread_id":"th","prompt":"","model":"m","reasoning_effort":"low","status":"pending_init"}}`, "c6", "th"},
		"a failed spawn's end":     {`{"timestamp":"t","type":"event_msg","payload":{"type":"collab_agent_spawn_end","call_id":"c7","sender_thread_id":"p","new_thread_id":null,"status":"not_found"}}`, "", ""},
		"another collab tool":      {`{"timestamp":"t","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CollabAgentToolCall","id":"c5","tool":"wait","receiver_thread_ids":["th"]}}}`, "", ""},
	} {
		call, thread, ok := read(tc.line).Spawn()
		if call != tc.call || thread != tc.thread || ok != (tc.call != "") {
			t.Errorf("%s: Spawn = %q, %q, %v", name, call, thread, ok)
		}
	}
	for name, tc := range map[string]struct{ line, thread string }{
		"string":        {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"{\"agent_id\":\"th\",\"nickname\":\"N\"}"}}`, "th"},
		"parts":         {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":[{"type":"input_text","text":"{\"agent_id\":\"th\"}"}]}}`, "th"},
		"v2 task name":  {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"{\"task_name\":\"/root/a\"}"}}`, ""},
		"not json":      {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"Agent depth limit reached."}}`, ""},
		"not an output": {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","call_id":"c","name":"spawn_agent","arguments":"{}"}}`, ""},
	} {
		if got := read(tc.line).Item.SpawnedThread(); got != tc.thread {
			t.Errorf("%s: SpawnedThread = %q, want %q", name, got, tc.thread)
		}
	}
	if got := read(`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"{\"task_name\":\"/root/a\",\"nickname\":null}"}}`).Item.SpawnedTask(); got != "/root/a" {
		t.Errorf("SpawnedTask = %q", got)
	}
	for name, tc := range map[string]struct {
		line string
		ok   bool
	}{
		"0.98 collab":    {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{}","call_id":"c"}}`, true},
		"multi_agent_v1": {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","namespace":"multi_agent_v1","arguments":"{}","call_id":"c"}}`, true},
		"an MCP tool":    {`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","namespace":"mcp__swarm","arguments":"{}","call_id":"c"}}`, false},
	} {
		if got := read(tc.line).Item.IsSpawnCall(); got != tc.ok {
			t.Errorf("%s: IsSpawnCall = %v", name, got)
		}
	}
}
