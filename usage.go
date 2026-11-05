package agentimport

import "github.com/ChristopherDavenport/openresponses"

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
