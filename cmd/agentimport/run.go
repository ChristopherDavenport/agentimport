package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
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
func convertOne(in input, slim bool) (o outcome) {
	o.in = in
	defer func() {
		if r := recover(); r != nil {
			o.res, o.err = nil, fmt.Errorf("the %s converter panicked: %v", in.source.name, r)
		}
	}()
	o.res, o.err = in.source.convert(in.path, slim)
	return o
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
