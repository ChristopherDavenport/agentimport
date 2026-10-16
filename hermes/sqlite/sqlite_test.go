package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture builds a fixture's database, keeping the writer open when
// asked, so its rows are in the write-ahead log alone.
func fixture(t *testing.T, name string, keepOpen bool) string {
	t.Helper()
	schema, err := os.ReadFile("../testdata/schema-v31.sql")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("../convert/testdata", name, "db.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := Fixture(path, string(schema)+"\n"+string(script))
	if err != nil {
		t.Fatal(err)
	}
	if keepOpen {
		t.Cleanup(func() { w.Close() })
	} else if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRead reads a database's rows, from the database and, with the
// writer open, from its write-ahead log alone, and writes nothing
// beside it.
func TestRead(t *testing.T) {
	for _, open := range []bool{false, true} {
		path := fixture(t, "subagents", open)
		before := listing(t, filepath.Dir(path))
		if !Probe(path) {
			t.Errorf("open %v: Probe does not take the database", open)
		}
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := db.Version(); !ok || v != 31 {
			t.Errorf("version %d %v", v, ok)
		}
		sessions, strays, err := db.Sessions()
		if err != nil || len(sessions) != 7 || len(strays) != 0 {
			t.Fatalf("open %v: %d sessions, %v, %v", open, len(sessions), strays, err)
		}
		msgs, err := db.Messages("20261005_060000_ee0007")
		if err != nil || len(msgs) != 7 {
			t.Fatalf("%d messages, %v", len(msgs), err)
		}
		for i := 1; i < len(msgs); i++ {
			if msgs[i].ID <= msgs[i-1].ID {
				t.Error("messages are not in id order")
			}
		}
		if n, err := db.Units(); err != nil || n != 7+21 {
			t.Errorf("units %d, %v", n, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		// Nothing is created, written or removed beside the database,
		// its write-ahead log and index included while the writer holds
		// them open.
		if after := listing(t, filepath.Dir(path)); after != before {
			t.Errorf("open %v: the directory changed:\n before %s\n after  %s", open, before, after)
		}
		if !open && !strings.Contains(before, "state.db ") || open && !strings.Contains(before, "state.db-wal ") {
			t.Errorf("open %v: unexpected files %s", open, before)
		}
	}
}

// listing writes a directory's files with their sizes and times.
func listing(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %d %d; ", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

func TestTables(t *testing.T) {
	path := fixture(t, "async", false)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dels, err := db.Delegations()
	if err != nil || len(dels) != 1 || dels[0].ParentSessionID != "20261006_050000_ff0014" {
		t.Errorf("delegations %v, %v", dels, err)
	}
	usage, err := db.ModelUsage("20261006_050000_ff0014")
	if err != nil || len(usage) != 0 {
		t.Errorf("usage %v, %v", usage, err)
	}
	if _, ok := db.SystemPrompt("none"); ok {
		t.Error("a prompt of no hash")
	}
	if strays, err := db.Orphans(nil); err != nil || len(strays) != 0 {
		t.Errorf("orphans %v, %v", strays, err)
	}
}

// TestNotHermes refuses a database of other tables, Crush's sessions
// and messages among them.
func TestNotHermes(t *testing.T) {
	for _, script := range []string{
		"CREATE TABLE notes (id INTEGER);",
		"CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT); CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, parts TEXT);",
	} {
		path := filepath.Join(t.TempDir(), "other.db")
		w, err := Fixture(path, script)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
		if _, err := Open(path); err != ErrNotHermes {
			t.Errorf("%s: %v", script, err)
		}
		if Probe(path) {
			t.Errorf("Probe takes %s", script)
		}
	}
	if Probe(filepath.Join(t.TempDir(), "none")) {
		t.Error("Probe takes a missing file")
	}
}
