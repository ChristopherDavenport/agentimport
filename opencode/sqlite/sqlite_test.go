package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

