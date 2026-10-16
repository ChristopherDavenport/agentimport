package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value.
type Members = jsonline.Members

// errNotObject is the error for a record that is not a JSON object.
var errNotObject = errors.New("not a JSON object")

// Message roles of the V1 model.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Part types of the V1 model, MessageV2.Part.
const (
	PartText       = "text"
	PartReasoning  = "reasoning"
	PartFile       = "file"
	PartAgent      = "agent"
	PartCompaction = "compaction"
	PartSubtask    = "subtask"
	PartRetry      = "retry"
	PartStepStart  = "step-start"
	PartStepFinish = "step-finish"
	PartSnapshot   = "snapshot"
	PartPatch      = "patch"
	PartTool       = "tool"
)

// KnownParts lists the part types OpenCode's V1 model defines.
var KnownParts = []string{
	PartText, PartReasoning, PartFile, PartAgent, PartCompaction, PartSubtask,
	PartRetry, PartStepStart, PartStepFinish, PartSnapshot, PartPatch, PartTool,
}

// Tool states of a V1 tool part.
const (
	ToolPending   = "pending"
	ToolRunning   = "running"
	ToolCompleted = "completed"
	ToolError     = "error"
)

// Assistant error names of the V1 model.
const (
	ErrorAborted = "MessageAbortedError"
)

// Message is a V1 message's info, MessageV2.Info: a user message or an
// assistant one, told apart by Role. A G1 file holds it whole; a G2
// row's data holds it without id and sessionID, which the row's columns
// carry.
type Message struct {
	ID        string       `json:"id"`
	SessionID string       `json:"sessionID"`
	Role      string       `json:"role"`
	Time      *MessageTime `json:"time"`

	// A user message's.
	Agent   string          `json:"agent"`
	Model   *ModelRef       `json:"model"`
	System  string          `json:"system"`
	Tools   json.RawMessage `json:"tools"`
	Format  json.RawMessage `json:"format"`
	Variant string          `json:"variant"`
	// Summary is an object on a user message and true on an assistant
	// message that holds a compaction's summary.
	Summary json.RawMessage `json:"summary"`

	// An assistant message's.
	ParentID   string          `json:"parentID"`
	ModelID    string          `json:"modelID"`
	ProviderID string          `json:"providerID"`
	Mode       string          `json:"mode"`
	Path       json.RawMessage `json:"path"`
	Cost       json.RawMessage `json:"cost"`
	Tokens     *Tokens         `json:"tokens"`
	Error      *MessageError   `json:"error"`
	Finish     string          `json:"finish"`
	Structured json.RawMessage `json:"structured"`

	Members
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members and
// members of another JSON type.
func (m *Message) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMessage)(m), &m.Members)
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessage(m), &m.Members)
}

// IsSummary reports whether an assistant message holds a compaction's
// summary.
func (m *Message) IsSummary() bool {
	return m.Role == RoleAssistant && string(bytes.TrimSpace(m.Summary)) == "true"
}

// Created is the message's time.created, in milliseconds.
func (m *Message) Created() (int64, bool) {
	if m.Time == nil {
		return 0, false
	}
	return Millis(m.Time.Created)
}

// Completed is an assistant message's time.completed, in milliseconds.
func (m *Message) Completed() (int64, bool) {
	if m.Time == nil {
		return 0, false
	}
	return Millis(m.Time.Completed)
}

// MessageTime is a message's time.
type MessageTime struct {
	Created   json.RawMessage `json:"created"`
	Completed json.RawMessage `json:"completed"`

	Members
}

type plainMessageTime MessageTime

// UnmarshalJSON decodes the times, keeping undeclared members.
func (t *MessageTime) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMessageTime)(t), &t.Members)
}

// MarshalJSON writes the times back as they were read.
func (t MessageTime) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessageTime(t), &t.Members)
}

// ModelRef is a model reference: a V1 user message's model
// {providerID, modelID, variant}, or V2's {providerID, id, variant}.
type ModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	ID         string `json:"id"`
	Variant    string `json:"variant"`

	Members
}

type plainModelRef ModelRef

// UnmarshalJSON decodes the reference, keeping undeclared members.
func (r *ModelRef) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainModelRef)(r), &r.Members)
}

// MarshalJSON writes the reference back as it was read.
func (r ModelRef) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainModelRef(r), &r.Members)
}

// Name is the model's ID, whichever member holds it.
func (r *ModelRef) Name() string {
	if r == nil {
		return ""
	}
	if r.ModelID != "" {
		return r.ModelID
	}
	return r.ID
}

// Tokens is a token count: a V1 assistant message's or step-finish
// part's, or V2's TokenUsage. Input counts neither cache reads nor
// cache writes.
type Tokens struct {
	Total     json.RawMessage `json:"total"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Reasoning json.RawMessage `json:"reasoning"`
	Cache     *Cache          `json:"cache"`

	Members
}

type plainTokens Tokens

// UnmarshalJSON decodes the counts, keeping undeclared members.
func (t *Tokens) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainTokens)(t), &t.Members)
}

// MarshalJSON writes the counts back as they were read.
func (t Tokens) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainTokens(t), &t.Members)
}

// Cache is a token count's cache reads and writes.
type Cache struct {
	Read  json.RawMessage `json:"read"`
	Write json.RawMessage `json:"write"`

	Members
}

type plainCache Cache

// UnmarshalJSON decodes the counts, keeping undeclared members.
func (c *Cache) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainCache)(c), &c.Members)
}

// MarshalJSON writes the counts back as they were read.
func (c Cache) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainCache(c), &c.Members)
}

// MessageError is a V1 assistant message's error: a name and its data.
type MessageError struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`

	Members
}

type plainMessageError MessageError

// UnmarshalJSON decodes the error, keeping undeclared members.
func (e *MessageError) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMessageError)(e), &e.Members)
}

// MarshalJSON writes the error back as it was read.
func (e MessageError) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessageError(e), &e.Members)
}

// Message is the error's data.message, when it has one.
func (e *MessageError) Message() string {
	var d struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.Message
}

// Part is one part of a V1 message, MessageV2.Part, told apart by Type.
// A G1 file holds it whole; a G2 row's data holds it without id,
// sessionID and messageID.
type Part struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`

	// text and reasoning; Metadata is the AI SDK's provider metadata on
	// text and reasoning, a tool part's call-side metadata on a tool.
	Text      string          `json:"text"`
	Synthetic bool            `json:"synthetic"`
	Ignored   bool            `json:"ignored"`
	Time      json.RawMessage `json:"time"`
	Metadata  json.RawMessage `json:"metadata"`

	// file
	Mime     string          `json:"mime"`
	Filename string          `json:"filename"`
	URL      string          `json:"url"`
	Source   json.RawMessage `json:"source"`

	// agent
	Name string `json:"name"`

	// compaction
	Auto        bool   `json:"auto"`
	Overflow    bool   `json:"overflow"`
	TailStartID string `json:"tail_start_id"`

	// subtask
	Prompt      string          `json:"prompt"`
	Description string          `json:"description"`
	Agent       string          `json:"agent"`
	Model       json.RawMessage `json:"model"`
	Command     string          `json:"command"`

	// retry
	Attempt json.RawMessage `json:"attempt"`
	Error   json.RawMessage `json:"error"`

	// step-start, step-finish, snapshot, patch
	Snapshot string          `json:"snapshot"`
	Reason   string          `json:"reason"`
	Cost     json.RawMessage `json:"cost"`
	Tokens   *Tokens         `json:"tokens"`
	Hash     string          `json:"hash"`
	Files    json.RawMessage `json:"files"`

	// tool
	CallID string     `json:"callID"`
	Tool   string     `json:"tool"`
	State  *ToolState `json:"state"`

	Members
}

type plainPart Part

// UnmarshalJSON decodes a part, keeping undeclared members and members
// of another JSON type.
func (p *Part) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainPart)(p), &p.Members)
}

// MarshalJSON writes the part back as it was read.
func (p Part) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainPart(p), &p.Members)
}

// IsKnownPart reports whether OpenCode's V1 model defines part type t.
func IsKnownPart(t string) bool {
	for _, k := range KnownParts {
		if k == t {
			return true
		}
	}
	return false
}

// ToolState is a V1 tool part's state, by Status: pending holds input
// and raw; running input, title, metadata and time; completed input,
// output, title, metadata, time and attachments; error input, error,
// metadata and time.
type ToolState struct {
	Status      string            `json:"status"`
	Input       json.RawMessage   `json:"input"`
	Raw         string            `json:"raw"`
	Output      string            `json:"output"`
	Title       string            `json:"title"`
	Metadata    json.RawMessage   `json:"metadata"`
	Time        *ToolTime         `json:"time"`
	Error       string            `json:"error"`
	Attachments []json.RawMessage `json:"attachments"`

	Members
}

type plainToolState ToolState

// UnmarshalJSON decodes the state, keeping undeclared members and
// members of another JSON type.
func (s *ToolState) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainToolState)(s), &s.Members)
}

// MarshalJSON writes the state back as it was read.
func (s ToolState) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainToolState(s), &s.Members)
}

// ToolTime is a tool state's time. Compacted is set when OpenCode
// pruned the output: every request after it sent a placeholder.
type ToolTime struct {
	Start     json.RawMessage `json:"start"`
	End       json.RawMessage `json:"end"`
	Compacted json.RawMessage `json:"compacted"`

	Members
}

type plainToolTime ToolTime

// UnmarshalJSON decodes the times, keeping undeclared members.
func (t *ToolTime) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainToolTime)(t), &t.Members)
}

// MarshalJSON writes the times back as they were read.
func (t ToolTime) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainToolTime(t), &t.Members)
}

// TaskMetadata is what a V1 task tool part's state metadata says of the
// sub-agent it ran, and V2's subagent tool.
type TaskMetadata struct {
	SessionID  string
	Background bool
}

// Task reads a task tool state's metadata: V1 writes sessionId, V2
// sessionID.
func (s *ToolState) Task() TaskMetadata {
	if s == nil {
		return TaskMetadata{}
	}
	return taskMetadata(s.Metadata)
}

func taskMetadata(raw json.RawMessage) TaskMetadata {
	var m struct {
		SessionID  any `json:"sessionId"`
		SessionID2 any `json:"sessionID"`
		Background any `json:"background"`
		Status     any `json:"status"`
	}
	if !isObject(raw) || json.Unmarshal(raw, &m) != nil {
		return TaskMetadata{}
	}
	var t TaskMetadata
	if s, ok := m.SessionID.(string); ok {
		t.SessionID = s
	} else if s, ok := m.SessionID2.(string); ok {
		t.SessionID = s
	}
	if b, ok := m.Background.(bool); ok {
		t.Background = b
	}
	if s, ok := m.Status.(string); ok && s == "running" {
		t.Background = true
	}
	return t
}

// Interrupted returns the output an errored tool call cleaned up after
// an abort carries, metadata.output with metadata.interrupted true,
// which OpenCode sends as the call's output.
func (s *ToolState) Interrupted() (string, bool) {
	if s == nil || s.Status != ToolError {
		return "", false
	}
	var m struct {
		Interrupted any `json:"interrupted"`
		Output      any `json:"output"`
	}
	if !isObject(s.Metadata) || json.Unmarshal(s.Metadata, &m) != nil {
		return "", false
	}
	b, _ := m.Interrupted.(bool)
	out, ok := m.Output.(string)
	return out, b && ok
}

// Compacted reports whether a completed tool state's output was pruned,
// and when, in milliseconds.
func (s *ToolState) Compacted() (int64, bool) {
	if s == nil || s.Time == nil || !Truthy(s.Time.Compacted) {
		return 0, false
	}
	ms, _ := Millis(s.Time.Compacted)
	return ms, true
}

// Truthy reports whether a JSON value is truthy as JavaScript reads it:
// absent, null, false, 0 and "" are not.
func Truthy(raw json.RawMessage) bool {
	t := string(bytes.TrimSpace(raw))
	switch t {
	case "", "null", "false", `""`:
		return false
	}
	if f, ok := Float(raw); ok {
		return f != 0
	}
	return true
}

// FileAttachment is the file part a completed tool state's attachments
// hold, or a user message's file part: the members a converter reads.
type FileAttachment struct {
	Mime     string `json:"mime"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// Attachment decodes one attachment; false when it is not a file part.
func Attachment(raw json.RawMessage) (FileAttachment, bool) {
	var a FileAttachment
	if !isObject(raw) || json.Unmarshal(raw, &a) != nil || a.URL == "" {
		return FileAttachment{}, false
	}
	return a, true
}

// IsNull reports whether raw is absent or null.
func IsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// isObject reports whether data is a JSON object, as far as its first
// byte tells.
func isObject(data []byte) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && t[0] == '{'
}

// Int reads a JSON number that is an integer.
func Int(raw json.RawMessage) (int64, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] == '"' || t[0] == 'n' {
		return 0, false
	}
	if n, err := strconv.ParseInt(string(t), 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(string(t), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// Count reads a token count: a non-negative integer, else 0.
func Count(raw json.RawMessage) int {
	n, ok := Int(raw)
	if !ok || n < 0 || n > 1<<53 {
		return 0
	}
	return int(n)
}

// Float reads a JSON number.
func Float(raw json.RawMessage) (float64, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] == '"' || t[0] == 'n' {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(t), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// Millis reads an epoch-millisecond time, OpenCode's every timestamp,
// in the range a time.Time holds.
func Millis(raw json.RawMessage) (int64, bool) {
	n, ok := Int(raw)
	if !ok || n < 0 || n > 1e15 {
		return 0, false
	}
	return n, true
}
