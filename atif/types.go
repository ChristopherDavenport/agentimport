package atif

import (
	"bytes"
	"encoding/json"
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

// Step sources.
const (
	SourceSystem = "system"
	SourceUser   = "user"
	SourceAgent  = "agent"
)

// Content part types.
const (
	PartText  = "text"
	PartImage = "image"
	PartAudio = "audio"
)

// Versions lists the schema versions Harbor's models accept, oldest
// first. A document may declare another; it is read with the newest
// version's members.
var Versions = []string{
	"ATIF-v1.0", "ATIF-v1.1", "ATIF-v1.2", "ATIF-v1.3", "ATIF-v1.4",
	"ATIF-v1.5", "ATIF-v1.6", "ATIF-v1.7", "ATIF-v1.8",
}

// Trajectory is one ATIF document: the root object, or a sub-agent's
// trajectory embedded in its parent's subagent_trajectories.
type Trajectory struct {
	SchemaVersion string `json:"schema_version"`
	// SessionID is scoped to the run, not the document: a parent, its
	// sub-agents and its continuations may share it.
	SessionID string `json:"session_id"`
	// TrajectoryID names this document; an embedded sub-agent must set
	// it, and a reference names the sub-agent by it.
	TrajectoryID string          `json:"trajectory_id"`
	Agent        *Agent          `json:"agent"`
	Steps        []*Step         `json:"steps"`
	Notes        string          `json:"notes"`
	FinalMetrics *Object         `json:"final_metrics"`
	ContinuedRef string          `json:"continued_trajectory_ref"`
	Ext          json.RawMessage `json:"extra"`
	// SubagentsRaw is subagent_trajectories as written, and Subagents
	// the trajectories it embeds, each of which may embed its own,
	// decoded one by one after this one: decoding them with it would
	// decode a nested trajectory again at every level whose members
	// need a second pass. SubagentsMistyped says the member is not an
	// array.
	SubagentsRaw      json.RawMessage `json:"subagent_trajectories"`
	Subagents         []*Trajectory   `json:"-"`
	SubagentsMistyped bool            `json:"-"`

	Members

	// Raw is the object as read, and Err why it did not decode, for a
	// value that is not an object; nothing else is then set.
	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainTrajectory Trajectory

// UnmarshalJSON decodes a trajectory, keeping undeclared members and
// members of another JSON type. It fails only on JSON that does not
// parse; a value that is not an object is kept with Err set.
func (t *Trajectory) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainTrajectory)(t), &t.Members); err != nil {
		*t = Trajectory{Raw: bytes.Clone(data), Err: err}
		return nil
	}
	if IsNull(t.SubagentsRaw) {
		return nil
	}
	var raws []json.RawMessage
	if json.Unmarshal(t.SubagentsRaw, &raws) != nil {
		t.SubagentsMistyped = true
		return nil
	}
	t.Subagents = make([]*Trajectory, len(raws))
	for i, raw := range raws {
		if IsNull(raw) {
			continue
		}
		sub := &Trajectory{}
		if err := json.Unmarshal(raw, sub); err != nil {
			sub = &Trajectory{Raw: bytes.Clone(raw), Err: err}
		}
		t.Subagents[i] = sub
	}
	return nil
}

// MarshalJSON writes the trajectory back as it was read.
func (t Trajectory) MarshalJSON() ([]byte, error) {
	if t.Err != nil {
		return t.Raw, nil
	}
	return jsonline.Encode(plainTrajectory(t), &t.Members)
}

// Units is the number of units the document accounts for: the root and
// each of its steps, and the same for every embedded trajectory, at any
// depth. A step or embedded trajectory that is null or not an object
// counts as one unit; an embedded one that is not an object counts its
// root alone.
func (t *Trajectory) Units() int {
	if t == nil {
		return 0
	}
	n := 1 + len(t.Steps)
	for _, s := range t.Subagents {
		if s == nil {
			n++
			continue
		}
		n += s.Units()
	}
	return n
}

// Agent describes the agent that produced the trajectory.
type Agent struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	ModelName string `json:"model_name"`
	// ToolDefinitions are OpenAI chat-completions tool objects,
	// {"type":"function","function":{name,description,parameters}},
	// kept as written.
	ToolDefinitions []json.RawMessage `json:"tool_definitions"`
	Ext             json.RawMessage   `json:"extra"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainAgent Agent

// UnmarshalJSON decodes an agent, keeping undeclared members; a value
// that is not an object is kept with Err set.
func (a *Agent) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainAgent)(a), &a.Members); err != nil {
		*a = Agent{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the agent back as it was read.
func (a Agent) MarshalJSON() ([]byte, error) {
	if a.Err != nil {
		return a.Raw, nil
	}
	return jsonline.Encode(plainAgent(a), &a.Members)
}

// Step is one turn: a system prompt or message, a user message, or one
// agent response with the observations its actions produced.
type Step struct {
	// StepID is the 1-based ordinal, kept as written.
	StepID    json.RawMessage `json:"step_id"`
	Timestamp string          `json:"timestamp"`
	Source    string          `json:"source"`
	ModelName string          `json:"model_name"`
	// ReasoningEffort is a string or a number, kept as written.
	ReasoningEffort  json.RawMessage `json:"reasoning_effort"`
	Message          Content         `json:"message"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCalls        []*ToolCall     `json:"tool_calls"`
	Observation      *Observation    `json:"observation"`
	Metrics          *Metrics        `json:"metrics"`
	IsCopiedContext  *bool           `json:"is_copied_context"`
	// LLMCallCount is the number of model calls the step stands for,
	// kept as written: 0 marks a deterministic dispatch no model made.
	LLMCallCount json.RawMessage `json:"llm_call_count"`
	Ext          json.RawMessage `json:"extra"`

	Members

	// Raw is the step as read, and Err why it did not decode, for a
	// value that is not an object.
	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainStep Step

// UnmarshalJSON decodes a step, keeping undeclared members and members
// of another JSON type; a value that is not an object is kept with Err
// set.
func (s *Step) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainStep)(s), &s.Members); err != nil {
		*s = Step{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the step back as it was read.
func (s Step) MarshalJSON() ([]byte, error) {
	if s.Err != nil {
		return s.Raw, nil
	}
	return jsonline.Encode(plainStep(s), &s.Members)
}

// Copied reports whether the step is marked as copied context.
func (s *Step) Copied() bool { return s.IsCopiedContext != nil && *s.IsCopiedContext }

// ToolCall is one call an agent step made.
type ToolCall struct {
	ToolCallID   string `json:"tool_call_id"`
	FunctionName string `json:"function_name"`
	// Arguments is a JSON object in a valid document, kept as written.
	Arguments json.RawMessage `json:"arguments"`
	Ext       json.RawMessage `json:"extra"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainToolCall ToolCall

// UnmarshalJSON decodes a tool call; a value that is not an object is
// kept with Err set.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainToolCall)(c), &c.Members); err != nil {
		*c = ToolCall{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the tool call back as it was read.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	if c.Err != nil {
		return c.Raw, nil
	}
	return jsonline.Encode(plainToolCall(c), &c.Members)
}

// Observation holds what a step's actions produced.
type Observation struct {
	Results []*Result `json:"results"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainObservation Observation

// UnmarshalJSON decodes an observation, keeping undeclared members; a value
// that is not an object is kept with Err set.
func (o *Observation) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainObservation)(o), &o.Members); err != nil {
		*o = Observation{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the observation back as it was read.
func (o Observation) MarshalJSON() ([]byte, error) {
	if o.Err != nil {
		return o.Raw, nil
	}
	return jsonline.Encode(plainObservation(o), &o.Members)
}

// Result is one observation result: a tool call's output, or, with no
// source_call_id, the output of a non-tool action or system operation.
type Result struct {
	// SourceCallID names a tool call of the same step; empty when the
	// result names none (absent or null).
	SourceCallID string          `json:"source_call_id"`
	Content      Content         `json:"content"`
	Refs         Refs            `json:"subagent_trajectory_ref"`
	Ext          json.RawMessage `json:"extra"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainResult Result

// UnmarshalJSON decodes a result; a value that is not an object is kept
// with Err set.
func (r *Result) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainResult)(r), &r.Members); err != nil {
		*r = Result{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the result back as it was read.
func (r Result) MarshalJSON() ([]byte, error) {
	if r.Err != nil {
		return r.Raw, nil
	}
	return jsonline.Encode(plainResult(r), &r.Members)
}

// Refs is a result's subagent_trajectory_ref: a list of references, or,
// as RFC 0001 v1.0 wrote it, one reference object.
type Refs struct {
	List []*Ref
	// Object says the member was one object rather than a list.
	Object bool
	// Raw is a value that is neither, kept as written.
	Raw json.RawMessage
}

// UnmarshalJSON accepts a list or one object; anything else is kept in
// Raw.
func (r *Refs) UnmarshalJSON(data []byte) error {
	*r = Refs{}
	trimmed := bytes.TrimSpace(data)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
	case len(trimmed) > 0 && trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &r.List); err != nil {
			r.List, r.Raw = nil, bytes.Clone(data)
		} else if r.List == nil {
			r.List = []*Ref{}
		}
	case len(trimmed) > 0 && trimmed[0] == '{':
		ref := &Ref{}
		if err := json.Unmarshal(trimmed, ref); err != nil {
			r.Raw = bytes.Clone(data)
			return nil
		}
		r.List, r.Object = []*Ref{ref}, true
	default:
		r.Raw = bytes.Clone(data)
	}
	return nil
}

// MarshalJSON writes the references back in the form they were read.
func (r Refs) MarshalJSON() ([]byte, error) {
	switch {
	case r.Raw != nil:
		return r.Raw, nil
	case r.Object && len(r.List) == 1:
		return json.Marshal(r.List[0])
	case r.List == nil:
		return []byte("null"), nil
	}
	return json.Marshal(r.List)
}

// Ref names a sub-agent's trajectory: by trajectory_id, an embedded
// trajectory of the same document, or by trajectory_path, a file.
// session_id alone, as references before v1.7 could, resolves nothing.
type Ref struct {
	TrajectoryID   string          `json:"trajectory_id"`
	SessionID      string          `json:"session_id"`
	TrajectoryPath string          `json:"trajectory_path"`
	Ext            json.RawMessage `json:"extra"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainRef Ref

// UnmarshalJSON decodes a reference; a value that is not an object is
// kept with Err set.
func (r *Ref) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainRef)(r), &r.Members); err != nil {
		*r = Ref{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the reference back as it was read.
func (r Ref) MarshalJSON() ([]byte, error) {
	if r.Err != nil {
		return r.Raw, nil
	}
	return jsonline.Encode(plainRef(r), &r.Members)
}

// Metrics is an agent step's usage. Counts are kept as written.
type Metrics struct {
	// PromptTokens counts all input, cached tokens included.
	PromptTokens json.RawMessage `json:"prompt_tokens"`
	// CompletionTokens counts all output, reasoning included.
	CompletionTokens json.RawMessage `json:"completion_tokens"`
	// CachedTokens is the part of PromptTokens read from a cache.
	CachedTokens       json.RawMessage `json:"cached_tokens"`
	CostUSD            json.RawMessage `json:"cost_usd"`
	PromptTokenIDs     json.RawMessage `json:"prompt_token_ids"`
	CompletionTokenIDs json.RawMessage `json:"completion_token_ids"`
	Logprobs           json.RawMessage `json:"logprobs"`
	Ext                json.RawMessage `json:"extra"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainMetrics Metrics

// UnmarshalJSON decodes metrics, keeping undeclared members; a value
// that is not an object is kept with Err set.
func (m *Metrics) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainMetrics)(m), &m.Members); err != nil {
		*m = Metrics{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the metrics back as they were read.
func (m Metrics) MarshalJSON() ([]byte, error) {
	if m.Err != nil {
		return m.Raw, nil
	}
	return jsonline.Encode(plainMetrics(m), &m.Members)
}

// Content is a message or result content: a string, or, since v1.6, a
// list of parts.
type Content struct {
	Text  string
	Parts []*Part
	// IsText says the value was a string, the empty string included.
	IsText bool
	// Raw is a value that is neither, kept as written.
	Raw json.RawMessage
}

// UnmarshalJSON accepts a string or a list of parts; anything else is
// kept in Raw.
func (c *Content) UnmarshalJSON(data []byte) error {
	*c = Content{}
	trimmed := bytes.TrimSpace(data)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
	case len(trimmed) > 0 && trimmed[0] == '"':
		if err := json.Unmarshal(trimmed, &c.Text); err != nil {
			c.Raw = bytes.Clone(data)
			return nil
		}
		c.IsText = true
	case len(trimmed) > 0 && trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &c.Parts); err != nil {
			c.Parts, c.Raw = nil, bytes.Clone(data)
		} else if c.Parts == nil {
			c.Parts = []*Part{}
		}
	default:
		c.Raw = bytes.Clone(data)
	}
	return nil
}

// MarshalJSON writes the content back in the form it was read.
func (c Content) MarshalJSON() ([]byte, error) {
	switch {
	case c.Raw != nil:
		return c.Raw, nil
	case c.Parts != nil:
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

// IsZero reports whether the content is absent or null.
func (c Content) IsZero() bool { return !c.IsText && c.Parts == nil && c.Raw == nil }

// String is the content's text: the string, or the text parts joined
// with newlines.
func (c Content) String() string {
	if c.Parts == nil {
		return c.Text
	}
	var texts []string
	for _, p := range c.Parts {
		if p != nil && p.Err == nil && p.Type == PartText {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// Part is one content part: text, an image or, since v1.8, audio.
// Media is always named by a path or URL, never inline.
type Part struct {
	Type   string  `json:"type"`
	Text   string  `json:"text"`
	Source *Source `json:"source"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainPart Part

// UnmarshalJSON decodes a part; a value that is not an object is kept
// with Err set.
func (p *Part) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainPart)(p), &p.Members); err != nil {
		*p = Part{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the part back as it was read.
func (p Part) MarshalJSON() ([]byte, error) {
	if p.Err != nil {
		return p.Raw, nil
	}
	return jsonline.Encode(plainPart(p), &p.Members)
}

// Source is a media part's file: an image's or an audio clip's.
type Source struct {
	MediaType string `json:"media_type"`
	// Path is relative to the document's directory, absolute, or a URL.
	Path string `json:"path"`
	// DurationSec is an audio clip's length, kept as written.
	DurationSec json.RawMessage `json:"duration_sec"`

	Members

	Raw json.RawMessage `json:"-"`
	Err error           `json:"-"`
}

type plainSource Source

// UnmarshalJSON decodes a source, keeping undeclared members; a value
// that is not an object is kept with Err set.
func (s *Source) UnmarshalJSON(data []byte) error {
	if err := jsonline.DecodeLenient(data, (*plainSource)(s), &s.Members); err != nil {
		*s = Source{Raw: bytes.Clone(data), Err: err}
	}
	return nil
}

// MarshalJSON writes the source back as it was read.
func (s Source) MarshalJSON() ([]byte, error) {
	if s.Err != nil {
		return s.Raw, nil
	}
	return jsonline.Encode(plainSource(s), &s.Members)
}

// Object is a JSON object kept as written, such as final_metrics, which
// the converter carries but does not interpret.
type Object struct {
	Raw json.RawMessage
}

// UnmarshalJSON keeps the value as written.
func (o *Object) UnmarshalJSON(data []byte) error {
	o.Raw = bytes.Clone(data)
	return nil
}

// MarshalJSON writes the value back.
func (o Object) MarshalJSON() ([]byte, error) { return o.Raw, nil }

// Int reads a count kept as written: an integer, or a number with no
// fraction such as 12.0, or a string of one, as Harbor's models read it
// leniently. plain says the value was a bare integer. ok is false for
// an absent or null value, or one that is no integer.
func Int(raw json.RawMessage) (n int64, plain, ok bool) {
	s := string(bytes.TrimSpace(raw))
	if s == "" || s == "null" {
		return 0, false, false
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, true, true
	}
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, false, false
		}
		s = strings.TrimSpace(s)
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, false, true
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false, false
	}
	return int64(f), false, true
}

// Float reads a number kept as written, or a string of one.
func Float(raw json.RawMessage) (float64, bool) {
	s := string(bytes.TrimSpace(raw))
	if s == "" || s == "null" {
		return 0, false
	}
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// IsNull reports whether raw is absent or JSON null.
func IsNull(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) == 0 || bytes.Equal(s, []byte("null"))
}
