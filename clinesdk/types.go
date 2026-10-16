package clinesdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value.
type Members = jsonline.Members

// Roles of a message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Content block types (@cline/shared's llms/messages.ts).
const (
	BlockText             = "text"
	BlockFile             = "file"
	BlockImage            = "image"
	BlockMedia            = "media"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
)

// Agent kinds a messages file names.
const (
	AgentLead     = "lead"
	AgentSubagent = "subagent"
	AgentTeammate = "teammate"
)

// Manifest is a session's <id>.json: the session record Cline writes
// beside its history (SessionManifest, version 1). Cline keeps the same
// record, with the sub-agent parentage the manifest leaves out, as a
// row of its index.
type Manifest struct {
	Version        json.RawMessage `json:"version"`
	SessionID      string          `json:"session_id"`
	Source         string          `json:"source"`
	PID            json.RawMessage `json:"pid"`
	StartedAt      string          `json:"started_at"`
	EndedAt        string          `json:"ended_at"`
	ExitCode       json.RawMessage `json:"exit_code"`
	Status         string          `json:"status"`
	Interactive    json.RawMessage `json:"interactive"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model"`
	CWD            string          `json:"cwd"`
	WorkspaceRoot  string          `json:"workspace_root"`
	TeamName       string          `json:"team_name"`
	EnableTools    json.RawMessage `json:"enable_tools"`
	EnableSpawn    json.RawMessage `json:"enable_spawn"`
	EnableTeams    json.RawMessage `json:"enable_teams"`
	Prompt         string          `json:"prompt"`
	Metadata       json.RawMessage `json:"metadata"`
	MessagesPath   string          `json:"messages_path"`
	CompactionPath string          `json:"compaction_path"`

	Members
}

type plainManifest Manifest

// UnmarshalJSON decodes the manifest, keeping undeclared members and
// members of another JSON type. A value that is not an object fails.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainManifest)(m), &m.Members)
}

// MarshalJSON writes the manifest back as it was read.
func (m Manifest) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainManifest(m), &m.Members)
}

// Started is the session's start time, started_at.
func (m *Manifest) Started() (time.Time, bool) { return ISOTime(m.StartedAt) }

// Meta is the manifest's metadata object; nil when it has none.
func (m *Manifest) Meta() map[string]json.RawMessage { return object(m.Metadata) }

// Title is the session's title, metadata.title.
func (m *Manifest) Title() string { return stringOf(m.Meta()["title"]) }

// Fork is metadata.fork: the session it was forked from, and when.
// nil when the session is no fork.
func (m *Manifest) Fork() *Fork {
	raw := m.Meta()["fork"]
	o := object(raw)
	if o == nil {
		return nil
	}
	f := &Fork{From: stringOf(o["forkedFromSessionId"]), At: stringOf(o["forkedAt"]), Source: stringOf(o["source"]), Raw: raw}
	if f.From == "" {
		return nil
	}
	return f
}

// ImportedFrom is metadata.importedFrom: the other agent's session Cline
// imported this one from, raw; nil when it was not imported. Cline
// reads it only when it has a tool and a source session ID.
func (m *Manifest) ImportedFrom() json.RawMessage {
	raw := m.Meta()["importedFrom"]
	o := object(raw)
	if o == nil || stringOf(o["tool"]) == "" || stringOf(o["sourceSessionId"]) == "" {
		return nil
	}
	return raw
}

// LegacyTask reports whether the metadata marks the session as a
// classic task the VS Code extension carried on: legacyTask or
// migratedFromLegacyTask true.
func (m *Manifest) LegacyTask() bool {
	meta := m.Meta()
	return isTrue(meta["legacyTask"]) || isTrue(meta["migratedFromLegacyTask"])
}

// Fork is a fork's metadata.fork.
type Fork struct {
	// From is forkedFromSessionId, At forkedAt (ISO), Source the
	// source session's source.
	From, At, Source string
	// Raw is the member as written.
	Raw json.RawMessage
}

// When is the time the fork was made.
func (f *Fork) When() (time.Time, bool) { return ISOTime(f.At) }

// Origin is a messages file's origin: the session's source and history
// mode, its parent thread and agent for a sub-agent, and the client's
// version.
type Origin struct {
	Source         string `json:"source"`
	Mode           string `json:"mode"`
	SessionID      string `json:"sessionId"`
	ParentThreadID string `json:"parentThreadId"`
	Subagent       string `json:"subagent"`
	Version        string `json:"version"`
	Trigger        string `json:"trigger"`

	Members
}

type plainOrigin Origin

// UnmarshalJSON decodes the origin, keeping undeclared members.
func (o *Origin) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainOrigin)(o), &o.Members)
}

// MarshalJSON writes the origin back as it was read.
func (o Origin) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainOrigin(o), &o.Members)
}

// Message is one element of a messages array: MessageWithMetadata, a
// provider-neutral message with Cline's members.
type Message struct {
	Role      string          `json:"role"`
	Content   Content         `json:"content"`
	ID        string          `json:"id"`
	Agent     string          `json:"agent"`
	SessionID string          `json:"sessionId"`
	Metadata  json.RawMessage `json:"metadata"`
	ModelInfo *ModelInfo      `json:"modelInfo"`
	Metrics   *Metrics        `json:"metrics"`
	TS        json.RawMessage `json:"ts"`

	Members

	// Elem is the element as read, whatever it holds.
	Elem json.RawMessage `json:"-"`
	// Err is why the element did not decode: it is not an object, or
	// its members do not fit; nothing else is then set.
	Err error `json:"-"`
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members and
// members of another JSON type; an element that is not an object is
// kept with Err set.
func (m *Message) UnmarshalJSON(data []byte) error {
	raw := bytes.Clone(data)
	if !isObject(data) {
		*m = Message{Elem: raw, Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainMessage)(m), &m.Members); err != nil {
		*m = Message{Elem: raw, Err: err}
		return nil
	}
	m.Elem = raw
	return nil
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.Err != nil {
		return m.Elem, nil
	}
	return jsonline.Encode(plainMessage(m), &m.Members)
}

// Time is the message's ts, ms since the epoch; false when it has none.
func (m *Message) Time() (time.Time, bool) { return Millis(m.TS) }

// Meta is the message's metadata object; nil when it has none.
func (m *Message) Meta() map[string]json.RawMessage { return object(m.Metadata) }

// DisplayOnly reports whether the message is metadata.displayOnly: a
// message the chat view shows and no request sends.
func (m *Message) DisplayOnly() bool { return isTrue(m.Meta()["displayOnly"]) }

// Kind is metadata.kind, such as "compaction_summary".
func (m *Message) Kind() string { return stringOf(m.Meta()["kind"]) }

// IsCompactionSummary reports whether the message is a compaction's
// summary, a user message of metadata.kind "compaction_summary".
func (m *Message) IsCompactionSummary() bool {
	return m.Err == nil && m.Role == RoleUser && m.Kind() == "compaction_summary"
}

// Blocks returns the message's content as blocks: its blocks, or one
// text block for string content. Content that is neither gives none.
func (m *Message) Blocks() []*Block {
	if m.Content.IsText {
		return []*Block{{Type: BlockText, Text: m.Content.Text}}
	}
	return m.Content.Blocks
}

// ModelInfo is the model an assistant message was written with.
type ModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Family   string `json:"family"`

	Members
}

type plainModelInfo ModelInfo

// UnmarshalJSON decodes the model info, keeping undeclared members.
func (i *ModelInfo) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainModelInfo)(i), &i.Members)
}

// MarshalJSON writes the model info back as it was read.
func (i ModelInfo) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainModelInfo(i), &i.Members)
}

// Metrics is an assistant message's usage. inputTokens is the AI SDK's
// total input, cache reads and writes included. Counts are kept as
// written.
type Metrics struct {
	InputTokens      json.RawMessage `json:"inputTokens"`
	OutputTokens     json.RawMessage `json:"outputTokens"`
	CacheReadTokens  json.RawMessage `json:"cacheReadTokens"`
	CacheWriteTokens json.RawMessage `json:"cacheWriteTokens"`
	Cost             json.RawMessage `json:"cost"`

	Members
}

type plainMetrics Metrics

// UnmarshalJSON decodes the metrics, keeping undeclared members.
func (x *Metrics) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMetrics)(x), &x.Members)
}

// MarshalJSON writes the metrics back as they were read.
func (x Metrics) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMetrics(x), &x.Members)
}

// Content is a message's content: a string, or a list of blocks, each
// decoded on its own.
type Content struct {
	Text   string
	IsText bool
	Blocks []*Block
	// Raw and Err are set for content that is neither: a number, an
	// object, null. Raw is written back.
	Raw json.RawMessage
	Err error
}

// UnmarshalJSON accepts a string or an array of blocks; anything else
// is kept in Raw with Err set.
func (c *Content) UnmarshalJSON(data []byte) error {
	*c = Content{}
	t := bytes.TrimSpace(data)
	switch {
	case len(t) > 0 && t[0] == '"':
		c.IsText = true
		if err := json.Unmarshal(t, &c.Text); err != nil {
			*c = Content{Raw: bytes.Clone(data), Err: err}
		}
		return nil
	case len(t) > 0 && t[0] == '[':
		var raws []json.RawMessage
		if err := json.Unmarshal(t, &raws); err != nil {
			*c = Content{Raw: bytes.Clone(data), Err: err}
			return nil
		}
		c.Blocks = make([]*Block, len(raws))
		for i, raw := range raws {
			b := &Block{}
			_ = b.UnmarshalJSON(raw)
			c.Blocks[i] = b
		}
		return nil
	}
	*c = Content{Raw: bytes.Clone(data), Err: errors.New("content that is neither a string nor an array")}
	return nil
}

// MarshalJSON writes the string, the blocks, or what was read.
func (c Content) MarshalJSON() ([]byte, error) {
	switch {
	case c.Err != nil:
		return c.Raw, nil
	case c.IsText:
		return json.Marshal(c.Text)
	case c.Blocks == nil:
		return []byte("[]"), nil
	}
	return json.Marshal(c.Blocks)
}

// IsZero reports whether the content was absent, so it is not written
// back.
func (c Content) IsZero() bool { return !c.IsText && c.Blocks == nil && c.Err == nil }

// Block is one content block. Type says which members apply.
type Block struct {
	Type string `json:"type"`

	// text; a thinking block's and a tool_use's Gemini thought
	// signature, a thinking block's Anthropic signature.
	Text      string `json:"text"`
	Signature string `json:"signature"`
	// file: content and path; tool_result: content, a string or an
	// array of text, image and file blocks or structured results.
	Content json.RawMessage `json:"content"`
	Path    string          `json:"path"`
	// image (base64 data and its media type); redacted_thinking's data.
	Data      string `json:"data"`
	MediaType string `json:"mediaType"`
	// media: model-generated media.
	Media json.RawMessage `json:"media"`
	// tool_use
	ID     string          `json:"id"`
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	// thinking
	Thinking string          `json:"thinking"`
	Details  json.RawMessage `json:"details"`
	Summary  json.RawMessage `json:"summary"`

	Members

	// Raw and Err are set for a block that is not an object, or whose
	// members do not decode as declared.
	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainBlock Block

// UnmarshalJSON decodes a block, keeping undeclared members and those
// of another JSON type. It never fails: a value that is not an object
// is kept in Raw with Err set.
func (b *Block) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		*b = Block{Raw: bytes.Clone(data), Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainBlock)(b), &b.Members); err != nil {
		*b = Block{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the block back as it was read.
func (b Block) MarshalJSON() ([]byte, error) {
	if b.Err != nil {
		return b.Raw, nil
	}
	return jsonline.Encode(plainBlock(b), &b.Members)
}

// FileText is a file block's content; false when it is not a string.
func (b *Block) FileText() (string, bool) {
	var s string
	if json.Unmarshal(b.Content, &s) != nil {
		return "", false
	}
	return s, true
}

// MessagesFile is a <id>.messages.json: the canonical history of a
// session, or of a sub-agent or team task, version 1. Before the
// version the file was a bare array of messages, which Cline still
// reads.
type MessagesFile struct {
	// Name is the file's name in the session directory.
	Name string `json:"-"`

	Version      json.RawMessage `json:"version"`
	UpdatedAt    string          `json:"updated_at"`
	Agent        string          `json:"agent"`
	SessionID    string          `json:"sessionId"`
	TaskType     string          `json:"taskType"`
	Origin       *Origin         `json:"origin"`
	SystemPrompt *string         `json:"system_prompt"`

	Members

	// Bare says the file is a bare array of messages, with no envelope.
	Bare bool `json:"-"`
	// Messages are the elements of the messages array, in order.
	Messages []*Message `json:"-"`
	// HasMessages says the envelope has a messages member that is an
	// array.
	HasMessages bool `json:"-"`
	// Err is set when the file could not be read whole: it does not
	// parse, it is not an object or an array, or it is cut short.
	// Messages then holds the elements that end before the fault.
	Err error `json:"-"`
}

type plainMessagesFile MessagesFile

// MarshalJSON writes the envelope and its messages back.
func (f MessagesFile) MarshalJSON() ([]byte, error) {
	if f.Bare {
		return marshalMessages(f.Messages)
	}
	g := f
	g.Members.Extra = map[string]json.RawMessage{}
	for k, v := range f.Members.Extra {
		g.Members.Extra[k] = v
	}
	if f.HasMessages {
		raw, err := marshalMessages(f.Messages)
		if err != nil {
			return nil, err
		}
		g.Members.Extra["messages"] = raw
	}
	return jsonline.Encode(plainMessagesFile(g), &g.Members)
}

func marshalMessages(ms []*Message) ([]byte, error) {
	if ms == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(ms)
}

// VersionNumber is the file's version; false when it records none or
// one that is not a number.
func (f *MessagesFile) VersionNumber() (int64, bool) {
	n, exact, ok := Int(f.Version)
	return n, ok && exact
}

// Compaction is a <id>.compaction.json: the working context Cline's
// latest compaction left, which replaces the first SourceMessageCount
// messages of the canonical history that are not display-only when
// SourcePrefixHash still matches them (SessionCompactionState, version
// 1). Earlier compactions are overwritten.
type Compaction struct {
	Version              json.RawMessage `json:"version"`
	UpdatedAt            string          `json:"updated_at"`
	ConversationID       string          `json:"conversation_id"`
	SourceMessageCount   json.RawMessage `json:"source_message_count"`
	SourcePrefixHash     string          `json:"source_prefix_hash"`
	SourceLastMessageKey string          `json:"source_last_message_key"`
	SystemPrompt         *string         `json:"system_prompt"`

	Members

	// Name is the file's name.
	Name string `json:"-"`
	// Messages are the compacted context's messages.
	Messages    []*Message `json:"-"`
	HasMessages bool       `json:"-"`
	// Err is set when the file could not be read whole.
	Err error `json:"-"`
}

type plainCompaction Compaction

// MarshalJSON writes the sidecar and its messages back.
func (c Compaction) MarshalJSON() ([]byte, error) {
	g := c
	g.Members.Extra = map[string]json.RawMessage{}
	for k, v := range c.Members.Extra {
		g.Members.Extra[k] = v
	}
	if c.HasMessages {
		raw, err := marshalMessages(c.Messages)
		if err != nil {
			return nil, err
		}
		g.Members.Extra["messages"] = raw
	}
	return jsonline.Encode(plainCompaction(g), &g.Members)
}

// Count is source_message_count; false when it is not a non-negative
// integer.
func (c *Compaction) Count() (int, bool) {
	n, exact, ok := Int(c.SourceMessageCount)
	if !ok || !exact || n < 0 || n > math.MaxInt32 || isQuoted(c.SourceMessageCount) {
		return 0, false
	}
	return int(n), true
}

// Millis reads a ms epoch timestamp written as a number; false for
// anything else, or one outside the years 1970 to 9999.
func Millis(raw json.RawMessage) (time.Time, bool) {
	n, ok := Float(raw)
	if !ok || isQuoted(raw) || n <= 0 || n > 253402300799999 {
		return time.Time{}, false
	}
	ms := int64(n)
	return time.UnixMilli(ms).UTC().Add(time.Duration((n - float64(ms)) * float64(time.Millisecond))), true
}

// ISOTime reads an ISO 8601 time as Cline writes it, Date's
// toISOString; false for anything else.
func ISOTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// Int reads an integer written as a number, possibly 12.0, or a string
// of one. exact is false when a fraction was dropped.
func Int(raw json.RawMessage) (n int64, exact, ok bool) {
	f, ok := Float(raw)
	if !ok || f >= math.MaxInt64 || f < math.MinInt64 {
		return 0, false, false
	}
	return int64(f), f == math.Trunc(f), true
}

// Float reads a number written as a number or a string of one.
func Float(raw json.RawMessage) (float64, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return 0, false
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) != nil {
			return 0, false
		}
		t = []byte(strings.TrimSpace(s))
	}
	f, err := strconv.ParseFloat(string(t), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// IsNull reports whether raw is absent or null.
func IsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func isQuoted(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

// errNotObject is the error for an element or block that is not a JSON
// object.
var errNotObject = errors.New("an element that is not an object")

// isObject reports whether data is a JSON object, as far as its first
// byte tells.
func isObject(data []byte) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && t[0] == '{'
}

// object decodes raw as an object of raw members; nil for anything
// else.
func object(raw json.RawMessage) map[string]json.RawMessage {
	if !isObject(raw) {
		return nil
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	return o
}

// stringOf is raw as a string; "" when it is not one.
func stringOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// isTrue reports whether raw is the JSON true.
func isTrue(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "true" }
