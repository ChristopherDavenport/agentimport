package agentimport

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// CacheWrites sets the cache writes a source reports on d, as
// openresponses' extensions §5 asks of a writer whose provider reports
// them and as its Claude adapter does: nothing when total is not
// positive; the split by TTL, its zero entries left out, when the source
// gives one whose values sum to total; the count alone otherwise. The
// tokens are counted in the usage's input already, as each converter
// folds them in.
func CacheWrites(d *openresponses.InputTokensDetails, total int64, byTTL map[string]int64) {
	if total <= 0 {
		return
	}
	split := map[string]int{}
	var sum int64
	for k, n := range byTTL {
		if n < 0 {
			split = nil
			break
		}
		sum += n
		if n > 0 {
			split[k] = int(n)
		}
	}
	if split != nil && byTTL != nil && sum == total {
		d.SetCacheWriteTokensByTTL(split)
		return
	}
	d.SetCacheWriteTokens(int(total))
}

// UsageInconsistentCode is the code of the finding that a usage counts
// more input read from and written to the prompt cache than input, which
// openresponses' extensions §5 rules out: the writes and the cache reads
// are disjoint parts of the input, so a reader pricing the rest, input
// less both, would get a negative count. The counts are kept as the
// source reported them; agentimport computes no uncached input from
// them.
const UsageInconsistentCode = "agentimport/usage-inconsistent"

// usageTally gathers the inconsistent usages of one session into one
// finding, the first named, the number of them kept up to date.
type usageTally struct {
	at, n int
	first string
}

// note checks the usage e carries, if any, and reports it on r when it
// is inconsistent.
func (t *usageTally) note(r *Report, file string, e agentsession.Entry) {
	u, what := entryUsage(e)
	if u == nil {
		return
	}
	writes, ok := u.InputTokensDetails.CacheWriteTokens()
	if !ok {
		writes = 0
		if byTTL, ok := u.InputTokensDetails.CacheWriteTokensByTTL(); ok {
			for _, n := range byTTL {
				writes += n
			}
		}
	}
	cached := u.InputTokensDetails.CachedTokens
	if int64(cached)+int64(writes) <= int64(u.InputTokens) {
		return
	}
	t.n++
	if t.n == 1 {
		t.first = fmt.Sprintf("the first, of %s, has input %d, cached %d and cache writes %d", what, u.InputTokens, cached, writes)
		r.Add(Finding{Code: UsageInconsistentCode, Kind: Lossy, File: file})
		t.at = len(r.Findings) - 1
	}
	noun := "usage counts"
	if t.n > 1 {
		noun = "usages count"
	}
	r.Findings[t.at].Detail = fmt.Sprintf("%d %s more input read from and written to the cache than input (%s); "+
		"kept as reported, though input less cache reads and writes, the input a reader prices at the full rate, is negative", t.n, noun, t.first)
}

// entryUsage returns the usage an entry records and names the entry: a
// response's, a compaction's or branch summary's, or a custom entry's
// data's top-level usage, as RFC 0001 reads a paid call with no
// response.
func entryUsage(e agentsession.Entry) (*openresponses.Usage, string) {
	switch v := e.(type) {
	case *agentsession.ResponseEntry:
		return v.Usage, "response " + v.ResponseID
	case *agentsession.CompactionEntry:
		return v.Usage, "a compaction"
	case *agentsession.BranchSummaryEntry:
		return v.Usage, "a branch summary"
	case *agentsession.CustomEntry:
		if !bytes.Contains(v.Data, []byte(`"usage"`)) {
			return nil, ""
		}
		var paid struct {
			Usage *openresponses.Usage `json:"usage"`
		}
		if json.Unmarshal(v.Data, &paid) != nil {
			return nil, ""
		}
		return paid.Usage, "a custom entry " + v.NS
	}
	return nil, ""
}

// CheckUsage reports, in one finding naming file, the usages of s whose
// cache reads and writes come to more than their input
// ([UsageInconsistentCode]). A [Builder] checks each entry it appends;
// this is for a session put together without one.
func CheckUsage(r *Report, file string, s *agentsession.Session) {
	var t usageTally
	for _, e := range s.Entries() {
		t.note(r, file, e)
	}
}
