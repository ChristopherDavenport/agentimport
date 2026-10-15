package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Line types: the "type" member of a rollout line, which names what its
// "payload" holds (RolloutItem's wire form).
const (
	TypeSessionMeta                     = "session_meta"
	TypeResponseItem                    = "response_item"
	TypeEventMsg                        = "event_msg"
	TypeTurnContext                     = "turn_context"
	TypeCompacted                       = "compacted"
	TypeInterAgentCommunication         = "inter_agent_communication"
	TypeInterAgentCommunicationMetadata = "inter_agent_communication_metadata"
	TypeTokenUsageRecord                = "token_usage_record"
	TypeWorldState                      = "world_state"
	TypeRetainedContext                 = "retained_context"
	TypeSecurityRiskScore               = "security_risk_score"
	TypeRealtimeItem                    = "realtime_item"
	// TypeState is the kind of a legacy line {"record_type":"state"},
	// a session state snapshot rollouts before September 2025 wrote.
	TypeState = "state"
)

// Line is one line of a rollout: an envelope {timestamp, ordinal, type,
// payload} or, in the legacy layout, a bare session meta, item or state
// record. The payload of a kind this package types is decoded into the
// matching field; every line keeps its raw bytes and writes back as it
// was read.
type Line struct {
	Timestamp string `json:"timestamp"`
	// Ordinal is the line's position in the thread's history, which
	// newer rollouts write.
	Ordinal json.Number     `json:"ordinal"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	// Metadata is a response item's harness metadata
	// (CodexHarnessMetadata), never sent to the model.
	Metadata json.RawMessage `json:"metadata"`

	Members

	// No is the line's 1-based number in its file.
	No int `json:"-"`
	// Raw is the line as read.
	Raw json.RawMessage `json:"-"`
	// Kind is what the line holds: Type for an envelope line; for a
	// legacy line TypeSessionMeta, TypeResponseItem or TypeState.
	Kind string `json:"-"`
	// Legacy says the line is in the layout before the envelope.
	Legacy bool `json:"-"`
	// Err is set when the line is JSON but not a rollout line: an object
	// with no type and no record_type past the first line.
	Err error `json:"-"`
	// PayloadErr is set when the payload of a kind this package types
	// does not decode as that kind; the typed field is then nil.
	PayloadErr error `json:"-"`

	Meta        *SessionMeta      `json:"-"`
	Item        *Item             `json:"-"`
	Event       *Event            `json:"-"`
	TurnContext *TurnContext      `json:"-"`
	Compacted   *Compacted        `json:"-"`
	InterAgent  *InterAgent       `json:"-"`
	UsageRecord *TokenUsageRecord `json:"-"`
}

type plainLine Line

// UnmarshalJSON decodes an envelope line, keeping undeclared members.
// The payload is decoded by [Line.decodePayload].
func (l *Line) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainLine)(l), &l.Members)
}

// MarshalJSON writes the line back as it was read.
func (l Line) MarshalJSON() ([]byte, error) {
	if l.Legacy || l.Err != nil || !l.Members.Has("type") {
		return l.Raw, nil
	}
	return encode(plainLine(l), &l.Members)
}

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

// PayloadRaw is what the line's typed field decodes from: the envelope's
// payload, or the whole legacy line.
func (l *Line) PayloadRaw() json.RawMessage {
	if l.Legacy {
		return l.Raw
	}
	return l.Payload
}

// decodePayload decodes the payload of a kind this package types.
func (l *Line) decodePayload() {
	raw := bytes.TrimSpace(l.PayloadRaw())
	if len(raw) == 0 || raw[0] != '{' {
		switch l.Kind {
		case TypeSessionMeta, TypeResponseItem, TypeEventMsg, TypeTurnContext, TypeCompacted,
			TypeInterAgentCommunication, TypeTokenUsageRecord:
			l.PayloadErr = fmt.Errorf("%s payload is not an object", l.Kind)
		}
		return
	}
	var err error
	switch l.Kind {
	case TypeSessionMeta:
		l.Meta = &SessionMeta{}
		err = json.Unmarshal(raw, l.Meta)
	case TypeResponseItem:
		l.Item = &Item{}
		err = json.Unmarshal(raw, l.Item)
	case TypeEventMsg:
		l.Event = &Event{}
		err = json.Unmarshal(raw, l.Event)
	case TypeTurnContext:
		l.TurnContext = &TurnContext{}
		err = json.Unmarshal(raw, l.TurnContext)
	case TypeCompacted:
		l.Compacted = &Compacted{}
		err = json.Unmarshal(raw, l.Compacted)
	case TypeInterAgentCommunication:
		l.InterAgent = &InterAgent{}
		err = json.Unmarshal(raw, l.InterAgent)
	case TypeTokenUsageRecord:
		l.UsageRecord = &TokenUsageRecord{}
		err = json.Unmarshal(raw, l.UsageRecord)
	}
	if err != nil {
		l.Meta, l.Item, l.Event, l.TurnContext, l.Compacted, l.InterAgent, l.UsageRecord = nil, nil, nil, nil, nil, nil, nil
		l.PayloadErr = err
	}
}

// MistypedMembers lists, as paths, the members of the line and its
// payload whose value was not of the type Codex defines for it, which
// the reader kept as written rather than fail the line.
func (l *Line) MistypedMembers() []string {
	var out []string
	add := func(prefix string, names []string) {
		for _, n := range names {
			out = append(out, prefix+n)
		}
	}
	if !l.Legacy {
		add("", l.Mistyped())
	}
	p := "payload."
	if l.Legacy {
		p = ""
	}
	switch {
	case l.Meta != nil:
		add(p, l.Meta.Mistyped())
		if l.Meta.Git != nil {
			add(p+"git.", l.Meta.Git.Mistyped())
		}
		if l.Meta.BaseInstructions != nil {
			add(p+"base_instructions.", l.Meta.BaseInstructions.Mistyped())
		}
	case l.Item != nil:
		add(p, l.Item.Mistyped())
	case l.Event != nil:
		add(p, l.Event.Mistyped())
	case l.TurnContext != nil:
		add(p, l.TurnContext.Mistyped())
	case l.Compacted != nil:
		add(p, l.Compacted.Mistyped())
	case l.InterAgent != nil:
		add(p, l.InterAgent.Mistyped())
	case l.UsageRecord != nil:
		add(p, l.UsageRecord.Mistyped())
	}
	return out
}

// SessionMeta is the first line's payload: the thread, where it ran and
// what started it. In the legacy layout it is the first line itself,
// with Instructions and no source.
type SessionMeta struct {
	// ID is the thread's id; SessionID the root thread's, which a
	// sub-agent shares.
	ID                         string            `json:"id"`
	SessionID                  string            `json:"session_id"`
	ForkedFromID               string            `json:"forked_from_id"`
	ForkedFromOrdinalExclusive json.Number       `json:"forked_from_ordinal_exclusive"`
	ParentThreadID             string            `json:"parent_thread_id"`
	Timestamp                  string            `json:"timestamp"`
	CWD                        string            `json:"cwd"`
	Originator                 string            `json:"originator"`
	CLIVersion                 string            `json:"cli_version"`
	Source                     json.RawMessage   `json:"source"`
	ThreadSource               string            `json:"thread_source"`
	AgentNickname              string            `json:"agent_nickname"`
	AgentRole                  string            `json:"agent_role"`
	AgentPath                  string            `json:"agent_path"`
	ModelProvider              string            `json:"model_provider"`
	BaseInstructions           *BaseInstructions `json:"base_instructions"`
	DynamicTools               json.RawMessage   `json:"dynamic_tools"`
	MemoryMode                 string            `json:"memory_mode"`
	// HistoryMode is "legacy" (the default) or "paginated".
	HistoryMode string `json:"history_mode"`
	// HistoryBase names a prefix of another rollout this thread
	// inherits by reference, not by copy.
	HistoryBase                 json.RawMessage `json:"history_base"`
	SubagentHistoryStartOrdinal json.Number     `json:"subagent_history_start_ordinal"`
	MultiAgentVersion           string          `json:"multi_agent_version"`
	ContextWindow               json.RawMessage `json:"context_window"`
	Git                         *GitInfo        `json:"git"`
	// Instructions is the legacy layout's user instructions, which later
	// versions moved to the turn context.
	Instructions *string `json:"instructions"`

	Members
}

type plainSessionMeta SessionMeta

// UnmarshalJSON decodes the meta, keeping undeclared members.
func (m *SessionMeta) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainSessionMeta)(m), &m.Members)
}

// MarshalJSON writes the meta back as it was read.
func (m SessionMeta) MarshalJSON() ([]byte, error) {
	return encode(plainSessionMeta(m), &m.Members)
}

// ThreadSpawn is a sub-agent's source: the thread that spawned it.
type ThreadSpawn struct {
	ParentThreadID string `json:"parent_thread_id"`
	Depth          int    `json:"depth"`
	AgentPath      string `json:"agent_path"`
	AgentNickname  string `json:"agent_nickname"`
	AgentRole      string `json:"agent_role"`
}

// SourceKind names the session source: "cli", "vscode", "exec", "mcp",
// a custom string, "subagent" or "internal"; empty when absent.
func (m *SessionMeta) SourceKind() string {
	var s string
	if json.Unmarshal(m.Source, &s) == nil {
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(m.Source, &obj) == nil {
		return firstKey(obj)
	}
	return ""
}

// firstKey returns the least key of an object, the variant of a Rust
// enum serde writes as {"variant": …}; "" for none.
func firstKey(obj map[string]json.RawMessage) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// SubAgent returns the source of a thread spawned by another, with the
// sub-agent's kind ("thread_spawn", "review", "compact",
// "memory_consolidation" or another), or nil when the thread is no
// sub-agent.
func (m *SessionMeta) SubAgent() (kind string, spawn *ThreadSpawn) {
	var obj struct {
		SubAgent json.RawMessage `json:"subagent"`
	}
	if json.Unmarshal(m.Source, &obj) != nil || len(obj.SubAgent) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(obj.SubAgent, &s) == nil {
		return s, nil
	}
	var sub struct {
		ThreadSpawn *ThreadSpawn `json:"thread_spawn"`
	}
	if json.Unmarshal(obj.SubAgent, &sub) == nil && sub.ThreadSpawn != nil {
		return "thread_spawn", sub.ThreadSpawn
	}
	var other map[string]json.RawMessage
	if json.Unmarshal(obj.SubAgent, &other) == nil && len(other) > 0 {
		return firstKey(other), nil
	}
	return "other", nil
}

// BaseInstructions are the thread's model instructions, the request's
// "instructions".
type BaseInstructions struct {
	Text       string          `json:"text"`
	Provenance json.RawMessage `json:"provenance"`

	Members
}

type plainBaseInstructions BaseInstructions

// UnmarshalJSON decodes the instructions, keeping undeclared members.
func (b *BaseInstructions) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainBaseInstructions)(b), &b.Members)
}

// MarshalJSON writes the instructions back as they were read.
func (b BaseInstructions) MarshalJSON() ([]byte, error) {
	return encode(plainBaseInstructions(b), &b.Members)
}

// GitInfo is the repository state when the thread started.
type GitInfo struct {
	CommitHash    string `json:"commit_hash"`
	Branch        string `json:"branch"`
	RepositoryURL string `json:"repository_url"`

	Members
}

type plainGitInfo GitInfo

// UnmarshalJSON decodes the git state, keeping undeclared members.
func (g *GitInfo) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainGitInfo)(g), &g.Members)
}

// MarshalJSON writes the git state back as it was read.
func (g GitInfo) MarshalJSON() ([]byte, error) {
	return encode(plainGitInfo(g), &g.Members)
}

// Item types: a response item's "type" (ResponseItem's variants, and
// the ones earlier versions wrote).
const (
	ItemMessage              = "message"
	ItemAgentMessage         = "agent_message"
	ItemReasoning            = "reasoning"
	ItemLocalShellCall       = "local_shell_call"
	ItemFunctionCall         = "function_call"
	ItemToolSearchCall       = "tool_search_call"
	ItemFunctionCallOutput   = "function_call_output"
	ItemCustomToolCall       = "custom_tool_call"
	ItemCustomToolCallOutput = "custom_tool_call_output"
	ItemToolSearchOutput     = "tool_search_output"
	ItemWebSearchCall        = "web_search_call"
	ItemImageGenerationCall  = "image_generation_call"
	ItemCompaction           = "compaction"
	// ItemCompactionSummary is an alias Codex reads as ItemCompaction.
	ItemCompactionSummary   = "compaction_summary"
	ItemConfigurationUpdate = "configuration_update"
	ItemCompactionTrigger   = "compaction_trigger"
	ItemContextCompaction   = "context_compaction"
	ItemAdditionalTools     = "additional_tools"
	// ItemGhostSnapshot is a git snapshot for undo, which versions of
	// late 2025 and early 2026 recorded and never sent the model.
	ItemGhostSnapshot = "ghost_snapshot"
)

// ItemTypes are the item types Codex defines, at the source this package
// follows or before. Codex reads any other as Other, which it neither
// writes nor sends.
var ItemTypes = map[string]bool{
	ItemMessage: true, ItemAgentMessage: true, ItemReasoning: true, ItemLocalShellCall: true,
	ItemFunctionCall: true, ItemToolSearchCall: true, ItemFunctionCallOutput: true,
	ItemCustomToolCall: true, ItemCustomToolCallOutput: true, ItemToolSearchOutput: true,
	ItemWebSearchCall: true, ItemImageGenerationCall: true, ItemCompaction: true,
	ItemCompactionSummary: true, ItemConfigurationUpdate: true, ItemCompactionTrigger: true,
	ItemContextCompaction: true, ItemAdditionalTools: true, ItemGhostSnapshot: true,
}

// Item is a response item: a Responses API item as Codex records it.
// It holds the members of every item type; Type says which apply.
// Members whose shape varies by type are kept raw.
type Item struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Status string `json:"status"`

	// message, additional_tools.
	Role string `json:"role"`
	// message (content items), reasoning (reasoning text), agent_message.
	Content json.RawMessage `json:"content"`
	// message: commentary, partial_answer or final_answer.
	Phase string `json:"phase"`
	// Codex's own encrypted form of several items, and the reasoning or
	// compaction item's encrypted content.
	EncryptedContent *string `json:"encrypted_content"`

	// reasoning.
	Summary json.RawMessage `json:"summary"`

	// function_call, custom_tool_call and their outputs; local_shell_call,
	// tool_search_call and tool_search_output, where it may be null.
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// function_call: a JSON string; tool_search_call: any value.
	Arguments             json.RawMessage `json:"arguments"`
	EncryptedFunctionArgs json.RawMessage `json:"encrypted_function_args"`
	// custom_tool_call: the free-form input.
	Input string `json:"input"`
	// function_call_output and custom_tool_call_output: a string or an
	// array of content items.
	Output json.RawMessage `json:"output"`

	// local_shell_call and web_search_call.
	Action json.RawMessage `json:"action"`
	// tool_search_call and tool_search_output.
	Execution string `json:"execution"`
	// tool_search_output and additional_tools.
	Tools json.RawMessage `json:"tools"`

	// agent_message.
	Author    string `json:"author"`
	Recipient string `json:"recipient"`

	// image_generation_call.
	RevisedPrompt string `json:"revised_prompt"`
	Result        string `json:"result"`

	// configuration_update.
	Reasoning json.RawMessage `json:"reasoning"`

	// ghost_snapshot.
	GhostCommit json.RawMessage `json:"ghost_commit"`

	// Passthrough is metadata Codex sends the Responses API beside the
	// item: the turn id, creation time, content kinds.
	Passthrough json.RawMessage `json:"internal_chat_message_metadata_passthrough"`

	Members
}

type plainItem Item

// UnmarshalJSON decodes an item, keeping undeclared members.
func (i *Item) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainItem)(i), &i.Members)
}

// MarshalJSON writes the item back as it was read.
func (i Item) MarshalJSON() ([]byte, error) {
	return encode(plainItem(i), &i.Members)
}

// ContentItem is one part of a message's content.
type ContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
	FileID   string `json:"file_id"`
	Detail   string `json:"detail"`
	AudioURL string `json:"audio_url"`
}

// Texts returns the text of a message item's input_text and output_text
// parts, in order; nil when the content is not an array of parts.
func (i *Item) Texts() []string {
	var parts []ContentItem
	if json.Unmarshal(i.Content, &parts) != nil {
		return nil
	}
	var out []string
	for _, p := range parts {
		if p.Type == "input_text" || p.Type == "output_text" {
			out = append(out, p.Text)
		}
	}
	return out
}

// Parts returns a message item's content parts; nil when the content is
// not an array of objects.
func (i *Item) Parts() []ContentItem {
	var parts []ContentItem
	if json.Unmarshal(i.Content, &parts) != nil {
		return nil
	}
	return parts
}

// Event is an event_msg payload: one of Codex's events. It holds the
// members of the events rollouts persist; Type says which apply, and
// every other member is kept.
type Event struct {
	Type string `json:"type"`

	// turn_started, turn_complete, turn_aborted.
	TurnID     string `json:"turn_id"`
	RootTurnID string `json:"root_turn_id"`
	// turn_aborted.
	Reason json.RawMessage `json:"reason"`

	// thread_rolled_back: how many user turns were dropped.
	NumTurns *int64 `json:"num_turns"`

	// token_count.
	Info       *TokenUsageInfo `json:"info"`
	RateLimits json.RawMessage `json:"rate_limits"`

	// user_message and agent_message.
	Message string `json:"message"`
	// user_message.
	Images json.RawMessage `json:"images"`
	// agent_reasoning and agent_reasoning_raw_content.
	Text string `json:"text"`

	// item_completed.
	Item json.RawMessage `json:"item"`

	Members
}

type plainEvent Event

// UnmarshalJSON decodes an event, keeping undeclared members.
func (e *Event) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainEvent)(e), &e.Members)
}

// MarshalJSON writes the event back as it was read.
func (e Event) MarshalJSON() ([]byte, error) {
	return encode(plainEvent(e), &e.Members)
}

// Event types this package names: those rollouts persist (policy.rs, and
// before it core/src/rollout/policy.rs).
const (
	EventTokenCount               = "token_count"
	EventTurnStarted              = "turn_started"
	EventTurnComplete             = "turn_complete"
	EventTurnAborted              = "turn_aborted"
	EventThreadRolledBack         = "thread_rolled_back"
	EventThreadSettingsApplied    = "thread_settings_applied"
	EventThreadGoalUpdated        = "thread_goal_updated"
	EventItemCompleted            = "item_completed"
	EventUserMessage              = "user_message"
	EventAgentMessage             = "agent_message"
	EventAgentReasoning           = "agent_reasoning"
	EventAgentReasoningRawContent = "agent_reasoning_raw_content"
	EventContextCompacted         = "context_compacted"
	EventEnteredReviewMode        = "entered_review_mode"
	EventExitedReviewMode         = "exited_review_mode"
	EventPatchApplyEnd            = "patch_apply_end"
	EventMcpToolCallEnd           = "mcp_tool_call_end"
	EventWebSearchEnd             = "web_search_end"
	EventImageGenerationEnd       = "image_generation_end"
	EventSubAgentActivity         = "sub_agent_activity"
	EventUndoCompleted            = "undo_completed"
)

// TokenUsage is Codex's token accounting for one response or a total.
// InputTokens counts the cached input among it, as OpenAI counts it.
type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`

	Members
}

type plainTokenUsage TokenUsage

// UnmarshalJSON decodes usage, keeping undeclared members.
func (u *TokenUsage) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainTokenUsage)(u), &u.Members)
}

// MarshalJSON writes usage back as it was read.
func (u TokenUsage) MarshalJSON() ([]byte, error) {
	return encode(plainTokenUsage(u), &u.Members)
}

// TokenUsageInfo is a token_count event's usage: the thread's running
// total and the last response's.
type TokenUsageInfo struct {
	TotalTokenUsage    *TokenUsage `json:"total_token_usage"`
	LastTokenUsage     *TokenUsage `json:"last_token_usage"`
	ModelContextWindow *int64      `json:"model_context_window"`

	Members
}

type plainTokenUsageInfo TokenUsageInfo

// UnmarshalJSON decodes the usage info, keeping undeclared members.
func (u *TokenUsageInfo) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainTokenUsageInfo)(u), &u.Members)
}

// MarshalJSON writes the usage info back as it was read.
func (u TokenUsageInfo) MarshalJSON() ([]byte, error) {
	return encode(plainTokenUsageInfo(u), &u.Members)
}

// TokenUsageRecord is one completed response's usage, with the response
// id, which newer rollouts write as a line of its own.
type TokenUsageRecord struct {
	ThreadID         string      `json:"thread_id"`
	TurnID           string      `json:"turn_id"`
	SessionID        string      `json:"session_id"`
	RootTurnID       string      `json:"root_turn_id"`
	ResponseID       string      `json:"response_id"`
	Usage            *TokenUsage `json:"usage"`
	TurnTokenUsage   *TokenUsage `json:"turn_token_usage"`
	ThreadTokenUsage *TokenUsage `json:"thread_token_usage"`

	Members
}

type plainTokenUsageRecord TokenUsageRecord

// UnmarshalJSON decodes the record, keeping undeclared members.
func (r *TokenUsageRecord) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainTokenUsageRecord)(r), &r.Members)
}

// MarshalJSON writes the record back as it was read.
func (r TokenUsageRecord) MarshalJSON() ([]byte, error) {
	return encode(plainTokenUsageRecord(r), &r.Members)
}

// TurnContext is the settings of one turn, written once per user turn
// and after a compaction re-established them: a snapshot, not a delta.
type TurnContext struct {
	TurnID            string          `json:"turn_id"`
	RootTurnID        string          `json:"root_turn_id"`
	CWD               string          `json:"cwd"`
	ApprovalPolicy    json.RawMessage `json:"approval_policy"`
	SandboxPolicy     json.RawMessage `json:"sandbox_policy"`
	PermissionProfile json.RawMessage `json:"permission_profile"`
	Model             string          `json:"model"`
	// Effort is the reasoning effort: none, minimal, low, medium, high,
	// xhigh, or absent for the model's default.
	Effort string `json:"effort"`
	// Summary is the reasoning summary setting: auto, concise, detailed
	// or none.
	Summary           json.RawMessage `json:"summary"`
	CollaborationMode json.RawMessage `json:"collaboration_mode"`
	// UserInstructions and DeveloperInstructions are what versions of
	// early 2026 recorded here; the text the model saw is in the
	// developer and user message items.
	UserInstructions      *string `json:"user_instructions"`
	DeveloperInstructions *string `json:"developer_instructions"`
	// TruncationPolicy is how tool output was cut before it was sent,
	// {"mode":"bytes"|"tokens","limit":n}, which those versions recorded.
	TruncationPolicy json.RawMessage `json:"truncation_policy"`

	Members
}

// Truncation returns the turn's tool output truncation policy, when it
// recorded one.
func (t *TurnContext) Truncation() (mode string, limit int64, ok bool) {
	var p struct {
		Mode  string `json:"mode"`
		Limit *int64 `json:"limit"`
	}
	if json.Unmarshal(t.TruncationPolicy, &p) != nil || p.Mode == "" || p.Limit == nil {
		return "", 0, false
	}
	return p.Mode, *p.Limit, true
}

type plainTurnContext TurnContext

// UnmarshalJSON decodes the turn context, keeping undeclared members.
func (t *TurnContext) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainTurnContext)(t), &t.Members)
}

// MarshalJSON writes the turn context back as it was read.
func (t TurnContext) MarshalJSON() ([]byte, error) {
	return encode(plainTurnContext(t), &t.Members)
}

// SummaryMode returns the reasoning summary setting when it is a string.
func (t *TurnContext) SummaryMode() string {
	var s string
	if json.Unmarshal(t.Summary, &s) == nil {
		return s
	}
	return ""
}

// Compacted is a compaction: the summary text and, since late 2025, the
// whole history the model window was replaced with.
type Compacted struct {
	Message string `json:"message"`
	// ReplacementHistory is the new window's items, in order; absent in
	// older rollouts, which rebuilt it from the user messages on resume.
	ReplacementHistory         []json.RawMessage `json:"replacement_history"`
	ReplacementHistoryMetadata json.RawMessage   `json:"replacement_history_metadata"`
	WindowNumber               json.Number       `json:"window_number"`
	CompactionResponseID       string            `json:"compaction_response_id"`
	LatestTokenUsageRecord     json.RawMessage   `json:"latest_token_usage_record"`
	ResumeMetadata             json.RawMessage   `json:"resume_metadata"`

	Members
}

type plainCompacted Compacted

// UnmarshalJSON decodes the compaction, keeping undeclared members.
func (c *Compacted) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainCompacted)(c), &c.Members)
}

// MarshalJSON writes the compaction back as it was read.
func (c Compacted) MarshalJSON() ([]byte, error) {
	return encode(plainCompacted(c), &c.Members)
}

// HasReplacement reports whether the compaction carries its replacement
// history, null or absent being the older form.
func (c *Compacted) HasReplacement() bool {
	return c.Has("replacement_history") && !c.IsNull("replacement_history")
}

// InterAgent is a message between agents of a multi-agent thread, which
// Codex puts in the recipient's context as an agent_message item.
type InterAgent struct {
	ID               string          `json:"id"`
	Author           string          `json:"author"`
	Recipient        string          `json:"recipient"`
	OtherRecipients  json.RawMessage `json:"other_recipients"`
	Content          string          `json:"content"`
	EncryptedContent string          `json:"encrypted_content"`
	Passthrough      json.RawMessage `json:"internal_chat_message_metadata_passthrough"`
	TriggerTurn      bool            `json:"trigger_turn"`

	Members
}

type plainInterAgent InterAgent

// UnmarshalJSON decodes the message, keeping undeclared members.
func (a *InterAgent) UnmarshalJSON(data []byte) error {
	return decodeLenient(data, (*plainInterAgent)(a), &a.Members)
}

// MarshalJSON writes the message back as it was read.
func (a InterAgent) MarshalJSON() ([]byte, error) {
	return encode(plainInterAgent(a), &a.Members)
}

// ModelInput returns the agent_message item Codex puts in the
// recipient's context for the message (InterAgentCommunication's
// to_model_input_item): the text, or for an encrypted message a header
// naming the task and sender followed by the encrypted payload.
func (a *InterAgent) ModelInput() map[string]any {
	var content []map[string]any
	if a.EncryptedContent != "" {
		kind := "MESSAGE"
		if a.TriggerTurn {
			kind = "NEW_TASK"
		}
		content = []map[string]any{
			{"type": "input_text", "text": fmt.Sprintf("Message Type: %s\nTask name: %s\nSender: %s\nPayload:\n", kind, a.Recipient, a.Author)},
			{"type": "encrypted_content", "encrypted_content": a.EncryptedContent},
		}
	} else {
		content = []map[string]any{{"type": "input_text", "text": a.Content}}
	}
	item := map[string]any{"type": ItemAgentMessage, "author": a.Author, "recipient": a.Recipient, "content": content}
	if a.ID != "" {
		item["id"] = a.ID
	}
	if len(a.Passthrough) > 0 && !bytes.Equal(bytes.TrimSpace(a.Passthrough), []byte("null")) {
		item["internal_chat_message_metadata_passthrough"] = a.Passthrough
	}
	return item
}
