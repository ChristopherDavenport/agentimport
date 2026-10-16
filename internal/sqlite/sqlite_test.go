package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	w, err := Fixture(path, "CREATE TABLE t (id INTEGER, name TEXT); INSERT INTO t VALUES (1, 'a'), (2, NULL);")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if !IsDatabase(path) {
		t.Error("the fixture is not a database")
	}
	c, err := Open(path, "agentimport-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.tmp); err != nil {
		t.Fatalf("no copy: %v", err)
	}
	rows, err := c.Query("SELECT id, name FROM t ORDER BY id")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v, %v", rows, err)
	}
	if n, ok := rows[0].Int("id"); !ok || n != 1 || rows[0].String("name") != "a" || rows[1].String("name") != "" {
		t.Errorf("rows %v", rows)
	}
	cols, err := c.Columns("t")
	if err != nil || !cols["name"] {
		t.Errorf("columns %v, %v", cols, err)
	}
	RemoveCopies()
	if _, err := os.Stat(c.tmp); !os.IsNotExist(err) {
		t.Errorf("the copy is still there: %v", err)
	}
	c.Close()
	if IsDatabase(filepath.Join(t.TempDir(), "none")) {
		t.Error("a missing file is a database")
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
