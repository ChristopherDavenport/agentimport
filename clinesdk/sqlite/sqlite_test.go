package sqlite

import (
	"os"
	"path/filepath"
	"strings"
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

// TestFamilyNonASCII finds a child of a root whose ID is not ASCII by
// its ID's prefix, and keeps every column of a row.
func TestFamilyNonASCII(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	script, err := os.ReadFile("../convert/testdata/s3-subagents/db.sql")
	if err != nil {
		t.Fatal(err)
	}
	w, err := Fixture(path, string(script)+`
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, hook_path, updated_at)
VALUES ('rööt__agent_1', 'cli', 1, 't', 'completed', 1, 'p', 'm', '/w', '/w', 1, 1, 1, '', 't');`)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Family("rööt")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v, %v", rows, err)
	}
	if !strings.Contains(string(rows[0].Raw), `"workspace_root":"/w"`) {
		t.Errorf("row %s", rows[0].Raw)
	}
}
