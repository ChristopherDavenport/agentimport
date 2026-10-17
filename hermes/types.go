package hermes

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Members is embedded in every JSON object this package decodes. It
// keeps the members the Go type does not declare, so an object decoded
// and encoded again is the same JSON value.
type Members = jsonline.Members

// errNotObject is the error for a value that is not a JSON object.
var errNotObject = errors.New("not a JSON object")

// SchemaVersion is the newest schema version this package was written
// against: hermes_state_common.SCHEMA_VERSION at hermes-agent 848fc0ad8b.
const SchemaVersion = 31

// DatabaseName is the name of Hermes' database in its home directory
// and in each profile's.
const DatabaseName = "state.db"

// Message roles Hermes writes.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
	// RoleSessionMeta is the gateway's first-turn marker, transcript
	// bookkeeping that no model reads.
	RoleSessionMeta = "session_meta"
)

// ContentPrefix marks content Hermes stored as JSON, multimodal parts
// or a dict, rather than text: SessionDB._CONTENT_JSON_PREFIX.
const ContentPrefix = "\x00json:"

// Model config markers that tie a child session to its parent.
const (
	MarkerBranch   = "_branched_from"
	MarkerDelegate = "_delegate_from"
	MarkerReset    = "_reset_from"
)

// Session is one row of the sessions table: the columns a converter
// reads, and the row itself.
type Session struct {
	// Unit names the row in findings: <database>:sessions/<id>.
	Unit string

	ID       string
	Source   string
	ParentID string
	Model    string
	// ModelConfig is the model_config column when it holds a JSON
	// object.
	ModelConfig json.RawMessage
	// SystemPrompt is the prompt column of the row, and
	// SystemPromptHash the key of system_prompts it names, from schema
	// version 25.
	SystemPrompt     string
	SystemPromptHash string
	Started          time.Time
	Ended            time.Time
	EndReason        string
	SessionKey       string
	CWD              string
	GitBranch        string
	GitRepoRoot      string
	Title            string

	// Cols is the row as read.
	Cols Columns
	// Problems lists the columns that do not hold what Hermes writes,
	// each kept as written.
	Problems []string
	// Err says why the row cannot be read; only Unit and, when the row
	// gave one, ID are then set.
	Err error
}

// Marker returns a model_config marker naming another session, such as
// _branched_from; empty when the config holds none.
func (s *Session) Marker(name string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(s.ModelConfig, &m) != nil {
		return ""
	}
	var v string
	_ = json.Unmarshal(m[name], &v)
	return v
}

// DecodeSessionRow reads a row of the sessions table.
func DecodeSessionRow(unit string, cols Columns) *Session {
	s := &Session{Unit: unit, Cols: cols}
	s.ID = cols.String("id")
	if s.ID == "" {
		s.Err = errors.New("the row has no id")
		return s
	}
	s.Source, s.ParentID, s.Model = cols.String("source"), cols.String("parent_session_id"), cols.String("model")
	if mc, ok := cols.Text("model_config"); ok && mc != "" {
		if isObject([]byte(mc)) {
			s.ModelConfig = json.RawMessage(mc)
		} else {
			s.Problems = append(s.Problems, "model_config")
		}
	}
	s.SystemPrompt, s.SystemPromptHash = cols.String("system_prompt"), cols.String("system_prompt_hash")
	if f, ok := cols.Float("started_at"); ok {
		if t, ok := Epoch(f); ok {
			s.Started = t
		}
	}
	if s.Started.IsZero() && cols.Has("started_at") {
		s.Problems = append(s.Problems, "started_at")
	}
	if f, ok := cols.Float("ended_at"); ok {
		s.Ended, _ = Epoch(f)
	}
	s.EndReason, s.SessionKey = cols.String("end_reason"), cols.String("session_key")
	s.CWD, s.GitBranch, s.GitRepoRoot = cols.String("cwd"), cols.String("git_branch"), cols.String("git_repo_root")
	s.Title = cols.String("title")
	return s
}

// Epoch is a Hermes timestamp, REAL seconds since the epoch, as a time;
// false outside the window Hermes itself accepts (hermes_cli.timefmt's
// EPOCH_MIN and EPOCH_MAX, 0 to 4.2e9), where a damaged or foreign cell
// lies.
func Epoch(f float64) (time.Time, bool) {
	if math.IsNaN(f) || f < 0 || f > 4.2e9 {
		return time.Time{}, false
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(math.Round(frac*1e6))*1000).UTC(), true
}

// Content is a message's content as Hermes stores it: text, or JSON
// behind [ContentPrefix], multimodal parts or a dict.
type Content struct {
	// Text is the content when it is text.
	Text string
	// Parts holds structured content, each part as written; a dict is
	// one part.
	Parts []json.RawMessage
	// Structured says the content is Parts; Null that the column is
	// null.
	Structured, Null bool
}

// DecodeContent reads a content column as SessionDB._decode_content
// does: text behind ContentPrefix is JSON, and a reserved prefix
// holding no valid JSON stays the text it is. ok is false for such
// text.
func DecodeContent(v any, present bool) (c Content, ok bool) {
	var s string
	switch v := v.(type) {
	case nil:
		return Content{Null: true}, true
	case string:
		s = v
	case []byte:
		s = string(v)
	case int64, float64:
		raw, _ := json.Marshal(v)
		return Content{Text: string(raw)}, true
	default:
		return Content{Null: true}, true
	}
	if !strings.HasPrefix(s, ContentPrefix) {
		return Content{Text: s}, true
	}
	body := []byte(s[len(ContentPrefix):])
	if !json.Valid(body) {
		return Content{Text: s}, false
	}
	body = bytes.TrimSpace(body)
	switch body[0] {
	case '"':
		var t string
		_ = json.Unmarshal(body, &t)
		return Content{Text: t}, true
	case '[':
		var parts []json.RawMessage
		_ = json.Unmarshal(body, &parts)
		if parts == nil {
			parts = []json.RawMessage{}
		}
		return Content{Parts: parts, Structured: true}, true
	case '{':
		return Content{Parts: []json.RawMessage{json.RawMessage(body)}, Structured: true}, true
	case 'n':
		return Content{Null: true}, true
	}
	return Content{Text: string(body)}, true
}

// String is the content's text: the text, or the text parts joined.
func (c Content) String() string {
	if !c.Structured {
		return c.Text
	}
	var b strings.Builder
	for _, p := range c.Parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(p, &part) == nil && part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

// Message is one row of the messages table: the columns a converter
// reads, and the row itself.
type Message struct {
	// Unit names the row in findings: <database>:messages/<id>.
	Unit string

	ID        int64
	SessionID string
	Role      string
	Content   Content
	// ToolCalls is an assistant's calls, those that decoded; BadCalls
	// the entries of the column that are not objects.
	ToolCalls  []*ToolCall
	BadCalls   []json.RawMessage
	ToolCallID string
	ToolName   string
	Time       time.Time
	// FinishReason is the provider's, as Hermes recorded it.
	FinishReason string
	// Reasoning and ReasoningContent are the reasoning texts; a nil
	// ReasoningContent is a null column, which Hermes tells from an
	// empty one.
	Reasoning        string
	ReasoningContent *string
	// ReasoningDetails, CodexReasoningItems and CodexMessageItems are
	// the reasoning carriers, as JSON arrays when they hold one.
	ReasoningDetails    json.RawMessage
	CodexReasoningItems json.RawMessage
	CodexMessageItems   json.RawMessage
	// APIContent is the text Hermes sent in place of the content, and
	// HasAPIContent says the row holds a non-empty one.
	APIContent    string
	HasAPIContent bool
	DisplayKind   string
	// DisplayMetadata is the display_metadata column when it holds a
	// JSON object, once unwrapped from the string layer rows written
	// before Hermes guarded it hold.
	DisplayMetadata json.RawMessage
	// Active and Compacted are the row's flags; a database without
	// the columns has every row active. Summary is
	// _compressed_summary, and HasSummary says the database has the
	// column.
	Active, Compacted   bool
	Summary, HasSummary bool
	Observed            bool

	// Cols is the row as read.
	Cols Columns
	// Problems lists the columns that do not hold what Hermes writes,
	// each kept as written and read as Hermes reads it.
	Problems []string
	// Err says why the row cannot be read.
	Err error
}

// DecodeMessageRow reads a row of the messages table.
func DecodeMessageRow(unit string, cols Columns) *Message {
	m := &Message{Unit: unit, Cols: cols}
	id, ok := cols.Int("id")
	if !ok {
		m.Err = errors.New("the row has no integer id")
		return m
	}
	m.ID = id
	m.SessionID = cols.String("session_id")
	m.Role = cols.String("role")
	if m.Role == "" {
		m.Err = errors.New("the row has no role")
		return m
	}
	v, present := cols.Get("content")
	c, ok := DecodeContent(v, present)
	if !ok {
		m.Problems = append(m.Problems, "content")
	}
	m.Content = c
	if raw, ok := cols.Text("tool_calls"); ok && raw != "" {
		calls, bad, err := DecodeToolCalls([]byte(raw))
		if err != nil {
			m.Problems = append(m.Problems, "tool_calls")
		}
		m.ToolCalls, m.BadCalls = calls, bad
	}
	m.ToolCallID, m.ToolName = cols.String("tool_call_id"), cols.String("tool_name")
	if f, ok := cols.Float("timestamp"); ok {
		m.Time, _ = Epoch(f)
	}
	m.FinishReason = cols.String("finish_reason")
	m.Reasoning = cols.String("reasoning")
	if rc, ok := cols.Text("reasoning_content"); ok {
		m.ReasoningContent = &rc
	}
	m.ReasoningDetails = m.jsonArray(cols, "reasoning_details")
	m.CodexReasoningItems = m.jsonArray(cols, "codex_reasoning_items")
	m.CodexMessageItems = m.jsonArray(cols, "codex_message_items")
	if ac, ok := cols.Text("api_content"); ok && ac != "" {
		m.APIContent, m.HasAPIContent = ac, true
	}
	m.DisplayKind = cols.String("display_kind")
	if raw, ok := cols.Text("display_metadata"); ok {
		meta, ok := displayMetadata(raw)
		if ok {
			m.DisplayMetadata = meta
		} else {
			m.Problems = append(m.Problems, "display_metadata")
		}
	}
	m.Active = cols.Flag("active", true)
	m.Compacted = cols.Flag("compacted", false)
	m.HasSummary = cols.Has("_compressed_summary")
	m.Summary = cols.Flag("_compressed_summary", false)
	m.Observed = cols.Flag("observed", false)
	return m
}

// jsonArray reads a reasoning carrier: a JSON array, or nil. Hermes
// falls back to nothing for one that does not decode, and so does this;
// the column is listed in Problems.
func (m *Message) jsonArray(cols Columns, name string) json.RawMessage {
	raw, ok := cols.Text(name)
	if !ok || raw == "" {
		return nil
	}
	t := bytes.TrimSpace([]byte(raw))
	if !json.Valid(t) {
		m.Problems = append(m.Problems, name)
		return nil
	}
	switch t[0] {
	case '[':
		return json.RawMessage(t)
	case 'n':
		return nil
	case '{':
		// A single object: Hermes' readers take a list, and _as_list
		// wraps one in it.
		return json.RawMessage("[" + string(t) + "]")
	}
	m.Problems = append(m.Problems, name)
	return nil
}

// displayMetadata decodes display_metadata as
// SessionMessagesMixin._decode_display_metadata does: an object, under
// at most one extra layer of string.
func displayMetadata(raw string) (json.RawMessage, bool) {
	data := []byte(raw)
	for range 2 {
		t := bytes.TrimSpace(data)
		if !json.Valid(t) {
			return nil, false
		}
		if isObject(t) {
			return json.RawMessage(t), true
		}
		var s string
		if json.Unmarshal(t, &s) != nil {
			return nil, false
		}
		data = []byte(s)
	}
	return nil, false
}

// Delegation is the parent session of a background delegation, from a
// row of async_delegations.
type Delegation struct {
	ID              string
	ParentSessionID string
	OriginSessionID string
	State           string
	Cols            Columns
}

// Stray is a row read that belongs to no session it can be converted
// with, or that cannot be read.
type Stray struct {
	Unit string
	// Session names the session it was read with, when it was.
	Session string
	// Kind says what the row is: "session" or "message".
	Kind string
	Why  string
	// Malformed says the row could not be read at all.
	Malformed bool
}

// Store is the rows of one Hermes database. Rows are read on demand, a
// session at a time.
type Store interface {
	// Name names the store in findings: the database's file name.
	Name() string
	// Version is the schema version the database records; false when
	// it records none.
	Version() (int, bool)
	// Sessions lists every row of sessions, and the rows that cannot
	// be read as one.
	Sessions() ([]*Session, []*Stray, error)
	// Messages reads a session's rows of messages in id order, Hermes'
	// order, never the timestamps'.
	Messages(sessionID string) ([]*Message, error)
	// SystemPrompt reads system_prompts' prompt of a hash.
	SystemPrompt(hash string) (string, bool)
	// ModelUsage reads a session's rows of session_model_usage.
	ModelUsage(sessionID string) ([]Columns, error)
	// Delegations reads async_delegations.
	Delegations() ([]*Delegation, error)
	// Orphans lists the rows of messages whose session is none of the
	// set.
	Orphans(sessions map[string]bool) ([]*Stray, error)
}

// Database is a store read from a database file, open until closed.
type Database interface {
	Store
	Close() error
}

// isObject reports whether data is a JSON object.
func isObject(data []byte) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}

// IsNull reports whether a raw value is absent or null.
func IsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}
