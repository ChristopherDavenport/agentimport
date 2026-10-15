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
	x, err := IndexDir(filepath.Join("convert", "testdata", "family"))
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
	for in, want := range map[string]string{
		"/home/u/.codex/sessions/2026/10/02/rollout-x.jsonl": "/home/u/.codex/sessions",
		"/tmp/fixture/relatives/rollout-x.jsonl":             "/tmp/fixture/relatives",
		"rollout-x.jsonl":                                    ".",
	} {
		if got := SessionsRoot(filepath.FromSlash(in)); got != filepath.FromSlash(want) {
			t.Errorf("SessionsRoot(%s) = %s, want %s", in, got, want)
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
	m, err := ReadMeta(write("a.jsonl", "\n"+`not json`+"\n"+`{"timestamp":"t","type":"session_meta","payload":{"id":"x","source":{"subagent":{"thread_spawn":{"parent_thread_id":"p","depth":1}}}}}`+"\n"))
	if err != nil || m == nil || m.ID != "x" || m.ParentThread() != "p" {
		t.Errorf("ReadMeta = %+v, %v", m, err)
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
}
