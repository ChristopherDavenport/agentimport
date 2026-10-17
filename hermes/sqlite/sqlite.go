// Package sqlite opens Hermes Agent's database, state.db, and reads its
// rows as the hermes package's types: sessions, messages,
// system_prompts, session_model_usage and async_delegations. It is one
// of the packages of this module that import a database driver,
// modernc.org/sqlite, a SQLite written in Go, so the hermes package and
// hermes/convert stay without one: hermes/convert takes [OpenStore] in
// its Options.
//
// The database is never opened in place: [Open] reads a private copy of
// it and its write-ahead log, which internal/sqlite makes and checks
// against Hermes writing to it while it copies, as opencode/sqlite does
// OpenCode's. Hermes keeps state.db in WAL mode and writes to it from
// every process of a profile, the gateway's included.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ChristopherDavenport/agentimport/hermes"
	isqlite "github.com/ChristopherDavenport/agentimport/internal/sqlite"
	_ "modernc.org/sqlite"
)

// ErrBusy is returned by [Open] when the database kept changing while
// it was copied: Hermes is writing to it.
var ErrBusy = errors.New("the database kept changing while it was copied; Hermes may be running, try again when it is idle")

// ErrNotHermes is returned by [Open] for a database without Hermes'
// sessions and messages tables.
var ErrNotHermes = errors.New("the database holds no sessions and messages tables of Hermes' (sessions with source and started_at, messages with session_id, role and timestamp)")

// DB is a copy of a Hermes database, open for reading. It implements
// [hermes.Store]. A DB is not safe for concurrent use.
type DB struct {
	name    string
	c       *isqlite.Copy
	db      *sql.DB
	tables  map[string]bool
	version int
	hasVer  bool
}

// Open copies the database at path and opens the copy. Close removes
// it.
func Open(path string) (*DB, error) {
	c, err := isqlite.Open(path, "agentimport-hermes-")
	if errors.Is(err, isqlite.ErrBusy) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	d := &DB{name: filepath.Base(path), c: c, db: c.DB}
	tables, err := c.Tables()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("read the schema: %w", err)
	}
	d.tables = tables
	if !isHermes(d.db, tables) {
		c.Close()
		return nil, ErrNotHermes
	}
	if tables["schema_version"] {
		rows, err := d.query("SELECT max(version) AS v FROM schema_version")
		if err == nil && len(rows) == 1 {
			if v, ok := rows[0].Int("v"); ok {
				d.version, d.hasVer = int(v), true
			}
		}
	}
	return d, nil
}

// OpenStore is [Open] as hermes/convert takes it, in its
// Options.OpenDatabase.
func OpenStore(path string) (hermes.Database, error) {
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// RemoveCopies removes the copy of every database open, for a program
// interrupted while it reads one.
func RemoveCopies() { isqlite.RemoveCopies() }

// Close closes the copy and removes it.
func (d *DB) Close() error { return d.c.Close() }

// Name is the database's file name.
func (d *DB) Name() string { return d.name }

// Version is the schema version schema_version records.
func (d *DB) Version() (int, bool) { return d.version, d.hasVer }

// query runs a query and returns each row's columns.
func (d *DB) query(q string, args ...any) ([]hermes.Columns, error) {
	return query(d.db, q, args...)
}

func query(db *sql.DB, q string, args ...any) ([]hermes.Columns, error) {
	rows, err := db.QueryContext(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []hermes.Columns
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		cols := make(hermes.Columns, len(names))
		for i, n := range names {
			cols[i] = hermes.Column{Name: n, Value: vals[i]}
		}
		out = append(out, cols)
	}
	return out, rows.Err()
}

// Sessions lists the rows of sessions.
func (d *DB) Sessions() ([]*hermes.Session, []*hermes.Stray, error) {
	rows, err := d.query("SELECT * FROM sessions ORDER BY rowid")
	if err != nil {
		return nil, nil, fmt.Errorf("read sessions: %w", err)
	}
	var out []*hermes.Session
	var strays []*hermes.Stray
	for i, cols := range rows {
		id := cols.String("id")
		if id == "" {
			id = fmt.Sprintf("#%d", i+1)
		}
		s := hermes.DecodeSessionRow(d.name+":sessions/"+id, cols)
		if s.Err != nil {
			strays = append(strays, &hermes.Stray{Unit: s.Unit, Kind: "session", Why: s.Err.Error(), Malformed: true})
			continue
		}
		out = append(out, s)
	}
	return out, strays, nil
}

// Messages reads a session's rows of messages in id order.
func (d *DB) Messages(sessionID string) ([]*hermes.Message, error) {
	rows, err := d.query("SELECT * FROM messages WHERE session_id = ? ORDER BY id", sessionID)
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	out := make([]*hermes.Message, 0, len(rows))
	for i, cols := range rows {
		id := cols.String("id")
		if id == "" {
			id = fmt.Sprintf("%s#%d", sessionID, i+1)
		}
		out = append(out, hermes.DecodeMessageRow(d.name+":messages/"+id, cols))
	}
	return out, nil
}

// SystemPrompt reads a prompt of system_prompts.
func (d *DB) SystemPrompt(hash string) (string, bool) {
	if !d.tables["system_prompts"] || hash == "" {
		return "", false
	}
	rows, err := d.query("SELECT prompt FROM system_prompts WHERE hash = ?", hash)
	if err != nil || len(rows) == 0 {
		return "", false
	}
	return rows[0].Text("prompt")
}

// ModelUsage reads a session's rows of session_model_usage.
func (d *DB) ModelUsage(sessionID string) ([]hermes.Columns, error) {
	if !d.tables["session_model_usage"] {
		return nil, nil
	}
	rows, err := d.query("SELECT * FROM session_model_usage WHERE session_id = ? ORDER BY rowid", sessionID)
	if err != nil {
		return nil, fmt.Errorf("read session_model_usage: %w", err)
	}
	return rows, nil
}

// Delegations reads async_delegations.
func (d *DB) Delegations() ([]*hermes.Delegation, error) {
	if !d.tables["async_delegations"] {
		return nil, nil
	}
	rows, err := d.query("SELECT * FROM async_delegations ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("read async_delegations: %w", err)
	}
	var out []*hermes.Delegation
	for _, cols := range rows {
		out = append(out, &hermes.Delegation{
			ID: cols.String("delegation_id"), ParentSessionID: cols.String("parent_session_id"),
			OriginSessionID: cols.String("origin_session_id"), State: cols.String("state"), Cols: cols,
		})
	}
	return out, nil
}

// Orphans lists the rows of messages whose session has no row in
// sessions, or a row that cannot be read. known is not read: every row
// of a session is read with it.
func (d *DB) Orphans(map[string]bool) ([]*hermes.Stray, error) {
	rows, err := d.query("SELECT id FROM messages WHERE session_id IS NULL OR session_id NOT IN (SELECT id FROM sessions WHERE id IS NOT NULL AND id != '') ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	var out []*hermes.Stray
	for _, cols := range rows {
		out = append(out, &hermes.Stray{Unit: d.name + ":messages/" + cols.String("id"), Kind: "message", Why: "its session has no row in sessions that can be read"})
	}
	return out, nil
}

// Units counts the rows a conversion accounts for, those of sessions
// and messages, so a test can hold a report's tally to it.
func (d *DB) Units() (int, error) {
	n := 0
	for _, t := range []string{"sessions", "messages"} {
		rows, err := d.query("SELECT count(*) AS n FROM " + t)
		if err != nil {
			return 0, err
		}
		c, _ := rows[0].Int("n")
		n += int(c)
	}
	return n, nil
}

// isHermes reports whether a database holds Hermes' sessions and
// messages tables, told by the columns Hermes has written since its
// first schema: Crush's opencode.db holds tables of the same names.
func isHermes(db *sql.DB, tables map[string]bool) bool {
	if !tables["sessions"] || !tables["messages"] {
		return false
	}
	has := func(table string, cols ...string) bool {
		rows, err := query(db, "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return false
		}
		set := map[string]bool{}
		for _, r := range rows {
			set[r.String("name")] = true
		}
		for _, c := range cols {
			if !set[c] {
				return false
			}
		}
		return true
	}
	return has("sessions", "id", "source", "started_at") && has("messages", "id", "session_id", "role", "timestamp")
}

// Probe reports whether the file at path is a Hermes database, reading
// its schema without copying it: the file is opened immutable, which
// takes no lock and writes nothing beside it, and reads what has been
// checkpointed into it. A database whose schema is still only in its
// write-ahead log, as one a running Hermes created may be, is read
// from a copy instead.
func Probe(path string) bool {
	if !isqlite.IsDatabase(path) {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false
	}
	tables, err := isqlite.Tables(db)
	ok := err == nil && isHermes(db, tables)
	db.Close()
	if ok {
		return true
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		return false
	}
	d, err := Open(path)
	if err != nil {
		return false
	}
	d.Close()
	return true
}

// Fixture builds a database at path from SQL text, for tests: the
// fixtures of this module are SQL text, never database files.
func Fixture(path, script string) (io.Closer, error) { return isqlite.Fixture(path, script) }

var _ hermes.Store = (*DB)(nil)
