package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// sameJSON reports whether two JSON texts are the same value.
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

// storageFiles lists the storage files of every converter fixture.
func storageFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("convert/testdata", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") && strings.Contains(filepath.ToSlash(path), "/storage/") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no storage fixtures")
	}
	return out
}

// TestRoundTrip decodes every storage file of the fixtures as its kind
// and encodes it again: the same JSON value, members undeclared
// included.
func TestRoundTrip(t *testing.T) {
	for _, path := range storageFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(data) {
			continue
		}
		slash := filepath.ToSlash(path)
		var v any
		switch {
		case strings.Contains(slash, "/storage/session/"):
			f := &SessionFile{}
			if err := f.UnmarshalJSON(data); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			v = f
		case strings.Contains(slash, "/storage/message/"):
			m := DecodeMessage(path, "", "", 0, data)
			if m.Info == nil {
				t.Fatalf("%s: %v", path, m.Err)
			}
			v = m.Info
		case strings.Contains(slash, "/storage/part/"):
			p := DecodePart(path, "", "", data)
			if p.Err != nil {
				t.Fatalf("%s: %v", path, p.Err)
			}
			v = p.Part
		default:
			continue
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !sameJSON(t, data, out) {
			t.Errorf("%s: wrote back\n%s\nfrom\n%s", path, out, data)
		}
	}
}

// TestRoundTripRows decodes V2 data of each shape and encodes it again.
func TestRoundTripRows(t *testing.T) {
	for _, data := range []string{
		`{"text":"hi","files":[{"data":"AA==","mime":"image/png","source":{"type":"inline"}}],"skills":[{"id":"s","name":"n","text":"t"}],"agents":[{"name":"g"}],"time":{"created":1}}`,
		`{"agent":"build","model":{"id":"m","providerID":"p","variant":"high"},"content":[{"type":"reasoning","text":"r","state":{"signature":"s"},"time":{"created":1,"completed":2}},{"type":"tool","id":"c","name":"read","state":{"status":"error","input":{},"error":{"type":"aborted","message":"x","extra":1}},"time":{"created":1}}],"finish":"stop","tokens":{"input":1,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"error":null,"time":{"created":1}}`,
		`{"status":"completed","reason":"auto","summary":"s","recent":"","providerContext":{"version":1},"cost":0,"time":{"created":1},"future":[1,2]}`,
		`{"shellID":"sh","command":"ls","status":"completed","exit":0,"output":{"output":"x"},"time":{"created":1,"completed":2}}`,
	} {
		r := DecodeRow2("u", Columns{{"id", "msg_1"}, {"session_id", "ses_1"}, {"type", "x"}, {"seq", int64(1)}, {"data", data}})
		if r.Err != nil {
			t.Fatalf("%v: %s", r.Err, data)
		}
		out, err := json.Marshal(r.Data)
		if err != nil {
			t.Fatal(err)
		}
		if !sameJSON(t, []byte(data), out) {
			t.Errorf("wrote back\n%s\nfrom\n%s", out, data)
		}
		for _, raw := range r.Data.Content {
			c := &Content2{}
			if err := c.UnmarshalJSON(raw); err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(c)
			if !sameJSON(t, raw, out) {
				t.Errorf("content wrote back\n%s\nfrom\n%s", out, raw)
			}
		}
	}
}

// TestTree reads a storage tree: its sessions, a session's messages in
// order with their parts, the records that read as nothing, the layout
// before MessageV2 and the records of no session.
func TestTree(t *testing.T) {
	root := filepath.Join("convert", "testdata", "g1-basic")
	tree, err := OpenTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tree.Legacy, []string{"project/olddir/storage/session"}) {
		t.Errorf("legacy %v", tree.Legacy)
	}
	sessions, strays, err := tree.Sessions()
	if err != nil || len(strays) > 0 || len(sessions) != 1 {
		t.Fatalf("sessions %v %v %v", sessions, strays, err)
	}
	s := sessions[0]
	if s.ID != "ses_g1demo" || s.Version != "1.1.60" || s.Created != 1760000000000 || s.Gen != G1 {
		t.Errorf("session %+v", s)
	}
	msgs, strays, err := tree.Messages(s)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"msg_0001", "msg_0002"}) {
		t.Errorf("messages %v", ids)
	}
	if len(msgs[1].Parts) != 7 || msgs[1].Parts[0].Part.Type != PartStepStart {
		t.Errorf("parts %d", len(msgs[1].Parts))
	}
	if len(strays) != 1 || !strays[0].Malformed {
		t.Errorf("strays %+v", strays)
	}
	orphans, err := tree.Orphans(map[string]bool{s.ID: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 3 {
		t.Errorf("orphans %+v", orphans)
	}
	if p, ok := tree.Project("prj0"); !ok || p.VCS != "git" {
		t.Errorf("project %+v", p)
	}
	if n := Units(root); n != 15 {
		t.Errorf("units %d", n)
	}
}

// TestFind finds data directories and databases, and tells the store a
// path is in.
func TestFind(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "share", "opencode")
	for _, d := range []string{filepath.Join(data, "storage", "session", "p"), filepath.Join(root, "elsewhere")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(data, DatabaseName), filepath.Join(data, "opencode-dev.db"), filepath.Join(data, "opencode-next.db"), filepath.Join(root, "elsewhere", "opencode-x.db"), filepath.Join(data, "storage", "session", "p", "ses_1.json")} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "elsewhere", "opencode-x.db"), data, filepath.Join(data, "opencode-dev.db")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
	for path, want := range map[string]string{
		data:                              data,
		filepath.Join(data, DatabaseName): data,
		filepath.Join(data, "storage", "session", "p", "ses_1.json"): data,
		filepath.Join(data, "opencode-dev.db"):                       filepath.Join(data, "opencode-dev.db"),
	} {
		if got, ok := StoreOf(path); !ok || got != want {
			t.Errorf("StoreOf(%s) = %s, %v", path, got, ok)
		}
	}
	if _, ok := StoreOf(filepath.Join(root, "elsewhere")); ok {
		t.Error("a directory of no store is one")
	}
}

// TestNumbers reads JSON numbers as OpenCode writes them, and refuses
// what is not one.
func TestNumbers(t *testing.T) {
	for raw, want := range map[string]int{`12`: 12, `12.0`: 12, `-1`: 0, `"12"`: 0, `null`: 0, `1e3`: 1000, `1.5`: 0, ``: 0} {
		if got := Count(json.RawMessage(raw)); got != want {
			t.Errorf("Count(%s) = %d, want %d", raw, got, want)
		}
	}
	for raw, want := range map[string]bool{`0`: false, `1`: true, `false`: false, `""`: false, `null`: false, ``: false, `"x"`: true, `{}`: true} {
		if got := Truthy(json.RawMessage(raw)); got != want {
			t.Errorf("Truthy(%s) = %v", raw, got)
		}
	}
}
