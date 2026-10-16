package convert

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// docInput is one document to convert.
type docInput struct {
	t *atif.Trajectory
	// file is the file the document was read from; nil for an embedded
	// trajectory.
	file *atif.File
	// name names the document in findings: empty for the main one, a
	// path relative to the conversion's directory for another file, and
	// "#" and the trajectory_id (or index) after its parent's name for
	// an embedded one.
	name string
	// dir is the directory relative paths in the document resolve
	// against.
	dir string
	// real is the file's real path, for a document read from a file
	// other than the main one.
	real string
	// synthetic says the document is steps of another, a sub-agent's
	// that Harbor's Claude Code adapter interleaved: its root is not a
	// unit of the input, and the producer's notes are the other's.
	synthetic bool
	// pos is, for a synthetic document, each step's index in the other.
	pos []int
}

// target is the session a document becomes.
type target struct {
	id string
	// native is the document's session_id; derived says why the session
	// ID is not it, empty when it is.
	native, derived string
	// parent and spawnedBy are set for a sub-agent's or continuation's
	// session.
	parent, spawnedBy string
	// created is the time the parent named the document at, the
	// fallback for a document with no timestamp.
	created time.Time
}

// one converts one document into one session, its findings in report,
// and the documents it names into theirs. It returns the session, then
// the sessions of the sub-agents it spawned, then its continuation's,
// and the entry the session's leaf is.
func (fam *family) one(in docInput, tgt target, report *agentimport.Report) ([]*agentsession.Session, string, error) {
	c := &docConv{
		fam: fam, in: in, t: in.t, tgt: tgt, report: report,
		disp:    map[int]agentimport.Disposition{},
		byTID:   map[string]*atif.Trajectory{},
		counts:  map[string]int{},
		firstAt: map[string]int{},
	}
	c.splitSidechains()
	h, err := c.header()
	if err != nil {
		return nil, "", err
	}
	c.b = agentimport.NewBuilder(h, Source, report)
	c.b.File = in.name
	if err := c.run(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", displayName(in.name), err)
	}
	s, err := agentimport.Finish(c.b.S)
	if err != nil {
		return nil, "", fmt.Errorf("%s: converted session does not verify: %w", displayName(in.name), err)
	}
	out := append([]*agentsession.Session{s}, c.children...)
	return append(out, c.after...), c.b.Resume(), nil
}

func displayName(name string) string {
	if name == "" {
		return "main document"
	}
	return name
}

// docConv converts one document.
type docConv struct {
	fam    *family
	in     docInput
	t      *atif.Trajectory
	tgt    target
	report *agentimport.Report
	b      *agentimport.Builder

	// disp records where each step, by index, ended up.
	disp map[int]agentimport.Disposition
	// leading marks the leading system steps the instructions hold.
	leading map[int]bool
	tail    string
	// last is the time of the entry appended last; created the
	// header's.
	last, created time.Time
	// settings are those in force: the model and effort steps change.
	settings agentsession.Settings
	// exporter says agentsession's exporter wrote the document.
	exporter bool
	// byTID holds the trajectories this one embeds, by trajectory_id.
	byTID map[string]*atif.Trajectory
	// calls are the document's tool calls in order.
	calls []*call
	// omitted lists the entries the exporter's document says the
	// request at its leaf leaves out.
	omitted []string
	// children are the sessions of the sub-agents converted with this
	// document; after its continuation's.
	children, after []*agentsession.Session
	// counts and firstAt gather findings reported once per document.
	counts  map[string]int
	firstAt map[string]int
	// sidechains are the Claude Code sub-agents whose steps the
	// document interleaves, in the order they appear; moved marks their
	// steps, which their own sessions hold; spawnedBy maps a call's
	// native ID to the agent its result names.
	sidechains     map[string]*sidechain
	sidechainOrder []*sidechain
	moved          map[int]bool
	spawnedBy      map[string]spawnRecord
}

// call is one tool call of the document.
type call struct {
	id, native string
	step       int
	answered   bool
}

func (c *docConv) addf(kind agentimport.Kind, code string, line int, format string, args ...any) {
	c.report.Addf(kind, code, c.in.name, c.line(line), format, args...)
}

// line is a step's 1-based position in the document it was written in:
// a synthetic document's steps are numbered as in the other.
func (c *docConv) line(line int) int {
	if line > 0 && line <= len(c.in.pos) {
		return c.in.pos[line-1] + 1
	}
	return line
}

// count notes a finding reported once per document, with the number of
// steps it applies to and the first.
func (c *docConv) count(code string, line int) {
	if c.counts[code] == 0 {
		c.firstAt[code] = line
	}
	c.counts[code]++
}

// mark records where a step ended up; the first mark stands.
func (c *docConv) mark(i int, d agentimport.Disposition) {
	if _, ok := c.disp[i]; !ok {
		c.disp[i] = d
	}
}

// header builds the session header from the root and the agent.
func (c *docConv) header() (agentsession.Header, error) {
	t := c.t
	h := agentsession.Header{ID: c.tgt.id, ParentSession: c.tgt.parent, SpawnedBy: c.tgt.spawnedBy}
	for i, s := range t.Steps {
		if s == nil || s.Err != nil || s.Timestamp == "" || c.moved[i] {
			continue
		}
		if ts, _, ok := parseTime(s.Timestamp); ok && (h.CreatedAt.IsZero() || ts.Before(h.CreatedAt)) {
			h.CreatedAt = ts
		}
	}
	if h.CreatedAt.IsZero() {
		switch {
		case !c.tgt.created.IsZero():
			h.CreatedAt = c.tgt.created
			c.addf(agentimport.Lossy, "atif/no-timestamp", 0, "the document has no timestamp; created_at is the time of the step that names it")
		case !c.fam.opts.CreatedAt.IsZero():
			h.CreatedAt = c.fam.opts.CreatedAt
			if c.fam.mtime {
				c.addf(agentimport.Lossy, "atif/no-timestamp", 0, "the document has no timestamp; created_at is the file's modification time")
			} else {
				c.addf(agentimport.Lossy, "atif/no-timestamp", 0, "the document has no timestamp; created_at is the one the conversion was given")
			}
		default:
			h.CreatedAt = time.Unix(0, 0).UTC()
			c.addf(agentimport.Lossy, "atif/no-timestamp", 0, "the document has no timestamp; created_at is the epoch")
		}
	}
	c.created = h.CreatedAt
	if a := t.Agent; a != nil && a.Err == nil && a.Name != "" {
		h.Harness = &agentsession.Harness{Name: a.Name, Version: a.Version}
	}
	src := agentimport.Source{Format: Source}
	if t.SessionID != "" && t.SessionID != h.ID {
		src.NativeID = t.SessionID
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	c.addf(agentimport.Gap, "agentimport/provenance", 0, "the import is recorded in the header member %s", agentimport.SourceMember)
	if c.tgt.derived != "" {
		c.addf(agentimport.Repaired, "atif/session-id", 0, "%s", c.tgt.derived)
	}
	if t.SessionID != "" && t.SessionID != h.ID {
		if err := c.headerMember(&h, "session_id", t.SessionID); err != nil {
			return h, err
		}
	}
	if err := c.headerMember(&h, "version", t.SchemaVersion); err != nil {
		return h, err
	}
	switch {
	case t.SchemaVersion == "":
		c.addf(agentimport.Lossy, "atif/version", 0, "the document declares no schema_version; it is read as %s", atif.Versions[len(atif.Versions)-1])
	case !slices.Contains(atif.Versions, t.SchemaVersion):
		c.addf(agentimport.Lossy, "atif/version", 0, "schema_version %s is not one Harbor defines; it is read as %s, and members it does not know are kept", t.SchemaVersion, atif.Versions[len(atif.Versions)-1])
	}
	if err := c.headerMember(&h, "trajectory_id", t.TrajectoryID); err != nil {
		return h, err
	}
	ext := object(t.Ext)
	if as := object(ext["agentsession"]); strings.HasPrefix(str(as["format"]), "agentsession/") {
		c.exporter = true
		h.CWD = str(as["cwd"])
	}
	if c.in.synthetic {
		return h, nil
	}
	a := t.Agent
	switch {
	case a == nil:
		c.addf(agentimport.Repaired, "atif/no-agent", 0, "the document has no agent; the session names no harness")
	case a.Err != nil:
		c.addf(agentimport.Repaired, "atif/no-agent", 0, "the agent is not an object; the session names no harness")
		if err := c.keepHeader(&h, "agent", a.Raw, "atif/mistyped-member", "the agent, which is not an object"); err != nil {
			return h, err
		}
	default:
		if a.Name == "" {
			c.addf(agentimport.Repaired, "atif/no-agent", 0, "the agent has no name; the session names no harness")
		}
		if native := nativeConverter(a.Name); native != "" {
			c.addf(agentimport.Lossy, "atif/native-available", 0,
				"the document was written by Harbor's %s adapter from %s's own log, which agentimport converts directly (-from %s) with less lost", a.Name, a.Name, native)
		}
		if a.Name == "terminus-2" {
			c.addf(agentimport.Lossy, "atif/synthetic-tool-calls", 0,
				"Terminus 2 parses its tool calls from the model's text and rewrites the message as analysis and plan; the model emitted no function calls")
		}
		if err := c.keepHeader(&h, "agent_extra", a.Ext, "atif/extra", "the agent's extra"); err != nil {
			return h, err
		}
		if err := c.keepMembers(&h, "agent_members", a.Members.Extra, a.Mistyped()); err != nil {
			return h, err
		}
	}
	if err := c.keepHeader(&h, "notes", jsonString(t.Notes), "atif/notes", "the document's notes"); err != nil {
		return h, err
	}
	if t.FinalMetrics != nil {
		if err := c.keepHeader(&h, "final_metrics", t.FinalMetrics.Raw, "atif/final-metrics",
			"final_metrics, whose totals some producers count sub-agents in, and from which no usage is taken"); err != nil {
			return h, err
		}
	}
	if err := c.keepHeader(&h, "extra", t.Ext, "atif/extra", "the document's extra"); err != nil {
		return h, err
	}
	rootExtra, mistyped := t.Members.Extra, t.Mistyped()
	if t.SubagentsMistyped {
		rootExtra = maps.Clone(rootExtra)
		if rootExtra == nil {
			rootExtra = map[string]json.RawMessage{}
		}
		rootExtra["subagent_trajectories"] = t.SubagentsRaw
		mistyped = append(slices.Clone(mistyped), "subagent_trajectories")
	}
	if err := c.keepMembers(&h, "root", rootExtra, mistyped); err != nil {
		return h, err
	}
	if err := c.headerMember(&h, "continued_trajectory_ref", t.ContinuedRef); err != nil {
		return h, err
	}
	return h, nil
}

// nativeConverter names the source agentimport converts a Harbor
// adapter's agent from, or "".
func nativeConverter(agent string) string {
	switch agent {
	case "claude-code":
		return "claudecode"
	case "codex":
		return "codex"
	case "pi":
		return "pi"
	}
	return ""
}

// headerMember sets a header member, cleaned to I-JSON. An empty
// string or a nil value is not set.
func (c *docConv) headerMember(h *agentsession.Header, name string, v any) error {
	var raw json.RawMessage
	switch v := v.(type) {
	case string:
		if v == "" {
			return nil
		}
		raw = jsonString(v)
	case json.RawMessage:
		if atif.IsNull(v) {
			return nil
		}
		raw = v
	default:
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return err
		}
	}
	raw, err := c.clean(0, raw)
	if err != nil {
		return err
	}
	if h.Extra == nil {
		h.Extra = map[string]json.RawMessage{}
	}
	h.Extra[Source+":"+name] = raw
	return nil
}

// keepHeader keeps a value the model never saw in a header member, or
// with Slim drops it, and reports which: what names the value.
func (c *docConv) keepHeader(h *agentsession.Header, name string, raw json.RawMessage, code, what string) error {
	if atif.IsNull(raw) {
		return nil
	}
	if c.fam.opts.Slim {
		c.addf(agentimport.Dropped, code, 0, "%s: left out", what)
		return nil
	}
	c.addf(agentimport.Kept, code, 0, "%s: kept in the header member %s:%s", what, Source, name)
	return c.headerMember(h, name, raw)
}

// keepMembers keeps an object's undeclared members, and the declared
// ones of another JSON type, in a header member.
func (c *docConv) keepMembers(h *agentsession.Header, name string, extra map[string]json.RawMessage, mistyped []string) error {
	if len(extra) == 0 {
		return nil
	}
	c.reportMembers(0, name, extra, mistyped)
	if c.fam.opts.Slim {
		return nil
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	return c.headerMember(h, name, json.RawMessage(raw))
}

// reportMembers reports members kept in member name, or dropped with
// Slim: undeclared ones, which Harbor's models refuse, and declared
// ones of another type.
func (c *docConv) reportMembers(line int, name string, extra map[string]json.RawMessage, mistyped []string) {
	var unknown []string
	for k := range extra {
		if !slices.Contains(mistyped, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	kind, verb := agentimport.Kept, "kept in "+Source+":"+name
	if c.fam.opts.Slim {
		kind, verb = agentimport.Dropped, "left out"
	}
	if len(unknown) > 0 {
		c.addf(kind, "atif/unknown-member", line, "members Harbor's models do not declare, which they refuse, are %s: %s", verb, strings.Join(unknown, ", "))
	}
	if len(mistyped) > 0 {
		c.addf(kind, "atif/mistyped-member", line, "members of a JSON type other than the one ATIF defines are %s: %s", verb, strings.Join(mistyped, ", "))
	}
}

// clean makes native JSON I-JSON, as the session format holds it, and
// reports what it changed.
func (c *docConv) clean(line int, raw json.RawMessage) (json.RawMessage, error) {
	if agentimport.RepeatedMembers(raw) {
		c.addf(agentimport.Repaired, "agentimport/repeated-member", line, "an object in the document named a member twice, which I-JSON forbids; the last is kept")
	}
	if agentimport.InvalidText(raw) {
		c.addf(agentimport.Repaired, "agentimport/invalid-text", line, "text in the document that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// run converts the document's steps, the sub-agents it embeds that no
// step names and its continuation, and accounts for every unit.
func (c *docConv) run() error {
	if !c.in.synthetic {
		c.report.Line(agentimport.LineMapped)
	}
	if f := c.in.file; f != nil && f.Cut != nil {
		lost := ""
		if len(f.CutMembers) > 0 {
			lost = "; cut: " + strings.Join(f.CutMembers, ", ")
		}
		c.addf(agentimport.Repaired, "atif/truncated", 0,
			"the document does not parse past byte %d, as a file rewritten in place when its writer stopped does not; the members and steps before it are converted%s", f.CutAt, lost)
		if f.CutStep {
			c.report.Line(agentimport.LineMalformed)
			c.addf(agentimport.Repaired, "atif/step-malformed", len(c.t.Steps)+1, "the step the document is cut inside is skipped")
		}
	}
	c.indexEmbedded()
	if err := c.firstConfig(); err != nil {
		return err
	}
	if err := c.steps(); err != nil {
		return err
	}
	if err := c.omit(); err != nil {
		return err
	}
	if err := c.unlinked(); err != nil {
		return err
	}
	c.unlinkedSidechains()
	if err := c.continuation(); err != nil {
		return err
	}
	c.tally()
	return nil
}

// tally accounts for every step and reports what is reported once per
// document.
func (c *docConv) tally() {
	for i := range c.t.Steps {
		if c.moved[i] {
			continue
		}
		d, ok := c.disp[i]
		if !ok {
			d = agentimport.LineDropped
			c.addf(agentimport.Dropped, "atif/unreached", i+1, "the converter did not reach this step")
		}
		c.report.Line(d)
	}
	for _, call := range c.calls {
		if !call.answered {
			c.addf(agentimport.Lossy, "atif/call-without-result", call.step+1,
				"tool call %s has no result in the document; the call is left without an output, which a provider asks for before the next request", call.native)
		}
	}
	once := []struct {
		kind   agentimport.Kind
		code   string
		detail string
	}{
		{agentimport.Repaired, "atif/timestamp", "step timestamps with no time zone are read as UTC, and those that do not parse take the time before them (steps: %d)"},
		{agentimport.Lossy, "atif/copied-context", "steps copied from another document's history are converted as this session's own entries, marked %s:copied, their responses' usage in %s:usage so it counts once (steps: %d)"},
		{agentimport.Lossy, "atif/reasoning-text", "reasoning_content is a reasoning item's summary text with no encrypted_content, which a provider that signs its reasoning will not take back (steps: %d)"},
		{agentimport.Lossy, "atif/response-status", "ATIF records no response status, so no response has one (responses: %d)"},
		{agentimport.Repaired, "atif/step-id", "step_id values that are not the step's position are read in document order and kept in %s:step_id (steps: %d)"},
	}
	for _, o := range once {
		n := c.counts[o.code]
		if n == 0 {
			continue
		}
		detail := strings.ReplaceAll(o.detail, "%s", Source)
		detail = strings.Replace(detail, "%d", fmt.Sprint(n), 1)
		c.addf(o.kind, o.code, c.firstAt[o.code], "%s", detail)
	}
}

// indexEmbedded indexes the trajectories this one embeds by
// trajectory_id, the scope a reference resolves in.
func (c *docConv) indexEmbedded() {
	for i, sub := range c.t.Subagents {
		if sub == nil || sub.Err != nil {
			continue
		}
		if sub.TrajectoryID == "" {
			c.addf(agentimport.Repaired, "atif/subagent-id", 0, "embedded trajectory %d has no trajectory_id, which ATIF requires; no reference can name it", i)
			continue
		}
		if _, dup := c.byTID[sub.TrajectoryID]; dup {
			c.addf(agentimport.Repaired, "atif/subagent-id", 0, "embedded trajectories share trajectory_id %s; a reference names the first", sub.TrajectoryID)
			continue
		}
		c.byTID[sub.TrajectoryID] = sub
	}
}

// firstConfig writes the settings the first step ran under: the agent's
// model and tools, and the leading system steps as the instructions.
func (c *docConv) firstConfig() error {
	cfg := &agentsession.ConfigEntry{}
	if a := c.t.Agent; a != nil && a.Err == nil {
		cfg.Model = a.ModelName
		var tools openresponses.Tools
		var bad []json.RawMessage
		for _, raw := range a.ToolDefinitions {
			if tool, ok := functionTool(raw); ok {
				tools = append(tools, tool)
			} else {
				bad = append(bad, raw)
			}
		}
		cfg.ToolsAdded = tools
		if len(bad) > 0 {
			if err := c.b.Member(cfg, "tool_definitions", bad); err != nil {
				return err
			}
			c.addf(agentimport.Lossy, "atif/tool-definition", 0,
				"%d tool definitions are not chat-completions function tools; they are kept in %s:tool_definitions and are not in the settings", len(bad), Source)
		}
	}
	c.leading = map[int]bool{}
	var parts []agentsession.InstructionPart
	var notes []json.RawMessage
	if !c.exporter {
		for i, s := range c.t.Steps {
			if c.moved[i] {
				continue
			}
			text, ok := leadingSystem(s)
			if !ok {
				break
			}
			c.leading[i] = true
			parts = append(parts, agentsession.InstructionPart{ID: fmt.Sprintf("step/%d", i+1), Text: text})
			if rest := c.stepRest(i, s); len(rest) > 0 || !atif.IsNull(s.Ext) {
				note := map[string]any{"step": i + 1}
				if !atif.IsNull(s.Ext) {
					note["extra"] = s.Ext
				}
				if len(rest) > 0 {
					note["members"] = rest
				}
				raw, err := json.Marshal(note)
				if err != nil {
					return err
				}
				notes = append(notes, raw)
			}
			c.mark(i, agentimport.LineMapped)
			c.addf(agentimport.Lossy, "atif/system-prompt", i+1,
				"a system step before the first user or agent step is the instructions; a reader cannot tell it from a message")
		}
	}
	switch len(parts) {
	case 0:
	case 1:
		cfg.Instructions = &parts[0].Text
	default:
		joined := agentsession.JoinInstructions(parts)
		cfg.Instructions = &joined
		cfg.InstructionsParts = parts
	}
	if len(notes) > 0 && !c.fam.opts.Slim {
		if err := c.b.Member(cfg, "system_steps", notes); err != nil {
			return err
		}
	}
	if cfg.Model == "" && cfg.Instructions == nil && len(cfg.ToolsAdded) == 0 && len(cfg.Unknown) == 0 {
		return nil
	}
	if len(parts) > 0 {
		if ts, _, ok := parseTime(c.t.Steps[0].Timestamp); ok {
			cfg.Timestamp = ts
		}
	}
	c.settings = c.settings.Apply(cfg)
	_, err := c.appendEntry(cfg)
	return err
}

// leadingSystem returns a step's text when it is a system step that can
// be part of the instructions: text alone, no observation, no context
// management.
func leadingSystem(s *atif.Step) (string, bool) {
	if s == nil || s.Err != nil || s.Source != atif.SourceSystem || s.Observation != nil {
		return "", false
	}
	if _, ok := object(s.Ext)["context_management"]; ok {
		return "", false
	}
	if s.Message.Raw != nil {
		return "", false
	}
	for _, p := range s.Message.Parts {
		if p == nil || p.Err != nil || p.Type != atif.PartText {
			return "", false
		}
	}
	return s.Message.String(), true
}

// functionTool converts a chat-completions tool definition to a
// Responses function tool.
func functionTool(raw json.RawMessage) (*openresponses.FunctionTool, bool) {
	var def struct {
		Type     string `json:"type"`
		Function *struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      *bool           `json:"strict"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &def) != nil || def.Type != "function" || def.Function == nil || def.Function.Name == "" {
		return nil, false
	}
	f := def.Function
	params := f.Parameters
	if atif.IsNull(params) {
		params = nil
	} else if p, _, err := agentimport.CleanJSON(params); err == nil {
		params = p
	}
	return &openresponses.FunctionTool{Name: f.Name, Description: f.Description, Parameters: params, Strict: f.Strict}, true
}

// appendEntry appends e after the tail with the time of the entry
// before it when it has none, and makes it the tail.
func (c *docConv) appendEntry(e agentsession.Entry) (string, error) {
	base := e.Base()
	if base.Timestamp.IsZero() {
		base.Timestamp = c.last
		if base.Timestamp.IsZero() {
			base.Timestamp = c.created
		}
	}
	id, err := c.b.Append(c.tail, e)
	if err != nil {
		return "", err
	}
	c.tail = id
	c.last = base.Timestamp
	return id, nil
}

// omit writes the omit setting an exporter's document records: the
// items the request at its leaf left out.
func (c *docConv) omit() error {
	if len(c.omitted) == 0 {
		return nil
	}
	cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: c.omitted}}
	if _, err := c.appendEntry(cfg); err != nil {
		return err
	}
	c.addf(agentimport.Lossy, "atif/omitted", 0,
		"the document lists %d items the request at its leaf left out; a config entry at the end omits them, whatever rule left them out", len(c.omitted))
	return nil
}

// object decodes a JSON object's members, or nil.
func object(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// str decodes a JSON string, or "".
func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func jsonString(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	raw, _ := json.Marshal(s)
	return raw
}

// parseTime reads an ISO 8601 time as Python's datetime.fromisoformat
// does, which is how Harbor checks one. naive says it carried no zone
// and was read as UTC.
func parseTime(s string) (t time.Time, naive, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, false
	}
	zoned := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-0700",
	}
	for _, layout := range zoned {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), false, true
		}
	}
	plain := []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02",
	}
	for _, layout := range plain {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true, true
		}
	}
	return time.Time{}, false, false
}
