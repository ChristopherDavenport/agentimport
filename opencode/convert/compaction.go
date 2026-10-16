package convert

import (
	"encoding/json"
	"slices"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// compactionPart returns a message's compaction part.
func (v *v1walk) compactionPart(j int) *opencode.PartRecord {
	for _, p := range v.msgs[j].Parts {
		if p.Part.Type == opencode.PartCompaction {
			return p
		}
	}
	return nil
}

// contextOrder is the messages OpenCode's filterCompacted gives a
// request made after message upto, in the order it gives them: walking
// back from upto, it stops at the user message of the latest compaction
// whose summary completed, or, from 1.14.19, at the message that
// compaction's tail_start_id names; from 1.14.40 it moves that tail
// after the summary.
func (v *v1walk) contextOrder(upto int) []int {
	completed := map[string]bool{}
	var result []int
	retain := ""
	for j := upto; j >= 0; j-- {
		info := v.msgs[j].Info
		result = append(result, j)
		if retain != "" {
			if info.ID == retain {
				break
			}
			continue
		}
		if info.Role == opencode.RoleUser && completed[info.ID] {
			p := v.compactionPart(j)
			if p == nil {
				continue
			}
			if !v.tail || p.Part.TailStartID == "" {
				break
			}
			retain = p.Part.TailStartID
			if info.ID == retain {
				break
			}
			continue
		}
		if info.IsSummary() && info.Finish != "" && info.Error == nil {
			completed[info.ParentID] = true
		}
	}
	slices.Reverse(result)
	if !v.reorder {
		return result
	}
	ci := -1
	for k := len(result) - 1; k >= 0; k-- {
		j := result[k]
		if v.msgs[j].Info.Role != opencode.RoleUser {
			continue
		}
		if p := v.compactionPart(j); p != nil && p.Part.Has("tail_start_id") && !p.Part.IsNull("tail_start_id") {
			ci = k
			break
		}
	}
	if ci < 0 {
		return result
	}
	comp := v.msgs[result[ci]]
	tailID := v.compactionPart(result[ci]).Part.TailStartID
	si, ti := -1, -1
	for k := ci + 1; k < len(result); k++ {
		info := v.msgs[result[k]].Info
		if info.IsSummary() && info.ParentID == comp.ID {
			si = k
			break
		}
	}
	for k, j := range result {
		if v.msgs[j].ID == tailID {
			ti = k
			break
		}
	}
	if ti < 0 || ti >= ci || si <= ci {
		return result
	}
	out := append([]int(nil), result[ci:si+1]...)
	out = append(out, result[ti:ci]...)
	return append(out, result[si+1:]...)
}

// summary writes the compaction an assistant message completes when it
// is the summary of a compaction: the context OpenCode sends from then
// on starts with the compaction's user message, which asks what was
// done so far, and the summary answering it, with the tail of messages
// the compaction kept before or after them as its release orders it.
// The compaction's summary is the first item of that context and the
// rest follows it, kept from first_kept when it stands on the path in
// that order and pinned when it does not.
func (v *v1walk) summary(s int) error {
	sc := v.sc
	m := v.msgs[s]
	info := m.Info
	c, ok := v.idx[info.ParentID]
	if !ok || c >= s || v.msgs[c].Info.Role != opencode.RoleUser || v.compactionPart(c) == nil {
		sc.addf(agentimport.Lossy, "opencode/compaction-orphan", m.Unit, "a summary message answers no compaction request before it; OpenCode cuts nothing at it")
		return nil
	}
	if info.Finish == "" || info.Error != nil {
		sc.addf(agentimport.Lossy, "opencode/compaction-incomplete", m.Unit, "the compaction's summary did not complete; OpenCode cuts nothing, and the request stays in context")
		return nil
	}
	order := v.contextOrder(s)
	var ids []string
	for _, j := range order {
		for _, id := range v.items[j] {
			if !v.omitted[id] {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		sc.addf(agentimport.Lossy, "opencode/compaction-incomplete", m.Unit, "the compaction leaves nothing OpenCode sends; no compaction is written")
		return nil
	}
	path := sc.b.S.Path(sc.at)
	pos := map[string]int{}
	var pathItems []string
	for k, e := range path {
		pos[e.Base().ID] = k
		if ie, ok := e.(*agentsession.ItemEntry); ok && !v.omitted[ie.ID] {
			pathItems = append(pathItems, ie.ID)
		}
	}
	first, ok := sc.b.S.Entry(ids[0])
	if !ok {
		return nil
	}
	summaryItem := first.(*agentsession.ItemEntry).Item
	contiguous := len(pathItems) >= len(ids) && slices.Equal(pathItems[len(pathItems)-len(ids):], ids)
	ts := ms(m.Created)
	if t, ok := info.Completed(); ok {
		ts = ms(t)
	}
	unit := v.compactionPart(c).Unit
	var (
		firstKept string
		pinned    openresponses.Items
		window    = map[string]bool{}
	)
	if contiguous && pos[ids[0]]+1 < len(path) {
		k := pos[ids[0]] + 1
		firstKept = path[k].Base().ID
		for _, e := range path[k:] {
			window[e.Base().ID] = true
		}
	} else {
		for _, id := range ids[1:] {
			e, _ := sc.b.S.Entry(id)
			ie := e.(*agentsession.ItemEntry)
			if _, ok := ie.Item.(*openresponses.ReasoningItem); ok && v.modelOf[id] != sc.model {
				continue
			}
			pinned = append(pinned, ie.Item)
		}
		var err error
		if firstKept, err = sc.keepNone(ts, unit); err != nil {
			return err
		}
	}
	settings, err := sc.checkpoint(window)
	if err != nil {
		return err
	}
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: summaryItem, Pinned: pinned, Config: settings}
	e.Timestamp = ts
	if err := sc.b.Member(e, "message_id", v.msgs[c].ID); err != nil {
		return err
	}
	if r := partRest(v.compactionPart(c)); len(r) > 0 {
		if err := sc.member(e, "compaction", r, unit); err != nil {
			return err
		}
	}
	if _, err := sc.append(e); err != nil {
		return err
	}
	how := "the messages from the compaction's request on"
	switch {
	case !contiguous:
		how = "the compaction's request and summary, then the tail of messages before the request that it kept"
	case v.msgs[order[0]].ID != v.msgs[c].ID:
		how = "the tail of messages it kept, then the compaction's request and summary"
	}
	sc.addf(agentimport.Lossy, "opencode/compaction", unit, "a compaction: OpenCode's requests from here send %s, by the rules of release %d.%d.%d; %d items are kept from first_kept and %d pinned", how, v.ver[0], v.ver[1], v.ver[2], len(ids)-1-len(pinned), len(pinned))
	sc.addf(agentimport.Gap, "agentimport/fold-head", unit, "OpenCode's compacted context starts with a message it sent verbatim, not a summary: the compaction's summary is that message, and the summary the assistant wrote follows it")
	return nil
}

// rawJSON is v as JSON, or nil.
func rawJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
