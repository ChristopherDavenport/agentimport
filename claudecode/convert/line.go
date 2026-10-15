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
// system line, or an assistant line with no message id.
func (c *converter) line(l *claudecode.Line, tail string, st *pathState) (string, error) {
	switch l.Type {
	case claudecode.TypeUser:
		if l.IsToolResult() {
			return c.custom(l, tail, "claudecode/orphan-result", "a tool result for a call not in the transcript is a custom entry")
		}
		if l.Message == nil {
			return c.custom(l, tail, "claudecode/no-message", "a user line with no message is a custom entry")
		}
		return c.userLine(l, tail)
	case claudecode.TypeAssistant:
		if l.Message == nil {
			return c.custom(l, tail, "claudecode/no-message", "an assistant line with no message is a custom entry")
		}
		r := &claudecode.Response{Lines: []*claudecode.Line{l}}
		for _, b := range l.Message.Content.Blocks {
			if b.Type == claudecode.BlockToolUse {
				r.Calls = append(r.Calls, &claudecode.Call{Use: b, Line: l})
			}
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
// injected it.
func (c *converter) userLine(l *claudecode.Line, tail string) (string, error) {
	e := itemEntry(openresponses.UserMessage(c.userContent(l, l.Message.Content)...))
	if l.IsMeta {
		hidden := false
		e.Visible = &hidden
	}
	if err := c.lineMembers(e, l); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineMapped)
	c.b.Bind(l.UUID, id)
	return id, nil
}

func (c *converter) userContent(l *claudecode.Line, content claudecode.Content) openresponses.Contents {
	if content.IsText {
		return openresponses.Contents{&openresponses.InputText{Text: content.Text}}
	}
	return c.contents(l, content.Blocks, true)
}

// contents maps user or tool-result blocks to input parts. A block
// with no input part is kept as an anthropic.<type> part carrying the
// block, the shape the provider gives such items.
func (c *converter) contents(l *claudecode.Line, blocks []*claudecode.ContentBlock, user bool) openresponses.Contents {
	out := openresponses.Contents{}
	for _, b := range blocks {
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
		raw, err := json.Marshal(b)
		if err != nil {
			raw = []byte("{}")
		}
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
		c.addf(agentimport.Kept, "claudecode/anthropic-content", l, "a %s block in %s is an %s part", b.Type, where, typ)
	}
	return out
}

// attachment converts context the CLI injected: what it rendered into
// the request becomes hidden messages in the rendered role; a prompt
// snapshot becomes a config delta; one with nothing rendered is kept
// aside.
func (c *converter) attachment(l *claudecode.Line, tail string, st *pathState) (string, error) {
	if snap, ok := l.PromptSnapshot(); ok {
		instructions := snap.SystemPrompt
		if snap.CLIPrefix != "" {
			instructions = append([]string{snap.CLIPrefix}, instructions...)
		}
		var tools []tool
		if snap.HasTools {
			for _, t := range snap.Tools {
				d := tool{name: t.Name, description: t.Description}
				if t.Schema != nil {
					d.schema = t.Schema.InputSchema
				}
				tools = append(tools, d)
			}
		}
		return c.config(l, tail, st, instructions, tools, snap.HasTools, "prompt_snapshot attachment")
	}
	rendered := len(l.Rendered) + len(l.RenderedInHumanTurn)
	if rendered == 0 || l.Attachment == nil {
		typ := ""
		if l.Attachment != nil {
			typ = l.Attachment.Type
		}
		return c.custom(l, tail, "claudecode/attachment-unrendered", "an attachment (%s) that rendered nothing into the request is a custom entry", typ)
	}
	hidden := false
	var err error
	first := true
	add := func(role openresponses.Role, text string) error {
		e := itemEntry(&openresponses.Message{Role: role, Content: openresponses.Contents{&openresponses.InputText{Text: text}}})
		e.Visible = &hidden
		if first {
			if c.opts.Slim {
				c.addf(agentimport.Dropped, "claudecode/attachment", l, "the %s attachment's payload is left out; its rendering is the message", l.Attachment.Type)
			} else {
				if err := c.b.Member(e, "attachment", l.Attachment); err != nil {
					return err
				}
				c.addf(agentimport.Kept, "claudecode/attachment", l, "the %s attachment's payload is %s:attachment", l.Attachment.Type, Source)
			}
			if err := c.lineMembers(e, l); err != nil {
				return err
			}
			first = false
		}
		tail, err = c.append(tail, e, l)
		return err
	}
	role := openresponses.RoleUser
	if l.RenderedRole == "system" {
		role = openresponses.RoleSystem
	}
	for _, r := range l.Rendered {
		if err := add(role, r.Content); err != nil {
			return "", err
		}
	}
	for _, r := range l.RenderedInHumanTurn {
		if err := add(openresponses.RoleUser, r.Content); err != nil {
			return "", err
		}
	}
	c.mark(l, agentimport.LineMapped)
	c.b.Bind(l.UUID, tail)
	return tail, nil
}

// tool is a tool definition from a prompt snapshot or a request shape.
type tool struct {
	name, description string
	schema            json.RawMessage
}

// config writes the system prompt and tools a snapshot of the request
// recorded, as a config delta: the instructions whole, the tools as
// what was added and removed against those in force.
func (c *converter) config(l *claudecode.Line, tail string, st *pathState, system []string, tools []tool, hasTools bool, what string) (string, error) {
	text := strings.Join(system, agentsession.PartSeparator)
	e := &agentsession.ConfigEntry{Instructions: &text}
	if hasTools {
		next := map[string]string{}
		for _, t := range tools {
			params := t.schema
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object"}`)
			}
			ft := &openresponses.FunctionTool{Name: t.name, Description: t.description, Parameters: params}
			raw, err := json.Marshal(ft)
			if err != nil {
				return "", err
			}
			next[t.name] = string(raw)
			if st.tools[t.name] != string(raw) {
				e.ToolsAdded = append(e.ToolsAdded, ft)
			}
		}
		for name, raw := range st.tools {
			if next[name] != raw {
				e.ToolsRemoved = append(e.ToolsRemoved, name)
			}
		}
		sort.Strings(e.ToolsRemoved)
		st.tools = next
	}
	if l.Attachment != nil && len(l.Attachment.Extra) > 0 && !c.opts.Slim {
		if err := c.b.Member(e, "attachment", l.Attachment); err != nil {
			return "", err
		}
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineMapped)
	if l.UUID != "" {
		c.b.Bind(l.UUID, id)
	}
	c.addf(agentimport.Lossy, "claudecode/request-snapshot", l,
		"a %s is a config delta: its %d system blocks are joined into one instructions string, and requests before it record none", what, len(system))
	return id, nil
}

// compaction converts a compact_boundary line and the summary line
// below it into a compaction entry under the line the conversation was
// at, keeping the lines the compaction preserved. It returns the
// summary's unit, which the entry folded in.
func (c *converter) compaction(u *claudecode.Unit, tail string) (string, *claudecode.Unit, error) {
	l := u.Line
	var summary *claudecode.Unit
	for _, ch := range u.Children {
		if ch.Line != nil && ch.Line.Type == claudecode.TypeUser && ch.Line.IsCompactSummary && ch.Line.Message != nil {
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
	if err := c.lineMembers(e, l); err != nil {
		return "", nil, err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", nil, err
	}
	c.mark(l, agentimport.LineMapped)
	c.mark(sl, agentimport.LineMapped)
	c.b.Bind(l.UUID, id)
	c.b.Bind(sl.UUID, id)
	return id, summary, nil
}

// side converts the side records written after a unit, in file order,
// each hanging from the one before.
func (c *converter) side(lines []*claudecode.Line, tail string) (string, error) {
	var err error
	for _, l := range lines {
		if tail, err = c.sideRecord(l, tail); err != nil {
			return "", err
		}
	}
	return tail, nil
}

// stateRecords are the side records that restate a value, the last one
// in force; a repeat of the value in force is not written again.
var stateRecords = map[string]bool{
	claudecode.TypeMode: true, claudecode.TypePermissionMode: true, claudecode.TypeAtisLatch: true,
	claudecode.TypeCostState: true, claudecode.TypePRLink: true, claudecode.TypeTag: true,
	claudecode.TypeBridgeSession: true,
}

func (c *converter) sideRecord(l *claudecode.Line, tail string) (string, error) {
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
		if c.repeat("title", name) {
			return c.drop(l, tail, "claudecode/side-record-repeat", "a %s repeating the title in force", l.Type)
		}
		e := &agentsession.InfoEntry{Name: name}
		if err := c.b.Member(e, "title_kind", l.Type); err != nil {
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
		for _, b := range l.Shape.System {
			if b.Type == claudecode.BlockText {
				system = append(system, b.Text)
			}
		}
		tools := make([]tool, 0, len(l.Shape.Tools))
		for _, t := range l.Shape.Tools {
			tools = append(tools, tool{name: t.Name, description: t.Description, schema: t.InputSchema})
		}
		// A side record has no path state of its own; the shape is
		// written whole.
		st := pathState{}
		return c.config(l, tail, &st, system, tools, true, "api-request-shape record")
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
		if c.repeat(l.Type, stateValue(l)) {
			return c.drop(l, tail, "claudecode/side-record-repeat", "a %s record repeating the value in force", l.Type)
		}
		return c.custom(l, tail, "claudecode/side-record", "a %s record is a custom entry", l.Type)
	}
	if l.Type == claudecode.TypeQueueOperation {
		return c.custom(l, tail, "claudecode/side-record", "a %s record is a custom entry", l.Type)
	}
	return c.custom(l, tail, "claudecode/unknown-line-type", "a side record of unknown type %q is a custom entry", l.Type)
}

// repeat reports whether value is the one in force for key, and puts
// it in force.
func (c *converter) repeat(key, value string) bool {
	old, ok := c.state[key]
	c.state[key] = value
	return ok && old == value
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

func sortStrings(s []string) { sort.Strings(s) }

// linkAgents finds the call that spawned each agent this transcript can
// have spawned: the one its meta file names, or the Agent call whose
// result names the agent.
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
		if l.Err != nil || l.Type != claudecode.TypeAssistant || l.Message == nil {
			continue
		}
		for _, b := range l.Message.Content.Blocks {
			if b.Type == claudecode.BlockToolUse {
				uses[b.ID] = true
			}
		}
	}
	byID := map[string]*claudecode.Agent{}
	for _, a := range candidates {
		byID[a.ID] = a
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
		id := r.AgentID
		if id == "" {
			id = r.AgentID2
		}
		a := byID[id]
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
