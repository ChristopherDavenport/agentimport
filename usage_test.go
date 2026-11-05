package agentimport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// TestUsageInconsistent checks the guard on openresponses extensions
// §5: a usage whose cache reads and writes come to more than its input
// is kept as the source reported it, and one finding per builder says
// how many there are and names the first.
func TestUsageInconsistent(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r := NewReport("test")
	b := NewBuilder(agentsession.Header{ID: "11111111-2222-4333-8444-555555555555", CreatedAt: t0}, "test", r)
	b.File = "f.jsonl"
	use := func(in, cached, writes int) *openresponses.Usage {
		u := &openresponses.Usage{InputTokens: in, OutputTokens: 1, TotalTokens: in + 1}
		u.InputTokensDetails.CachedTokens = cached
		CacheWrites(&u.InputTokensDetails, int64(writes), nil)
		return u
	}
	tail := ""
	add := func(e agentsession.Entry) {
		t.Helper()
		t0 = t0.Add(time.Second)
		e.Base().Timestamp = t0
		id, err := b.Append(tail, e)
		if err != nil {
			t.Fatal(err)
		}
		tail = id
	}
	add(agentsession.NewItemEntry(openresponses.UserText("hi")))
	// Consistent: reads and writes are within the input.
	add(&agentsession.ResponseEntry{ResponseID: "resp_ok", Status: openresponses.ResponseStatusCompleted, Usage: use(140, 100, 40)})
	if len(r.Findings) != 0 {
		t.Fatalf("a consistent usage: %+v", r.Findings)
	}
	bad := use(130, 100, 40)
	add(&agentsession.ResponseEntry{ResponseID: "resp_bad", Status: openresponses.ResponseStatusCompleted, Usage: bad})
	data, _ := json.Marshal(map[string]any{"usage": use(10, 20, 0)})
	add(&agentsession.CustomEntry{NS: "test", Data: data})
	var got []Finding
	for _, f := range r.Findings {
		if f.Code == UsageInconsistentCode {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("findings %+v, want one %s", r.Findings, UsageInconsistentCode)
	}
	f := got[0]
	if f.Kind != Lossy || f.File != "f.jsonl" {
		t.Errorf("finding %+v", f)
	}
	for _, want := range []string{"2 usages", "resp_bad", "input 130", "cached 100", "cache writes 40"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q lacks %q", f.Detail, want)
		}
	}
	// The counts are kept as reported.
	if n, _ := bad.InputTokensDetails.CacheWriteTokens(); bad.InputTokens != 130 || bad.InputTokensDetails.CachedTokens != 100 || n != 40 {
		t.Errorf("usage changed: %+v", bad)
	}

	// A finished session is checked the same way, for a path that
	// appends without a builder.
	r2 := NewReport("test")
	CheckUsage(r2, "g.json", b.S)
	if len(r2.Findings) != 1 || !strings.Contains(r2.Findings[0].Detail, "2 usages") || r2.Findings[0].File != "g.json" {
		t.Errorf("CheckUsage: %+v", r2.Findings)
	}
}
