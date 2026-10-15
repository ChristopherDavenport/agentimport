package convert

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// line converts a unit of one line: a user turn, an attachment, a
// system line, an assistant line with no message id, or a line that
// did not decode.
func (c *converter) line(l *claudecode.Line, tail string, st *pathState) (string, error) {
	if l.Err != nil {
		return c.custom(l, tail, "claudecode/undecodable-line", "a line that does not decode as its type (%v) is a custom entry", l.Err)
	}
	switch l.Type {
	case claudecode.TypeUser:
		if l.IsToolResult() {
			return c.custom(l, tail, "claudecode/orphan-result", "a tool result for a call not in the transcript is a custom entry")
		}
		if l.Message == nil {
			return c.custom(l, tail, "claudecode/no-message", "a user line with no message is a custom entry")
		}
		if l.IsVisibleInTranscriptOnly {
			return c.custom(l, tail, "claudecode/transcript-only", "a line shown in the transcript and not sent to the model is a custom entry")
		}
		return c.userLine(l, tail)
	case claudecode.TypeAssistant:
		if l.Message == nil {
			return c.custom(l, tail, "claudecode/no-message", "an assistant line with no message is a custom entry")
		}
		r := &claudecode.Response{Lines: []*claudecode.Line{l}}
		for _, b := range l.ToolUses() {
			r.Calls = append(r.Calls, &claudecode.Call{Use: b, Line: l})
		}
		return c.response(r, tail, st)
	case claudecode.TypeAttachment:
		return c.attachment(l, tail, st)
	case claudecode.TypeSystem:
		return c.custom(l, tail, "claudecode/system-line", "a system line (%s) is a custom entry", l.Subtype)
	}
	return c.custom(l, tail, "claudecode/unknown-line-type", "a tree line of unknown type %q is a custom entry", l.Type)
}

// userLine converts a prompt: a user message, hidden when the CLI
// injected it, its origin as the item's source.
func (c *converter) userLine(l *claudecode.Line, tail string) (string, error) {
	e := itemEntry(openresponses.UserMessage(c.userContent(l, l.Message.Content)...))
	if l.IsMeta {
		hidden := false
		e.Visible = &hidden
	}
	var skip []string
	if src := origin(l); src != nil {
		e.Source = src
		skip = append(skip, "origin")
	}
	if !l.Message.Content.IsText {
		if err := c.blockMembers(e, l, l.Message.Content.Blocks...); err != nil {
			return "", err
		}
	}
	if err := c.messageMembers(e, l, l.Message); err != nil {
		return "", err
	}
	if err := c.lineMembers(e, l, "line", skip...); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineMapped)
	c.bind(l, id)
	return id, nil
}

// origin maps how a prompt arrived to the item's source trigger: kind
// the origin's kind (human, peer, task-notification, ...), ref the
// agent or task that sent it, source "claudecode", and the origin's
// other members as the trigger's own.
func origin(l *claudecode.Line) *agentsession.Trigger {
	raw, ok := l.Extra["origin"]
	if !ok {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	var kind string
	if json.Unmarshal(m["kind"], &kind) != nil || kind == "" {
		return nil
	}
	t := &agentsession.Trigger{Kind: kind, Source: Source}
	for _, k := range []string{"senderTaskId", "from", "runId"} {
		var s string
		if json.Unmarshal(m[k], &s) == nil && s != "" {
			t.Ref = s
			break
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "kind" || k == "ref" || k == "source" {
			continue
		}
		if err := t.SetMember(k, m[k]); err != nil {
			return nil
		}
	}
	return t
}

func (c *converter) userContent(l *claudecode.Line, content claudecode.Content) openresponses.Contents {
	if content.IsText {
		return openresponses.Contents{&openresponses.InputText{Text: content.Text}}
	}
	return c.contents(l, content.Blocks, true)
}

// contents maps user or tool-result blocks to input parts. A block
// with no input part is kept as an anthropic.<type> part carrying the
// block, which the provider adapter cannot send: a gap upstream.
func (c *converter) contents(l *claudecode.Line, blocks []*claudecode.ContentBlock, user bool) openresponses.Contents {
	out := openresponses.Contents{}
	for _, b := range blocks {
		if b.IsRaw() && b.Type == "" {
			c.addf(agentimport.Dropped, "claudecode/null-block", l, "a content block that is not an object is left out")
			continue
		}
		if !b.IsRaw() {
			switch {
			case b.Type == claudecode.BlockText:
				out = append(out, &openresponses.InputText{Text: b.Text})
				continue
			case b.Type == claudecode.BlockImage && b.Source != nil && b.Source.Type == "base64":
				out = append(out, &openresponses.InputImage{ImageURL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
				continue
			case b.Type == claudecode.BlockImage && b.Source != nil && b.Source.Type == "url":
				out = append(out, &openresponses.InputImage{ImageURL: b.Source.URL})
				continue
			case b.Type == claudecode.BlockDocument && b.Source != nil && b.Source.Type == "base64":
				out = append(out, &openresponses.InputFile{FileData: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
				continue
			}
		}
		raw, err := json.Marshal(b)
		if err != nil {
			raw = []byte("{}")
		}
		raw = c.clean(raw, l)
		typ := "anthropic." + b.Type
		part, _ := json.Marshal(struct {
			Type  string          `json:"type"`
			Block json.RawMessage `json:"block"`
		}{typ, raw})
		out = append(out, &openresponses.UnknownContent{Type: typ, Raw: part})
		where := "a tool result"
		if user {
			where = "a user message"
		}
		c.addf(agentimport.Kept, "claudecode/extension-part", l,
			"a %s block in %s is the extension part %s", b.Type, where, typ)
	}
	return out
}

// attachment converts context the CLI injected: what it rendered into
// the request becomes hidden messages in the rendered role; a prompt
// snapshot becomes a config delta; one with nothing rendered is kept
// aside. A queued prompt carries two renderings, for mid-turn and for
// a new turn; the requests Claude Code logs hold the first, so it is
// the one written.
func (c *converter) attachment(l *claudecode.Line, tail string, st *pathState) (string, error) {
	if snap, ok := l.PromptSnapshot(); ok {
		echo := snap.EchoWireToolInputs
		st.echo = &echo
		instructions := snap.SystemPrompt
		if snap.CLIPrefix != "" {
			instructions = append([]string{snap.CLIPrefix}, instructions...)
		}
		var tools []tool
		if snap.HasTools {
			for _, t := range snap.Tools {
				d := tool{name: t.Name, description: t.Description}
				if t.Schema != nil {
					d.def = t.Schema
				}
				tools = append(tools, d)
			}
		}
		return c.config(l, tail, st, instructions, tools, snap.HasTools, "prompt_snapshot attachment")
	}
	if l.Attachment == nil || len(l.Rendered)+len(l.RenderedInHumanTurn) == 0 {
		typ := ""
		if l.Attachment != nil {
			typ = l.Attachment.Type
		}
		return c.custom(l, tail, "claudecode/attachment-unrendered", "an attachment (%s) that rendered nothing into the request is a custom entry", typ)
	}
	role := openresponses.RoleUser
	if l.RenderedRole == "system" {
		role = openresponses.RoleSystem
	}
	rendered := l.Rendered
	if len(rendered) == 0 {
		rendered, role = l.RenderedInHumanTurn, openresponses.RoleUser
	} else if len(l.RenderedInHumanTurn) > 0 {
		c.addf(agentimport.Lossy, "claudecode/alternate-rendering", l,
			"the %s attachment has a second rendering, for a new turn, which is kept in %s:line, not sent", l.Attachment.Type, Source)
	}
	hidden := false
	for i, r := range rendered {
		e := itemEntry(&openresponses.Message{Role: role, Content: openresponses.Contents{&openresponses.InputText{Text: r.Content}}})
		e.Visible = &hidden
		if i == 0 {
			if c.opts.Slim {
				c.addf(agentimport.Dropped, "claudecode/attachment", l, "the %s attachment's payload is left out; its rendering is the message", l.Attachment.Type)
			} else {
				if err := c.b.Member(e, "attachment", l.Attachment); err != nil {
					return "", err
				}
				c.addf(agentimport.Kept, "claudecode/attachment", l, "the %s attachment's payload is %s:attachment", l.Attachment.Type, Source)
			}
			if err := c.lineMembers(e, l, "line"); err != nil {
				return "", err
			}
		}
		if m := nonNull(r.Extra); len(m) > 0 {
			if err := c.b.Member(e, "rendered", m); err != nil {
				return "", err
			}
			c.addf(agentimport.Kept, "claudecode/block-members", l, "rendering members kept in %s:rendered", Source)
		}
		id, err := c.append(tail, e, l)
		if err != nil {
			return "", err
		}
		c.bind(l, id)
		tail = id
	}
	c.mark(l, agentimport.LineMapped)
	return tail, nil
}

// tool is a tool definition from a prompt snapshot or a request shape.
type tool struct {
	name, description string
	def               *claudecode.ToolDef
}

// encode maps a tool definition to the payload's tool: a function tool
// for a client tool, and for a server tool (one whose definition has a
// type) an anthropic.<type> tool carrying the definition, the shape
// providers/anthropic sends as written.
func (t tool) encode() (openresponses.Tool, error) {
	if t.def != nil {
		var typ string
		if json.Unmarshal(t.def.Extra["type"], &typ) == nil && typ != "" && typ != "custom" {
			raw, err := json.Marshal(t.def)
			if err != nil {
				return nil, err
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			m["type"], _ = json.Marshal("anthropic." + typ)
			if raw, err = json.Marshal(m); err != nil {
				return nil, err
			}
			raw, _, err = agentimport.CleanJSON(raw)
			return &openresponses.UnknownTool{Type: "anthropic." + typ, Raw: raw}, err
		}
	}
	params := json.RawMessage(`{"type":"object"}`)
	desc := t.description
	if t.def != nil {
		if len(t.def.InputSchema) > 0 && json.Valid(t.def.InputSchema) && string(t.def.InputSchema) != "null" {
			params = t.def.InputSchema
		}
		if desc == "" {
			desc = t.def.Description
		}
	}
	return &openresponses.FunctionTool{Name: t.name, Description: desc, Parameters: params}, nil
}

// config writes the system prompt and tools a snapshot of the request
// recorded, as a config delta: the instructions whole, the tools as
// what was added and removed against those in force on the path.
func (c *converter) config(l *claudecode.Line, tail string, st *pathState, system []string, tools []tool, hasTools bool, what string) (string, error) {
	text := strings.Join(system, agentsession.PartSeparator)
	e := &agentsession.ConfigEntry{Instructions: &text}
	if hasTools {
		next := map[string]string{}
		for _, t := range tools {
			if t.name == "" && t.def != nil {
				t.name = t.def.Name
			}
			ot, err := t.encode()
			if err != nil {
				return "", err
			}
			raw, err := json.Marshal(ot)
			if err != nil {
				return "", err
			}
			if _, dup := next[t.name]; dup {
				continue
			}
			next[t.name] = string(raw)
			if st.tools[t.name] != string(raw) {
				e.ToolsAdded = append(e.ToolsAdded, ot)
				if _, ok := ot.(*openresponses.UnknownTool); ok {
					c.addf(agentimport.Kept, "claudecode/server-tool", l, "server tool %s is an %s tool carrying its definition", t.name, ot.ToolType())
				}
			}
		}
		// A delta removes, then adds, an added tool replacing one of
		// its name: a changed tool is added again, a gone one removed.
		for name := range st.tools {
			if _, ok := next[name]; !ok {
				e.ToolsRemoved = append(e.ToolsRemoved, name)
			}
		}
		sort.Strings(e.ToolsRemoved)
		st.tools = next
	}
	if !c.opts.Slim {
		var keep any
		switch {
		case l.Attachment != nil && len(l.Attachment.Extra) > 0:
			if err := c.b.Member(e, "attachment", l.Attachment); err != nil {
				return "", err
			}
		case l.Shape != nil:
			keep = l.Shape
			if err := c.b.Member(e, "shape", keep); err != nil {
				return "", err
			}
		}
	}
	if err := c.lineMembers(e, l, "line"); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineMapped)
	c.bind(l, id)
	c.addf(agentimport.Lossy, "claudecode/request-snapshot", l,
		"the %s is a config delta: its %d system blocks are joined into one instructions string, and requests before it record none", what, len(system))
	return id, nil
}

// compaction converts a compact_boundary line and the summary line
// below it into a compaction entry under the line the conversation was
// at, keeping the lines the compaction preserved. It returns the
// summary's unit, which the entry folded in.
func (c *converter) compaction(u *claudecode.Unit, tail string, st *pathState) (string, *claudecode.Unit, error) {
	l := u.Line
	var summary *claudecode.Unit
	for _, ch := range u.Children {
		if x := ch.Line; x != nil && x.Err == nil && x.Type == claudecode.TypeUser && x.IsCompactSummary && x.Message != nil {
			summary = ch
			break
		}
	}
	if !u.Logical || summary == nil || l.CompactMetadata == nil || tail == "" {
		id, err := c.custom(l, tail, "claudecode/compaction-unmapped", "a compact boundary without its summary or its logical parent is a custom entry")
		return id, nil, err
	}
	path := c.b.S.Path(tail)
	firstKept := ""
	if uuid := l.CompactMetadata.FirstPreserved(); uuid != "" {
		if id, ok := c.b.ID(uuid); ok {
			for _, e := range path {
				if e.Base().ID == id {
					firstKept = id
					break
				}
			}
		}
	}
	if firstKept == "" {
		// The format keeps at least the entry first_kept names; the
		// entry the conversation was at is the least that can be.
		firstKept = tail
		c.addf(agentimport.Lossy, "claudecode/compaction-kept", l,
			"the compaction preserved no line on the path; first_kept is the entry before it, which stays in context")
	}
	ctx, err := c.b.S.ContextAt(tail)
	if err != nil {
		return "", nil, err
	}
	sl := summary.Line
	e := &agentsession.CompactionEntry{
		FirstKept:    firstKept,
		Summary:      openresponses.UserMessage(c.userContent(sl, sl.Message.Content)...),
		Config:       ctx.Settings,
		TokensBefore: int(l.CompactMetadata.PreTokens),
	}
	if err := c.b.Member(e, "summary_uuid", sl.UUID); err != nil {
		return "", nil, err
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "claudecode/compact-metadata", l, "the compaction's metadata is left out")
	} else {
		if err := c.b.Member(e, "compact_metadata", l.CompactMetadata); err != nil {
			return "", nil, err
		}
		c.addf(agentimport.Kept, "claudecode/compact-metadata", l, "the compaction's metadata is %s:compact_metadata", Source)
	}
	if err := c.lineMembers(e, l, "line"); err != nil {
		return "", nil, err
	}
	if err := c.lineMembers(e, sl, "summary_line"); err != nil {
		return "", nil, err
	}
	if err := c.messageMembers(e, sl, sl.Message); err != nil {
		return "", nil, err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", nil, err
	}
	c.mark(l, agentimport.LineMapped)
	c.mark(sl, agentimport.LineMapped)
	c.bind(l, id)
	c.bind(sl, id)
	return id, summary, nil
}

// side converts the side records written after a unit, in file order,
// each hanging from the one before.
func (c *converter) side(lines []*claudecode.Line, tail string, st *pathState) (string, error) {
	var err error
	for _, l := range lines {
		if tail, err = c.sideRecord(l, tail, st); err != nil {
			return "", err
		}
	}
	return tail, nil
}

// stateRecords are the side records that restate a value, the last one
// in force; a repeat of the value in force on the path is not written
// again.
var stateRecords = map[string]bool{
	claudecode.TypeMode: true, claudecode.TypePermissionMode: true, claudecode.TypeAtisLatch: true,
	claudecode.TypeCostState: true, claudecode.TypePRLink: true, claudecode.TypeTag: true,
	claudecode.TypeBridgeSession: true,
}

func (c *converter) sideRecord(l *claudecode.Line, tail string, st *pathState) (string, error) {
	if l.Err != nil {
		return c.custom(l, tail, "claudecode/undecodable-line", "a line that does not decode as its type (%v) is a custom entry", l.Err)
	}
	switch l.Type {
	case claudecode.TypeAITitle, claudecode.TypeCustomTitle, claudecode.TypeSummary:
		name := l.AITitle
		if l.Type == claudecode.TypeCustomTitle {
			name = l.CustomTitle
		} else if l.Type == claudecode.TypeSummary {
			name = l.Summary
		}
		if repeat(st, "title", name) {
			return c.drop(l, tail, "claudecode/side-record-repeat", "a %s repeating the title in force", l.Type)
		}
		e := &agentsession.InfoEntry{Name: name}
		if err := c.b.Member(e, "title_kind", l.Type); err != nil {
			return "", err
		}
		if err := c.lineMembers(e, l, "line"); err != nil {
			return "", err
		}
		id, err := c.append(tail, e, l)
		if err != nil {
			return "", err
		}
		c.mark(l, agentimport.LineMapped)
		return id, nil
	case claudecode.TypeLastPrompt:
		return c.drop(l, tail, "claudecode/last-prompt", "the last prompt repeats a user message; only the last record's leaf is used")
	case claudecode.TypeAPIRequestShape:
		if l.Shape == nil {
			break
		}
		var system []string
		if l.Shape.System.IsText {
			system = append(system, l.Shape.System.Text)
		}
		for _, b := range l.Shape.System.Blocks {
			if b.Type == claudecode.BlockText && !b.IsRaw() {
				system = append(system, b.Text)
			}
		}
		tools := make([]tool, 0, len(l.Shape.Tools))
		for _, t := range l.Shape.Tools {
			if t != nil {
				tools = append(tools, tool{name: t.Name, description: t.Description, def: t})
			}
		}
		return c.config(l, tail, st, system, tools, true, "api-request-shape record")
	case claudecode.TypeAPIRequest, claudecode.TypeAPIRequestBlob:
		if c.opts.Slim {
			return c.drop(l, tail, "claudecode/request-log", "the %s record is left out", l.Type)
		}
		return c.custom(l, tail, "claudecode/request-log", "the %s record is a custom entry", l.Type)
	case claudecode.TypeFileHistorySnapshot, claudecode.TypeFileHistoryDelta:
		if c.opts.Slim {
			return c.drop(l, tail, "claudecode/file-history", "the %s record is left out", l.Type)
		}
		return c.custom(l, tail, "claudecode/file-history", "the %s record is a custom entry", l.Type)
	case claudecode.TypeForkContextRef:
		return c.customAs(agentimport.Lossy, l, tail, "claudecode/fork-context", "the fork's inherited context, the parent session up to %s, is not in the session; the record is a custom entry", l.ParentLastUUID)
	}
	if stateRecords[l.Type] {
		if repeat(st, l.Type, stateValue(l)) {
			return c.drop(l, tail, "claudecode/side-record-repeat", "a %s record repeating the value in force", l.Type)
		}
		return c.custom(l, tail, "claudecode/side-record", "a %s record is a custom entry", l.Type)
	}
	if l.Type == claudecode.TypeQueueOperation {
		return c.custom(l, tail, "claudecode/side-record", "a %s record is a custom entry", l.Type)
	}
	return c.custom(l, tail, "claudecode/unknown-line-type", "a side record of unknown type %q is a custom entry", l.Type)
}

// repeat reports whether value is the one in force on the path for key,
// and puts it in force there.
func repeat(st *pathState, key, value string) bool {
	old, ok := st.side[key]
	if ok && old == value {
		return true
	}
	next := make(map[string]string, len(st.side)+1)
	for k, v := range st.side {
		next[k] = v
	}
	next[key] = value
	st.side = next
	return false
}

// stateValue is a state record without its timestamp, which a repeat
// changes and the value does not.
func stateValue(l *claudecode.Line) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(l.Raw, &m) != nil {
		return string(l.Raw)
	}
	delete(m, "timestamp")
	raw, _ := json.Marshal(m)
	return string(raw)
}

// drop accounts for a line that is not written.
func (c *converter) drop(l *claudecode.Line, tail, code, format string, args ...any) (string, error) {
	c.mark(l, agentimport.LineDropped)
	c.addf(agentimport.Dropped, code, l, format, args...)
	return tail, nil
}

// linkAgents finds the call that spawned each agent this transcript can
// have spawned: the one its meta file names, or the Agent call whose
// result names the agent, by its ID or, for a team member, by
// name@team.
func (c *converter) linkAgents() {
	parent := ""
	if c.t.agent != nil {
		parent = c.t.agent.ID
	}
	candidates := c.cv.children[parent]
	if len(candidates) == 0 {
		return
	}
	uses := map[string]bool{}
	for _, l := range c.tr.Lines {
		if l.Err == nil {
			for _, b := range l.ToolUses() {
				uses[b.ID] = true
			}
		}
	}
	byName := map[string]*claudecode.Agent{}
	for _, a := range candidates {
		byName[a.ID] = a
		if a.Meta != nil && a.Meta.Name != "" && a.Meta.TeamName != "" {
			byName[a.Meta.Name+"@"+a.Meta.TeamName] = a
		}
		if a.Meta != nil && a.Meta.ToolUseID != "" && uses[a.Meta.ToolUseID] {
			if _, taken := c.agents[a.Meta.ToolUseID]; !taken {
				c.agents[a.Meta.ToolUseID] = a
			}
		}
	}
	for _, l := range c.tr.Lines {
		if l.Err != nil || len(l.ToolUseResult) == 0 || l.ToolUseResult[0] != '{' {
			continue
		}
		var r struct {
			AgentID  string `json:"agentId"`
			AgentID2 string `json:"agent_id"`
		}
		if json.Unmarshal(l.ToolUseResult, &r) != nil {
			continue
		}
		a := byName[r.AgentID]
		if a == nil {
			a = byName[r.AgentID2]
		}
		if a == nil || a.Meta != nil && a.Meta.ToolUseID != "" {
			continue
		}
		for _, b := range l.ToolResults() {
			if _, taken := c.agents[b.ToolUseID]; !taken && uses[b.ToolUseID] {
				c.agents[b.ToolUseID] = a
				break
			}
		}
	}
}
