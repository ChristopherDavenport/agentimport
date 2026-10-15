package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TypeSession is the header line's type.
const TypeSession = "session"

// Entry types: lines of the tree, each with an id and a parentId.
const (
	TypeMessage             = "message"
	TypeModelChange         = "model_change"
	TypeThinkingLevelChange = "thinking_level_change"
	TypeUsage               = "usage"
	TypeCompaction          = "compaction"
	TypeBranchSummary       = "branch_summary"
	TypeCustom              = "custom"
	TypeCustomMessage       = "custom_message"
	TypeContextEdit         = "context_edit"
	TypeLabel               = "label"
	TypeSessionInfo         = "session_info"
)

// CurrentVersion is the session version pi writes, and the one
// [Migrate] brings a file to.
const CurrentVersion = 3

// Line is one line of a session file: the header or an entry. It holds
// the members of every line type; Type says which apply. Members the
// type does not declare are kept in Extra and written back.
type Line struct {
	Type string `json:"type"`
	// ID is the session's id on the header, the entry's id otherwise.
	ID string `json:"id"`
	// ParentID is empty for a root entry; the input wrote null.
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`

	// session (the header). Version is a number, as JavaScript writes
	// it; [File.Version] reads it.
	Version       float64 `json:"version"`
	CWD           string  `json:"cwd"`
	ParentSession string  `json:"parentSession"`

	// message.
	Message *Message `json:"message"`

	// model_change and usage.
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
	// thinking_level_change.
	ThinkingLevel string `json:"thinkingLevel"`

	// usage.
	Kind  string `json:"kind"`
	Model string `json:"model"`
	Note  string `json:"note"`
	// usage, compaction and branch_summary.
	Usage *Usage `json:"usage"`

	// compaction and branch_summary.
	Summary  string          `json:"summary"`
	Details  json.RawMessage `json:"details"`
	FromHook *bool           `json:"fromHook"`
	// compaction.
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	// FirstKeptEntryIndex is version 1's form of FirstKeptEntryID: an
	// index into the file's entries, the header counted.
	FirstKeptEntryIndex *int     `json:"firstKeptEntryIndex"`
	TokensBefore        int      `json:"tokensBefore"`
	SystemMessage       *Message `json:"systemMessage"`
	// branch_summary.
	FromID string `json:"fromId"`

	// custom and custom_message.
	CustomType string `json:"customType"`
	// custom.
	Data json.RawMessage `json:"data"`
	// custom_message.
	Content *Content `json:"content"`
	Display bool     `json:"display"`

	// context_edit and label.
	TargetID string `json:"targetId"`
	// context_edit: null omits the target from context, an object
	// replaces its content.
	Replacement json.RawMessage `json:"replacement"`
	// label: absent clears the label.
	Label *string `json:"label"`
	// session_info.
	Name *string `json:"name"`

	Members

	// No is the line's 1-based number in its file.
	No int `json:"-"`
	// Raw is the line as read, without NUL bytes.
	Raw json.RawMessage `json:"-"`
	// Err is set when the line is JSON but does not decode as a line of
	// this package's types; only Type, No, Raw and, when they read, ID,
	// ParentID and Timestamp are then set.
	Err error `json:"-"`
	// Synthetic says ID and ParentID were given by [Migrate] to a
	// version 1 entry, which had none on disk.
	Synthetic bool `json:"-"`
}

type plainLine Line

// UnmarshalJSON decodes a line, keeping undeclared members.
func (l *Line) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainLine)(l), &l.Members)
}

// MarshalJSON writes the line back as it was read.
func (l Line) MarshalJSON() ([]byte, error) {
	if l.Err != nil {
		return l.Raw, nil
	}
	return encode(plainLine(l), &l.Members)
}

// IsEntry reports whether the line is an entry of the tree, rather than
// the header. Every line that is not the header is one, whatever its
// type, as pi indexes them.
func (l *Line) IsEntry() bool { return l.Type != TypeSession }

// Time is the line's timestamp, zero when it has none or it does not
// parse.
func (l *Line) Time() time.Time {
	if l.Timestamp == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// IsRetainNone reports whether a compaction kept no entry from before
// it, which pi writes as firstKeptEntryId naming the compaction itself.
func (l *Line) IsRetainNone() bool {
	return l.Type == TypeCompaction && l.FirstKeptEntryID != "" && l.FirstKeptEntryID == l.ID
}

// EditReplacement decodes a context_edit's replacement: nil when the
// edit omits its target.
func (l *Line) EditReplacement() (*Content, error) {
	raw := bytes.TrimSpace(l.Replacement)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var r struct {
		Content *Content `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Content == nil {
		return nil, errors.New("replacement has no content")
	}
	return r.Content, nil
}

// Message roles.
const (
	RoleSystem            = "system"
	RoleUser              = "user"
	RoleAssistant         = "assistant"
	RoleToolResult        = "toolResult"
	RoleBashExecution     = "bashExecution"
	RoleCustom            = "custom"
	RoleBranchSummary     = "branchSummary"
	RoleCompactionSummary = "compactionSummary"
	// RoleHookMessage is version 2's name for RoleCustom.
	RoleHookMessage = "hookMessage"
)

// Stop reasons of an assistant message.
const (
	StopPending  = "pending"
	StopStop     = "stop"
	StopLength   = "length"
	StopToolUse  = "toolUse"
	StopError    = "error"
	StopAborted  = "aborted"
	StopDeferred = "deferred"
)

// Message is an AgentMessage: the members of every role; Role says
// which apply.
type Message struct {
	Role    string   `json:"role"`
	Content *Content `json:"content"`
	// Timestamp is Unix milliseconds.
	Timestamp float64 `json:"timestamp"`

	// system.
	Sections     Sections    `json:"sections"`
	ToolsAdded   []*Tool     `json:"toolsAdded"`
	ToolsRemoved []*ToolName `json:"toolsRemoved"`
	Replace      bool        `json:"replace"`

	// assistant.
	API                   string          `json:"api"`
	Provider              string          `json:"provider"`
	Model                 string          `json:"model"`
	ResponseModel         string          `json:"responseModel"`
	ResponseID            string          `json:"responseId"`
	ThinkingLevel         string          `json:"thinkingLevel"`
	ProviderThinkingLevel string          `json:"providerThinkingLevel"`
	Diagnostics           json.RawMessage `json:"diagnostics"`
	StopReason            string          `json:"stopReason"`
	RawStopReason         string          `json:"rawStopReason"`
	Deferred              json.RawMessage `json:"deferred"`
	ErrorMessage          string          `json:"errorMessage"`
	EndTurn               *bool           `json:"endTurn"`
	// assistant and toolResult.
	Usage *Usage `json:"usage"`

	// toolResult.
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
	// toolResult and custom.
	Details json.RawMessage `json:"details"`

	// bashExecution.
	Command            string `json:"command"`
	Output             string `json:"output"`
	ExitCode           *int   `json:"exitCode"`
	Cancelled          bool   `json:"cancelled"`
	Truncated          bool   `json:"truncated"`
	FullOutputPath     string `json:"fullOutputPath"`
	ExcludeFromContext bool   `json:"excludeFromContext"`

	// custom.
	CustomType string `json:"customType"`
	Display    bool   `json:"display"`

	// branchSummary and compactionSummary, which pi derives in memory
	// and does not persist.
	Summary      string `json:"summary"`
	FromID       string `json:"fromId"`
	TokensBefore int    `json:"tokensBefore"`

	Members
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members.
func (m *Message) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainMessage)(m), &m.Members)
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	return encode(plainMessage(m), &m.Members)
}

// Content is a message's content: a string, or an array of blocks.
type Content struct {
	// Text is the content when it was a string.
	Text string
	// IsText says the content was a string.
	IsText bool
	Blocks []*Block
}

// UnmarshalJSON accepts a string or an array of blocks. The blocks are
// decoded one by one, so one that is not an object, or is null, is kept
// as written (see [Block.Invalid]) and its neighbours still decode.
func (c *Content) UnmarshalJSON(data []byte) error {
	*c = Content{}
	if len(data) > 0 && data[0] == '"' {
		c.IsText = true
		return json.Unmarshal(data, &c.Text)
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	if raws == nil {
		return nil
	}
	c.Blocks = make([]*Block, 0, len(raws))
	for _, raw := range raws {
		b := &Block{}
		if err := json.Unmarshal(raw, b); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			b = &Block{invalid: true}
		}
		b.raw = append(json.RawMessage(nil), raw...)
		c.Blocks = append(c.Blocks, b)
	}
	return nil
}

// MarshalJSON writes the string or the blocks.
func (c Content) MarshalJSON() ([]byte, error) {
	if c.IsText {
		return json.Marshal(c.Text)
	}
	if c.Blocks == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(c.Blocks)
}

// TextOf joins the text of the content's text blocks with sep, as pi
// does when a role needs one string; a string content is itself.
func (c *Content) TextOf(sep string) string {
	if c == nil {
		return ""
	}
	if c.IsText {
		return c.Text
	}
	var b bytes.Buffer
	first := true
	for _, x := range c.Blocks {
		if x.Type != BlockText {
			continue
		}
		if !first {
			b.WriteString(sep)
		}
		b.WriteString(x.Text)
		first = false
	}
	return b.String()
}

// Block types.
const (
	BlockText     = "text"
	BlockImage    = "image"
	BlockThinking = "thinking"
	BlockToolCall = "toolCall"
)

// Block is a content block: the members of every block type; Type says
// which apply.
type Block struct {
	Type string `json:"type"`

	// text.
	Text          string `json:"text"`
	TextSignature string `json:"textSignature"`

	// image: base64 data.
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`

	// thinking.
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature"`
	Redacted          bool   `json:"redacted"`

	// toolCall.
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature"`
	Namespace        string          `json:"namespace"`

	Members

	// raw is the block as read; invalid says it was not an object.
	raw     json.RawMessage
	invalid bool
}

// Invalid reports whether the block, as read, was not a JSON object:
// null, a number, a string or an array. Only its raw form is kept.
func (b *Block) Invalid() bool { return b.invalid }

// Raw is the block as read, empty for a block built in Go.
func (b *Block) Raw() json.RawMessage { return b.raw }

type plainBlock Block

// UnmarshalJSON decodes a block, keeping undeclared members, and a
// member whose value does not fit its field among them.
func (b *Block) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainBlock)(b), &b.Members)
}

// MarshalJSON writes the block back as it was read.
func (b Block) MarshalJSON() ([]byte, error) {
	if b.invalid {
		return b.raw, nil
	}
	return encode(plainBlock(b), &b.Members)
}

// Usage is pi's token and cost accounting. Input excludes the tokens
// read from and written to the cache, which have their own counts;
// Reasoning, when present, is already in Output.
type Usage struct {
	Input        int64  `json:"input"`
	Output       int64  `json:"output"`
	CacheRead    int64  `json:"cacheRead"`
	CacheWrite   int64  `json:"cacheWrite"`
	CacheWrite1h *int64 `json:"cacheWrite1h"`
	Reasoning    *int64 `json:"reasoning"`
	TotalTokens  int64  `json:"totalTokens"`
	// Cost is in dollars, per kind of token and in total.
	Cost json.RawMessage `json:"cost"`

	Members
}

type plainUsage Usage

// UnmarshalJSON decodes usage, keeping undeclared members.
func (u *Usage) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainUsage)(u), &u.Members)
}

// MarshalJSON writes usage back as it was read.
func (u Usage) MarshalJSON() ([]byte, error) {
	return encode(plainUsage(u), &u.Members)
}

// Tool is a tool declaration a system message adds.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`

	Members
}

type plainTool Tool

// UnmarshalJSON decodes a tool, keeping undeclared members.
func (t *Tool) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainTool)(t), &t.Members)
}

// MarshalJSON writes the tool back as it was read.
func (t Tool) MarshalJSON() ([]byte, error) {
	return encode(plainTool(t), &t.Members)
}

// ToolName names a tool a system message removes.
type ToolName struct {
	Name string `json:"name"`

	Members
}

type plainToolName ToolName

// UnmarshalJSON decodes a tool reference, keeping undeclared members.
func (t *ToolName) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainToolName)(t), &t.Members)
}

// MarshalJSON writes the tool reference back as it was read.
func (t ToolName) MarshalJSON() ([]byte, error) {
	return encode(plainToolName(t), &t.Members)
}

// Section is one named prompt section a system message sets, or
// removes when Remove is set.
type Section struct {
	Name   string
	Text   string
	Remove bool
}

// Sections are a system message's section patches, in the order the
// object wrote them: pi replays them in that order, and a new section
// takes its place at the end.
type Sections []Section

// UnmarshalJSON reads the object's members in order.
func (s *Sections) UnmarshalJSON(data []byte) error {
	*s = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("sections: want an object, have %v", tok)
	}
	out := Sections{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		var v *string
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("section %q: %w", name, err)
		}
		if v == nil {
			out = append(out, Section{Name: name, Remove: true})
		} else {
			out = append(out, Section{Name: name, Text: *v})
		}
	}
	*s = out
	return nil
}

// MarshalJSON writes the object in order.
func (s Sections) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, x := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(x.Name)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		if x.Remove {
			b.WriteString("null")
			continue
		}
		v, err := json.Marshal(x.Text)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// MistypedMembers lists, as paths from the line, the members anywhere in
// the line whose value was not of the type pi defines for it, which the
// reader kept as written in their object's Extra rather than fail the
// line.
func (l *Line) MistypedMembers() []string {
	var out []string
	add := func(prefix string, names []string) {
		for _, n := range names {
			out = append(out, prefix+n)
		}
	}
	add("", l.Mistyped())
	content := func(prefix string, c *Content) {
		if c == nil {
			return
		}
		for i, b := range c.Blocks {
			add(fmt.Sprintf("%s[%d].", prefix, i), b.Mistyped())
		}
	}
	message := func(prefix string, m *Message) {
		if m == nil {
			return
		}
		add(prefix, m.Mistyped())
		content(prefix+"content", m.Content)
		if m.Usage != nil {
			add(prefix+"usage.", m.Usage.Mistyped())
		}
		for i, t := range m.ToolsAdded {
			if t != nil {
				add(fmt.Sprintf("%stoolsAdded[%d].", prefix, i), t.Mistyped())
			}
		}
		for i, t := range m.ToolsRemoved {
			if t != nil {
				add(fmt.Sprintf("%stoolsRemoved[%d].", prefix, i), t.Mistyped())
			}
		}
	}
	message("message.", l.Message)
	message("systemMessage.", l.SystemMessage)
	content("content", l.Content)
	if l.Usage != nil {
		add("usage.", l.Usage.Mistyped())
	}
	return out
}
