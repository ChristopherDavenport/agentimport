// Package usagecheck reads back what the converters write of a
// response's usage, for their tests.
package usagecheck

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentsession"
)

// CacheWrites lists, in session and entry order, the cache writes each
// response of res carries in its usage (openresponses extensions §5):
// the count, and its split by TTL as key=value pairs in key order when
// there is one. A response with no cache writes is left out.
func CacheWrites(res *agentimport.Result) []string {
	var out []string
	for _, s := range res.Sessions {
		for _, e := range s.Entries() {
			r, ok := e.(*agentsession.ResponseEntry)
			if !ok || r.Usage == nil {
				continue
			}
			n, ok := r.Usage.InputTokensDetails.CacheWriteTokens()
			if !ok {
				continue
			}
			w := fmt.Sprint(n)
			if split, ok := r.Usage.InputTokensDetails.CacheWriteTokensByTTL(); ok {
				var kv []string
				for _, k := range slices.Sorted(maps.Keys(split)) {
					kv = append(kv, fmt.Sprintf("%s=%d", k, split[k]))
				}
				w += " " + strings.Join(kv, ",")
			}
			out = append(out, w)
		}
	}
	return out
}
