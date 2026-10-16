package openhands

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// State is base_state.json: the conversation's ConversationState,
// rewritten on every change. Events are not in it.
type State struct {
	ID                 string          `json:"id"`
	Agent              json.RawMessage `json:"agent"`
	Workspace          json.RawMessage `json:"workspace"`
	ExecutionStatus    string          `json:"execution_status"`
	ConfirmationPolicy json.RawMessage `json:"confirmation_policy"`
	LeafEventID        string          `json:"leaf_event_id"`
	HeadIsEmpty        bool            `json:"head_is_empty"`
	Stats              *Stats          `json:"stats"`

	Members
}

type plainState State

// UnmarshalJSON decodes the state, keeping undeclared members and
// members of another JSON type.
func (s *State) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainState)(s), &s.Members)
}

// MarshalJSON writes the state back as it was read.
func (s State) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainState(s), &s.Members)
}

// AgentInfo is what the converter reads of the agent's configuration.
type AgentInfo struct {
	// Kind is "Agent", or "ACPAgent" for an agent that drives an
	// external ACP agent.
	Kind string
	// Model, UsageID and ReasoningEffort are the LLM's, as last saved.
	Model, UsageID, ReasoningEffort string
}

// AgentKindACP is the kind of an agent that drives an external ACP
// agent, such as Claude Code, and records its tool calls rather than a
// model context.
const AgentKindACP = "ACPAgent"

// AgentInfo reads the agent's kind and LLM.
func (s *State) AgentInfo() AgentInfo {
	var a struct {
		Kind json.RawMessage `json:"kind"`
		LLM  json.RawMessage `json:"llm"`
	}
	var info AgentInfo
	if json.Unmarshal(s.Agent, &a) != nil {
		return info
	}
	_ = json.Unmarshal(a.Kind, &info.Kind)
	var llm map[string]json.RawMessage
	if json.Unmarshal(a.LLM, &llm) == nil {
		_ = json.Unmarshal(llm["model"], &info.Model)
		_ = json.Unmarshal(llm["usage_id"], &info.UsageID)
		_ = json.Unmarshal(llm["reasoning_effort"], &info.ReasoningEffort)
	}
	return info
}

// WorkingDir reads the workspace's working directory.
func (s *State) WorkingDir() string {
	var w map[string]json.RawMessage
	var dir string
	if json.Unmarshal(s.Workspace, &w) == nil {
		_ = json.Unmarshal(w["working_dir"], &dir)
	}
	return dir
}

// Stats is the conversation's per-LLM metrics, by usage ID: "agent",
// "condenser", an LLM's own usage_id, and the copies of sub-agents'
// metrics under "task:<id>" and "delegate:<id>".
type Stats struct {
	UsageToMetrics MetricsMap `json:"usage_to_metrics"`

	Members
}

type plainStats Stats

// UnmarshalJSON decodes the stats, keeping undeclared members.
func (s *Stats) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainStats)(s), &s.Members)
}

// MarshalJSON writes the stats back as it was read.
func (s Stats) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainStats(s), &s.Members)
}

// MetricsMap is usage_to_metrics, read value by value.
type MetricsMap map[string]*Metrics

// UnmarshalJSON reads each value on its own: one that is not an object
// is kept with its error.
func (m *MetricsMap) UnmarshalJSON(data []byte) error {
	var raws map[string]json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	*m = MetricsMap{}
	for k, raw := range raws {
		x := &Metrics{}
		_ = x.UnmarshalJSON(raw)
		(*m)[k] = x
	}
	return nil
}

// Keys returns the usage IDs, sorted.
func (m MetricsMap) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Metrics is one LLM's metrics: every call's token usage, latency and
// cost.
type Metrics struct {
	ModelName         string        `json:"model_name"`
	Costs             []*Cost       `json:"costs"`
	ResponseLatencies []*Latency    `json:"response_latencies"`
	TokenUsages       []*TokenUsage `json:"token_usages"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainMetrics Metrics

// UnmarshalJSON decodes the metrics, keeping undeclared members; a
// value that is not an object is kept with Err set.
func (x *Metrics) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		*x = Metrics{Raw: bytes.Clone(data), Err: errNotObject}
		return nil
	}
	if err := jsonline.DecodeLenient(data, (*plainMetrics)(x), &x.Members); err != nil {
		*x = Metrics{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the metrics back as they were read.
func (x Metrics) MarshalJSON() ([]byte, error) {
	if x.Err != nil {
		return x.Raw, nil
	}
	return jsonline.Encode(plainMetrics(x), &x.Members)
}

// Cost is one call's cost. It names no response.
type Cost struct {
	Model     string          `json:"model"`
	Cost      json.RawMessage `json:"cost"`
	Timestamp json.RawMessage `json:"timestamp"`

	Members
}

type plainCost Cost

// UnmarshalJSON decodes the cost, keeping undeclared members.
func (c *Cost) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainCost)(c), &c.Members)
}

// MarshalJSON writes the cost back as it was read.
func (c Cost) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainCost(c), &c.Members)
}

// Latency is one call's latency in seconds.
type Latency struct {
	Model      string          `json:"model"`
	Latency    json.RawMessage `json:"latency"`
	ResponseID string          `json:"response_id"`

	Members
}

type plainLatency Latency

// UnmarshalJSON decodes the latency, keeping undeclared members.
func (l *Latency) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainLatency)(l), &l.Members)
}

// MarshalJSON writes the latency back as it was read.
func (l Latency) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainLatency(l), &l.Members)
}

// TokenUsage is one call's token counts. PromptTokens is litellm's,
// which counts cache reads and writes, except for an ACPAgent, which
// counts them apart.
type TokenUsage struct {
	Model            string          `json:"model"`
	PromptTokens     json.RawMessage `json:"prompt_tokens"`
	CompletionTokens json.RawMessage `json:"completion_tokens"`
	CacheReadTokens  json.RawMessage `json:"cache_read_tokens"`
	CacheWriteTokens json.RawMessage `json:"cache_write_tokens"`
	ReasoningTokens  json.RawMessage `json:"reasoning_tokens"`
	ContextWindow    json.RawMessage `json:"context_window"`
	PerTurnToken     json.RawMessage `json:"per_turn_token"`
	ResponseID       string          `json:"response_id"`

	Members
}

type plainTokenUsage TokenUsage

// UnmarshalJSON decodes the usage, keeping undeclared members.
func (u *TokenUsage) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainTokenUsage)(u), &u.Members)
}

// MarshalJSON writes the usage back as it was read.
func (u TokenUsage) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainTokenUsage(u), &u.Members)
}

// Count reads a token count: 0 for one that is absent, negative, not a
// whole number or over a billion billion.
func Count(raw json.RawMessage) int {
	n, exact, ok := Int(raw)
	if !ok || !exact || n < 0 || n > 1<<53 {
		return 0
	}
	return int(n)
}

// Meta is meta.json, the agent server's StoredConversation.
type Meta struct {
	ID                       string `json:"id"`
	Title                    string `json:"title"`
	CreatedAt                string `json:"created_at"`
	UpdatedAt                string `json:"updated_at"`
	ForkedFromConversationID string `json:"forked_from_conversation_id"`
	ForkedFromEventID        string `json:"forked_from_event_id"`

	Members
}

type plainMeta Meta

// UnmarshalJSON decodes the metadata, keeping undeclared members and
// members of another JSON type.
func (m *Meta) UnmarshalJSON(data []byte) error {
	return jsonline.DecodeLenient(data, (*plainMeta)(m), &m.Members)
}

// MarshalJSON writes the metadata back as it was read.
func (m Meta) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainMeta(m), &m.Members)
}

// isoTime matches Python's datetime.isoformat: a date, a T, a time with
// up to six fractional digits, and an optional zone.
var isoTime = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?(Z|[+-]\d{2}:?\d{2}(?::\d{2}(?:\.\d+)?)?)?$`)

// ParseTime reads a timestamp as OpenHands writes it,
// datetime.now().isoformat(): local time with no zone, which takes loc.
// naive says the timestamp had no zone. A time outside the years 1 to
// 9999 is none.
func ParseTime(s string, loc *time.Location) (t time.Time, naive, ok bool) {
	m := isoTime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, false, false
	}
	if loc == nil {
		loc = time.UTC
	}
	layout := "2006-01-02T15:04:05"
	value := m[1] + "-" + m[2] + "-" + m[3] + "T" + m[4] + ":" + m[5] + ":" + m[6]
	if m[7] != "" {
		layout += ".999999999"
		value += m[7]
	}
	zone := m[8]
	if zone == "" {
		t, err := time.ParseInLocation(layout, value, loc)
		if err != nil {
			return time.Time{}, false, false
		}
		return t, true, true
	}
	if zone != "Z" && zone != "z" {
		// Python writes seconds in an offset only when they are not
		// zero; they are dropped here.
		zone = zone[:3] + ":" + strings.TrimPrefix(zone[3:], ":")[:2]
	} else {
		zone = "Z"
	}
	t, err := time.Parse(layout+"Z07:00", value+zone)
	if err != nil {
		return time.Time{}, false, false
	}
	return t, false, true
}

// errLarge is the error for a file over MaxSize.
var errLarge = errors.New("the file is larger than the reader reads")
