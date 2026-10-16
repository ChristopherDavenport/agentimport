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
// whose summary completed, or at the message that compaction's
// tail_start_id names, which only a release that keeps a tail writes;
// with reorder, as from 1.14.40, it moves the tail of the latest
// compaction that names one after that compaction's summary, whether
// the summary completed or not.
func (v *v1walk) contextOrder(upto int, reorder bool) []int {
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
			if p.Part.TailStartID == "" {
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
	if !reorder {
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

// summaryEnded reports a compaction summary that answers no request, or
// did not complete.
func (v *v1walk) summaryEnded(s int) {
	sc := v.sc
	m := v.msgs[s]
	info := m.Info
	c, ok := v.idx[info.ParentID]
	if !ok || c >= s || v.msgs[c].Info.Role != opencode.RoleUser || v.compactionPart(c) == nil {
		sc.addf(agentimport.Lossy, "opencode/compaction-orphan", m.Unit, "a summary message answers no compaction request before it; OpenCode cuts nothing at it")
		return
	}
	if info.Finish == "" || info.Error != nil {
		sc.addf(agentimport.Lossy, "opencode/compaction-incomplete", m.Unit, "the compaction's summary did not complete: it cuts nothing before 1.14.40, and from 1.14.40 a tail it wrote still moves after the request")
	}
}

// sync makes the context of the next request, made after message upto,
// the one OpenCode built: filterCompacted's messages in its order, each
// with the items toModelMessages sends. When the context the path holds
// differs, it is changed: messages cut from its head are left out by
// omit, which keeps every item where it stands; a context in another
// order, or holding items left out before, is a compaction whose summary
// is the first item sent and the rest kept from first_kept when it
// stands on the path in that order, pinned when it does not.
func (v *v1walk) sync(upto int) error {
	if upto < 0 {
		return nil
	}
	sc := v.sc
	at := v.msgs[upto].Created
	if upto+1 < len(v.msgs) {
		at = v.msgs[upto+1].Created
	}
	unit := v.msgs[upto].Unit
	order := v.contextOrder(upto, v.ruleAt(r11440, at, unit, "a compaction's tail moves after its summary"))
	var want []string
	for _, j := range order {
		for _, id := range v.items[j] {
			if !v.omitted[id] {
				want = append(want, id)
			}
		}
	}
	if slices.Equal(want, v.ctx) || len(want) == 0 {
		return nil
	}
	comp := v.latestCompaction(order)
	if cut, ok := subsequence(want, v.ctx); ok {
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: cut}}
		cfg.Timestamp = ms(at)
		if comp >= 0 {
			if err := sc.b.Member(cfg, "compaction", v.msgs[comp].ID); err != nil {
				return err
			}
		}
		if _, err := sc.append(cfg); err != nil {
			return err
		}
		v.ctx = want
		sc.addf(agentimport.Lossy, "opencode/compaction", unit, "a compaction: OpenCode's requests from here leave out the %d items before the messages it keeps, which omit leaves out where they stand", len(cut))
		return nil
	}
	return v.compact(want, comp, at, unit)
}

// subsequence returns the items of have that want lacks, when want is
// have with some items taken out.
func subsequence(want, have []string) ([]string, bool) {
	var cut []string
	k := 0
	for _, id := range have {
		if k < len(want) && want[k] == id {
			k++
			continue
		}
		cut = append(cut, id)
	}
	return cut, k == len(want)
}

// latestCompaction is the message of the latest compaction request
// among the messages, -1 for none.
func (v *v1walk) latestCompaction(order []int) int {
	best := -1
	for _, j := range order {
		if v.msgs[j].Info.Role == opencode.RoleUser && v.compactionPart(j) != nil && j > best {
			best = j
		}
	}
	return best
}

// compact writes a compaction whose context is want.
func (v *v1walk) compact(want []string, comp int, at int64, unit string) error {
	sc := v.sc
	path := sc.b.S.Path(sc.at)
	pos := map[string]int{}
	var pathItems []string
	for k, e := range path {
		pos[e.Base().ID] = k
		if ie, ok := e.(*agentsession.ItemEntry); ok && !v.omitted[ie.ID] {
			pathItems = append(pathItems, ie.ID)
		}
	}
	first, ok := sc.b.S.Entry(want[0])
	if !ok {
		return nil
	}
	summaryItem := first.(*agentsession.ItemEntry).Item
	ts := ms(at)
	var (
		firstKept string
		pinned    openresponses.Items
		window    = map[string]bool{}
	)
	suffix := len(pathItems) >= len(want) && slices.Equal(pathItems[len(pathItems)-len(want):], want)
	if suffix && pos[want[0]]+1 < len(path) {
		k := pos[want[0]] + 1
		firstKept = path[k].Base().ID
		for _, e := range path[k:] {
			window[e.Base().ID] = true
		}
	} else {
		for _, id := range want[1:] {
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
	// Only what no request sends stays left out in the window kept: an
	// item a cut left out may be in this context again.
	settings.Omit.Items = slices.DeleteFunc(settings.Omit.Items, func(id string) bool { return !v.omitted[id] })
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: summaryItem, Pinned: pinned, Config: settings}
	e.Timestamp = ts
	if comp >= 0 {
		if err := sc.b.Member(e, "message_id", v.msgs[comp].ID); err != nil {
			return err
		}
		if r := partRest(v.compactionPart(comp)); len(r) > 0 {
			if err := sc.member(e, "compaction", r, unit); err != nil {
				return err
			}
		}
	}
	if _, err := sc.append(e); err != nil {
		return err
	}
	v.ctx = want
	sc.addf(agentimport.Lossy, "opencode/compaction", unit, "a compaction: OpenCode's requests from here send %d items in another order than the path's, the compaction request first; %d are pinned", len(want), len(pinned))
	what := "a message it sent verbatim"
	if _, ok := summaryItem.(*openresponses.Message); !ok {
		what = "a " + summaryItem.ItemType() + " item, which is no message"
	}
	sc.addf(agentimport.Gap, "agentimport/fold-head", unit, "OpenCode's compacted context starts with %s, not a summary: the compaction's summary is that item, and the summary the assistant wrote follows it", what)
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
