package convert

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
)

// children are a conversation's sub-agents and the calls each answered.
type children struct {
	list []*child
	// byCall maps a call's native ID to the sub-agents' answers to it.
	byCall map[string][]*answer
}

// child is one sub-agent's conversation under subagents/.
type child struct {
	name string
	dir  string
	real string
	conv *openhands.Conversation
	// answers are the calls the sub-agent answered, in its order.
	answers []*answer
	// why says why the sub-agent is linked to no call, when it is not.
	why string
	cv  *conv
	sid string
	// failed says the conversion failed and was reported.
	failed bool
}

// answer is one call a sub-agent answered: the call's prompt was its
// user message msg. first says it started the sub-agent.
type answer struct {
	child *child
	call  string
	msg   int
	first bool
	n     int
}

// spawn is a parent call that sends a sub-agent a prompt: a task call,
// or a delegate call with one task per sub-agent.
type spawn struct {
	native string
	texts  []string
	start  time.Time
	end    time.Time
	timed  bool
	ended  bool
}

// matchChildren reads the sub-agents under subagents/ and matches each
// to the calls that sent it its prompts: OpenHands records no link, so
// a sub-agent's user message is matched to a task or delegate call by
// its text and by falling between the call and its answer.
func (c *conv) matchChildren() {
	c.kids = &children{byCall: map[string][]*answer{}}
	subs := c.c.Subagents
	if len(subs) == 0 {
		return
	}
	if c.fam.opts.Alone {
		c.addf(agentimport.Dropped, "openhands/subagents-alone", openhands.DirSubagents, 0, "%d sub-agents are not converted: the conversion reads no other conversation", len(subs))
		return
	}
	if c.depth+1 >= maxDepth {
		c.addf(agentimport.Dropped, "openhands/subagent-nesting", openhands.DirSubagents, 0, "%d sub-agents are nested past %d conversations and are not converted", len(subs), maxDepth)
		return
	}
	spawns := c.spawns()
	for _, name := range subs {
		dir := filepath.Join(c.c.Dir, openhands.DirSubagents, name)
		real := realPath(dir)
		file := openhands.DirSubagents + "/" + name
		if c.fam.active[real] || c.fam.heldSet()[real] {
			c.addf(agentimport.Dropped, "openhands/subagent-cycle", file, 0, "the sub-agent's directory is a conversation being converted or converted already; it is not converted again")
			continue
		}
		cv, err := readChild(dir)
		if err != nil {
			c.addf(agentimport.Dropped, "openhands/subagent-unreadable", file, 0, "the sub-agent cannot be read: %s", agentimport.ErrorText(err))
			continue
		}
		ch := &child{name: name, dir: dir, real: real, conv: cv}
		c.kids.list = append(c.kids.list, ch)
		c.matchChild(ch, spawns)
	}
}

// spawns lists the parent's task and delegate calls, with the time of
// each and of its first answer.
func (c *conv) spawns() []*spawn {
	var out []*spawn
	for i, e := range c.c.Events {
		if e.Err != nil || c.info[i].kind != openhands.KindAction || c.info[i].bad != nil {
			continue
		}
		a, _ := e.Action()
		act := openhands.ParseAction(a.Action)
		if act == nil {
			continue
		}
		s := &spawn{native: a.ToolCallID, start: c.info[i].ts, timed: c.info[i].tsOK}
		switch act.Kind {
		case openhands.ActionTask:
			s.texts = []string{act.Prompt}
		case openhands.ActionDelegate:
			if act.Command != "delegate" {
				continue
			}
			for _, k := range sortedKeys(keysOf(act.Tasks)) {
				s.texts = append(s.texts, act.Tasks[k])
			}
		default:
			continue
		}
		for j := i + 1; j < len(c.c.Events); j++ {
			if openhands.IsObservationLike(c.info[j].kind) && c.info[j].toolCallID == s.native {
				s.end, s.ended = c.info[j].ts, c.info[j].tsOK
				break
			}
		}
		out = append(out, s)
	}
	return out
}

func keysOf(m map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// matchChild matches each user message of a sub-agent to the one call
// that sent it. A message no call sent is passed over; one two calls
// could have sent makes the sub-agent unlinked.
func (c *conv) matchChild(ch *child, spawns []*spawn) {
	loc := c.fam.opts.Location
	for k, e := range ch.conv.Events {
		if e.Err != nil || e.Kind != openhands.KindMessage || e.Source != openhands.SourceUser {
			continue
		}
		m, err := e.Message()
		if err != nil || m.LLMMessage == nil {
			continue
		}
		text := textOf(m.LLMMessage.Content)
		ts, _, tsOK := openhands.ParseTime(e.Timestamp, loc)
		var cands []*spawn
		for _, s := range spawns {
			if !slices.Contains(s.texts, text) {
				continue
			}
			if tsOK && s.timed && ts.Before(s.start) {
				continue
			}
			if tsOK && s.ended && ts.After(s.end) {
				continue
			}
			cands = append(cands, s)
		}
		switch len(cands) {
		case 0:
			continue
		case 1:
		default:
			ch.why = fmt.Sprintf("its message %d matches %d calls by text and time", k+1, len(cands))
			ch.answers = nil
			return
		}
		a := &answer{child: ch, call: cands[0].native, msg: k, first: len(ch.answers) == 0}
		ch.answers = append(ch.answers, a)
	}
	if len(ch.answers) == 0 {
		ch.why = "no task or delegate call sent it a prompt it received"
		return
	}
	for _, a := range ch.answers {
		list := c.kids.byCall[a.call]
		if a.first {
			n := 0
			for _, b := range list {
				if b.first {
					n++
				}
			}
			a.n = n
		}
		c.kids.byCall[a.call] = append(list, a)
	}
}

// heldSet is the conversation directories converted so far.
func (fam *family) heldSet() map[string]bool {
	m := make(map[string]bool, len(fam.held))
	for _, h := range fam.held {
		m[h] = true
	}
	return m
}

// convertChild converts a sub-agent into the session tgt names; false
// when it could not be.
func (c *conv) convertChild(ch *child, tgt target) bool {
	file := openhands.DirSubagents + "/" + ch.name
	if c.fam.active[ch.real] {
		c.addf(agentimport.Dropped, "openhands/subagent-cycle", file, 0, "the sub-agent's directory is a conversation being converted; it is not converted again")
		return false
	}
	c.fam.active[ch.real] = true
	sub := agentimport.NewReport(Source)
	sub.Unit = Unit
	cv, err := c.fam.convert(ch.conv, tgt, c.prefix+file+"/", c.depth+1, sub)
	delete(c.fam.active, ch.real)
	if err != nil {
		ch.failed = true
		c.addf(agentimport.Dropped, "openhands/subagent-failed", file, 0, "the sub-agent could not be converted: %s", agentimport.ErrorText(err))
		return false
	}
	c.report.Merge(sub)
	c.fam.held = append(c.fam.held, ch.real)
	c.children = append(c.children, cv)
	ch.cv, ch.sid = cv, tgt.id
	return true
}

// linkChildren converts the sub-agents call cl started and writes their
// subsession links before its output, returning the entries of the
// sub-agents the output came from: each one's last entry before the
// next prompt it was sent, or its leaf.
func (c *conv) linkChildren(i int, at string, cl *call) ([]agentsession.EntryRef, string, error) {
	var refs []agentsession.EntryRef
	for _, a := range c.kids.byCall[cl.native] {
		if !a.first {
			continue
		}
		ch := a.child
		if ch.failed {
			continue
		}
		key := cl.id
		if a.n > 0 {
			key = cl.id + "/" + ch.name
		}
		sid := agentsession.SubsessionID(c.tgt.id, key)
		if c.fam.ids[sid] {
			continue
		}
		c.fam.ids[sid] = true
		if !c.convertChild(ch, target{id: sid, native: nativeID(ch.conv), parent: c.tgt.id, spawnedBy: cl.id}) {
			continue
		}
		link := agentsession.NewSubsessionLink(sid, cl.id)
		link.Timestamp = c.ts(i)
		id, err := c.add(i, at, link)
		if err != nil {
			return nil, "", err
		}
		at = id
		c.at(agentimport.Lossy, "openhands/subagent-match", i, "sub-agent %s is matched to call %s by its prompt and time, as OpenHands records no link; it is a subsession", ch.name, cl.id)
		if ref := c.answerEntry(a); ref != "" {
			refs = append(refs, agentsession.EntryRef{Session: sid, Entry: ref})
		}
	}
	return refs, at, nil
}

// answerEntry is the sub-agent's entry an answer ends on: its last
// before the next prompt it was sent, or its leaf.
func (c *conv) answerEntry(a *answer) string {
	cv := a.child.cv
	k := slices.Index(a.child.answers, a)
	if k+1 < len(a.child.answers) {
		next := a.child.answers[k+1].msg
		for j := next - 1; j >= 0; j-- {
			if t := cv.tail[j]; t != "" {
				return t
			}
		}
		return ""
	}
	return cv.b.Resume()
}

// peerChildren writes, after a call's output, a peer link to each
// sub-agent the call resumed: a sub-agent that answers several calls
// outlives the first.
func (c *conv) peerChildren(i int, at string, cl *call) (string, error) {
	for _, a := range c.kids.byCall[cl.native] {
		if a.first {
			continue
		}
		ch := a.child
		if ch.failed {
			continue
		}
		if ch.cv == nil {
			if !c.convertChild(ch, c.unlinkedTarget(ch)) {
				continue
			}
		}
		link := agentsession.NewPeerLink(cl.id, agentsession.Callee{Session: ch.sid})
		link.Timestamp = c.ts(i)
		id, err := c.add(i, at, link)
		if err != nil {
			return "", err
		}
		at = id
		c.at(agentimport.Gap, "openhands/resumed-subagent", i, "call %s resumed sub-agent %s, which an earlier call started: one sub-agent answers several calls, which a subsession, derived from one call, cannot say; it is a peer link after the output", cl.id, ch.name)
	}
	return at, nil
}

// unlinkedTarget is the session of a sub-agent no call is linked to:
// its own ID, the parent in parent_session alone.
func (c *conv) unlinkedTarget(ch *child) target {
	native := nativeID(ch.conv)
	id, derived := c.fam.sessionID(native)
	return target{id: id, native: native, derived: derived, parent: c.tgt.id}
}

// unlinkedChildren converts the sub-agents the walk linked to no call.
func (c *conv) unlinkedChildren() error {
	for _, ch := range c.kids.list {
		if ch.cv != nil || ch.failed {
			continue
		}
		why := ch.why
		if why == "" {
			why = "the call it answered has no output on any path"
		}
		if !c.convertChild(ch, c.unlinkedTarget(ch)) {
			continue
		}
		c.addf(agentimport.Lossy, "openhands/subagent-unlinked", openhands.DirSubagents+"/"+ch.name, 0, "the sub-agent is linked to no call (%s); its session names this one in parent_session alone", why)
	}
	return nil
}

// forkCopies finds the events a fork copied from its source, which
// meta.json names: the path to forked_from_event_id, else the events
// whose IDs the source's events directory holds.
func (c *conv) forkCopies() {
	m := c.c.Meta
	if m == nil || m.ForkedFromConversationID == "" {
		return
	}
	src := openhands.HexID(m.ForkedFromConversationID)
	switch {
	case m.ForkedFromEventID != "":
		for i, e := range c.c.Events {
			if e.Err == nil && e.ID == m.ForkedFromEventID {
				c.copied, c.copyKnown = i+1, true
				break
			}
		}
	case c.c.Dir != "":
		ids, err := openhands.EventIDs(filepath.Join(filepath.Dir(c.c.Dir), src))
		if err != nil {
			break
		}
		for _, e := range c.c.Events {
			if e.Err != nil || !ids[e.ID] {
				break
			}
			c.copied++
		}
		c.copyKnown = true
	}
	if !c.copyKnown {
		c.addf(agentimport.Lossy, "openhands/fork-copy-unknown", openhands.FileMeta, 0, "the conversation is a fork of %s, but which of its events are copies cannot be told; they convert as its own, and their usage, if the fork kept the source's metrics, is counted again", dashed(src))
		return
	}
	c.addf(agentimport.Lossy, "openhands/fork-copied", openhands.FileMeta, 0, "the conversation is a fork of %s, named in parent_session; its first %d events are copies of the source's, converted as this session's own", dashed(src), c.copied)
}
