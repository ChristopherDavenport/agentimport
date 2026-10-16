package openhands

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value.
type Members = jsonline.Members

// Event kinds: the Python class name OpenHands writes as each event's
// kind.
const (
	KindSystemPrompt        = "SystemPromptEvent"
	KindMessage             = "MessageEvent"
	KindAction              = "ActionEvent"
	KindObservation         = "ObservationEvent"
	KindUserReject          = "UserRejectObservation"
	KindAgentError          = "AgentErrorEvent"
	KindCondensation        = "Condensation"
	KindCondensationRequest = "CondensationRequest"
	KindCondensationSummary = "CondensationSummaryEvent"
	KindPause               = "PauseEvent"
	KindInterrupt           = "InterruptEvent"
	KindStateUpdate         = "ConversationStateUpdateEvent"
	KindConversationError   = "ConversationErrorEvent"
	KindServerError         = "ServerErrorEvent"
	KindSecurityAnalysis    = "SecurityAnalysisEvent"
	KindHookExecution       = "HookExecutionEvent"
	KindToken               = "TokenEvent"
	KindCompletionLog       = "LLMCompletionLogEvent"
	KindACPToolCall         = "ACPToolCallEvent"
	KindStreamingDelta      = "StreamingDeltaEvent"
)

// KnownKinds lists every event kind the SDK writes, from 1.0.0 to
// 1.55.0.
var KnownKinds = []string{
	KindSystemPrompt, KindMessage, KindAction, KindObservation, KindUserReject,
	KindAgentError, KindCondensation, KindCondensationRequest, KindCondensationSummary,
	KindPause, KindInterrupt, KindStateUpdate, KindConversationError, KindServerError,
	KindSecurityAnalysis, KindHookExecution, KindToken, KindCompletionLog,
	KindACPToolCall, KindStreamingDelta,
}

// Sources of an event.
const (
	SourceAgent       = "agent"
	SourceUser        = "user"
	SourceEnvironment = "environment"
	SourceHook        = "hook"
)

// RootParent is the parent_id that marks an event a new root of the
// event tree.
const RootParent = "__root__"

// Event is one event file as read: its envelope, decoded leniently, and
// the object itself. The kind's members are decoded on demand by the
// Decode methods, so an event of a kind this package does not know, or
// one whose members do not fit its kind, is still an event.
type Event struct {
	// Index is the event's position, from its file name; File is the
	// file name.
	Index int
	File  string

	Kind      string
	ID        string
	Timestamp string
	Source    string
	// ParentID is the event's parent_id; HasParent says the event
	// carried one, as events from SDK 1.31 do.
	ParentID  string
	HasParent bool

	// Raw is the event object as read.
	Raw json.RawMessage
	// Err is set when the file is not a JSON object; nothing else is
	// then set but Index and File.
	Err error
}

// errNotObject is the error for an event or element that is not a JSON
// object.
var errNotObject = errors.New("not a JSON object")

// parseEvent reads an event's envelope. A member of the wrong JSON type
// is left empty: the kind's own decoding reports it.
func parseEvent(data []byte) (*Event, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("the file is empty")
	}
	if !json.Valid(data) {
		return nil, errors.New("the file is not JSON")
	}
	var all map[string]json.RawMessage
	if !isObject(data) || json.Unmarshal(data, &all) != nil {
		return nil, errNotObject
	}
	e := &Event{Raw: bytes.Clone(data)}
	str := func(name string) string {
		var s string
		_ = json.Unmarshal(all[name], &s)
		return s
	}
	e.Kind, e.ID, e.Timestamp, e.Source = str("kind"), str("id"), str("timestamp"), str("source")
	if raw, ok := all["parent_id"]; ok && !IsNull(raw) {
		e.ParentID, e.HasParent = str("parent_id"), true
	}
	return e, nil
}

// Contents is a list of TextContent and ImageContent, read element by
// element; OpenHands also accepts a bare string for a message's
// content, which IsText keeps.
type Contents struct {
	Items  []*Content
	IsText bool
	Text   string
}

// UnmarshalJSON reads the list element by element: an element that is
// not an object is kept with its error.
func (c *Contents) UnmarshalJSON(data []byte) error {
	*c = Contents{}
	t := bytes.TrimSpace(data)
	if len(t) > 0 && t[0] == '"' {
		c.IsText = true
		return json.Unmarshal(t, &c.Text)
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(t, &raws); err != nil {
		return err
	}
	c.Items = make([]*Content, 0, len(raws))
	for _, raw := range raws {
		p := &Content{}
		_ = p.UnmarshalJSON(raw)
		c.Items = append(c.Items, p)
	}
	return nil
}

// MarshalJSON writes the list back as it was read.
func (c Contents) MarshalJSON() ([]byte, error) {
	if c.IsText {
		return json.Marshal(c.Text)
	}
	if c.Items == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(c.Items)
}

// IsZero reports a list that was not read, so an absent member stays
// absent.
func (c Contents) IsZero() bool { return c.Items == nil && !c.IsText }

// Content types.
const (
	ContentText  = "text"
	ContentImage = "image"
)

// Content is one TextContent or ImageContent.
type Content struct {
	Type        string   `json:"type"`
	Text        string   `json:"text"`
	ImageURLs   []string `json:"image_urls"`
	CachePrompt bool     `json:"cache_prompt"`

	Members

	// Raw is the element as read, and Err why it did not decode, for an
	// element that is not an object; nothing else is then set.
	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainContent Content

// UnmarshalJSON decodes a content element, keeping undeclared members.
func (p *Content) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		*p = Content{Raw: bytes.Clone(data), Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainContent)(p), &p.Members); err != nil {
		*p = Content{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the element back as it was read.
func (p Content) MarshalJSON() ([]byte, error) {
	if p.Err != nil {
		return p.Raw, nil
	}
	return jsonline.Encode(plainContent(p), &p.Members)
}

// Message is OpenHands' own LLM message, the llm_message of a
// MessageEvent.
type Message struct {
	Role                   string           `json:"role"`
	Content                Contents         `json:"content"`
	ToolCalls              []*ToolCall      `json:"tool_calls"`
	ToolCallID             string           `json:"tool_call_id"`
	Name                   string           `json:"name"`
	ReasoningContent       string           `json:"reasoning_content"`
	ThinkingBlocks         []*ThinkingBlock `json:"thinking_blocks"`
	ResponsesReasoningItem *ReasoningItem   `json:"responses_reasoning_item"`

	Members
}

type plainMessage Message

// UnmarshalJSON decodes a message, keeping undeclared members and
// members of another JSON type.
func (m *Message) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainMessage)(m), &m.Members)
}

// MarshalJSON writes the message back as it was read.
func (m Message) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessage(m), &m.Members)
}

// ToolCall is a MessageToolCall: one function call as OpenHands stores
// it, its arguments a JSON string.
type ToolCall struct {
	ID              string `json:"id"`
	ResponsesItemID string `json:"responses_item_id"`
	Name            string `json:"name"`
	Arguments       string `json:"arguments"`
	Origin          string `json:"origin"`

	Members
}

type plainToolCall ToolCall

// UnmarshalJSON decodes a tool call, keeping undeclared members.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainToolCall)(c), &c.Members)
}

// MarshalJSON writes the tool call back as it was read.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainToolCall(c), &c.Members)
}

// Thinking block types.
const (
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
)

// ThinkingBlock is an Anthropic thinking or redacted_thinking block.
type ThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	Data      string `json:"data"`

	Members
}

type plainThinking ThinkingBlock

// UnmarshalJSON decodes a block, keeping undeclared members.
func (b *ThinkingBlock) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainThinking)(b), &b.Members)
}

// MarshalJSON writes the block back as it was read.
func (b ThinkingBlock) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainThinking(b), &b.Members)
}

// ReasoningItem is the OpenAI Responses reasoning item OpenHands keeps:
// summary and content as plain strings.
type ReasoningItem struct {
	ID               string   `json:"id"`
	Summary          []string `json:"summary"`
	Content          []string `json:"content"`
	EncryptedContent string   `json:"encrypted_content"`
	Status           string   `json:"status"`

	Members
}

type plainReasoning ReasoningItem

// UnmarshalJSON decodes the item, keeping undeclared members.
func (r *ReasoningItem) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainReasoning)(r), &r.Members)
}

// MarshalJSON writes the item back as it was read.
func (r ReasoningItem) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainReasoning(r), &r.Members)
}

// SystemPromptEvent is the system prompt and the tools the agent was
// given. Tools are chat tool params in SDK 1.0 to 1.6, ToolDefinition
// dumps with no parameter schema from 1.7.
type SystemPromptEvent struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	SystemPrompt   *Content          `json:"system_prompt"`
	Tools          []json.RawMessage `json:"tools"`
	DynamicContext *Content          `json:"dynamic_context"`

	Members
}

type plainSystem SystemPromptEvent

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *SystemPromptEvent) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainSystem)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e SystemPromptEvent) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainSystem(e), &e.Members)
}

// MessageEvent is a message from the user, the agent or the
// environment.
type MessageEvent struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	LLMMessage      *Message        `json:"llm_message"`
	LLMResponseID   string          `json:"llm_response_id"`
	ActivatedSkills []string        `json:"activated_skills"`
	ExtendedContent Contents        `json:"extended_content"`
	Sender          string          `json:"sender"`
	CriticResult    json.RawMessage `json:"critic_result"`

	Members
}

type plainMessageEvent MessageEvent

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *MessageEvent) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainMessageEvent)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e MessageEvent) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMessageEvent(e), &e.Members)
}

// ActionEvent is one tool call. The response's text, reasoning and
// thinking are on the first action of a response only.
type ActionEvent struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	Thought                Contents         `json:"thought"`
	ReasoningContent       string           `json:"reasoning_content"`
	ThinkingBlocks         []*ThinkingBlock `json:"thinking_blocks"`
	ResponsesReasoningItem *ReasoningItem   `json:"responses_reasoning_item"`
	Action                 json.RawMessage  `json:"action"`
	ToolName               string           `json:"tool_name"`
	ToolCallID             string           `json:"tool_call_id"`
	ToolCall               *ToolCall        `json:"tool_call"`
	LLMResponseID          string           `json:"llm_response_id"`
	SecurityRisk           string           `json:"security_risk"`
	CriticResult           json.RawMessage  `json:"critic_result"`
	Summary                string           `json:"summary"`

	Members
}

type plainActionEvent ActionEvent

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *ActionEvent) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainActionEvent)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e ActionEvent) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainActionEvent(e), &e.Members)
}

// ObservationEvent is a tool's result.
type ObservationEvent struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	ToolName        string       `json:"tool_name"`
	ToolCallID      string       `json:"tool_call_id"`
	Observation     *Observation `json:"observation"`
	ActionID        string       `json:"action_id"`
	ExtendedContent Contents     `json:"extended_content"`

	Members
}

type plainObservationEvent ObservationEvent

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *ObservationEvent) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainObservationEvent)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e ObservationEvent) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainObservationEvent(e), &e.Members)
}

// Observation is a tool's kind-tagged observation. The members its
// kind's rendering reads are declared; the rest are kept.
type Observation struct {
	Kind    string   `json:"kind"`
	Content Contents `json:"content"`
	IsError bool     `json:"is_error"`

	// TerminalObservation (ExecuteBashObservation in 1.0) and
	// BrowserObservation.
	Metadata          json.RawMessage `json:"metadata"`
	FullOutputSaveDir string          `json:"full_output_save_dir"`
	ScreenshotData    string          `json:"screenshot_data"`
	// TaskObservation.
	TaskID   string `json:"task_id"`
	Subagent string `json:"subagent"`
	Status   string `json:"status"`
	// ConsultTomObservation and SleeptimeComputeObservation.
	Suggestions       string          `json:"suggestions"`
	Confidence        json.RawMessage `json:"confidence"`
	Reasoning         string          `json:"reasoning"`
	Message           string          `json:"message"`
	SessionsProcessed json.RawMessage `json:"sessions_processed"`

	Members
}

type plainObservation Observation

// UnmarshalJSON decodes the observation, keeping undeclared members and
// members of another JSON type.
func (o *Observation) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainObservation)(o), &o.Members)
}

// MarshalJSON writes the observation back as it was read.
func (o Observation) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainObservation(o), &o.Members)
}

// TerminalMetadata is a terminal observation's metadata, as its
// rendering reads it.
type TerminalMetadata struct {
	ExitCode          int64
	WorkingDir        string
	PyInterpreterPath string
	Prefix, Suffix    string
}

// Terminal reads the metadata of a terminal observation: exit code -1,
// the SDK's default, when it has none.
func (o *Observation) Terminal() TerminalMetadata {
	m := TerminalMetadata{ExitCode: -1}
	var all map[string]json.RawMessage
	if json.Unmarshal(o.Metadata, &all) != nil {
		return m
	}
	str := func(name string) string {
		var s string
		_ = json.Unmarshal(all[name], &s)
		return s
	}
	if n, exact, ok := Int(all["exit_code"]); ok && exact {
		m.ExitCode = n
	}
	m.WorkingDir, m.PyInterpreterPath = str("working_dir"), str("py_interpreter_path")
	m.Prefix, m.Suffix = str("prefix"), str("suffix")
	return m
}

// UserRejectObservation answers a call the user or a hook rejected.
type UserRejectObservation struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	ToolName        string `json:"tool_name"`
	ToolCallID      string `json:"tool_call_id"`
	RejectionReason string `json:"rejection_reason"`
	RejectionSource string `json:"rejection_source"`
	ActionID        string `json:"action_id"`

	Members
}

type plainReject UserRejectObservation

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *UserRejectObservation) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainReject)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e UserRejectObservation) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainReject(e), &e.Members)
}

// DefaultRejectionReason is the reason OpenHands writes when none was
// given.
const DefaultRejectionReason = "User rejected the action"

// AgentErrorEvent answers a call the agent could not run: an unknown
// tool, invalid arguments, a tool that raised, or a call cut by a pause
// or a crash.
type AgentErrorEvent struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	ToolName       string          `json:"tool_name"`
	ToolCallID     string          `json:"tool_call_id"`
	Error          string          `json:"error"`
	Classification json.RawMessage `json:"classification"`

	Members
}

type plainAgentError AgentErrorEvent

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *AgentErrorEvent) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainAgentError)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e AgentErrorEvent) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainAgentError(e), &e.Members)
}

// InterruptedPrefix starts the error of an AgentErrorEvent that answers
// a call a pause or a crash cut.
const InterruptedPrefix = "Tool call interrupted before completion."

// Condensation is a context edit: the events it forgets leave the view,
// and its summary, when it has one and an offset, enters it at the
// offset.
type Condensation struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	ParentID  string `json:"parent_id"`

	ForgottenEventIDs []json.RawMessage `json:"forgotten_event_ids"`
	Summary           *string           `json:"summary"`
	SummaryOffset     json.RawMessage   `json:"summary_offset"`
	LLMResponseID     string            `json:"llm_response_id"`

	Members
}

type plainCondensation Condensation

// UnmarshalJSON decodes the event, keeping undeclared members.
func (e *Condensation) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainCondensation)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e Condensation) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainCondensation(e), &e.Members)
}

// Forgotten returns the IDs the condensation forgets, and how many of
// its elements were not strings.
func (e *Condensation) Forgotten() (ids []string, bad int) {
	for _, raw := range e.ForgottenEventIDs {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			bad++
			continue
		}
		ids = append(ids, s)
	}
	return ids, bad
}

// Offset returns the summary offset; false when there is none or it is
// not a count.
func (e *Condensation) Offset() (int, bool) {
	n, exact, ok := Int(e.SummaryOffset)
	if !ok || !exact || n < 0 || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}

// SummaryID is the ID OpenHands gives the summary a condensation
// inserts, which a later condensation forgets it by.
func SummaryID(condensationID string) string { return condensationID + "-summary" }

// Action is a tool call's action, kind-tagged. The members of the
// actions the converter reads are declared: the task tool's TaskAction
// and the delegate tool's DelegateAction.
type Action struct {
	Kind string `json:"kind"`

	// TaskAction.
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
	Resume       string `json:"resume"`
	// DelegateAction.
	Command string            `json:"command"`
	IDs     []string          `json:"ids"`
	Tasks   map[string]string `json:"tasks"`

	Members
}

type plainAction Action

// UnmarshalJSON decodes the action, keeping undeclared members and
// members of another JSON type.
func (a *Action) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainAction)(a), &a.Members)
}

// MarshalJSON writes the action back as it was read.
func (a Action) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainAction(a), &a.Members)
}

// Action kinds the converter reads.
const (
	ActionTask     = "TaskAction"
	ActionDelegate = "DelegateAction"
)

// ParseAction reads an action object; nil when it is absent or not an
// object.
func ParseAction(raw json.RawMessage) *Action {
	if !isObject(raw) {
		return nil
	}
	a := &Action{}
	if a.UnmarshalJSON(raw) != nil {
		return nil
	}
	return a
}

// decodeAs decodes an event's object into v, a kind's struct.
func decodeAs(e *Event, kind string, v json.Unmarshaler) error {
	if e.Err != nil {
		return e.Err
	}
	if e.Kind != kind {
		return errors.New("the event is a " + e.Kind + ", not a " + kind)
	}
	return v.UnmarshalJSON(e.Raw)
}

// SystemPrompt decodes a SystemPromptEvent.
func (e *Event) SystemPrompt() (*SystemPromptEvent, error) {
	v := &SystemPromptEvent{}
	return v, decodeAs(e, KindSystemPrompt, v)
}

// Message decodes a MessageEvent.
func (e *Event) Message() (*MessageEvent, error) {
	v := &MessageEvent{}
	return v, decodeAs(e, KindMessage, v)
}

// Action decodes an ActionEvent.
func (e *Event) Action() (*ActionEvent, error) {
	v := &ActionEvent{}
	return v, decodeAs(e, KindAction, v)
}

// Observation decodes an ObservationEvent.
func (e *Event) Observation() (*ObservationEvent, error) {
	v := &ObservationEvent{}
	return v, decodeAs(e, KindObservation, v)
}

// UserReject decodes a UserRejectObservation.
func (e *Event) UserReject() (*UserRejectObservation, error) {
	v := &UserRejectObservation{}
	return v, decodeAs(e, KindUserReject, v)
}

// AgentError decodes an AgentErrorEvent.
func (e *Event) AgentError() (*AgentErrorEvent, error) {
	v := &AgentErrorEvent{}
	return v, decodeAs(e, KindAgentError, v)
}

// Condensation decodes a Condensation.
func (e *Event) Condensation() (*Condensation, error) {
	v := &Condensation{}
	return v, decodeAs(e, KindCondensation, v)
}

// Int reads an integer written as a number, possibly 12.0. exact is
// false when a fraction was dropped.
func Int(raw json.RawMessage) (n int64, exact, ok bool) {
	f, ok := Float(raw)
	if !ok || f >= math.MaxInt64 || f < math.MinInt64 {
		return 0, false, false
	}
	return int64(f), f == math.Trunc(f), true
}

// Float reads a number written as a number.
func Float(raw json.RawMessage) (float64, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] == '"' {
		return 0, false
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

// isObject reports whether data is a JSON object, as far as its first
// byte tells.
func isObject(data []byte) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && t[0] == '{'
}

// IsKnownKind reports whether the SDK writes events of kind.
func IsKnownKind(kind string) bool {
	for _, k := range KnownKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// InContext reports whether events of kind enter the model's context.
func InContext(kind string) bool {
	switch kind {
	case KindSystemPrompt, KindMessage, KindAction, KindObservation, KindUserReject,
		KindAgentError, KindCondensationSummary:
		return true
	}
	return false
}

// IsObservationLike reports whether events of kind answer a call.
func IsObservationLike(kind string) bool {
	switch kind {
	case KindObservation, KindUserReject, KindAgentError:
		return true
	}
	return false
}

// IsArtifact reports whether events of kind are not nodes of the event
// tree: OpenHands stamps them with a parent but never makes one the
// head, and skips them when it picks a leaf.
func IsArtifact(kind string) bool {
	return kind == KindStateUpdate || kind == KindConversationError
}

// trimDash is s with its dashes removed, lowercased: a conversation ID
// as a directory names it.
func trimDash(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
