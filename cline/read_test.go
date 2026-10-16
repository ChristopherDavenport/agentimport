package cline

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

// fixtureTasks lists every task directory of the converter's fixtures.
func fixtureTasks(t *testing.T) []string {
	t.Helper()
	paths, err := FindSessions("convert/testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixture tasks")
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

// TestRoundTrip decodes every element of every fixture's API history
// and UI messages and encodes it again: the same JSON value.
func TestRoundTrip(t *testing.T) {
	for _, p := range fixtureTasks(t) {
		dir := filepath.Dir(p)
		for _, name := range []string{FileAPI, FileUI, FileLegacy} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			raws, _ := elements(data)
			for i, raw := range raws {
				var out []byte
				if name == FileUI || name == FileLegacy && !legacyIsAPI(data) {
					var m UIMessage
					_ = m.UnmarshalJSON(raw)
					out, err = json.Marshal(m)
				} else {
					var m Message
					_ = m.UnmarshalJSON(raw)
					out, err = json.Marshal(m)
				}
				if err != nil {
					t.Fatalf("%s %s %d: %v", dir, name, i, err)
				}
				if !sameJSON(t, raw, out) {
					t.Errorf("%s %s element %d does not round-trip:\n%s\n%s", dir, name, i, raw, out)
				}
			}
		}
	}
}

// TestReadTask reads every fixture task and checks its units add up.
func TestReadTask(t *testing.T) {
	for _, p := range fixtureTasks(t) {
		task, err := ReadTask(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		n := task.API.Units() + task.UI.Units() + task.Context.Units() + len(task.Others)
		if task.Units() < n {
			t.Errorf("%s: %d units, fewer than its arrays hold", p, task.Units())
		}
	}
}

func TestElements(t *testing.T) {
	for _, tc := range []struct {
		in    string
		n     int
		fault bool
	}{
		{`[]`, 0, false},
		{`[1,null,{"a":1}]`, 3, false},
		{`[{"a":1},{"b":`, 1, true},
		{`{}`, 0, true},
		{``, 0, true},
		{`[1] [2]`, 1, true},
		{`"x"`, 0, true},
	} {
		got, err := elements([]byte(tc.in))
		if len(got) != tc.n || (err != nil) != tc.fault {
			t.Errorf("%q: %d elements, error %v", tc.in, len(got), err)
		}
	}
}

func TestParseContextHistory(t *testing.T) {
	in := `[[1,[0,[[0,[[1760000000000,"text",["notice"],[]],[1760000000001,"text",["later"],[]]]]]]],[2,"bad"],[3,[0,[[0,[7]],["x",[]]]]]]`
	a := ParseContextHistory([]byte(in))
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	var ok, bad int
	for _, u := range a.Elems {
		if u.Err != nil {
			bad++
			continue
		}
		ok++
		if u.Message != 1 || u.Block != 0 || u.Kind != "text" {
			t.Errorf("update %+v", u)
		}
	}
	if ok != 2 || bad != 3 {
		t.Errorf("%d updates, %d bad; want 2 and 3", ok, bad)
	}
	if got := a.Elems[1].NewText(); got != "later" {
		t.Errorf("new text %q", got)
	}
}

func TestParseXMLTools(t *testing.T) {
	text := "I'll look.\n<read_file>\n<path>a.go</path>\n</read_file>\nThen <b>bold</b> and\n<write_to_file>\n<path>b.go</path>\n<content>\nx := \"</content>\"\n</content>\n</write_to_file>\n<execute_command>\n<command>ls"
	got := ParseXMLTools(text)
	if len(got) != 3 {
		t.Fatalf("got %d calls: %+v", len(got), got)
	}
	if got[0].Name != "read_file" || got[0].Params["path"] != "a.go" || got[0].Partial {
		t.Errorf("first call %+v", got[0])
	}
	if c := got[1].Params["content"]; c != "x := \"</content>\"" {
		t.Errorf("content %q", c)
	}
	if !got[2].Partial || got[2].Params["command"] != "ls" {
		t.Errorf("partial call %+v", got[2])
	}
	if names := Names(got); !slices.Equal(names, []string{"execute_command", "read_file", "write_to_file"}) {
		t.Errorf("names %v", names)
	}
	if ParseXMLTools("<task>\nx\n</task> and <unknown_tool></unknown_tool>") != nil {
		t.Error("parsed a tag that is no tool")
	}
}

// TestDetect checks the variant by shape when no storage directory
// names it.
func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		files map[string]string
		want  Variant
	}{
		{map[string]string{FileAPI: `[{"role":"user","content":"x","isSummary":true}]`}, Roo},
		{map[string]string{FileAPI: `[{"type":"reasoning","encrypted_content":"e"}]`}, Roo},
		{map[string]string{FileAPI: `[]`, FileHistoryItem: `{"id":"x"}`}, Roo},
		{map[string]string{FileAPI: `[]`, FileUI: `[{"ts":1,"type":"say","say":"api_req_started","apiProtocol":"openai"}]`}, Roo},
		{map[string]string{FileAPI: `[{"role":"assistant","content":"x","modelInfo":{"modelId":"m"}}]`}, Classic},
		{map[string]string{FileAPI: `[]`, FileContextHistory: `[]`}, Classic},
		{map[string]string{FileAPI: `[]`}, Classic},
	} {
		files := map[string][]byte{}
		for k, v := range tc.files {
			files[k] = []byte(v)
		}
		task := ParseTask("t", files)
		if task.Variant != tc.want || !task.Guessed {
			t.Errorf("%v: %s (%s), guessed %v", tc.files, task.Variant, task.VariantBy, task.Guessed)
		}
	}
}

// TestFindSessions finds the tasks of a storage tree, not walking into
// checkpoints, and takes a file as it is.
func TestFindSessions(t *testing.T) {
	root := t.TempDir()
	mk := func(p, data string) {
		t.Helper()
		p = filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("tasks/a/api_conversation_history.json", "[]")
	mk("tasks/a/ui_messages.json", "[]")
	mk("tasks/b/ui_messages.json", "[]")
	mk("tasks/c/checkpoints/tasks/d/api_conversation_history.json", "[]")
	mk("other/e/api_conversation_history.json", "[]")
	got, err := FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, p := range got {
		r, _ := filepath.Rel(root, p)
		rel = append(rel, filepath.ToSlash(r))
	}
	want := []string{"tasks/a/api_conversation_history.json", "tasks/b/ui_messages.json"}
	if !slices.Equal(rel, want) {
		t.Errorf("found %v, want %v", rel, want)
	}
	if !IsTaskFile(got[0]) || IsTaskFile(filepath.Join(root, "other/e/api_conversation_history.json")) {
		t.Error("IsTaskFile")
	}
	one, err := FindSessions(got[0])
	if err != nil || len(one) != 1 {
		t.Errorf("a file: %v %v", one, err)
	}
}

// TestReadTaskErrors reads a directory that holds no task, and one
// whose files do not parse.
func TestReadTaskErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadTask(dir); err == nil || !strings.Contains(err.Error(), "not a Cline or Roo task directory") {
		t.Errorf("empty directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileAPI), []byte("[1,{\"role\":\"user\",\"content\":\"x\"},"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileMetadata), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := ReadTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.API.Elems) != 2 || task.API.Err == nil || task.MetadataErr == nil {
		t.Errorf("api %d elements, err %v; metadata err %v", len(task.API.Elems), task.API.Err, task.MetadataErr)
	}
	if task.Units() != 4 {
		t.Errorf("%d units, want 4", task.Units())
	}
	if !bytes.Equal(task.API.Elems[0].Raw, []byte("1")) {
		t.Errorf("raw %s", task.API.Elems[0].Raw)
	}
}

// TestIntBounds checks counts at the edge of int64 are refused rather
// than converted with an implementation-defined result.
func TestIntBounds(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"9223372036854775807", false},
		{"9223372036854774784", true},
		{"-9223372036854775808", true},
		{"1e300", false},
		{`"12"`, true},
	} {
		if _, _, ok := Int(json.RawMessage(tc.in)); ok != tc.ok {
			t.Errorf("Int(%s) ok %v, want %v", tc.in, ok, tc.ok)
		}
	}
}
