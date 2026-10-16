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
// as a notice. The rewrites are kept for as long as one Cline process
// runs the session; the conversion assumes one did.
type reads struct {
	names     map[string]string
	inputs    map[string][]locator
	latest    map[string]string
	owner     map[string]string
	committed map[string]map[string]bool
	parsed    map[*clinesdk.Block]*parsedResult
	// indexed is how many messages of the context are indexed.
	indexed int
	// sent is the rewrite each result was last sent with, by block;
	// reclaim what replacing a result's newly outdated reads saves, by
	// block and the reads.
	sent    map[*clinesdk.Block]string
	reclaim map[*clinesdk.Block]reclaimed
}

// reclaimed is reclaimBytes of a result, for one set of reads.
type reclaimed struct {
	keys  string
	bytes int
}

// parsedResult is a tool result's content, parsed, and the reads it
// names.
type parsedResult struct {
	content *jsjson.Value
	locs    []locator
}

func newReads() *reads {
	return &reads{names: map[string]string{}, inputs: map[string][]locator{}, latest: map[string]string{}, owner: map[string]string{},
		committed: map[string]map[string]bool{}, parsed: map[*clinesdk.Block]*parsedResult{}, sent: map[*clinesdk.Block]string{},
		reclaim: map[*clinesdk.Block]reclaimed{}}
}

// reset drops the index, as a context that is not the last one plus
// messages makes MessageBuilder do; the rewrites stay.
func (r *reads) reset() {
	r.names, r.inputs, r.latest, r.owner = map[string]string{}, map[string][]locator{}, map[string]string{}, map[string]string{}
	r.indexed = 0
}

func (r *reads) parse(b *clinesdk.Block) *parsedResult {
	if p, ok := r.parsed[b]; ok {
		return p
	}
	p := &parsedResult{}
	if v, err := jsjson.Parse(b.Content); err == nil {
		p.content = v
		p.locs = resultLocators(v)
	} else {
		p.content = jsjson.NewString("")
	}
	r.parsed[b] = p
	return p
}

// name is resolveToolName.
func (r *reads) name(b *clinesdk.Block) string {
	if n, ok := r.names[b.ToolUseID]; ok {
		return n
	}
	return strings.ToLower(b.Name)
}

// locators is getReadLocators.
func (r *reads) locators(b *clinesdk.Block) []locator {
	if p := r.parse(b); len(p.locs) > 0 {
		return p.locs
	}
	return r.inputs[b.ToolUseID]
}

// outdated is isOutdatedReadLocator.
func (r *reads) outdated(l locator, id string) bool {
	if o, ok := r.owner[l.path]; ok && o != id {
		return true
	}
	return r.latest[l.key()] != id
}

// isRead reports whether a block is a read's result that succeeded.
func (r *reads) isRead(b *clinesdk.Block) bool {
	return b.Err == nil && b.Type == clinesdk.BlockToolResult && !b.IsError && readTools[r.name(b)]
}

// reindex indexes the messages of ctx not yet indexed.
func (r *reads) reindex(ctx []*clinesdk.Message) {
	for i := r.indexed; i < len(ctx); i++ {
		for j, b := range ctx[i].Blocks() {
			if b.Err != nil {
				continue
			}
			switch b.Type {
			case clinesdk.BlockFile:
				r.owner[b.Path] = fmt.Sprintf("file:%d:%d", i, j)
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
				if !r.isRead(b) {
					continue
				}
				for _, l := range r.locators(b) {
					r.latest[l.key()] = b.ToolUseID
					if l.full() {
						r.owner[l.path] = b.ToolUseID
					}
				}
			}
		}
	}
	r.indexed = len(ctx)
}

// commit is commitOutdatedRewrites: the reads newly outdated are
// committed once what replacing them saves reaches 64 KiB.
func (r *reads) commit(ctx []*clinesdk.Message) {
	pending := map[string]map[string]bool{}
	seen := map[string]bool{}
	bytes := 0
	for _, m := range ctx {
		for _, b := range m.Blocks() {
			if !r.isRead(b) {
				continue
			}
			id := b.ToolUseID
			seen[id] = true
			committed := r.committed[id]
			fresh, valid := map[string]bool{}, map[string]bool{}
			for _, l := range r.locators(b) {
				if !r.outdated(l, id) {
					continue
				}
				valid[l.key()] = true
				if !committed[l.key()] {
					fresh[l.key()] = true
				}
			}
			if committed != nil {
				for key := range committed {
					if !valid[key] {
						delete(committed, key)
					}
				}
				if len(committed) == 0 {
					delete(r.committed, id)
				}
			}
			if len(fresh) == 0 {
				continue
			}
			if pending[id] == nil {
				pending[id] = map[string]bool{}
			}
			for key := range fresh {
				pending[id][key] = true
			}
			bytes += r.reclaimed(b, fresh)
		}
	}
	for id := range r.committed {
		if !seen[id] {
			delete(r.committed, id)
		}
	}
	if len(pending) == 0 || bytes < minOutdatedRewriteBytes {
		return
	}
	for id, keys := range pending {
		if r.committed[id] == nil {
			r.committed[id] = map[string]bool{}
		}
		for key := range keys {
			r.committed[id][key] = true
		}
	}
}

// reclaimed is reclaimBytes of a result for a set of reads, computed
// once per set.
func (r *reads) reclaimed(b *clinesdk.Block, keys map[string]bool) int {
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	sig := strings.Join(sorted, "\n")
	if c, ok := r.reclaim[b]; ok && c.keys == sig {
		return c.bytes
	}
	p := r.parse(b)
	n := reclaimBytes(p.content, p.locs, keys)
	r.reclaim[b] = reclaimed{sig, n}
	return n
}

// copySent carries what each result of the history was sent as over to
// a compaction's copy of it, whose blocks are the copy's own.
func (r *reads) copySent(from, to *clinesdk.Message) {
	fb, tb := from.Blocks(), to.Blocks()
	for i := range fb {
		if i < len(tb) {
			if sig, ok := r.sent[fb[i]]; ok {
				r.sent[tb[i]] = sig
			}
		}
	}
}

// rewrite is the outdated reads a result is sent without, and their
// keys joined, "" when it is sent whole.
func (r *reads) rewrite(b *clinesdk.Block) ([]locator, string) {
	committed := r.committed[b.ToolUseID]
	if len(committed) == 0 {
		return nil, ""
	}
	var out []locator
	var keys []string
	for _, l := range r.locators(b) {
		if committed[l.key()] && r.outdated(l, b.ToolUseID) {
			out = append(out, l)
			keys = append(keys, l.key())
		}
	}
	sort.Strings(keys)
	return out, strings.Join(keys, "\n")
}

// push adds a converted message to the context the next request sends.
func (sc *sconv) push(m *clinesdk.Message) {
	sc.ctx = append(sc.ctx, m)
	sc.bytes += sc.msgBytes(m)
}

// msgBytes is the text a message adds to a request, about.
func (sc *sconv) msgBytes(m *clinesdk.Message) int {
	n := 0
	for _, b := range m.Blocks() {
		if b.Err != nil {
			continue
		}
		var content *jsjson.Value
		if b.Type == clinesdk.BlockToolResult {
			content = truncateResult(sc.rd.parse(b).content)
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
		sc.bytes += sc.msgBytes(m)
	}
	sc.rd.reset()
}

// request runs the projection for the request assistant message k
// answered: each read result sent from it on as a notice is a context
// edit, and a context over the budget is reported.
func (sc *sconv) request(k int, ts time.Time) error {
	r := sc.rd
	r.reindex(sc.ctx)
	r.commit(sc.ctx)
	for _, m := range sc.ctx {
		for _, b := range m.Blocks() {
			if !r.isRead(b) {
				continue
			}
			outdated, sig := r.rewrite(b)
			if sig == r.sent[b] {
				continue
			}
			r.sent[b] = sig
			p := sc.calls[b.ToolUseID]
			if p == nil {
				continue
			}
			content := r.parse(b).content
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
				sc.at(agentimport.Lossy, "clinesdk/outdated-read", k, "from this request on Cline sends the result of read %s (%s) with %d of its reads replaced by %q, a later read covering them; the rewrite assumes one Cline process ran the session, as a restart batches the rewrites anew", p.id, p.name, len(outdated), outdatedFileContent)
			} else {
				sc.at(agentimport.Lossy, "clinesdk/outdated-read", k, "from this request on Cline sends the result of read %s (%s) whole again", p.id, p.name)
			}
		}
	}
	if sc.bytes > maxTotalTextBytes && !sc.budgeted {
		sc.budgeted = true
		sc.at(agentimport.Lossy, "clinesdk/request-budget", k, "the context's text is about %d bytes, over Cline's %d-byte budget for a request: from this request on Cline cuts the largest results and texts in the middle until it fits, which is not rebuilt", sc.bytes, maxTotalTextBytes)
	}
	return nil
}
