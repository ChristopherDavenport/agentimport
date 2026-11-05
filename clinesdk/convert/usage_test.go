package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: the Cline SDK's cacheWriteTokens is the usage's
// openresponses-experimental.cache_write_tokens (#37).
func TestCacheWrites(t *testing.T) {
	var got []string
	for _, res := range convertFixture(t, "s1-basic", Options{}) {
		got = append(got, usagecheck.CacheWrites(res)...)
	}
	if want := []string{"100", "150", "7600"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
