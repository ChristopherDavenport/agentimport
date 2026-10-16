// Portions of this file are ported from cline/cline
// (https://github.com/cline/cline) at c8f808b5, Copyright 2026 Cline Bot
// Inc., licensed under the Apache License 2.0; they have been modified.
// See THIRD_PARTY_NOTICES.md.

package convert

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
)

// reads is MessageBuilder's index of the reads a context holds and its
// sticky rewrites of the outdated ones: a read result is outdated when
// a later read covers its file or its range, and from the request at
// which the outdated reads add up to 64 KiB every one of them is sent
// as a notice. MessageBuilder rebuilds the index for every request; a
// context only grows between compactions, and a read once outdated
// stays so, so the index here is kept and grown instead, each read
// visited when a later one outdates it, and rebuilt at a compaction.
// The rewrites are kept for as long as one Cline process runs the
// session, and a manual compaction or an OAuth retry clears them too;
// the conversion assumes neither happened.
type reads struct {
	names  map[string]string
	inputs map[string][]locator
	latest map[string]string
	owner  map[string]string
	// byKey and byPath are the reads a later read of a range or a
	// file, or a file block, may outdate.
	byKey  map[string][]*readState
	byPath map[string][]*readState
	// all are the context's reads, in order; indexed is how many of
	// its messages are indexed, files how many file blocks it holds.
	all     []*readState
	indexed int
	files   int
	// committed are the rewrites, by call, which outlive a rebuild.
	committed map[string]map[string]bool
	// pending are the reads with outdated reads not yet committed, and
	// what replacing those saves; dirty those to count again; bytes
	// the sum.
	pending map[*readState]int
	dirty   map[*readState]bool
	bytes   int
	// fresh says the index was rebuilt and every read is to be
	// checked against the rewrites.
	fresh bool
	// sent is the rewrite each call's result was last sent with.
	sent map[string]string
}

// readState is one read's result in the context.
type readState struct {
	b  *clinesdk.Block
	id string
	// locs are the reads it names (getReadLocators); content those of
	// its content alone.
	locs, content []locator
	// out are the keys of its reads that are outdated; n is its place
	// among the context's reads.
	out map[string]bool
	n   int
}

func newReads() *reads {
	r := &reads{committed: map[string]map[string]bool{}, sent: map[string]string{}}
	r.reset()
	return r
}

// reset drops the index, as a compaction's context makes MessageBuilder
// do; the rewrites stay.
func (r *reads) reset() {
	r.names, r.inputs, r.latest, r.owner = map[string]string{}, map[string][]locator{}, map[string]string{}, map[string]string{}
	r.byKey, r.byPath = map[string][]*readState{}, map[string][]*readState{}
	r.all, r.indexed, r.files = nil, 0, 0
	r.pending, r.dirty, r.bytes = map[*readState]int{}, map[*readState]bool{}, 0
	r.fresh = true
}

// name is resolveToolName.
func (r *reads) name(b *clinesdk.Block) string {
	if n, ok := r.names[b.ToolUseID]; ok {
		return n
	}
	return strings.ToLower(b.Name)
}

// isRead reports whether a block is a read's result that succeeded.
func (r *reads) isRead(b *clinesdk.Block) bool {
	return b.Err == nil && b.Type == clinesdk.BlockToolResult && !b.IsError && readTools[r.name(b)]
}

// outdate marks one of a read's reads outdated.
func (r *reads) outdate(rs *readState, key string) {
	if rs.out[key] {
		return
	}
	rs.out[key] = true
	if !r.committed[rs.id][key] {
		r.dirty[rs] = true
	}
}

// setLatest makes rs the latest read of a range: every other read of it
// is outdated.
func (r *reads) setLatest(key string, rs *readState) {
	r.latest[key] = rs.id
	keep := []*readState{rs}
	for _, o := range r.byKey[key] {
		if o == rs {
			continue
		}
		if o.id == rs.id {
			keep = append(keep, o)
			continue
		}
		r.outdate(o, key)
	}
	r.byKey[key] = keep
}

// setOwner makes owner hold a file whole: every read of it by another
// call is outdated.
func (r *reads) setOwner(path, owner string, rs *readState) {
	r.owner[path] = owner
	var keep []*readState
	if rs != nil {
		keep = append(keep, rs)
	}
	for _, o := range r.byPath[path] {
		if o == rs {
			continue
		}
		if o.id == owner {
			keep = append(keep, o)
			continue
		}
		for _, l := range o.locs {
			if l.path == path {
				r.outdate(o, l.key())
			}
		}
	}
	r.byPath[path] = keep
}

// reindex indexes the messages of ctx not yet indexed.
func (r *reads) reindex(ctx []*clinesdk.Message) {
	for i := r.indexed; i < len(ctx); i++ {
		for _, b := range ctx[i].Blocks() {
			if b.Err != nil {
				continue
			}
			switch b.Type {
			case clinesdk.BlockFile:
				r.files++
				r.setOwner(b.Path, fmt.Sprintf("file:%d", r.files), nil)
			case clinesdk.BlockToolUse:
				name := strings.ToLower(b.Name)
				r.names[b.ID] = name
				if readTools[name] {
					if in, err := jsjson.Parse(b.Input); err == nil {
						if ls := inputLocators(in); len(ls) > 0 {
							r.inputs[b.ID] = ls
						}
					}
				}
			case clinesdk.BlockToolResult:
				if r.isRead(b) {
					r.add(b)
				}
			}
		}
	}
	r.indexed = len(ctx)
}

// add indexes a read's result.
func (r *reads) add(b *clinesdk.Block) {
	rs := &readState{b: b, id: b.ToolUseID, out: map[string]bool{}, n: len(r.all)}
	if v, err := jsjson.Parse(b.Content); err == nil {
		rs.content = resultLocators(v)
	}
	rs.locs = rs.content
	if len(rs.locs) == 0 {
		rs.locs = r.inputs[b.ToolUseID]
	}
	r.all = append(r.all, rs)
	for _, l := range rs.locs {
		r.setLatest(l.key(), rs)
		if l.full() {
			r.setOwner(l.path, rs.id, rs)
			continue
		}
		if o, ok := r.owner[l.path]; ok && o != rs.id {
			r.outdate(rs, l.key())
			continue
		}
		r.byPath[l.path] = append(r.byPath[l.path], rs)
	}
}

// keysOf joins a set's keys, sorted.
func keysOf(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

// commit is commitOutdatedRewrites: once what replacing the reads newly
// outdated saves reaches 64 KiB, they are all committed. It returns the
// reads whose rewrite may have changed, in the context's order.
func (r *reads) commit() []*readState {
	changed := r.commitAll()
	sort.Slice(changed, func(i, j int) bool { return changed[i].n < changed[j].n })
	return changed
}

func (r *reads) commitAll() []*readState {
	var changed []*readState
	if r.fresh {
		// After a rebuild, MessageBuilder keeps a rewrite only for a
		// call the context still holds and a read still outdated.
		r.fresh = false
		held := map[string]map[string]bool{}
		for _, rs := range r.all {
			if held[rs.id] == nil {
				held[rs.id] = map[string]bool{}
			}
			for k := range rs.out {
				held[rs.id][k] = true
			}
			r.dirty[rs] = true
		}
		for id, keys := range r.committed {
			for k := range keys {
				if !held[id][k] {
					delete(keys, k)
				}
			}
			if len(keys) == 0 {
				delete(r.committed, id)
			}
		}
		changed = append(changed, r.all...)
	}
	for rs := range r.dirty {
		fresh := map[string]bool{}
		for k := range rs.out {
			if !r.committed[rs.id][k] {
				fresh[k] = true
			}
		}
		r.bytes -= r.pending[rs]
		delete(r.pending, rs)
		if len(fresh) > 0 {
			n := 0
			if v, err := jsjson.Parse(rs.b.Content); err == nil {
				n = reclaimBytes(v, rs.content, fresh)
			}
			r.pending[rs] = n
			r.bytes += n
		}
	}
	r.dirty = map[*readState]bool{}
	if len(r.pending) == 0 || r.bytes < minOutdatedRewriteBytes {
		return changed
	}
	for rs := range r.pending {
		if r.committed[rs.id] == nil {
			r.committed[rs.id] = map[string]bool{}
		}
		for k := range rs.out {
			r.committed[rs.id][k] = true
		}
		changed = append(changed, rs)
	}
	r.pending, r.bytes = map[*readState]int{}, 0
	return changed
}

// rewrite is the reads a result is sent without, and their keys
// joined, "" when it is sent whole.
func (r *reads) rewrite(rs *readState) ([]locator, string) {
	committed := r.committed[rs.id]
	if len(committed) == 0 {
		return nil, ""
	}
	var out []locator
	set := map[string]bool{}
	for _, l := range rs.locs {
		if committed[l.key()] && rs.out[l.key()] {
			out = append(out, l)
			set[l.key()] = true
		}
	}
	return out, keysOf(set)
}

// push adds a converted message to the context the next request sends.
func (sc *sconv) push(m *clinesdk.Message) {
	sc.ctx = append(sc.ctx, m)
	sc.bytes += msgBytes(m)
}

// msgBytes is the text a message adds to a request, about.
func msgBytes(m *clinesdk.Message) int {
	n := 0
	for _, b := range m.Blocks() {
		if b.Err != nil {
			continue
		}
		var content *jsjson.Value
		if b.Type == clinesdk.BlockToolResult {
			if v, err := jsjson.Parse(b.Content); err == nil {
				content = truncateResult(v)
			}
		}
		n += textBytes(b, content)
	}
	return n
}

// project replaces the context with a compaction's, as the request
// after it sends.
func (sc *sconv) project(msgs []*clinesdk.Message) {
	sc.ctx = append([]*clinesdk.Message(nil), msgs...)
	sc.bytes = 0
	for _, m := range sc.ctx {
		sc.bytes += msgBytes(m)
	}
	sc.rd.reset()
}

// request runs the projection for the request assistant message k
// answered: each read result sent from it on as a notice, or whole
// again, is a context edit, and a context over the budget is reported.
func (sc *sconv) request(k int, ts time.Time) error {
	r := sc.rd
	r.reindex(sc.ctx)
	for _, rs := range r.commit() {
		outdated, sig := r.rewrite(rs)
		if sig == r.sent[rs.id] {
			continue
		}
		r.sent[rs.id] = sig
		p := sc.calls[rs.id]
		if p == nil {
			continue
		}
		content, err := jsjson.Parse(rs.b.Content)
		if err != nil {
			continue
		}
		if len(outdated) > 0 {
			content = replaceOutdated(content, outdated)
		}
		out := render(truncateResult(content), false)
		data := map[string]any{"kind": "context_replace", "call_id": p.id, "output": out}
		if len(outdated) > 0 {
			data["reason"] = "outdated_read"
		} else {
			data["reason"] = "read_restored"
		}
		if err := sc.replaced(k, ts, data); err != nil {
			return err
		}
		if len(outdated) > 0 {
			sc.at(agentimport.Lossy, "clinesdk/outdated-read", k, "from this request on Cline sends the result of read %s (%s) with %d of its reads replaced by %q, a later read covering them; the rewrite assumes one Cline process ran the session with MessageBuilder's default limits, as a restart, a manual compaction or an OAuth retry batches the rewrites anew and CLINE_MESSAGE_BUILDER_MIN_OUTDATED_REWRITE_BYTES moves the batch", p.id, p.name, len(outdated), outdatedFileContent)
		} else {
			sc.at(agentimport.Lossy, "clinesdk/outdated-read", k, "from this request on Cline sends the result of read %s (%s) whole again", p.id, p.name)
		}
	}
	if sc.bytes > maxTotalTextBytes && !sc.budgeted {
		sc.budgeted = true
		sc.at(agentimport.Lossy, "clinesdk/request-budget", k, "the context's text is about %d bytes, over Cline's %d-byte budget for a request: from this request on Cline cuts the largest results and texts in the middle until it fits, which is not rebuilt", sc.bytes, maxTotalTextBytes)
	}
	return nil
}
