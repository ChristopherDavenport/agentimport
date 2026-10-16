// Portions of this file are ported from RooCodeInc/Roo-Code
// (https://github.com/RooCodeInc/Roo-Code) at b867ec91 and v3.40.0,
// Copyright 2025 Roo Code, Inc., licensed under the Apache License 2.0;
// they have been modified. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Event kinds: a change of classic's deleted range, a Roo summary and
// a Roo sliding-window marker.
const (
	evRange      = "range"
	evCondense   = "condense"
	evTruncation = "truncation"
)

// Roo's condense eras, guessed from a summary's shape.
const (
	eraFresh       = "fresh-start"
	eraTagged      = "non-destructive"
	eraDestructive = "destructive"
)

// keptAfterSummary is how many messages Roo's condense kept after an
// assistant-role summary, N_MESSAGES_TO_KEEP.
const keptAfterSummary = 3

// event is a change of what requests send, at the time it happened:
// before API element p, the first written after it.
type event struct {
	p    int
	kind string
	// order breaks ties between events at one p: the file order of
	// what records them.
	order int
	// rng is a classic range, none for one that was reset.
	rng     [2]int
	noRange bool
	// special is the Roo summary or marker the event wrote; era the
	// summary's guessed era.
	special int
	era     string
	// ui is the UI record that tells of it, -1 for none; ts its time.
	ui int
	ts time.Time
	// placed says how p was chosen when nothing recorded it.
	placed string
}

// prepareEvents finds every change of what requests send: the deleted
// ranges classic's UI messages carry, and Roo's summaries and markers,
// each placed where it happened.
func (c *conv) prepareEvents() {
	if c.roo {
		c.rooEvents()
	} else {
		c.rangeEvents()
	}
	sort.SliceStable(c.events, func(a, b int) bool {
		if c.events[a].p != c.events[b].p {
			return c.events[a].p < c.events[b].p
		}
		return c.events[a].order < c.events[b].order
	})
}

// rangeEvents reads classic's deleted ranges. Each UI message carries
// the range in force when it was written and the index of the last API
// message then; a request applies a new range to the user message just
// written, so the first UI message carrying it, written while the
// response streamed, names that user message. The index entry holds the
// final range, which a task whose UI messages do not show it gets at the
// end.
func (c *conv) rangeEvents() {
	n := len(c.t.API.Elems)
	var cur *[2]int
	lastP := 0
	for i, m := range c.t.UI.Elems {
		if m.Err != nil {
			continue
		}
		has := !cline.IsNull(m.ConversationHistoryDeletedRange)
		r, ok := m.DeletedRange()
		if has && !ok {
			c.uiAt(agentimport.Lossy, "cline/range-invalid", i, "a deleted range that is not two counts in order is ignored")
			continue
		}
		r[0], r[1] = min(r[0], n), min(r[1], n)
		if !has && cur == nil || has && cur != nil && *cur == r {
			continue
		}
		ev := &event{kind: evRange, order: i, special: -1, ui: i}
		ev.ts, _ = m.Time()
		if hi, ok := m.HistoryIndex(); ok {
			ev.p = hi + 1
			if c.summarizes(hi + 1) {
				// summarize_task and condense set the range while their
				// own response streams; it governs the request after it.
				ev.p = hi + 2
			}
		} else {
			ev.p = c.posByTime(ev.ts) + 1
			ev.placed = "by the UI message's time"
		}
		ev.p = min(max(ev.p, lastP, 0), n)
		lastP = ev.p
		if has {
			ev.rng = r
			cur = &r
		} else {
			ev.noRange = true
			cur = nil
		}
		c.events = append(c.events, ev)
	}
	hi := c.t.History
	if hi == nil {
		return
	}
	r, ok := hi.DeletedRange()
	r[0], r[1] = min(r[0], n), min(r[1], n)
	if ok && (cur == nil || *cur != r) {
		c.events = append(c.events, &event{p: n, kind: evRange, order: len(c.t.UI.Elems), rng: r, special: -1, ui: -1,
			placed: "at the end, from the index entry, since no UI message carries it"})
	}
}

// summarizes reports whether API element k is a response calling
// summarize_task or condense, natively or as XML in its text.
func (c *conv) summarizes(k int) bool {
	if k < 0 || k >= len(c.t.API.Elems) {
		return false
	}
	m := c.t.API.Elems[k]
	if m.Err != nil || m.Role != cline.RoleAssistant {
		return false
	}
	is := func(name string) bool { return name == "summarize_task" || name == "condense" }
	for _, b := range m.ToolUses() {
		if is(b.Name) {
			return true
		}
	}
	texts := []string{}
	if m.Content.IsText {
		texts = append(texts, m.Content.Text)
	}
	for _, b := range m.Content.Blocks {
		if b.Err == nil && b.Type == cline.BlockText {
			texts = append(texts, b.Text)
		}
	}
	for _, t := range texts {
		for _, x := range cline.ParseXMLTools(t) {
			if is(x.Name) {
				return true
			}
		}
	}
	return false
}

// rooEvents reads Roo's summaries and markers. Each is matched to the
// UI record of its condense or truncation, by ID, else in order; the
// record's time places it among the messages it kept. A fresh-start
// summary was the last message when written, so it is placed where it
// stands.
func (c *conv) rooEvents() {
	elems := c.t.API.Elems
	n := len(elems)
	var condense, truncs []int
	for i, m := range c.t.UI.Elems {
		if m.Err != nil || m.Partial || m.Type != "say" {
			continue
		}
		switch m.Say {
		case "condense_context":
			condense = append(condense, i)
		case "sliding_window_truncation":
			truncs = append(truncs, i)
		}
	}
	// Records are matched by ID first, every summary and marker, then
	// the rest pair in order, so a summary whose record is gone does
	// not take the next one's.
	used := map[int]bool{}
	recordID := func(i int, trunc bool) string {
		u := c.t.UI.Elems[i]
		raw, key := u.ContextCondense, "condenseId"
		if trunc {
			raw, key = u.ContextTruncation, "truncationId"
		}
		var v map[string]json.RawMessage
		var id string
		if json.Unmarshal(raw, &v) == nil && json.Unmarshal(v[key], &id) == nil {
			return id
		}
		return ""
	}
	var specials []int
	byID := map[int]int{}
	for s, m := range elems {
		if m.Err != nil || !(m.IsSummary || m.IsTruncationMarker) {
			continue
		}
		specials = append(specials, s)
		trunc, list, id := !m.IsSummary, condense, m.CondenseID
		if trunc {
			list, id = truncs, m.TruncationID
		}
		if id == "" {
			continue
		}
		for _, i := range list {
			if !used[i] && recordID(i, trunc) == id {
				used[i] = true
				byID[s] = i
				break
			}
		}
	}
	match := func(s int, list []int) int {
		if i, ok := byID[s]; ok {
			return i
		}
		for _, i := range list {
			if !used[i] {
				used[i] = true
				return i
			}
		}
		return -1
	}
	for _, s := range specials {
		m := elems[s]
		c.special[s] = true
		ev := &event{order: s, special: s, ui: -1}
		if m.IsSummary {
			ev.kind = evCondense
			switch {
			case m.Role == cline.RoleUser:
				ev.era = eraFresh
			case m.CondenseID != "":
				ev.era = eraTagged
			default:
				ev.era = eraDestructive
			}
			ev.ui = match(s, condense)
		} else {
			ev.kind = evTruncation
			ev.ui = match(s, truncs)
		}
		if ev.ui >= 0 {
			ev.ts, _ = c.t.UI.Elems[ev.ui].Time()
			c.mark(unit{uUI, ev.ui}, agentimport.LineMapped)
		}
		switch {
		case ev.era == eraFresh:
			ev.p = s + 1
			if ev.ts.IsZero() {
				ev.ts, _ = m.Time()
			}
		case ev.ui >= 0 && !ev.ts.IsZero():
			ev.p = n
			for i := s + 1; i < n; i++ {
				if c.special[i] || elems[i].Err != nil {
					continue
				}
				if t, ok := elems[i].Time(); ok && t.After(ev.ts) {
					ev.p = i
					break
				}
			}
		case ev.kind == evCondense:
			ev.p, ev.placed = n, fmt.Sprintf("after the %d messages Roo kept, since no UI record of the condense was found", keptAfterSummary)
			kept := 0
			for i := s + 1; i < n; i++ {
				if elems[i].Err != nil || elems[i].IsSummary || elems[i].IsTruncationMarker {
					continue
				}
				if kept++; kept == keptAfterSummary {
					ev.p = i + 1
					break
				}
			}
		default:
			ev.p, ev.placed = n, "at the end of the messages it kept, since no UI record of the truncation was found"
			for i := s + 1; i < n; i++ {
				if elems[i].Err == nil && (elems[i].IsSummary || elems[i].IsTruncationMarker) {
					ev.p = i
					break
				}
			}
		}
		ev.p = max(ev.p, s+1)
		c.events = append(c.events, ev)
	}
}

// applyEvents writes the events placed before element k: an omit
// config entry for a filter of the history, as classic's ranges are; a
// compaction for a Roo summary or marker.
func (c *conv) applyEvents(k int) error {
	for c.nextEvt < len(c.events) && c.events[c.nextEvt].p <= k {
		idx := c.nextEvt
		c.nextEvt++
		if next := idx + 1; !c.roo && next < len(c.events) && c.events[next].p == c.events[idx].p {
			// Another range replaced this one before any request.
			continue
		}
		spec := c.spec(idx)
		var err error
		if slices.ContainsFunc(spec, func(k int) bool { return c.special[k] }) {
			err = c.compaction(idx, spec)
		} else {
			err = c.filter(idx, spec)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// spec is the list of API elements the requests after event idx send,
// in order.
func (c *conv) spec(idx int) []int {
	ev := c.events[idx]
	if !c.roo {
		var out []int
		if ev.noRange {
			for k := 0; k < ev.p; k++ {
				out = append(out, k)
			}
			return out
		}
		for k := 0; k < 2 && k < ev.p; k++ {
			out = append(out, k)
		}
		for k := ev.rng[1] + 1; k < ev.p; k++ {
			if k >= 2 {
				out = append(out, k)
			}
		}
		return out
	}
	return c.rooSpec(idx)
}

// rooSpec is what the Roo release that wrote the file sent after event
// idx: the messages and the summaries and markers that existed then,
// filtered as getEffectiveApiHistory filters them, from the last
// summary on. A user-role summary is 3.43's fresh start, which sends
// nothing before it; before 3.43 an assistant-role summary was sent
// after the task's first message, which getMessagesSinceLastSummary
// put back, and Roo's current release no longer does.
func (c *conv) rooSpec(idx int) []int {
	ev := c.events[idx]
	elems := c.t.API.Elems
	exists := map[int]bool{}
	for i := 0; i <= idx; i++ {
		if s := c.events[i].special; s >= 0 {
			exists[s] = true
		}
	}
	var arr []int
	for k, m := range elems {
		if m.Err != nil {
			continue
		}
		if c.special[k] {
			if exists[k] {
				arr = append(arr, k)
			}
		} else if k < ev.p {
			arr = append(arr, k)
		}
	}
	lastSummary := func(list []int) int {
		at := -1
		for i, k := range list {
			if elems[k].IsSummary {
				at = i
			}
		}
		return at
	}
	if ls := lastSummary(arr); ls >= 0 && elems[arr[ls]].Role == cline.RoleUser {
		slice := arr[ls:]
		markers := map[string]bool{}
		for _, k := range slice {
			if m := elems[k]; m.IsTruncationMarker && m.TruncationID != "" {
				markers[m.TruncationID] = true
			}
		}
		var out []int
		for _, k := range slice {
			if tp := elems[k].TruncationParent; tp != "" && markers[tp] {
				continue
			}
			out = append(out, k)
		}
		return out
	}
	summaries, markers := map[string]bool{}, map[string]bool{}
	for _, k := range arr {
		m := elems[k]
		if m.IsSummary && m.CondenseID != "" {
			summaries[m.CondenseID] = true
		}
		if m.IsTruncationMarker && m.TruncationID != "" {
			markers[m.TruncationID] = true
		}
	}
	var eff []int
	for _, k := range arr {
		m := elems[k]
		if m.CondenseParent != "" && summaries[m.CondenseParent] || m.TruncationParent != "" && markers[m.TruncationParent] {
			continue
		}
		eff = append(eff, k)
	}
	ls := lastSummary(eff)
	if ls < 0 {
		return eff
	}
	slice := slices.Clone(eff[ls:])
	if elems[slice[0]].Role != cline.RoleUser {
		if ls > 0 && elems[eff[0]].Role == cline.RoleUser {
			return append([]int{eff[0]}, slice...)
		}
		c.apiAt(agentimport.Lossy, "cline/roo-no-first-message", ev.special, "the history before the summary holds no user message to send first; Roo sent \"Please continue from the following summary:\", which is not written")
	}
	return slice
}

// filter writes event idx as what it is when the requests after it
// send messages the path holds, in its order, and leave others out:
// an omit config entry naming the item entries left out. Classic's
// range is this: messages 0 and 1 stay where they are, and what the
// range deletes is omitted, with the tool results of the first message
// after it. Cline sends "result missing" for message 1's calls, whose
// results the range deletes; their outputs stay, in that place, and the
// replaced text is a context-replace gap.
func (c *conv) filter(idx int, spec []int) error {
	ev := c.events[idx]
	in := map[int]bool{}
	visible := map[string]bool{}
	for _, k := range spec {
		in[k] = true
		for _, id := range c.callsAt[k] {
			visible[id] = true
		}
	}
	var hide []string
	replaced := 0
	for k := 0; k < ev.p && k < len(c.itemEntries); k++ {
		if in[k] || c.special[k] {
			continue
		}
		for _, id := range c.itemEntries[k] {
			if call, ok := c.outputCall[id]; ok && visible[call.id] && !c.roo {
				replaced++
				continue
			}
			hide = append(hide, id)
		}
	}
	if !c.roo {
		c.cut, c.cutDone = -1, false
		if !ev.noRange {
			c.cut = ev.rng[1] + 1
			if c.cut < ev.p && c.cut < len(c.outputs) {
				hide = append(hide, c.outputs[c.cut]...)
				c.cutDone = true
			}
		}
	}
	if err := c.setOmit(append(slices.Clone(c.reasonOmits), hide...), ev.ts); err != nil {
		return err
	}
	line := 0
	if ev.special >= 0 {
		line = ev.special + 1
		c.mark(unit{uAPI, ev.special}, agentimport.LineDropped)
		c.apiAt(agentimport.Dropped, "cline/special-unsent", ev.special, "the requests after this %s do not send it; it is left out", ev.kind)
	}
	switch {
	case ev.kind == evRange && ev.noRange:
		c.addf(agentimport.Lossy, "cline/deleted-range", c.t.API.Name, line, "from API message %d the deleted range is reset: requests send the whole history again, and the omit config entry clears what it left out", ev.p+1)
	case ev.kind == evRange:
		c.addf(agentimport.Lossy, "cline/deleted-range", c.t.API.Name, line, "from API message %d requests leave out API messages %d to %d; an omit config entry names their items, and messages 1 and 2 stay where they are", ev.p+1, ev.rng[0]+1, ev.rng[1]+1)
	}
	if replaced > 0 {
		c.addf(agentimport.Gap, "agentimport/context-replace", c.t.API.Name, line,
			"from API message %d Cline sends %q in place of the %d outputs of message 2's calls, whose results the range deleted; the session keeps the outputs", ev.p+1, resultMissingText, replaced)
	}
	if ev.placed != "" {
		c.addf(agentimport.Lossy, "cline/event-placed", c.t.API.Name, line, "the %s is placed %s", ev.kind, ev.placed)
	}
	return nil
}

// setOmit makes the items omitted in force desired: an omit config
// entry adding those not omitted yet, after one clearing the set when
// something omitted is no longer.
func (c *conv) setOmit(desired []string, ts time.Time) error {
	want := map[string]bool{}
	var list []string
	for _, id := range desired {
		if !want[id] {
			want[id] = true
			list = append(list, id)
		}
	}
	clearing := false
	for id := range c.omitNow {
		if !want[id] {
			clearing = true
			break
		}
	}
	var add []string
	if clearing {
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{}}
		cfg.Timestamp = ts
		if _, err := c.appendEntry(cfg); err != nil {
			return err
		}
		add = list
	} else {
		for _, id := range list {
			if !c.omitNow[id] {
				add = append(add, id)
			}
		}
	}
	if len(add) > 0 {
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: add}}
		cfg.Timestamp = ts
		if _, err := c.appendEntry(cfg); err != nil {
			return err
		}
	}
	c.omitNow = want
	return nil
}

// addOmit omits the item entries ids of element k from here on;
// reasoning says they stay omitted whatever a range does.
func (c *conv) addOmit(k int, ids []string, ts time.Time, reasoning bool) error {
	for _, id := range ids {
		c.omitted[id] = k
	}
	if reasoning {
		c.reasonOmits = append(c.reasonOmits, ids...)
	}
	cur := make([]string, 0, len(c.omitNow)+len(ids))
	for id := range c.omitNow {
		cur = append(cur, id)
	}
	slices.Sort(cur)
	return c.setOmit(append(cur, ids...), ts)
}

// specialParts is what a Roo summary or marker put in a request: its
// text and a user's images, as the parts of the compaction's summary,
// and the calls and encrypted reasoning a 3.36 to 3.42 summary carries
// for the results it kept, as the item entries already written for
// them, to pin. Its blocks' other members are returned by index.
func (c *conv) specialParts(k int) (openresponses.Contents, openresponses.Items, map[string]json.RawMessage) {
	m := c.t.API.Elems[k]
	var parts openresponses.Contents
	var pinned openresponses.Items
	var members map[string]json.RawMessage
	keep := func(i int, rest map[string]json.RawMessage) {
		if len(rest) == 0 {
			return
		}
		raw, err := json.Marshal(rest)
		if err != nil {
			return
		}
		if members == nil {
			members = map[string]json.RawMessage{}
		}
		members[fmt.Sprint(i)] = raw
	}
	if m.Content.IsText {
		parts = append(parts, &openresponses.InputText{Text: m.Content.Text})
	}
	plain, missing, left := 0, 0, 0
	for i, b := range m.Content.Blocks {
		switch {
		case b.Err != nil:
			left++
		case b.Type == cline.BlockText:
			parts = append(parts, &openresponses.InputText{Text: b.Text})
			keep(i, blockRest(b, "text"))
		case b.Type == cline.BlockImage && b.Source != nil && b.Source.Type == "base64" && m.Role == cline.RoleUser:
			parts = append(parts, &openresponses.InputImage{ImageURL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
			keep(i, blockRest(b, "source"))
		case b.Type == cline.BlockToolUse:
			if p := c.callByNative[b.ID]; p != nil && p.elem < k {
				pinned = append(pinned, p.item)
			} else {
				missing++
			}
		case b.Type == cline.BlockReasoning && b.EncryptedContent != "", b.Type == cline.BlockThinking && b.Signature != "":
			enc := b.EncryptedContent
			if enc == "" {
				enc = b.Signature
			}
			if r, ok := c.reasoningByEnc[enc]; ok && r.elem < k {
				pinned = append(pinned, r.item)
			} else {
				missing++
			}
		case b.Type == cline.BlockReasoning || b.Type == cline.BlockThinking:
			plain++
		default:
			left++
		}
	}
	if plain > 0 {
		c.apiAt(agentimport.Lossy, "cline/roo-plain-reasoning", k, "the summary's %d plain reasoning blocks, which Roo leaves out unless the model preserves reasoning, are left out of the compaction", plain)
	}
	if missing > 0 {
		c.apiAt(agentimport.Dropped, "cline/summary-content", k, "the summary carries %d calls or reasoning items no earlier message wrote; they are left out of the compaction", missing)
	}
	if left > 0 || m.Content.Err != nil {
		c.apiAt(agentimport.Dropped, "cline/summary-content", k, "a summary or marker holds %d blocks other than text, images, calls and reasoning, which are left out of the compaction", left)
	}
	return parts, pinned, members
}

// compaction writes event idx, a Roo summary or marker, as a
// compaction: its summary a user message of the summaries' and
// markers' text the requests carried, pinned the item entries the
// requests sent with them (the first message an older summary was sent
// after, the calls a summary carried for the results it kept),
// first_kept the first message kept after them, and an omit in its
// settings for what the requests left out after that. The format puts
// the summary first; a request that sent a message ahead of it, or the
// summary as an assistant message, is the fold-head gap.
func (c *conv) compaction(idx int, spec []int) error {
	ev := c.events[idx]
	elems := c.t.API.Elems
	ls := 0
	for i, k := range spec {
		if c.special[k] {
			ls = i
		}
	}
	head, tail := spec[:ls+1], spec[ls+1:]
	var parts openresponses.Contents
	var pinned openresponses.Items
	var members map[string]json.RawMessage
	ahead, assistant := false, false
	var heads []string
	for _, k := range head {
		heads = append(heads, fmt.Sprint(k+1))
		if !c.special[k] {
			ahead = true
			pinned = append(pinned, c.items[k]...)
			continue
		}
		p, pin, mem := c.specialParts(k)
		parts = append(parts, p...)
		pinned = append(pinned, pin...)
		if k == ev.special {
			members = mem
		}
		if elems[k].Role == cline.RoleAssistant {
			assistant = true
		}
	}
	if len(parts) == 0 {
		c.addf(agentimport.Lossy, "cline/compaction-empty", c.t.API.Name, ev.special+1, "the summaries and markers the requests sent hold no text; no compaction is written")
		c.mark(unit{uAPI, ev.special}, agentimport.LineDropped)
		return nil
	}
	var firstKept string
	var err error
	if len(tail) > 0 {
		firstKept = c.first[tail[0]]
	} else if firstKept, err = c.keepNone(ev); err != nil {
		return err
	}
	ctx, err := c.b.S.ContextAt(c.tail)
	if err != nil {
		return err
	}
	cfg := ctx.Settings
	// The checkpoint omits what stays omitted in the window it keeps,
	// and what the requests left out of it.
	var omit []string
	if len(tail) > 0 {
		start := tail[0]
		for _, id := range cfg.Omit.Items {
			if k, ok := c.omitted[id]; ok && k >= start {
				omit = append(omit, id)
			}
		}
		in := map[int]bool{}
		for _, k := range tail {
			in[k] = true
		}
		for k := start; k < ev.p && k < len(c.itemEntries); k++ {
			if !in[k] && !c.special[k] {
				omit = append(omit, c.itemEntries[k]...)
			}
		}
	}
	cfg.Omit.Items = omit
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: openresponses.UserMessage(parts...), Pinned: pinned, Config: cfg}
	e.Timestamp = ev.ts
	if err := c.eventMembers(e, ev, members); err != nil {
		return err
	}
	if _, err := c.appendEntry(e); err != nil {
		return err
	}
	c.mark(unit{uAPI, ev.special}, agentimport.LineMapped)
	c.omitNow = map[string]bool{}
	for _, id := range omit {
		c.omitNow[id] = true
	}
	if ev.era == eraFresh {
		// Roo leaves out a later result whose call is before this.
		c.condensed = map[string]bool{}
		for native := range c.callByNative {
			c.condensed[native] = true
		}
	}
	kept := "nothing after it"
	if len(tail) > 0 {
		kept = fmt.Sprintf("the messages from API message %d kept", tail[0]+1)
	}
	switch ev.kind {
	case evCondense:
		c.apiAt(agentimport.Lossy, "cline/roo-condense", ev.special, "a %s summary (%s): a compaction of API messages %s, which the requests sent ahead of %s, as that release sent them", ev.era, eraReleases(ev.era), strings.Join(heads, ", "), kept)
	case evTruncation:
		c.apiAt(agentimport.Lossy, "cline/roo-truncation", ev.special, "a sliding-window marker: a compaction of API messages %s, which the requests sent ahead of %s, as Roo sends them", strings.Join(heads, ", "), kept)
	}
	if ahead || assistant {
		why := []string{}
		if ahead {
			why = append(why, "the requests sent a message ahead of the summary, which is pinned after it")
		}
		if assistant {
			why = append(why, "the requests sent the summary as an assistant message, which the compaction's summary carries as a user message so no request starts with the assistant")
		}
		c.apiAt(agentimport.Gap, "agentimport/fold-head", ev.special, "%s", strings.Join(why, "; "))
	}
	if ev.placed != "" {
		c.apiAt(agentimport.Lossy, "cline/event-placed", ev.special, "the %s is placed %s", ev.kind, ev.placed)
	}
	return nil
}

// keepNone returns the entry a compaction that keeps nothing of the
// path names as first_kept: the tail when it puts nothing in context,
// else a custom marker written for it.
func (c *conv) keepNone(ev *event) (string, error) {
	if e, ok := c.b.S.Entry(c.tail); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return c.tail, nil
		}
	}
	data, err := json.Marshal(map[string]string{"keeps_nothing": ev.kind})
	if err != nil {
		return "", err
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: data}
	marker.Timestamp = ev.ts
	id, err := c.appendEntry(marker)
	if err != nil {
		return "", err
	}
	c.addf(agentimport.Kept, "cline/compaction-marker", c.t.API.Name, ev.special+1,
		"the requests keep nothing written before this point, so a custom marker written before the compaction is its first_kept, which puts nothing in context")
	return id, nil
}

// eventMembers sets what an event records beside the compaction: the
// UI record of a Roo condense or truncation, the summary's or marker's
// own members, the range.
func (c *conv) eventMembers(e *agentsession.CompactionEntry, ev *event, blocks map[string]json.RawMessage) error {
	info := map[string]any{"kind": ev.kind}
	if ev.kind == evRange {
		if ev.noRange {
			info["range"] = nil
		} else {
			info["range"] = ev.rng
		}
	}
	if ev.era != "" {
		info["era"] = ev.era
	}
	if ev.placed != "" {
		info["placed"] = ev.placed
	}
	if err := c.b.Member(e, "event", info); err != nil {
		return err
	}
	if ev.special >= 0 {
		m := c.t.API.Elems[ev.special]
		rest, err := messageRest(m)
		if err != nil {
			return err
		}
		if len(blocks) > 0 {
			raw, err := json.Marshal(blocks)
			if err != nil {
				return err
			}
			if rest == nil {
				rest = map[string]json.RawMessage{}
			}
			rest["content_blocks"] = raw
		}
		if err := c.keepMembers(e, ev.special, "message", rest, "the summary's or marker's"); err != nil {
			return err
		}
	}
	if ev.ui < 0 || ev.kind == evRange {
		return nil
	}
	u := c.t.UI.Elems[ev.ui]
	detail := u.ContextCondense
	if ev.kind == evTruncation {
		detail = u.ContextTruncation
	}
	var d struct {
		Prev json.RawMessage `json:"prevContextTokens"`
	}
	if json.Unmarshal(detail, &d) == nil {
		if n, exact, ok := cline.Int(d.Prev); ok && exact && n > 0 && n < 1<<40 {
			e.TokensBefore = int(n)
		}
	}
	if c.fam.opts.Slim {
		c.uiAt(agentimport.Dropped, "cline/ui-record", ev.ui, "the UI record of the %s is left out beyond the tokens before it", ev.kind)
		return nil
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if raw, err = c.clean(c.t.UI.Name, ev.ui+1, raw); err != nil {
		return err
	}
	return c.b.Member(e, "ui", json.RawMessage(raw))
}

// eraReleases names the Roo releases that wrote a condense era.
func eraReleases(era string) string {
	switch era {
	case eraFresh:
		return "user role, Roo 3.43 and later"
	case eraTagged:
		return "assistant role with a condenseId, Roo 3.36 to 3.42"
	}
	return "assistant role, no condenseId, Roo 3.35 and earlier"
}
