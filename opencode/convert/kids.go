package convert

import (
	"encoding/json"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentsession"
)

// kids are a session's sub-agents: the child sessions its task (V1) or
// subagent (V2) calls ran, by the calls that name each.
type kids struct {
	// child maps a call's native ID to the child session it names.
	child map[string]string
	// calls lists each child's calls, in the order the walk meets them;
	// background marks the calls that returned before the child ended.
	calls      map[string][]string
	background map[string]bool
	// sc holds each child's conversion; failed the children that could
	// not be converted.
	sc     map[string]*sconv
	failed map[string]bool
	// reported marks the calls whose child was reported missing.
	reported map[string]bool
}

// findKids reads which calls ran which child sessions: a V1 task part's
// state metadata names its child as sessionId, a V2 subagent tool's as
// sessionID, and so does the input of a call that continues one. A
// child is a subsession of the one foreground call that ran it, and a
// peer of every call when a call ran it in the background or several
// calls ran it: its work outlived a call.
func (sc *sconv) findKids() *kids {
	k := &kids{child: map[string]string{}, calls: map[string][]string{}, background: map[string]bool{}, sc: map[string]*sconv{}, failed: map[string]bool{}, reported: map[string]bool{}}
	add := func(callID, child string, background bool) {
		if callID == "" || child == "" {
			return
		}
		if _, ok := k.child[callID]; ok {
			return
		}
		k.child[callID] = child
		k.calls[child] = append(k.calls[child], callID)
		k.background[callID] = background
	}
	if sc.rec.v1 != nil {
		for _, m := range sc.msgs {
			for _, p := range m.Parts {
				pt := p.Part
				if pt.Type != opencode.PartTool || pt.Tool != "task" || pt.State == nil {
					continue
				}
				t := pt.State.Task()
				if t.SessionID == "" {
					t.SessionID = inputString(pt.State.Input, "task_id")
				}
				bg := t.Background || inputBool(pt.State.Input, "background")
				add(pt.CallID, t.SessionID, bg)
			}
		}
	}
	for _, r := range sc.v2Rows() {
		if r.Err != nil || r.Type != opencode.TypeAssistant {
			continue
		}
		for _, raw := range r.Data.Content {
			var c opencode.Content2
			if c.UnmarshalJSON(raw) != nil || c.Type != opencode.ContentTool || c.Name != "subagent" {
				continue
			}
			st, err := c.Tool()
			if err != nil {
				continue
			}
			t := st.Task()
			if t.SessionID == "" {
				t.SessionID = inputString(st.Input, "sessionID")
			}
			bg := t.Background || inputBool(st.Input, "background")
			add(c.ID, t.SessionID, bg)
		}
	}
	return k
}

// v2Rows are the V2 rows the walk converts: those OpenCode 2 wrote
// itself.
func (sc *sconv) v2Rows() []*opencode.Row2 {
	if sc.rec.v2 == nil {
		return nil
	}
	return sc.rows[sc.native:]
}

func inputString(raw json.RawMessage, name string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(m[name], &s)
	return s
}

func inputBool(raw json.RawMessage, name string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	var b bool
	_ = json.Unmarshal(m[name], &b)
	return b
}

// peer reports whether a child outlives its calls.
func (k *kids) peer(child string) bool {
	calls := k.calls[child]
	if len(calls) > 1 {
		return true
	}
	for _, c := range calls {
		if k.background[c] {
			return true
		}
	}
	return false
}

// childOf returns the child session a call ran, when it is one this
// conversion converts: a session of the stores whose parent is this
// one. A call naming another is reported once.
func (sc *sconv) childOf(native, unit string) (*record, bool) {
	id, ok := sc.kids.child[native]
	if !ok {
		return nil, false
	}
	rec := sc.fam.recs[id]
	first := !sc.kids.reported[native]
	sc.kids.reported[native] = true
	switch {
	case rec == nil:
		if !first {
			return nil, false
		}
		sc.addf(agentimport.Lossy, "opencode/subagent-missing", unit, "the call ran sub-agent session %s, which the store does not hold", id)
		return nil, false
	case rec.parentID() != sc.rec.id:
		if !first {
			return nil, false
		}
		sc.addf(agentimport.Lossy, "opencode/subagent-missing", unit, "the call names session %s, whose parent is not this session; it is not linked", id)
		return nil, false
	case sc.fam.opts.Alone, sc.depth+1 >= maxDepth:
		return nil, false
	case sc.kids.failed[id]:
		return nil, false
	}
	return rec, true
}

// linkBefore converts the child a foreground call ran, when that call
// alone ran it, and writes its subsession link before the call's output,
// returning the child's leaf, which the output names in parents.
func (sc *sconv) linkBefore(native, callID, unit string, ts time.Time) ([]agentsession.EntryRef, error) {
	rec, ok := sc.childOf(native, unit)
	if !ok || sc.kids.peer(rec.id) {
		return nil, nil
	}
	if sc.fam.active[rec.id] || sc.fam.done[rec.id] {
		sc.addf(agentimport.Dropped, "opencode/subagent-cycle", unit, "sub-agent session %s is being converted or was converted already; it is not converted again", rec.id)
		return nil, nil
	}
	sid := agentsession.SubsessionID(sc.tgt.id, callID)
	if sc.fam.ids[sid] {
		return nil, nil
	}
	sc.fam.ids[sid] = true
	ch := sc.convertChild(rec, target{id: sid, parent: sc.tgt.id, spawnedBy: callID})
	if ch == nil {
		return nil, nil
	}
	link := agentsession.NewSubsessionLink(sid, callID)
	link.Timestamp = ts
	if _, err := sc.append(link); err != nil {
		return nil, err
	}
	sc.addf(agentimport.Kept, "opencode/subagent", unit, "the call ran sub-agent session %s in the foreground and alone; it is a subsession, linked before the call's output", rec.id)
	return []agentsession.EntryRef{{Session: sid, Entry: ch.b.Resume()}}, nil
}

// linkAfter writes, after a call's output, a peer link to the child the
// call ran when the child outlives it: a background run, or one of
// several calls that ran the child. The child is converted once, under
// its own ID, naming this session in parent_session alone.
func (sc *sconv) linkAfter(native, callID, unit string, ts time.Time) error {
	rec, ok := sc.childOf(native, unit)
	if !ok || !sc.kids.peer(rec.id) {
		return nil
	}
	ch := sc.kids.sc[rec.id]
	if ch == nil {
		if sc.fam.active[rec.id] || sc.fam.done[rec.id] {
			sc.addf(agentimport.Dropped, "opencode/subagent-cycle", unit, "sub-agent session %s is being converted or was converted already; it is not converted again", rec.id)
			return nil
		}
		tgt := sc.fam.ownTarget(rec)
		tgt.parent, tgt.peer = sc.tgt.id, true
		if ch = sc.convertChild(rec, tgt); ch == nil {
			return nil
		}
	}
	link := agentsession.NewPeerLink(callID, agentsession.Callee{Session: ch.tgt.id})
	link.Timestamp = ts
	if _, err := sc.append(link); err != nil {
		return err
	}
	why := "several calls ran it"
	if sc.kids.background[native] {
		why = "the call ran it in the background"
	}
	sc.addf(agentimport.Lossy, "opencode/subagent-peer", unit, "sub-agent session %s outlives the call (%s); it is a peer, linked after the call's output, which is OpenCode's acknowledgement", rec.id, why)
	return nil
}

// convertChild converts a child session into the session tgt names; nil
// when it could not be, which is reported.
func (sc *sconv) convertChild(rec *record, tgt target) *sconv {
	ch := sc.fam.convertInto(rec, tgt, sc.depth+1, sc.report)
	if ch == nil {
		sc.kids.failed[rec.id] = true
		return nil
	}
	sc.kids.sc[rec.id] = ch
	sc.children = append(sc.children, ch)
	return ch
}

// unlinkedKids converts the child sessions no call of this session
// linked: their parent is this session, and no call ran them, or the
// call that did is on no path converted.
func (sc *sconv) unlinkedKids() error {
	for _, id := range sc.fam.children[sc.rec.id] {
		if sc.fam.done[id] || sc.kids.failed[id] {
			continue
		}
		rec := sc.fam.recs[id]
		if sc.fam.opts.Alone || sc.depth+1 >= maxDepth {
			continue
		}
		tgt := sc.fam.ownTarget(rec)
		tgt.parent = sc.tgt.id
		if sc.convertChild(rec, tgt) == nil {
			continue
		}
		why := "no call of the parent names it"
		if len(sc.kids.calls[id]) > 0 {
			why = "the call that ran it has no output in the parent"
		}
		sc.addf(agentimport.Lossy, "opencode/subagent-unlinked", rec.primary().Unit, "the session is a child of %s, linked to no call (%s); its session names the parent in parent_session alone", sc.rec.id, why)
	}
	return nil
}

// convertInto converts a session into tgt as a child, its tally and
// findings merged into into rather than the family's report.
func (fam *family) convertInto(rec *record, tgt target, depth int, into *agentimport.Report) *sconv {
	fam.done[rec.id] = true
	fam.active[rec.id] = true
	defer delete(fam.active, rec.id)
	sc := newSconv(fam, rec, tgt, depth)
	sc.into = into
	if err := sc.run(); err != nil {
		sc.report.Addf(agentimport.Dropped, "opencode/session-failed", rec.primary().Unit, 0, "the session could not be converted: %s", agentimport.ErrorText(err))
		sc.abandon()
		return nil
	}
	read, err := agentimport.Finish(sc.b.S)
	if err != nil {
		sc.report = agentimport.NewReport(Source)
		sc.report.Addf(agentimport.Dropped, "opencode/session-failed", rec.primary().Unit, 0, "the converted session does not verify: %s", agentimport.ErrorText(err))
		sc.abandon()
		return nil
	}
	sc.out = read
	sc.tally()
	return sc
}
