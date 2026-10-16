package convert

import (
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentsession"
)

// The children of a session: Cline's spawn_agent waits for its
// sub-agent and returns its result, so the sub-agent is a subsession of
// the one call matched to it, linked before the call's output; a team
// task outlives the team_run_task call that queued it, or ran it as one
// of several, so it is a peer of each call matched to it, linked after
// the call's output.

// linkBefore converts the sub-agent call p spawned, and writes its
// subsession link before the call's output, returning the child's leaf,
// which the output names in parents.
func (sc *sconv) linkBefore(k int, p *call, ts time.Time) ([]agentsession.EntryRef, error) {
	fam := sc.fam
	ci, ok := fam.sub[callKey{sc.h.i, p.native}]
	if !ok {
		return nil, nil
	}
	ch := fam.hists[ci]
	if !sc.mayConvert(k, ch) {
		return nil, nil
	}
	sid := agentsession.SubsessionID(sc.tgt.id, p.id)
	if fam.ids[sid] {
		return nil, nil
	}
	fam.ids[sid] = true
	tgt := target{id: sid, native: ch.native, parent: sc.tgt.id, spawnedBy: p.id, system: fam.systemOf(sc.h.i, p.native)}
	csc, err := fam.convert(ch, tgt, sc.depth+1)
	if err != nil || csc == nil {
		return nil, err
	}
	sc.children = append(sc.children, csc)
	link := agentsession.NewSubsessionLink(sid, p.id)
	link.Timestamp = ts
	id, err := sc.appendEntry(link)
	if err != nil {
		return nil, err
	}
	sc.written(k, id)
	how := "its first message is the call's task"
	if ch.row != nil && ch.row.ParentAgentID != "" {
		how += ", among the calls of the agent the index names as its parent"
	}
	sc.at(agentimport.Lossy, "clinesdk/subagent-matched", k, "Cline records no call ID for a sub-agent: %s is matched to call %s (%s) because %s; it is a subsession, linked before the output", ch.f.Name, p.id, p.name, how)
	return []agentsession.EntryRef{{Session: sid, Entry: csc.b.Resume()}}, nil
}

// mayConvert reports whether a child can be converted from this
// history's call, reporting why not.
func (sc *sconv) mayConvert(k int, ch *hist) bool {
	switch {
	case sc.fam.opts.Alone:
		return false
	case ch.active || ch.done:
		sc.at(agentimport.Dropped, "clinesdk/subagent-cycle", k, "%s is being converted or was converted already; it is not converted again", ch.f.Name)
		return false
	case sc.depth+1 >= maxDepth:
		ch.why = "it is nested past " + itoa(maxDepth) + " sessions"
		return false
	}
	return true
}

// linkAfter writes, after a call's output, a peer link to each team
// task the call ran, converting it first when no call converted it.
func (sc *sconv) linkAfter(k int, p *call, ts time.Time) error {
	fam := sc.fam
	for _, ci := range fam.peers[callKey{sc.h.i, p.native}] {
		ch := fam.hists[ci]
		if ch.sc == nil {
			if ch.failed || !sc.mayConvert(k, ch) {
				continue
			}
			tgt := fam.ownID(ch.native)
			tgt.parent = sc.tgt.id
			csc, err := fam.convert(ch, tgt, sc.depth+1)
			if err != nil {
				return err
			}
			if csc == nil {
				continue
			}
			sc.children = append(sc.children, csc)
		}
		link := agentsession.NewPeerLink(p.id, agentsession.Callee{Session: ch.sc.tgt.id})
		link.Timestamp = ts
		id, err := sc.appendEntry(link)
		if err != nil {
			return err
		}
		sc.written(k, id)
		sc.at(agentimport.Lossy, "clinesdk/teammate-peer", k, "team task %s outlives call %s (%s), whose output is the dispatch's acknowledgement or the run's text; matched by its agent and task, it is a peer, linked after the output", ch.f.Name, p.id, p.name)
	}
	return nil
}

// systemOf is the system prompt a spawn_agent call gave its sub-agent.
func (fam *family) systemOf(hi int, native string) string {
	for _, c := range fam.hists[hi].calls {
		if c.native == native {
			return c.system
		}
	}
	return ""
}

// rootTarget is the session the directory's own history becomes: its
// native ID, or, for a classic task the VS Code extension carried on,
// an ID derived from the task's, which the cline source's session has.
func (fam *family) rootTarget() target {
	native := fam.rootID()
	if fam.s.Messages != nil && legacyAt(fam.s.Messages.Messages) >= 0 {
		id := agentsession.SubsessionID(native, Source)
		for n := 2; fam.ids[id]; n++ {
			id = agentsession.SubsessionID(native, Source+"/"+itoa(n))
		}
		fam.ids[id] = true
		return target{id: id, native: native, derived: "the VS Code extension carried a classic task on under the task's ID, which the cline source's session of the task has; this one's ID is derived from it, as SubsessionID(task ID, \"" + Source + "\")"}
	}
	return fam.ownID(native)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// teamCopy reports a team task whose history starts with an earlier
// task's conversation.
func (sc *sconv) teamCopy() {
	if sc.copyWhy == "team" {
		sc.addf(agentimport.Lossy, "clinesdk/teammate-continued", "", 0, "the team task continued its teammate's conversation: its first %d messages are a copy of an earlier task's, converted as its own entries, their usage kept off the responses", sc.copyEnd)
	}
}
