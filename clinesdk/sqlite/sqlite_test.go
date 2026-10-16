package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, keepOpen bool) string {
	t.Helper()
	script, err := os.ReadFile("../convert/testdata/s3-subagents/db.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sessions.db")
	w, err := Fixture(path, string(script))
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

// TestFamily reads a root's rows, from the database and, with the
// writer open, from its write-ahead log alone.
func TestFamily(t *testing.T) {
	for _, open := range []bool{false, true} {
		path := fixture(t, open)
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Family("1759327200000_sub01")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 5 {
			t.Fatalf("open %v: %d rows", open, len(rows))
		}
		byID := map[string]string{}
		for _, r := range rows {
			byID[r.SessionID] = r.ParentAgentID
		}
		if byID["1759327200000_sub01__agent_9"] != "agent_8" {
			t.Errorf("agent_9's parent agent is %q", byID["1759327200000_sub01__agent_9"])
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The copy is removed, and nothing is written beside the database.
	entries, _ := os.ReadDir(filepath.Dir(fixture(t, false)))
	for _, e := range entries {
		if e.Name() != "sessions.db" {
			t.Errorf("%s beside the database", e.Name())
		}
	}
}

func TestNotCline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	w, err := Fixture(path, "CREATE TABLE notes (id INTEGER);")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := Open(path); err != ErrNotCline {
		t.Errorf("got %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("a missing database opened")
	}
}
