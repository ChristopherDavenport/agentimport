package sqlite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/opencode"
)

func schema(t *testing.T, names ...string) string {
	t.Helper()
	var out string
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join("..", "testdata", "schema-"+n+".sql"))
		if err != nil {
			t.Fatal(err)
		}
		out += string(data) + "\n"
	}
	return out
}

const rows = `
INSERT INTO project VALUES ('prj_a', '/work/demo', 'git', NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated)
  VALUES ('ses_a', 'prj_a', 'quiet-otter', '/work/demo', 'A', '1.4.0', 1760000000000, 1760000000000);
INSERT INTO message VALUES ('msg_a1', 'ses_a', 1760000000001, 1760000000001,
  '{"role":"user","time":{"created":1760000000001},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_a1', 'msg_a1', 'ses_a', 1760000000001, 1760000000001, '{"type":"text","text":"hello"}');
`

// files lists a directory's entries by name.
func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestOpenReadsTheLog reads rows that are only in the write-ahead log,
// while the writer holds the database open, as OpenCode does while it
// runs.
func TestOpenReadsTheLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, opencode.DatabaseName)
	w, err := Fixture(path, schema(t, "project", "v1")+"PRAGMA wal_checkpoint(TRUNCATE);\n"+rows)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the rows are not in the log: %v", err)
	}
	before := files(t, dir)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sessions, strays, err := db.Sessions()
	if err != nil || len(strays) > 0 {
		t.Fatalf("sessions: %v %v", err, strays)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_a" {
		t.Fatalf("sessions %+v", sessions)
	}
	msgs, strays, err := db.Messages(sessions[0])
	if err != nil || len(strays) > 0 {
		t.Fatalf("messages: %v %v", err, strays)
	}
	if len(msgs) != 1 || len(msgs[0].Parts) != 1 || msgs[0].Parts[0].Part.Text != "hello" {
		t.Fatalf("messages %+v", msgs)
	}
	if p, ok := db.Project("prj_a"); !ok || p.Worktree != "/work/demo" || p.VCS != "git" {
		t.Errorf("project %+v %v", p, ok)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if after := files(t, dir); !slices.Equal(before, after) {
		t.Errorf("the directory held %v and holds %v", before, after)
	}
	if _, err := os.Stat(db.tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy was not removed: %v", err)
	}
	if !Probe(path) {
		t.Error("Probe does not take the database for OpenCode's")
	}
}

// TestOpenWritesNothing opens a database with no log beside it and
// leaves none.
func TestOpenWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, opencode.DatabaseName)
	w, err := Fixture(path, schema(t, "project", "v1")+rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before := files(t, dir)
	if !slices.Equal(before, []string{opencode.DatabaseName}) {
		t.Fatalf("the fixture left %v", before)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sessions, _, err := db.Sessions()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions %v %v", sessions, err)
	}
	db.Close()
	if !Probe(path) {
		t.Error("Probe does not take the database for OpenCode's")
	}
	if after := files(t, dir); !slices.Equal(before, after) {
		t.Errorf("the directory held %v and holds %v", before, after)
	}
}

// TestNotOpenCode refuses a database of other tables, such as Crush's.
func TestNotOpenCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), opencode.DatabaseName)
	w, err := Fixture(path, "CREATE TABLE sessions (id TEXT PRIMARY KEY); CREATE TABLE messages (id TEXT PRIMARY KEY, parts TEXT);")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := Open(path); !errors.Is(err, ErrNotOpenCode) {
		t.Errorf("Open: %v", err)
	}
	if Probe(path) {
		t.Error("Probe takes Crush's tables for OpenCode's")
	}
	junk := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(junk, []byte("not a database at all, but long enough to be read as one: 0123456789012345678901234567890123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Probe(junk) {
		t.Error("Probe takes text for a database")
	}
	if _, err := Open(junk); err == nil {
		t.Error("Open reads text as a database")
	}
}

// TestBusy refuses a copy taken while the database keeps changing.
func TestBusy(t *testing.T) {
	a := state{size: 1}
	b := state{size: 2}
	if a.same(b) {
		t.Error("states of two sizes are the same")
	}
	if !a.same(state{size: 1}) {
		t.Error("a state is not itself")
	}
	if a.same(state{size: 1, wal: true, walHead: []byte{1}}) {
		t.Error("a log appearing is no change")
	}
}

// TestRoundTripFixtures decodes the data of every row of the converter's
// fixtures and encodes it again: the same JSON value, members undeclared
// included.
func TestRoundTripFixtures(t *testing.T) {
	scripts, _ := filepath.Glob(filepath.Join("..", "convert", "testdata", "*", "db.sql"))
	if len(scripts) == 0 {
		t.Fatal("no fixtures")
	}
	for _, s := range scripts {
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "-- schema:"); ok {
				names = strings.Fields(rest)
			}
		}
		path := filepath.Join(t.TempDir(), opencode.DatabaseName)
		w, err := Fixture(path, schema(t, names...)+string(data))
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		w.Close()
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sessions, _, err := db.Sessions()
		if err != nil {
			t.Fatal(err)
		}
		for _, sess := range sessions {
			var raws [][2][]byte
			if sess.Gen == opencode.G2 {
				msgs, _, err := db.Messages(sess)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range msgs {
					out, _ := json.Marshal(m.Info)
					raws = append(raws, [2][]byte{withoutIDs(t, m.Raw), withoutIDs(t, out)})
					for _, p := range m.Parts {
						out, _ := json.Marshal(p.Part)
						raws = append(raws, [2][]byte{withoutIDs(t, p.Raw), withoutIDs(t, out)})
					}
				}
			} else {
				rows, _, err := db.Rows(sess)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					if r.Err != nil {
						continue
					}
					out, _ := json.Marshal(r.Data)
					raws = append(raws, [2][]byte{r.Raw, out})
				}
			}
			for _, pair := range raws {
				var a, b any
				if json.Unmarshal(pair[0], &a) != nil || json.Unmarshal(pair[1], &b) != nil || !reflect.DeepEqual(a, b) {
					t.Errorf("%s: wrote back\n%s\nfrom\n%s", s, pair[1], pair[0])
				}
			}
		}
		db.Close()
	}
}

// withoutIDs drops the members a decoder fills from a row's columns.
func withoutIDs(t *testing.T, raw []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	for _, k := range []string{"id", "sessionID", "messageID"} {
		delete(m, k)
	}
	out, _ := json.Marshal(m)
	return out
}

// TestV1Projection reads a V1 database that holds OpenCode 1's own
// session_message table: its rows are no records and no orphans, and
// the table is reported once.
func TestV1Projection(t *testing.T) {
	path := filepath.Join(t.TempDir(), opencode.DatabaseName)
	w, err := Fixture(path, schema(t, "project", "v1")+rows+`
CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
INSERT INTO session_message VALUES ('msg_p1', 'ses_a', 'user', 0, 1, 1, '{}');
INSERT INTO session_message VALUES ('msg_p2', 'ses_a', 'assistant', 1, 1, 1, '{}');`)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	strays, err := db.Orphans(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 || strays[0].Kind != "table" {
		t.Errorf("strays %+v", strays)
	}
	if n, err := db.Units(); err != nil || n != 3 {
		t.Errorf("units %d %v", n, err)
	}
}

// TestRemoveCopies removes the copy of a database still open, as an
// interrupted run does.
func TestRemoveCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), opencode.DatabaseName)
	w, err := Fixture(path, schema(t, "project", "v1")+rows)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	RemoveCopies()
	if _, err := os.Stat(db.tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy is still there: %v", err)
	}
	db.Close()
}
