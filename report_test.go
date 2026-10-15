package agentimport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReport(t *testing.T) {
	r := NewReport("test")
	r.Line(LineMapped)
	r.Line(LineMapped)
	r.Line(LineKept)
	r.Line(LineMalformed)
	r.Addf(Lossy, "test/usage", "", 2, "cache writes folded into input tokens")
	r.Addf(Lossy, "test/usage", "", 3, "cache writes folded into input tokens")
	r.Addf(Gap, "test/provenance", "", 0, "no import provenance")
	r.Addf(Repaired, "test/nul", "sub.jsonl", 4, "NUL bytes stripped")

	if r.Lines() != 4 {
		t.Errorf("Lines = %d, want 4", r.Lines())
	}
	if !r.Has(Gap) || r.Has(Dropped) {
		t.Errorf("Has: gap %v dropped %v", r.Has(Gap), r.Has(Dropped))
	}
	gs := r.Groups()
	var order []string
	for _, g := range gs {
		order = append(order, g.Code)
	}
	if got := strings.Join(order, " "); got != "test/provenance test/usage test/nul" {
		t.Errorf("group order = %s", got)
	}
	if gs[1].Count != 2 || strings.Join(gs[1].At, ",") != "2,3" {
		t.Errorf("usage group = %+v", gs[1])
	}
	if gs[2].At[0] != "sub.jsonl:4" {
		t.Errorf("file finding at = %v", gs[2].At)
	}

	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "test: 4 lines: mapped 2 kept 1 dropped 0 malformed 1\n") {
		t.Errorf("text:\n%s", b.String())
	}

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Lines    int
		Groups   []Group
		Findings []Finding
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Lines != 4 || len(back.Groups) != 3 || len(back.Findings) != 4 {
		t.Errorf("json = %s", raw)
	}
}

// TestReportUnit checks a report names what its tally counts, and a
// merge into a report with no unit carries it.
func TestReportUnit(t *testing.T) {
	r := NewReport("doc")
	r.Unit = "roots and steps"
	r.Line(LineMapped)
	total := NewReport("doc")
	total.Merge(r)
	var b strings.Builder
	if err := total.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "doc: 1 roots and steps: mapped 1") {
		t.Errorf("text:\n%s", b.String())
	}
}
