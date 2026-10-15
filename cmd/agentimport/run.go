package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	codexconvert "github.com/ChristopherDavenport/agentimport/codex/convert"
)

// common holds the flags both commands take.
type common struct {
	from   string
	strict bool
	slim   bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.from, "from", "auto", "the source format: "+fromValues())
	fs.BoolVar(&c.strict, "strict", false, "exit 1 when a report holds a dropped or gap finding")
	fs.BoolVar(&c.slim, "slim", false, "drop what the model never saw rather than keep it in namespaced members")
}

func (c *common) check() error {
	if c.from == "auto" {
		return nil
	}
	if _, ok := sourceNamed(c.from); !ok {
		return fmt.Errorf("-from %q: want %s", c.from, fromValues())
	}
	return nil
}

// outcome is one input converted, or the reason it was not.
type outcome struct {
	in  input
	res *agentimport.Result
	err error
}

// strictFails reports whether -strict refuses the outcome's report.
func (o outcome) strictFails() bool {
	return o.res != nil && o.res.Report.Has(agentimport.Dropped, agentimport.Gap)
}

// convertOne converts one input. A converter is held never to panic and
// is fuzzed for it; a panic that gets through anyway fails this input
// rather than a survey of hundreds.
func (r *runner) convertOne(in input) (o outcome) {
	o.in = in
	defer func() {
		if p := recover(); p != nil {
			o.res, o.err = nil, fmt.Errorf("the %s converter panicked: %v", in.source.name, p)
		}
	}()
	o.res, o.err = in.source.convert(in.path, r)
	return o
}

// runner converts a command's inputs. It indexes each Codex sessions
// tree once, however many of its rollouts are inputs, and converts a
// Codex sub-agent's rollout with its ancestor when both are inputs.
type runner struct {
	slim    bool
	skipped *[]skipped
	indexes map[indexKey]*codexIndex
}

type indexKey struct {
	root string
	tree bool
}

// codexIndex is one tree's index, or why it could not be built.
type codexIndex struct {
	x   *codex.Index
	err error
}

func newRunner(slim bool, skip *[]skipped) *runner {
	return &runner{slim: slim, skipped: skip, indexes: map[indexKey]*codexIndex{}}
}

// index returns the index of the tree a Codex rollout's relatives are
// in, built once per tree. What it left out is listed with the skipped
// inputs, once.
func (r *runner) index(path string) (*codex.Index, error) {
	root, tree, err := codex.SessionsRoot(path)
	if err != nil {
		return nil, err
	}
	k := indexKey{root, tree}
	ci, ok := r.indexes[k]
	if !ok {
		ci = &codexIndex{}
		if tree {
			ci.x, ci.err = codex.IndexDir(root)
		} else {
			ci.x, ci.err = codex.IndexFlat(root)
		}
		if ci.x != nil {
			for _, sk := range ci.x.Skipped {
				*r.skipped = append(*r.skipped, skipped{sk.Path, "left out of the Codex index: " + sk.Err.Error()})
			}
		}
		r.indexes[k] = ci
	}
	return ci.x, ci.err
}

// convertCodex converts a Codex rollout with the sub-agents its tree's
// index holds; with no index, alone, and says so.
func (r *runner) convertCodex(path string) (*agentimport.Result, error) {
	x, err := r.index(path)
	if err != nil {
		res, cerr := codexconvert.ConvertFile(path, codexconvert.Options{Slim: r.slim, Alone: true})
		if cerr != nil {
			return nil, cerr
		}
		res.Report.Addf(agentimport.Lossy, "codex/index-failed", "", 0,
			"the tree the rollout is in could not be indexed (%v); it is converted without its sub-agents", err)
		return res, nil
	}
	return codexconvert.ConvertFile(path, codexconvert.Options{Slim: r.slim, Index: x})
}

// planned is an input whose conversion may wait: a Codex sub-agent's
// rollout with an ancestor among the inputs.
type planned struct {
	in     input
	thread string
	depth  int
}

// plan splits the inputs into those converted first and the Codex
// sub-agents that wait for their ancestors: a rollout whose thread has
// an ancestor, at any depth through the index, among the inputs'
// threads. Ancestors stop at the thread itself and at a cycle, so a
// rollout is never its own ancestor's. A waiting rollout converts on its
// own after all, if no conversion before it produced its session.
func (r *runner) plan(ins []input) (first []input, wait []planned) {
	threads := map[string]bool{}
	ids := make([]string, len(ins))
	for i, in := range ins {
		if in.source.name != codexconvert.Source {
			continue
		}
		if m, err := codex.ReadMeta(in.path); err == nil && m != nil && m.ID != "" {
			ids[i] = m.ID
			threads[m.ID] = true
		}
	}
	for i, in := range ins {
		if ids[i] == "" {
			first = append(first, in)
			continue
		}
		x, err := r.index(in.path)
		if err != nil {
			first = append(first, in)
			continue
		}
		// depth counts the ancestors among the inputs, so a rollout
		// waits behind every waiting ancestor of its own.
		depth := 0
		for _, a := range x.Ancestors(ids[i]) {
			if threads[a] {
				depth++
			}
		}
		if depth == 0 {
			first = append(first, in)
			continue
		}
		wait = append(wait, planned{in, ids[i], depth})
	}
	sort.SliceStable(wait, func(i, j int) bool { return wait[i].depth < wait[j].depth })
	return first, wait
}

// each converts the inputs, ancestors before the Codex sub-agents that
// wait for them, and hands each outcome to visit. A waiting rollout
// whose session a conversion already produced is listed as skipped;
// one no conversion produced, its ancestor having failed or not found
// it, converts on its own, so no input is lost.
func (r *runner) each(ins []input, visit func(outcome)) {
	first, wait := r.plan(ins)
	produced := map[string]bool{}
	run := func(in input) {
		o := r.convertOne(in)
		if o.res != nil {
			for _, s := range o.res.Sessions {
				produced[s.ID()] = true
			}
		}
		visit(o)
	}
	for _, in := range first {
		run(in)
	}
	for _, w := range wait {
		if produced[w.thread] {
			*r.skipped = append(*r.skipped, skipped{w.in.path, "a Codex sub-agent's rollout, converted with its parent"})
			continue
		}
		run(w.in)
	}
}

// summary is a report without its findings: the tally and the findings
// grouped by code. It is what a report over many sessions prints.
type summary struct {
	Source string                          `json:"source"`
	Lines  int                             `json:"lines"`
	Tally  map[agentimport.Disposition]int `json:"tally"`
	Groups []agentimport.Group             `json:"groups,omitempty"`
}

func summarise(r *agentimport.Report) summary {
	return summary{Source: r.Source, Lines: r.Lines(), Tally: r.Tally, Groups: r.Groups()}
}

// totals adds up the outcomes of many inputs, per source.
type totals struct {
	reports map[string]*agentimport.Report
	files   map[string]int
	failed  []outcome
	strict  int
}

func newTotals() *totals {
	return &totals{reports: map[string]*agentimport.Report{}, files: map[string]int{}}
}

func (t *totals) add(o outcome) {
	if o.err != nil {
		t.failed = append(t.failed, o)
		return
	}
	name := o.in.source.name
	t.files[name]++
	r, ok := t.reports[name]
	if !ok {
		r = agentimport.NewReport(name)
		t.reports[name] = r
	}
	r.Merge(o.res.Report)
	if o.strictFails() {
		t.strict++
	}
}

// names lists the sources that converted anything, in source order.
func (t *totals) names() []string {
	var out []string
	for _, s := range sources {
		if t.files[s.name] > 0 {
			out = append(out, s.name)
		}
	}
	return out
}

// writeText prints the counts, each source's added-up report and the
// failures.
func (t *totals) writeText(w io.Writer, skip []skipped) error {
	var parts []string
	for _, name := range t.names() {
		parts = append(parts, fmt.Sprintf("%s %d", name, t.files[name]))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	if _, err := fmt.Fprintf(w, "converted: %s; failed %d; skipped %d\n", strings.Join(parts, ", "), len(t.failed), len(skip)); err != nil {
		return err
	}
	for _, name := range t.names() {
		if err := t.reports[name].WriteText(w); err != nil {
			return err
		}
	}
	for _, o := range t.failed {
		if _, err := fmt.Fprintf(w, "failed: %s: %v\n", displayPath(o.in.path), o.err); err != nil {
			return err
		}
	}
	return nil
}

func (t *totals) aggregate() map[string]summary {
	out := map[string]summary{}
	for name, r := range t.reports {
		out[name] = summarise(r)
	}
	return out
}

// exit is the status for the totals: 1 on a failure, or with -strict on
// a dropped or gap finding.
func (t *totals) exit(strict bool) int {
	if len(t.failed) > 0 || strict && t.strict > 0 {
		return 1
	}
	return 0
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// errString is an error's text, or empty.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// sortedSkips orders skipped files by path.
func sortedSkips(s []skipped) []skipped {
	slices.SortFunc(s, func(a, b skipped) int { return strings.Compare(a.Path, b.Path) })
	return s
}
