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
		if !has && cur == nil || has && cur != nil && *cur == r {
			continue
		}
		ev := &event{kind: evRange, order: i, special: -1, ui: i}
		ev.ts, _ = m.Time()
		if hi, ok := m.HistoryIndex(); ok {
			ev.p = hi + 1
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
	if ok && (cur == nil || *cur != r) {
		c.events = append(c.events, &event{p: n, kind: evRange, order: len(c.t.UI.Elems), rng: r, special: -1, ui: -1,
			placed: "at the end, from the index entry, since no UI message carries it"})
	}
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
	used := map[int]bool{}
	match := func(list []int, member func(*cline.UIMessage) json.RawMessage, key, id string) int {
		if id != "" {
			for _, i := range list {
				var v map[string]json.RawMessage
				var got string
				if !used[i] && json.Unmarshal(member(c.t.UI.Elems[i]), &v) == nil && json.Unmarshal(v[key], &got) == nil && got == id {
					used[i] = true
					return i
				}
			}
		}
		for _, i := range list {
			if !used[i] {
				used[i] = true
				return i
			}
		}
		return -1
	}
	for s, m := range elems {
		if m.Err != nil || !(m.IsSummary || m.IsTruncationMarker) {
			continue
		}
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
			ev.ui = match(condense, func(u *cline.UIMessage) json.RawMessage { return u.ContextCondense }, "condenseId", m.CondenseID)
		} else {
			ev.kind = evTruncation
			ev.ui = match(truncs, func(u *cline.UIMessage) json.RawMessage { return u.ContextTruncation }, "truncationId", m.TruncationID)
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

// applyEvents writes the compactions of the events placed before
// element k.
func (c *conv) applyEvents(k int) error {
	for c.nextEvt < len(c.events) && c.events[c.nextEvt].p <= k {
		idx := c.nextEvt
		c.nextEvt++
		if next := idx + 1; !c.roo && next < len(c.events) && c.events[next].p == c.events[idx].p {
			// Another range replaced this one before any request.
			continue
		}
		if err := c.compaction(idx); err != nil {
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

// itemsOf is the items element k puts in context: those written for an
// ordinary element, those of the summary or marker for a special one.
func (c *conv) itemsOf(k int) []openresponses.Item {
	if !c.special[k] {
		return c.items[k]
	}
	if items, ok := c.specialItems[k]; ok {
		return items
	}
	items := c.summaryItems(k)
	c.specialItems[k] = items
	return items
}

// summaryItems is the message a Roo summary or marker puts in context:
// its text, and a user's images. Other blocks are left out and
// reported.
func (c *conv) summaryItems(k int) []openresponses.Item {
	m := c.t.API.Elems[k]
	role := openresponses.RoleUser
	if m.Role == cline.RoleAssistant {
		role = openresponses.RoleAssistant
	}
	var parts openresponses.Contents
	text := func(s string) openresponses.Content {
		if role == openresponses.RoleAssistant {
			return &openresponses.OutputText{Text: s, Annotations: openresponses.Annotations{}}
		}
		return &openresponses.InputText{Text: s}
	}
	if m.Content.IsText {
		parts = append(parts, text(m.Content.Text))
	}
	left := 0
	for _, b := range m.Content.Blocks {
		switch {
		case b.Err == nil && b.Type == cline.BlockText:
			parts = append(parts, text(b.Text))
		case b.Err == nil && b.Type == cline.BlockImage && b.Source != nil && b.Source.Type == "base64" && role == openresponses.RoleUser:
			parts = append(parts, &openresponses.InputImage{ImageURL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
		default:
			left++
		}
	}
	if left > 0 || m.Content.Err != nil {
		c.apiAt(agentimport.Dropped, "cline/summary-content", k, "a summary or marker holds content other than text and images, which is left out of the compaction (%d blocks)", left)
	}
	if len(parts) == 0 {
		return nil
	}
	msg := &openresponses.Message{Role: role, Content: parts}
	if role == openresponses.RoleAssistant {
		msg.Status = openresponses.StatusCompleted
	}
	return []openresponses.Item{msg}
}

// compaction writes event idx: the requests after it send the list
// spec gives, which is written as the longest run at the path's end
// that the list ends with, kept from first_kept, and the elements
// before that run, whose items are copied unedited into the
// compaction's summary (the first) and pinned items (the rest), so the
// context holds them in the order sent.
func (c *conv) compaction(idx int) error {
	ev := c.events[idx]
	spec := c.spec(idx)
	var live []int
	for _, k := range spec {
		if len(c.itemsOf(k)) > 0 {
			live = append(live, k)
		}
	}
	var ord []int
	for k := 0; k < ev.p && k < len(c.t.API.Elems); k++ {
		if !c.special[k] && len(c.items[k]) > 0 {
			ord = append(ord, k)
		}
	}
	i, j := len(live)-1, len(ord)-1
	for i >= 0 && j >= 0 && !c.special[live[i]] && live[i] == ord[j] {
		i--
		j--
	}
	split := i + 1
	if split == 0 {
		if j < 0 && c.compacts == 0 && ev.special < 0 {
			// The requests send everything on the path.
			return nil
		}
		split = 1
	}
	if len(live) == 0 {
		c.addf(agentimport.Lossy, "cline/compaction-empty", c.t.API.Name, ev.special+1, "the requests after this point send nothing written so far; no compaction is written")
		if ev.special >= 0 {
			c.mark(unit{uAPI, ev.special}, agentimport.LineDropped)
		}
		return nil
	}
	head, tail := live[:split], live[split:]
	var headItems []openresponses.Item
	var copied []string
	for _, k := range head {
		headItems = append(headItems, c.itemsOf(k)...)
		copied = append(copied, fmt.Sprint(k+1))
		if !c.roo && k == 1 {
			headItems = append(headItems, c.missingAfterCut()...)
		}
	}
	firstKept := ""
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
	// The checkpoint omits what is still omitted in the window it
	// keeps; what is before first_kept is out of context anyway.
	var omit []string
	for _, id := range cfg.Omit.Items {
		if k, ok := c.omitted[id]; ok && len(tail) > 0 && k >= tail[0] {
			omit = append(omit, id)
		}
	}
	if !c.roo && ev.kind == evRange && !ev.noRange {
		c.cut, c.cutDone = ev.rng[1]+1, false
		if c.cut < ev.p && c.cut < len(c.outputs) && len(c.outputs[c.cut]) > 0 {
			for _, id := range c.outputs[c.cut] {
				c.omitted[id] = c.cut
			}
			omit = append(omit, c.outputs[c.cut]...)
			c.cutDone = true
		}
	} else if !c.roo {
		c.cut = -1
	}
	cfg.Omit.Items = omit
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: headItems[0], Pinned: openresponses.Items(headItems[1:]), Config: cfg}
	e.Timestamp = ev.ts
	if err := c.eventMembers(e, ev); err != nil {
		return err
	}
	if _, err := c.appendEntry(e); err != nil {
		return err
	}
	if ev.special >= 0 {
		c.mark(unit{uAPI, ev.special}, agentimport.LineMapped)
	}
	c.compacts++
	c.eventFindings(ev, copied, len(tail) > 0)
	return nil
}

// missingAfterCut is the output Cline sends for each call of API
// message 1 once a range has cut the message that answered it: "result
// missing", first in the message after the cut.
func (c *conv) missingAfterCut() []openresponses.Item {
	var out []openresponses.Item
	if len(c.items) < 2 {
		return nil
	}
	for _, it := range c.items[1] {
		if fc, ok := it.(*openresponses.FunctionCall); ok {
			out = append(out, &openresponses.FunctionCallOutput{CallID: fc.CallID, Output: openresponses.FunctionCallOutputData{Text: resultMissingText}})
		}
	}
	if len(out) > 0 {
		c.apiAt(agentimport.Repaired, "cline/result-missing", 1, "the range cut the results of this message's calls; Cline sends %q for each, first in the message after the cut, which the compaction's pinned items hold", resultMissingText)
	}
	return out
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
func (c *conv) eventMembers(e *agentsession.CompactionEntry, ev *event) error {
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

// eventFindings reports how an event was written.
func (c *conv) eventFindings(ev *event, copied []string, keptTail bool) {
	head := "the compaction's summary is a copy of API message " + copied[0]
	if len(copied) > 1 {
		head += " and its pinned items copies of " + strings.Join(copied[1:], ", ")
	}
	tail := "nothing after"
	if keptTail {
		tail = "the messages after it kept from first_kept"
	}
	line := ev.special + 1
	if ev.ui >= 0 && ev.kind == evRange {
		line = 0
	}
	switch ev.kind {
	case evRange:
		what := "the deleted range is reset"
		if !ev.noRange {
			what = fmt.Sprintf("requests leave out API messages %d to %d", ev.rng[0]+1, ev.rng[1]+1)
		}
		c.addf(agentimport.Lossy, "cline/deleted-range", c.t.API.Name, line, "from API message %d, %s: %s, %s, as Cline sends them", ev.p+1, what, head, tail)
		if !ev.noRange {
			keys := make([][2]int, 0, len(c.edits))
			for key := range c.edits {
				if key[0] == 0 || key[0] == 1 {
					keys = append(keys, key)
				}
			}
			slices.SortFunc(keys, func(a, b [2]int) int {
				if a[0] != b[0] {
					return a[0] - b[0]
				}
				return a[1] - b[1]
			})
			for _, key := range keys {
				list := c.edits[key]
				c.apiAt(agentimport.Lossy, "cline/pinned-unedited", key[0], "the compaction holds API message %d unedited; the requests sent %s with its text replaced by %s", key[0]+1, c.describeBlock(key[0], key[1]), describeText(list[len(list)-1].NewText()))
			}
		}
	case evCondense:
		c.apiAt(agentimport.Lossy, "cline/roo-condense", ev.special, "a %s summary (%s): %s, %s, as that release sent them", ev.era, eraReleases(ev.era), head, tail)
	case evTruncation:
		c.apiAt(agentimport.Lossy, "cline/roo-truncation", ev.special, "a sliding-window marker: %s, %s, as Roo sends them", head, tail)
	}
	if ev.placed != "" {
		c.addf(agentimport.Lossy, "cline/event-placed", c.t.API.Name, line, "the %s is placed %s", ev.kind, ev.placed)
	}
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
