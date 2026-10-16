package agentimport

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Disposition is where one native line ended up. Every line read ends
// in exactly one, so a report's tally adds up to the lines read.
type Disposition string

const (
	// LineMapped: the line became core entries.
	LineMapped Disposition = "mapped"
	// LineKept: the line became a namespaced extension, a custom entry or a
	// namespaced member, that other readers carry but do not interpret.
	LineKept Disposition = "kept"
	// LineDropped: the line is not in the output.
	LineDropped Disposition = "dropped"
	// LineMalformed: the line could not be read.
	LineMalformed Disposition = "malformed"
)

// Dispositions lists every disposition in report order.
var Dispositions = []Disposition{LineMapped, LineKept, LineDropped, LineMalformed}

// Kind classifies a finding.
type Kind string

const (
	// Lossy: mapped, with a change of meaning or precision.
	Lossy Kind = "lossy"
	// Kept: carried as a namespaced extension, present but opaque
	// to other readers.
	Kept Kind = "kept"
	// Dropped: not in the output.
	Dropped Kind = "dropped"
	// Repaired: the source broke a rule of its own and the converter
	// fixed it or skipped it.
	Repaired Kind = "repaired"
	// Gap: the agentsession format has no place for this. The converter
	// falls back to keeping it aside or dropping it, and the code is
	// listed in docs/incompatibilities.md as a question for the RFC.
	Gap Kind = "gap"
)

// Kinds lists every kind in report order.
var Kinds = []Kind{Gap, Dropped, Lossy, Kept, Repaired}

// Finding is one place the output differs from the input.
type Finding struct {
	// Code is stable and namespaced by source, such as
	// "claudecode/thinking-signature". Each source documents its codes
	// in docs/formats/<source>.md.
	Code string `json:"code"`
	Kind Kind   `json:"kind"`
	// File is the source file, as the converter was given it; empty
	// for the main file.
	File string `json:"file,omitempty"`
	// Line is 1-based, or 0 when the finding is not tied to a line.
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Report is what one conversion did to its input.
type Report struct {
	// Source names the native format, such as "claudecode".
	Source string `json:"source"`
	// Unit names what the tally counts, "lines" when empty. A source
	// whose native files are not line-oriented counts other units: ATIF,
	// one JSON document per file, counts each document's root and each
	// of its steps.
	Unit string `json:"unit,omitempty"`
	// Tally counts native lines, or units, by where they ended up.
	Tally    map[Disposition]int `json:"tally"`
	Findings []Finding           `json:"findings,omitempty"`
}

// NewReport starts a report for a source.
func NewReport(source string) *Report {
	return &Report{Source: source, Tally: map[Disposition]int{}}
}

// Line records where one native line ended up.
func (r *Report) Line(d Disposition) { r.Tally[d]++ }

// Lines is the number of native lines the report accounts for.
func (r *Report) Lines() int {
	n := 0
	for _, c := range r.Tally {
		n += c
	}
	return n
}

// Add records a finding.
func (r *Report) Add(f Finding) { r.Findings = append(r.Findings, f) }

// Addf records a finding with a formatted detail.
func (r *Report) Addf(kind Kind, code, file string, line int, format string, args ...any) {
	r.Add(Finding{Code: code, Kind: kind, File: file, Line: line, Detail: fmt.Sprintf(format, args...)})
}

// Merge adds another report's tally and findings to r, and its unit
// when r has none.
func (r *Report) Merge(o *Report) {
	if r.Unit == "" {
		r.Unit = o.Unit
	}
	for d, n := range o.Tally {
		r.Tally[d] += n
	}
	r.Findings = append(r.Findings, o.Findings...)
}

// Has reports whether any finding is of one of the kinds.
func (r *Report) Has(kinds ...Kind) bool {
	for _, f := range r.Findings {
		if slices.Contains(kinds, f.Kind) {
			return true
		}
	}
	return false
}

// Codes lists the distinct codes the report holds, sorted.
func (r *Report) Codes() []string {
	var codes []string
	for _, f := range r.Findings {
		if !slices.Contains(codes, f.Code) {
			codes = append(codes, f.Code)
		}
	}
	slices.Sort(codes)
	return codes
}

// Group is the findings of one code, counted.
type Group struct {
	Code  string `json:"code"`
	Kind  Kind   `json:"kind"`
	Count int    `json:"count"`
	// Example is the first finding's detail.
	Example string `json:"example,omitempty"`
	// At lists where the first few findings were, as file:line or line.
	At []string `json:"at,omitempty"`
}

// maxAt bounds Group.At.
const maxAt = 5

// Groups gathers the findings by code, ordered by kind as [Kinds]
// lists them and then by code.
func (r *Report) Groups() []Group {
	idx := map[string]int{}
	var gs []Group
	for _, f := range r.Findings {
		i, ok := idx[f.Code]
		if !ok {
			i = len(gs)
			idx[f.Code] = i
			gs = append(gs, Group{Code: f.Code, Kind: f.Kind, Example: f.Detail})
		}
		g := &gs[i]
		g.Count++
		if f.Line > 0 && len(g.At) < maxAt {
			at := fmt.Sprint(f.Line)
			if f.File != "" {
				at = f.File + ":" + at
			}
			g.At = append(g.At, at)
		}
	}
	slices.SortFunc(gs, func(a, b Group) int {
		if c := slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Code, b.Code)
	})
	return gs
}

// WriteText prints the tally and the grouped findings.
func (r *Report) WriteText(w io.Writer) error {
	var b strings.Builder
	unit := r.Unit
	if unit == "" {
		unit = "lines"
	}
	fmt.Fprintf(&b, "%s: %d %s:", r.Source, r.Lines(), unit)
	for _, d := range Dispositions {
		fmt.Fprintf(&b, " %s %d", d, r.Tally[d])
	}
	b.WriteString("\n")
	for _, g := range r.Groups() {
		fmt.Fprintf(&b, "  %-8s %-44s %6d", g.Kind, g.Code, g.Count)
		if g.Example != "" {
			fmt.Fprintf(&b, "  %s", g.Example)
		}
		if len(g.At) > 0 {
			fmt.Fprintf(&b, " (at %s)", strings.Join(g.At, ", "))
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// MarshalJSON writes the tally, the groups and every finding.
func (r *Report) MarshalJSON() ([]byte, error) {
	type report Report
	return json.Marshal(struct {
		*report
		Lines  int     `json:"lines"`
		Groups []Group `json:"groups,omitempty"`
	}{(*report)(r), r.Lines(), r.Groups()})
}
