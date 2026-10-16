// Package sqlite opens OpenCode's database, opencode.db, and reads its
// rows as the opencode package's types: the V1 tables session, message
// and part, and the V2 tables session_v2, session_message,
// session_pending and session_inbox. It is the one package of this
// module that imports a database driver, modernc.org/sqlite, a SQLite
// written in Go, so the opencode package stays on the standard library
// alone.
//
// The database is never opened in place: [Open] reads a private copy of
// it and its write-ahead log, which internal/sqlite makes and checks
// against OpenCode writing to it while it copies, as clinesdk/sqlite
// does Cline's. Frames appended to the log while it was copied are ones
// SQLite's recovery reads up to the last commit it finds whole.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	isqlite "github.com/ChristopherDavenport/agentimport/internal/sqlite"
	"github.com/ChristopherDavenport/agentimport/opencode"
	_ "modernc.org/sqlite"
)

// ErrBusy is returned by [Open] when the database kept changing while
// it was copied: OpenCode is writing to it.
var ErrBusy = errors.New("the database kept changing while it was copied; OpenCode may be running, try again when it is idle")

// ErrNotOpenCode is returned by [Open] for a database that holds none
// of OpenCode's session tables, such as Crush's (opencode-ai/opencode),
// whose tables are sessions and messages.
var ErrNotOpenCode = errors.New("the database holds none of OpenCode's session tables (session, message and part, or session_v2 and session_message); Crush's opencode.db is another program's")

// DB is a copy of an OpenCode database, open for reading. It implements
// [opencode.Store]. A DB is not safe for concurrent use.
type DB struct {
	name   string
	c      *isqlite.Copy
	db     *sql.DB
	tables map[string]bool
}

// Open copies the database at path and opens the copy. Close removes
// it.
func Open(path string) (*DB, error) {
	c, err := isqlite.Open(path, "agentimport-opencode-")
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
	if !d.IsOpenCode() {
		c.Close()
		return nil, ErrNotOpenCode
	}
	return d, nil
}

// RemoveCopies removes the copy of every database open: what a program
// interrupted while it reads one calls before it exits, as a deferred
// Close does not run then. The copies are in os.TempDir, under
// $TMPDIR when it is set.
func RemoveCopies() { isqlite.RemoveCopies() }

// OpenStore is [Open] as the opencode/convert package takes it, in its
// Options.OpenDatabase.
func OpenStore(path string) (opencode.Database, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// Close closes the copy and removes it.
func (d *DB) Close() error { return d.c.Close() }

// Has reports whether the database holds a table.
func (d *DB) Has(table string) bool { return d.tables[table] }

// IsOpenCode reports whether the database holds OpenCode's V1 or V2
// session tables.
func (d *DB) IsOpenCode() bool {
	return d.tables["session"] && d.tables["message"] && d.tables["part"] ||
		d.tables["session_v2"] && d.tables["session_message"]
}

// Name names the database in findings: its file name.
func (d *DB) Name() string { return d.name }

// query runs a query and returns each row's columns.
func (d *DB) query(q string, args ...any) ([]opencode.Columns, error) {
	rows, err := d.db.QueryContext(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []opencode.Columns
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		cols := make(opencode.Columns, len(names))
		for i, n := range names {
			cols[i] = opencode.Column{Name: n, Value: vals[i]}
		}
		out = append(out, cols)
	}
	return out, rows.Err()
}

// unit names a row in findings.
func (d *DB) unit(table, id string) string {
	return d.name + ":" + table + "/" + id
}

// Sessions lists the rows of session (G2) and session_v2 (G3).
func (d *DB) Sessions() ([]*opencode.Session, []*opencode.Stray, error) {
	var out []*opencode.Session
	var strays []*opencode.Stray
	for _, t := range []struct {
		table string
		gen   opencode.Generation
	}{{"session", opencode.G2}, {"session_v2", opencode.G3}} {
		if !d.tables[t.table] {
			continue
		}
		if t.gen == opencode.G2 && !(d.tables["message"] && d.tables["part"]) {
			continue
		}
		if t.gen == opencode.G3 && !d.tables["session_message"] {
			continue
		}
		rows, err := d.query("SELECT * FROM " + t.table + " ORDER BY rowid")
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", t.table, err)
		}
		for i, cols := range rows {
			id := cols.String("id")
			if id == "" {
				id = fmt.Sprintf("#%d", i+1)
			}
			s := opencode.DecodeSessionRow(t.gen, d.unit(t.table, id), cols)
			if s.Err != nil {
				strays = append(strays, &opencode.Stray{Unit: s.Unit, Kind: "session", Why: s.Err.Error(), Malformed: true})
				continue
			}
			out = append(out, s)
		}
	}
	return out, strays, nil
}

// Messages reads a V1 session's message and part rows.
func (d *DB) Messages(s *opencode.Session) ([]*opencode.MessageRecord, []*opencode.Stray, error) {
	if s.Gen != opencode.G2 {
		return nil, nil, errors.New("not a session of the V1 tables")
	}
	msgs, err := d.query("SELECT id, session_id, time_created, time_updated, data FROM message WHERE session_id = ?", s.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read message: %w", err)
	}
	// OpenCode reads a message's parts by message, whatever session the
	// part row names.
	parts, err := d.query("SELECT id, message_id, session_id, time_created, time_updated, data FROM part WHERE message_id IN (SELECT id FROM message WHERE session_id = ?)", s.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read part: %w", err)
	}
	var out []*opencode.MessageRecord
	var strays []*opencode.Stray
	byID := map[string]*opencode.MessageRecord{}
	bad := map[string]bool{}
	for _, cols := range msgs {
		id := cols.String("id")
		created, _ := cols.Int("time_created")
		m := opencode.DecodeMessage(d.unit("message", id), id, s.ID, created, []byte(cols.String("data")))
		if m.Err != nil {
			strays = append(strays, &opencode.Stray{Unit: m.Unit, ID: id, Session: s.ID, Kind: "message", Why: m.Err.Error(), Err: m.Err, Malformed: true})
			bad[id] = true
			continue
		}
		out = append(out, m)
		byID[id] = m
	}
	for _, cols := range parts {
		id, mid := cols.String("id"), cols.String("message_id")
		p := opencode.DecodePart(d.unit("part", id), id, mid, []byte(cols.String("data")))
		m := byID[mid]
		switch {
		case bad[mid]:
			strays = append(strays, &opencode.Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: "its message cannot be read"})
		case m == nil:
			strays = append(strays, &opencode.Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: "its message is not a message of the session"})
		case p.Err != nil:
			strays = append(strays, &opencode.Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: p.Err.Error(), Malformed: true})
		default:
			m.Parts = append(m.Parts, p)
		}
	}
	opencode.SortMessages(opencode.G2, out)
	return out, strays, nil
}

// Rows reads a V2 session's session_message rows and its queued input.
func (d *DB) Rows(s *opencode.Session) ([]*opencode.Row2, []*opencode.QueueRow, error) {
	if s.Gen != opencode.G3 {
		return nil, nil, errors.New("not a session of the V2 tables")
	}
	rows, err := d.query("SELECT * FROM session_message WHERE session_id = ? ORDER BY seq, rowid", s.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read session_message: %w", err)
	}
	var out []*opencode.Row2
	for _, cols := range rows {
		out = append(out, opencode.DecodeRow2(d.unit("session_message", cols.String("id")), cols))
	}
	var queue []*opencode.QueueRow
	for _, t := range []string{"session_pending", "session_inbox"} {
		if !d.tables[t] {
			continue
		}
		rows, err := d.query("SELECT * FROM "+t+" WHERE session_id = ? ORDER BY rowid", s.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", t, err)
		}
		for _, cols := range rows {
			queue = append(queue, opencode.DecodeQueueRow(t, d.unit(t, cols.String("id")), cols))
		}
	}
	return out, queue, nil
}

// Orphans lists the message rows of no session of the V1 tables, the
// part rows of no message read with one, and the session_message,
// session_pending and session_inbox rows of no session of the V2
// tables. sessions is not read: every row of a session is read with
// it, so a row is an orphan when its session row does not exist. A V1
// database's own session_message, OpenCode 1's projection of its
// messages, which no V1 request reads, is reported once, its rows not
// records.
func (d *DB) Orphans(map[string]bool) ([]*opencode.Stray, error) {
	var out []*opencode.Stray
	add := func(table, kind, why, q string) error {
		rows, err := d.query(q + " ORDER BY rowid")
		if err != nil {
			return fmt.Errorf("read %s: %w", table, err)
		}
		for _, cols := range rows {
			out = append(out, &opencode.Stray{Unit: d.unit(table, cols.String("id")), ID: cols.String("id"), Kind: kind, Why: why})
		}
		return nil
	}
	const sessions = "SELECT id FROM session WHERE id IS NOT NULL"
	if d.v1() {
		if err := add("message", "message", "its session has no row in session",
			"SELECT id FROM message WHERE session_id IS NULL OR session_id NOT IN ("+sessions+")"); err != nil {
			return nil, err
		}
		if err := add("part", "part", "its message is no message of a session",
			"SELECT id FROM part WHERE message_id IS NULL OR message_id NOT IN (SELECT id FROM message WHERE id IS NOT NULL AND session_id IN ("+sessions+"))"); err != nil {
			return nil, err
		}
	}
	if d.v2() {
		for _, t := range []string{"session_message", "session_pending", "session_inbox"} {
			if !d.tables[t] {
				continue
			}
			if err := add(t, "row", "its session has no row in session_v2",
				"SELECT id FROM "+t+" WHERE session_id IS NULL OR session_id NOT IN (SELECT id FROM session_v2 WHERE id IS NOT NULL)"); err != nil {
				return nil, err
			}
		}
	} else if d.tables["session_message"] {
		rows, err := d.query("SELECT count(*) AS n FROM session_message")
		if err != nil {
			return nil, fmt.Errorf("read session_message: %w", err)
		}
		if n, _ := rows[0].Int("n"); n > 0 {
			out = append(out, &opencode.Stray{Unit: d.unit("session_message", ""), Kind: "table", Why: fmt.Sprintf("OpenCode 1's own session_message table, %d rows of its projection of the V1 messages, which no V1 request reads, is not read", n)})
		}
	}
	return out, nil
}

// v1 reports whether the database holds the V1 tables, and v2 the V2
// tables.
func (d *DB) v1() bool { return d.tables["session"] && d.tables["message"] && d.tables["part"] }
func (d *DB) v2() bool { return d.tables["session_v2"] && d.tables["session_message"] }

// Project reads a project row.
func (d *DB) Project(id string) (opencode.Project, bool) {
	if !d.tables["project"] {
		return opencode.Project{}, false
	}
	rows, err := d.query("SELECT * FROM project WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return opencode.Project{}, false
	}
	return opencode.Project{Worktree: rows[0].String("worktree"), VCS: rows[0].String("vcs")}, true
}

// MigrationDone reports whether OpenCode 2 recorded its copy of the V1
// tables complete, in kv's migration.v1-v2.
func (d *DB) MigrationDone() bool {
	if !d.tables["kv"] {
		return false
	}
	rows, err := d.query("SELECT value FROM kv WHERE key = 'migration.v1-v2'")
	if err != nil || len(rows) == 0 {
		return false
	}
	var v struct {
		Phase string `json:"phase"`
	}
	return json.Unmarshal([]byte(rows[0].String("value")), &v) == nil && v.Phase == "completed"
}

// Probe reports whether the file at path is an OpenCode database,
// reading its schema without copying it: the file is opened immutable,
// which takes no lock and writes nothing beside it, and reads what has
// been checkpointed into it, where OpenCode's tables are from the
// database's first start.
func Probe(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 100 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	head := make([]byte, 16)
	_, err = io.ReadFull(f, head)
	f.Close()
	if err != nil || string(head) != "SQLite format 3\x00" {
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
	defer db.Close()
	d := &DB{db: db}
	tables, err := isqlite.Tables(db)
	if err != nil {
		return false
	}
	d.tables = tables
	return d.IsOpenCode()
}

// ensure DB is a store.
var _ opencode.Store = (*DB)(nil)

// recordTables are the tables whose rows a conversion reads.
var recordTables = []string{"session", "message", "part", "session_v2", "session_message", "session_pending", "session_inbox"}

// Units counts the rows of the tables a conversion reads, the session
// tables of each generation it reads and their records, so a test can
// hold a report's tally to it.
func (d *DB) Units() (int, error) {
	n := 0
	for _, t := range recordTables {
		switch t {
		case "session", "message", "part":
			if !d.v1() {
				continue
			}
		default:
			if !d.v2() || !d.tables[t] {
				continue
			}
		}
		rows, err := d.query("SELECT count(*) AS n FROM " + t)
		if err != nil {
			return 0, err
		}
		c, _ := rows[0].Int("n")
		n += int(c)
	}
	return n, nil
}
