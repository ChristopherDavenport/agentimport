package claudecode

import (
	"encoding/json"
	"time"
)

// Line types that form the conversation tree: they carry uuid and
// parentUuid.
const (
	TypeUser       = "user"
	TypeAssistant  = "assistant"
	TypeAttachment = "attachment"
	TypeSystem     = "system"
)

// Side record types: state the CLI appends beside the tree, with no
// uuid, the last one written in force.
const (
	TypeAITitle             = "ai-title"
	TypeCustomTitle         = "custom-title"
	TypeSummary             = "summary"
	TypeTag                 = "tag"
	TypeLastPrompt          = "last-prompt"
	TypeMode                = "mode"
	TypePermissionMode      = "permission-mode"
	TypeAtisLatch           = "atis-latch"
	TypeQueueOperation      = "queue-operation"
	TypePRLink              = "pr-link"
	TypeFileHistorySnapshot = "file-history-snapshot"
	TypeFileHistoryDelta    = "file-history-delta"
	TypeCostState           = "cost-state"
	TypeBridgeSession       = "bridge-session"
	TypeForkContextRef      = "fork-context-ref"
	TypeAPIRequest          = "api-request"
	TypeAPIRequestBlob      = "api-request-blob"
	TypeAPIRequestShape     = "api-request-shape"
)

// System line subtypes the converter treats specially. Others are kept
// as they are.
const (
	SubtypeCompactBoundary = "compact_boundary"
	SubtypeTurnDuration    = "turn_duration"
	SubtypeLocalCommand    = "local_command"
)

// SyntheticModel is the model a line the CLI wrote itself, such as an
// API error, names.
const SyntheticModel = "<synthetic>"

// Line is one line of a transcript. It holds the members of every line
// type; Type says which apply. Members the type does not declare are
// kept in Extra and written back.
type Line struct {
	Type string `json:"type"`

	// The envelope of the tree's lines.
	UUID              string `json:"uuid"`
	ParentUUID        string `json:"parentUuid"`
	LogicalParentUUID string `json:"logicalParentUuid"`
	IsSidechain       bool   `json:"isSidechain"`
	SessionID         string `json:"sessionId"`
	AgentID           string `json:"agentId"`
	Timestamp         string `json:"timestamp"`
	CWD               string `json:"cwd"`
	GitBranch         string `json:"gitBranch"`
	Version           string `json:"version"`
	UserType          string `json:"userType"`
	Entrypoint        string `json:"entrypoint"`

	// user and assistant.
	Message *Message `json:"message"`

	// user.
	IsMeta                    bool            `json:"isMeta"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	ToolUseResult             json.RawMessage `json:"toolUseResult"`
	SourceToolAssistantUUID   string          `json:"sourceToolAssistantUUID"`
	PermissionMode            string          `json:"permissionMode"`

	// assistant.
	RequestID         string                     `json:"requestId"`
	APIBlockIndex     int                        `json:"apiBlockIndex"`
	RequestedModel    string                     `json:"requestedModel"`
	WireToolInputs    map[string]json.RawMessage `json:"wireToolInputs"`
	IsAPIErrorMessage bool                       `json:"isApiErrorMessage"`
	Error             json.RawMessage            `json:"error"`
	APIErrorStatus    int                        `json:"apiErrorStatus"`

	// attachment.
	Attachment          *Attachment `json:"attachment"`
	Rendered            []Rendered  `json:"rendered"`
	RenderedRole        string      `json:"renderedRole"`
	RenderedInHumanTurn []Rendered  `json:"renderedInHumanTurn"`

	// system.
	Subtype         string           `json:"subtype"`
	Content         json.RawMessage  `json:"content"`
	CompactMetadata *CompactMetadata `json:"compactMetadata"`

	// Side records.
	AITitle         string        `json:"aiTitle"`
	CustomTitle     string        `json:"customTitle"`
	Summary         string        `json:"summary"`
	LeafUUID        string        `json:"leafUuid"`
	LastPrompt      string        `json:"lastPrompt"`
	ParentSessionID string        `json:"parentSessionId"`
	ParentLastUUID  string        `json:"parentLastUuid"`
	Shape           *RequestShape `json:"shape"`

	Members

	// No is the line's 1-based number in its file.
	No int `json:"-"`
	// Raw is the line as read, without NUL bytes.
	Raw json.RawMessage `json:"-"`
	// Err is set when the line is JSON but does not decode as a line of
	// this package's types; only Type, No and Raw are then set.
	Err error `json:"-"`
}

type plainLine Line

// UnmarshalJSON decodes a line, keeping undeclared members.
func (l *Line) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainLine)(l), &l.Members)
}

// MarshalJSON writes the line back as it was read.
func (l Line) MarshalJSON() ([]byte, error) {
	if l.Err != nil {
		return l.Raw, nil
	}
	return encode(plainLine(l), &l.Members)
}

// InTree reports whether the line is a node of the conversation tree,
// rather than a side record.
func (l *Line) InTree() bool { return l.UUID != "" }

// Time parses the line's timestamp; zero when it has none.
func (l *Line) Time() time.Time {
	t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ToolResults returns the line's tool_result blocks.
func (l *Line) ToolResults() []*ContentBlock {
	if l.Message == nil {
		return nil
	}
	var out []*ContentBlock
	for _, b := range l.Message.Content.Blocks {
		if b.Type == BlockToolResult {
			out = append(out, b)
		}
	}
	return out
}

// IsToolResult reports whether the line carries a tool result.
func (l *Line) IsToolResult() bool {
	return l.Type == TypeUser && len(l.ToolResults()) > 0
}

// IsSynthetic reports whether the line is an assistant line the CLI
// wrote itself rather than the model: an API error or an interruption.
func (l *Line) IsSynthetic() bool {
	return l.Type == TypeAssistant && (l.IsAPIErrorMessage || l.Message != nil && l.Message.Model == SyntheticModel)
}

// Text returns a system line's content as text, when it is a string.
func (l *Line) Text() string {
	var s string
	_ = json.Unmarshal(l.Content, &s)
	return s
}

// Message is an Anthropic Messages API message: a user turn, or one
// content block of an assistant response.
type Message struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Model        string          `json:"model"`
	Content      Content         `json:"content"`
	StopReason   string          `json:"stop_reason"`
	StopSequence string          `json:"stop_sequence"`
	Usage        json.RawMessage `json:"usage"`

	Members
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members.
func (m *Message) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainMessage)(m), &m.Members)
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	return encode(plainMessage(m), &m.Members)
}

// DecodeUsage decodes the message's usage; nil when it has none.
func (m *Message) DecodeUsage() (*Usage, error) {
	if len(m.Usage) == 0 || string(m.Usage) == "null" {
		return nil, nil
	}
	var u Usage
	if err := json.Unmarshal(m.Usage, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Content is a message's content: a string, or a list of blocks.
type Content struct {
	// Text is the content when it was a string.
	Text string
	// IsText says the content was a string.
	IsText bool
	Blocks []*ContentBlock
}

// UnmarshalJSON accepts a string or an array of blocks.
func (c *Content) UnmarshalJSON(data []byte) error {
	*c = Content{}
	if len(data) > 0 && data[0] == '"' {
		c.IsText = true
		return json.Unmarshal(data, &c.Text)
	}
	return json.Unmarshal(data, &c.Blocks)
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

// Content block types.
const (
	BlockText             = "text"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"
	BlockImage            = "image"
	BlockDocument         = "document"
	BlockToolReference    = "tool_reference"
)

// ContentBlock is one Anthropic content block. Type says which members
// apply.
type ContentBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text"`
	// thinking
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	// redacted_thinking
	Data string `json:"data"`
	// tool_use, server_tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string   `json:"tool_use_id"`
	Content   *Content `json:"content"`
	IsError   bool     `json:"is_error"`
	// image, document
	Source *MediaSource `json:"source"`
	// tool_reference
	ToolName string `json:"tool_name"`

	Members
}

type plainBlock ContentBlock

// UnmarshalJSON decodes a block, keeping undeclared members.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainBlock)(b), &b.Members)
}

// MarshalJSON writes the block back as it was read.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	return encode(plainBlock(b), &b.Members)
}

// MediaSource is where an image's or a document's bytes are.
type MediaSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
	FileID    string `json:"file_id"`

	Members
}

type plainSource MediaSource

// UnmarshalJSON decodes a source, keeping undeclared members.
func (s *MediaSource) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainSource)(s), &s.Members)
}

// MarshalJSON writes the source back as it was read.
func (s MediaSource) MarshalJSON() ([]byte, error) {
	return encode(plainSource(s), &s.Members)
}

// Usage is an Anthropic response's token accounting. Anthropic's
// input_tokens leaves out the cached tokens, read and written.
type Usage struct {
	InputTokens              int64                `json:"input_tokens"`
	OutputTokens             int64                `json:"output_tokens"`
	CacheCreationInputTokens int64                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                `json:"cache_read_input_tokens"`
	OutputTokensDetails      *OutputTokensDetails `json:"output_tokens_details"`

	Members
}

type plainUsage Usage

// UnmarshalJSON decodes usage, keeping undeclared members.
func (u *Usage) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainUsage)(u), &u.Members)
}

// MarshalJSON writes the usage back as it was read.
func (u Usage) MarshalJSON() ([]byte, error) {
	return encode(plainUsage(u), &u.Members)
}

// OutputTokensDetails breaks down output tokens.
type OutputTokensDetails struct {
	ThinkingTokens int64 `json:"thinking_tokens"`

	Members
}

type plainOutputDetails OutputTokensDetails

// UnmarshalJSON decodes the details, keeping undeclared members.
func (d *OutputTokensDetails) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainOutputDetails)(d), &d.Members)
}

// MarshalJSON writes the details back as they were read.
func (d OutputTokensDetails) MarshalJSON() ([]byte, error) {
	return encode(plainOutputDetails(d), &d.Members)
}

// Attachment is context the CLI injected into a turn. Type
// discriminates some thirty kinds; their members stay in Extra.
type Attachment struct {
	Type string `json:"type"`

	Members
}

type plainAttachment Attachment

// UnmarshalJSON decodes an attachment, keeping undeclared members.
func (a *Attachment) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainAttachment)(a), &a.Members)
}

// MarshalJSON writes the attachment back as it was read.
func (a Attachment) MarshalJSON() ([]byte, error) {
	return encode(plainAttachment(a), &a.Members)
}

// Member decodes an attachment member into v; false when it is absent
// or does not decode.
func (a *Attachment) Member(name string, v any) bool {
	raw, ok := a.Extra[name]
	return ok && json.Unmarshal(raw, v) == nil
}

// Rendered is the text an attachment put into the model's context.
type Rendered struct {
	Content string `json:"content"`

	Members
}

type plainRendered Rendered

// UnmarshalJSON decodes the rendering, keeping undeclared members.
func (r *Rendered) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainRendered)(r), &r.Members)
}

// MarshalJSON writes the rendering back as it was read.
func (r Rendered) MarshalJSON() ([]byte, error) {
	return encode(plainRendered(r), &r.Members)
}

// CompactMetadata describes a compaction on its boundary line.
type CompactMetadata struct {
	Trigger           string             `json:"trigger"`
	PreTokens         int64              `json:"preTokens"`
	PostTokens        int64              `json:"postTokens"`
	PreservedSegment  *PreservedSegment  `json:"preservedSegment"`
	PreservedMessages *PreservedMessages `json:"preservedMessages"`

	Members
}

type plainCompact CompactMetadata

// UnmarshalJSON decodes the metadata, keeping undeclared members.
func (c *CompactMetadata) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainCompact)(c), &c.Members)
}

// MarshalJSON writes the metadata back as it was read.
func (c CompactMetadata) MarshalJSON() ([]byte, error) {
	return encode(plainCompact(c), &c.Members)
}

// FirstPreserved returns the uuid of the earliest line the compaction
// kept verbatim, or "" when it kept none.
func (c *CompactMetadata) FirstPreserved() string {
	if c.PreservedSegment != nil && c.PreservedSegment.HeadUUID != "" {
		return c.PreservedSegment.HeadUUID
	}
	if c.PreservedMessages != nil && len(c.PreservedMessages.UUIDs) > 0 {
		return c.PreservedMessages.UUIDs[0]
	}
	return ""
}

// PreservedSegment names the run of lines a compaction kept: from head
// to tail on the path before the boundary, relinked after anchor.
type PreservedSegment struct {
	AnchorUUID string `json:"anchorUuid"`
	HeadUUID   string `json:"headUuid"`
	TailUUID   string `json:"tailUuid"`

	Members
}

type plainSegment PreservedSegment

// UnmarshalJSON decodes the segment, keeping undeclared members.
func (s *PreservedSegment) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainSegment)(s), &s.Members)
}

// MarshalJSON writes the segment back as it was read.
func (s PreservedSegment) MarshalJSON() ([]byte, error) {
	return encode(plainSegment(s), &s.Members)
}

// PreservedMessages lists the lines a compaction kept: uuids[0]
// relinks to anchor, each later one to the one before it.
type PreservedMessages struct {
	AnchorUUID string   `json:"anchorUuid"`
	UUIDs      []string `json:"uuids"`
	AllUUIDs   []string `json:"allUuids"`

	Members
}

type plainPreserved PreservedMessages

// UnmarshalJSON decodes the list, keeping undeclared members.
func (p *PreservedMessages) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainPreserved)(p), &p.Members)
}

// MarshalJSON writes the list back as it was read.
func (p PreservedMessages) MarshalJSON() ([]byte, error) {
	return encode(plainPreserved(p), &p.Members)
}

// RequestShape is the system prompt and tools of a request, on an
// api-request-shape line (2.1.300).
type RequestShape struct {
	System []*ContentBlock `json:"system"`
	Tools  []*ToolDef      `json:"tools"`

	Members
}

type plainShape RequestShape

// UnmarshalJSON decodes the shape, keeping undeclared members.
func (s *RequestShape) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainShape)(s), &s.Members)
}

// MarshalJSON writes the shape back as it was read.
func (s RequestShape) MarshalJSON() ([]byte, error) {
	return encode(plainShape(s), &s.Members)
}

// ToolDef is an Anthropic tool definition.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`

	Members
}

type plainToolDef ToolDef

// UnmarshalJSON decodes the definition, keeping undeclared members.
func (t *ToolDef) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainToolDef)(t), &t.Members)
}

// MarshalJSON writes the definition back as it was read.
func (t ToolDef) MarshalJSON() ([]byte, error) {
	return encode(plainToolDef(t), &t.Members)
}

// PromptSnapshot is a prompt_snapshot attachment: the system prompt,
// and sometimes the tools, of the requests from that point.
type PromptSnapshot struct {
	CLIPrefix    string `json:"cliPrefix"`
	SystemPrompt []string
	Tools        []SnapshotTool
	// HasTools says the snapshot listed tools; one without them leaves
	// the tools as they were.
	HasTools bool
	// EchoWireToolInputs says the requests replay a tool call's
	// arguments as the model wrote them (wireToolInputs) rather than as
	// the CLI normalised them.
	EchoWireToolInputs bool
}

// SnapshotTool is one tool of a prompt snapshot.
type SnapshotTool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Schema      *ToolDef `json:"schema"`
}

// PromptSnapshot decodes a prompt_snapshot attachment; false for any
// other line.
func (l *Line) PromptSnapshot() (*PromptSnapshot, bool) {
	a := l.Attachment
	if l.Type != TypeAttachment || a == nil || a.Type != "prompt_snapshot" {
		return nil, false
	}
	var p PromptSnapshot
	a.Member("cliPrefix", &p.CLIPrefix)
	if !a.Member("systemPrompt", &p.SystemPrompt) {
		return nil, false
	}
	p.HasTools = a.Member("tools", &p.Tools)
	a.Member("echoWireToolInputs", &p.EchoWireToolInputs)
	return &p, true
}
