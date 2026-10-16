package convert

import (
	"encoding/json"
	"sort"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// A port of how OpenHands builds the model's context
// (context/view/view.py and context/view/properties/, SDK 1.12 to
// 1.55): the events on the path in order, each condensation applied
// where it stands, then four properties enforced until none removes
// anything.

// vEvent is one element of the view: an event, or a condensation's
// summary when idx is -1.
type vEvent struct {
	idx       int
	summaryOf int
}

// viewState is the view as of one event, and what its properties read
// of the whole path: the actions of each response, and the tool loops.
type viewState struct {
	view    []vEvent
	batches map[string]map[int]bool
	// loops are the path's tool loops, a run of actions and outputs from
	// an action with thinking blocks; open says the last one is still
	// open.
	loops  [][]int
	loopOf map[int]int
	open   bool
}

func newViewState() *viewState {
	return &viewState{batches: map[string]map[int]bool{}, loopOf: map[int]int{}}
}

// stateKey is the event whose view the view after event p is: p, or,
// for a malformed event or a tree artifact, which change nothing, its
// parent's.
func (c *conv) stateKey(p int) int {
	for p >= 0 && (c.c.Events[p].Err != nil || c.treeArtifact(p)) {
		p = c.parent[p]
	}
	return p
}

// viewFor makes the view the one event i is written after: its
// parent's, rebuilt from the path when the walk last left another.
func (c *conv) viewFor(i int) {
	want := c.stateKey(c.parent[i])
	if want == c.vsKey {
		return
	}
	c.vs = newViewState()
	var path []int
	for p := want; p >= 0; p = c.parent[p] {
		path = append(path, p)
	}
	for k := len(path) - 1; k >= 0; k-- {
		c.vs.apply(c, path[k])
	}
	c.vsKey = want
}

// viewApply adds event i to the view.
func (c *conv) viewApply(i int) {
	if c.stateKey(i) != i {
		return
	}
	c.vs.apply(c, i)
	c.vsKey = i
}

// apply adds event j, the next on the path, to the view.
func (vs *viewState) apply(c *conv, j int) {
	e := c.c.Events[j]
	if e.Err != nil {
		return
	}
	in := c.info[j]
	ok := in.bad == nil
	switch {
	case ok && in.kind == openhands.KindAction && in.thinking:
		vs.loops = append(vs.loops, []int{j})
		vs.loopOf[j] = len(vs.loops) - 1
		vs.open = true
	case ok && (in.kind == openhands.KindAction || openhands.IsObservationLike(in.kind)):
		if vs.open {
			k := len(vs.loops) - 1
			vs.loops[k] = append(vs.loops[k], j)
			vs.loopOf[j] = k
		}
	default:
		vs.open = false
	}
	if !ok {
		return
	}
	if in.kind == openhands.KindAction {
		if vs.batches[in.respID] == nil {
			vs.batches[in.respID] = map[int]bool{}
		}
		vs.batches[in.respID][j] = true
	}
	if openhands.InContext(in.kind) {
		vs.view = append(vs.view, vEvent{idx: j})
		return
	}
	if in.kind != openhands.KindCondensation {
		return
	}
	cd, err := e.Condensation()
	if err != nil {
		return
	}
	ids, _ := cd.Forgotten()
	forget := map[string]bool{}
	for _, id := range ids {
		forget[id] = true
	}
	kept := vs.view[:0:0]
	for _, v := range vs.view {
		if !forget[c.viewID(v)] {
			kept = append(kept, v)
		}
	}
	if off, ok := cd.Offset(); ok && cd.Summary != nil {
		off = min(off, len(kept))
		kept = append(kept[:off:off], append([]vEvent{{idx: -1, summaryOf: j}}, kept[off:]...)...)
	}
	vs.view = kept
}

// viewID is the ID OpenHands knows a view element by.
func (c *conv) viewID(v vEvent) string {
	if v.idx < 0 {
		return openhands.SummaryID(c.c.Events[v.summaryOf].ID)
	}
	return c.c.Events[v.idx].ID
}

// enforce is View.enforce_properties: observation uniqueness, batch
// atomicity, tool-call matching and tool-loop atomicity, each applied
// in turn and the turn begun again whenever one removes events. It
// returns the view and why each removed event left it.
func (c *conv) enforce() ([]vEvent, map[int]string) {
	view := append([]vEvent(nil), c.vs.view...)
	why := map[int]string{}
	drop := func(gone map[int]bool, reason string) {
		kept := view[:0]
		for _, v := range view {
			if v.idx >= 0 && gone[v.idx] {
				why[v.idx] = reason
				continue
			}
			kept = append(kept, v)
		}
		view = kept
	}
	for {
		if gone := c.uniqueness(view); len(gone) > 0 {
			drop(gone, "a second answer to a call already answered")
			continue
		}
		if gone := c.batchAtomicity(view); len(gone) > 0 {
			drop(gone, "an action of a response another of whose actions left the context")
			continue
		}
		if gone := c.callMatching(view); len(gone) > 0 {
			drop(gone, "a call with no answer in the context, or an answer with no call")
			continue
		}
		if gone := c.loopAtomicity(view); len(gone) > 0 {
			drop(gone, "part of a tool loop another part of which left the context")
			continue
		}
		return view, why
	}
}

func (c *conv) uniqueness(view []vEvent) map[int]bool {
	gone := map[int]bool{}
	seen := map[string]bool{}
	for _, v := range view {
		if v.idx < 0 || !openhands.IsObservationLike(c.info[v.idx].kind) {
			continue
		}
		id := c.info[v.idx].toolCallID
		if seen[id] {
			gone[v.idx] = true
		}
		seen[id] = true
	}
	return gone
}

func (c *conv) batchAtomicity(view []vEvent) map[int]bool {
	in := map[string]map[int]bool{}
	for _, v := range view {
		if v.idx >= 0 && c.info[v.idx].kind == openhands.KindAction {
			r := c.info[v.idx].respID
			if in[r] == nil {
				in[r] = map[int]bool{}
			}
			in[r][v.idx] = true
		}
	}
	gone := map[int]bool{}
	for r, set := range in {
		if len(set) != len(c.vs.batches[r]) {
			for j := range set {
				gone[j] = true
			}
		}
	}
	return gone
}

func (c *conv) callMatching(view []vEvent) map[int]bool {
	actions, answers := map[string]bool{}, map[string]bool{}
	for _, v := range view {
		if v.idx < 0 {
			continue
		}
		switch k := c.info[v.idx].kind; {
		case k == openhands.KindAction:
			actions[c.info[v.idx].toolCallID] = true
		case openhands.IsObservationLike(k):
			answers[c.info[v.idx].toolCallID] = true
		}
	}
	gone := map[int]bool{}
	for _, v := range view {
		if v.idx < 0 {
			continue
		}
		id := c.info[v.idx].toolCallID
		switch k := c.info[v.idx].kind; {
		case k == openhands.KindAction && !answers[id]:
			gone[v.idx] = true
		case openhands.IsObservationLike(k) && !actions[id]:
			gone[v.idx] = true
		}
	}
	return gone
}

func (c *conv) loopAtomicity(view []vEvent) map[int]bool {
	in := map[int]bool{}
	for _, v := range view {
		if v.idx >= 0 {
			in[v.idx] = true
		}
	}
	gone := map[int]bool{}
	for _, v := range view {
		if v.idx < 0 || gone[v.idx] {
			continue
		}
		k, ok := c.vs.loopOf[v.idx]
		if !ok {
			continue
		}
		whole := true
		for _, j := range c.vs.loops[k] {
			if !in[j] {
				whole = false
				break
			}
		}
		if !whole {
			for _, j := range c.vs.loops[k] {
				if in[j] {
					gone[j] = true
				}
			}
		}
	}
	return gone
}

// activePins returns the entries the compaction in force on a context
// pinned, whose items it carries.
func (c *conv) activePins(ctx agentsession.Context) map[string]bool {
	if len(ctx.Entries) > 0 {
		if comp, ok := ctx.Entries[0].(*agentsession.CompactionEntry); ok {
			return c.pinnedOf[comp.ID]
		}
	}
	return nil
}

// reconcile makes the context after at the view OpenHands builds for
// event i: an omit config entry leaves out the items the view does
// not hold, after one clearing the omit in force when something it left
// out is back. decorate, when set, is applied to the last entry
// written. It returns the entry the event's own entries follow.
func (c *conv) reconcile(i int, at string, decorate func(agentsession.Entry) error) (string, error) {
	view, why := c.enforce()
	return c.reconcileTo(i, at, view, why, decorate)
}

func (c *conv) reconcileTo(i int, at string, view []vEvent, why map[int]string, decorate func(agentsession.Entry) error) (string, error) {
	ctx, err := c.b.S.ContextAt(at)
	if err != nil {
		return "", err
	}
	desired := map[string]bool{}
	for _, v := range view {
		if v.idx >= 0 {
			for _, id := range c.items[v.idx] {
				desired[id] = true
			}
		}
	}
	pins := c.activePins(ctx)
	var drop []string
	for _, e := range ctx.ItemEntries {
		if ie, ok := e.(*agentsession.ItemEntry); ok && !desired[ie.ID] {
			drop = append(drop, ie.ID)
		}
	}
	for _, id := range sortedKeys(pins) {
		if !desired[id] {
			if j, ok := c.eventOf[id]; ok {
				c.at(agentimport.Lossy, "openhands/view-enforced", j, "OpenHands left this event out of the context from event %d (%s), but the compaction in force pins it, and a pinned item cannot be left out", i+1, nonEmpty(why[j], "forgotten"))
			}
		}
	}
	omitted := ctx.Settings.Omit.Items
	var restore []string
	for _, id := range omitted {
		if desired[id] && !pins[id] {
			restore = append(restore, id)
		}
	}
	if len(drop) == 0 && len(restore) == 0 {
		return at, nil
	}
	reported := map[int]bool{}
	for _, id := range drop {
		j, ok := c.eventOf[id]
		if !ok || reported[j] || why[j] == "" {
			continue
		}
		reported[j] = true
		c.at(agentimport.Lossy, "openhands/view-enforced", j, "OpenHands leaves this event out of the context from event %d: %s; an omit config entry leaves out its items", i+1, why[j])
	}
	var writes []*agentsession.ConfigEntry
	if len(restore) > 0 {
		back := map[string]bool{}
		for _, id := range restore {
			back[id] = true
			if j, ok := c.eventOf[id]; ok && !reported[j] {
				reported[j] = true
				c.at(agentimport.Lossy, "openhands/view-enforced", j, "OpenHands sends this event again from event %d; the omit in force is cleared and written again without it", i+1)
			}
		}
		var keep []string
		for _, id := range omitted {
			if !back[id] {
				keep = append(keep, id)
			}
		}
		keep = append(keep, drop...)
		writes = append(writes, &agentsession.ConfigEntry{Omit: &agentsession.Omit{}})
		if len(keep) > 0 {
			writes = append(writes, &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: keep}})
		}
	} else {
		writes = append(writes, &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: drop}})
	}
	for k, cfg := range writes {
		cfg.Timestamp = c.ts(i)
		if k == len(writes)-1 && decorate != nil {
			if err := decorate(cfg); err != nil {
				return "", err
			}
		}
		if at, err = c.add(i, at, cfg); err != nil {
			return "", err
		}
	}
	return at, nil
}

// condensation writes a Condensation: with a summary and an offset, a
// compaction of the view it leaves; without, an omit of what it
// forgot. Either carries the event's other members.
func (c *conv) condensation(i int) error {
	cd, _ := c.c.Events[i].Condensation()
	c.viewApply(i)
	view, why := c.enforce()
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	ids, bad := cd.Forgotten()
	if bad > 0 {
		c.at(agentimport.Repaired, "openhands/bad-forgotten-id", i, "%d forgotten event IDs are not strings and forget nothing", bad)
	}
	members, err := rest(cd, "summary")
	if err != nil {
		return err
	}
	decorate := func(e agentsession.Entry) error { return c.keepAll(i, e, members) }
	_, hasOffset := cd.Offset()
	pos := -1
	for k, v := range view {
		if v.idx < 0 && v.summaryOf == i {
			pos = k
		}
	}
	if cd.Summary != nil && hasOffset && pos >= 0 {
		return c.compaction(i, at, cd, view, pos, why, members)
	}
	switch {
	case cd.Summary != nil:
		c.at(agentimport.Lossy, "openhands/condensation", i, "a condensation with a summary but no offset, which OpenHands does not insert: it forgets %d events, and an omit config entry leaves out their items; the summary is kept in %s:event", len(ids), Source)
		members["summary"], _ = json.Marshal(*cd.Summary)
	default:
		c.at(agentimport.Lossy, "openhands/condensation", i, "a condensation with no summary forgets %d events; an omit config entry leaves out their items", len(ids))
	}
	id, err := c.reconcileTo(i, at, view, why, decorate)
	if err != nil {
		return err
	}
	if id == at {
		// Nothing it forgot was in the context: the event is a record.
		c.at(agentimport.Kept, "openhands/condensation-noop", i, "the condensation forgets nothing the context holds; it is a custom entry")
		return c.customEvent(i, at, "")
	}
	c.setTail(i, id)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// compaction writes a condensation with a summary as a compaction: its
// summary the condensation's, the events OpenHands kept ahead of the
// summary pinned, first_kept the first entry of the events it kept
// after it, and an omit in its settings for what lies between that it
// did not keep.
func (c *conv) compaction(i int, at string, cd *openhands.Condensation, view []vEvent, pos int, why map[int]string, members map[string]json.RawMessage) error {
	var parts openresponses.Contents
	var pinned openresponses.Items
	pinnedSet := map[string]bool{}
	restated := 0
	summaryText := func(j int) string {
		s, _ := c.c.Events[j].Condensation()
		if s == nil || s.Summary == nil {
			return ""
		}
		return *s.Summary
	}
	for _, v := range view[:pos] {
		if v.idx < 0 {
			parts = append(parts, &openresponses.InputText{Text: summaryText(v.summaryOf)})
			restated++
			continue
		}
		for _, id := range c.items[v.idx] {
			e, ok := c.b.S.Entry(id)
			if !ok {
				continue
			}
			if ie, ok := e.(*agentsession.ItemEntry); ok {
				pinned = append(pinned, ie.Item)
				pinnedSet[id] = true
			}
		}
	}
	parts = append(parts, &openresponses.InputText{Text: *cd.Summary})
	keptAfter := map[string]bool{}
	for _, v := range view[pos+1:] {
		if v.idx < 0 {
			parts = append(parts, &openresponses.InputText{Text: summaryText(v.summaryOf)})
			restated++
			continue
		}
		for _, id := range c.items[v.idx] {
			keptAfter[id] = true
		}
	}
	path := c.b.S.Path(at)
	first := -1
	for k, e := range path {
		if keptAfter[e.Base().ID] {
			first = k
			break
		}
	}
	var firstKept string
	var omit []string
	if first >= 0 {
		firstKept = path[first].Base().ID
		for _, e := range path[first:] {
			if ie, ok := e.(*agentsession.ItemEntry); ok && !keptAfter[ie.ID] {
				omit = append(omit, ie.ID)
			}
		}
	} else {
		var err error
		if firstKept, at, err = c.keepNone(i, at); err != nil {
			return err
		}
	}
	ctx, err := c.b.S.ContextAt(at)
	if err != nil {
		return err
	}
	settings := ctx.Settings
	settings.Omit.Items = omit
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: openresponses.UserMessage(parts...), Pinned: pinned, Config: settings}
	e.Timestamp = c.ts(i)
	if err := c.joinCompaction(i, e, cd.LLMResponseID); err != nil {
		return err
	}
	if err := c.keepAll(i, e, members); err != nil {
		return err
	}
	id, err := c.add(i, at, e)
	if err != nil {
		return err
	}
	c.pinnedOf[id] = pinnedSet
	ids, _ := cd.Forgotten()
	c.at(agentimport.Lossy, "openhands/condensation", i, "a condensation with a summary forgets %d events: a compaction whose summary is the condensation's, %d items pinned from the events kept ahead of it, and %d items left out by omit among those kept after it", len(ids), len(pinned), len(omit))
	if len(pinned) > 0 {
		c.at(agentimport.Gap, "agentimport/fold-head", i, "OpenHands puts the summary after the %d items it keeps at the head of the context; a compaction puts its summary first, so the head is pinned after it", len(pinned))
	}
	if restated > 0 && !c.tree {
		c.at(agentimport.Lossy, "openhands/version-guess", i, "an earlier summary is still in the view; OpenHands from 1.12 sends it, as converted, and releases before 1.12 sent the last condensation's summary alone, which the file cannot tell apart")
	}
	if restated > 0 {
		c.at(agentimport.Lossy, "openhands/summary-restated", i, "%d earlier summaries OpenHands still sends are restated in this compaction's summary, as only the last compaction contributes; they move to its place", restated)
	}
	gone := make([]int, 0, len(why))
	for j := range why {
		gone = append(gone, j)
	}
	sort.Ints(gone)
	for _, j := range gone {
		if c.items[j] != nil {
			c.at(agentimport.Lossy, "openhands/view-enforced", j, "OpenHands leaves this event out of the context from the condensation at event %d: %s", i+1, why[j])
		}
	}
	c.setTail(i, id)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// keepNone returns the entry a compaction that keeps nothing after its
// summary names as first_kept: at when it puts nothing in context, else
// a custom marker written for it, and the entry the compaction follows.
func (c *conv) keepNone(i int, at string) (string, string, error) {
	if e, ok := c.b.S.Entry(at); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return at, at, nil
		}
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: json.RawMessage(`{"keeps_nothing":true}`)}
	marker.Timestamp = c.ts(i)
	id, err := c.add(i, at, marker)
	if err != nil {
		return "", "", err
	}
	c.at(agentimport.Kept, "openhands/compaction-marker", i, "the condensation keeps nothing after its summary, so a custom marker written before the compaction is its first_kept, which puts nothing in context")
	return id, id, nil
}

// sortedKeys returns a set's keys, sorted.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
