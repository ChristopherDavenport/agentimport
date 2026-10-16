package convert

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
)

// allocsOf counts the allocations of converting a session's files.
func allocsOf(t *testing.T, files map[string][]byte) float64 {
	t.Helper()
	return testing.AllocsPerRun(2, func() {
		if _, err := Convert(clinesdk.ParseSession("r", files), Options{}); err != nil {
			t.Fatal(err)
		}
	})
}

// linear fails t when doubling a session more than triples what its
// conversion allocates: the work is not linear in its size.
func linear(t *testing.T, what string, make func(n int) map[string][]byte, n int) {
	t.Helper()
	a, b := allocsOf(t, make(n)), allocsOf(t, make(2*n))
	if b > 3*a {
		t.Errorf("%s: %v allocations for %d, %v for %d: not linear", what, a, n, b, 2*n)
	}
}

// TestCompactionScale matches a sidecar's kept messages against the
// history in linear time: a sidecar of identical messages whose last
// differs from the history's.
func TestCompactionScale(t *testing.T) {
	linear(t, "sidecar", func(n int) map[string][]byte {
		var hist, side []string
		for i := 0; i < n; i++ {
			hist = append(hist, userMsg("same", 1000+i))
		}
		hist[n-1] = userMsg("last", 1000+n)
		side = append(side, `{"role":"user","content":"Context summary:\n\ns","metadata":{"kind":"compaction_summary"}}`)
		for i := 0; i < n; i++ {
			side = append(side, userMsg("same", 1000+i))
		}
		comp := fmt.Sprintf(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":%d,"source_last_message_key":"ts:user:%d","messages":[%s]}`, n, 1000+n, strings.Join(side, ","))
		return map[string][]byte{"r.messages.json": msgs(hist...), "r.compaction.json": []byte(comp)}
	}, 300)
}

// TestReadsScale runs MessageBuilder's index over a session of many
// reads in time linear in it: each request does not scan the context
// again. Allocations do not tell, the scan allocating nothing, so the
// test times four times the reads against once, the best of three.
func TestReadsScale(t *testing.T) {
	session := func(n int) map[string][]byte {
		ms := []string{userMsg("go", 1000)}
		for i := 0; i < n; i++ {
			ms = append(ms, fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","id":"r%d","name":"read_files","input":{"files":[{"path":"/f%d"}]}}],"ts":%d}`, i, i, 2000+2*i))
			ms = append(ms, fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"r%d","name":"read_files","content":[{"query":"/f%d","result":"x","success":true}]}],"ts":%d}`, i, i, 2001+2*i))
		}
		return map[string][]byte{"r.messages.json": msgs(ms...)}
	}
	best := func(files map[string][]byte) time.Duration {
		var b time.Duration
		for i := 0; i < 3; i++ {
			s := clinesdk.ParseSession("r", files)
			start := time.Now()
			if _, err := Convert(s, Options{}); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); i == 0 || d < b {
				b = d
			}
		}
		return b
	}
	a, b := best(session(1000)), best(session(4000))
	// Linear is about 4 times; rescanning the context made it 8.6.
	if b > 6*a {
		t.Errorf("reads: %v for 1000, %v for 4000: not linear", a, b)
	}
}

// oracle is MessageBuilder as it runs: the index rebuilt over the whole
// context for every request, then commitOutdatedRewrites. It returns,
// per call, the reads it sends as notices.
type oracle struct {
	committed map[string]map[string]bool
}

func (o *oracle) build(ctx []*clinesdk.Message) map[string]string {
	names, inputs := map[string]string{}, map[string][]locator{}
	latest, owner := map[string]string{}, map[string]string{}
	parsed := map[*clinesdk.Block][]locator{}
	isRead := func(b *clinesdk.Block) bool {
		n, ok := names[b.ToolUseID]
		if !ok {
			n = strings.ToLower(b.Name)
		}
		return b.Err == nil && b.Type == clinesdk.BlockToolResult && !b.IsError && readTools[n]
	}
	content := func(b *clinesdk.Block) *jsjson.Value {
		v, err := jsjson.Parse(b.Content)
		if err != nil {
			return jsjson.NewString("")
		}
		return v
	}
	locs := func(b *clinesdk.Block) []locator {
		if l, ok := parsed[b]; ok && len(l) > 0 {
			return l
		}
		l := resultLocators(content(b))
		parsed[b] = l
		if len(l) > 0 {
			return l
		}
		return inputs[b.ToolUseID]
	}
	for i, m := range ctx {
		for j, b := range m.Blocks() {
			switch {
			case b.Err != nil:
			case b.Type == clinesdk.BlockFile:
				owner[b.Path] = fmt.Sprintf("file:%d:%d", i, j)
			case b.Type == clinesdk.BlockToolUse:
				names[b.ID] = strings.ToLower(b.Name)
				if in, err := jsjson.Parse(b.Input); err == nil {
					inputs[b.ID] = inputLocators(in)
				}
			case isRead(b):
				for _, l := range locs(b) {
					latest[l.key()] = b.ToolUseID
					if l.full() {
						owner[l.path] = b.ToolUseID
					}
				}
			}
		}
	}
	outdated := func(l locator, id string) bool {
		if o, ok := owner[l.path]; ok && o != id {
			return true
		}
		return latest[l.key()] != id
	}
	pending := map[string]map[string]bool{}
	seen := map[string]bool{}
	bytes := 0
	for _, m := range ctx {
		for _, b := range m.Blocks() {
			if !isRead(b) {
				continue
			}
			id := b.ToolUseID
			seen[id] = true
			fresh, valid := map[string]bool{}, map[string]bool{}
			for _, l := range locs(b) {
				if outdated(l, id) {
					valid[l.key()] = true
					if !o.committed[id][l.key()] {
						fresh[l.key()] = true
					}
				}
			}
			for k := range o.committed[id] {
				if !valid[k] {
					delete(o.committed[id], k)
				}
			}
			if len(fresh) == 0 {
				continue
			}
			if pending[id] == nil {
				pending[id] = map[string]bool{}
			}
			for k := range fresh {
				pending[id][k] = true
			}
			bytes += reclaimBytes(content(b), resultLocators(content(b)), fresh)
		}
	}
	for id := range o.committed {
		if !seen[id] {
			delete(o.committed, id)
		}
	}
	if len(pending) > 0 && bytes >= minOutdatedRewriteBytes {
		for id, keys := range pending {
			if o.committed[id] == nil {
				o.committed[id] = map[string]bool{}
			}
			for k := range keys {
				o.committed[id][k] = true
			}
		}
	}
	out := map[string]string{}
	for _, m := range ctx {
		for _, b := range m.Blocks() {
			if !isRead(b) {
				continue
			}
			var keys []string
			for _, l := range locs(b) {
				if o.committed[b.ToolUseID][l.key()] && outdated(l, b.ToolUseID) {
					keys = append(keys, l.key())
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				out[b.ToolUseID] = strings.Join(keys, "\n")
			}
		}
	}
	return out
}

// TestReadsOracle runs the index on random sessions of reads, partial
// and whole, and file blocks, and checks every request's rewrites
// against MessageBuilder's own way of computing them.
func TestReadsOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	body := strings.Repeat("x", 9000)
	committed := 0
	for run := 0; run < 40; run++ {
		var ctx []*clinesdk.Message
		r := newReads()
		o := &oracle{committed: map[string]map[string]bool{}}
		for step := 0; step < 60; step++ {
			path := fmt.Sprintf("/p%d", rng.Intn(4))
			id := fmt.Sprintf("c%d_%d", run, step)
			var raw string
			switch rng.Intn(5) {
			case 0:
				raw = fmt.Sprintf(`{"role":"user","content":[{"type":"file","path":%q,"content":"f"}]}`, path)
			case 1:
				lo := 1 + rng.Intn(3)
				raw = fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"name":"read_files","content":[{"query":"%s:%d-%d","result":%q,"success":true}]}]}`, id, path, lo, lo+5, body)
			default:
				raw = fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"name":"read_files","content":[{"query":%q,"result":%q,"success":true}]}]}`, id, path, body)
			}
			m := &clinesdk.Message{}
			if err := m.UnmarshalJSON([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			ctx = append(ctx, m)
			r.reindex(ctx)
			r.commit()
			got := map[string]string{}
			for _, rs := range r.all {
				if _, sig := r.rewrite(rs); sig != "" {
					got[rs.id] = sig
				}
			}
			want := o.build(ctx)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("run %d step %d: rewrites %v, MessageBuilder's %v", run, step, got, want)
			}
			committed += len(got)
		}
	}
	if committed == 0 {
		t.Error("no run committed a rewrite; the oracle checks nothing")
	}
}
