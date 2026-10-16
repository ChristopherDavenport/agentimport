package convert

import (
	"fmt"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
)

// kids are a session's sub-agents, by the delegate_task calls that ran
// them. Hermes records neither side of the tie: the call's output does
// not name the child, and the child's row names only its parent. A
// child is a call's when it started between the call and its output, as
// Hermes creates a child's session while the call runs; when one row
// made several delegate_task calls, a child is the call's whose goal
// its first message holds, as Hermes sends a child its goal as that
// message.
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
	row      *hermes.Message
	output   *hermes.Message
	callID   string
	goals    []string
	children []*record
	// background says the output is the handle of a background run,
	// and delegationID its delegation.
	background   bool
	delegationID string
}

// findKids binds the session's sub-agents to the calls that ran them.
func (sc *sconv) findKids(msgs []*hermes.Message) *kids {
	k := &kids{calls: map[callKey]*delegateCall{}, current: map[string]*delegateCall{}, bound: map[*record]bool{}, failed: map[*record]bool{}}
	// open maps an alias of a delegate_task call of the current
	// assistant turn to the call, as Hermes pairs a result.
	open := map[string]*delegateCall{}
	var byRow [][]*delegateCall
	for _, m := range msgs {
		if m.Err != nil {
			continue
		}
		switch m.Role {
		case hermes.RoleAssistant:
			open = map[string]*delegateCall{}
			var row []*delegateCall
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
				row = append(row, dc)
			}
			if len(row) > 0 {
				byRow = append(byRow, row)
			}
		case hermes.RoleUser:
			open = map[string]*delegateCall{}
		case hermes.RoleTool:
			for _, a := range hermes.ResultAliases(m.ToolCallID) {
				if dc := open[a]; dc != nil && dc.output == nil {
					dc.output = m
					dc.delegationID, dc.background = delegation(m.Content.String())
					break
				}
			}
		}
	}
	var children []*record
	for _, id := range sc.fam.children[sc.rec.s.ID] {
		rec := sc.fam.recs[id]
		if rec.rel == hermes.Delegate || rec.rel == hermes.Tool {
			children = append(children, rec)
		}
	}
	for _, row := range byRow {
		for _, child := range children {
			if k.bound[child] {
				continue
			}
			var in []*delegateCall
			for _, dc := range row {
				if dc.window(child) {
					in = append(in, dc)
				}
			}
			switch {
			case len(in) == 0:
			case len(row) == 1:
				in[0].children = append(in[0].children, child)
				k.bound[child] = true
			default:
				text := sc.firstUserText(child)
				for _, dc := range in {
					if dc.holdsGoal(text) {
						dc.children = append(dc.children, child)
						k.bound[child] = true
						break
					}
				}
			}
		}
	}
	return k
}

// window reports whether a child started while the call ran: after the
// call's row and no later than its output, or at any later time when
// the call has none.
func (dc *delegateCall) window(child *record) bool {
	start := child.s.Started
	if start.IsZero() || dc.row.Time.IsZero() || start.Before(dc.row.Time) {
		return false
	}
	return dc.output == nil || dc.output.Time.IsZero() || !start.After(dc.output.Time)
}

// holdsGoal reports whether a child's first message holds one of the
// call's goals.
func (dc *delegateCall) holdsGoal(text string) bool {
	for _, g := range dc.goals {
		if g != "" && strings.Contains(text, g) {
			return true
		}
	}
	return false
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
func (sc *sconv) usable(dc *delegateCall, unit string) []*record {
	var out []*record
	for _, rec := range dc.children {
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
	if dc == nil || dc.output != out || dc.background {
		return nil, nil
	}
	recs := sc.usable(dc, out.Unit)
	var refs []agentsession.EntryRef
	for _, rec := range recs {
		var tgt target
		if len(dc.children) == 1 {
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
		if len(dc.children) > 1 {
			how = fmt.Sprintf("one of the %d sub-agents the call ran; it keeps its own session ID", len(dc.children))
		}
		sc.addf(agentimport.Lossy, "hermes/subagent", out.Unit, "the call ran sub-agent session %s in the foreground (%s): a subsession, linked before the call's output, tied to the call by when it started, as Hermes records no tie", rec.s.ID, how)
	}
	return refs, nil
}

// linkAfter writes, after a call's output, a peer link to each child a
// background call ran: the output is Hermes' handle of the run, and the
// child's answer comes later, as a user message of its own.
func (sc *sconv) linkAfter(callID string, out *hermes.Message) error {
	dc := sc.kids.current[callID]
	if dc == nil || dc.output != out || !dc.background {
		return nil
	}
	for _, rec := range sc.usable(dc, out.Unit) {
		tgt := sc.fam.ownTarget(rec)
		tgt.parent = sc.tgt.id
		ch := sc.convertChild(rec, tgt)
		if ch == nil {
			continue
		}
		link := agentsession.NewPeerLink(callID, agentsession.Callee{Session: tgt.id})
		link.Timestamp = out.Time
		if err := sc.b.Member(link, "delegation_id", dc.delegationID); err != nil {
			return err
		}
		if d := sc.fam.delegations[dc.delegationID]; d != nil {
			if err := sc.b.Member(link, "delegation_state", d.State); err != nil {
				return err
			}
		}
		if _, err := sc.append(link); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "hermes/subagent-peer", out.Unit, "the call ran sub-agent session %s in the background: a peer, linked after the call's output, which is Hermes' handle of the run; tied to the call by when it started", rec.s.ID)
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
