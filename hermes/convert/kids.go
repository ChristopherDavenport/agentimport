package convert

import (
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
)

// kids are a session's sub-agents, by the delegate_task calls that ran
// them. Hermes records neither side of the tie: the call's output does
// not name the child, and the child's row names only its parent. The
// tie is a heuristic, reported as one: a child is a call's when it
// started while the call's run was open, after the call's row and no
// later than its end; and when several calls' runs were open, the call
// whose goals its first message holds, as Hermes sends a child its goal
// as that message, else the call made last before it started. A run
// ends at the call's output for a call that waited, at the delivery of
// its result (or the time async_delegations records it completed) for a
// background run, and at the next turn for a call with no output.
type kids struct {
	// calls are the delegate_task calls, by row and pairing ID;
	// current each one the walk wrote, by the call ID it gave it.
	calls   map[callKey]*delegateCall
	current map[string]*delegateCall
	// bound marks the children a call ran; failed those that could not
	// be converted.
	bound  map[*record]bool
	failed map[*record]bool
}

type callKey struct {
	row    int64
	native string
}

// delegateCall is one delegate_task call and what it ran.
type delegateCall struct {
	row    *hermes.Message
	output *hermes.Message
	callID string
	goals  []string
	// end is when the call's run ended; zero when nothing says.
	end time.Time
	// h is what the output says of the run.
	h        handle
	children []*record
	// unit names the delegation that ran each child of a background
	// call, and inline the children of tasks it ran inline.
	unit   map[*record]string
	inline map[*record]bool
}

// findKids binds the session's sub-agents to the calls that ran them.
func (sc *sconv) findKids(msgs []*hermes.Message) *kids {
	k := &kids{calls: map[callKey]*delegateCall{}, current: map[string]*delegateCall{}, bound: map[*record]bool{}, failed: map[*record]bool{}}
	// open maps an alias of a delegate_task call of the current
	// assistant turn to the call, as Hermes pairs a result; pending
	// lists the calls whose run has no end yet.
	open := map[string]*delegateCall{}
	var all, pending []*delegateCall
	delivered := map[string]time.Time{}
	turn := func(at time.Time) {
		for _, dc := range pending {
			if dc.output == nil && !at.IsZero() {
				dc.end = at
			}
		}
		pending = nil
		open = map[string]*delegateCall{}
	}
	for _, m := range msgs {
		if m.Err != nil {
			continue
		}
		switch m.Role {
		case hermes.RoleAssistant:
			turn(m.Time)
			for _, c := range m.ToolCalls {
				if c.Name() != "delegate_task" {
					continue
				}
				dc := &delegateCall{row: m, goals: goals(c.ArgumentsText())}
				k.calls[callKey{m.ID, c.PairingID()}] = dc
				for _, a := range c.Aliases() {
					if open[a] == nil {
						open[a] = dc
					}
				}
				all = append(all, dc)
				pending = append(pending, dc)
			}
		case hermes.RoleUser:
			if id := delegationID(m); id != "" && !m.Time.IsZero() && m.Time.After(delivered[id]) {
				delivered[id] = m.Time
			}
			turn(m.Time)
		case hermes.RoleTool:
			for _, a := range hermes.ResultAliases(m.ToolCallID) {
				if dc := open[a]; dc != nil && dc.output == nil {
					dc.output = m
					dc.h = readHandle(m.Content.String())
					dc.end = m.Time
					break
				}
			}
		}
	}
	for _, dc := range all {
		if !dc.h.background {
			continue
		}
		// A background run ends when its last unit's result arrives,
		// or async_delegations says it completed; until then, it is
		// open.
		dc.end = time.Time{}
		if len(dc.h.goals) > 0 {
			dc.goals = dc.h.goals
		}
		for _, id := range dc.h.ids() {
			t := delivered[id]
			if d := sc.fam.delegations[id]; d != nil && t.IsZero() {
				if f, ok := d.Cols.Float("completed_at"); ok {
					t, _ = hermes.Epoch(f)
				}
			}
			if t.IsZero() {
				dc.end = time.Time{}
				break
			}
			if t.After(dc.end) {
				dc.end = t
			}
		}
	}
	for _, id := range sc.fam.children[sc.rec.s.ID] {
		rec := sc.fam.recs[id]
		if rec.rel != hermes.Delegate || rec.s.Started.IsZero() {
			continue
		}
		dc := sc.bind(rec, all)
		if dc == nil {
			continue
		}
		dc.children = append(dc.children, rec)
		k.bound[rec] = true
		if dc.h.background {
			if dc.unit == nil {
				dc.unit, dc.inline = map[*record]string{}, map[*record]bool{}
			}
			i := dc.goalIndex(sc.firstUserText(rec))
			dc.unit[rec] = dc.h.unitOf(i)
			dc.inline[rec] = i >= 0 && dc.h.inline[i]
		}
	}
	return k
}

// bind chooses the call that ran a child, nil when none can be told.
func (sc *sconv) bind(child *record, calls []*delegateCall) *delegateCall {
	var in []*delegateCall
	for _, dc := range calls {
		if dc.window(child) {
			in = append(in, dc)
		}
	}
	if len(in) == 0 {
		return nil
	}
	text := sc.firstUserText(child)
	var held []*delegateCall
	for _, dc := range in {
		if dc.goalIndex(text) >= 0 {
			held = append(held, dc)
		}
	}
	pool := in
	if len(held) > 0 {
		pool = held
	}
	// The call made last before the child started; calls of one row
	// that its goals do not tell apart cannot be told apart.
	var best *delegateCall
	tie := false
	for _, dc := range pool {
		switch {
		case best == nil || dc.row.Time.After(best.row.Time):
			best, tie = dc, false
		case dc.row.Time.Equal(best.row.Time):
			tie = true
		}
	}
	if tie {
		return nil
	}
	return best
}

// window reports whether a child started while the call's run was
// open: after the call's row and no later than the run's end, when it
// has one.
func (dc *delegateCall) window(child *record) bool {
	start := child.s.Started
	if dc.row.Time.IsZero() || start.Before(dc.row.Time) {
		return false
	}
	return dc.end.IsZero() || !start.After(dc.end)
}

// goalIndex is the index of the first of the call's goals a child's
// first message holds, -1 for none.
func (dc *delegateCall) goalIndex(text string) int {
	for i, g := range dc.goals {
		if g = strings.TrimSpace(g); g != "" && strings.Contains(text, g) {
			return i
		}
	}
	return -1
}

// firstUserText is the text of a session's first user row.
func (sc *sconv) firstUserText(rec *record) string {
	msgs, err := sc.fam.messages(rec)
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		if m.Err == nil && m.Role == hermes.RoleUser {
			return m.Content.String()
		}
	}
	return ""
}

// called records that the walk wrote a delegate_task call.
func (k *kids) called(c *hermes.ToolCall, callID string, row *hermes.Message) {
	dc := k.calls[callKey{row.ID, c.PairingID()}]
	if dc == nil {
		return
	}
	dc.callID = callID
	k.current[callID] = dc
}

// usable lists the children of a call this conversion converts now.
func (sc *sconv) usable(dc *delegateCall, recs []*record, unit string) []*record {
	var out []*record
	for _, rec := range recs {
		switch {
		case sc.fam.opts.Alone, sc.depth+1 >= maxDepth, sc.kids.failed[rec]:
			continue
		case sc.fam.active[rec.s.ID] || sc.fam.done[rec.s.ID]:
			sc.addf(agentimport.Dropped, "hermes/subagent-cycle", unit, "sub-agent session %s is being converted or was converted already; it is not converted again", rec.s.ID)
			continue
		}
		out = append(out, rec)
	}
	return out
}

// linkBefore converts the children a call the walk is answering ran in
// the foreground, and writes a subsession link to each before the
// call's output, returning their leaves, which the output names in
// parents.
func (sc *sconv) linkBefore(callID string, out *hermes.Message) ([]agentsession.EntryRef, error) {
	dc := sc.kids.current[callID]
	if dc == nil || dc.output != out {
		return nil, nil
	}
	var waited []*record
	for _, rec := range dc.children {
		if !dc.h.background || dc.inline[rec] {
			waited = append(waited, rec)
		}
	}
	recs := sc.usable(dc, waited, out.Unit)
	var refs []agentsession.EntryRef
	for _, rec := range recs {
		var tgt target
		if len(waited) == 1 && len(dc.children) == 1 {
			sid := agentsession.SubsessionID(sc.tgt.id, callID)
			if sc.fam.ids[sid] {
				continue
			}
			sc.fam.ids[sid] = true
			tgt = target{id: sid}
		} else {
			tgt = sc.fam.ownTarget(rec)
		}
		tgt.parent, tgt.spawnedBy = sc.tgt.id, callID
		ch := sc.convertChild(rec, tgt)
		if ch == nil {
			continue
		}
		link := agentsession.NewSubsessionLink(tgt.id, callID)
		link.Timestamp = out.Time
		if _, err := sc.append(link); err != nil {
			return nil, err
		}
		refs = append(refs, agentsession.EntryRef{Session: tgt.id, Entry: ch.b.Resume()})
		how := "the call's only sub-agent; its session ID is derived from the call's"
		if dc.h.background {
			how = "a task its background run ran inline, before the call returned; it keeps its own session ID"
		} else if len(dc.children) > 1 {
			how = fmt.Sprintf("one of the %d sub-agents the call ran; it keeps its own session ID", len(dc.children))
		}
		sc.addf(agentimport.Lossy, "hermes/subagent", out.Unit, "the call ran sub-agent session %s (%s): a subsession, linked before the call's output, tied to the call by when it started and the goal it was sent, a heuristic, as Hermes records no tie", rec.s.ID, how)
	}
	return refs, nil
}

// linkAfter writes, after a call's output, a peer link to each child a
// background call ran: the output is Hermes' handle of the run, and the
// child's answer comes later, as a user message of its own.
func (sc *sconv) linkAfter(callID string, out *hermes.Message) error {
	dc := sc.kids.current[callID]
	if dc == nil || dc.output != out || !dc.h.background {
		return nil
	}
	var later []*record
	for _, rec := range dc.children {
		if !dc.inline[rec] {
			later = append(later, rec)
		}
	}
	for _, rec := range sc.usable(dc, later, out.Unit) {
		tgt := sc.fam.ownTarget(rec)
		tgt.parent = sc.tgt.id
		ch := sc.convertChild(rec, tgt)
		if ch == nil {
			continue
		}
		link := agentsession.NewPeerLink(callID, agentsession.Callee{Session: tgt.id})
		link.Timestamp = out.Time
		did := dc.unit[rec]
		if err := sc.b.Member(link, "delegation_id", did); err != nil {
			return err
		}
		if d := sc.fam.delegations[did]; d != nil {
			if err := sc.b.Member(link, "delegation_state", d.State); err != nil {
				return err
			}
		}
		if _, err := sc.append(link); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "hermes/subagent-peer", out.Unit, "the call ran sub-agent session %s in the background: a peer, linked after the call's output, which is Hermes' handle of the run; tied to the call by when it started and the goal it was sent, a heuristic, as Hermes records no tie", rec.s.ID)
	}
	return nil
}

// convertChild converts a child session into tgt; nil when it could not
// be, which is reported.
func (sc *sconv) convertChild(rec *record, tgt target) *sconv {
	ch := sc.fam.convertInto(rec, tgt, sc.depth+1, sc.report)
	if ch == nil {
		sc.kids.failed[rec] = true
		return nil
	}
	sc.children = append(sc.children, ch)
	return ch
}

// unlinkedKids converts the sub-agents and tool sessions of this
// session that no call linked: no call's time holds their start, or the
// call that ran them is not on a path converted.
func (sc *sconv) unlinkedKids() error {
	if sc.fam.opts.Alone || sc.depth+1 >= maxDepth {
		return nil
	}
	for _, id := range sc.fam.children[sc.rec.s.ID] {
		rec := sc.fam.recs[id]
		if rec.rel != hermes.Delegate && rec.rel != hermes.Tool {
			continue
		}
		if sc.fam.done[id] || sc.kids.failed[rec] {
			continue
		}
		tgt := sc.fam.ownTarget(rec)
		tgt.parent = sc.tgt.id
		if sc.convertChild(rec, tgt) == nil {
			continue
		}
		why := "no delegate_task call of the parent was running when it started"
		if sc.kids.bound[rec] {
			why = "the call that ran it was not converted"
		}
		if rec.rel == hermes.Tool {
			why = "a tool ran it, and Hermes records which call did nowhere"
		}
		sc.addf(agentimport.Lossy, "hermes/subagent-unlinked", rec.s.Unit, "the session is a %s of %s, linked to no call (%s); its session names the parent in parent_session alone", rec.rel, sc.rec.s.ID, why)
	}
	return nil
}

// continuations converts the sessions a compression of this one
// continued in, each with a continued_in link at the end of this one:
// Hermes ends a session it compresses and carries the conversation on
// in a child session holding the compacted context, which is all a
// resume of the child sends (get_resume_conversations).
func (sc *sconv) continuations() error {
	for _, id := range sc.fam.children[sc.rec.s.ID] {
		rec := sc.fam.recs[id]
		if rec.rel != hermes.Continuation || sc.fam.done[id] {
			continue
		}
		tgt := sc.fam.ownTarget(rec)
		tgt.parent = sc.tgt.id
		ch := sc.fam.convertInto(rec, tgt, sc.depth, sc.report)
		if ch == nil {
			continue
		}
		sc.children = append(sc.children, ch)
		link := agentsession.NewLinkEntry(agentsession.RelContinuedIn, tgt.id)
		if e, ok := sc.b.S.Entry(sc.at); ok {
			link.Timestamp = e.Base().Timestamp
		}
		if !rec.s.Started.IsZero() {
			link.Timestamp = rec.s.Started
		}
		if _, err := sc.append(link); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "hermes/continuation", rec.s.Unit, "a compression ended %s and continued it in session %s, which holds the compacted context a resume sends: a continued_in link, the successor naming it in parent_session", sc.rec.s.ID, rec.s.ID)
	}
	return nil
}
