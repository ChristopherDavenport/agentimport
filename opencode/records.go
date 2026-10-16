package opencode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Generation is one of OpenCode's three ways of storing sessions.
type Generation int

const (
	// G1 is the storage/ tree of JSON files, up to 1.1.65.
	G1 Generation = 1
	// G2 is the V1 tables of opencode.db, session, message and part,
	// from 1.2.0 to 1.19.
	G2 Generation = 2
	// G3 is the V2 tables of opencode.db, session_v2 and
	// session_message, from 2.0.0.
	G3 Generation = 3
)

// String names the generation as the documents do.
func (g Generation) String() string {
	switch g {
	case G1:
		return "storage files"
	case G2:
		return "V1 tables"
	case G3:
		return "V2 tables"
	}
	return "unknown storage"
}

// Column is one column of a database row: its name and its value as
// database/sql scans it, an int64, float64, string, []byte or nil.
type Column struct {
	Name  string
	Value any
}

// Columns is a row of a table, in the table's column order.
type Columns []Column

// Get returns a column's value; false when the row has no such column.
func (cs Columns) Get(name string) (any, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c.Value, true
		}
	}
	return nil, false
}

// String returns a text column; empty when it is absent, null or not
// text.
func (cs Columns) String(name string) string {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
	}
	return ""
}

// Int returns an integer column.
func (cs Columns) Int(name string) (int64, bool) {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case int64:
		return v, true
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
			return int64(v), true
		}
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// jsonColumns are the columns OpenCode writes JSON text into.
var jsonColumns = map[string]bool{
	"data": true, "summary_diffs": true, "metadata": true, "revert": true, "permission": true,
	"model": true, "fork_boundary": true, "payload": true, "sandboxes": true, "commands": true,
}

// JSON writes the row as one JSON object, a member per column in the
// table's order: a JSON text column as the value it holds when it holds
// one, text as a string, a number as a number, null as null, and a
// blob as base64 text.
func (cs Columns) JSON() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range cs {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(c.Name)
		b.Write(name)
		b.WriteByte(':')
		b.Write(columnJSON(c))
	}
	b.WriteByte('}')
	return b.Bytes()
}

func columnJSON(c Column) []byte {
	switch v := c.Value.(type) {
	case nil:
		return []byte("null")
	case int64:
		return strconv.AppendInt(nil, v, 10)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return []byte("null")
		}
		out, _ := json.Marshal(v)
		return out
	case []byte:
		if !utf8.Valid(v) {
			out, _ := json.Marshal(base64.StdEncoding.EncodeToString(v))
			return out
		}
		return columnJSON(Column{Name: c.Name, Value: string(v)})
	case string:
		if jsonColumns[c.Name] && json.Valid([]byte(v)) {
			var buf bytes.Buffer
			if json.Compact(&buf, []byte(v)) == nil {
				return buf.Bytes()
			}
		}
		out, _ := json.Marshal(v)
		return out
	case bool:
		if v {
			return []byte("true")
		}
		return []byte("false")
	}
	out, err := json.Marshal(fmt.Sprint(c.Value))
	if err != nil {
		return []byte("null")
	}
	return out
}

// Session is one copy of a session's record, from any generation: the
// members a converter reads, and the record itself.
type Session struct {
	Gen Generation
	// Unit names the record in findings: a file's path under the data
	// directory, or <database>:<table>/<id>.
	Unit string

	ID, ProjectID, WorkspaceID, ParentID string
	// ForkSessionID is a V2 fork's origin, and ForkBoundary where it was
	// forked: {type: before|through, messageID}.
	ForkSessionID string
	ForkBoundary  json.RawMessage
	Slug          string
	Directory     string
	Path          string
	Title         string
	// Version is the OpenCode release that created the session.
	Version string
	Agent   string
	Model   *ModelRef
	// Revert is a staged revert, {messageID, partID?, snapshot?, …}.
	Revert json.RawMessage
	// Created and Updated are in milliseconds; HasCreated says the
	// record holds a creation time.
	Created, Updated int64
	HasCreated       bool

	// Record is the session as read: the file's object, or the row's
	// columns as one object.
	Record json.RawMessage
	// Err says why the record could not be read; only Unit and, when
	// the file name or row gave it, ID are then set.
	Err error
}

// SessionFile is a G1 session file, Session.Info: the members a
// converter reads, the rest kept.
type SessionFile struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	ProjectID   string          `json:"projectID"`
	WorkspaceID string          `json:"workspaceID"`
	Directory   string          `json:"directory"`
	Path        string          `json:"path"`
	ParentID    string          `json:"parentID"`
	Title       string          `json:"title"`
	Version     string          `json:"version"`
	Agent       string          `json:"agent"`
	Model       *ModelRef       `json:"model"`
	Revert      json.RawMessage `json:"revert"`
	Time        *SessionTime    `json:"time"`

	Members
}

type plainSessionFile SessionFile

// UnmarshalJSON decodes the session, keeping undeclared members and
// members of another JSON type.
func (s *SessionFile) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainSessionFile)(s), &s.Members)
}

// MarshalJSON writes the session back as it was read.
func (s SessionFile) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainSessionFile(s), &s.Members)
}

// SessionTime is a G1 session's time.
type SessionTime struct {
	Created json.RawMessage `json:"created"`
	Updated json.RawMessage `json:"updated"`

	Members
}

type plainSessionTime SessionTime

// UnmarshalJSON decodes the times, keeping undeclared members.
func (t *SessionTime) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainSessionTime)(t), &t.Members)
}

// MarshalJSON writes the times back as they were read.
func (t SessionTime) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainSessionTime(t), &t.Members)
}

// DecodeSessionFile reads a G1 session file's contents.
func DecodeSessionFile(unit string, data []byte) *Session {
	s := &Session{Gen: G1, Unit: unit}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		s.Err = errors.New("the file is not JSON")
		return s
	}
	var f SessionFile
	if err := f.UnmarshalJSON(data); err != nil {
		s.Err = err
		return s
	}
	s.Record = bytes.Clone(data)
	s.ID, s.Slug, s.ProjectID, s.WorkspaceID = f.ID, f.Slug, f.ProjectID, f.WorkspaceID
	s.Directory, s.Path, s.ParentID, s.Title = f.Directory, f.Path, f.ParentID, f.Title
	s.Version, s.Agent, s.Model = f.Version, f.Agent, f.Model
	if !IsNull(f.Revert) {
		s.Revert = f.Revert
	}
	if f.Time != nil {
		s.Created, s.HasCreated = Millis(f.Time.Created)
		s.Updated, _ = Millis(f.Time.Updated)
	}
	if s.ID == "" {
		s.Err = errors.New("the session has no id")
	}
	return s
}

// DecodeSessionRow reads a row of the session table (G2) or the
// session_v2 table (G3).
func DecodeSessionRow(gen Generation, unit string, cols Columns) *Session {
	s := &Session{Gen: gen, Unit: unit, Record: cols.JSON()}
	s.ID = cols.String("id")
	if s.ID == "" {
		s.Err = errors.New("the row has no id")
		return s
	}
	s.ProjectID, s.WorkspaceID, s.ParentID = cols.String("project_id"), cols.String("workspace_id"), cols.String("parent_id")
	s.ForkSessionID = cols.String("fork_session_id")
	if fb := cols.String("fork_boundary"); fb != "" && json.Valid([]byte(fb)) {
		s.ForkBoundary = json.RawMessage(fb)
	}
	s.Slug, s.Directory, s.Path = cols.String("slug"), cols.String("directory"), cols.String("path")
	s.Title, s.Version, s.Agent = cols.String("title"), cols.String("version"), cols.String("agent")
	if m := cols.String("model"); m != "" {
		var ref ModelRef
		if ref.UnmarshalJSON([]byte(m)) == nil {
			s.Model = &ref
		}
	}
	if r := cols.String("revert"); r != "" && json.Valid([]byte(r)) && !IsNull(json.RawMessage(r)) {
		s.Revert = json.RawMessage(r)
	}
	if n, ok := cols.Int("time_created"); ok && n >= 0 && n <= 1e15 {
		s.Created, s.HasCreated = n, true
	}
	if n, ok := cols.Int("time_updated"); ok && n >= 0 && n <= 1e15 {
		s.Updated = n
	}
	return s
}

// Revert is a staged revert: the message it starts at, and the part
// within it, when it starts at one.
type Revert struct {
	MessageID string
	PartID    string
}

// StagedRevert reads the session's staged revert; false when none is
// staged.
func (s *Session) StagedRevert() (Revert, bool) {
	var r struct {
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
	}
	if !isObject(s.Revert) || json.Unmarshal(s.Revert, &r) != nil || r.MessageID == "" {
		return Revert{}, false
	}
	return Revert{MessageID: r.MessageID, PartID: r.PartID}, true
}

// Fork is a V2 fork's boundary: where the origin's history it copied
// ends.
type Fork struct {
	Type      string
	MessageID string
}

// ForkPoint reads a V2 session's fork boundary.
func (s *Session) ForkPoint() (Fork, bool) {
	var f struct {
		Type      string `json:"type"`
		MessageID string `json:"messageID"`
	}
	if !isObject(s.ForkBoundary) || json.Unmarshal(s.ForkBoundary, &f) != nil {
		return Fork{}, false
	}
	return Fork{Type: f.Type, MessageID: f.MessageID}, true
}

// MessageRecord is one V1 message as read, with its parts.
type MessageRecord struct {
	Unit string
	ID   string
	// Created is when OpenCode orders the message by: the row's
	// time_created (G2), or info's time.created (G1).
	Created int64
	Info    *Message
	// Raw is the message's object as read: a G1 file's contents, or a
	// G2 row's data.
	Raw json.RawMessage
	// Err says why the message could not be read; its parts are then
	// strays.
	Err   error
	Parts []*PartRecord
}

// PartRecord is one V1 part as read.
type PartRecord struct {
	Unit      string
	ID        string
	MessageID string
	Part      *Part
	Raw       json.RawMessage
	Err       error
}

// DecodeMessage reads a V1 message: a G1 file's contents, or a G2 row's
// data with the row's id and session.
func DecodeMessage(unit, id, sessionID string, created int64, data []byte) *MessageRecord {
	r := &MessageRecord{Unit: unit, ID: id, Created: created}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		r.Err = errors.New("not JSON")
		return r
	}
	m := &Message{}
	if err := m.UnmarshalJSON(data); err != nil {
		r.Err = err
		return r
	}
	r.Raw = bytes.Clone(data)
	if m.ID == "" {
		m.ID = id
	}
	if m.SessionID == "" {
		m.SessionID = sessionID
	}
	if r.ID == "" {
		r.ID = m.ID
	}
	r.Info = m
	if m.Role != RoleUser && m.Role != RoleAssistant {
		r.Err = fmt.Errorf("role %q is neither user nor assistant", m.Role)
	}
	return r
}

// DecodePart reads a V1 part: a G1 file's contents, or a G2 row's data
// with the row's id and message.
func DecodePart(unit, id, messageID string, data []byte) *PartRecord {
	r := &PartRecord{Unit: unit, ID: id, MessageID: messageID}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		r.Err = errors.New("not JSON")
		return r
	}
	p := &Part{}
	if err := p.UnmarshalJSON(data); err != nil {
		r.Err = err
		return r
	}
	r.Raw = bytes.Clone(data)
	if p.ID == "" {
		p.ID = id
	}
	if p.MessageID == "" {
		p.MessageID = messageID
	}
	if r.ID == "" {
		r.ID = p.ID
	}
	if r.MessageID == "" {
		r.MessageID = p.MessageID
	}
	r.Part = p
	return r
}

// SortMessages orders a session's messages as OpenCode reads them: G2
// by time_created, then ID; G1 by ID, its file name. Each message's
// parts are ordered by ID.
func SortMessages(gen Generation, ms []*MessageRecord) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if gen == G2 && a.Created != b.Created {
			return a.Created < b.Created
		}
		return a.ID < b.ID
	})
	for _, m := range ms {
		sort.SliceStable(m.Parts, func(i, j int) bool { return m.Parts[i].ID < m.Parts[j].ID })
	}
}

// Row2 is one V2 session_message row as read.
type Row2 struct {
	Unit      string
	ID        string
	SessionID string
	Type      string
	Seq       int64
	Created   int64
	Updated   int64
	Data      *Message2
	// Raw is the row's data as read.
	Raw json.RawMessage
	Err error
}

// DecodeRow2 reads a session_message row.
func DecodeRow2(unit string, cols Columns) *Row2 {
	r := &Row2{Unit: unit, ID: cols.String("id"), SessionID: cols.String("session_id"), Type: cols.String("type")}
	seq, ok := cols.Int("seq")
	if !ok {
		r.Err = errors.New("the row has no seq")
		return r
	}
	r.Seq = seq
	r.Created, _ = cols.Int("time_created")
	r.Updated, _ = cols.Int("time_updated")
	data := []byte(cols.String("data"))
	if !json.Valid(data) {
		r.Err = errors.New("its data is not JSON")
		return r
	}
	m := &Message2{}
	if err := m.UnmarshalJSON(data); err != nil {
		r.Err = fmt.Errorf("its data: %w", err)
		return r
	}
	r.Raw = bytes.Clone(bytes.TrimSpace(data))
	r.Data = m
	return r
}

// QueueRow is a row of session_pending or session_inbox: input
// OpenCode 2 has queued and not yet put in the history.
type QueueRow struct {
	Unit  string
	Table string
	ID    string
	Type  string
	// Seq is admitted_seq or enqueued_seq.
	Seq     int64
	Created int64
	Record  json.RawMessage
}

// DecodeQueueRow reads a session_pending or session_inbox row.
func DecodeQueueRow(table, unit string, cols Columns) *QueueRow {
	q := &QueueRow{Unit: unit, Table: table, ID: cols.String("id"), Type: cols.String("type"), Record: cols.JSON()}
	if n, ok := cols.Int("admitted_seq"); ok {
		q.Seq = n
	} else if n, ok := cols.Int("enqueued_seq"); ok {
		q.Seq = n
	}
	q.Created, _ = cols.Int("time_created")
	return q
}

// Stray is a record read that belongs to no session it can be
// converted with: a part of no message, a message of no session, a file
// or row that cannot be read.
type Stray struct {
	Unit string
	// Session names the session it was read with, when it was.
	Session string
	// Kind says what the record is: "session", "message", "part", "row".
	Kind string
	// Why says why it is a stray.
	Why string
	// Malformed says the record could not be read at all.
	Malformed bool
}

// Project is what a project record says of a session's project.
type Project struct {
	Worktree string
	VCS      string
}

// Store is the records of one OpenCode store: the storage/ tree of a
// data directory, or a database. Records are read on demand, a session
// at a time.
type Store interface {
	// Name names the store in findings: "storage", or the database's
	// file name.
	Name() string
	// Sessions lists every session record the store holds, in any
	// generation, and the records that could not be read as one.
	Sessions() ([]*Session, []*Stray, error)
	// Messages reads a V1 session's messages with their parts, in the
	// order OpenCode reads them, and the records read with them that
	// belong to none of its messages.
	Messages(s *Session) ([]*MessageRecord, []*Stray, error)
	// Rows reads a V2 session's rows in seq order, and its queued
	// input.
	Rows(s *Session) ([]*Row2, []*QueueRow, error)
	// Orphans lists the message, part and row records that name a
	// session not in the set, or a message that does not exist.
	Orphans(sessions map[string]bool) ([]*Stray, error)
	// Project reads a project record.
	Project(id string) (Project, bool)
	// MigrationDone reports whether OpenCode 2 finished copying the V1
	// tables into its own, as its kv table records.
	MigrationDone() bool
}

// Database is a store read from a database file, open until closed.
type Database interface {
	Store
	Close() error
}
