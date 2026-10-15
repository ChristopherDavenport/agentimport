package convert

import (
	"encoding/json"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// summaryPrefix opens the user message Codex writes for a compaction's
// summary (codex-rs/prompts/templates/compact/summary_prefix.md). Such a
// message is no user turn.
const summaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

// compactUserBudget is the token budget Codex keeps user messages within
// when it rebuilds a window from them (COMPACT_USER_MESSAGE_MAX_TOKENS).
const compactUserBudget = 20_000

// contextualUserMarkers open the user messages Codex writes itself
// (environment context, instructions, notices), which are no user turn.
// Codex tells them apart by a matcher per fragment; this list is the
// common ones, so the classification is an approximation.
var contextualUserMarkers = []string{
	"<environment_context>", "<user_instructions>", "# AGENTS.md instructions",
	"<user_shell_command>", "<turn_aborted>", "<subagent_notification>",
	"<codex_internal_context", "<goal_context>", "<skill>",
}

// texts returns the text of a message's text parts, and whether every
// part was text.
func texts(m *openresponses.Message) ([]string, bool) {
	var out []string
	all := true
	for _, p := range m.Content {
		switch p := p.(type) {
		case *openresponses.InputText:
			out = append(out, p.Text)
		case *openresponses.OutputText:
			out = append(out, p.Text)
		default:
			all = false
		}
	}
	return out, all
}

// contextualUser reports whether a user message is context Codex wrote
// rather than a person's input.
func contextualUser(m *openresponses.Message) bool {
	ts, all := texts(m)
	if !all || len(ts) == 0 {
		return false
	}
	for _, t := range ts {
		t = strings.TrimSpace(t)
		found := false
		for _, mk := range contextualUserMarkers {
			if strings.HasPrefix(t, mk) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// contextualDeveloper reports whether a developer message is context
// Codex writes before a turn: every part a tagged fragment.
func contextualDeveloper(m *openresponses.Message) bool {
	ts, all := texts(m)
	if !all || len(ts) == 0 {
		return false
	}
	for _, t := range ts {
		if !strings.HasPrefix(strings.TrimSpace(t), "<") {
			return false
		}
	}
	return true
}

// isSummary reports whether a message is a compaction's summary.
func isSummary(m *openresponses.Message) bool {
	ts, _ := texts(m)
	return len(ts) > 0 && strings.HasPrefix(ts[0], summaryPrefix+"\n")
}

// userTurn reports whether an item in context starts a user turn, as
// Codex counts turns to roll back (is_user_turn_boundary): a person's
// message, or a message from another agent.
func userTurn(item openresponses.Item) bool {
	switch v := item.(type) {
	case *openresponses.UnknownItem:
		return v.Type == extensionType(codex.ItemAgentMessage)
	case *openresponses.Message:
		return v.Role == openresponses.RoleUser && !contextualUser(v) && !isSummary(v)
	}
	return false
}

// boundaryItem is userTurn for a native item.
func boundaryItem(it *codex.Item) bool {
	switch it.Type {
	case codex.ItemAgentMessage:
		return true
	case codex.ItemMessage:
		if it.Role != "user" {
			return false
		}
		var m openresponses.Message
		if json.Unmarshal(it.Content, &m.Content) != nil {
			return true
		}
		m.Role = openresponses.RoleUser
		return !contextualUser(&m) && !isSummary(&m)
	}
	return false
}

// rollback branches the session where Codex's rollback of the last n
// user turns cut its history (drop_last_n_user_turns): at the n-th user
// turn from the end, moved back over the context Codex wrote just before
// it. The dropped turns stay in the file on the branch left; the
// rollback is recorded as a custom entry, the first of the new branch.
func (c *converter) rollback(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	n := int64(0)
	if l.Event.NumTurns != nil {
		n = *l.Event.NumTurns
	}
	var ctx agentsession.Context
	if c.tail != "" {
		var err error
		if ctx, err = c.b.S.ContextAt(c.tail); err != nil {
			return err
		}
	}
	var turns []int
	for i, it := range ctx.Items {
		if userTurn(it) {
			turns = append(turns, i)
		}
	}
	if n <= 0 || len(turns) == 0 {
		return c.custom(l, "codex/rollback-nothing", "a rollback of %d turns with no user turn to drop changes nothing; it is a custom entry", n)
	}
	cut := turns[0]
	if int(n) < len(turns) {
		cut = turns[len(turns)-int(n)]
	}
	first := turns[0]
	for cut > first {
		m, ok := ctx.Items[cut-1].(*openresponses.Message)
		if !ok || !(m.Role == openresponses.RoleDeveloper && contextualDeveloper(m) || m.Role == openresponses.RoleUser && contextualUser(m)) {
			break
		}
		cut--
	}
	if cut == first {
		c.addf(agentimport.Lossy, "codex/rollback-first-turn", l,
			"the rollback dropped every user turn; Codex also strips model-switch and mode notices from the first turn's developer messages, which the converter does not")
	}
	at := ctx.ItemEntries[cut]
	parent := at.Base().Parent
	if _, ok := at.(*agentsession.CompactionEntry); ok {
		// The cut falls inside a compaction's window, which the format
		// cannot keep part of: the whole window stays.
		parent = at.Base().ID
		c.addf(agentimport.Lossy, "codex/rollback-into-compaction", l,
			"the rollback cut into the window a compaction installed; the session keeps the whole window, where Codex kept the part before the cut")
	}
	c.tail = parent
	c.st = c.states[parent]
	if parent == "" {
		c.st = state{truncation: defaultTruncation}
	}
	if err := c.custom(l, "codex/rollback", "rolling back the last %d user turns is a branch from the entry before the first turn dropped; the dropped turns stay on the branch left, and the rollback is a custom entry", n); err != nil {
		return err
	}
	c.disp[l] = agentimport.LineMapped
	return nil
}

// compacted converts a compaction. Codex replaces the model's window
// with a new history: the replacement history the line carries, or, in
// older rollouts, one rebuilt from the user messages and the summary.
// The session writes a custom entry holding the compaction record, then
// a compaction entry whose first_kept names that record, so nothing
// before is kept, whose summary is the window's first item and whose
// pins are the items after it that copy items already on the path; the
// rest of the window follows as item entries, in order. The context
// after it is the window Codex installed, item for item.
func (c *converter) compacted(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	cp := l.Compacted
	var window []json.RawMessage
	if cp.HasReplacement() {
		window = cp.ReplacementHistory
	} else {
		rebuilt, err := c.rebuild(l, cp.Message)
		if err != nil {
			return err
		}
		window = rebuilt
		c.addf(agentimport.Lossy, "codex/compaction-rebuilt", l,
			"the compaction carries no replacement history (older rollouts); the window is rebuilt as Codex rebuilds it on resume, from the user messages within %d tokens and the summary, with tokens estimated from bytes", compactUserBudget)
	}
	// The compaction record, without the window it carries.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(l.PayloadRaw(), &payload); err != nil {
		return err
	}
	delete(payload, "replacement_history")
	stripped := l.Raw
	if !l.Legacy {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(l.Raw, &env); err != nil {
			return err
		}
		p, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		env["payload"] = p
		if stripped, err = json.Marshal(env); err != nil {
			return err
		}
	}
	marker, err := c.customEntry(l, stripped)
	if err != nil {
		return err
	}
	if err := c.appendNow(marker, l); err != nil {
		return err
	}
	markerID := c.tail
	// The items on the path a pin may copy.
	onPath := map[string]bool{}
	for _, e := range c.b.S.Path(markerID) {
		if ie, ok := e.(*agentsession.ItemEntry); ok {
			if raw, err := json.Marshal(ie.Item); err == nil {
				onPath[string(raw)] = true
			}
		}
	}
	var items []json.RawMessage
	var natives []*codex.Item
	for _, raw := range window {
		it := &codex.Item{}
		if err := json.Unmarshal(raw, it); err != nil || !inWindow(it) {
			c.addf(agentimport.Lossy, "codex/window-item-skipped", l,
				"an item of the replacement history that Codex does not send (type %q) is left out of the window", it.Type)
			continue
		}
		items = append(items, raw)
		natives = append(natives, it)
	}
	var summary openresponses.Item
	var pins openresponses.Items
	rest := 0
	if len(items) == 0 {
		summary = openresponses.AssistantText(cp.Message)
		c.addf(agentimport.Lossy, "codex/compaction-empty", l,
			"the compaction installed an empty window; the format's compaction needs a summary, so it is the summary text as an assistant message, which Codex did not send")
	} else {
		if summary, err = c.copyItem(l, items[0], natives[0]); err != nil {
			return err
		}
		rest = 1
		for rest < len(items) {
			item, err := c.copyItem(l, items[rest], natives[rest])
			if err != nil {
				return err
			}
			raw, err := json.Marshal(item)
			if err != nil || !onPath[string(raw)] {
				break
			}
			pins = append(pins, item)
			rest++
		}
		for _, item := range append(openresponses.Items{summary}, pins...) {
			if err := c.itemGaps(nil, l, item); err != nil {
				return err
			}
		}
	}
	ctx, err := c.b.S.ContextAt(markerID)
	if err != nil {
		return err
	}
	e := &agentsession.CompactionEntry{FirstKept: markerID, Summary: summary, Pinned: pins, Config: ctx.Settings}
	if err := c.appendNow(e, l); err != nil {
		return err
	}
	for i := rest; i < len(items); i++ {
		item, extra, err := c.convertItem(l, items[i], natives[i])
		if err != nil {
			c.addf(agentimport.Lossy, "codex/window-item-skipped", l,
				"an item of the replacement history that does not decode (%v) is left out of the window", err)
			continue
		}
		ie := agentsession.NewItemEntry(item)
		hidden := false
		ie.Visible = &hidden
		if err := c.b.Member(ie, "window", true); err != nil {
			return err
		}
		if err := c.customMembers(ie, l, natives[i]); err != nil {
			return err
		}
		if err := c.itemGaps(ie, l, item); err != nil {
			return err
		}
		if err := c.extraMembers(ie, l, extra); err != nil {
			return err
		}
		if err := c.appendNow(ie, l); err != nil {
			return err
		}
	}
	c.addf(agentimport.Kept, "codex/compaction", l,
		"the compaction is a compaction entry after a custom entry holding its record: its window's first item is the summary, %d items copying the path are pinned, and the %d after them, new or not pinnable, follow it as hidden entries marked %s:window", len(pins), len(items)-rest, Source)
	c.mark(l, agentimport.LineMapped)
	return nil
}

// inWindow reports whether Codex keeps a replaced window's item in the
// history it sends.
func inWindow(it *codex.Item) bool {
	switch {
	case !codex.ItemTypes[it.Type], it.Type == codex.ItemGhostSnapshot, it.Type == codex.ItemCompactionTrigger,
		it.Type == codex.ItemMessage && it.Role == "system":
		return false
	}
	return true
}

// copyItem converts a window item that may copy one already recorded:
// a call or output takes the call id recorded for its native one, and
// nothing new is recorded.
func (c *converter) copyItem(l *codex.Line, raw json.RawMessage, it *codex.Item) (openresponses.Item, error) {
	raw, err := c.clean(l, raw)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if it.Type == codex.ItemCompactionSummary {
		obj["type"] = json.RawMessage(`"compaction"`)
	}
	if (callTypes[it.Type] || answerTypes[it.Type]) && it.CallID != "" {
		if id, ok := c.st.calls[it.CallID]; ok && id != it.CallID {
			obj["call_id"], _ = json.Marshal(id)
		}
	}
	if obj, _, err = asFunction(obj, it); err != nil {
		return nil, err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	item, err := openresponses.UnmarshalItem(data)
	if err != nil {
		return nil, err
	}
	if u, ok := item.(*openresponses.UnknownItem); ok {
		if u.Raw, err = extension(obj); err != nil {
			return nil, err
		}
		u.Type = extensionType(it.Type)
	}
	return item, nil
}

// rebuild builds the window an older compaction installed, as Codex
// rebuilds it on resume (build_compacted_history with no initial
// context): the user messages in context, newest first while they fit
// the budget and then the one that does not, cut to what is left, in
// order, then the summary as a user message.
func (c *converter) rebuild(l *codex.Line, summaryText string) ([]json.RawMessage, error) {
	var msgs []*openresponses.Message
	if c.tail != "" {
		ctx, err := c.b.S.ContextAt(c.tail)
		if err != nil {
			return nil, err
		}
		for _, it := range ctx.Items {
			if m, ok := it.(*openresponses.Message); ok && m.Role == openresponses.RoleUser && !contextualUser(m) && !isSummary(m) {
				msgs = append(msgs, m)
			}
		}
	}
	remaining := compactUserBudget
	var picked []openresponses.Item
	for i := len(msgs) - 1; i >= 0 && remaining > 0; i-- {
		m := msgs[i]
		ts, all := texts(m)
		text := strings.Join(ts, "\n")
		tokens := (len(text) + 3) / 4
		if tokens <= remaining && all {
			picked = append(picked, m)
			remaining -= tokens
			continue
		}
		cut := text
		if tokens > remaining {
			if n := remaining * 4; n < len(cut) {
				cut = cut[:n]
			}
			c.addf(agentimport.Lossy, "codex/compaction-rebuilt", l,
				"a user message over the rebuilt window's budget is cut to the budget by bytes; Codex cuts it by its own token estimate")
		}
		picked = append(picked, openresponses.UserText(cut))
		if tokens > remaining {
			break
		}
		remaining -= tokens
	}
	if summaryText == "" {
		summaryText = "(no summary available)"
	}
	var out []json.RawMessage
	for i := len(picked) - 1; i >= 0; i-- {
		raw, err := json.Marshal(picked[i])
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	raw, err := json.Marshal(openresponses.UserText(summaryText))
	if err != nil {
		return nil, err
	}
	return append(out, raw), nil
}
