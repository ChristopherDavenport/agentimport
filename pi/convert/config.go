// Portions of this file are ported from earendil-works/pi
// (https://github.com/earendil-works/pi) at v1.0.1, Copyright (c) 2025
// Mario Zechner, licensed under the MIT License; they have been
// modified. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// sysState is pi's prompt and tool state, as replaying system messages
// in order builds it (pi-ai's getCurrentSystemMessage): every non-empty
// content appended, sections patched by name, tools removed and set by
// name. Its slices are replaced, never changed in place.
type sysState struct {
	content  []string
	sections []pi.Section
	tools    []*pi.Tool
}

// apply returns the state after system message m, as pi-ai's
// getCurrentSystemMessage replays it. That function ignores replace,
// which message-types.md says starts a new baseline; the conversion
// follows what pi sent.
func (s sysState) apply(m *pi.Message) sysState {
	out := sysState{content: s.content, sections: s.sections, tools: s.tools}
	if text := m.Content.TextOf("\n"); text != "" {
		out.content = append(slices.Clip(s.content), text)
	}
	if len(m.Sections) > 0 {
		secs := slices.Clone(s.sections)
		for _, x := range jsOrder(m.Sections) {
			i := slices.IndexFunc(secs, func(y pi.Section) bool { return y.Name == x.Name })
			switch {
			case x.Remove && i >= 0:
				secs = slices.Delete(secs, i, i+1)
			case x.Remove:
			case i >= 0:
				secs[i].Text = x.Text
			default:
				secs = append(secs, pi.Section{Name: x.Name, Text: x.Text})
			}
		}
		out.sections = secs
	}
	if len(m.ToolsRemoved) > 0 || len(m.ToolsAdded) > 0 {
		tools := slices.Clone(s.tools)
		for _, r := range m.ToolsRemoved {
			if r != nil {
				tools = slices.DeleteFunc(tools, func(t *pi.Tool) bool { return t.Name == r.Name })
			}
		}
		for _, t := range m.ToolsAdded {
			if t == nil {
				continue
			}
			if i := slices.IndexFunc(tools, func(x *pi.Tool) bool { return x.Name == t.Name }); i >= 0 {
				tools[i] = t
			} else {
				tools = append(tools, t)
			}
		}
		out.tools = tools
	}
	return out
}

// jsOrder returns sections in the order a JavaScript object iterates
// its keys, which is how pi reads a message's sections and renders the
// prompt: keys that are array indices first, in numeric order, then the
// rest in the order written.
func jsOrder(secs []pi.Section) []pi.Section {
	var idx, rest []pi.Section
	for _, x := range secs {
		if _, ok := arrayIndex(x.Name); ok {
			idx = append(idx, x)
		} else {
			rest = append(rest, x)
		}
	}
	if len(idx) == 0 {
		return secs
	}
	slices.SortStableFunc(idx, func(a, b pi.Section) int {
		x, _ := arrayIndex(a.Name)
		y, _ := arrayIndex(b.Name)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
	return append(idx, rest...)
}

// arrayIndex reports whether a key is an array index in JavaScript's
// sense: a canonical decimal below 2^32-1.
func arrayIndex(s string) (uint64, bool) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n >= 1<<32-1 {
		return 0, false
	}
	return n, true
}

// parts are the instruction parts the state renders, in pi's order: the
// contents, then the sections as a JavaScript object orders them, each
// non-empty one a part.
func (s sysState) parts() []agentsession.InstructionPart {
	var out []agentsession.InstructionPart
	for i, t := range s.content {
		out = append(out, agentsession.InstructionPart{ID: fmt.Sprintf("content/%d", i+1), Text: t})
	}
	for _, x := range jsOrder(s.sections) {
		if x.Text != "" {
			out = append(out, agentsession.InstructionPart{ID: "section/" + x.Name, Text: x.Text})
		}
	}
	return out
}

// functionTool converts a pi tool declaration.
func functionTool(t *pi.Tool) *openresponses.FunctionTool {
	params := t.Parameters
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage(`{"type":"object"}`)
	}
	return &openresponses.FunctionTool{Name: t.Name, Description: t.Description, Parameters: params}
}

// system converts a system message into a config delta: the
// instructions as named parts, the tools added and removed against
// those in force.
func (c *converter) system(l *pi.Line, m *pi.Message, tail string, st *pathState) (string, error) {
	next := st.sys.apply(m)
	settings := agentsession.Settings{}
	if tail != "" {
		ctx, err := c.b.S.ContextAt(tail)
		if err != nil {
			return "", err
		}
		settings = ctx.Settings
	}
	e := settings.InstructionsDelta(next.parts())
	if e == nil {
		e = &agentsession.ConfigEntry{}
	}
	added, removed, sameOrder := toolsDelta(settings.Tools, next.tools)
	e.ToolsAdded, e.ToolsRemoved = added, removed
	if !sameOrder && len(added)+len(removed) > 0 {
		c.addf(agentimport.Lossy, "pi/tool-order", l,
			"a tool redeclared in place keeps its position in pi and moves to the end in the session format")
	}
	if err := c.systemExtras(e, l, m); err != nil {
		return "", err
	}
	st.sys = next
	if e.Instructions == nil && len(e.InstructionsParts) == 0 && len(e.ToolsAdded) == 0 && len(e.ToolsRemoved) == 0 && len(e.Unknown) == 0 {
		return c.drop(l, tail, "pi/system-unchanged", "a system message that changes neither the prompt nor the tools in force")
	}
	if st.conversed {
		c.addf(agentimport.Lossy, "pi/system-update", l,
			"a system message during the conversation is a config delta; a provider that takes system messages mid-conversation was sent it there, as an update, and kept the earlier prompt")
	}
	if err := c.messageMembers(e, l, nil); err != nil {
		return "", err
	}
	return c.mapped(l, tail, e)
}

// systemExtras reports what a system message holds that the config has
// no place for: tool declaration members, kept on e, and content blocks
// other than text, which pi does not render.
func (c *converter) systemExtras(e agentsession.Entry, l *pi.Line, m *pi.Message) error {
	if extra := toolMembers(m.ToolsAdded); len(extra) > 0 {
		if err := c.b.Member(e, "tool_members", extra); err != nil {
			return err
		}
		c.addf(agentimport.Kept, "pi/tool-members", l, "tool declaration members a function tool has no place for are %s:tool_members", Source)
	}
	for _, b := range contentBlocks(m.Content) {
		if b.Invalid() || b.Type != pi.BlockText {
			c.addf(agentimport.Dropped, "pi/system-content", l, "a block of type %q in a system message is not part of the prompt pi renders", b.Type)
		}
	}
	return nil
}

func contentBlocks(c *pi.Content) []*pi.Block {
	if c == nil || c.IsText {
		return nil
	}
	return c.Blocks
}

// toolMembers returns, by tool name, the members of a declaration a
// function tool does not carry.
func toolMembers(tools []*pi.Tool) map[string]map[string]json.RawMessage {
	out := map[string]map[string]json.RawMessage{}
	for _, t := range tools {
		if t == nil {
			continue
		}
		if extra := nonNull(t.Extra); len(extra) > 0 {
			out[t.Name] = extra
		}
	}
	return out
}

// toolsDelta computes the tools to remove and add to turn the tools in
// force into next: a tool absent from next, or declared differently, is
// removed; a tool new or changed is added. sameOrder says the tools the
// delta leaves in force are in next's order.
func toolsDelta(force openresponses.Tools, next []*pi.Tool) (added openresponses.Tools, removed []string, sameOrder bool) {
	want := map[string]string{}
	var order []string
	for _, t := range next {
		raw, _ := json.Marshal(functionTool(t))
		want[t.Name] = string(raw)
		order = append(order, t.Name)
	}
	have := map[string]bool{}
	var kept []string
	for _, t := range force {
		name := agentsession.ToolName(t)
		raw, _ := json.Marshal(t)
		if w, ok := want[name]; !ok || w != string(raw) {
			removed = append(removed, name)
			continue
		}
		have[name] = true
		kept = append(kept, name)
	}
	for _, t := range next {
		if !have[t.Name] {
			added = append(added, functionTool(t))
			kept = append(kept, t.Name)
		}
	}
	return added, removed, slices.Equal(kept, order)
}

// compaction converts a compaction into a compaction entry: the framed
// summary pi sends, the entry the kept range starts at, and a full
// checkpoint of the settings, with pi's prompt and tool checkpoint when
// the entry has one.
func (c *converter) compaction(l *pi.Line, tail string, st *pathState) (string, error) {
	firstKept, err := c.firstKept(l, tail)
	if err != nil {
		return "", err
	}
	if firstKept == "" {
		if firstKept, tail, err = c.keepNone(l, tail); err != nil {
			return "", err
		}
	}
	ctx, err := c.b.S.ContextAt(tail)
	if err != nil {
		return "", err
	}
	cfg := ctx.Settings
	if l.SystemMessage != nil {
		st.sys = sysState{}.apply(l.SystemMessage)
		cfg.InstructionsParts = st.sys.parts()
		cfg.Instructions = agentsession.JoinInstructions(cfg.InstructionsParts)
		cfg.Tools = nil
		for _, t := range st.sys.tools {
			cfg.Tools = append(cfg.Tools, functionTool(t))
		}
	}
	cfg.Omit = c.keptReplacements(cfg.Omit, firstKept, tail)
	e := &agentsession.CompactionEntry{
		FirstKept:    firstKept,
		Summary:      openresponses.UserText(compactionPrefix + l.Summary + compactionSuffix),
		Config:       cfg,
		TokensBefore: l.TokensBefore,
		Usage:        usage(l.Usage),
	}
	if l.SystemMessage != nil {
		if err := c.systemExtras(e, l, l.SystemMessage); err != nil {
			return "", err
		}
	}
	if err := c.summaryMembers(e, l); err != nil {
		return "", err
	}
	return c.mapped(l, tail, e)
}

// firstKept resolves a compaction's firstKeptEntryId to the session
// entry the kept range starts at, or "" when the compaction keeps
// nothing. pi keeps the entries on the path from the one it names; when
// that one wrote nothing to the session (an edit that changed nothing, a
// system message that changed nothing), the range starts at the first
// later entry on the path that did.
func (c *converter) firstKept(l *pi.Line, tail string) (string, error) {
	if l.IsRetainNone() || tail == "" {
		return "", nil
	}
	if id, ok := c.firstOf(l.FirstKeptEntryID); ok && c.onPath(id, tail) {
		return id, nil
	}
	target := c.tree.Node(l.FirstKeptEntryID)
	self := c.nodes[l]
	var chain []*pi.Node
	found := false
	if self != nil && target != nil {
		for n := self.Parent; n != nil; n = n.Parent {
			if n == target {
				found = true
				break
			}
			chain = append(chain, n)
		}
	}
	if !found {
		// pi keeps nothing from before a compaction whose kept entry
		// is not on the path.
		c.addf(agentimport.Repaired, "pi/first-kept-missing", l,
			"the first kept entry %s is not on the compaction's path; nothing before it is kept, as pi reads it", l.FirstKeptEntryID)
		return "", nil
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if id, ok := c.first[nodeKey(chain[i])]; ok && c.onPath(id, tail) {
			return id, nil
		}
	}
	return "", nil
}

// keepNone finds or makes an entry for a compaction that keeps nothing
// to name as its first kept: the format's first_kept keeps the entry it
// names, so it must name one that puts nothing in context. The entry
// the compaction follows serves when it is such an entry; otherwise a
// custom marker is written after it, or as a root for a compaction with
// nothing before it.
func (c *converter) keepNone(l *pi.Line, tail string) (firstKept, newTail string, err error) {
	if e, ok := c.b.S.Entry(tail); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return tail, tail, nil
		}
	}
	data, err := json.Marshal(map[string]string{"retain_none": l.ID})
	if err != nil {
		return "", "", err
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: data}
	marker.Timestamp = c.lineTime(l)
	id, err := c.b.Append(tail, marker)
	if err != nil {
		return "", "", err
	}
	c.addf(agentimport.Kept, "pi/retain-none", l,
		"the compaction keeps nothing from before it and follows an item or nothing; its first_kept is a custom marker entry written before it, which puts nothing in context")
	return id, id, nil
}

// keptReplacements adds to omit the replacements of context edits that
// a compaction keeps while it drops their targets: pi applies an edit
// only to an entry still in context, so such a replacement reaches no
// request after the compaction.
func (c *converter) keptReplacements(omit agentsession.Omit, firstKept, tail string) agentsession.Omit {
	if len(c.replacements) == 0 {
		return omit
	}
	kept := map[string]bool{}
	in := false
	for _, e := range c.b.S.Path(tail) {
		id := e.Base().ID
		if id == firstKept {
			in = true
		}
		if in {
			kept[id] = true
		}
	}
	var add []string
	for _, e := range c.b.S.Path(tail) {
		repl := e.Base().ID
		targets, ok := c.replacements[repl]
		if !ok || !kept[repl] || slices.Contains(omit.Items, repl) {
			continue
		}
		if !slices.ContainsFunc(targets, func(t string) bool { return kept[t] }) {
			add = append(add, repl)
		}
	}
	if len(add) == 0 {
		return omit
	}
	return agentsession.Omit{Reasoning: omit.Reasoning, Items: append(slices.Clone(omit.Items), add...)}
}

// summaryMembers keeps a summary entry's native usage, details and
// origin.
func (c *converter) summaryMembers(e agentsession.Entry, l *pi.Line) error {
	if l.Usage != nil {
		if err := c.b.Member(e, "usage", l.Usage); err != nil {
			return err
		}
		c.addf(agentimport.Gap, "agentimport/usage-detail", l,
			"cache writes are folded into input_tokens; the native usage and its cost are %s:usage", Source)
	}
	if len(l.Details) > 0 && string(l.Details) != "null" {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "pi/details", l, "the %s's details are left out", l.Type)
		} else {
			if err := c.b.Member(e, "details", l.Details); err != nil {
				return err
			}
			c.addf(agentimport.Kept, "pi/details", l, "the %s's details, which pi does not send the model, are %s:details", l.Type, Source)
		}
	}
	if l.FromHook != nil {
		if err := c.b.Member(e, "from_hook", *l.FromHook); err != nil {
			return err
		}
	}
	return nil
}

// branchSummary converts a branch summary: the framed summary pi sends,
// naming the leaf the branch left.
func (c *converter) branchSummary(l *pi.Line, tail string) (string, error) {
	if l.Summary == "" {
		return c.custom(l, tail, "pi/branch-summary-empty", "a branch summary with no text, which pi leaves out of context, is a custom entry")
	}
	from, ok := c.lastOf(l.FromID)
	if !ok {
		return c.custom(l, tail, "pi/branch-summary-unlinked", "a branch summary whose left leaf %s is not in the file before it is a custom entry", l.FromID)
	}
	e := &agentsession.BranchSummaryEntry{
		From:    from,
		Summary: openresponses.UserText(branchPrefix + l.Summary + branchSuffix),
		Usage:   usage(l.Usage),
	}
	if err := c.summaryMembers(e, l); err != nil {
		return "", err
	}
	id, err := c.mapped(l, tail, e)
	if err != nil {
		return "", err
	}
	c.items[nodeKeyLine(l)] = append(c.items[nodeKeyLine(l)], id)
	return id, nil
}

// editable are the roles whose content a context edit replaces
// (pi's projectContextEntry); an edit leaves any other as it was.
var editable = map[string]bool{pi.RoleUser: true, pi.RoleAssistant: true, pi.RoleToolResult: true, pi.RoleCustom: true}

// contextEdit converts an edit of an earlier entry's contribution to
// context. Leaving the entry out is the format's omit. Replacing its
// content in place has no equivalent: a user or extension message is
// left out and its replacement written where the edit was; an assistant
// message or tool result stays as it was, since leaving out a call or
// its output breaks the pair a provider needs, and the replacement is
// kept aside.
func (c *converter) contextEdit(l *pi.Line, tail string) (string, error) {
	n := c.tree.Node(l.TargetID)
	var ids []string
	if n != nil && tail != "" {
		ctx, err := c.b.S.ContextAt(tail)
		if err != nil {
			return "", err
		}
		inRange := map[string]bool{}
		for _, e := range ctx.Entries {
			inRange[e.Base().ID] = true
		}
		for _, id := range c.items[nodeKey(n)] {
			if inRange[id] {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return c.drop(l, tail, "pi/context-edit-target", "an edit of an entry that is not in context on this path changes nothing, as pi applies it")
	}
	repl, err := l.EditReplacement()
	if err != nil {
		return c.custom(l, tail, "pi/context-edit-unreadable", "a context edit whose replacement does not decode (%s) is a custom entry", agentimport.ErrorText(err))
	}
	var items, summaries []string
	for _, id := range ids {
		if e, ok := c.b.S.Entry(id); ok {
			if _, isItem := e.(*agentsession.ItemEntry); isItem {
				items = append(items, id)
				continue
			}
		}
		summaries = append(summaries, id)
	}
	role := ""
	switch {
	case n.Line.Message != nil:
		role = n.Line.Message.Role
	case n.Line.Type == pi.TypeCustomMessage:
		role = pi.RoleCustom
	case n.Line.Type == pi.TypeBranchSummary:
		role = pi.RoleBranchSummary
	}
	if repl == nil {
		if len(summaries) > 0 {
			c.addf(agentimport.Gap, "agentimport/omit-summary", l,
				"pi leaves the %s out of context; the format's omit names item entries alone, so it stays", n.Line.Type)
		}
		if len(items) == 0 {
			c.mark(l, agentimport.LineDropped)
			return tail, nil
		}
		return c.mapped(l, tail, &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: items}})
	}
	if !editable[role] || len(items) == 0 {
		return c.drop(l, tail, "pi/context-edit-ignored", "an edit that replaces the content of a %s, which pi leaves as it was", nameOf(role, n.Line.Type))
	}
	if role == pi.RoleAssistant || role == pi.RoleToolResult {
		return c.customAs(agentimport.Gap, l, tail, "agentimport/context-replace",
			"the %s pi sent with new content stays in context as it was, so its call and output stay paired; the edit is a custom entry", role)
	}
	if tail, err = c.mapped(l, tail, &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: items}}); err != nil {
		return "", err
	}
	item := itemEntry(openresponses.UserMessage(c.contents(l, repl)...))
	if t, ok := c.b.S.Entry(items[0]); ok {
		if ie, ok := t.(*agentsession.ItemEntry); ok {
			item.Visible = ie.Visible
		}
	}
	if tail, err = c.append(tail, item, l); err != nil {
		return "", err
	}
	c.items[nodeKey(n)] = append(c.items[nodeKey(n)], tail)
	c.replacements[tail] = items
	c.addf(agentimport.Gap, "agentimport/context-replace", l,
		"the %s message is left out of context and its replacement written where the edit was, not in its place", role)
	return tail, nil
}

func nameOf(role, typ string) string {
	if role != "" {
		return role + " message"
	}
	return typ
}
