package cline

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

// Roles of an API message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Content block types. Reasoning and ThoughtSignature are Roo's.
const (
	BlockText             = "text"
	BlockImage            = "image"
	BlockDocument         = "document"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
	BlockReasoning        = "reasoning"
	BlockThoughtSignature = "thoughtSignature"
)

// Message is one element of api_conversation_history.json: an
// Anthropic MessageParam and the members Cline and Roo add to it. Roo
// also writes elements with no role, a reasoning item on its own
// (Type "reasoning").
type Message struct {
	Role    string          `json:"role"`
	Content Content         `json:"content"`
	ID      string          `json:"id"`
	TS      json.RawMessage `json:"ts"`

	// Cline classic, on an assistant message.
	ModelInfo *ModelInfo `json:"modelInfo"`
	Metrics   *Metrics   `json:"metrics"`

	// Roo.
	IsSummary          bool            `json:"isSummary"`
	CondenseID         string          `json:"condenseId"`
	CondenseParent     string          `json:"condenseParent"`
	TruncationID       string          `json:"truncationId"`
	TruncationParent   string          `json:"truncationParent"`
	IsTruncationMarker bool            `json:"isTruncationMarker"`
	Type               string          `json:"type"`
	EncryptedContent   string          `json:"encrypted_content"`
	Summary            json.RawMessage `json:"summary"`
	ReasoningDetails   json.RawMessage `json:"reasoning_details"`
	ReasoningContent   json.RawMessage `json:"reasoning_content"`

	Members

	// Raw is the element as read, and Err why it did not decode, for an
	// element that is not an object; nothing else is then set.
	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members and
// members of another JSON type; an element that is not an object is
// kept with Err set.
func (m *Message) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		*m = Message{Raw: bytes.Clone(data), Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainMessage)(m), &m.Members); err != nil {
		*m = Message{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.Err != nil {
		return m.Raw, nil
	}
	return jsonline.Encode(plainMessage(m), &m.Members)
}

// Time is the message's ts; false when it has none.
func (m *Message) Time() (time.Time, bool) { return Millis(m.TS) }

// IsStandaloneReasoning reports whether the element is Roo's reasoning
// item written on its own, with no role.
func (m *Message) IsStandaloneReasoning() bool {
	return m.Err == nil && m.Role == "" && m.Type == BlockReasoning
}

// ToolUses returns the message's tool_use blocks that decoded.
func (m *Message) ToolUses() []*Block {
	var out []*Block
	for _, b := range m.Content.Blocks {
		if b.Err == nil && b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// ModelInfo is the model a classic assistant message was written with.
type ModelInfo struct {
	ModelID    string `json:"modelId"`
	ProviderID string `json:"providerId"`
	Mode       string `json:"mode"`

	Members
}

type plainModelInfo ModelInfo

// UnmarshalJSON decodes the model info, keeping undeclared members.
func (i *ModelInfo) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainModelInfo)(i), &i.Members)
}

// MarshalJSON writes the model info back as it was read.
func (i ModelInfo) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainModelInfo(i), &i.Members)
}

// Metrics is a classic assistant message's usage: prompt is the
// provider's input tokens, whose meaning differs by provider, and
// cached the cache writes and reads added together.
type Metrics struct {
	Tokens *MetricTokens   `json:"tokens"`
	Cost   json.RawMessage `json:"cost"`

	Members
}

type plainMetrics Metrics

// UnmarshalJSON decodes the metrics, keeping undeclared members.
func (x *Metrics) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainMetrics)(x), &x.Members)
}

// MarshalJSON writes the metrics back as they were read.
func (x Metrics) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMetrics(x), &x.Members)
}

// MetricTokens are the counts of [Metrics], kept as written.
type MetricTokens struct {
	Prompt     json.RawMessage `json:"prompt"`
	Completion json.RawMessage `json:"completion"`
	Cached     json.RawMessage `json:"cached"`

	Members
}

type plainMetricTokens MetricTokens

// UnmarshalJSON decodes the counts, keeping undeclared members.
func (x *MetricTokens) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainMetricTokens)(x), &x.Members)
}

// MarshalJSON writes the counts back as they were read.
func (x MetricTokens) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMetricTokens(x), &x.Members)
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

// Block is one content block: Anthropic's, with Cline's and Roo's
// members. Type says which members apply.
type Block struct {
	Type string `json:"type"`

	// text, Roo plain reasoning
	Text string `json:"text"`
	// thinking
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	// redacted_thinking
	Data string `json:"data"`
	// tool_use; id is also a Roo reasoning item's
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string   `json:"tool_use_id"`
	Content   *Content `json:"content"`
	IsError   bool     `json:"is_error"`
	// image, document
	Source *MediaSource `json:"source"`
	// Roo encrypted reasoning
	EncryptedContent string `json:"encrypted_content"`
	// Roo thoughtSignature
	ThoughtSignature string `json:"thoughtSignature"`

	Members

	// Raw and Err are set for a block that is not an object, or whose
	// members do not decode as declared; Type is read when it can be.
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

// MediaSource is where an image's or a document's bytes are.
type MediaSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`

	Members
}

type plainSource MediaSource

// UnmarshalJSON decodes a source, keeping undeclared members.
func (s *MediaSource) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainSource)(s), &s.Members)
}

// MarshalJSON writes the source back as it was read.
func (s MediaSource) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainSource(s), &s.Members)
}

// UIMessage is one element of ui_messages.json: what the chat view
// shows, an ask or a say.
type UIMessage struct {
	TS                              json.RawMessage `json:"ts"`
	Type                            string          `json:"type"`
	Ask                             string          `json:"ask"`
	Say                             string          `json:"say"`
	Text                            string          `json:"text"`
	Partial                         bool            `json:"partial"`
	ConversationHistoryIndex        json.RawMessage `json:"conversationHistoryIndex"`
	ConversationHistoryDeletedRange json.RawMessage `json:"conversationHistoryDeletedRange"`
	// Roo.
	APIProtocol       string          `json:"apiProtocol"`
	ContextCondense   json.RawMessage `json:"contextCondense"`
	ContextTruncation json.RawMessage `json:"contextTruncation"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainUIMessage UIMessage

// UnmarshalJSON decodes a UI message, keeping undeclared members; an
// element that is not an object is kept with Err set.
func (m *UIMessage) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		*m = UIMessage{Raw: bytes.Clone(data), Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainUIMessage)(m), &m.Members); err != nil {
		*m = UIMessage{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the message back as it was read.
func (m UIMessage) MarshalJSON() ([]byte, error) {
	if m.Err != nil {
		return m.Raw, nil
	}
	return jsonline.Encode(plainUIMessage(m), &m.Members)
}

// Time is the message's ts; false when it has none.
func (m *UIMessage) Time() (time.Time, bool) { return Millis(m.TS) }

// Kind is the message's ask or say, whichever its type names.
func (m *UIMessage) Kind() string {
	if m.Type == "ask" {
		return m.Ask
	}
	return m.Say
}

// HistoryIndex is conversationHistoryIndex, the index of the last API
// message when the UI message was written; false when absent.
func (m *UIMessage) HistoryIndex() (int, bool) {
	n, _, ok := Int(m.ConversationHistoryIndex)
	if !ok || n < -1 || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}

// DeletedRange is conversationHistoryDeletedRange, the range of API
// messages a request left out when the UI message was written. ok is
// false when it is absent or not two integers; set says it was there.
func (m *UIMessage) DeletedRange() (r [2]int, ok bool) {
	return Range(m.ConversationHistoryDeletedRange)
}

// ReqInfo decodes the text of an api_req_started or deleted_api_reqs
// message; nil when it is not a JSON object.
func (m *UIMessage) ReqInfo() *ReqInfo {
	t := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(t, "{") {
		return nil
	}
	var r ReqInfo
	if err := json.Unmarshal([]byte(t), &r); err != nil {
		return nil
	}
	return &r
}

// ReqInfo is ClineApiReqInfo: one request's usage, the text of an
// api_req_started message. Counts are kept as written.
type ReqInfo struct {
	Request                string          `json:"request"`
	TokensIn               json.RawMessage `json:"tokensIn"`
	TokensOut              json.RawMessage `json:"tokensOut"`
	CacheWrites            json.RawMessage `json:"cacheWrites"`
	CacheReads             json.RawMessage `json:"cacheReads"`
	Cost                   json.RawMessage `json:"cost"`
	CancelReason           string          `json:"cancelReason"`
	StreamingFailedMessage string          `json:"streamingFailedMessage"`
	APIProtocol            string          `json:"apiProtocol"`

	Members
}

type plainReqInfo ReqInfo

// UnmarshalJSON decodes the info, keeping undeclared members.
func (r *ReqInfo) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainReqInfo)(r), &r.Members)
}

// MarshalJSON writes the info back as it was read.
func (r ReqInfo) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainReqInfo(r), &r.Members)
}

// HasUsage reports whether the info carries any count or a cost.
func (r *ReqInfo) HasUsage() bool {
	for _, v := range []json.RawMessage{r.TokensIn, r.TokensOut, r.CacheWrites, r.CacheReads, r.Cost} {
		if !IsNull(v) {
			return true
		}
	}
	return false
}

// HistoryItem is a task's index entry: classic Cline's element of
// state/taskHistory.json, Roo's history_item.json.
type HistoryItem struct {
	ID                              string          `json:"id"`
	TS                              json.RawMessage `json:"ts"`
	Task                            string          `json:"task"`
	ConversationHistoryDeletedRange json.RawMessage `json:"conversationHistoryDeletedRange"`
	CWDOnTaskInitialization         string          `json:"cwdOnTaskInitialization"`
	ShadowGitConfigWorkTree         string          `json:"shadowGitConfigWorkTree"`
	ModelID                         string          `json:"modelId"`
	// Roo.
	RootTaskID              string   `json:"rootTaskId"`
	ParentTaskID            string   `json:"parentTaskId"`
	Workspace               string   `json:"workspace"`
	Mode                    string   `json:"mode"`
	APIConfigName           string   `json:"apiConfigName"`
	Status                  string   `json:"status"`
	ChildIDs                []string `json:"childIds"`
	CompletionResultSummary string   `json:"completionResultSummary"`

	Members

	// Raw is the item as read.
	Raw json.RawMessage `json:"-"`
}

type plainHistoryItem HistoryItem

// UnmarshalJSON decodes the item, keeping undeclared members and those
// of another JSON type.
func (h *HistoryItem) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	if err := jsonline.DecodeLenient(data, (*plainHistoryItem)(h), &h.Members); err != nil {
		return err
	}
	h.Raw = bytes.Clone(data)
	return nil
}

// MarshalJSON writes the item back as it was read.
func (h HistoryItem) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainHistoryItem(h), &h.Members)
}

// DeletedRange is the item's conversationHistoryDeletedRange.
func (h *HistoryItem) DeletedRange() ([2]int, bool) { return Range(h.ConversationHistoryDeletedRange) }

// TaskMetadata is a classic task's task_metadata.json, or Roo's, which
// holds files_in_context alone.
type TaskMetadata struct {
	FilesInContext     json.RawMessage `json:"files_in_context"`
	ModelUsage         []*ModelUsage   `json:"model_usage"`
	EnvironmentHistory []*Environment  `json:"environment_history"`

	Members
}

type plainMetadata TaskMetadata

// UnmarshalJSON decodes the metadata, keeping undeclared members and
// those of another JSON type.
func (t *TaskMetadata) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainMetadata)(t), &t.Members)
}

// MarshalJSON writes the metadata back as it was read.
func (t TaskMetadata) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMetadata(t), &t.Members)
}

// ModelUsage is one model switch the metadata records.
type ModelUsage struct {
	TS              json.RawMessage `json:"ts"`
	ModelID         string          `json:"model_id"`
	ModelProviderID string          `json:"model_provider_id"`
	Mode            string          `json:"mode"`

	Members
}

type plainModelUsage ModelUsage

// UnmarshalJSON decodes the record, keeping undeclared members.
func (u *ModelUsage) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainModelUsage)(u), &u.Members); err != nil {
		*u = ModelUsage{}
	}
	return nil
}

// MarshalJSON writes the record back as it was read.
func (u ModelUsage) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainModelUsage(u), &u.Members)
}

// Environment is one environment the task ran in: the only place a
// classic task records the Cline version.
type Environment struct {
	TS           json.RawMessage `json:"ts"`
	ClineVersion string          `json:"cline_version"`

	Members
}

type plainEnvironment Environment

// UnmarshalJSON decodes the record, keeping undeclared members.
func (e *Environment) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainEnvironment)(e), &e.Members); err != nil {
		*e = Environment{}
	}
	return nil
}

// MarshalJSON writes the record back as it was read.
func (e Environment) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainEnvironment(e), &e.Members)
}

// ContextUpdate is one update of a classic task's context_history.json:
// from TS on, every request sends block Block of API message Message
// with Text in place of its text. The latest update of a block wins.
type ContextUpdate struct {
	Message  int
	EditType json.RawMessage
	Block    int
	TS       json.RawMessage
	// Kind is the update's type; Cline writes "text" alone.
	Kind string
	// Text is the new text; Cline writes one.
	Text     []string
	Metadata json.RawMessage
	// Raw is the update as read, and Err why it does not decode. A
	// message or block entry that does not decode is one update with
	// Err set and Message or Block -1.
	Raw json.RawMessage
	Err error
}

// Time is the update's ts; false when it has none.
func (u *ContextUpdate) Time() (time.Time, bool) { return Millis(u.TS) }

// NewText is the replacement text, the update's first.
func (u *ContextUpdate) NewText() string {
	if len(u.Text) == 0 {
		return ""
	}
	return u.Text[0]
}

// Millis reads a ms epoch timestamp written as a number, or a string
// of one; false for anything else, or one outside the years 1970 to
// 9999.
func Millis(raw json.RawMessage) (time.Time, bool) {
	n, ok := Float(raw)
	if !ok || n <= 0 || n > 253402300799999 {
		return time.Time{}, false
	}
	ms := int64(n)
	return time.UnixMilli(ms).UTC().Add(time.Duration((n - float64(ms)) * float64(time.Millisecond))), true
}

// Int reads an integer written as a number, possibly 12.0, or a string
// of one. exact is false when a fraction was dropped.
func Int(raw json.RawMessage) (n int64, exact, ok bool) {
	f, ok := Float(raw)
	if !ok || f > math.MaxInt64 || f < math.MinInt64 {
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

// Range reads a deleted range, two integers [start, end] with start at
// most end; false for anything else.
func Range(raw json.RawMessage) (r [2]int, ok bool) {
	if IsNull(raw) {
		return r, false
	}
	var pair []json.RawMessage
	if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
		return r, false
	}
	for i, p := range pair {
		n, exact, ok := Int(p)
		if !ok || !exact || n < 0 || n > math.MaxInt32 {
			return r, false
		}
		r[i] = int(n)
	}
	return r, r[0] <= r[1]
}

// IsNull reports whether raw is absent or null.
func IsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
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
