package codex

import (
	"bytes"
	"encoding/json"
	"errors"
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

// TestRoundTrip decodes every fixture line and encodes it again, and
// each typed payload: the result must be the same JSON value, so
// nothing the package does not type is lost and nothing absent is
// invented.
func TestRoundTrip(t *testing.T) {
	files, err := filepath.Glob("convert/testdata/*/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// A fixture's relatives, the rollouts of the threads it spawned.
	relatives, err := filepath.Glob("convert/testdata/*/relatives/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, relatives...)
	if len(files) < 9 {
		t.Fatalf("found %d fixture files", len(files))
	}
	typed := 0
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
			var v any
			switch {
			case l.Meta != nil:
				v = l.Meta
			case l.Item != nil:
				v = l.Item
			case l.Event != nil:
				v = l.Event
			case l.TurnContext != nil:
				v = l.TurnContext
			case l.Compacted != nil:
				v = l.Compacted
			case l.InterAgent != nil:
				v = l.InterAgent
			case l.UsageRecord != nil:
				v = l.UsageRecord
			default:
				continue
			}
			typed++
			got, err = json.Marshal(v)
			if err != nil {
				t.Fatalf("%s:%d payload: %v", f, l.No, err)
			}
			if !sameJSON(t, l.PayloadRaw(), got) {
				t.Errorf("%s:%d payload does not round-trip\nread:  %s\nwrote: %s", f, l.No, l.PayloadRaw(), got)
			}
		}
	}
	if typed < 100 {
		t.Errorf("only %d typed payloads", typed)
	}
}

func TestRead(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		kinds    []string
		legacy   bool
		errors   int
	}{
		{"envelope", `{"timestamp":"t","type":"session_meta","payload":{"id":"a","timestamp":"t","cwd":"/","originator":"o","cli_version":"1"}}` + "\n" +
			`{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user","content":[]}}`,
			[]string{TypeSessionMeta, TypeResponseItem}, false, 0},
		{"legacy", `{"id":"a","timestamp":"t","instructions":null}` + "\n" + `{"record_type":"state"}` + "\n" +
			`{"type":"message","role":"user","content":[]}`,
			[]string{TypeSessionMeta, TypeState, TypeResponseItem}, true, 0},
		{"blank and broken", "\n\n" + `{"timestamp":"t","type":"event_msg","payload":{"type":"turn_started","turn_id":"x"}}` + "\nnot json\n[1]\nnull\n",
			[]string{TypeEventMsg}, false, 3},
		{"nul", "{\"timestamp\":\"t\",\"type\":\"x\",\"payload\":\"a\x00\"}", nil, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Read(strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			var kinds []string
			for _, l := range f.Lines {
				kinds = append(kinds, l.Kind)
			}
			if !reflect.DeepEqual(kinds, tc.kinds) || f.Legacy != tc.legacy || len(f.Errors) != tc.errors {
				t.Errorf("kinds %v legacy %v errors %v", kinds, f.Legacy, f.Errors)
			}
		})
	}
}

func TestCompressed(t *testing.T) {
	_, err := Read(bytes.NewReader([]byte{0x28, 0xB5, 0x2F, 0xFD, 0, 0}))
	if !errors.Is(err, ErrCompressed) {
		t.Errorf("err = %v", err)
	}
	if _, err := ReadFile("x.jsonl.zst"); !errors.Is(err, ErrCompressed) {
		t.Errorf("err = %v", err)
	}
}

func TestSessionMeta(t *testing.T) {
	var m SessionMeta
	in := `{"id":"c","source":{"subagent":{"thread_spawn":{"parent_thread_id":"p","depth":2}}}}`
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	kind, spawn := m.SubAgent()
	if kind != "thread_spawn" || spawn == nil || spawn.ParentThreadID != "p" || spawn.Depth != 2 || m.SourceKind() != "subagent" {
		t.Errorf("sub-agent = %q %+v, source %q", kind, spawn, m.SourceKind())
	}
	m = SessionMeta{}
	if err := json.Unmarshal([]byte(`{"id":"c","source":{"subagent":"review"}}`), &m); err != nil {
		t.Fatal(err)
	}
	if kind, spawn := m.SubAgent(); kind != "review" || spawn != nil {
		t.Errorf("review = %q %+v", kind, spawn)
	}
	m = SessionMeta{}
	if err := json.Unmarshal([]byte(`{"id":"c","source":"cli"}`), &m); err != nil {
		t.Fatal(err)
	}
	if kind, _ := m.SubAgent(); kind != "" || m.SourceKind() != "cli" {
		t.Errorf("cli = %q %q", kind, m.SourceKind())
	}
}

func TestIDFromFileName(t *testing.T) {
	for in, want := range map[string]string{
		"rollout-2026-10-01T10-00-00-01990000-0000-7000-8000-000000000001.jsonl":                                      "01990000-0000-7000-8000-000000000001",
		"rollout-2026-10-01T10-00-00-01990000-0000-7000-8000-000000000001_01990000-0000-7000-8000-000000000002.jsonl": "01990000-0000-7000-8000-000000000001",
		"rollout-2026-10-01T10-00-00-01990000-0000-7000-8000-000000000001.jsonl.zst":                                  "01990000-0000-7000-8000-000000000001",
		"other.jsonl": "",
	} {
		if got := IDFromFileName(in); got != want {
			t.Errorf("IDFromFileName(%q) = %q", in, got)
		}
	}
}
