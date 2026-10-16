package opencode

import (
	"bytes"
	"encoding/json"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Types of a V2 session_message row, SessionMessage.Info.
const (
	TypeUser             = "user"
	TypeSynthetic        = "synthetic"
	TypeSystem           = "system"
	TypeSkill            = "skill"
	TypeShell            = "shell"
	TypeAssistant        = "assistant"
	TypeCompaction       = "compaction"
	TypeAgentSwitched    = "agent-switched"
	TypeModelSwitched    = "model-switched"
	TypeLocationSwitched = "location-switched"
	TypeIdle             = "idle"
)

// KnownTypes lists the row types OpenCode 2 defines.
var KnownTypes = []string{
	TypeUser, TypeSynthetic, TypeSystem, TypeSkill, TypeShell, TypeAssistant,
	TypeCompaction, TypeAgentSwitched, TypeModelSwitched, TypeLocationSwitched, TypeIdle,
}

// IsKnownType reports whether OpenCode 2 defines row type t.
func IsKnownType(t string) bool {
	for _, k := range KnownTypes {
		if k == t {
			return true
		}
	}
	return false
}

// Compaction statuses of V2.
const (
	CompactionRunning   = "running"
	CompactionCompleted = "completed"
	CompactionFailed    = "failed"
)

// Message2 is the data of a V2 session_message row: the message without
// its id and type, which the row's columns carry. Which members it has
// depends on the row's type; a member one type declares with another
// JSON type than another type does is kept raw.
type Message2 struct {
	Metadata json.RawMessage `json:"metadata"`
	Time     *Time2          `json:"time"`

	// user, synthetic, system, skill
	Text        string            `json:"text"`
	Description string            `json:"description"`
	Files       []json.RawMessage `json:"files"`
	Agents      json.RawMessage   `json:"agents"`
	Skills      []json.RawMessage `json:"skills"`
	Skill       string            `json:"skill"`
	Name        string            `json:"name"`

	// shell
	ShellID string          `json:"shellID"`
	Command string          `json:"command"`
	Status  string          `json:"status"`
	Exit    json.RawMessage `json:"exit"`
	Output  json.RawMessage `json:"output"`

	// assistant, and the switches
	Agent         string            `json:"agent"`
	Model         *ModelRef         `json:"model"`
	Previous      json.RawMessage   `json:"previous"`
	Content       []json.RawMessage `json:"content"`
	Snapshot      json.RawMessage   `json:"snapshot"`
	Finish        string            `json:"finish"`
	RawFinish     string            `json:"rawFinish"`
	ProviderState json.RawMessage   `json:"providerState"`
	Cost          json.RawMessage   `json:"cost"`
	Tokens        *Tokens           `json:"tokens"`
	Error         *SessionError     `json:"error"`
	Retry         json.RawMessage   `json:"retry"`

	// compaction
	Reason          string          `json:"reason"`
	Summary         string          `json:"summary"`
	Recent          string          `json:"recent"`
	ProviderContext json.RawMessage `json:"providerContext"`

	// location-switched
	Location  json.RawMessage `json:"location"`
	ProjectID string          `json:"projectID"`
	Subpath   string          `json:"subpath"`

	// idle
	Outcome string `json:"outcome"`

	Members
}

type plainMessage2 Message2

// UnmarshalJSON decodes a row's data, keeping undeclared members and
// members of another JSON type.
func (m *Message2) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMessage2)(m), &m.Members)
}

// MarshalJSON writes the data back as it was read.
func (m Message2) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessage2(m), &m.Members)
}

// Time2 is a V2 message's time.
type Time2 struct {
	Created   json.RawMessage `json:"created"`
	Streamed  json.RawMessage `json:"streamed"`
	Completed json.RawMessage `json:"completed"`

	Members
}

type plainTime2 Time2

// UnmarshalJSON decodes the times, keeping undeclared members.
func (t *Time2) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainTime2)(t), &t.Members)
}

// MarshalJSON writes the times back as they were read.
func (t Time2) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainTime2(t), &t.Members)
}

// SessionError is V2's error: a type, such as provider.rate-limit,
// permission.rejected or aborted, and a message.
type SessionError struct {
	Type    string `json:"type"`
	Message string `json:"message"`

	Members
}

type plainSessionError SessionError

// UnmarshalJSON decodes the error, keeping undeclared members.
func (e *SessionError) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainSessionError)(e), &e.Members)
}

// MarshalJSON writes the error back as it was read.
func (e SessionError) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainSessionError(e), &e.Members)
}

// Assistant content types of V2.
const (
	ContentText      = "text"
	ContentReasoning = "reasoning"
	ContentTool      = "tool"
)

// Content2 is one item of a V2 assistant's content: text, reasoning or a
// tool. State is the provider state of text and reasoning, and the
// ToolState2 of a tool, which [Content2.Tool] decodes.
type Content2 struct {
	Type                string          `json:"type"`
	Text                string          `json:"text"`
	State               json.RawMessage `json:"state"`
	Time                json.RawMessage `json:"time"`
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	Executed            bool            `json:"executed"`
	ProviderState       json.RawMessage `json:"providerState"`
	ProviderResultState json.RawMessage `json:"providerResultState"`

	Members
}

type plainContent2 Content2

// UnmarshalJSON decodes an item, keeping undeclared members and
// members of another JSON type.
func (c *Content2) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainContent2)(c), &c.Members)
}

// MarshalJSON writes the item back as it was read.
func (c Content2) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainContent2(c), &c.Members)
}

// Tool decodes a tool item's state.
func (c *Content2) Tool() (*ToolState2, error) {
	s := &ToolState2{}
	if err := s.UnmarshalJSON(c.State); err != nil {
		return nil, err
	}
	return s, nil
}

// ReasoningDone reports whether a reasoning item's time says it
// completed; an item with no time is taken as completed, as OpenCode
// reads it.
func (c *Content2) ReasoningDone() bool {
	if IsNull(c.Time) {
		return true
	}
	var t struct {
		Completed json.RawMessage `json:"completed"`
	}
	if json.Unmarshal(c.Time, &t) != nil {
		return true
	}
	return !IsNull(t.Completed)
}

// Tool states of V2.
const (
	Tool2Streaming = "streaming"
	Tool2Running   = "running"
	Tool2Completed = "completed"
	Tool2Error     = "error"
)

// ToolState2 is a V2 tool's state: streaming holds input as a string,
// the others as an object; completed holds content, error an error and
// perhaps content.
type ToolState2 struct {
	Status   string            `json:"status"`
	Input    json.RawMessage   `json:"input"`
	Content  []json.RawMessage `json:"content"`
	Error    *SessionError     `json:"error"`
	Metadata json.RawMessage   `json:"metadata"`

	Members
}

type plainToolState2 ToolState2

// UnmarshalJSON decodes the state, keeping undeclared members and
// members of another JSON type.
func (s *ToolState2) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainToolState2)(s), &s.Members)
}

// MarshalJSON writes the state back as it was read.
func (s ToolState2) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainToolState2(s), &s.Members)
}

// Task reads a subagent tool state's metadata: the child session, and
// whether it runs in the background.
func (s *ToolState2) Task() TaskMetadata { return taskMetadata(s.Metadata) }

// ToolContent is one item of a V2 tool's content: text, or a file by
// URI.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URI  string `json:"uri"`
	Mime string `json:"mime"`
	Name string `json:"name"`
}

// DecodeToolContent reads one content item; false when it is not an
// object.
func DecodeToolContent(raw json.RawMessage) (ToolContent, bool) {
	var c ToolContent
	if !isObject(raw) || json.Unmarshal(raw, &c) != nil {
		return ToolContent{}, false
	}
	return c, true
}

// File2 is a V2 user message's attachment, FileAttachment: base64 data,
// its mime type, where it came from, and how it was mentioned.
type File2 struct {
	Data        string `json:"data"`
	Mime        string `json:"mime"`
	Name        string `json:"name"`
	Description *string
	Source      struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"source"`
	Mention *struct {
		Text string `json:"text"`
	} `json:"mention"`
}

// DecodeFile2 reads an attachment; false when it is not an object.
func DecodeFile2(raw json.RawMessage) (File2, bool) {
	var f struct {
		Data        string          `json:"data"`
		Mime        string          `json:"mime"`
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
		Source      json.RawMessage `json:"source"`
		Mention     json.RawMessage `json:"mention"`
	}
	if !isObject(raw) || json.Unmarshal(raw, &f) != nil {
		return File2{}, false
	}
	out := File2{Data: f.Data, Mime: f.Mime, Name: f.Name}
	var d string
	if json.Unmarshal(f.Description, &d) == nil && !IsNull(f.Description) {
		out.Description = &d
	}
	_ = json.Unmarshal(f.Source, &out.Source)
	if isObject(f.Mention) {
		m := &struct {
			Text string `json:"text"`
		}{}
		if json.Unmarshal(f.Mention, m) == nil {
			out.Mention = m
		}
	}
	return out, true
}

// SkillText reads a V2 user message's skill: its text, when it carries
// one.
func SkillText(raw json.RawMessage) (string, bool) {
	var s struct {
		Text *string `json:"text"`
	}
	if !isObject(raw) || json.Unmarshal(raw, &s) != nil || s.Text == nil {
		return "", false
	}
	return *s.Text, true
}

// LocationDirectory reads a location-switched row's location.directory.
func LocationDirectory(raw json.RawMessage) string {
	var l struct {
		Directory string `json:"directory"`
	}
	_ = json.Unmarshal(raw, &l)
	return l.Directory
}

// ShellOutput reads a shell row's output.output.
func ShellOutput(raw json.RawMessage) string {
	var o struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.Output
}

// MetadataBool reads a boolean member of a metadata object.
func MetadataBool(raw json.RawMessage, name string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	return string(bytes.TrimSpace(m[name])) == "true"
}

// MetadataString reads a string member of a metadata object.
func MetadataString(raw json.RawMessage, name string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(m[name], &s)
	return s
}
