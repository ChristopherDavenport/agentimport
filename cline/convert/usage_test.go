package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: a request record's cacheWrites is the usage's
// openresponses-experimental.cache_write_tokens (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, fixtures(t)["classic-summarize"], Options{})
	// Seven request records, each writing 5.
	got := usagecheck.CacheWrites(res)
	if want := strings.Repeat("5|", 7); strings.Join(got, "|")+"|" != want {
		t.Errorf("cache writes %q, want seven of 5", got)
	}
}
