// Package sqlite reads Cline's session index, sessions.db: its sessions
// table, which alone records which agent spawned a sub-agent, yielding
// its rows as the clinesdk package's. It reads
// a private copy of the database and its write-ahead log, never the
// database in place (internal/sqlite), so it neither writes beside a
// database Cline holds open in WAL mode nor waits on its locks. It is
// one of the packages of this module that import a database driver, as
// clinesdk/convert does not: it takes [OpenIndex] in its Options.
package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ChristopherDavenport/agentimport/clinesdk"
	isqlite "github.com/ChristopherDavenport/agentimport/internal/sqlite"
)

// ErrNotCline is returned by [Open] for a database without Cline's
// sessions table.
var ErrNotCline = errors.New("the database has no sessions table of Cline's")

// DB is a copy of sessions.db, open for reading. It implements
// clinesdk.Index. A DB is not safe for concurrent use.
type DB struct {
	name    string
	c       *isqlite.Copy
	columns map[string]bool
}

// Open copies the database at path and opens the copy. Close removes
// it.
func Open(path string) (*DB, error) {
	c, err := isqlite.Open(path, "agentimport-clinesdk-")
	if err != nil {
		return nil, err
	}
	d := &DB{name: filepath.Base(path), c: c}
	tables, err := c.Tables()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("read the schema: %w", err)
	}
	if !tables["sessions"] {
		c.Close()
		return nil, ErrNotCline
	}
	if d.columns, err = c.Columns("sessions"); err != nil {
		c.Close()
		return nil, fmt.Errorf("read the schema: %w", err)
	}
	if !d.columns["session_id"] {
		c.Close()
		return nil, ErrNotCline
	}
	return d, nil
}

// OpenIndex is [Open] as clinesdk/convert takes it, in its
// Options.OpenIndex.
func OpenIndex(path string) (clinesdk.Index, error) {
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// RemoveCopies removes the copy of every database open, for a program
// interrupted while it reads one.
func RemoveCopies() { isqlite.RemoveCopies() }

// Name is the database's file name.
func (d *DB) Name() string { return d.name }

// Close closes the copy and removes it.
func (d *DB) Close() error { return d.c.Close() }

// Family returns the rows of root and of every session whose parent is
// root or whose ID starts with root and "__".
func (d *DB) Family(root string) ([]*clinesdk.Row, error) {
	// substr and length count characters, both of the same argument.
	where := "session_id = ? OR substr(session_id, 1, length(?)) = ?"
	args := []any{root, root + "__", root + "__"}
	if d.columns["parent_session_id"] {
		where += " OR parent_session_id = ?"
		args = append(args, root)
	}
	rows, err := d.c.Query("SELECT * FROM sessions WHERE "+where+" ORDER BY session_id", args...)
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	out := make([]*clinesdk.Row, 0, len(rows))
	for _, r := range rows {
		sub, _ := r.Int("is_subagent")
		row := &clinesdk.Row{
			SessionID: r.String("session_id"), Source: r.String("source"), StartedAt: r.String("started_at"), EndedAt: r.String("ended_at"),
			Status: r.String("status"), ParentSessionID: r.String("parent_session_id"), ParentAgentID: r.String("parent_agent_id"),
			AgentID: r.String("agent_id"), ConversationID: r.String("conversation_id"), IsSubagent: sub == 1, Prompt: r.String("prompt"),
			MessagesPath: r.String("messages_path"),
		}
		if m := r.String("metadata_json"); m != "" && json.Valid([]byte(m)) {
			row.Metadata = json.RawMessage(m)
		}
		row.Unit = d.name + ":sessions/" + row.SessionID
		all := map[string]any{}
		for k, v := range r {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if k == "metadata_json" && row.Metadata != nil {
				v = row.Metadata
			}
			all[k] = v
		}
		if raw, err := json.Marshal(all); err == nil {
			row.Raw = raw
		}
		out = append(out, row)
	}
	return out, nil
}

// Fixture builds a database at path from SQL text, for tests.
var Fixture = isqlite.Fixture

var _ clinesdk.Index = (*DB)(nil)
