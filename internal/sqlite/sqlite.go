// Package sqlite reads another program's SQLite database without
// touching it: [Open] copies the database and its write-ahead log into
// a private temporary directory and opens the copy, which SQLite
// recovers as a crashed writer's, so every frame the log holds up to
// its last commit is read, checkpointed or not. A database is never
// opened in place, since SQLite creates a -wal and a -shm file beside
// a database in WAL mode even for a read-only connection, and a
// connection that closes last may checkpoint. The program that owns the
// database may keep writing while it is copied, so the copy is checked:
// the database file and the log's header must be the same after the
// copy as before it, else the copy is retried a few times and then
// refused with [ErrBusy].
//
// It imports modernc.org/sqlite, a SQLite written in Go, and is one of
// the packages scripts/check-deps.sh lets do so, its path ending in
// /sqlite; the format packages' sqlite packages build on it.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ErrBusy is returned by [Open] when the database kept changing while
// it was copied: the program that owns it is writing to it.
var ErrBusy = errors.New("the database kept changing while it was copied; the program writing it may be running, try again when it is idle")

// attempts and pause bound the copy's retries.
const (
	attempts = 5
	pause    = 200 * time.Millisecond
)

// Copy is a copy of a database, open for reading. It is not safe for
// concurrent use.
type Copy struct {
	// DB is the copy, open.
	DB  *sql.DB
	tmp string
}

// Open copies the database at path into a temporary directory whose
// name starts with prefix, and opens the copy. Close removes it.
func Open(path, prefix string) (*Copy, error) {
	tmp, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		return nil, err
	}
	copies.add(tmp)
	c := &Copy{tmp: tmp}
	copyPath := filepath.Join(tmp, filepath.Base(path))
	if err := snapshot(path, copyPath); err != nil {
		c.remove()
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: copyPath, RawQuery: "_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		c.remove()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	c.DB = db
	return c, nil
}

func (c *Copy) remove() error {
	err := os.RemoveAll(c.tmp)
	copies.remove(c.tmp)
	return err
}

// Close closes the copy and removes it.
func (c *Copy) Close() error {
	var err error
	if c.DB != nil {
		err = c.DB.Close()
	}
	if rerr := c.remove(); err == nil {
		err = rerr
	}
	return err
}

// Tables lists the database's tables.
func (c *Copy) Tables() (map[string]bool, error) { return tables(c.DB) }

func tables(db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Columns lists a table's columns.
func (c *Copy) Columns(table string) (map[string]bool, error) {
	rows, err := c.DB.QueryContext(context.Background(), "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Row is one row of a query, by column name.
type Row map[string]any

// String is a column as text: a TEXT or BLOB value, a number written
// out, "" for NULL or an absent column.
func (r Row) String(name string) string {
	switch v := r[name].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return fmt.Sprint(v)
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

// Int is a column as an integer; false when it is not one.
func (r Row) Int(name string) (int64, bool) {
	switch v := r[name].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), v == float64(int64(v))
	}
	return 0, false
}

// Query runs a query and returns its rows.
func (c *Copy) Query(q string, args ...any) ([]Row, error) {
	rows, err := c.DB.QueryContext(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(names))
		for i, n := range names {
			r[n] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// copies are the private directories of the copies open.
var copies = &registry{dirs: map[string]bool{}}

type registry struct {
	mu   sync.Mutex
	dirs map[string]bool
}

func (r *registry) add(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirs[dir] = true
}

func (r *registry) remove(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dirs, dir)
}

// RemoveCopies removes the copy of every database open: what a program
// interrupted while it reads one calls before it exits, as a deferred
// Close does not run then. The copies are in os.TempDir, under
// $TMPDIR when it is set.
func RemoveCopies() {
	copies.mu.Lock()
	defer copies.mu.Unlock()
	for dir := range copies.dirs {
		os.RemoveAll(dir)
		delete(copies.dirs, dir)
	}
}

// snapshot copies the database at src, and its -wal when it has one, to
// dst, retrying while either changes under the copy.
func snapshot(src, dst string) error {
	for try := 0; try < attempts; try++ {
		if try > 0 {
			time.Sleep(pause)
		}
		before, err := mark(src)
		if err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
		os.Remove(dst + "-wal")
		if before.wal {
			if err := copyFile(src+"-wal", dst+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		after, err := mark(src)
		if err != nil {
			return err
		}
		if before.same(after) {
			return nil
		}
	}
	return ErrBusy
}

// state is what tells a database file and its log unchanged: the
// file's size and modification time, and the log's header, which a
// checkpoint that restarts the log rewrites.
type state struct {
	size    int64
	mod     time.Time
	wal     bool
	walHead []byte
}

func (s state) same(o state) bool {
	return s.size == o.size && s.mod.Equal(o.mod) && s.wal == o.wal && bytes.Equal(s.walHead, o.walHead)
}

func mark(path string) (state, error) {
	info, err := os.Stat(path)
	if err != nil {
		return state{}, err
	}
	if !info.Mode().IsRegular() {
		return state{}, fmt.Errorf("%s is not a file", filepath.Base(path))
	}
	s := state{size: info.Size(), mod: info.ModTime()}
	f, err := os.Open(path + "-wal")
	if err == nil {
		head := make([]byte, 32)
		n, _ := io.ReadFull(f, head)
		f.Close()
		s.wal = true
		s.walHead = head[:n]
	}
	return s, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// IsDatabase reports whether the file at path starts with SQLite's
// header.
func IsDatabase(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 100 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 16)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head) == "SQLite format 3\x00"
}

// Fixture builds a database at path from SQL text, for tests: the
// fixtures of this module are SQL text, never database files. The
// database is put in WAL mode with automatic checkpoints off and
// returned open: rows the script writes stay in the write-ahead log,
// where a reader finds them only through the log, for as long as the
// returned closer is not closed. Closing it checkpoints the log into
// the database.
func Fixture(path, script string) (io.Closer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", script} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
