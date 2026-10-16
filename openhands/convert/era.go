// Portions of this file are ported from OpenHands/software-agent-sdk
// (https://github.com/OpenHands/software-agent-sdk) at d5995133 and
// earlier tags, Copyright (c) 2025 All Hands AI and Copyright (c) 2026
// OpenHands contributors, licensed under the MIT License; they have
// been modified. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"encoding/json"
	"sort"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
)

// era is how the SDK that wrote a conversation built the model's view.
// No file records a version, so the era is told by the members the files
// hold, and reported.
type era int

const (
	// eraLast is SDK 1.0 to 1.9: every condensation's forgotten events
	// left out at once, the last condensation's summary alone inserted,
	// then unmatched calls and answers filtered, once, at every request.
	eraLast era = iota
	// eraSeq is 1.10 to 1.11.4: condensations applied in order, earlier
	// summaries kept, then batch atomicity and call matching at every
	// request.
	eraSeq
	// eraProps is 1.11.5 to 1.16: the view's properties (batch
	// atomicity, call matching, tool-loop atomicity) enforced at every
	// request.
	eraProps
	// eraLive is 1.17 and later: the view is built incrementally and the
	// properties, observation uniqueness from 1.21 among them, are
	// enforced only when the conversation is reloaded.
	eraLive
)

func (e era) String() string {
	switch e {
	case eraLast:
		return "1.0 to 1.9"
	case eraSeq:
		return "1.10 to 1.11.4"
	case eraProps:
		return "1.11.5 to 1.16"
	}
	return "1.17 and later"
}

// pickEra tells the era from the members the files hold: events with a
// parent_id (1.31 and later), base_state members later releases added
// (invoked_skills from 1.18, tags from 1.16, hook_config from 1.13,
// last_user_message_id from 1.11.5), and the Message members 1.0 to 1.9
// wrote. base_state names the release that last saved it, so a
// conversation resumed by a newer one reads as newer.
func (c *conv) pickEra() {
	if c.tree {
		c.era, c.eraWhy = eraLive, "its events carry parent_id, which SDK 1.31 and later write"
		return
	}
	var state map[string]json.RawMessage
	if c.c.State != nil {
		_ = json.Unmarshal(c.c.StateRaw, &state)
	}
	has := func(name string) bool { _, ok := state[name]; return ok }
	oldMessage := false
	for i, e := range c.c.Events {
		if e.Err != nil || c.info[i].kind != openhands.KindMessage {
			continue
		}
		if m, err := e.Message(); err == nil && m.LLMMessage != nil {
			if _, ok := m.LLMMessage.Extra["cache_enabled"]; ok {
				oldMessage = true
				break
			}
		}
	}
	switch {
	case has("invoked_skills"):
		c.era, c.eraWhy = eraLive, "base_state has invoked_skills, which SDK 1.18 and later write"
	case oldMessage:
		c.era, c.eraWhy = eraLast, "a message carries cache_enabled, which SDK 1.0 to 1.9 wrote"
	case has("tags"):
		c.era, c.eraWhy = eraProps, "base_state has tags and not invoked_skills, as SDK 1.16 and 1.17 write; 1.16 is taken"
	case has("hook_config") || has("last_user_message_id"):
		c.era, c.eraWhy = eraProps, "base_state has hook_config or last_user_message_id and not tags, as SDK 1.11.5 to 1.15 write"
	case state != nil:
		c.era, c.eraWhy = eraSeq, "base_state has none of the members SDK 1.11.5 and later write"
	default:
		c.era, c.eraWhy = eraLive, "nothing tells the release, and the OpenHands CLI's (1.28.1) is taken"
	}
	c.addf(agentimport.Lossy, "openhands/version-guess", "", 0, "the view is built as SDK %s built it: %s; base_state names the release that last saved it, and events an earlier release wrote are read the same way", c.era, c.eraWhy)
}

// viewNow is the view OpenHands sent at a request made at event i, and
// why each event it left out left it: enforced at every request before
// 1.17, and from 1.17 the view as built since the last reload, where a
// reload the files do not show is reported as a guess.
func (c *conv) viewNow(i int) ([]vEvent, map[int]string) {
	switch c.era {
	case eraLast:
		return c.enforceWith(false, false, false, true)
	case eraSeq:
		return c.enforceSeq()
	case eraProps:
		return c.enforceWith(false, true, true, false)
	}
	why := c.reloadWhy
	c.reloadWhy = nil
	if trimmed, would := c.enforceWith(true, true, true, false); len(trimmed) != len(c.vs.view) {
		gone := make([]int, 0, len(would))
		for j := range would {
			gone = append(gone, j)
		}
		sort.Ints(gone)
		for _, j := range gone {
			if c.items[j] == nil || c.guessed[j] {
				continue
			}
			c.guessed[j] = true
			c.at(agentimport.Lossy, "openhands/reload-guess", j, "from 1.17 OpenHands checks its view only when it reloads the conversation; a reload before event %d would have left this event out (%s), and a live run sent it, as the session does", i+1, would[j])
		}
	}
	return c.vs.view, why
}

// reload enforces the view's properties as OpenHands does when it
// reloads a conversation from 1.17, and keeps the result as the view
// until the next reload. what names the reload in findings.
func (c *conv) reload(what string) {
	if c.era != eraLive {
		return
	}
	view, why := c.enforceWith(true, true, true, false)
	if len(view) == len(c.vs.view) {
		return
	}
	c.vs.view = view
	if c.reloadWhy == nil {
		c.reloadWhy = map[int]string{}
	}
	for j, w := range why {
		c.reloadWhy[j] = w + ", at " + what
	}
}

// enforceWith enforces the properties named: observation uniqueness,
// batch atomicity, call matching and tool-loop atomicity, each in turn,
// the turn begun again whenever one removes events. once runs call
// matching a single time, as SDK 1.0 to 1.9 filtered unmatched calls.
func (c *conv) enforceWith(unique, batches, loops, once bool) ([]vEvent, map[int]string) {
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
	if once {
		drop(c.callMatching(view), whyMatching)
		return view, why
	}
	for {
		if gone := c.uniqueness(view); unique && len(gone) > 0 {
			drop(gone, whyUnique)
			continue
		}
		if gone := c.batchAtomicity(view); batches && len(gone) > 0 {
			drop(gone, whyBatch)
			continue
		}
		if gone := c.callMatching(view); len(gone) > 0 {
			drop(gone, whyMatching)
			continue
		}
		if gone := c.loopAtomicity(view); loops && len(gone) > 0 {
			drop(gone, whyLoop)
			continue
		}
		return view, why
	}
}

// Why an event left the view.
const (
	whyUnique   = "a second answer to a call already answered"
	whyBatch    = "an action of a response another of whose actions left the context"
	whyMatching = "a call with no answer in the context, or an answer with no call"
	whyLoop     = "part of a tool loop another part of which left the context"
)

// enforceSeq is the view of SDK 1.10 to 1.11.4: batch atomicity, call
// matching, batch atomicity again, then the answers to every action on
// the path the view does not hold left out, each a single time.
func (c *conv) enforceSeq() ([]vEvent, map[int]string) {
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
	drop(c.batchAtomicity(view), whyBatch)
	drop(c.callMatching(view), whyMatching)
	drop(c.batchAtomicity(view), whyBatch)
	in := map[int]bool{}
	for _, v := range view {
		in[v.idx] = true
	}
	calls := map[string]bool{}
	for _, set := range c.vs.batches {
		for j := range set {
			if !in[j] {
				calls[c.info[j].toolCallID] = true
			}
		}
	}
	gone := map[int]bool{}
	for _, v := range view {
		if v.idx >= 0 && openhands.IsObservationLike(c.info[v.idx].kind) && calls[c.info[v.idx].toolCallID] {
			gone[v.idx] = true
		}
	}
	drop(gone, whyMatching)
	return view, why
}

// probableResume reports an answer whose call waited across another
// context event, which suggests the conversation was reloaded before
// the answer: from 1.17 a reload drops the unanswered call, and
// OpenHands then sends the answer with no call.
func (c *conv) probableResume(i int, cl *call) {
	if c.era != eraLive {
		return
	}
	resp := c.info[cl.event].respID
	for j := c.parent[i]; j > cl.event && j >= 0; j = c.parent[j] {
		in := c.info[j]
		if c.c.Events[j].Err != nil || !openhands.InContext(in.kind) {
			continue
		}
		if in.kind == openhands.KindAction && in.respID == resp || openhands.IsObservationLike(in.kind) {
			continue
		}
		c.at(agentimport.Lossy, "openhands/reload-guess", i, "call %s waited across event %d before this answer, which suggests the conversation was reloaded; from 1.17 a reload leaves out the unanswered call, and OpenHands then sends the answer with no call, which a session cannot hold: the session keeps the pair", cl.id, j+1)
		return
	}
}

// longText reports a text OpenHands before 1.11.1 cut when it sent it:
// those releases cut every text over 50,000 characters, a person's and
// the system prompt among them, where later ones cut tool results
// alone.
func (c *conv) longText(i int, s string) {
	if c.era > eraSeq || len([]rune(s)) <= textContentLimit {
		return
	}
	c.at(agentimport.Lossy, "openhands/version-guess", i, "OpenHands before 1.11.1 cut every text over %d characters when it sent it, this one among them, and later releases cut tool results alone; the text is kept whole", textContentLimit)
}
