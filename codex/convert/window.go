package convert

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// compactUserBudget is the token budget Codex keeps user messages within
// when it rebuilds a window from them (COMPACT_USER_MESSAGE_MAX_TOKENS).
const compactUserBudget = 20_000

// histItem is an item of the history Codex rolls back over, with the
// entry that carries it.
type histItem struct {
	entry agentsession.Entry
	item  openresponses.Item
}

// rollbackHistory is the history Codex counts a rollback's turns over
// (rollout_reconstruction.rs:287-558): a compaction that carries its
// replacement history is the history's base only when its turn survives
// the rollback, and its turn does when the cut falls after the turn it
// was written in; so the turns are counted over the items as recorded,
// a replacement compaction's window left out. A compaction rebuilt from
// the user messages acts wherever it falls, as Codex rebuilds it in the
// forward replay, so its window stands for what came before it.
func (c *converter) rollbackHistory(path []agentsession.Entry) []histItem {
	var h []histItem
	for _, e := range path {
		switch e := e.(type) {
		case *agentsession.ItemEntry:
			if c.windows[e.ID] == windowReplacement {
				continue
			}
			h = append(h, histItem{e, e.Item})
		case *agentsession.CompactionEntry:
			if c.compactions[e.ID] != windowRebuilt {
				continue
			}
			h = h[:0]
			h = append(h, histItem{e, e.Summary})
			for _, p := range e.Pinned {
				h = append(h, histItem{e, p})
			}
		}
	}
	return h
}

// rollback branches the session where Codex's rollback of the last n
// user turns cut its history (drop_last_n_user_turns, history.rs:777):
// at the n-th user turn from the end, moved back over the context Codex
// wrote just before it, never past the first turn. A compaction written
// in a turn the rollback drops is undone, as Codex's reconstruction
// skips the segments of dropped turns with their compactions, so the
// branch starts before it. The dropped turns stay in the file on the
// branch left; the rollback is a custom entry, the first of the new
// branch.
func (c *converter) rollback(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	n := int64(0)
	if l.Event.NumTurns != nil {
		n = *l.Event.NumTurns
	}
	var path []agentsession.Entry
	if c.tail != "" {
		path = c.b.S.Path(c.tail)
	}
	h := c.rollbackHistory(path)
	var turns []int
	for i, x := range h {
		if userTurn(x.item) {
			turns = append(turns, i)
		}
	}
	if n <= 0 || len(turns) == 0 {
		return c.custom(l, "codex/rollback-nothing", "a rollback of %d turns with no user turn to drop changes nothing; it is a custom entry", n)
	}
	cut := turns[0]
	if n < int64(len(turns)) {
		cut = turns[len(turns)-int(n)]
	}
	first := turns[0]
	// trim_pre_turn_context_updates (history.rs:820-858).
	for cut > first {
		m, ok := h[cut-1].item.(*openresponses.Message)
		if !ok || !(m.Role == openresponses.RoleDeveloper && contextualDeveloper(m) || m.Role == openresponses.RoleUser && contextualUser(m)) {
			break
		}
		cut--
	}
	if cut == first {
		c.addf(agentimport.Lossy, "codex/rollback-first-turn", l,
			"the rollback dropped every user turn; Codex also strips model-switch and mode notices from the first turn's developer messages, which the converter does not")
	}
	at := h[cut].entry
	parent := at.Base().Parent
	if _, ok := at.(*agentsession.CompactionEntry); ok {
		// The cut falls inside a window rebuilt from the user messages,
		// which the format cannot keep part of: the whole window stays.
		parent = at.Base().ID
		c.addf(agentimport.Lossy, "codex/rollback-into-compaction", l,
			"the rollback cut into a window rebuilt from the user messages by a compaction with no replacement history; the session keeps the whole window, where Codex keeps the part before the cut")
	}
	// A replacement compaction written in the turn of the cut, before
	// its first item, is in a segment the rollback drops: start before
	// it.
	at0 := -1
	for i, e := range path {
		if e.Base().ID == at.Base().ID {
			at0 = i
			break
		}
	}
	seg := 0
	for i := at0; i >= 0; i-- {
		if c.turnStarts[path[i].Base().ID] {
			seg = i
			break
		}
	}
	for i := seg; i >= 0 && i < at0; i++ {
		if !c.markers[path[i].Base().ID] {
			continue
		}
		lost := 0
		for _, e := range path[i+1 : at0] {
			if ie, ok := e.(*agentsession.ItemEntry); ok && c.windows[ie.ID] == "" {
				lost++
			}
		}
		if lost > 0 {
			c.addf(agentimport.Lossy, "codex/rollback-pre-turn", l,
				"the rollback undoes a compaction written before the dropped turn's first item; the %d items written between the compaction and the turn, which Codex keeps, are left on the branch left", lost)
		}
		parent = path[i].Base().Parent
		break
	}
	c.tail = parent
	c.st = c.states[parent]
	if parent == "" {
		c.st = state{truncation: defaultTruncation}
	}
	if err := c.custom(l, "codex/rollback", "rolling back the last %d user turns is a branch from the entry before the first turn dropped; the dropped turns, and a compaction written in them, stay on the branch left, and the rollback is a custom entry", n); err != nil {
		return err
	}
	c.disp[l] = agentimport.LineMapped
	return nil
}

// Window kinds: a compaction's window from its replacement history, or
// rebuilt from the user messages.
const (
	windowReplacement = "replacement"
	windowRebuilt     = "rebuilt"
)

// compacted converts a compaction. Codex replaces the model's window
// with a new history: the replacement history the line carries, or, in
// older rollouts, one rebuilt from the user messages and the summary.
// The session writes a custom entry holding the compaction record, then
// a compaction entry whose first_kept names that record, so nothing
// before is kept, whose summary is the window's first item and whose
// pins are the items after it that copy items already on the path; the
// rest of the window follows as item entries, in order. The context
// after it is the window Codex installed, item for item. An item of the
// window that does not decode is left out and reported; nothing in a
// window stops the conversion.
func (c *converter) compacted(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	cp := l.Compacted
	kind := windowReplacement
	var window []json.RawMessage
	if cp.HasReplacement() {
		window = cp.ReplacementHistory
	} else {
		rebuilt, err := c.rebuild(l, cp.Message)
		if err != nil {
			return err
		}
		window, kind = rebuilt, windowRebuilt
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
	if kind == windowReplacement {
		c.markers[markerID] = true
	}
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
	var copies []openresponses.Item
	for _, raw := range window {
		it := &codex.Item{}
		if err := json.Unmarshal(raw, it); err != nil || !inWindow(it) {
			c.addf(agentimport.Lossy, "codex/window-item-skipped", l,
				"an item of the replacement history that Codex does not send (type %q) is left out of the window", it.Type)
			continue
		}
		cp, err := c.copyItem(l, raw, it)
		if err != nil {
			c.addf(agentimport.Lossy, "codex/window-item-skipped", l,
				"a %s item of the replacement history that does not decode as one (%s) is left out of the window", it.Type, agentimport.ErrorText(err))
			continue
		}
		items, natives, copies = append(items, raw), append(natives, it), append(copies, cp)
	}
	var summary openresponses.Item
	var pins openresponses.Items
	rest := 0
	if len(items) == 0 {
		summary = openresponses.AssistantText(cp.Message)
		c.addf(agentimport.Lossy, "codex/compaction-empty", l,
			"the compaction installed an empty window; the format's compaction needs a summary, so it is the summary text as an assistant message, which Codex did not send")
	} else {
		summary, rest = copies[0], 1
		for rest < len(items) {
			raw, err := json.Marshal(copies[rest])
			if err != nil || !onPath[string(raw)] {
				break
			}
			pins = append(pins, copies[rest])
			rest++
		}
	}
	ctx, err := c.b.S.ContextAt(markerID)
	if err != nil {
		return err
	}
	e := &agentsession.CompactionEntry{FirstKept: markerID, Summary: summary, Pinned: pins, Config: ctx.Settings}
	for _, item := range append(openresponses.Items{summary}, pins...) {
		if err := c.itemGaps(e, l, item); err != nil {
			return err
		}
	}
	// The calls in context after the compaction are the window's: an
	// output of a call the window left out is one Codex drops.
	calls, answered := map[string]string{}, map[string]bool{}
	keep := func(it *codex.Item) {
		if it.CallID == "" {
			return
		}
		rec, ok := c.st.calls[it.CallID]
		if !ok {
			return
		}
		if callTypes[it.Type] {
			calls[it.CallID] = rec
		}
		if answerTypes[it.Type] {
			answered[rec] = true
		}
	}
	for i := 0; i < rest; i++ {
		keep(natives[i])
	}
	if err := c.appendNow(e, l); err != nil {
		return err
	}
	c.compactions[c.tail] = kind
	for i := rest; i < len(items); i++ {
		item, extra, err := c.convertItem(l, items[i], natives[i])
		if err != nil {
			c.addf(agentimport.Lossy, "codex/window-item-skipped", l,
				"an item of the replacement history that does not decode (%s) is left out of the window", agentimport.ErrorText(err))
			continue
		}
		keep(natives[i])
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
		c.windows[c.tail] = kind
	}
	c.st.calls, c.st.answered = calls, answered
	c.states[c.tail] = c.st
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
// rebuilds it on resume (compact.rs, build_compacted_history with no
// initial context): the user messages in context, newest first while
// they fit the budget, a message with parts other than text kept as its
// text parts, and the one that does not fit cut to what is left; in
// order; then the summary as a user message.
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
		ts := inputTexts(m)
		tokens := (len(strings.Join(ts, "")) + 3) / 4
		textOnly := len(ts) == len(m.Content)
		if tokens <= remaining && textOnly {
			picked = append(picked, m)
			remaining -= tokens
			continue
		}
		if !textOnly {
			c.addf(agentimport.Lossy, "codex/compaction-rebuilt", l,
				"a user message with parts other than text keeps its text parts alone in the rebuilt window, as Codex keeps them")
		}
		parts := ts
		if tokens > remaining {
			parts = cutTexts(ts, remaining*4)
			c.addf(agentimport.Lossy, "codex/compaction-rebuilt", l,
				"a user message over the rebuilt window's budget is cut to the budget by bytes; Codex cuts it by its own token estimate")
		}
		var content openresponses.Contents
		for _, t := range parts {
			content = append(content, &openresponses.InputText{Text: t})
		}
		if len(content) == 0 {
			content = openresponses.Contents{&openresponses.InputText{Text: ""}}
		}
		picked = append(picked, openresponses.UserMessage(content...))
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

// cutTexts keeps the first n bytes of the texts, in order, cutting the
// last kept at a character boundary.
func cutTexts(ts []string, n int) []string {
	var out []string
	for _, t := range ts {
		if n <= 0 {
			break
		}
		if len(t) > n {
			t = t[:n]
			for len(t) > 0 && !utf8.ValidString(t) {
				t = t[:len(t)-1]
			}
		}
		out = append(out, t)
		n -= len(t)
	}
	return out
}
